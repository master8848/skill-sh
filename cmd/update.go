package cmd

import "github.com/spf13/cobra"

var updateCmd = &cobra.Command{
	Use:     "update [skills...]",
	Aliases: []string{"upgrade"},
	Short:   "Update skills to latest (re-fetch + re-link)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolP("global", "g", false, "only global")
	updateCmd.Flags().BoolP("project", "p", false, "only project")
	updateCmd.Flags().BoolP("yes", "y", false, "skip confirm")
	updateCmd.Flags().Bool("force", false, "force re-fetch even if tag SHA matches")
}
