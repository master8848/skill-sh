package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePathsDefaults(t *testing.T) {
	t.Setenv("MSKILL_CACHE_DIR", "")
	t.Setenv("MSKILL_DOT_DIR", "")
	t.Setenv("MSKILL_CONFIG", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.DotDir == "" || p.CacheDir == "" || p.ConfigFile == "" || p.TrustFile == "" {
		t.Fatalf("empty paths: %+v", p)
	}
	if filepath.Base(p.TrustFile) != "trust.json" {
		t.Fatalf("trust file basename: %s", p.TrustFile)
	}
}

func TestResolvePathsEnvOverride(t *testing.T) {
	t.Setenv("MSKILL_CACHE_DIR", "/tmp/mskill-cache-test")
	t.Setenv("MSKILL_DOT_DIR", "/tmp/mskill-dot-test")
	t.Setenv("MSKILL_CONFIG", "/tmp/mskill-dot-test/config.yaml")

	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.CacheDir != "/tmp/mskill-cache-test" {
		t.Fatalf("CacheDir = %q want /tmp/mskill-cache-test", p.CacheDir)
	}
	if p.DotDir != "/tmp/mskill-dot-test" {
		t.Fatalf("DotDir = %q want /tmp/mskill-dot-test", p.DotDir)
	}
	if p.ConfigFile != "/tmp/mskill-dot-test/config.yaml" {
		t.Fatalf("ConfigFile = %q", p.ConfigFile)
	}
	_ = os.Getenv
}

func TestResolvePathsXDG(t *testing.T) {
	t.Setenv("MSKILL_CACHE_DIR", "")
	t.Setenv("MSKILL_DOT_DIR", "")
	t.Setenv("MSKILL_CONFIG", "")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")

	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.CacheDir != filepath.Join("/tmp/xdg-cache", "mskill") {
		t.Fatalf("CacheDir XDG = %q", p.CacheDir)
	}
	if p.ConfigFile != filepath.Join("/tmp/xdg-config", "mskill", "config.yaml") {
		t.Fatalf("ConfigFile XDG = %q", p.ConfigFile)
	}
}
