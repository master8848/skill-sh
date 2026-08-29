package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
	"golang.org/x/crypto/bcrypt"
)

func TestRequirePassword_FailClosed(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	cache := filepath.Join(dir, "cache")
	_ = os.MkdirAll(dot, 0700)
	_ = os.MkdirAll(cache, 0755)
	paths := config.Paths{CacheDir: cache, DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml"), TrustFile: filepath.Join(dot, "trust.json")}
	viper.Reset()
	_ = config.InitViper(paths)
	viper.Set("security.password_hash", "$2a$12$dummyhashdummyhashdummyha")
	viper.Set("security.trust_enabled", false)
	viper.Set("security.trust_enabled_at", "")
	// Ensure agent env empty, yes false
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("AGENT", "")
	viper.Set("yes", false)
	viper.Set("non-interactive", false)
	// This will attempt TTY check; in CI/non-TTY it should fail-closed
	err := RequirePassword("evil/skill", "test reason", paths)
	if err == nil {
		t.Fatalf("RequirePassword should fail closed in non-TTY")
	}
	if !containsStr(err.Error(), "UNSAFE skill") {
		t.Fatalf("error should contain UNSAFE skill, got %q", err.Error())
	}
	viper.Reset()
}

func TestRequirePassword_TrustBypass(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	cache := filepath.Join(dir, "cache")
	_ = os.MkdirAll(dot, 0700)
	paths := config.Paths{CacheDir: cache, DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml")}
	viper.Reset()
	_ = config.InitViper(paths)
	h, _ := bcrypt.GenerateFromPassword([]byte("secret123"), 4)
	viper.Set("security.password_hash", string(h))
	viper.Set("security.trust_enabled", true)
	// Even in non-TTY/agent env, trust bypass should return nil
	t.Setenv("CI", "true")
	err := RequirePassword("any/skill", "reason", paths)
	if err != nil {
		t.Fatalf("trust bypass should allow, got %v", err)
	}
	t.Setenv("CI", "")
	viper.Reset()
}

func TestRequirePassword_YesFlagFails(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	cache := filepath.Join(dir, "cache")
	_ = os.MkdirAll(dot, 0700)
	paths := config.Paths{CacheDir: cache, DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml")}
	viper.Reset()
	_ = config.InitViper(paths)
	viper.Set("security.password_hash", "$2a$12$dummyhashdummyhashdummyha")
	viper.Set("security.trust_enabled", false)
	viper.Set("yes", true)
	err := RequirePassword("evil/skill", "reason", paths)
	if err == nil || !containsStr(err.Error(), "UNSAFE") {
		t.Fatalf("yes flag should fail closed, got %v", err)
	}
	viper.Reset()
}

func TestRequirePassword_AgentEnvFails(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	paths := config.Paths{CacheDir: filepath.Join(dir, "cache"), DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml")}
	_ = os.MkdirAll(dot, 0700)
	viper.Reset()
	_ = config.InitViper(paths)
	viper.Set("security.password_hash", "$2a$12$dummyhashdummyhashdummyha")
	viper.Set("security.trust_enabled", false)
	t.Setenv("CI", "true")
	err := RequirePassword("evil/skill", "reason", paths)
	if err == nil {
		t.Fatalf("agent env should fail closed")
	}
	t.Setenv("CI", "")
	viper.Reset()
}

func TestEnableTrust_Gating(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	paths := config.Paths{CacheDir: filepath.Join(dir, "cache"), DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml")}
	_ = os.MkdirAll(dot, 0700)
	viper.Reset()
	_ = config.InitViper(paths)
	// In test, stdin is not a TTY, so EnableTrust should fail with FATAL
	err := EnableTrust(paths)
	if err == nil || !containsStr(err.Error(), "FATAL") {
		t.Fatalf("EnableTrust should require TTY, got %v", err)
	}
	// Also with agent env
	t.Setenv("CI", "true")
	err = EnableTrust(paths)
	if err == nil {
		t.Fatalf("EnableTrust should fail in agent env")
	}
	t.Setenv("CI", "")
	// Also with --yes
	viper.Set("yes", true)
	err = EnableTrust(paths)
	if err == nil {
		t.Fatalf("EnableTrust should reject --yes")
	}
	viper.Reset()
}

func TestDisableTrustAndResetTrust_Gating(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	paths := config.Paths{CacheDir: filepath.Join(dir, "cache"), DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml")}
	_ = os.MkdirAll(dot, 0700)
	viper.Reset()
	_ = config.InitViper(paths)
	if err := DisableTrust(paths); err == nil || !containsStr(err.Error(), "FATAL") {
		t.Fatalf("DisableTrust gating %v", err)
	}
	if err := ResetTrust(paths); err == nil || !containsStr(err.Error(), "FATAL") {
		t.Fatalf("ResetTrust gating %v", err)
	}
	viper.Reset()
}

func TestIsInteractiveTTYNonTTY(t *testing.T) {
	// In go test, stdin is not a TTY (pipe), so should be false
	if IsInteractiveTTY() {
		t.Logf("unexpected TTY in test env, skipping")
	}
	// At least ensure it doesn't panic
	_ = IsInteractiveTTY()
}

func TestIsAgentEnvLowercase(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("ci", "true")
	if !IsAgentEnv() {
		t.Fatalf("lowercase ci should be detected")
	}
	t.Setenv("ci", "")
	t.Setenv("AGENT", "")
	t.Setenv("agent", "1")
	if !IsAgentEnv() {
		t.Fatalf("lowercase agent should be detected")
	}
	t.Setenv("agent", "")
}

func TestHashPasswordAndVerify(t *testing.T) {
	h, err := HashPassword("mySecret123")
	if err != nil {
		t.Fatalf("HashPassword %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(h), []byte("mySecret123")); err != nil {
		t.Fatalf("verify %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(h), []byte("wrong")); err == nil {
		t.Fatalf("wrong password should fail")
	}
}

func TestGetSetPasswordHash(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	_ = os.MkdirAll(dot, 0700)
	paths := config.Paths{CacheDir: filepath.Join(dir, "cache"), DotDir: dot, ConfigFile: filepath.Join(dot, "config.yaml")}
	viper.Reset()
	_ = config.InitViper(paths)
	if GetPasswordHash() != "" {
		t.Fatalf("initial empty")
	}
	h := "$2a$12$testhashdummyhashdummyhashdummyhashdummyhashdum"
	if err := SetPasswordHash(h, paths); err != nil {
		t.Fatalf("SetPasswordHash %v", err)
	}
	if GetPasswordHash() != h {
		t.Fatalf("hash mismatch")
	}
	if _, err := os.Stat(paths.ConfigFile); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	info, _ := os.Stat(paths.ConfigFile)
	if info.Mode().Perm() != 0600 {
		t.Logf("perm %o want 0600", info.Mode().Perm())
	}
	viper.Reset()
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
