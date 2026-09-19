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
	Long: `Resolve → Cache → Link a skill.

  1. Resolve — parse owner/repo/skill and --skill/--ref
  2. Cache   — sparse git fetch into ~/.cache/mskill (or --cache-dir)
  3. Link    — symlink/copy into agent dirs (~/.agents/skills, ./.agents/skills, etc.)

Security is fail-closed: UNSAFE/UNKNOWN requires human trust unless mskill trust enable.`,
	Example: `  # Anki-import (the primary skill)
  mskill get master8848/Anki-import/ --project
  mskill get master8848/Anki-import --skill anki-import-cli --project --yes
  mskill get master8848/Anki-import --skill anki-import-cli --project --ref main --force

  # discovery before install
  mskill get vercel-labs/agent-skills --list
  mskill get vercel-labs/agent-skills --skill vercel-optimize --global --agent claude-code

  # view without linking
  mskill get master8848/Anki-import --skill anki-import-cli --show --file SKILL.md
  mskill get owner/repo --skill "*" --all   # all skills → all agents`,
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
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
			if security.IsInteractiveTTY() && !security.IsAgentEnv() && !viper.GetBool("yes") {
				// pass first raw for context if available
				repoHint := ""
				if len(args) > 0 {
					repoHint = args[0]
				}
				if sel, err := promptAgentSelection(repoHint); err == nil && strings.TrimSpace(sel) != "" {
					agentFilter = sel
				}
			} else if agentFilter == "" {
				// non-interactive / --yes / agent env: auto-pick sensible default
				def := "project"
				if !isProjectDirectory() {
					def = "agents"
				}
				agentFilter = def
				if viper.GetBool("verbose") || security.IsInteractiveTTY() {
					fmt.Fprintf(os.Stderr, "\x1b[2m› auto-selecting --agent %s (use --agent to override, --yes to skip prompts)\x1b[0m\n", def)
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
			return fmt.Errorf("skill reference required. Try: mskill get owner/repo/skill or mskill get owner/repo --skill <name> --help\nTip: for example mskill get vercel-labs/agent-skills --list to discover skills")
		}
		isList, _ := cmd.Flags().GetBool("list")

		verbose := viper.GetBool("verbose")
		for _, rawOrig := range raws {
			printStep(cmd, "Resolving", rawOrig+"…")
			raw := normalizeColonRef(rawOrig)
			r, err := resolve.ParseSkillRef(raw)
			if err != nil {
				// human-readable resolve errors: empty reference, sanitize ".." etc.
				if strings.Contains(err.Error(), "empty skill reference") {
					return fmt.Errorf("Cannot resolve %q: empty skill reference. Try: mskill get owner/repo/skill or mskill get owner/repo --skill <name> --list\nTip: mskill show owner/repo --list to discover available skills (%w)", raw, err)
				}
				if strings.Contains(err.Error(), "invalid subpath") {
					return fmt.Errorf("Cannot resolve %q: %v. Tip: subpath cannot contain \"..\" and must be inside skill directory (%w)", raw, err, err)
				}
				return fmt.Errorf("Cannot resolve %q: %v. Try: mskill get owner/repo/skill --help or mskill show owner/repo --list\nTip: check spelling, use owner/repo or owner/repo/skill (%w)", raw, err, err)
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
							cmd.Println("no skills found (missing SKILL.md). Tip: check path contains SKILL.md or use mskill show <path> --list")
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
							// suggest available via cache.ListSkills on parent
							base := filepath.Dir(strings.TrimSuffix(localPath, "/"))
							if avail, _ := cache.ListSkills(ctx, base); len(avail) > 0 {
								return fmt.Errorf("local skill %q not found (checked %q). Available: %s. Tip: use mskill get %s --skill <name> --list (%w)", r.Slug, localPath, strings.Join(allDisplay(avail), ", "), base, err)
							}
							return fmt.Errorf("local skill %q not found (checked %q). Tip: check path exists or use mskill get owner/repo --list (%w)", r.Slug, localPath, err)
						}
					} else {
						return fmt.Errorf("local path %q does not exist: %w. Tip: check path or use mskill get owner/repo (e.g., mskill get vercel-labs/agent-skills --list)", localPath, err)
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
								return fmt.Errorf("no skills found in %s (missing SKILL.md). Tip: check path or try mskill show %s --list", localPath, localPath)
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
								sanitized, errSan := resolve.SanitizeSubpath(sf)
								if errSan != nil {
									return fmt.Errorf("invalid --skill %q: %v. Tip: skill subpath cannot contain \"..\" (%w)", sf, errSan, errSan)
								}
								if sanitized == "" {
									sanitized = sf
								}
								cand := filepath.Join(localPath, sanitized)
								if _, err := os.Stat(cand); err == nil {
									selectedLocal = append(selectedLocal, cand)
								} else if discovered := discoverSkillInCache(localPath, filepath.Base(sanitized)); discovered != "" {
									selectedLocal = append(selectedLocal, discovered)
								} else {
									if avail, _ := cache.ListSkills(ctx, localPath); len(avail) > 0 {
										return fmt.Errorf("local skill %q not found in %s. Available: %s. Tip: use --list to discover", sf, localPath, strings.Join(allDisplay(avail), ", "))
									}
									return fmt.Errorf("local skill %q not found in %s. Tip: mskill show %s --list to see available", sf, localPath, localPath)
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
							} else if _, err := os.Stat(filepath.Join(localPath, "skill.md")); err == nil {
								selectedLocal = []string{localPath}
							} else {
								return fmt.Errorf("no skills found in %s (missing SKILL.md). Try --skill <name> or --list\nTip: mskill show %s --list or check repo contains SKILL.md", localPath, localPath)
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
								chosen, err := promptSkillSelection(localPath, all)
								if err != nil {
									return fmt.Errorf("skill selection failed: %w. Tip: use --skill <name> or --skill \"*\" -y to skip prompt", err)
								}
								for _, c := range chosen {
									if c == "." || c == "" {
										selectedLocal = append(selectedLocal, localPath)
									} else {
										selectedLocal = append(selectedLocal, filepath.Join(localPath, c))
									}
								}
							} else {
								if viper.GetBool("yes") {
									for _, p := range all {
										if p == "" {
											selectedLocal = append(selectedLocal, localPath)
										} else {
											selectedLocal = append(selectedLocal, filepath.Join(localPath, p))
										}
									}
									fmt.Fprintf(os.Stderr, "\x1b[2m› auto-selecting all %d skills (--yes).\x1b[0m\n", len(all))
								} else {
									return fmt.Errorf("local repo %s contains %d skills; use --skill <name> (repeatable), --skill \"*\" for all, or --list to discover. Available: %s\nTip: mskill get %s --skill \"*\" -y  to install all", localPath, len(all), strings.Join(allDisplay(all), ", "), localPath)
								}
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
								return fmt.Errorf("invalid --file %q: %v. Tip: file cannot contain \"..\" (%w)", f, err, err)
							}
							full := filepath.Join(lp, sanitized)
							data, err := os.ReadFile(full)
							if err != nil {
								return fmt.Errorf("read %s: %w. Tip: check file exists; try mskill show %s --list", f, err, lp)
							}
							cmdPrint(cmd, string(data))
							if len(selectedLocal) > 1 || len(targetFiles) > 1 {
								cmdPrint(cmd, "\n---\n")
							}
						}
					}
					// footer: where skill lives (useful when skill has other files)
					for _, lp := range selectedLocal {
						cmdPrint(cmd, fmt.Sprintf("\n---\nskill dir: %s\n", lp))
						cmdPrint(cmd, fmt.Sprintf("explore: ls %s\n", lp))
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
					printStep(cmd, "Linking", lp+" → "+skillName+"…")
					if err := link.Install(lp, skillName, link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}); err != nil {
						return fmt.Errorf("link %q: %w. Tip: check destination writable or try --copy", skillName, err)
					}
					cmdPrint(cmd, fmt.Sprintf("linked local %s -> %s\n", lp, skillName))
					cmdPrint(cmd, fmt.Sprintf("  source: %s\n", lp))
					// show linked destinations
					dests := link.ResolveDestinations(agentFilter, global, project)
					for _, ag := range dests {
						var targets []string
						if global && !project {
							targets = append(targets, filepath.Join(expandHome(ag.GlobalDir), skillName))
						} else if project && !global {
							targets = append(targets, filepath.Join(ag.ProjectDir, skillName))
						} else {
							targets = append(targets, filepath.Join(expandHome(ag.GlobalDir), skillName))
							targets = append(targets, filepath.Join(ag.ProjectDir, skillName))
						}
						for _, t := range targets {
							cmdPrint(cmd, fmt.Sprintf("  linked: %s\n", t))
						}
					}
					cmdPrint(cmd, fmt.Sprintf("  explore: ls %s\n", lp))
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
				printStep(cmd, "Caching", r.Owner+"/"+r.Repo+"…")
				cachePath, meta, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
				if err != nil {
					return fmt.Errorf("Cannot cache %q at ref %q: %v. Tip: try --ref main or check repo exists and --ref valid (%w)", raw, effectiveRef, err, err)
				}
				if verbose {
					fmt.Fprintf(os.Stderr, "cache: %s commit %s\n", cachePath, meta.CommitSHA)
				}
				skills, _ := cache.ListSkills(ctx, cachePath)
				if len(skills) == 0 {
					cmd.Println("no skills found (missing SKILL.md). Tip: try mskill show owner/repo --list or verify repo contains SKILL.md")
					continue
				}
				for _, s := range skills {
					disp := s
					if disp == "" {
						disp = "."
					}
					cmd.Printf("%s\n", disp)
				}
				cmdPrint(cmd, fmt.Sprintf("\n---\ncached at: %s\n", cachePath))
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
					printStep(cmd, "Caching", r.Owner+"/"+r.Repo+"…")
					tmpPath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
					if err != nil {
						return fmt.Errorf("Cannot cache %q at ref %q: %v. Tip: try --ref main (%w)", raw, effectiveRef, err, err)
					}
					all, _ := cache.ListSkills(ctx, tmpPath)
					if len(all) == 0 {
						return fmt.Errorf("no skills found in %s/%s (missing SKILL.md). Try verifying repo exists or use --skill <name> or --list\nTip: mskill show %s/%s --list", r.Owner, r.Repo, r.Owner, r.Repo)
					}
					selected = all
				} else {
					for _, sf := range skillFilters {
						sf = strings.ReplaceAll(sf, "\\", "/")
						sanitized, err := resolve.SanitizeSubpath(sf)
						if err != nil {
							return fmt.Errorf("invalid --skill %q: %v. Tip: skill name cannot contain \"..\" (%w)", sf, err, err)
						}
						if sanitized == "" {
							sanitized = sf
						}
						selected = append(selected, sanitized)
					}
				}
			} else {
				// no explicit subpath nor --skill → discover
				printStep(cmd, "Caching", r.Owner+"/"+r.Repo+"…")
				tmpPath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
				if err != nil {
					return fmt.Errorf("Cannot cache %q at ref %q: %v. Tip: try --ref main or check repo exists (%w)", raw, effectiveRef, err, err)
				}
				all, _ := cache.ListSkills(ctx, tmpPath)
				if len(all) == 0 {
					return fmt.Errorf("no skills found in %s/%s (missing SKILL.md). Try mskill get %s/%s --skill <name> or --list\nTip: mskill show %s/%s --list to discover", r.Owner, r.Repo, r.Owner, r.Repo, r.Owner, r.Repo)
				}
				// Filter empty root handling: if repo has root SKILL.md plus subdirs, keep all
				if len(all) == 1 {
					selected = all
				} else {
					if security.IsInteractiveTTY() && !security.IsAgentEnv() && !viper.GetBool("yes") {
						chosen, err := promptSkillSelection(r.Owner+"/"+r.Repo, all)
						if err != nil {
							return fmt.Errorf("skill selection failed: %w. Tip: use --skill <name> or --skill \"*\" -y to skip prompt", err)
						}
						if len(chosen) == 0 {
							return fmt.Errorf("no skills selected. Tip: use --skill <name> or --skill \"*\" -y to install all. Available: %s", strings.Join(allDisplay(all), ", "))
						}
						selected = chosen
					} else {
						if viper.GetBool("yes") {
							// --yes or non-TTY with --yes: auto-select all
							selected = all
							fmt.Fprintf(os.Stderr, "\x1b[2m› auto-selecting all %d skills (--yes). Use --skill <name> to pick specific skills.\x1b[0m\n", len(all))
						} else {
							return fmt.Errorf("repo %s/%s contains %d skills; use --skill <name> (repeatable), --skill \"*\" for all, or --list to discover. Available: %s\nTip: mskill get %s/%s --skill \"*\" -y  to install all", r.Owner, r.Repo, len(all), strings.Join(allDisplay(all), ", "), r.Owner, r.Repo)
						}
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
			printStep(cmd, "Caching", r.Owner+"/"+r.Repo+" → "+strings.Join(allDisplay(selected), ", ")+"…")
			cachePath, meta, err := cache.Ensure(ctx, paths, r, effectiveRef, sparsePaths, force)
			if err != nil {
				return fmt.Errorf("Cannot cache %q at ref %q (sparse %v): %v. Tip: try --ref main or --skill \"*\" (%w)", raw, effectiveRef, sparsePaths, err, err)
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
							return fmt.Errorf("invalid --file %q: %v. Tip: file cannot contain \"..\" (%w)", f, err, err)
						}
						full := filepath.Join(baseSkillDir, sanitized)
						rel, err := filepath.Rel(baseSkillDir, full)
						if err != nil || strings.HasPrefix(rel, "..") {
							return fmt.Errorf("file %q outside skill directory (traversal not allowed)", f)
						}
						data, err := os.ReadFile(full)
						if err != nil {
							return fmt.Errorf("read %s at %q: %w. Tip: try mskill show %s --list to see files", f, full, err, raw)
						}
						cmdPrint(cmd, string(data))
						if !strings.HasSuffix(string(data), "\n") {
							cmdPrint(cmd, "\n")
						}
						if len(selected) > 1 || len(targetFiles) > 1 {
							cmdPrint(cmd, "\n---\n")
						}
					}
					// footer per skill
					footerDir := baseSkillDir
					cmdPrint(cmd, fmt.Sprintf("\n---\nskill: %s\ncached at: %s\nskill dir: %s\n", skillRel, cachePath, footerDir))
					cmdPrint(cmd, fmt.Sprintf("explore: ls %s  |  mskill show %s --list\n", footerDir, raw))
				}
				continue
			}

			// Iterate over selected skills for Link
			printStep(cmd, "Linking", fmt.Sprintf("%d skill(s) → %s…", len(selected), agentFilter))
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
									return fmt.Errorf("security gate for %q: %w", slugForAudit, err)
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
							return fmt.Errorf("skill %q not found at ref %q in %s (checked %q). Try: mskill get %s/%s --skill \"*\" --ref %q, or mskill show %s/%s --list to discover\nTip: verify skill path exists at that ref", resolve.SanitizeName(filepath.Base(skillRel)), effectiveRef, cachePath, candidate, r.Owner, r.Repo, effectiveRef, r.Owner, r.Repo)
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
					return fmt.Errorf("skill path %q not found in cache %s: %v. Tip: check cache; try --ref main or mskill show %s/%s --list (%w)", cacheSkillPath, cachePath, err, r.Owner, r.Repo, err)
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
					return fmt.Errorf("link %q to %s: %w. Tip: check destination writable or try --copy", skillName, agentFilter, err)
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
				cmdPrint(cmd, fmt.Sprintf("  cache: %s\n", cacheSkillPath))
				cmdPrint(cmd, fmt.Sprintf("  skill dir: %s\n", cacheSkillPath))
				// linked destinations
				linkedDests := link.ResolveDestinations(agentFilter, global, project)
				for _, ag := range linkedDests {
					var targets []string
					if global && !project {
						targets = append(targets, filepath.Join(expandHome(ag.GlobalDir), skillName))
					} else if project && !global {
						targets = append(targets, filepath.Join(ag.ProjectDir, skillName))
					} else {
						targets = append(targets, filepath.Join(expandHome(ag.GlobalDir), skillName))
						targets = append(targets, filepath.Join(ag.ProjectDir, skillName))
					}
					for _, t := range targets {
						cmdPrint(cmd, fmt.Sprintf("  linked: %s\n", t))
					}
				}
				cmdPrint(cmd, fmt.Sprintf("  explore: ls %s  |  mskill show %s --list\n", cacheSkillPath, raw))
			}
		}
		return nil
	},
}

// promptAgentSelection shows an interactive MultiSelect for agent destinations.
func promptAgentSelection(repoHint string) (string, error) {
	// styled header
	displayRepo := strings.TrimSpace(repoHint)
	if displayRepo == "" {
		displayRepo = "skill"
	}
	// dim handling
	dim := "\x1b[2m"
	bold := "\x1b[1m"
	reset := "\x1b[0m"
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		dim, bold, reset = "", "", ""
	}
	fmt.Fprintf(os.Stderr, "\n%sSelect install targets for %s%s\n%sGlobal = ~/.agents/skills  •  Project = ./.agents/skills  •  agents = universal%s\n", bold, displayRepo, reset, dim, reset)
	fmt.Fprintf(os.Stderr, "%sSpace to select, Enter to confirm. Project = ./.agents/skills%s\n", dim, reset)

	// priority ordering
	priority := []string{"project", "claude-code", "cursor", "opencode", "agents"}
	prioritySet := map[string]bool{}
	for _, p := range priority {
		prioritySet[p] = true
	}
	// collect keys excluding "*"
	keysSet := make(map[string]bool)
	for k := range link.Agents {
		keysSet[k] = true
	}
	// alphabetical remainder
	var rest []string
	for k := range keysSet {
		if !prioritySet[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	var ordered []string
	for _, p := range priority {
		if keysSet[p] {
			ordered = append(ordered, p)
		}
	}
	ordered = append(ordered, rest...)
	// "*" last with label
	displayOptions := make([]string, 0, len(ordered)+1)
	valueMap := map[string]string{} // display -> value
	for _, k := range ordered {
		displayOptions = append(displayOptions, k)
		valueMap[k] = k
	}
	starDisplay := "* — All agents (global + project)"
	displayOptions = append(displayOptions, starDisplay)
	valueMap[starDisplay] = "*"

	// default: ["project"] if inside project else ["agents"]
	def := []string{"project"}
	if !isProjectDirectory() {
		def = []string{"agents"}
	}
	// map def to display
	var defDisplay []string
	for _, d := range def {
		// if def is project/agents, it matches display directly
		defDisplay = append(defDisplay, d)
	}

	var selectedDisplay []string
	prompt := &survey.MultiSelect{
		Message: "Select agents to install skill to:",
		Options: displayOptions,
		Default: defDisplay,
		Help:    "Space to select, Enter to confirm. Project = ./.agents/skills  •  * = all agents",
	}
	// optional icons: use default survey icons but ensure color when possible
	if err := survey.AskOne(prompt, &selectedDisplay); err != nil {
		return "", err
	}
	if len(selectedDisplay) == 0 {
		// none selected => default to project (don't silently return def without guidance)
		fmt.Fprintf(os.Stderr, "%s› no selection — defaulting to %s%s\n", dim, strings.Join(def, ","), reset)
		return strings.Join(def, ","), nil
	}
	// map display back to values
	var selected []string
	for _, d := range selectedDisplay {
		if v, ok := valueMap[d]; ok {
			selected = append(selected, v)
		} else {
			selected = append(selected, d)
		}
	}
	for _, s := range selected {
		if s == "*" || strings.HasPrefix(s, "*") {
			return "*", nil
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range selected {
		ls := strings.ToLower(strings.TrimSpace(s))
		// strip label if any (e.g., "* — ...")
		if idx := strings.Index(ls, " —"); idx != -1 {
			ls = strings.TrimSpace(ls[:idx])
		}
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

func promptSkillSelection(repo string, skills []string) ([]string, error) {
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills to select")
	}
	// Normalize incoming: "" means root
	repoDisplay := strings.TrimSpace(repo)
	if repoDisplay == "" {
		repoDisplay = "repo"
	}
	dim := "\x1b[2m"
	bold := "\x1b[1m"
	reset := "\x1b[0m"
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		dim, bold, reset = "", "", ""
	}
	fmt.Fprintf(os.Stderr, "\n%sRepo %s contains %d skills:%s\n", bold, repoDisplay, len(skills), reset)
	for i, s := range skills {
		disp := s
		if disp == "" {
			disp = ". (root skill)"
		}
		fmt.Fprintf(os.Stderr, "%s  %d. %s%s\n", dim, i+1, disp, reset)
	}

	// Build display options: keep root "." at top labeled as "(root skill)", sort rest
	hasRoot := false
	var rest []string
	for _, s := range skills {
		if s == "" || s == "." {
			hasRoot = true
		} else {
			rest = append(rest, s)
		}
	}
	sort.Strings(rest)
	var ordered []string
	if hasRoot {
		ordered = append(ordered, "")
	}
	ordered = append(ordered, rest...)

	// Map to display strings
	displayOptions := make([]string, len(ordered))
	displayToValue := map[string]string{}
	valueToDisplay := map[string]string{}
	for i, v := range ordered {
		disp := v
		if v == "" {
			disp = ". (root skill)"
		}
		displayOptions[i] = disp
		displayToValue[disp] = v
		valueToDisplay[v] = disp
	}
	// Ensure "." input maps to root as well (normalize to "")
	for _, s := range skills {
		if s == "." {
			valueToDisplay["."] = ". (root skill)"
			displayToValue[". (root skill)"] = ""
		}
	}

	// Default selection logic
	var defDisplay []string
	if len(ordered) >= 2 && len(ordered) <= 5 {
		// 2-5 skills: default select all
		defDisplay = append([]string{}, displayOptions...)
	} else if len(ordered) > 5 {
		// >5: default none
		defDisplay = []string{}
	} else {
		// single already handled, but keep
		defDisplay = displayOptions
	}

	if len(ordered) == 1 {
		// single skill confirm via Select for UX but keep MultiSelect semantics
		var oneDisplay string
		prompt := &survey.Select{
			Message: "Select skill to install:",
			Options: displayOptions,
			Default: displayOptions[0],
			Help:    "Enter to confirm. Use --skill NAME to skip this prompt next time.",
		}
		if err := survey.AskOne(prompt, &oneDisplay); err != nil {
			return nil, err
		}
		val := displayToValue[oneDisplay]
		if val == "" && oneDisplay == ". (root skill)" {
			val = ""
		}
		// normalize "." to "" for internal
		if val == "." {
			val = ""
		}
		return []string{val}, nil
	}
	// multiple → MultiSelect; use survey.MultiSelect per spec requirement
	var selectedDisplay []string
	prompt := &survey.MultiSelect{
		Message: "Select skills to install:",
		Options: displayOptions,
		Default: defDisplay,
		Help:    "Space selects, a toggles all. Use --skill NAME to skip this prompt next time.",
	}
	if err := survey.AskOne(prompt, &selectedDisplay); err != nil {
		return nil, err
	}
	// map back
	var out []string
	for _, d := range selectedDisplay {
		if v, ok := displayToValue[d]; ok {
			if v == "." {
				v = ""
			}
			out = append(out, v)
		} else {
			// fallback: if d is ". (root skill)" handle
			if d == ". (root skill)" {
				out = append(out, "")
			} else {
				out = append(out, d)
			}
		}
	}
	// If none selected, return empty (caller will error) but for 2-5 we defaulted to all, so empty is intentional for >5
	return out, nil
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
