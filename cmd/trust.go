package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/security"
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
		if err := security.EnableTrust(paths); err != nil {
			return err
		}
		cmd.Println("trust enabled")
		return nil
	},
}

var trustDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable trust",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := security.DisableTrust(paths); err != nil {
			return err
		}
		cmd.Println("trust disabled")
		return nil
	},
}

var trustStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show trust status",
	RunE: func(cmd *cobra.Command, args []string) error {
		s := security.Status(paths)
		trustStr := "disabled"
		if s.TrustEnabled {
			trustStr = "enabled"
		}
		pwStr := "not set"
		if s.PasswordSet {
			pwStr = "set"
		}
		cmd.Printf("Trust: %s\nPassword: %s\n", trustStr, pwStr)
		if s.TrustAt != "" {
			cmd.Printf("Trust enabled at: %s\n", s.TrustAt)
		}
		if s.PasswordAt != "" {
			cmd.Printf("Password set at: %s\n", s.PasswordAt)
		}
		// never print hash
		return nil
	},
}

var trustResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset trust and password",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := security.ResetTrust(paths); err != nil {
			return err
		}
		cmd.Println("trust reset")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(trustCmd)
	trustCmd.AddCommand(trustEnableCmd)
	trustCmd.AddCommand(trustDisableCmd)
	trustCmd.AddCommand(trustStatusCmd)
	trustCmd.AddCommand(trustResetCmd)
	// Ensure no --yes bypass: we don't add --yes flag to trust subcommands, but global --yes via viper is checked inside security
	_ = fmt.Sprintf
}
