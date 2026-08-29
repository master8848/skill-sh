package security

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

// IsInteractiveTTY reports whether stdin is a character device and terminal.
func IsInteractiveTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// IsAgentEnv checks known agent/CI env vars.
func IsAgentEnv() bool {
	keys := []string{
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "JENKINS_URL", "TF_BUILD",
		"AGENT", "CLAUDECODE", "CLAUDE_CODE", "CURSOR_AGENT", "CURSOR",
		"OPENCODE", "VSCODE_AGENT", "VSCODE", "CODESPACES", "CODEX",
		"CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "TEAMCITY_VERSION",
	}
	for _, k := range keys {
		// case-insensitive check: try upper and original
		for _, cand := range []string{k, strings.ToLower(k)} {
			if v := os.Getenv(cand); strings.TrimSpace(v) != "" {
				return true
			}
		}
		// also check env existence via lookup (case-sensitive already)
		if _, ok := os.LookupEnv(k); ok {
			if v := os.Getenv(k); strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	// generic check: any key containing AGENT? Do simple scan?
	return false
}

// IsTrustEnabled returns true if trust is enabled and password hash is set.
func IsTrustEnabled(paths config.Paths) bool {
	hash := viper.GetString("security.password_hash")
	if hash == "" {
		return false
	}
	enabled := viper.GetBool("security.trust_enabled")
	trustAt := viper.GetString("security.trust_enabled_at")
	// Also consider trust_enabled_at as indicator
	if enabled || strings.TrimSpace(trustAt) != "" {
		return true
	}
	// If trust_enabled not set but hash exists, trust is not enabled
	return false
}

// HashPassword hashes password with bcrypt cost 12.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// SetPasswordHash sets hash in viper and writes config.
func SetPasswordHash(hash string, paths config.Paths) error {
	viper.Set("security.password_hash", hash)
	viper.Set("security.password_set_at", time.Now().UTC().Format(time.RFC3339))
	return config.WriteConfig(paths)
}

// GetPasswordHash returns stored hash.
func GetPasswordHash() string { return viper.GetString("security.password_hash") }

// promptPassword reads password from terminal (or fallback).
func promptPassword(message string) (string, error) {
	fmt.Fprint(os.Stderr, message+" ")
	// Try survey-like fallback? Use term.ReadPassword if TTY.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	// Fallback to bufio (non-TTY but we already gate, should not happen)
	reader := bufio.NewReader(os.Stdin)
	s, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

// RequirePassword gates risky skill installs.
func RequirePassword(slug, reason string, paths config.Paths) error {
	if IsTrustEnabled(paths) {
		return nil
	}
	if !IsInteractiveTTY() || IsAgentEnv() || viper.GetBool("yes") || viper.GetBool("non-interactive") {
		return fmt.Errorf("UNSAFE skill %q requires human password. Re-run in TTY or mskill trust enable: %s", slug, reason)
	}
	hash := viper.GetString("security.password_hash")
	if hash == "" {
		// Create password
		pw1, err := promptPassword("Create install password (for risky skills):")
		if err != nil {
			return err
		}
		if strings.TrimSpace(pw1) == "" {
			return fmt.Errorf("password cannot be empty")
		}
		pw2, err := promptPassword("Confirm password:")
		if err != nil {
			return err
		}
		if pw1 != pw2 {
			return fmt.Errorf("passwords do not match")
		}
		h, err := HashPassword(pw1)
		if err != nil {
			return err
		}
		viper.Set("security.password_hash", h)
		viper.Set("security.password_set_at", time.Now().UTC().Format(time.RFC3339))
		if err := config.WriteConfig(paths); err != nil {
			return err
		}
		// Ensure file perms? WriteConfig already chmod 0600
		return nil
	}
	// Verify existing password, 3 retries
	for i := 0; i < 3; i++ {
		pw, err := promptPassword(fmt.Sprintf("Enter password to install UNSAFE skill %q (reason: %s):", slug, reason))
		if err != nil {
			return err
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)); err == nil {
			return nil
		}
		fmt.Fprintln(os.Stderr, "incorrect password")
		if i == 2 {
			return fmt.Errorf("incorrect password after 3 attempts")
		}
	}
	return fmt.Errorf("incorrect password")
}

// EnableTrust enables global trust (TTY required)
func EnableTrust(paths config.Paths) error {
	if !IsInteractiveTTY() || IsAgentEnv() {
		return fmt.Errorf("FATAL: trust change requires human TTY")
	}
	if viper.GetBool("yes") {
		return fmt.Errorf("trust enable --yes rejected")
	}
	hash := viper.GetString("security.password_hash")
	if hash == "" {
		// create password
		pw1, err := promptPassword("Create trust password:")
		if err != nil {
			return err
		}
		if strings.TrimSpace(pw1) == "" {
			return fmt.Errorf("password cannot be empty")
		}
		pw2, err := promptPassword("Confirm password:")
		if err != nil {
			return err
		}
		if pw1 != pw2 {
			return fmt.Errorf("passwords do not match")
		}
		h, err := HashPassword(pw1)
		if err != nil {
			return err
		}
		viper.Set("security.password_hash", h)
		viper.Set("security.password_set_at", time.Now().UTC().Format(time.RFC3339))
		hash = h
	} else {
		// verify existing password
		verified := false
		for i := 0; i < 3; i++ {
			pw, err := promptPassword("Enter password to enable trust:")
			if err != nil {
				return err
			}
			if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)); err == nil {
				verified = true
				break
			}
			fmt.Fprintln(os.Stderr, "incorrect password")
			if i == 2 {
				return fmt.Errorf("incorrect password after 3 attempts")
			}
		}
		if !verified {
			return fmt.Errorf("password verification failed")
		}
	}
	viper.Set("security.trust_enabled", true)
	viper.Set("security.trust_enabled_at", time.Now().UTC().Format(time.RFC3339))
	return config.WriteConfig(paths)
}

// DisableTrust disables trust (requires password)
func DisableTrust(paths config.Paths) error {
	if !IsInteractiveTTY() || IsAgentEnv() {
		return fmt.Errorf("FATAL: trust change requires human TTY")
	}
	hash := viper.GetString("security.password_hash")
	if hash != "" {
		verified := false
		for i := 0; i < 3; i++ {
			pw, err := promptPassword("Enter password to disable trust:")
			if err != nil {
				return err
			}
			if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)); err == nil {
				verified = true
				break
			}
			fmt.Fprintln(os.Stderr, "incorrect password")
			if i == 2 {
				return fmt.Errorf("incorrect password after 3 attempts")
			}
		}
		if !verified {
			return fmt.Errorf("password verification failed")
		}
	}
	viper.Set("security.trust_enabled", false)
	viper.Set("security.trust_enabled_at", "")
	return config.WriteConfig(paths)
}

// ResetTrust deletes hash and trust, requires TTY + typing RESET.
func ResetTrust(paths config.Paths) error {
	if !IsInteractiveTTY() || IsAgentEnv() {
		return fmt.Errorf("FATAL: trust change requires human TTY")
	}
	fmt.Fprint(os.Stderr, "Type RESET to confirm trust reset: ")
	reader := bufio.NewReader(os.Stdin)
	s, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(s) != "RESET" {
		return fmt.Errorf("reset aborted: must type RESET")
	}
	viper.Set("security.password_hash", "")
	viper.Set("security.password_set_at", "")
	viper.Set("security.trust_enabled", false)
	viper.Set("security.trust_enabled_at", "")
	viper.Set("security.salt", "")
	return config.WriteConfig(paths)
}

// StatusInfo holds trust status.
type StatusInfo struct {
	TrustEnabled bool
	PasswordSet  bool
	TrustAt      string
	PasswordAt   string
}

// Status returns current trust status.
func Status(paths config.Paths) StatusInfo {
	return StatusInfo{
		TrustEnabled: IsTrustEnabled(paths),
		PasswordSet:  viper.GetString("security.password_hash") != "",
		TrustAt:      viper.GetString("security.trust_enabled_at"),
		PasswordAt:   viper.GetString("security.password_set_at"),
	}
}
