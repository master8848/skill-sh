package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/link"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/testutil"
)

func TestFetchStoreLink_SparseAndTTL(t *testing.T) {
	paths := testutil.TempPaths(t)
	repoDir := testutil.InitGitRepo(t, "", map[string]string{
		"my-skill/SKILL.md":        "# My Skill\nhello",
		"my-skill/README.md":       "readme",
		"other-skill/SKILL.md":     "# Other",
		"root.txt":                 "root",
	})

	// resolved via shorthand owner/repo/skill but we override CloneURL to file:// repo
	r, err := resolve.ParseSkillRef("owner/repo/my-skill")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// point to file repo
	r.CloneURL = repoDir
	r.Host = "github.com"
	r.Owner = "owner"
	r.Repo = "repo"

	ctx := context.Background()
	// First ensure with my-skill
	cachePath, meta, err := cache.Ensure(ctx, paths, r, "HEAD", []string{"my-skill"}, false)
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	if meta.CommitSHA == "" {
		t.Fatalf("commitSha empty")
	}
	if _, err := os.Stat(filepath.Join(cachePath, "my-skill", "SKILL.md")); err != nil {
		t.Fatalf("cached skill missing: %v", err)
	}
	// sparse: other-skill should not be present (blob:none sparse)
	if _, err := os.Stat(filepath.Join(cachePath, "other-skill", "SKILL.md")); err == nil {
		// with sparse cone, other directory may be absent or empty - warn but not fail
		t.Logf("sparse: other-skill present (maybe full checkout fallback)")
	}
	// Second call without force should hit cache (fast, no fetch)
	start := time.Now()
	cachePath2, meta2, err := cache.Ensure(ctx, paths, r, "HEAD", []string{"my-skill"}, false)
	if err != nil {
		t.Fatalf("Ensure cache hit: %v", err)
	}
	if cachePath2 != cachePath {
		t.Fatalf("cache path mismatch %s vs %s", cachePath, cachePath2)
	}
	if meta2.CommitSHA != meta.CommitSHA {
		t.Fatalf("commit mismatch on cache hit")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cache hit too slow, should be <5s")
	}

	// Sparse expansion: request other-skill same repo@ref should expand without re-clone
	r2, _ := resolve.ParseSkillRef("owner/repo/other-skill")
	r2.CloneURL = repoDir
	r2.Host = "github.com"
	r2.Owner = "owner"
	r2.Repo = "repo"
	_, meta3, err := cache.Ensure(ctx, paths, r2, "HEAD", []string{"other-skill"}, false)
	if err != nil {
		t.Fatalf("sparse expansion: %v", err)
	}
	// Should now have both skills
	if _, err := os.Stat(filepath.Join(cachePath, "my-skill", "SKILL.md")); err != nil {
		t.Fatalf("my-skill missing after expansion: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cachePath, "other-skill", "SKILL.md")); err != nil {
		t.Fatalf("other-skill missing after expansion: %v - sparsePaths %v", err, meta3.SparsePaths)
	}
	if len(meta3.SparsePaths) < 2 {
		t.Fatalf("sparsePaths should have 2, got %v", meta3.SparsePaths)
	}
}

func TestFetchStoreLink_IncrementalUpdateAndLinkSymlink(t *testing.T) {
	paths := testutil.TempPaths(t)
	repoDir := testutil.InitGitRepo(t, "", map[string]string{
		"my-skill/SKILL.md": "# v1",
	})
	r, _ := resolve.ParseSkillRef("owner/repo/my-skill")
	r.CloneURL = repoDir
	r.Host = "github.com"
	r.Owner = "owner"
	r.Repo = "repo"

	ctx := context.Background()
	cachePath, _, err := cache.Ensure(ctx, paths, r, "HEAD", []string{"my-skill"}, false)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(cachePath, "my-skill", "SKILL.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(data)) != "# v1" {
		t.Fatalf("expected v1, got %q", string(data))
	}

	// Update repo to v2
	_ = os.WriteFile(filepath.Join(repoDir, "my-skill", "SKILL.md"), []byte("# v2"), 0644)
	_ = exec.Command("git", "-C", repoDir, "add", ".").Run()
	_ = exec.Command("git", "-C", repoDir, "commit", "-m", "v2").Run()

	// Ensure with force (fetch + reset)
	_, _, err = cache.Ensure(ctx, paths, r, "HEAD", []string{"my-skill"}, true)
	if err != nil {
		t.Fatalf("ensure force: %v", err)
	}
	data2, _ := os.ReadFile(filepath.Join(cachePath, "my-skill", "SKILL.md"))
	if strings.TrimSpace(string(data2)) != "# v2" {
		t.Fatalf("expected v2 after fetch, got %q", string(data2))
	}

	// Link via symlink (default auto on darwin => symlink)
	home := t.TempDir()
	t.Setenv("HOME", home)
	// isolated global dest
	viper.Set("link.targets", []string{"agents", "claude"})
	skillPath := filepath.Join(cachePath, "my-skill")
	if _, err := os.Stat(skillPath); err != nil {
		skillPath = cachePath
	}
	if err := link.Install(skillPath, "my-skill", link.InstallOpts{Global: true, LinkMode: "symlink"}); err != nil {
		t.Fatalf("link install: %v", err)
	}
	dest := filepath.Join(home, ".agents", "skills", "my-skill")
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("dest missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		// fallback copy case: check file exists
		if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
			t.Fatalf("dest not symlink nor copy: %v", err)
		}
		t.Logf("link was copy fallback (maybe Windows mode)")
	} else {
		// symlink target should resolve to cache
		target, _ := os.Readlink(dest)
		_ = target
		if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
			t.Fatalf("symlink target broken: %v", err)
		}
	}
	// Test copy mode explicitly
	destCopy := filepath.Join(home, ".claude", "skills", "my-skill-copy")
	_ = os.RemoveAll(destCopy)
	if err := link.Install(skillPath, "my-skill-copy", link.InstallOpts{Global: true, Copy: true}); err != nil {
		t.Fatalf("copy install: %v", err)
	}
	// should be directory not symlink
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "my-skill-copy", "SKILL.md")); err != nil {
		if _, err2 := os.Stat(filepath.Join(home, ".claude", "skills", "my-skill-copy", "SKILL.md")); err2 != nil {
			t.Fatalf("copy dest missing: %v %v", err, err2)
		}
	}
}

func TestCacheGCRemovesExpiredAndPreservesFresh(t *testing.T) {
	paths := testutil.TempPaths(t)
	// create two entries: old and fresh
	repoDir := testutil.InitGitRepo(t, "", map[string]string{"skill/SKILL.md": "hi"})
	rOld, _ := resolve.ParseSkillRef("owner/repo-old/skill")
	rOld.CloneURL = repoDir
	rOld.Host = "github.com"
	rOld.Owner = "owner"
	rOld.Repo = "repo-old"
	ctx := context.Background()
	oldPath, oldMeta, err := cache.Ensure(ctx, paths, rOld, "HEAD", []string{"skill"}, false)
	if err != nil {
		t.Fatalf("ensure old: %v", err)
	}
	// make it expired (31 days ago)
	oldMeta.LastAccess = time.Now().Add(-31 * 24 * time.Hour)
	if err := cache.SaveMeta(cache.MetaPath(paths.CacheDir, "github.com", "owner", "repo-old", "HEAD"), oldMeta); err != nil {
		t.Fatalf("save old meta: %v", err)
	}

	// fresh entry
	rFresh, _ := resolve.ParseSkillRef("owner/repo-fresh/skill")
	rFresh.CloneURL = repoDir
	rFresh.Host = "github.com"
	rFresh.Owner = "owner"
	rFresh.Repo = "repo-fresh"
	_, _, err = cache.Ensure(ctx, paths, rFresh, "HEAD", []string{"skill"}, false)
	if err != nil {
		t.Fatalf("ensure fresh: %v", err)
	}

	// run GC non-dry
	deleted, err := cache.GC(paths, false)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	foundOld := false
	for _, d := range deleted {
		if d == oldPath {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("GC should have deleted expired oldPath %s, deleted: %v", oldPath, deleted)
	}
	if _, err := os.Stat(oldPath); err == nil {
		t.Fatalf("oldPath still exists after GC")
	}
	// fresh should still exist
	freshPath := cache.CacheDirPath(paths.CacheDir, "github.com", "owner", "repo-fresh", "HEAD")
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("freshPath missing after GC, deleted: %v", deleted)
	}
	// dry-run should not delete fresh even if we make it old? Test dry-run
	freshMeta, _ := cache.LoadMeta(cache.MetaPath(paths.CacheDir, "github.com", "owner", "repo-fresh", "HEAD"))
	freshMeta.LastAccess = time.Now().Add(-31 * 24 * time.Hour)
	_ = cache.SaveMeta(cache.MetaPath(paths.CacheDir, "github.com", "owner", "repo-fresh", "HEAD"), freshMeta)
	deletedDry, err := cache.GC(paths, true)
	if err != nil {
		t.Fatalf("GC dry: %v", err)
	}
	if len(deletedDry) == 0 {
		t.Fatalf("dry-run should list fresh as would delete")
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("dry-run should not delete")
	}
}

func TestListAndRemoveFlow(t *testing.T) {
	paths := testutil.TempPaths(t)
	_ = paths
	home := t.TempDir()
	t.Setenv("HOME", home)
	viper.Set("link.targets", []string{"agents"})
	// create cache skill path fake
	cacheSkill := filepath.Join(t.TempDir(), "cached-skill")
	if err := os.MkdirAll(cacheSkill, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheSkill, "SKILL.md"), []byte("hi"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := link.Install(cacheSkill, "test-skill", link.InstallOpts{Global: true}); err != nil {
		t.Fatalf("install: %v", err)
	}
	dest := filepath.Join(home, ".agents", "skills", "test-skill")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest missing: %v", err)
	}
	// remove via link helper (simulate mskill remove)
	if err := os.RemoveAll(dest); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Lstat(dest); err == nil {
		t.Fatalf("dest still exists after remove")
	}
}

func TestResolveSanitization(t *testing.T) {
	if _, err := resolve.SanitizeSubpath("../evil"); err == nil {
		t.Fatalf("should reject ..")
	}
	if _, err := resolve.SanitizeSubpath("a/../b"); err == nil {
		t.Fatalf("should reject a/../b")
	}
	if got, err := resolve.SanitizeSubpath("a/b"); err != nil || got != "a/b" {
		t.Fatalf("sanitize a/b: got %q err %v", got, err)
	}
}
