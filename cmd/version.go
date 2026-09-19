package cmd

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Long:  "Print mskill version. Also available as --version / -v (root flag).",
		Example: `  mskill version
  mskill --version
  mskill -v`,
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Println("mskill", version)
		},
	})
}
