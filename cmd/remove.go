package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/link"
)

var removeCmd = &cobra.Command{
	Use:     "remove [skills...]",
	Aliases: []string{"rm"},
	Short:   "Remove installed skills",
	Long:    "Remove linked skills from agent directories (global and/or project). Operates on symlinks/copies created by 'get'.",
	Example: `  mskill remove anki-import-cli
  mskill remove --global anki-import-cli
  mskill remove --agent project anki-import-cli
  mskill remove skill-a skill-b --global`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		globalOnly, _ := cmd.Flags().GetBool("global")
		agentFilter, _ := cmd.Flags().GetString("agent")

		agents := link.ResolveDestinations(agentFilter, globalOnly, false)
		for _, skill := range args {
			sanitized := link.SanitizeName(skill)
			if sanitized == "" {
				return fmt.Errorf("invalid skill name %q", skill)
			}
			found := false
			for _, ag := range agents {
				var dirs []string
				if globalOnly {
					dirs = []string{expandHome(ag.GlobalDir)}
				} else {
					dirs = []string{expandHome(ag.GlobalDir), ag.ProjectDir}
				}
				for _, base := range dirs {
					check := base
					if !filepath.IsAbs(base) && !strings.HasPrefix(base, "~/") {
						wd, _ := os.Getwd()
						if !filepath.IsAbs(base) {
							check = filepath.Join(wd, base)
						}
					}
					// expandHome already handled for global, but project is relative
					if strings.HasPrefix(base, "~/") {
						check = expandHome(base)
					}
					target := filepath.Join(check, sanitized)
					if _, err := os.Lstat(target); err == nil {
						if err := os.RemoveAll(target); err != nil {
							return fmt.Errorf("remove %s: %w", target, err)
						}
						cmd.Printf("removed %s (%s)\n", sanitized, target)
						found = true
					}
				}
			}
			if !found {
				cmd.Printf("skill not found: %s\n", sanitized)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().BoolP("global", "g", false, "only global")
	removeCmd.Flags().StringP("agent", "a", "", "filter by agent")
}
