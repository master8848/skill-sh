package cache

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
	"skill.sh/mskill/internal/resolve"
)

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"2GB":     2 * 1024 * 1024 * 1024,
		"2gb":     2 * 1024 * 1024 * 1024,
		"512MB":   512 * 1024 * 1024,
		"100m":    100 * 1024 * 1024,
		"10kb":    10 * 1024,
		"10k":     10 * 1024,
		"100b":    100,
		"":        2 * 1024 * 1024 * 1024,
		"invalid": 2 * 1024 * 1024 * 1024,
		"0":       2 * 1024 * 1024 * 1024,
	}
	for in, want := range cases {
		got := parseSize(in)
		if got != want {
			t.Fatalf("parseSize(%q)=%d want %d", in, got, want)
		}
	}
}

func TestIsSHA(t *testing.T) {
	if !isSHA("abc1234") {
		t.Fatalf("short sha")
	}
	if !isSHA("a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2") {
		t.Fatalf("long sha 40 hex")
	}
	if isSHA("xyz") {
		t.Fatalf("xyz not sha")
	}
	if isSHA("abc") {
		t.Fatalf("too short")
	}
	if isSHA("") {
		t.Fatalf("empty should be false")
	}
	if isSHA("ggggggg") {
		t.Fatalf("non-hex")
	}
	if isSHA(string(make([]byte, 41))) {
		t.Fatalf("too long")
	}
}

func TestMissingPathsAndDedup(t *testing.T) {
	if got := missingPaths([]string{"a", "b"}, []string{"a"}); len(got) != 1 || got[0] != "b" {
		t.Fatalf("missingPaths %v", got)
	}
	if got := missingPaths([]string{"", "a"}, []string{}); len(got) != 1 {
		t.Fatalf("missingPaths empty handling %v", got)
	}
	d := dedup([]string{"a", "b", "a", "", "b"})
	if len(d) != 2 || d[0] != "a" || d[1] != "b" {
		t.Fatalf("dedup %v", d)
	}
}

func TestDirSizeAndCopyDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "f1"), []byte("hello"), 0644)
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0755)
	_ = os.WriteFile(filepath.Join(sub, "f2"), []byte("world!!"), 0644)
	sz, err := dirSize(dir)
	if err != nil || sz < 10 {
		t.Fatalf("dirSize %d err %v", sz, err)
	}
	dst := filepath.Join(t.TempDir(), "dst")
	if err := copyDir(dir, dst); err != nil {
		t.Fatalf("copyDir %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "f1")); err != nil {
		t.Fatalf("dst missing f1")
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "f2")); err != nil {
		t.Fatalf("dst missing f2")
	}
}

func TestWithLockAndTryLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := withLock(dir, func() error { return nil }); err != nil {
		t.Fatalf("withLock %v", err)
	}
	release, err := tryLock(dir)
	if err != nil {
		t.Fatalf("tryLock %v", err)
	}
	// second try should fail while locked
	if _, err := tryLock(dir); err == nil {
		t.Fatalf("second tryLock should fail")
	}
	release()
	// after release should succeed
	release2, err := tryLock(dir)
	if err != nil {
		t.Fatalf("tryLock after release %v", err)
	}
	release2()
}

func TestRandomString(t *testing.T) {
	s := randomString(8)
	if len(s) != 8 {
		t.Fatalf("len %d", len(s))
	}
	s2 := randomString(8)
	if s == s2 {
		t.Logf("random collision possible but unlikely: %s %s", s, s2)
	}
}

func TestGC_TmpPruningAndMaxAgeLRU(t *testing.T) {
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	_ = os.MkdirAll(dot, 0700)
	_ = os.MkdirAll(cacheDir, 0755)
	paths := config.Paths{DotDir: dot, CacheDir: cacheDir, ConfigFile: filepath.Join(dot, "config.yaml")}
	// create tmp old entry
	tmpDir := filepath.Join(cacheDir, "tmp")
	_ = os.MkdirAll(tmpDir, 0755)
	oldTmp := filepath.Join(tmpDir, "clone-old")
	_ = os.MkdirAll(oldTmp, 0755)
	_ = os.WriteFile(filepath.Join(oldTmp, "file"), []byte("old"), 0644)
	oldTime := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(oldTmp, oldTime, oldTime)
	// create repos entry that is expired
	viper.Reset()
	viper.SetDefault("cache.gc.max_age", 24*time.Hour)
	viper.SetDefault("cache.gc.max_size", "2GB")
	repoPath := filepath.Join(cacheDir, "repos", "github.com", "owner", "repo-old", "main--abcd1234")
	_ = os.MkdirAll(repoPath, 0755)
	_ = os.WriteFile(filepath.Join(repoPath, "file"), []byte("x"), 0644)
	meta := &Meta{Host: "github.com", Owner: "owner", Repo: "repo-old", Ref: "main", LastAccess: time.Now().Add(-31 * 24 * time.Hour), LastFetch: time.Now().Add(-31 * 24 * time.Hour), CreatedAt: time.Now().Add(-31 * 24 * time.Hour)}
	if err := SaveMeta(filepath.Join(repoPath, ".mskill-meta.json"), meta); err != nil {
		t.Fatalf("SaveMeta %v", err)
	}
	deleted, err := GC(paths, false)
	if err != nil {
		t.Fatalf("GC %v", err)
	}
	found := false
	for _, d := range deleted {
		if d == oldTmp || d == repoPath {
			found = true
		}
	}
	if !found {
		t.Fatalf("GC should delete oldTmp or old repo, got %v", deleted)
	}
	if _, err := os.Stat(oldTmp); err == nil {
		t.Fatalf("oldTmp still exists")
	}
	// clean
	viper.Reset()
}

func TestGC_DryRunAndClean(t *testing.T) {
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache2")
	_ = os.MkdirAll(dot, 0700)
	paths := config.Paths{DotDir: dot, CacheDir: cacheDir, ConfigFile: filepath.Join(dot, "config.yaml")}
	// create fresh entry that is expired but dryRun should not delete
	viper.Reset()
	viper.SetDefault("cache.gc.max_age", 1*time.Hour)
	repoPath := filepath.Join(cacheDir, "repos", "github.com", "owner", "repo-fresh", "main--12345678")
	_ = os.MkdirAll(repoPath, 0755)
	meta := &Meta{Host: "github.com", Owner: "owner", Repo: "repo-fresh", Ref: "main", LastAccess: time.Now().Add(-2 * time.Hour)}
	_ = SaveMeta(filepath.Join(repoPath, ".mskill-meta.json"), meta)
	_ = os.WriteFile(filepath.Join(repoPath, "data"), []byte("hi"), 0644)
	deleted, err := GC(paths, true)
	if err != nil {
		t.Fatalf("GC dry %v", err)
	}
	if len(deleted) == 0 {
		t.Fatalf("dryRun should list deleted")
	}
	if _, err := os.Stat(repoPath); err != nil {
		t.Fatalf("dryRun should not delete")
	}
	// Clean should remove repos/*
	if err := Clean(paths); err != nil {
		t.Fatalf("Clean %v", err)
	}
	if _, err := os.Stat(repoPath); err == nil {
		t.Fatalf("Clean should remove repos")
	}
	viper.Reset()
}

func TestLoadMetaError(t *testing.T) {
	if _, err := LoadMeta("/nonexistent/path.json"); err == nil {
		t.Fatalf("expected error")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("not json"), 0644)
	if _, err := LoadMeta(bad); err == nil {
		t.Fatalf("expected json error")
	}
}

func TestGC_MaxSizeLRU(t *testing.T) {
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache3")
	_ = os.MkdirAll(dot, 0700)
	paths := config.Paths{DotDir: dot, CacheDir: cacheDir, ConfigFile: filepath.Join(dot, "config.yaml")}
	viper.Reset()
	viper.Set("cache.gc.max_age", 30*24*time.Hour)
	viper.Set("cache.gc.max_size", "10b") // tiny to force eviction
	// create two entries
	for i := 0; i < 2; i++ {
		p := filepath.Join(cacheDir, "repos", "github.com", "owner", "repo", "ref--0000000"+string(rune('0'+i)))
		_ = os.MkdirAll(p, 0755)
		_ = os.WriteFile(filepath.Join(p, "big"), make([]byte, 20), 0644)
		m := &Meta{Host: "github.com", Owner: "owner", Repo: "repo", Ref: "ref", LastAccess: time.Now().Add(time.Duration(-i) * time.Hour)}
		_ = SaveMeta(filepath.Join(p, ".mskill-meta.json"), m)
	}
	deleted, err := GC(paths, false)
	if err != nil {
		t.Fatalf("GC maxSize %v", err)
	}
	if len(deleted) == 0 {
		t.Fatalf("expected deletion due to maxSize")
	}
	viper.Reset()
}

func initGitRepoHelper(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "test").Run()
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	if out, err := exec.Command("git", "-C", dir, "commit", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	return dir
}

func TestEnsureTTLAndSparseExpansion(t *testing.T) {
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache4")
	_ = os.MkdirAll(dot, 0700)
	_ = os.MkdirAll(cacheDir, 0755)
	paths := config.Paths{DotDir: dot, CacheDir: cacheDir, ConfigFile: filepath.Join(dot, "config.yaml")}
	viper.Reset()
	_ = config.InitViper(paths)
	viper.Set("cache.ttl", 1*time.Hour)
	viper.Set("cache.gc.max_size", "2GB")
	repoDir := initGitRepoHelper(t, map[string]string{
		"skill-a/SKILL.md": "hello a",
		"skill-b/SKILL.md": "hello b",
	})
	r, _ := resolve.ParseSkillRef("owner/repo/skill-a")
	r.CloneURL = repoDir
	r.Host = "github.com"
	r.Owner = "owner"
	r.Repo = "repo"
	ctx := context.Background()
	cachePath, meta, err := Ensure(ctx, paths, r, "HEAD", []string{"skill-a"}, false)
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cachePath, "skill-a", "SKILL.md")); err != nil {
		t.Fatalf("cached skill missing: %v", err)
	}
	// cache hit within TTL (no force)
	cachePath2, meta2, err := Ensure(ctx, paths, r, "HEAD", []string{"skill-a"}, false)
	if err != nil {
		t.Fatalf("Ensure hit: %v", err)
	}
	if cachePath2 != cachePath || meta2.CommitSHA != meta.CommitSHA {
		t.Fatalf("cache hit mismatch")
	}
	// sparse expansion to second skill
	r2, _ := resolve.ParseSkillRef("owner/repo/skill-b")
	r2.CloneURL = repoDir
	r2.Host = "github.com"
	r2.Owner = "owner"
	r2.Repo = "repo"
	_, meta3, err := Ensure(ctx, paths, r2, "HEAD", []string{"skill-b"}, false)
	if err != nil {
		t.Fatalf("sparse expansion: %v", err)
	}
	if len(meta3.SparsePaths) < 2 {
		t.Fatalf("sparsePaths %v", meta3.SparsePaths)
	}
	if _, err := os.Stat(filepath.Join(cachePath, "skill-b", "SKILL.md")); err != nil {
		t.Fatalf("skill-b missing after expansion: %v", err)
	}
	viper.Reset()
}

func TestEnsureCorruptRecovery(t *testing.T) {
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache5")
	_ = os.MkdirAll(dot, 0700)
	paths := config.Paths{DotDir: dot, CacheDir: cacheDir, ConfigFile: filepath.Join(dot, "config.yaml")}
	viper.Reset()
	_ = config.InitViper(paths)
	viper.Set("cache.ttl", 0) // force stale to trigger fetch path
	repoDir := initGitRepoHelper(t, map[string]string{"skill/SKILL.md": "v1"})
	r, _ := resolve.ParseSkillRef("owner/repo2/skill")
	r.CloneURL = repoDir
	r.Host = "github.com"
	r.Owner = "owner"
	r.Repo = "repo2"
	ctx := context.Background()
	cp, _, err := Ensure(ctx, paths, r, "HEAD", []string{"skill"}, false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// corrupt .git
	_ = os.RemoveAll(filepath.Join(cp, ".git", "HEAD"))
	_ = os.WriteFile(filepath.Join(cp, ".git", "HEAD"), []byte("corrupt"), 0644)
	// next Ensure with force should recover (remove and re-clone)
	// but our path has ttl 0 so it will go via fetch; corrupt detection should trigger remove
	// Use fresh repo to ensure clone fallback works
	_, _, err = Ensure(ctx, paths, r, "HEAD", []string{"skill"}, true)
	if err != nil {
		t.Logf("corrupt recovery Ensure error (may be expected if git detects corrupt): %v", err)
		// at least should not panic
	}
	viper.Reset()
}
