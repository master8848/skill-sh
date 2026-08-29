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
		for _, raw := range raws {
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
				cacheSkillPath = filepath.Join(cachePath, r.SkillPath)
			} else if len(skillPaths) > 0 && skillPaths[0] != "" {
				// fallback
				candidate := filepath.Join(cachePath, skillPaths[0])
				if _, err := os.Stat(candidate); err == nil {
					cacheSkillPath = candidate
				}
			}
			// If SkillPath points to nested dir but repo root contains discovery, try to discover SKILL.md?
			// For minimal: if cacheSkillPath doesn't exist, keep cachePath
			if _, err := os.Stat(cacheSkillPath); err != nil {
				cacheSkillPath = cachePath
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
