package cmd

import "github.com/spf13/cobra"

var removeCmd = &cobra.Command{
	Use:     "remove [skills...]",
	Aliases: []string{"rm"},
	Short:   "Remove installed skills",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().BoolP("global", "g", false, "only global")
	removeCmd.Flags().StringP("agent", "a", "", "filter by agent")
}
