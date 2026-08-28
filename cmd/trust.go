package cmd

import (
	"github.com/spf13/cobra"
)

var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Manage trust for risky skill installs",
	Long:  "Human-gated trust: enable/disable/status/reset. Requires interactive TTY; agents cannot flip trust.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var trustEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable trust (human TTY required)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var trustDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable trust",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var trustStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show trust status",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var trustResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset trust and password",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(trustCmd)
	trustCmd.AddCommand(trustEnableCmd)
	trustCmd.AddCommand(trustDisableCmd)
	trustCmd.AddCommand(trustStatusCmd)
	trustCmd.AddCommand(trustResetCmd)
}
