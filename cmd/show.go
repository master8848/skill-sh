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
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/security"
)

var showCmd = &cobra.Command{
	Use:     "show [skill]",
	Aliases: []string{"cat"},
	Short:   "Show cached skill without linking",
	Long:    "Resolve → Cache → cat skill contents. Same security gate as get, no link step. Supports --file and --list for auxiliary files.",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		raw := args[0]
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
				cmdPrint(cmd, string(data))
				if !strings.HasSuffix(string(data), "\n") {
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
			baseSkillDir = filepath.Join(cachePath, r.SkillPath)
		}
		if _, err := os.Stat(baseSkillDir); err != nil {
			baseSkillDir = cachePath
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
			cmdPrint(cmd, string(data))
			if !strings.HasSuffix(string(data), "\n") {
				cmdPrint(cmd, "\n")
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
	showCmd.Flags().StringArray("file", []string{"SKILL.md"}, "file(s) to print (repeatable, default SKILL.md)")
	showCmd.Flags().Bool("list", false, "list files in skill instead of printing")
	showCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	showCmd.Flags().Bool("force", false, "force cache refresh")
}
