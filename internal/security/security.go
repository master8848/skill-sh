package security

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/viper"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
	"skill.sh/mskill/internal/config"
)

// IsInteractiveTTY reports whether stdin is a character device and terminal.
// Falls back to checking stderr TTY so that prompts on stderr work when
// stdin is redirected (common in piped contexts). Password reading still
// requires stdin TTY for term.ReadPassword; IsInteractiveTTY is a gate.
func IsInteractiveTTY() bool {
	fi, err := os.Stdin.Stat()
	if err == nil {
		if fi.Mode()&os.ModeCharDevice != 0 && term.IsTerminal(int(os.Stdin.Fd())) {
			return true
		}
	}
	// Fallback: stderr is terminal (prompts are printed to stderr)
	if term.IsTerminal(int(os.Stderr.Fd())) {
		return true
	}
	return false
}

// IsAgentEnv checks known agent/CI env vars. Narrowed: do NOT treat
// VSCODE or CURSOR bare as agent — a human in VS Code terminal sets
// TERM_PROGRAM=vscode but not CURSOR_AGENT. Only specific agent indicators.
func IsAgentEnv() bool {
	keys := []string{
		"CI", "GITHUB_ACTIONS", "GITLAB_CI", "JENKINS_URL", "TF_BUILD",
		"AGENT", "CLAUDECODE", "CLAUDE_CODE", "CURSOR_AGENT",
		"OPENCODE", "VSCODE_AGENT", "CODESPACES", "CODEX",
		"CONTINUOUS_INTEGRATION", "BUILD_NUMBER", "TEAMCITY_VERSION",
		"MSKILL_AGENT",
	}
	for _, k := range keys {
		for _, cand := range []string{k, strings.ToLower(k)} {
			if v, ok := os.LookupEnv(cand); ok && strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
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

// promptPassword reads password via survey/v2 Password prompt for nicer UX,
// consistent with other prompts in cmd/get.go. Falls back to term.ReadPassword
// if survey fails (e.g., no TTY). Messages go to Stderr.
func promptPassword(message string) (string, error) {
	var pw string
	prompt := &survey.Password{Message: message}
	// Survey handles TTY detection and masking; use Stdin/Stderr explicitly
	err := survey.AskOne(prompt, &pw, survey.WithStdio(os.Stdin, os.Stderr, os.Stderr))
	if err == nil {
		return pw, nil
	}
	// Survey failed (e.g., not a TTY or interrupted) — fallback to term.ReadPassword
	fmt.Fprint(os.Stderr, message+" ")
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err2 := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err2 == nil {
			return string(b), nil
		}
		// return original survey error if term also fails? prefer term error
		return "", err2
	}
	// Try stderr fd as fallback for reading if stdin not tty but stderr is
	if term.IsTerminal(int(os.Stderr.Fd())) {
		// Cannot read password from stderr; fall through to bufio
		// Keep stderr newline for UX
		fmt.Fprintln(os.Stderr)
	}
	// Final fallback to buffered read (non-TTY but gated path shouldn't reach here)
	reader := bufio.NewReader(os.Stdin)
	s, err2 := reader.ReadString('\n')
	if err2 != nil {
		// prefer original survey error for context
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func verifyPasswordWithRetry(hash string, prompt string) error {
	for i := 0; i < 3; i++ {
		pw, err := promptPassword(prompt)
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

// blocked error helpers preserve test-required substrings (UNSAFE, FATAL) while
// guiding AI agents to ask human to run mskill trust enable.

func requirePasswordBlockedError(slug, reason string) error {
	isAgent := IsAgentEnv()
	isTTY := IsInteractiveTTY()
	isYes := viper.GetBool("yes") || viper.GetBool("non-interactive")
	if isAgent {
		return fmt.Errorf("🤖 Agent detected: cannot set password automatically for UNSAFE skill %q. Ask the user to run in a human TTY: mskill trust enable  (or: mskill get %q in a human terminal to create password). This cannot be changed by AI without user approval. Reason: %s", slug, slug, reason)
	}
	if !isTTY {
		return fmt.Errorf("This skill is marked UNSAFE/UNKNOWN (reason: %s). UNSAFE skill %q requires human password. Please run in an interactive terminal (TTY) and set a password: mskill trust enable  Then re-run: mskill get %q", reason, slug, slug)
	}
	if isYes {
		return fmt.Errorf("UNSAFE skill %q requires human password (reason: %s). --yes cannot bypass. Please run without --yes in an interactive terminal: mskill trust enable  or mskill get %q. If you are an AI agent, ask the user to run: mskill trust enable in a human terminal. This trust cannot be changed by AI without user approval.", slug, reason, slug)
	}
	return fmt.Errorf("UNSAFE skill %q requires human password. Re-run in TTY or mskill trust enable: %s", slug, reason)
}

func trustBlockedError(action string) error {
	if IsAgentEnv() {
		return fmt.Errorf("FATAL: trust %s requires human TTY. 🤖 Agent detected: cannot change trust automatically. If you are an AI agent, ask the user to run: mskill trust %s in a human terminal. This trust cannot be changed by AI.", action, action)
	}
	if !IsInteractiveTTY() {
		return fmt.Errorf("FATAL: trust %s requires human TTY. Please run in an interactive terminal: mskill trust %s. If you are an AI agent, ask the user to run: mskill trust %s in a human terminal. This trust cannot be changed by AI.", action, action, action)
	}
	return fmt.Errorf("FATAL: trust change requires human TTY. If you are an AI agent, ask the user to run: mskill trust %s in a human terminal. This trust cannot be changed by AI.", action)
}

// RequirePassword gates risky skill installs.
func RequirePassword(slug, reason string, paths config.Paths) error {
	if IsSkillTrusted(slug) {
		return nil
	}
	if IsTrustEnabled(paths) {
		return nil
	}
	if !IsInteractiveTTY() || IsAgentEnv() || viper.GetBool("yes") || viper.GetBool("non-interactive") {
		return requirePasswordBlockedError(slug, reason)
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
	// hash exists but trust not enabled → verify
	// re-check gate for verify path (agent/non-TTY/yes should still block)
	if !IsInteractiveTTY() || IsAgentEnv() || viper.GetBool("yes") || viper.GetBool("non-interactive") {
		return requirePasswordBlockedError(slug, reason)
	}
	return verifyPasswordWithRetry(hash, fmt.Sprintf("Enter password to install UNSAFE skill %q (reason: %s):", slug, reason))
}

// EnableTrust enables global trust (TTY required)
func EnableTrust(paths config.Paths) error {
	if !IsInteractiveTTY() || IsAgentEnv() {
		return trustBlockedError("enable")
	}
	if viper.GetBool("yes") || viper.GetBool("non-interactive") {
		return fmt.Errorf("FATAL: trust enable --yes rejected. If you are an AI agent, ask the user to run: mskill trust enable in a human terminal. This trust cannot be changed by AI.")
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
		if err := verifyPasswordWithRetry(hash, "Enter password to enable trust:"); err != nil {
			return err
		}
	}
	viper.Set("security.trust_enabled", true)
	viper.Set("security.trust_enabled_at", time.Now().UTC().Format(time.RFC3339))
	return config.WriteConfig(paths)
}

// DisableTrust disables trust (requires password)
func DisableTrust(paths config.Paths) error {
	if !IsInteractiveTTY() || IsAgentEnv() {
		return trustBlockedError("disable")
	}
	hash := viper.GetString("security.password_hash")
	if hash != "" {
		if err := verifyPasswordWithRetry(hash, "Enter password to disable trust:"); err != nil {
			return err
		}
	}
	viper.Set("security.trust_enabled", false)
	viper.Set("security.trust_enabled_at", "")
	return config.WriteConfig(paths)
}

// ResetTrust deletes hash and trust, requires TTY + typing RESET.
func ResetTrust(paths config.Paths) error {
	if !IsInteractiveTTY() || IsAgentEnv() {
		return trustBlockedError("reset")
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
	TrustEnabled  bool
	PasswordSet   bool
	TrustAt       string
	PasswordAt    string
	TrustedSkills []string
}

// Status returns current trust status.
func Status(paths config.Paths) StatusInfo {
	return StatusInfo{
		TrustEnabled:  IsTrustEnabled(paths),
		PasswordSet:   viper.GetString("security.password_hash") != "",
		TrustAt:       viper.GetString("security.trust_enabled_at"),
		PasswordAt:    viper.GetString("security.password_set_at"),
		TrustedSkills: ListTrustedSkills(),
	}
}

// normalizeSkillKey lowercases and trims a skill slug for trusted-list comparison.
func normalizeSkillKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ListTrustedSkills returns the per-skill trusted slugs.
func ListTrustedSkills() []string {
	raw := viper.GetStringSlice("security.trusted_skills")
	var out []string
	seen := map[string]bool{}
	for _, s := range raw {
		// support comma-separated entries from manual config edits
		for _, part := range strings.Split(s, ",") {
			n := normalizeSkillKey(part)
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// IsSkillTrusted reports whether a slug is in the per-skill trusted list.
func IsSkillTrusted(slug string) bool {
	n := normalizeSkillKey(slug)
	if n == "" {
		return false
	}
	for _, s := range ListTrustedSkills() {
		if s == n {
			return true
		}
	}
	return false
}

// TrustSkill adds a slug to the per-skill trusted list (idempotent).
func TrustSkill(slug string, paths config.Paths) error {
	n := normalizeSkillKey(slug)
	if n == "" {
		return fmt.Errorf("skill name required")
	}
	if IsSkillTrusted(n) {
		return nil
	}
	current := ListTrustedSkills()
	current = append(current, n)
	viper.Set("security.trusted_skills", current)
	return config.WriteConfig(paths)
}

// UntrustSkill removes a slug from the per-skill trusted list.
func UntrustSkill(slug string, paths config.Paths) error {
	n := normalizeSkillKey(slug)
	if n == "" {
		return fmt.Errorf("skill name required")
	}
	current := ListTrustedSkills()
	var kept []string
	found := false
	for _, s := range current {
		if s == n {
			found = true
			continue
		}
		kept = append(kept, s)
	}
	if !found {
		return fmt.Errorf("skill %q is not in trusted list", slug)
	}
	viper.Set("security.trusted_skills", kept)
	return config.WriteConfig(paths)
}
