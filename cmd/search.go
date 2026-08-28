package cmd

import (
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:     "search [query]",
	Aliases: []string{"find"},
	Short:   "Search skills with SAFE/UNSAFE/UNKNOWN and topic/official filters",
	Long:    "Search skills.sh with compact TSV output. Web parity: --topic mirrors https://www.skills.sh/topic and --official mirrors https://www.skills.sh/official.",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().String("topic", "", "filter by topic (react|nextjs|design|mobile|agent-workflows|databases|testing|marketing|all)")
	searchCmd.Flags().Bool("official", false, "only official skills (https://www.skills.sh/official)")
	searchCmd.Flags().String("owner", "", "filter by owner")
	searchCmd.Flags().Int("limit", 20, "max results")
	searchCmd.Flags().Bool("header", false, "print TSV header")
}
