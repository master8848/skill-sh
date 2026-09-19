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
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/security"
)

// stripYAMLFrontmatter removes YAML frontmatter delimited by --- at start of file.
// If file starts with "---\n", it strips until next "---\n" (or "...\n") and returns body trimmed left.
func stripYAMLFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	// Normalize line endings and check first line is ---
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return s
	}
	// Find closing ---
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			// Body is after closing line
			body := strings.Join(lines[i+1:], "\n")
			// Trim single leading newline but preserve body structure
			return strings.TrimLeft(body, "\n")
		}
	}
	return s
}

var showCmd = &cobra.Command{
	Use:     "show [skill]",
	Aliases: []string{"cat"},
	Short:   "Show cached skill without linking",
	Long: `Resolve → Cache → cat — view a skill without linking.

Same Resolve → Cache path as 'get' (sparse git + audit gate), but stops
before Link and prints files to stdout. Useful for inspection.

Supports --file for auxiliary files and --list to explore.`,
	Example: `  mskill show master8848/Anki-import --skill anki-import-cli
  mskill show master8848/Anki-import --skill anki-import-cli --file README.md --file scripts/setup.sh
  mskill show master8848/Anki-import --skill anki-import-cli --list
  mskill show vercel-labs/agent-skills/vercel-optimize --ref v1.2.0 --file SKILL.md`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		// support --skill filter like `get` for UX: `show owner/repo --skill docs`
		var skillFilters []string
		if vals, err := cmd.Flags().GetStringArray("skill"); err == nil && len(vals) > 0 {
			for _, v := range vals {
				if strings.TrimSpace(v) != "" {
					skillFilters = append(skillFilters, strings.TrimSpace(v))
				}
			}
		}
		if len(skillFilters) == 0 {
			if v, err := cmd.Flags().GetString("skill-compat"); err == nil && strings.TrimSpace(v) != "" {
				for _, part := range strings.Split(v, ",") {
					if s := strings.TrimSpace(part); s != "" && s != "skill" {
						skillFilters = append(skillFilters, s)
					}
				}
			}
		}
		rawInput := normalizeColonRef(args[0])
		// If --skill provided and raw is repo only, expand to repo/skill (like get)
		if len(skillFilters) == 1 && skillFilters[0] != "*" {
			// Normalize Windows separator for filter
			skillFilters[0] = strings.ReplaceAll(skillFilters[0], "\\", "/")
			// Check if rawInput already contains skill path beyond owner/repo
			// If user did `show owner/repo --skill docs`, rawInput is owner/repo, expand
			tmpR, err := resolve.ParseSkillRef(rawInput)
			if err == nil && tmpR.SkillPath == "" && tmpR.Slug == "" {
				rawInput = strings.TrimSuffix(rawInput, "/") + "/" + strings.TrimPrefix(skillFilters[0], "/")
			} else if err == nil && tmpR.SkillPath != "" {
				// already has skill path, keep
			} else if tmpR == nil {
				rawInput = strings.TrimSuffix(rawInput, "/") + "/" + strings.TrimPrefix(skillFilters[0], "/")
			}
		}
		raw := rawInput
		files, _ := cmd.Flags().GetStringArray("file")
		list, _ := cmd.Flags().GetBool("list")
		refFlag, _ := cmd.Flags().GetString("ref")
		force, _ := cmd.Flags().GetBool("force")
		verbose := viper.GetBool("verbose")

		r, err := resolve.ParseSkillRef(raw)
		if err != nil {
			if strings.Contains(err.Error(), "empty skill reference") {
				return fmt.Errorf("Cannot resolve %q: empty skill reference. Try: mskill show owner/repo/skill or mskill show owner/repo --skill <name> --list (%w)", raw, err)
			}
			if strings.Contains(err.Error(), "invalid subpath") {
				return fmt.Errorf("Cannot resolve %q: %v. Tip: subpath cannot contain \"..\" (%w)", raw, err, err)
			}
			return fmt.Errorf("Cannot resolve %q: %v. Try: mskill show owner/repo --help or mskill show owner/repo --list\nTip: check spelling (%w)", raw, err, err)
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "resolve: %+v\n", r)
		}
		if r.IsLocal {
			localPath := r.CloneURL
			// discovery for nested local skill (e.g., repo has skills/docs but user passed repo/docs top-level missing)
			if _, err := os.Stat(localPath); err != nil && r.Slug != "" {
				base := filepath.Dir(strings.TrimSuffix(localPath, "/"))
				if discovered := discoverSkillInCache(base, r.Slug); discovered != "" {
					localPath = discovered
				} else if discovered := discoverSkillInCache(base, filepath.Base(localPath)); discovered != "" {
					localPath = discovered
				} else {
					return fmt.Errorf("local skill %q not found (checked %q). Tip: mskill show %q --list to see available or check path exists", r.Slug, r.CloneURL, base)
				}
			} else if _, err := os.Stat(filepath.Join(localPath, "SKILL.md")); err != nil && r.Slug != "" {
				if discovered := discoverSkillInCache(localPath, r.Slug); discovered != "" {
					localPath = discovered
				}
			}
			if list {
				entries, err := os.ReadDir(localPath)
				if err != nil {
					return fmt.Errorf("list %q: %w. Tip: check path exists or try mskill show %s --list", localPath, err, r.CloneURL)
				}
				for _, e := range entries {
					cmdPrint(cmd, e.Name()+"\n")
				}
				cmdPrint(cmd, fmt.Sprintf("\n---\nskill dir: %s\n", localPath))
				return nil
			}
			targetFiles := files
			if len(targetFiles) == 0 {
				targetFiles = []string{"SKILL.md"}
			}
			for _, f := range targetFiles {
				f = strings.ReplaceAll(f, "\\", "/")
				sanitized, err := resolve.SanitizeSubpath(f)
				if err != nil {
					return fmt.Errorf("invalid --file %q: %v. Tip: file cannot contain \"..\" (%w)", f, err, err)
				}
				full := filepath.Join(localPath, sanitized)
				rel, err := filepath.Rel(localPath, full)
				if err != nil || strings.HasPrefix(rel, "..") {
					return fmt.Errorf("file %q outside skill directory (traversal not allowed)", f)
				}
				data, err := os.ReadFile(full)
				if err != nil {
					return fmt.Errorf("read %s at %q: %w. Tip: try mskill show %s --list to see files", f, full, err, localPath)
				}
				out := string(data)
				if strings.EqualFold(filepath.Base(sanitized), "SKILL.md") {
					out = stripYAMLFrontmatter(out)
				}
				cmdPrint(cmd, out)
				if !strings.HasSuffix(out, "\n") {
					cmdPrint(cmd, "\n")
				}
			}
			cmdPrint(cmd, fmt.Sprintf("\n---\nskill dir: %s\n", localPath))
			return nil
		}

		effectiveRef := refFlag
		if effectiveRef == "" {
			effectiveRef = r.Ref
		}
		var skillPaths []string
		if r.SkillPath != "" {
			skillPaths = []string{r.SkillPath}
		} else if r.Slug != "" {
			skillPaths = []string{r.Slug}
		}

		// Security gate same as get
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
					if !security.IsTrustEnabled(paths) {
						if err := security.RequirePassword(slugForAudit, v.Reason, paths); err != nil {
							return fmt.Errorf("security gate for %q: %w", slugForAudit, err)
						}
					}
				}
			}
		}

		cachePath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, skillPaths, force)
		if err != nil {
			return fmt.Errorf("Cannot cache %q at ref %q: %v. Tip: try --ref main or check repo exists and --ref valid (%w)", raw, effectiveRef, err, err)
		}

		// Ensure sparse checkout is materialized for a given skill rel path.
		ensureSkillMaterialized := func(rel string) {
			if rel == "" {
				return
			}
			full := filepath.Join(cachePath, rel)
			if hasSkillMD(full) {
				return
			}
			// Try to materialize via Ensure with that sparse path; ignore error fallback to git sparse-checkout
			if np, _, err := cache.Ensure(ctx, paths, r, effectiveRef, []string{rel}, force); err == nil {
				cachePath = np
				return
			}
			// Fallback: try git sparse-checkout directly
			if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(rel)); gitPath != "" {
				_ = resolveShowSkillViaGit(ctx, cachePath, gitPath)
			} else {
				_, _ = git.Run(ctx, cachePath, "sparse-checkout", "set", "--cone", rel)
			}
		}

		baseSkillDir := cachePath
		// Repo-level reference without explicit skill: discover and handle gracefully
		if r.SkillPath == "" && r.Slug == "" {
			// If root contains SKILL.md, treat as single root skill
			if hasSkillMD(cachePath) {
				baseSkillDir = cachePath
			} else {
				skills, _ := cache.ListSkills(ctx, cachePath)
				// If cache is broken/empty (no skills but repo should have skills), try force refresh once: ensure with nil sparse paths to repopulate tree
				if len(skills) == 0 {
					if np, _, ferr := cache.Ensure(ctx, paths, r, effectiveRef, nil, true); ferr == nil {
						cachePath = np
						skills, _ = cache.ListSkills(ctx, cachePath)
					}
				}
				if len(skills) == 0 {
					return fmt.Errorf("no skills found in %s/%s (missing SKILL.md). Try: mskill show %s/%s --skill <name> or --list\nTip: verify repo contains SKILL.md or check --ref", r.Owner, r.Repo, r.Owner, r.Repo)
				}
				if len(skills) == 1 {
					sel := skills[0]
					if sel == "" {
						baseSkillDir = cachePath
					} else {
						ensureSkillMaterialized(sel)
						candidate := filepath.Join(cachePath, sel)
						if hasSkillMD(candidate) {
							baseSkillDir = candidate
						} else if discovered := discoverSkillInCache(cachePath, filepath.Base(sel)); discovered != "" {
							baseSkillDir = discovered
						} else if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(sel)); gitPath != "" {
							baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
						} else {
							baseSkillDir = candidate
						}
					}
				} else {
					// Multiple skills: if --list, show available skills instead of error
					if list {
						for _, s := range skills {
							disp := s
							if disp == "" {
								disp = "."
							}
							cmdPrint(cmd, disp+"\n")
						}
						cmdPrint(cmd, fmt.Sprintf("\n---\ncached at: %s\n", cachePath))
						return nil
					}
					// Multiple skills: require explicit --skill - do NOT try to read SKILL.md from cachePath root
					display := allDisplay(skills)
					return fmt.Errorf("repo %s/%s contains %d skills; use --skill <name> (repeatable), --skill \"*\" for all, or --list to discover. Available: %s\nTip: mskill show %s/%s --skill \"*\" --file SKILL.md to see all, or mskill get %s/%s --skill \"*\" -y to install\nExample: mskill show %s/%s --skill %s --file SKILL.md", r.Owner, r.Repo, len(skills), strings.Join(display, ", "), r.Owner, r.Repo, r.Owner, r.Repo, r.Owner, r.Repo, display[0])
				}
			}
		} else if r.SkillPath != "" {
			candidate := filepath.Join(cachePath, r.SkillPath)
			if _, err := os.Stat(candidate); err == nil && hasSkillMD(candidate) {
				baseSkillDir = candidate
			} else {
				// Ensure materialized if missing
				ensureSkillMaterialized(r.SkillPath)
				if _, err := os.Stat(candidate); err == nil && hasSkillMD(candidate) {
					baseSkillDir = candidate
				} else if discovered := discoverSkillInCache(cachePath, filepath.Base(r.SkillPath)); discovered != "" {
					baseSkillDir = discovered
				} else if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(r.SkillPath)); gitPath != "" {
					baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
				} else {
					// keep candidate check; will error below if still missing SKILL.md
					baseSkillDir = candidate
				}
			}
		} else if r.Slug != "" {
			candidate := filepath.Join(cachePath, r.Slug)
			if _, err := os.Stat(candidate); err == nil && hasSkillMD(candidate) {
				baseSkillDir = candidate
			} else {
				ensureSkillMaterialized(r.Slug)
				if _, err := os.Stat(candidate); err == nil && hasSkillMD(candidate) {
					baseSkillDir = candidate
				} else if discovered := discoverSkillInCache(cachePath, r.Slug); discovered != "" {
					baseSkillDir = discovered
				} else if gitPath := discoverSkillViaGit(ctx, cachePath, r.Slug); gitPath != "" {
					baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
				}
			}
		}
		// If baseSkillDir still missing or lacking SKILL.md, try any discovery
		if !hasSkillMD(baseSkillDir) {
			if r.Slug != "" {
				if discovered := discoverSkillInCache(cachePath, r.Slug); discovered != "" {
					baseSkillDir = discovered
				} else if gitPath := discoverSkillViaGit(ctx, cachePath, r.Slug); gitPath != "" {
					baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
				}
			} else if r.SkillPath != "" {
				if discovered := discoverSkillInCache(cachePath, filepath.Base(r.SkillPath)); discovered != "" {
					baseSkillDir = discovered
				} else if gitPath := discoverSkillViaGit(ctx, cachePath, filepath.Base(r.SkillPath)); gitPath != "" {
					baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
				}
			} else {
				// repo-level single skill fallback: discover any
				if discovered := discoverSkillInCache(cachePath, ""); discovered != "" && hasSkillMD(discovered) {
					baseSkillDir = discovered
				} else if any := discoverAnySkill(cachePath); any != "" {
					baseSkillDir = any
				}
			}
			if !hasSkillMD(baseSkillDir) {
				if r.SkillPath != "" {
					return fmt.Errorf("skill %q not found at ref %q in %s (checked %q). Try: mskill show %s/%s --skill \"*\" --list to discover, or mskill get %s/%s --skill \"*\" --ref %q\nTip: verify skill path exists at that ref", r.Slug, effectiveRef, cachePath, baseSkillDir, r.Owner, r.Repo, r.Owner, r.Repo, effectiveRef)
				}
				if r.Slug == "" && r.SkillPath == "" {
					// repo-level already handled; but if still missing, provide generic
					return fmt.Errorf("skill not found at ref %q in %s (checked %q). Try: mskill show %s/%s --list or mskill get %s/%s --skill \"*\" --ref %q", effectiveRef, cachePath, baseSkillDir, r.Owner, r.Repo, r.Owner, r.Repo, effectiveRef)
				}
			}
		}

		if list {
			entries, err := os.ReadDir(baseSkillDir)
			if err != nil {
				return fmt.Errorf("list %q: %w. Tip: check path exists or try mskill show %s --list", baseSkillDir, err, raw)
			}
			for _, e := range entries {
				cmdPrint(cmd, e.Name()+"\n")
			}
			// footer: show where skill lives (cache + skill dir), useful when skill has other files
			cmdPrint(cmd, fmt.Sprintf("\n---\nskill: %s\ncached at: %s\nskill dir: %s\nlist: ls %s\n", slugForAudit, cachePath, baseSkillDir, baseSkillDir))
			return nil
		}

		targetFiles := files
		if len(targetFiles) == 0 {
			targetFiles = []string{"SKILL.md"}
		}
		for _, f := range targetFiles {
			f = strings.ReplaceAll(f, "\\", "/")
			sanitized, err := resolve.SanitizeSubpath(f)
			if err != nil {
				return fmt.Errorf("invalid --file %q: %v. Tip: file cannot contain \"..\" (%w)", f, err, err)
			}
			full := filepath.Join(baseSkillDir, sanitized)
			rel, err := filepath.Rel(baseSkillDir, full)
			if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("file %q outside skill directory (traversal not allowed)", f)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return fmt.Errorf("read %s at %q: %w. Tip: try mskill show %s --list to see files", f, full, err, raw)
			}
			out := string(data)
			if strings.EqualFold(filepath.Base(sanitized), "SKILL.md") {
				out = stripYAMLFrontmatter(out)
			}
			cmdPrint(cmd, out)
			if !strings.HasSuffix(out, "\n") {
				cmdPrint(cmd, "\n")
			}
		}
		// footer: location where skill is cached/installed (helps explore other files)
		cmdPrint(cmd, fmt.Sprintf("\n---\nskill: %s\ncached at: %s\nskill dir: %s\n", slugForAudit, cachePath, baseSkillDir))
		if verbose {
			cmdPrint(cmd, fmt.Sprintf("ref: %s\n", effectiveRef))
		}
		cmdPrint(cmd, fmt.Sprintf("explore: ls %s  |  mskill show %s --list\n", baseSkillDir, raw))
		return nil
	},
}

func resolveShowSkillViaGit(ctx context.Context, cachePath, gitPath string) string {
	full := filepath.Join(cachePath, gitPath)
	if _, err := git.Run(ctx, cachePath, "sparse-checkout", "set", "--cone", gitPath); err == nil {
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	if _, err := git.Run(ctx, cachePath, "sparse-checkout", "set", "--no-cone", gitPath); err == nil {
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	_, _ = git.Run(ctx, cachePath, "sparse-checkout", "disable")
	_, _ = git.Run(ctx, cachePath, "read-tree", "-mu", "HEAD")
	if _, err := os.Stat(full); err == nil {
		return full
	}
	return full
}

func init() {
	rootCmd.AddCommand(showCmd)
	showCmd.Flags().StringArray("file", []string{}, "file(s) to print (repeatable, default SKILL.md)")
	showCmd.Flags().Bool("list", false, "list files in skill instead of printing")
	showCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	showCmd.Flags().Bool("force", false, "force cache refresh")
	showCmd.Flags().StringArray("skill", []string{}, "filter skill from repo (for show owner/repo --skill name)")
	showCmd.Flags().String("skill-compat", "", "compat single skill filter")
	_ = showCmd.Flags().MarkHidden("skill-compat")
}
