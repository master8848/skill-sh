package cmd

import "github.com/spf13/cobra"

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List installed skills",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolP("global", "g", false, "only global installs")
	listCmd.Flags().StringP("agent", "a", "", "filter by agent")
}
