package cmd

import (
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:     "get [skill]",
	Aliases: []string{"add", "a"},
	Short:   "Fetch, store and link a skill",
	Long:    "Resolve a skill reference, cache it with sparse git, and link into agent directories.",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(getCmd)
	getCmd.Flags().BoolP("global", "g", false, "install to global (~/.agents/skills)")
	getCmd.Flags().BoolP("project", "p", false, "install to project (./.agents/skills)")
	getCmd.Flags().StringP("agent", "a", "", "comma-separated agents or * for all")
	getCmd.Flags().StringP("skill", "s", "", "filter skills from repo (repeatable, * = all)")
	getCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	getCmd.Flags().Bool("copy", false, "copy files instead of symlink")
	getCmd.Flags().String("link-mode", "", "link mode: auto|symlink|copy")
	getCmd.Flags().Bool("show", false, "show skill contents without linking (Resolve→Cache→cat)")
	getCmd.Flags().StringArray("file", []string{}, "with --show: file(s) to print (repeatable, default SKILL.md)")
	getCmd.Flags().BoolP("yes", "y", false, "skip confirmation (risky still fails closed)")
	getCmd.Flags().Bool("force", false, "force refetch even if cache is fresh")
	getCmd.Flags().Bool("full-depth", false, "recursive skill discovery depth 5")
}
