package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
)

// TempPaths creates isolated temp directories for MSKILL_DOT_DIR, MSKILL_CACHE_DIR
// and MSKILL_CONFIG, resets viper, resolves paths, ensures dirs and inits viper.
// It mirrors the duplicated helper previously found in e2e, cmd and cache tests.
func TempPaths(t *testing.T) config.Paths {
	t.Helper()
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", cacheDir)
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	viper.Reset()
	p, err := config.ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if err := config.EnsureDirs(p); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	if err := config.InitViper(p); err != nil {
		t.Fatalf("InitViper: %v", err)
	}
	viper.Set("cache.ttl", 1*time.Hour)
	return p
}

// InitGitRepo initializes a git repo at dir (or a new TempDir if dir == "")
// with the given files, commits them, and returns the repo dir.
func InitGitRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "test").Run()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	return dir
}
