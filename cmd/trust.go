package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/security"
)

var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Manage trust for risky skill installs",
	Long:  "Human-gated trust: enable/disable/status/reset/list/add/remove. Requires interactive TTY; agents cannot flip trust.",
	Example: `  mskill trust status
  mskill trust list
  mskill trust add my-skill
  mskill trust remove my-skill`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var trustEnableCmd = &cobra.Command{
	Use:     "enable",
	Short:   "Enable trust (human TTY required)",
	Long:    "Enable trust to allow installing UNSAFE/UNKNOWN skills without per-skill password prompt. Requires interactive TTY.",
	Example: `  mskill trust enable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := security.EnableTrust(paths); err != nil {
			return fmt.Errorf("enable trust failed: %w. Tip: requires TTY and not in agent env; try in a terminal", err)
		}
		cmd.Println("trust enabled")
		return nil
	},
}

var trustDisableCmd = &cobra.Command{
	Use:     "disable",
	Short:   "Disable trust",
	Long:    "Disable trust — future risky installs will require password again.",
	Example: `  mskill trust disable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := security.DisableTrust(paths); err != nil {
			return fmt.Errorf("disable trust failed: %w. Tip: check trust file writable (%s)", err, paths.TrustFile)
		}
		cmd.Println("trust disabled")
		return nil
	},
}

var trustStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show trust status",
	Long:    "Show whether trust is enabled and whether a password is set (never prints the hash).",
	Example: `  mskill trust status`,
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
		if len(s.TrustedSkills) > 0 {
			cmd.Printf("Trusted skills (%d): %s\n", len(s.TrustedSkills), strings.Join(s.TrustedSkills, ", "))
		}
		// never print hash
		return nil
	},
}

var trustResetCmd = &cobra.Command{
	Use:     "reset",
	Short:   "Reset trust and password",
	Long:    "Delete trust flag and password hash (requires TTY + typing RESET). Irreversible — bcrypt hash cannot be recovered.",
	Example: `  mskill trust reset`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := security.ResetTrust(paths); err != nil {
			return fmt.Errorf("reset trust failed: %w. Tip: check trust file writable (%s)", err, paths.TrustFile)
		}
		cmd.Println("trust reset")
		return nil
	},
}

var trustListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List per-skill trusted slugs",
	Long:    "List skills trusted individually via 'trust add'. These bypass the password gate for that slug only.",
	Example: `  mskill trust list`,
	RunE: func(cmd *cobra.Command, args []string) error {
		skills := security.ListTrustedSkills()
		if len(skills) == 0 {
			cmd.Println("no trusted skills")
			return nil
		}
		for _, s := range skills {
			cmd.Println(s)
		}
		return nil
	},
}

var trustAddCmd = &cobra.Command{
	Use:   "add [skills...]",
	Short: "Trust one or more skill slugs",
	Long:  "Add skill slugs to the per-skill trusted list. Trusted slugs skip the password gate.",
	Example: `  mskill trust add my-skill
  mskill trust add skill-a skill-b`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, a := range args {
			if err := security.TrustSkill(a, paths); err != nil {
				return fmt.Errorf("trust add %q failed: %w", a, err)
			}
			cmd.Printf("trusted %s\n", a)
		}
		return nil
	},
}

var trustRemoveCmd = &cobra.Command{
	Use:     "remove [skills...]",
	Aliases: []string{"rm"},
	Short:   "Untrust one or more skill slugs",
	Long:    "Remove skill slugs from the per-skill trusted list.",
	Example: `  mskill trust remove my-skill`,
	Args:    cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, a := range args {
			if err := security.UntrustSkill(a, paths); err != nil {
				return fmt.Errorf("trust remove %q failed: %w", a, err)
			}
			cmd.Printf("untrusted %s\n", a)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(trustCmd)
	trustCmd.AddCommand(trustEnableCmd)
	trustCmd.AddCommand(trustDisableCmd)
	trustCmd.AddCommand(trustStatusCmd)
	trustCmd.AddCommand(trustResetCmd)
	trustCmd.AddCommand(trustListCmd)
	trustCmd.AddCommand(trustAddCmd)
	trustCmd.AddCommand(trustRemoveCmd)
	// Ensure no --yes bypass: we don't add --yes flag to trust subcommands, but global --yes via viper is checked inside security
	_ = fmt.Sprintf
}
