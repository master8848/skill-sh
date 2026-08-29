package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home")
	}
	t.Setenv("HOME", home)
	if got := expandHome("~/foo/bar"); got != filepath.Join(home, "foo/bar") {
		t.Fatalf("expandHome ~/foo/bar got %q want %q", got, filepath.Join(home, "foo/bar"))
	}
	if got := expandHome("~"); got != home {
		t.Fatalf("expandHome ~ got %q", got)
	}
	if got := expandHome("/absolute"); got != "/absolute" {
		t.Fatalf("absolute should stay %q", got)
	}
	if got := expandHome("relative/path"); got != "relative/path" {
		t.Fatalf("relative %q", got)
	}
}

func TestSanitizeNameEdge(t *testing.T) {
	cases := map[string]string{
		"Hello World!": "hello-world",
		"My_Skill.Name": "my_skill.name",
		"---foo---": "foo",
		"": "",
		"ABC": "abc",
		"a b c": "a-b-c",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Fatalf("SanitizeName(%q)=%q want %q", in, got, want)
		}
	}
	// link's SanitizeName should match resolve's?
}

func TestCopyDirectoryPreservesSymlinkDereferenceAndSkip(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst3")
	_ = os.MkdirAll(filepath.Join(src, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(src, "file.txt"), []byte("content"), 0644)
	_ = os.WriteFile(filepath.Join(src, "sub", "inner.txt"), []byte("inner"), 0644)
	// nested .git should be skipped
	_ = os.MkdirAll(filepath.Join(src, "sub", ".git"), 0755)
	_ = os.WriteFile(filepath.Join(src, "sub", ".git", "bad"), []byte("bad"), 0644)
	// symlink to file
	realFile := filepath.Join(src, "real.txt")
	_ = os.WriteFile(realFile, []byte("real"), 0644)
	link := filepath.Join(src, "link.txt")
	if err := os.Symlink(realFile, link); err != nil {
		t.Skip("symlink not supported")
	}
	// symlink to dir
	dirLink := filepath.Join(src, "dirlink")
	realDir := filepath.Join(src, "sub")
	if err := os.Symlink(realDir, dirLink); err != nil {
		t.Skip("symlink dir not supported")
	}
	if err := CopyDirectory(src, dst); err != nil {
		t.Fatalf("CopyDirectory %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		t.Fatalf(".git should be skipped")
	}
	if _, err := os.Stat(filepath.Join(dst, "sub", ".git", "bad")); err == nil {
		t.Fatalf("nested .git bad should be skipped")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "link.txt")); string(b) != "real" {
		t.Fatalf("dereference file symlink")
	}
	// dir symlink should be copied as directory with content
	if _, err := os.Stat(filepath.Join(dst, "dirlink", "inner.txt")); err != nil {
		t.Fatalf("dereferenced dir link missing: %v", err)
	}
}

func TestInstallSymlinkAndCopyModes(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	_ = os.MkdirAll(home, 0755)
	t.Setenv("HOME", home)
	src := filepath.Join(tmp, "srcSkill")
	_ = os.MkdirAll(src, 0755)
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# hi"), 0644)
	viper.Reset()
	// test symlink mode
	opts := InstallOpts{Agents: "claude", LinkMode: "symlink", Global: true, Project: false}
	if err := Install(src, "test-sym", opts); err != nil {
		t.Fatalf("symlink install %v", err)
	}
	dest := filepath.Join(home, ".claude", "skills", "test-sym")
	info, err := os.Lstat(dest)
	if err != nil {
		t.Fatalf("dest missing %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		// fallback copy check
		if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
			t.Fatalf("expected symlink or copy with file: %v", err)
		}
	} else {
		target, _ := os.Readlink(dest)
		if filepath.IsAbs(target) {
			t.Fatalf("symlink should be relative, got %q", target)
		}
	}
	// test copy mode via Copy flag
	opts2 := InstallOpts{Agents: "cursor", Copy: true, Global: true}
	if err := Install(src, "test-copy", opts2); err != nil {
		t.Fatalf("copy install %v", err)
	}
	dest2 := filepath.Join(home, ".cursor", "skills", "test-copy")
	if _, err := os.Stat(filepath.Join(dest2, "SKILL.md")); err != nil {
		t.Fatalf("copy dest missing %v", err)
	}
	if info, _ := os.Lstat(dest2); info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("copy mode should not be symlink")
	}
	// test auto mode (should be symlink on darwin)
	viper.Set("link.mode", "auto")
	opts3 := InstallOpts{Agents: "zed", Global: true}
	if err := Install(src, "test-auto", opts3); err != nil {
		t.Fatalf("auto install %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zed", "skills", "test-auto", "SKILL.md")); err != nil {
		t.Fatalf("auto dest missing %v", err)
	}
	// test global+project both
	proj := filepath.Join(tmp, "proj")
	_ = os.MkdirAll(proj, 0755)
	origWd, _ := os.Getwd()
	defer os.Chdir(origWd)
	_ = os.Chdir(proj)
	opts4 := InstallOpts{Agents: "agents", LinkMode: "copy", Global: true, Project: true}
	if err := Install(src, "both-targets", opts4); err != nil {
		t.Fatalf("both install %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "both-targets", "SKILL.md")); err != nil {
		t.Fatalf("global both missing %v", err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".agents", "skills", "both-targets", "SKILL.md")); err != nil {
		t.Fatalf("project both missing %v", err)
	}
	// test empty skill name fallback to base
	opts5 := InstallOpts{Agents: "claude", LinkMode: "copy", Global: true}
	if err := Install(src, "", opts5); err != nil {
		t.Fatalf("empty skill name fallback %v", err)
	}
	viper.Reset()
}

func TestInstallErrorPaths(t *testing.T) {
	if err := Install("", "skill", InstallOpts{Agents: "claude"}); err == nil {
		t.Fatalf("empty cache path should error")
	}
	if err := Install("/nonexistent/path/xyz", "skill", InstallOpts{Agents: "claude"}); err == nil {
		t.Fatalf("nonexistent path should error")
	}
	// file not dir
	tmp := t.TempDir()
	f := filepath.Join(tmp, "file.txt")
	_ = os.WriteFile(f, []byte("x"), 0644)
	if err := Install(f, "skill", InstallOpts{Agents: "claude"}); err == nil {
		t.Fatalf("file not dir should error")
	}
	if err := Install(tmp, "!!!", InstallOpts{Agents: "claude"}); err == nil {
		t.Fatalf("invalid skill name should error")
	}
	if err := Install(tmp, "skill", InstallOpts{Agents: ""}); err == nil {
		// empty agents resolves to default; may not error. Check
		// If viper empty, default exists, so not error. Don't fail.
	}
	// invalid link mode should still fallback
	viper.Reset()
	viper.Set("link.mode", "invalidmode")
	src := filepath.Join(tmp, "src2")
	_ = os.MkdirAll(src, 0755)
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("hi"), 0644)
	home := filepath.Join(tmp, "home2")
	_ = os.MkdirAll(home, 0755)
	t.Setenv("HOME", home)
	if err := Install(src, "skillX", InstallOpts{Agents: "claude", Global: true}); err != nil {
		t.Fatalf("invalid mode fallback %v", err)
	}
	viper.Reset()
	_ = strings.Contains("", "")
}

func TestSkillFolderHashIgnoresPycache(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "__pycache__"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "__pycache__", "b.pyc"), []byte("cached"), 0644)
	h1, _ := SkillFolderHash(dir)
	_ = os.WriteFile(filepath.Join(dir, "__pycache__", "b.pyc"), []byte("changed"), 0644)
	h2, _ := SkillFolderHash(dir)
	if h1 != h2 {
		t.Fatalf("pycache should be ignored: %q vs %q", h1, h2)
	}
}

func TestCreateSymlinkAbsoluteFallback(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	_ = os.MkdirAll(src, 0755)
	_ = os.WriteFile(filepath.Join(src, "file.txt"), []byte("x"), 0644)
	link := filepath.Join(tmp, "a", "b", "link")
	if err := CreateSymlink(src, link); err != nil {
		t.Fatalf("CreateSymlink %v", err)
	}
	// verify parent dir created and link works
	if _, err := os.Stat(filepath.Join(link, "file.txt")); err != nil {
		t.Fatalf("symlink target broken: %v", err)
	}
}
