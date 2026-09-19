package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheKey(t *testing.T) {
	k1 := CacheKey("github.com", "vercel-labs", "agent-skills", "main")
	k2 := CacheKey("github.com", "vercel-labs", "agent-skills", "main")
	if k1 != k2 {
		t.Fatalf("CacheKey not deterministic: %q vs %q", k1, k2)
	}
	if !contains(k1, "--") {
		t.Fatalf("CacheKey missing --: %q", k1)
	}
	// sanitized slash
	k3 := CacheKey("github.com", "owner", "repo", "feature/foo/bar")
	if contains(k3, "/") {
		t.Fatalf("CacheKey should not contain /: %q", k3)
	}
	if !contains(k3, "feature-foo-bar") {
		t.Fatalf("expected sanitized ref: %q", k3)
	}
	// empty ref => HEAD
	k4 := CacheKey("github.com", "owner", "repo", "")
	k5 := CacheKey("github.com", "owner", "repo", "HEAD")
	if k4 != k5 {
		t.Fatalf("empty ref should map to HEAD: %q vs %q", k4, k5)
	}
	// hash 8 chars after --
	parts := split(k1, "--")
	if len(parts) != 2 || len(parts[1]) != 8 {
		t.Fatalf("hash8 length wrong: %q", k1)
	}
}

func TestMetaSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos", "github.com", "owner", "repo", "main--abcd1234", ".mskill-meta.json")
	m := &Meta{
		Host:        "github.com",
		Owner:       "owner",
		Repo:        "repo",
		Ref:         "main",
		CloneURL:    "https://github.com/owner/repo.git",
		CommitSHA:   "abc123",
		SparsePaths: []string{"skills/foo"},
		Shallow:     true,
		Depth:       1,
		Filter:      "blob:none",
		LastFetch:   time.Now().UTC().Truncate(time.Second),
		LastAccess:  time.Now().UTC().Truncate(time.Second),
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
	if err := SaveMeta(path, m); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	loaded, err := LoadMeta(path)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if loaded.Host != m.Host || loaded.Owner != m.Owner || loaded.Repo != m.Repo || loaded.Ref != m.Ref {
		t.Fatalf("loaded mismatch: %+v vs %+v", loaded, m)
	}
	if loaded.CommitSHA != m.CommitSHA || loaded.Filter != m.Filter {
		t.Fatalf("commit/filter mismatch")
	}
	if len(loaded.SparsePaths) != 1 || loaded.SparsePaths[0] != "skills/foo" {
		t.Fatalf("sparse mismatch: %v", loaded.SparsePaths)
	}
	// Check dir perms
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm()&0755 != 0755 {
		t.Logf("dir perm %o, expected 0755", info.Mode().Perm())
	}
}

func TestMetaPathAndCacheDirPath(t *testing.T) {
	cacheDir := "/tmp/cache"
	mp := MetaPath(cacheDir, "github.com", "owner", "repo", "main")
	expectedKey := CacheKey("github.com", "owner", "repo", "main")
	expected := filepath.Join(cacheDir, "repos", "github.com", "owner", "repo", expectedKey, ".mskill-meta.json")
	if mp != expected {
		t.Fatalf("MetaPath %q != %q", mp, expected)
	}
	cd := CacheDirPath(cacheDir, "github.com", "owner", "repo", "main")
	if cd != filepath.Dir(expected) {
		t.Fatalf("CacheDirPath %q != %q", cd, filepath.Dir(expected))
	}
}

func contains(s, sub string) bool  { return strings.Contains(s, sub) }
func split(s, sep string) []string { return strings.Split(s, sep) }
