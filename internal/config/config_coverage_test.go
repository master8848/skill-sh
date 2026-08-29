package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestEnsureDirsAndInitViperAndWriteConfig(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".mskill")
	cache := filepath.Join(dir, "cache")
	cfgFile := filepath.Join(dot, "config.yaml")
	p := Paths{DotDir: dot, CacheDir: cache, ConfigFile: cfgFile, TrustFile: filepath.Join(dot, "trust.json")}
	if err := EnsureDirs(p); err != nil {
		t.Fatalf("EnsureDirs %v", err)
	}
	if info, err := os.Stat(dot); err != nil || info.Mode().Perm()&0700 != 0700 {
		t.Fatalf("dot perm %v err %v", info, err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache not created: %v", err)
	}
	viper.Reset()
	if err := InitViper(p); err != nil {
		t.Fatalf("InitViper %v", err)
	}
	if viper.GetDuration("cache.ttl") == 0 {
		t.Fatalf("cache.ttl not set")
	}
	viper.Set("security.password_hash", "testhash")
	if err := WriteConfig(p); err != nil {
		t.Fatalf("WriteConfig %v", err)
	}
	if _, err := os.Stat(cfgFile); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	info, _ := os.Stat(cfgFile)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config perm %o want 0600", info.Mode().Perm())
	}
	// Ensure WriteConfig updates existing
	viper.Set("link.mode", "copy")
	if err := WriteConfig(p); err != nil {
		t.Fatalf("second WriteConfig %v", err)
	}
	b, _ := os.ReadFile(cfgFile)
	if len(b) == 0 {
		t.Fatalf("config empty")
	}
	viper.Reset()
}

func TestRandomSalt(t *testing.T) {
	s, err := RandomSalt(16)
	if err != nil || len(s) != 32 {
		t.Fatalf("RandomSalt 16: %q %v len %d", s, err, len(s))
	}
	s2, _ := RandomSalt(0)
	if len(s2) != 32 {
		t.Fatalf("default salt len %d", len(s2))
	}
	s3, _ := RandomSalt(8)
	if len(s3) != 16 {
		t.Fatalf("8 byte salt len %d", len(s3))
	}
}

func TestResolvePathsWithXDGConfigOverride(t *testing.T) {
	t.Setenv("MSKILL_DOT_DIR", "")
	t.Setenv("MSKILL_CACHE_DIR", "")
	t.Setenv("MSKILL_CONFIG", "/tmp/custom/config.yaml")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths %v", err)
	}
	if p.ConfigFile != "/tmp/custom/config.yaml" {
		t.Fatalf("custom config %q", p.ConfigFile)
	}
	t.Setenv("MSKILL_CONFIG", "")
}

func TestEnsureDirsWithXDGConfigPath(t *testing.T) {
	dir := t.TempDir()
	xdg := filepath.Join(dir, "xdgconf")
	customCfg := filepath.Join(xdg, "mskill", "config.yaml")
	p := Paths{DotDir: filepath.Join(dir, ".mskill"), CacheDir: filepath.Join(dir, "cache"), ConfigFile: customCfg, TrustFile: filepath.Join(dir, ".mskill", "trust.json")}
	if err := EnsureDirs(p); err != nil {
		t.Fatalf("EnsureDirs xdg %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".mskill")); err != nil {
		t.Fatalf("dot not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "cache")); err != nil {
		t.Fatalf("cache not created")
	}
	if _, err := os.Stat(filepath.Dir(customCfg)); err != nil {
		t.Fatalf("xdg dir not created: %v", err)
	}
}
