package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
)

func TestIsAgentEnv(t *testing.T) {
	origCI := os.Getenv("CI")
	origGH := os.Getenv("GITHUB_ACTIONS")
	defer func() {
		os.Setenv("CI", origCI)
		os.Setenv("GITHUB_ACTIONS", origGH)
	}()

	os.Unsetenv("CI")
	os.Unsetenv("GITHUB_ACTIONS")
	os.Unsetenv("GITLAB_CI")
	os.Unsetenv("AGENT")
	os.Unsetenv("CLAUDECODE")
	os.Unsetenv("CURSOR_AGENT")
	os.Unsetenv("OPENCODE")
	os.Unsetenv("VSCODE_AGENT")
	os.Unsetenv("CODESPACES")

	if IsAgentEnv() {
		t.Fatalf("expected not agent env when all unset")
	}
	os.Setenv("CI", "true")
	if !IsAgentEnv() {
		t.Fatalf("expected agent env when CI set")
	}
	os.Unsetenv("CI")
	os.Setenv("GITHUB_ACTIONS", "1")
	if !IsAgentEnv() {
		t.Fatalf("expected agent env when GITHUB_ACTIONS set")
	}
	os.Unsetenv("GITHUB_ACTIONS")
}

func TestIsTrustEnabled(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	cache := filepath.Join(dir, ".cache", "mskill")
	os.MkdirAll(dot, 0700)
	os.MkdirAll(cache, 0755)
	cfgFile := filepath.Join(dot, "config.yaml")
	paths := config.Paths{CacheDir: cache, DotDir: dot, ConfigFile: cfgFile, TrustFile: filepath.Join(dot, "trust.json")}

	viper.Reset()
	_ = config.InitViper(paths)

	// No hash => not enabled
	if IsTrustEnabled(paths) {
		t.Fatalf("expected not trusted when no hash")
	}
	// Hash but no trust flag => not enabled
	viper.Set("security.password_hash", "$2a$12$abc")
	viper.Set("security.trust_enabled", false)
	viper.Set("security.trust_enabled_at", "")
	if IsTrustEnabled(paths) {
		t.Fatalf("expected not trusted when trust_enabled false")
	}
	// Hash + trust_enabled true => enabled
	viper.Set("security.trust_enabled", true)
	if !IsTrustEnabled(paths) {
		t.Fatalf("expected trusted when hash+enabled")
	}
	// Hash + trust_enabled_at set (but bool false) => enabled per spec (trust_enabled_at present)
	viper.Set("security.trust_enabled", false)
	viper.Set("security.trust_enabled_at", "2026-08-28T15:05:00Z")
	if !IsTrustEnabled(paths) {
		t.Fatalf("expected trusted when hash+trust_enabled_at")
	}
	// No hash but trust_enabled true => false (hash required)
	viper.Set("security.password_hash", "")
	viper.Set("security.trust_enabled", true)
	viper.Set("security.trust_enabled_at", "2026-08-28T15:05:00Z")
	if IsTrustEnabled(paths) {
		t.Fatalf("expected not trusted when hash empty even if trust flag set")
	}
	// reset
	viper.Reset()
}

func TestHashPassword(t *testing.T) {
	h, err := HashPassword("test1234")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if h == "" || h == "test1234" {
		t.Fatalf("hash invalid: %q", h)
	}
	// Verify cost 12? bcrypt prefix $2a$12$
	if len(h) < 7 || h[:7] != "$2a$12$" {
		t.Fatalf("expected bcrypt cost 12 prefix, got %q", h[:7])
	}
}

func TestStatus(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	cache := filepath.Join(dir, ".cache", "mskill")
	os.MkdirAll(dot, 0700)
	cfgFile := filepath.Join(dot, "config.yaml")
	paths := config.Paths{CacheDir: cache, DotDir: dot, ConfigFile: cfgFile, TrustFile: filepath.Join(dot, "trust.json")}
	viper.Reset()
	_ = config.InitViper(paths)
	viper.Set("security.password_hash", "$2a$12$abc")
	viper.Set("security.password_set_at", "2026-08-28T15:00:00Z")
	viper.Set("security.trust_enabled", true)
	viper.Set("security.trust_enabled_at", "2026-08-28T15:05:00Z")
	s := Status(paths)
	if !s.PasswordSet || !s.TrustEnabled {
		t.Fatalf("status mismatch: %+v", s)
	}
	if s.PasswordAt == "" || s.TrustAt == "" {
		t.Fatalf("status timestamps missing: %+v", s)
	}
	viper.Reset()
}
