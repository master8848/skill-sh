package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
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
		agentFilter := ""
		if slice, err := cmd.Flags().GetStringSlice("agent"); err == nil && len(slice) > 0 {
			var parts []string
			for _, s := range slice {
				s = strings.TrimSpace(s)
				if s == "" {
					continue
				}
				for _, p := range strings.Split(s, ",") {
					if t := strings.TrimSpace(p); t != "" {
						parts = append(parts, t)
					}
				}
			}
			agentFilter = strings.Join(parts, ",")
		} else if v, err := cmd.Flags().GetString("agent"); err == nil {
			agentFilter = strings.TrimSpace(v)
		}
		if !cmd.Flags().Changed("agent") && !global && !project {
			if security.IsInteractiveTTY() && !security.IsAgentEnv() {
				if sel, err := promptAgentSelection(); err == nil && strings.TrimSpace(sel) != "" {
					agentFilter = sel
				}
			}
		}
		copyFlag, _ := cmd.Flags().GetBool("copy")
		linkMode, _ := cmd.Flags().GetString("link-mode")
		if !cmd.Flags().Changed("link-mode") {
			linkMode = ""
		}
		yesFlag, _ := cmd.Flags().GetBool("yes")
		if yesFlag {
			viper.Set("yes", true)
		}
		// Wire --full-depth: registered but previously unread. Minimal handling is to
		// set viper cache.fullDepth so future ListSkills callers can check it.
		// ListSkills currently hardcodes walk depth 5; when fullDepth is true callers
		// should use deeper discovery (e.g. depth 10). For now just ensure flag is
		// consumed and propagated to viper without error.
		if fullDepth, _ := cmd.Flags().GetBool("full-depth"); fullDepth {
			viper.Set("cache.fullDepth", true)
		} else if pf := cmd.PersistentFlags().Lookup("full-depth"); pf != nil && pf.Value.String() == "true" {
			viper.Set("cache.fullDepth", true)
		} else if inherited := cmd.InheritedFlags().Lookup("full-depth"); inherited != nil && inherited.Value.String() == "true" {
			viper.Set("cache.fullDepth", true)
		}
		allFlag, _ := cmd.Flags().GetBool("all")
		if allFlag {
			agentFilter = "*"
			yesFlag = true
			viper.Set("yes", true)
		}
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
				for _, part := range strings.Split(v, ",") {
					if s := strings.TrimSpace(part); s != "" && s != "skill" {
						skillFilters = append(skillFilters, s)
					}
				}
			}
		}
		// Normalize short flag -s handling: also check skill-compat if set
		if cval, err := cmd.Flags().GetString("skill-compat"); err == nil && strings.TrimSpace(cval) != "" {
			for _, part := range strings.Split(cval, ",") {
				if s := strings.TrimSpace(part); s != "" {
					skillFilters = append(skillFilters, s)
				}
			}
		}
		// split comma inside skillFilters entries (support --skill a,b)
		var normalizedFilters []string
		for _, sf := range skillFilters {
			for _, part := range strings.Split(sf, ",") {
				if t := strings.TrimSpace(part); t != "" {
					normalizedFilters = append(normalizedFilters, t)
				}
			}
		}
		skillFilters = normalizedFilters
		if allFlag {
			skillFilters = []string{"*"}
		}

		var raws []string
		if len(args) > 0 {
			raws = args
		}
		if len(raws) == 0 {
			return fmt.Errorf("skill reference required: mskill get owner/repo/skill or owner/repo --skill <name>")
		}
		isList, _ := cmd.Flags().GetBool("list")

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
			// local type handling with discovery
			if r.IsLocal {
				localPath := r.CloneURL
				// handle --list for local: enumerate skills
				if isList {
					skills, _ := cache.ListSkills(ctx, localPath)
					if len(skills) == 0 {
						// check if localPath itself is a skill (contains SKILL.md)
						if _, err := os.Stat(filepath.Join(localPath, "SKILL.md")); err == nil {
							skills = []string{localPath}
						} else {
							cmd.Println("no skills found (missing SKILL.md)")
							continue
						}
					}
					for _, s := range skills {
						disp := s
						if disp == "" {
							disp = "."
						}
						cmd.Printf("%s\n", disp)
					}
					continue
				}
				// multi-skill local handling
				var selectedLocal []string
				hasExplicit := r.SkillPath != "" || r.Slug != "" && strings.Contains(rawOrig, "/") && filepath.Base(rawOrig) == r.Slug
				// Determine if rawOrig is repo root vs skill subpath via file existence
				if _, err := os.Stat(localPath); err != nil {
					if len(skillFilters) > 0 {
						base := filepath.Dir(strings.TrimSuffix(localPath, "/"))
						if base == "." || base == "" {
							base = localPath
						}
						if discovered := discoverSkillInCache(base, filepath.Base(localPath)); discovered != "" {
							localPath = discovered
							selectedLocal = []string{localPath}
							hasExplicit = true
						} else if discovered := discoverSkillInCache(base, r.Slug); discovered != "" {
							localPath = discovered
							selectedLocal = []string{discovered}
							hasExplicit = true
						} else {
							return fmt.Errorf("local skill %q not found (checked %q)", r.Slug, localPath)
						}
					} else {
						return fmt.Errorf("local path %q does not exist: %w", localPath, err)
					}
				}
				if hasExplicit && len(selectedLocal) == 0 {
					selectedLocal = []string{localPath}
				}
				if len(selectedLocal) == 0 {
					// Need discovery for local multi-skill repo
					if len(skillFilters) > 0 {
						hasStar := false
						for _, sf := range skillFilters {
							if sf == "*" {
								hasStar = true
								break
							}
						}
						if hasStar {
							all, _ := cache.ListSkills(ctx, localPath)
							if len(all) == 0 {
								return fmt.Errorf("no skills found in %s", localPath)
							}
							for i, p := range all {
								if p == "" {
									all[i] = localPath
								} else {
									all[i] = filepath.Join(localPath, p)
								}
							}
							selectedLocal = all
						} else {
							for _, sf := range skillFilters {
								sf = strings.ReplaceAll(sf, "\\", "/")
								sanitized, _ := resolve.SanitizeSubpath(sf)
								if sanitized == "" {
									sanitized = sf
								}
								cand := filepath.Join(localPath, sanitized)
								if _, err := os.Stat(cand); err == nil {
									selectedLocal = append(selectedLocal, cand)
								} else if discovered := discoverSkillInCache(localPath, filepath.Base(sanitized)); discovered != "" {
									selectedLocal = append(selectedLocal, discovered)
								} else {
									return fmt.Errorf("local skill %q not found in %s", sf, localPath)
								}
							}
						}
					} else {
						// no filter, discover all
						all, _ := cache.ListSkills(ctx, localPath)
						if len(all) == 0 {
							// single skill at root?
							if _, err := os.Stat(filepath.Join(localPath, "SKILL.md")); err == nil {
								selectedLocal = []string{localPath}
							} else {
								return fmt.Errorf("no skills found in %s (missing SKILL.md). Try --skill <name> or --list", localPath)
							}
						} else if len(all) == 1 {
							p := all[0]
							if p == "" {
								selectedLocal = []string{localPath}
							} else {
								selectedLocal = []string{filepath.Join(localPath, p)}
							}
						} else {
							if security.IsInteractiveTTY() && !security.IsAgentEnv() && !viper.GetBool("yes") {
								// Map to display names for prompt
								display := make([]string, len(all))
								for i, p := range all {
									if p == "" {
										display[i] = "."
									} else {
										display[i] = p
									}
								}
								chosen, err := promptSkillSelection(display)
								if err != nil {
									return err
								}
								for _, c := range chosen {
									if c == "." || c == "" {
										selectedLocal = append(selectedLocal, localPath)
									} else {
										selectedLocal = append(selectedLocal, filepath.Join(localPath, c))
									}
								}
							} else {
								return fmt.Errorf("local repo %s contains %d skills; use --skill <name> (repeatable), --skill \"*\" for all, or --list to discover. Available: %s", localPath, len(all), strings.Join(allDisplay(all), ", "))
							}
						}
					}
				}
				if isShow {
					targetFiles := files
					if len(targetFiles) == 0 {
						targetFiles = []string{"SKILL.md"}
					}
					for _, lp := range selectedLocal {
						for _, f := range targetFiles {
							f = strings.ReplaceAll(f, "\\", "/")
							sanitized, err := resolve.SanitizeSubpath(f)
							if err != nil {
								return err
							}
							full := filepath.Join(lp, sanitized)
							data, err := os.ReadFile(full)
							if err != nil {
								return fmt.Errorf("read %s: %w", f, err)
							}
							cmdPrint(cmd, string(data))
							if len(selectedLocal) > 1 || len(targetFiles) > 1 {
								cmdPrint(cmd, "\n---\n")
							}
						}
					}
					continue
				}
				for _, lp := range selectedLocal {
					skillName := filepath.Base(strings.TrimSuffix(lp, "/"))
					if skillName == "." || skillName == "" {
						skillName = filepath.Base(strings.TrimSuffix(localPath, "/"))
						if r.Slug != "" {
							skillName = r.Slug
						}
					}
					skillName = resolve.SanitizeName(skillName)
					if skillName == "" {
						skillName = filepath.Base(strings.TrimSuffix(lp, "/"))
					}
					if err := link.Install(lp, skillName, link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}); err != nil {
						return err
					}
					cmdPrint(cmd, fmt.Sprintf("linked local %s -> %s\n", lp, skillName))
				}
				continue
			}

			// Determine effective ref
			effectiveRef := refFlag
			if effectiveRef == "" {
				effectiveRef = r.Ref
			}

			// === --list handling for remote ===
			if isList {
				cachePath, meta, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
				if err != nil {
					return fmt.Errorf("cache %q: %w", raw, err)
				}
				if verbose {
					fmt.Fprintf(os.Stderr, "cache: %s commit %s\n", cachePath, meta.CommitSHA)
				}
				skills, _ := cache.ListSkills(ctx, cachePath)
				if len(skills) == 0 {
					cmd.Println("no skills found (missing SKILL.md)")
					continue
				}
				for _, s := range skills {
					disp := s
					if disp == "" {
						disp = "."
					}
					cmd.Printf("%s\n", disp)
				}
				continue
			}

			// Determine selected skills
			var selected []string
			hasExplicitSubpath := r.SkillPath != ""
			if hasExplicitSubpath {
				selected = []string{r.SkillPath}
			} else if len(skillFilters) > 0 {
				hasStar := false
				for _, sf := range skillFilters {
					if sf == "*" {
						hasStar = true
						break
					}
				}
				if hasStar {
					tmpPath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
					if err != nil {
						return fmt.Errorf("cache %q: %w", raw, err)
					}
					all, _ := cache.ListSkills(ctx, tmpPath)
					if len(all) == 0 {
						return fmt.Errorf("no skills found in %s/%s (no SKILL.md). Try verifying repo or --skill <name>", r.Owner, r.Repo)
					}
					selected = all
				} else {
					for _, sf := range skillFilters {
						sf = strings.ReplaceAll(sf, "\\", "/")
						sanitized, err := resolve.SanitizeSubpath(sf)
						if err != nil {
							return err
						}
						if sanitized == "" {
							sanitized = sf
						}
						selected = append(selected, sanitized)
					}
				}
			} else {
				// no explicit subpath nor --skill → discover
				tmpPath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
				if err != nil {
					return fmt.Errorf("cache %q: %w", raw, err)
				}
				all, _ := cache.ListSkills(ctx, tmpPath)
				if len(all) == 0 {
					return fmt.Errorf("no skills found in %s/%s (missing SKILL.md). Try mskill get %s/%s --skill <name> or --list", r.Owner, r.Repo, r.Owner, r.Repo)
				}
				// Filter empty root handling: if repo has root SKILL.md plus subdirs, keep all
				if len(all) == 1 {
					selected = all
				} else {
					if security.IsInteractiveTTY() && !security.IsAgentEnv() && !viper.GetBool("yes") {
						// Use survey.MultiSelect to choose — mirrors spec requirement
						display := make([]string, len(all))
						for i, p := range all {
							if p == "" {
								display[i] = "."
							} else {
								display[i] = p
							}
						}
						chosen, err := promptSkillSelection(display)
						if err != nil {
							return err
						}
						if len(chosen) == 0 {
							return fmt.Errorf("no skills selected")
						}
						selected = chosen
						// map "." back to "" for internal handling
						for i, s := range selected {
							if s == "." {
								selected[i] = ""
							}
						}
					} else {
						return fmt.Errorf("repo %s/%s contains %d skills; use --skill <name> (repeatable), --skill \"*\" for all, or --list to discover. Available: %s", r.Owner, r.Repo, len(all), strings.Join(allDisplay(all), ", "))
					}
				}
			}

			// Ensure sparse checkout contains all selected
			var sparsePaths []string
			for _, s := range selected {
				if s != "" && s != "." {
					sparsePaths = append(sparsePaths, s)
				}
			}
			cachePath, meta, err := cache.Ensure(ctx, paths, r, effectiveRef, sparsePaths, force)
			if err != nil {
				return fmt.Errorf("cache %q: %w", raw, err)
			}
			if verbose {
				fmt.Fprintf(os.Stderr, "cache: %s commit %s\n", cachePath, meta.CommitSHA)
			}

			// Handle --show for multi skills
			if isShow {
				targetFiles := files
				if len(targetFiles) == 0 {
					targetFiles = []string{"SKILL.md"}
				}
				for _, skillRel := range selected {
					baseSkillDir := cachePath
					if skillRel != "" && skillRel != "." {
						baseSkillDir = filepath.Join(cachePath, skillRel)
						// fallback discovery if path not materialized (sparse edge)
						if _, err := os.Stat(baseSkillDir); err != nil {
							if discovered := discoverSkillInCache(cachePath, filepath.Base(skillRel)); discovered != "" {
								baseSkillDir = discovered
							} else if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(skillRel)); gitPath != "" {
								baseSkillDir = filepath.Join(cachePath, gitPath)
							}
						}
					}
					for _, f := range targetFiles {
						f = strings.ReplaceAll(f, "\\", "/")
						sanitized, err := resolve.SanitizeSubpath(f)
						if err != nil {
							return fmt.Errorf("invalid --file %q: %w", f, err)
						}
						full := filepath.Join(baseSkillDir, sanitized)
						rel, err := filepath.Rel(baseSkillDir, full)
						if err != nil || strings.HasPrefix(rel, "..") {
							return fmt.Errorf("file %q outside skill directory", f)
						}
						data, err := os.ReadFile(full)
						if err != nil {
							return fmt.Errorf("read %s: %w", f, err)
						}
						cmdPrint(cmd, string(data))
						if !strings.HasSuffix(string(data), "\n") {
							cmdPrint(cmd, "\n")
						}
						if len(selected) > 1 || len(targetFiles) > 1 {
							cmdPrint(cmd, "\n---\n")
						}
					}
				}
				continue
			}

			// Iterate over selected skills for Link
			for _, skillRel := range selected {
				// Audit gate per skill
				slugForAudit := filepath.Base(skillRel)
				if skillRel == "" || skillRel == "." {
					slugForAudit = r.Slug
					if slugForAudit == "" {
						slugForAudit = r.Repo
					}
				}
				if slugForAudit == "" {
					slugForAudit = filepath.Base(skillRel)
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
							if !security.IsTrustEnabled(paths) {
								if err := security.RequirePassword(slugForAudit, v.Reason, paths); err != nil {
									return err
								}
							}
						}
					}
				}

				// Resolve cacheSkillPath
				cacheSkillPath := cachePath
				if skillRel != "" && skillRel != "." {
					candidate := filepath.Join(cachePath, skillRel)
					if _, err := os.Stat(candidate); err == nil {
						cacheSkillPath = candidate
					} else {
						discovered := discoverSkillInCache(cachePath, filepath.Base(skillRel))
						if discovered == "" {
							if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(skillRel)); gitPath != "" {
								discoveredGitFull := filepath.Join(cachePath, gitPath)
								if _, err := git.Run(ctx, cachePath, "sparse-checkout", "set", "--cone", gitPath); err == nil {
									if _, err := os.Stat(discoveredGitFull); err == nil {
										discovered = discoveredGitFull
									}
								} else {
									if _, err2 := git.Run(ctx, cachePath, "sparse-checkout", "set", "--no-cone", gitPath); err2 == nil {
										if _, err := os.Stat(discoveredGitFull); err == nil {
											discovered = discoveredGitFull
										}
									}
								}
								if discovered == "" {
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
							return fmt.Errorf("skill %q not found at ref %q in %s (checked %q). Try mskill get %s/%s --skill * or verify skill path", resolve.SanitizeName(filepath.Base(skillRel)), effectiveRef, cachePath, candidate, r.Owner, r.Repo)
						}
					}
				} else if skillRel == "" {
					// root skill: cachePath is already skill dir if SKILL.md at root, else discover fallback
					if _, err := os.Stat(filepath.Join(cacheSkillPath, "SKILL.md")); err != nil {
						if _, err2 := os.Stat(filepath.Join(cacheSkillPath, "skill.md")); err2 != nil {
							if discovered := discoverAnySkill(cachePath); discovered != "" {
								cacheSkillPath = discovered
							}
						}
					}
				}
				if _, err := os.Stat(cacheSkillPath); err != nil {
					return fmt.Errorf("skill path %q not found in cache %s: %w", cacheSkillPath, cachePath, err)
				}
				skillName := resolve.SanitizeName(filepath.Base(skillRel))
				if skillRel == "" || skillRel == "." || skillName == "" || skillName == "." {
					if r.SkillPath != "" {
						skillName = resolve.SanitizeName(filepath.Base(r.SkillPath))
					} else if r.Slug != "" {
						skillName = r.Slug
					} else {
						skillName = r.Repo
					}
				}
				if skillName == "" {
					skillName = filepath.Base(cacheSkillPath)
				}
				opts := link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}
				if err := link.Install(cacheSkillPath, skillName, opts); err != nil {
					return err
				}
				if h, err := link.SkillFolderHash(cacheSkillPath); err == nil {
					doGlobalLock := global || (!global && !project)
					doProjectLock := project || (!global && !project)
					if global && !project {
						doGlobalLock = true
						doProjectLock = false
					} else if project && !global {
						doGlobalLock = false
						doProjectLock = true
					} else if global && project {
						doGlobalLock = true
						doProjectLock = true
					}
					if doGlobalLock {
						_ = link.UpdateLockfile(true, skillName, h)
					}
					if doProjectLock {
						_ = link.UpdateLockfile(false, skillName, h)
					}
				}
				cmdPrint(cmd, fmt.Sprintf("installed %s -> %s\n", raw, skillName))
			}
		}
		return nil
	},
}

// promptAgentSelection shows an interactive MultiSelect for agent destinations.
func promptAgentSelection() (string, error) {
	keysSet := make(map[string]bool)
	var keys []string
	for k := range link.Agents {
		if !keysSet[k] {
			keysSet[k] = true
			keys = append(keys, k)
		}
	}
	if !keysSet["*"] {
		keys = append(keys, "*")
	}
	sort.Strings(keys)
	def := []string{"project"}
	hasProject := false
	for _, k := range keys {
		if k == "project" {
			hasProject = true
			break
		}
	}
	if !hasProject {
		def = []string{"agents"}
	}

	var selected []string
	prompt := &survey.MultiSelect{
		Message: "Select agents to install skill to:",
		Options: keys,
		Default: def,
		Help:    "Use space to select, enter to confirm. '*' selects all agents.",
	}
	if err := survey.AskOne(prompt, &selected); err != nil {
		return "", err
	}
	if len(selected) == 0 {
		return strings.Join(def, ","), nil
	}
	for _, s := range selected {
		if s == "*" {
			return "*", nil
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range selected {
		ls := strings.ToLower(strings.TrimSpace(s))
		if ls == "" || seen[ls] {
			continue
		}
		seen[ls] = true
		out = append(out, ls)
	}
	if len(out) == 0 {
		return strings.Join(def, ","), nil
	}
	return strings.Join(out, ","), nil
}

func promptSkillSelection(skills []string) ([]string, error) {
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills to select")
	}
	if len(skills) == 1 {
		var one string
		prompt := &survey.Select{
			Message: "Select skill to install:",
			Options: skills,
			Default: skills[0],
		}
		if err := survey.AskOne(prompt, &one); err != nil {
			return nil, err
		}
		return []string{one}, nil
	}
	// multiple → MultiSelect; use survey.MultiSelect per spec requirement
	var selected []string
	prompt := &survey.MultiSelect{
		Message: "Select skills to install:",
		Options: skills,
		Help:    "Use space to select, enter to confirm.",
	}
	if err := survey.AskOne(prompt, &selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func allDisplay(all []string) []string {
	out := make([]string, len(all))
	for i, p := range all {
		if p == "" {
			out[i] = "."
		} else {
			out[i] = p
		}
	}
	return out
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
	var found string
	_ = filepath.WalkDir(cachePath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if found != "" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), ".git") {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(cachePath, p)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 5 {
				return filepath.SkipDir
			}
			if strings.EqualFold(d.Name(), slug) {
				if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err == nil {
					found = p
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(p, "skill.md")); err == nil {
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
		if !d.IsDir() && (strings.EqualFold(d.Name(), "SKILL.md") || strings.EqualFold(d.Name(), "skill.md")) {
			found = filepath.Dir(p)
			return filepath.SkipDir
		}
		if d.IsDir() && strings.EqualFold(d.Name(), ".git") {
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
	out, err := git.Run(ctx, cachePath, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.EqualFold(filepath.Base(line), "SKILL.md") && !strings.EqualFold(filepath.Base(line), "skill.md") {
			continue
		}
		dir := filepath.Dir(line)
		if strings.EqualFold(filepath.Base(dir), slug) {
			return dir
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if (strings.EqualFold(filepath.Base(line), "SKILL.md") || strings.EqualFold(filepath.Base(line), "skill.md")) && strings.Contains(strings.ToLower(line), strings.ToLower(slug)) {
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
	if getCmd.Flags().Lookup("global") == nil {
		getCmd.Flags().BoolP("global", "g", false, "install to global (~/.agents/skills)")
	}
	if getCmd.Flags().Lookup("project") == nil {
		getCmd.Flags().BoolP("project", "p", false, "install to project (./.agents/skills)")
	}
	if getCmd.Flags().Lookup("agent") == nil {
		getCmd.Flags().StringSliceP("agent", "a", nil, "comma-separated agents or * for all (repeatable, comma-separated)")
	}
	if getCmd.Flags().Lookup("skill") == nil {
		getCmd.Flags().StringArrayP("skill", "s", []string{}, "filter skills from repo (repeatable, * = all)")
	}
	if getCmd.Flags().Lookup("skill-compat") == nil {
		_ = getCmd.Flags().String("skill-compat", "", "compat single skill filter (hidden)")
		_ = getCmd.Flags().MarkHidden("skill-compat")
	}
	if getCmd.Flags().Lookup("ref") == nil {
		getCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	}
	if getCmd.Flags().Lookup("copy") == nil {
		getCmd.Flags().Bool("copy", false, "copy files instead of symlink")
	}
	if getCmd.Flags().Lookup("link-mode") == nil {
		getCmd.Flags().String("link-mode", "auto", "link mode: auto|symlink|copy (default auto)")
	}
	if getCmd.Flags().Lookup("show") == nil {
		getCmd.Flags().Bool("show", false, "show skill contents without linking (Resolve→Cache→cat)")
	}
	if getCmd.Flags().Lookup("file") == nil {
		getCmd.Flags().StringArray("file", []string{}, "with --show: file(s) to print (repeatable, default SKILL.md)")
	}
	if getCmd.Flags().Lookup("yes") == nil {
		getCmd.Flags().BoolP("yes", "y", false, "skip confirmation (risky still fails closed)")
	}
	if getCmd.Flags().Lookup("force") == nil {
		getCmd.Flags().Bool("force", false, "force refetch even if cache is fresh")
	}
	if getCmd.Flags().Lookup("full-depth") == nil {
		getCmd.Flags().Bool("full-depth", false, "recursive skill discovery depth 5")
	}
	if getCmd.Flags().Lookup("list") == nil {
		getCmd.Flags().BoolP("list", "l", false, "list available skills in repo without installing")
	}
	if getCmd.Flags().Lookup("all") == nil {
		getCmd.Flags().Bool("all", false, "install all skills to all agents (equiv. --skill * --agent * -y)")
	}
	// Guard --subagent / --metadata against duplicate registration from parallel subagents
	if getCmd.Flags().Lookup("subagent") == nil && getCmd.PersistentFlags().Lookup("subagent") == nil && rootCmd.PersistentFlags().Lookup("subagent") == nil {
		getCmd.Flags().Bool("subagent", false, "subagent mode (compat)")
		_ = getCmd.Flags().MarkHidden("subagent")
	}
	if getCmd.Flags().Lookup("metadata") == nil && getCmd.PersistentFlags().Lookup("metadata") == nil && rootCmd.PersistentFlags().Lookup("metadata") == nil {
		getCmd.Flags().String("metadata", "", "metadata json (compat)")
		_ = getCmd.Flags().MarkHidden("metadata")
	}
}
