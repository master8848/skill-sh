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

func normalizeColonRef(raw string) string {
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
	// replace first ":" with "/"
	return prefix + "/" + raw[idx+1:]
}

var showCmd = &cobra.Command{
	Use:     "show [skill]",
	Aliases: []string{"cat"},
	Short:   "Show cached skill without linking",
	Long:    "Resolve → Cache → cat skill contents. Same security gate as get, no link step. Supports --file and --list for auxiliary files.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
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
			return fmt.Errorf("resolve %q: %w", raw, err)
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
					return fmt.Errorf("local skill %q not found (checked %q)", r.Slug, r.CloneURL)
				}
			} else if _, err := os.Stat(filepath.Join(localPath, "SKILL.md")); err != nil && r.Slug != "" {
				if discovered := discoverSkillInCache(localPath, r.Slug); discovered != "" {
					localPath = discovered
				}
			}
			if list {
				entries, err := os.ReadDir(localPath)
				if err != nil {
					return err
				}
				for _, e := range entries {
					cmdPrint(cmd, e.Name()+"\n")
				}
				return nil
			}
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
				rel, err := filepath.Rel(localPath, full)
				if err != nil || strings.HasPrefix(rel, "..") {
					return fmt.Errorf("file %q outside skill directory", f)
				}
				data, err := os.ReadFile(full)
				if err != nil {
					return fmt.Errorf("read %s: %w", f, err)
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
							return err
						}
					}
				}
			}
		}

		cachePath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, skillPaths, force)
		if err != nil {
			return fmt.Errorf("cache %q: %w", raw, err)
		}

		baseSkillDir := cachePath
		if r.SkillPath != "" {
			candidate := filepath.Join(cachePath, r.SkillPath)
			if _, err := os.Stat(candidate); err == nil {
				baseSkillDir = candidate
			} else {
				// nested skill like engineering/skills/documentation with slug "documentation"
				if discovered := discoverSkillInCache(cachePath, filepath.Base(r.SkillPath)); discovered != "" {
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
			if _, err := os.Stat(candidate); err == nil {
				baseSkillDir = candidate
			} else {
				if discovered := discoverSkillInCache(cachePath, r.Slug); discovered != "" {
					baseSkillDir = discovered
				} else if gitPath := discoverSkillViaGit(ctx, cachePath, r.Slug); gitPath != "" {
					baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
				}
			}
		}
		// If baseSkillDir still missing or lacking SKILL.md, try any discovery
		if _, err := os.Stat(filepath.Join(baseSkillDir, "SKILL.md")); err != nil {
			if discovered := discoverSkillInCache(cachePath, r.Slug); discovered != "" {
				baseSkillDir = discovered
			} else if r.Slug != "" {
				if gitPath := discoverSkillViaGit(ctx, cachePath, r.Slug); gitPath != "" {
					baseSkillDir = resolveShowSkillViaGit(ctx, cachePath, gitPath)
				}
			}
			if _, err := os.Stat(filepath.Join(baseSkillDir, "SKILL.md")); err != nil {
				if r.SkillPath != "" {
					return fmt.Errorf("skill %q not found at ref %q in %s (checked %q). Try mskill get %s/%s --skill * or verify skill path", r.Slug, effectiveRef, cachePath, baseSkillDir, r.Owner, r.Repo)
				}
			}
		}

		if list {
			entries, err := os.ReadDir(baseSkillDir)
			if err != nil {
				return err
			}
			for _, e := range entries {
				cmdPrint(cmd, e.Name()+"\n")
			}
			return nil
		}

		targetFiles := files
		if len(targetFiles) == 0 {
			targetFiles = []string{"SKILL.md"}
		}
		for _, f := range targetFiles {
			sanitized, err := resolve.SanitizeSubpath(f)
			if err != nil {
				return fmt.Errorf("invalid --file %q: %w", f, err)
			}
			full := filepath.Join(baseSkillDir, sanitized)
			rel, err := filepath.Rel(baseSkillDir, full)
			if err != nil || strings.HasPrefix(rel, "..") || strings.Contains(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("file %q outside skill directory", f)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return fmt.Errorf("read %s: %w", f, err)
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
}
