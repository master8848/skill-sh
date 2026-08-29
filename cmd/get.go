package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/api"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/git"
	"skill.sh/mskill/internal/link"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/security"
)

var getCmd = &cobra.Command{
	Use:     "get [skill]",
	Aliases: []string{"add", "a"},
	Short:   "Fetch, store and link a skill",
	Long:    "Resolve a skill reference, cache it with sparse git, and link into agent directories.",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		isShow, _ := cmd.Flags().GetBool("show")
		files, _ := cmd.Flags().GetStringArray("file")
		refFlag, _ := cmd.Flags().GetString("ref")
		force, _ := cmd.Flags().GetBool("force")
		global, _ := cmd.Flags().GetBool("global")
		project, _ := cmd.Flags().GetBool("project")
		agentFilter, _ := cmd.Flags().GetString("agent")
		copyFlag, _ := cmd.Flags().GetBool("copy")
		linkMode, _ := cmd.Flags().GetString("link-mode")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		if yesFlag {
			viper.Set("yes", true)
		}
		// skill filter may be String or StringArray; try both
		var skillFilters []string
		if vals, err := cmd.Flags().GetStringArray("skill"); err == nil && len(vals) > 0 {
			for _, v := range vals {
				if strings.TrimSpace(v) != "" {
					skillFilters = append(skillFilters, strings.TrimSpace(v))
				}
			}
		}
		if len(skillFilters) == 0 {
			if v, err := cmd.Flags().GetString("skill"); err == nil && strings.TrimSpace(v) != "" {
				// comma separated or single
				for _, part := range strings.Split(v, ",") {
					if s := strings.TrimSpace(part); s != "" && s != "skill" {
						skillFilters = append(skillFilters, s)
					}
				}
			}
		}

		// Determine raw inputs to process
		var raws []string
		if len(args) > 0 {
			raws = args
		}
		// If skillFilters provided and raws has single repo, expand to repo/skill combos
		if len(skillFilters) > 0 && len(raws) == 1 {
			repo := strings.TrimSpace(raws[0])
			// if skill filter is "*" treat as all: for simplicity keep as repo (no filter) and let cache handle full clone
			if len(skillFilters) == 1 && skillFilters[0] == "*" {
				// keep repo as is, no extra expansion
			} else {
				var expanded []string
				for _, sf := range skillFilters {
					if sf == "*" {
						expanded = append(expanded, repo)
					} else {
						expanded = append(expanded, strings.TrimSuffix(repo, "/")+"/"+strings.TrimPrefix(sf, "/"))
					}
				}
				raws = expanded
			}
		}
		if len(raws) == 0 {
			return fmt.Errorf("skill reference required: mskill get owner/repo/skill or owner/repo --skill <name>")
		}

		verbose := viper.GetBool("verbose")
		for _, rawOrig := range raws {
			raw := normalizeColonRefGet(rawOrig)
			r, err := resolve.ParseSkillRef(raw)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", raw, err)
			}
			if verbose {
				fmt.Fprintf(os.Stderr, "resolve: %+v\n", r)
			}
			// local type: just link directly without cache? For now treat local as cacheSkillPath = CloneURL
			if r.IsLocal {
				localPath := r.CloneURL
				// If localPath doesn't exist, try discovery under parent (for --skill nested case like /repo --skill docs where docs is at /repo/skills/docs)
				if _, err := os.Stat(localPath); err != nil {
					if len(skillFilters) > 0 {
						base := filepath.Dir(strings.TrimSuffix(localPath, "/"))
						if base == "." || base == "" {
							base = localPath
						}
						// Try to find skill under base
						if discovered := discoverSkillInCache(base, filepath.Base(localPath)); discovered != "" {
							localPath = discovered
						} else if discovered := discoverSkillInCache(base, r.Slug); discovered != "" {
							localPath = discovered
						} else {
							return fmt.Errorf("local skill %q not found (checked %q)", r.Slug, localPath)
						}
					} else {
						return fmt.Errorf("local path %q does not exist: %w", localPath, err)
					}
				} else {
					// localPath exists but may be repo root without SKILL.md and skillFilters provided? check SKILL.md presence
					if _, err := os.Stat(filepath.Join(localPath, "SKILL.md")); err != nil && len(skillFilters) > 0 {
						if discovered := discoverSkillInCache(localPath, r.Slug); discovered != "" {
							// localPath is repo root, discovered is nested skill
							// Keep localPath as discovered only if user used repo --skill pattern where localPath is repo root with skill subdir not existing as direct child
							// But current localPath is already /repo/skill, so this branch not needed
						}
					}
				}
				if isShow {
					// for local, list files or cat SKILL.md
					targetFiles := files
					if len(targetFiles) == 0 {
						targetFiles = []string{"SKILL.md"}
					}
					for _, f := range targetFiles {
						sanitized, err := resolve.SanitizeSubpath(f)
						if err != nil {
							return err
						}
						full := filepath.Join(localPath, sanitized)
						data, err := os.ReadFile(full)
						if err != nil {
							return fmt.Errorf("read %s: %w", f, err)
						}
						cmdPrint(cmd, string(data))
						if len(targetFiles) > 1 {
							cmdPrint(cmd, "\n---\n")
						}
					}
					continue
				}
				// link local directly
				skillName := r.Slug
				if skillName == "" {
					skillName = filepath.Base(strings.TrimSuffix(localPath, "/"))
				}
				if err := link.Install(localPath, skillName, link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}); err != nil {
					return err
				}
				cmdPrint(cmd, fmt.Sprintf("linked local %s -> %s\n", localPath, skillName))
				continue
			}

			// Determine effective ref
			effectiveRef := refFlag
			if effectiveRef == "" {
				effectiveRef = r.Ref
			}

			// Build skillPaths for sparse checkout
			var skillPaths []string
			if r.SkillPath != "" {
				skillPaths = []string{r.SkillPath}
			} else if r.Slug != "" {
				skillPaths = []string{r.Slug}
			}

			// Audit before cache? Spec: Resolve → Cache → Link with audit gate. Audit needs to happen before link, but caching should happen regardless? We'll audit before linking, but also before show.
			// For determinism, fetch audit now (3s timeout) to gate
			slugForAudit := r.Slug
			if slugForAudit == "" && r.SkillPath != "" {
				slugForAudit = filepath.Base(r.SkillPath)
			}
			if slugForAudit == "" {
				slugForAudit = r.Repo
			}
			sourceForAudit := ""
			if r.Owner != "" && r.Repo != "" {
				sourceForAudit = r.Owner + "/" + r.Repo
			}
			if sourceForAudit != "" && slugForAudit != "" {
				verdicts, _ := api.Audit(ctx, sourceForAudit, []string{slugForAudit})
				if v, ok := verdicts[slugForAudit]; ok {
					if !v.Safe || v.Unknown {
						// fail-closed unless trust enabled
						if !security.IsTrustEnabled(paths) {
							if err := security.RequirePassword(slugForAudit, v.Reason, paths); err != nil {
								return err
							}
						}
					}
				}
			}

			cachePath, meta, err := cache.Ensure(ctx, paths, r, effectiveRef, skillPaths, force)
			if err != nil {
				return fmt.Errorf("cache %q: %w", raw, err)
			}
			if verbose {
				fmt.Fprintf(os.Stderr, "cache: %s commit %s\n", cachePath, meta.CommitSHA)
			}

			if isShow {
				// Cache-view: cat files without linking
				targetFiles := files
				if len(targetFiles) == 0 {
					targetFiles = []string{"SKILL.md"}
				}
				// skill dir inside cache
				baseSkillDir := cachePath
				if r.SkillPath != "" {
					baseSkillDir = filepath.Join(cachePath, r.SkillPath)
				}
				for _, f := range targetFiles {
					sanitized, err := resolve.SanitizeSubpath(f)
					if err != nil {
						return fmt.Errorf("invalid --file %q: %w", f, err)
					}
					// Prevent escaping skill dir
					full := filepath.Join(baseSkillDir, sanitized)
					// Ensure inside
					rel, err := filepath.Rel(baseSkillDir, full)
					if err != nil || strings.HasPrefix(rel, "..") {
						return fmt.Errorf("file %q outside skill directory", f)
					}
					data, err := os.ReadFile(full)
					if err != nil {
						return fmt.Errorf("read %s: %w", f, err)
					}
					cmdPrint(cmd, string(data))
					// ensure newline between files
					if !strings.HasSuffix(string(data), "\n") {
						cmdPrint(cmd, "\n")
					}
				}
				continue
			}

			// Link step
			cacheSkillPath := cachePath
			if r.SkillPath != "" {
				candidate := filepath.Join(cachePath, r.SkillPath)
				if _, err := os.Stat(candidate); err == nil {
					cacheSkillPath = candidate
				} else {
					// Try discovery: walk cache for SKILL.md with matching basename (for nested skills like skills/<name>)
					discovered := discoverSkillInCache(cachePath, filepath.Base(r.SkillPath))
					if discovered == "" {
						// Try git ls-tree discovery for sparse clones where file not checked out
						if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(r.SkillPath)); gitPath != "" {
							discoveredGitFull := filepath.Join(cachePath, gitPath)
							// Expand sparse checkout to include discovered path
							if _, err := git.Run(ctx, cachePath, "sparse-checkout", "set", "--cone", gitPath); err == nil {
								if _, err := os.Stat(discoveredGitFull); err == nil {
									discovered = discoveredGitFull
								}
							} else {
								// fallback: try --no-cone
								if _, err2 := git.Run(ctx, cachePath, "sparse-checkout", "set", "--no-cone", gitPath); err2 == nil {
									if _, err := os.Stat(discoveredGitFull); err == nil {
										discovered = discoveredGitFull
									}
								}
							}
							if discovered == "" {
								// As last resort, disable sparse and checkout all
								_, _ = git.Run(ctx, cachePath, "sparse-checkout", "disable")
								_, _ = git.Run(ctx, cachePath, "read-tree", "-mu", "HEAD")
								if _, err := os.Stat(discoveredGitFull); err == nil {
									discovered = discoveredGitFull
								}
							}
						}
					}
					if discovered != "" {
						cacheSkillPath = discovered
					} else {
						return fmt.Errorf("skill %q not found at ref %q in %s (checked %q). Try mskill get %s/%s --skill * or verify skill path", r.Slug, effectiveRef, cachePath, candidate, r.Owner, r.Repo)
					}
				}
			} else if len(skillPaths) > 0 && skillPaths[0] != "" {
				candidate := filepath.Join(cachePath, skillPaths[0])
				if _, err := os.Stat(candidate); err == nil {
					cacheSkillPath = candidate
				} else {
					discovered := discoverSkillInCache(cachePath, filepath.Base(skillPaths[0]))
					if discovered == "" {
						if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(skillPaths[0])); gitPath != "" {
							discoveredGitFull := filepath.Join(cachePath, gitPath)
							if _, err := git.Run(ctx, cachePath, "sparse-checkout", "set", "--cone", gitPath); err == nil {
								if _, err := os.Stat(discoveredGitFull); err == nil {
									discovered = discoveredGitFull
								}
							}
						}
					}
					if discovered != "" {
						cacheSkillPath = discovered
					}
				}
			}
			if _, err := os.Stat(cacheSkillPath); err != nil {
				return fmt.Errorf("skill path %q not found in cache %s: %w", cacheSkillPath, cachePath, err)
			}
			// If skillPath == "" and cachePath is repo root with single SKILL.md at root, that's valid.
			// Otherwise if cachePath lacks SKILL.md and skillPath was empty, try discovery for repo name
			if r.SkillPath == "" && len(skillPaths) == 0 {
				if _, err := os.Stat(filepath.Join(cacheSkillPath, "SKILL.md")); err != nil {
					// try to discover any SKILL.md under cachePath depth 2
					if discovered := discoverAnySkill(cachePath); discovered != "" {
						cacheSkillPath = discovered
					}
				}
			}
			skillName := r.Slug
			if skillName == "" {
				if r.SkillPath != "" {
					skillName = filepath.Base(r.SkillPath)
				} else {
					skillName = r.Repo
				}
			}
			opts := link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}
			if err := link.Install(cacheSkillPath, skillName, opts); err != nil {
				return err
			}
			// update lockfile hash
			if h, err := link.SkillFolderHash(cacheSkillPath); err == nil {
				_ = link.UpdateLockfile(global && !project, skillName, h)
			}
			cmdPrint(cmd, fmt.Sprintf("installed %s -> %s\n", raw, skillName))
		}
		return nil
	},
}

func normalizeColonRefGet(raw string) string {
	if resolve.IsLocalPath(raw) {
		return raw
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	if strings.HasPrefix(raw, "github:") || strings.HasPrefix(raw, "gitlab:") {
		return raw
	}
	idx := strings.Index(raw, ":")
	if idx == -1 {
		return raw
	}
	prefix := raw[:idx]
	if !strings.Contains(prefix, "/") {
		return raw
	}
	return prefix + "/" + raw[idx+1:]
}

func discoverSkillInCache(cachePath, slug string) string {
	// Search depth 5 for directory named slug containing SKILL.md (mirrors upstream findSkillDirs)
	var found string
	_ = filepath.WalkDir(cachePath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			// skip .git
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			// depth check via rel parts
			rel, _ := filepath.Rel(cachePath, p)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 5 {
				return filepath.SkipDir
			}
			if strings.EqualFold(d.Name(), slug) {
				if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
					found = p
					return filepath.SkipDir
				}
			}
		}
		return nil
	})
	return found
}

func discoverAnySkill(cachePath string) string {
	var found string
	_ = filepath.WalkDir(cachePath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if found != "" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "SKILL.md" {
			found = filepath.Dir(p)
			return filepath.SkipDir
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(cachePath, p)
		if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 5 {
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

func discoverSkillViaGit(ctx context.Context, cachePath, slug string) string {
	// Use git ls-tree to find SKILL.md files without needing full checkout (works with sparse/filter)
	out, err := git.Run(ctx, cachePath, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		// Try FETCH_HEAD or HEAD
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, "SKILL.md") {
			continue
		}
		dir := filepath.Dir(line)
		if strings.EqualFold(filepath.Base(dir), slug) {
			return dir
		}
	}
	// Fallback: any SKILL.md containing slug as dir component?
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "SKILL.md") && strings.Contains(strings.ToLower(line), strings.ToLower(slug)) {
			return filepath.Dir(line)
		}
	}
	return ""
}

func cmdPrint(cmd *cobra.Command, s string) {
	if cmd.OutOrStdout() != nil {
		fmt.Fprint(cmd.OutOrStdout(), s)
	} else {
		fmt.Fprint(os.Stdout, s)
	}
}

func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.Flags().BoolP("global", "g", false, "install to global (~/.agents/skills)")
	getCmd.Flags().BoolP("project", "p", false, "install to project (./.agents/skills)")
	getCmd.Flags().StringP("agent", "a", "", "comma-separated agents or * for all")
	getCmd.Flags().StringArray("skill", []string{}, "filter skills from repo (repeatable, * = all)")
	// keep alias -s single string for compat (hidden)
	_ = getCmd.Flags().StringP("skill-compat", "s", "", "compat single skill filter")
	getCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	getCmd.Flags().Bool("copy", false, "copy files instead of symlink")
	getCmd.Flags().String("link-mode", "", "link mode: auto|symlink|copy")
	getCmd.Flags().Bool("show", false, "show skill contents without linking (Resolve→Cache→cat)")
	getCmd.Flags().StringArray("file", []string{}, "with --show: file(s) to print (repeatable, default SKILL.md)")
	getCmd.Flags().BoolP("yes", "y", false, "skip confirmation (risky still fails closed)")
	getCmd.Flags().Bool("force", false, "force refetch even if cache is fresh")
	getCmd.Flags().Bool("full-depth", false, "recursive skill discovery depth 5")
}
