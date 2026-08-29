package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/link"
	"skill.sh/mskill/internal/resolve"
)

var updateCmd = &cobra.Command{
	Use:     "update [skills...]",
	Aliases: []string{"upgrade"},
	Short:   "Update skills to latest (re-fetch + re-link)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		globalOnly, _ := cmd.Flags().GetBool("global")
		projectOnly, _ := cmd.Flags().GetBool("project")
		force, _ := cmd.Flags().GetBool("force")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		if yesFlag {
			viper.Set("yes", true)
		}
		_ = globalOnly
		_ = projectOnly

		// If no args, try to list installed and update all
		if len(args) == 0 {
			// For simplicity, report help
			cmd.Println("update: no skills specified, use mskill update <skill> or reinstall via mskill get --force")
			return nil
		}
		for _, raw := range args {
			r, err := resolve.ParseSkillRef(raw)
			if err != nil {
				// try treat as skill name alone: need to discover owner/repo? fallback to searching installed dirs
				// For now error
				return fmt.Errorf("resolve %q: %w", raw, err)
			}
			if r.Owner == "" || r.Repo == "" {
				// Maybe user passed just skill slug, we can't resolve without owner/repo; error
				return fmt.Errorf("update requires fully qualified owner/repo/skill, got %q", raw)
			}
			ref := r.Ref
			var skillPaths []string
			if r.SkillPath != "" {
				skillPaths = []string{r.SkillPath}
			}
			cachePath, _, err := cache.Ensure(ctx, paths, r, ref, skillPaths, true)
			if err != nil {
				return fmt.Errorf("update fetch %q: %w", raw, err)
			}
			// Re-link
			cacheSkillPath := cachePath
			if r.SkillPath != "" {
				cacheSkillPath = filepath.Join(cachePath, r.SkillPath)
			}
			if _, err := os.Stat(cacheSkillPath); err != nil {
				cacheSkillPath = cachePath
			}
			skillName := r.Slug
			if skillName == "" {
				skillName = filepath.Base(r.SkillPath)
			}
			if skillName == "" {
				skillName = r.Repo
			}
			// Determine link opts: respect global/project flags
			opts := link.InstallOpts{Global: globalOnly, Project: projectOnly, Copy: false}
			// force is honored via cache Ensure already
			_ = force
			if err := link.Install(cacheSkillPath, skillName, opts); err != nil {
				return err
			}
			cmd.Printf("updated %s (%s)\n", raw, cachePath)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolP("global", "g", false, "only global")
	updateCmd.Flags().BoolP("project", "p", false, "only project")
	updateCmd.Flags().BoolP("yes", "y", false, "skip confirm")
	updateCmd.Flags().Bool("force", false, "force re-fetch even if tag SHA matches")
}
