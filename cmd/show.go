package cmd

import (
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:     "show [skill]",
	Aliases: []string{"cat"},
	Short:   "Show cached skill without linking",
	Long:    "Resolve → Cache → cat skill contents. Same security gate as get, no link step. Supports --file and --list for auxiliary files.",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
	showCmd.Flags().StringArray("file", []string{"SKILL.md"}, "file(s) to print (repeatable, default SKILL.md)")
	showCmd.Flags().Bool("list", false, "list files in skill instead of printing")
	showCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	showCmd.Flags().Bool("force", false, "force cache refresh")
}
