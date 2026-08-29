package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestCopyDirectory(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst")
	// create files
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// create .git that should be skipped
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, ".git", "config"), []byte("git"), 0644)
	// __pycache__ should be skipped
	if err := os.MkdirAll(filepath.Join(src, "__pycache__"), 0755); err != nil {
		t.Fatalf("mkdir pycache: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, "__pycache__", "cache.pyc"), []byte("pyc"), 0644)

	if err := CopyDirectory(src, dst); err != nil {
		t.Fatalf("CopyDirectory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil {
		t.Fatalf("a.txt missing: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(b) != "hello" {
		t.Fatalf("a.txt content %q", string(b))
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", "b.txt")); err != nil {
		t.Fatalf("b.txt missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git", "config")); err == nil {
		t.Fatalf(".git should be skipped")
	}
	if _, err := os.Stat(filepath.Join(dst, "__pycache__")); err == nil {
		t.Fatalf("__pycache__ should be skipped")
	}
	// check file perms 0644
	if info, err := os.Stat(filepath.Join(dst, "a.txt")); err == nil {
		if info.Mode().Perm() != 0644 {
			t.Fatalf("perm %o want 0644", info.Mode().Perm())
		}
	}
}

func TestCopyDirectoryDereference(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst2")
	realFile := filepath.Join(src, "real.txt")
	if err := os.WriteFile(realFile, []byte("realcontent"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	link := filepath.Join(src, "link.txt")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if err := CopyDirectory(src, dst); err != nil {
		t.Fatalf("CopyDirectory: %v", err)
	}
	// dst/link.txt should be regular file with same content, not symlink
	info, err := os.Lstat(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatalf("link.txt missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected dereferenced file, got symlink")
	}
	b, _ := os.ReadFile(filepath.Join(dst, "link.txt"))
	if string(b) != "realcontent" {
		t.Fatalf("dereferenced content %q", string(b))
	}
}

func TestCreateSymlinkRelative(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, "file.txt"), []byte("x"), 0644)
	link := filepath.Join(tmp, "link")
	// Ensure symlink creation works (may fail on windows without privilege)
	err := CreateSymlink(src, link)
	if err != nil {
		t.Fatalf("CreateSymlink: %v", err)
	}
	// Check if link is symlink (fallback copy results in dir, not symlink)
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(link)
		if err != nil {
			t.Fatalf("readlink: %v", err)
		}
		// Should be relative
		if filepath.IsAbs(target) {
			t.Fatalf("expected relative symlink, got abs %q", target)
		}
		// Resolve and check file exists via link
		if _, err := os.Stat(filepath.Join(link, "file.txt")); err != nil {
			t.Fatalf("file via symlink missing: %v", err)
		}
	} else {
		// fallback copy: directory should exist with file
		if _, err := os.Stat(filepath.Join(link, "file.txt")); err != nil {
			t.Fatalf("fallback copy missing file: %v", err)
		}
	}
}

func TestSkillFolderHashDeterministic(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("world"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1, err := SkillFolderHash(dir)
	if err != nil {
		t.Fatalf("hash1: %v", err)
	}
	h2, err := SkillFolderHash(dir)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash not deterministic %q vs %q", h1, h2)
	}
	// modify file should change hash
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello2"), 0644)
	h3, _ := SkillFolderHash(dir)
	if h3 == h1 {
		t.Fatalf("hash should change after modification")
	}
	// ensure .git ignored
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".git", "ignored.txt"), []byte("ignored"), 0644)
	h4, _ := SkillFolderHash(dir)
	if h4 != h3 {
		t.Fatalf("hash should ignore .git, got %q vs %q", h3, h4)
	}
}

func TestResolveDestinations(t *testing.T) {
	// Reset viper
	viper.Reset()
	viper.SetDefault("link.targets", []string{"claude", "agents", "project"})
	// empty filter => default
	dests := ResolveDestinations("", false, false)
	if len(dests) == 0 {
		t.Fatalf("expected default destinations")
	}
	// Check dedupe: claude and agents should be present, project alias maps to agents so should dedupe
	found := map[string]bool{}
	for _, d := range dests {
		found[d.Name] = true
	}
	if !found["claude"] {
		t.Fatalf("default should contain claude")
	}
	if !found["agents"] {
		t.Fatalf("default should contain agents")
	}
	// filter "*" => all agents at least 10
	viper.Reset()
	all := ResolveDestinations("*", false, false)
	if len(all) < 10 {
		t.Fatalf("expected at least 10 agents for *, got %d", len(all))
	}
	// comma separated
	viper.Reset()
	specific := ResolveDestinations("claude,codex", false, false)
	if len(specific) != 2 {
		t.Fatalf("expected 2, got %d: %+v", len(specific), specific)
	}
	names := map[string]bool{}
	for _, d := range specific {
		names[d.Name] = true
	}
	if !names["claude"] || !names["codex"] {
		t.Fatalf("unexpected names %+v", names)
	}
	// claude-code alias should map to claude
	viper.Reset()
	alias := ResolveDestinations("claude-code", false, false)
	if len(alias) != 1 || alias[0].Name != "claude" {
		t.Fatalf("alias claude-code should resolve to claude, got %+v", alias)
	}
	// unknown agent fallback
	viper.Reset()
	unk := ResolveDestinations("myagent", false, false)
	if len(unk) != 1 || unk[0].Name != "myagent" {
		t.Fatalf("unknown agent fallback failed: %+v", unk)
	}
	if !strings.Contains(unk[0].GlobalDir, "myagent") {
		t.Fatalf("unknown global dir should contain name: %q", unk[0].GlobalDir)
	}
	// test with viper GetStringSlice override
	viper.Reset()
	viper.Set("link.targets", []string{"cursor", "windsurf"})
	d2 := ResolveDestinations("", false, false)
	if len(d2) != 2 {
		t.Fatalf("viper override expected 2, got %d", len(d2))
	}
	viper.Reset()
}

func TestInstallCopyAndDryRun(t *testing.T) {
	// Setup cache dir with skill
	tmp := t.TempDir()
	// Use home override via HOME env for expandHome?
	home := filepath.Join(tmp, "home")
	_ = os.MkdirAll(home, 0755)
	t.Setenv("HOME", home)
	// Also override USERPROFILE for windows? ignore
	src := filepath.Join(tmp, "skillSrc")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# skill"), 0644)
	// Use viper isolated
	viper.Reset()
	viper.SetDefault("link.mode", "copy")
	viper.SetDefault("link.targets", []string{"claude"})
	// Create a temporary project dir as cwd
	proj := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(proj, 0755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)
	if err := os.Chdir(proj); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	opts := InstallOpts{Agents: "claude", LinkMode: "copy", Global: false, Project: true}
	if err := Install(src, "my-skill", opts); err != nil {
		t.Fatalf("Install copy project: %v", err)
	}
	// Expect project dest .claude/skills/my-skill exists
	if _, err := os.Stat(filepath.Join(proj, ".claude", "skills", "my-skill", "SKILL.md")); err != nil {
		t.Fatalf("project install missing: %v", err)
	}
	// DryRun should not create additional
	optsDry := InstallOpts{Agents: "cursor", LinkMode: "copy", Global: false, Project: true, DryRun: true}
	if err := Install(src, "dry-skill", optsDry); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".cursor", "skills", "dry-skill")); err == nil {
		t.Fatalf("dry-run should not create file")
	}
	viper.Reset()
}

func TestUpdateLockfile(t *testing.T) {
	tmp := t.TempDir()
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)
	_ = os.Chdir(tmp)
	// global false -> ./skills-lock.json
	if err := UpdateLockfile(false, "my-skill", "abc123"); err != nil {
		t.Fatalf("UpdateLockfile project: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(tmp, "skills-lock.json"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if !strings.Contains(string(b), "abc123") {
		t.Fatalf("lock missing hash: %s", string(b))
	}
	if !strings.Contains(string(b), "my-skill") {
		t.Fatalf("lock missing skill name: %s", string(b))
	}
	// global true -> ~/.agents/.skill-lock.json; use HOME trick
	home := filepath.Join(tmp, "home2")
	_ = os.MkdirAll(home, 0755)
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := UpdateLockfile(true, "global-skill", "hash999"); err != nil {
		t.Fatalf("global lock: %v", err)
	}
	gPath := filepath.Join(home, ".agents", ".skill-lock.json")
	if _, err := os.Stat(gPath); err != nil {
		t.Fatalf("global lock missing: %v", err)
	}
	gb, _ := os.ReadFile(gPath)
	if !strings.Contains(string(gb), "hash999") {
		t.Fatalf("global lock hash missing: %s", string(gb))
	}
}
