package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFeedCommandBuildsIndex(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	src := t.TempDir()
	skillDir := filepath.Join(src, "hello")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: hello\ndescription: hi\n---\n# Hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "public")
	dot := filepath.Join(t.TempDir(), ".mskill")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"feed", src, "--base-url", "https://example.com", "--out", out, "--version", "2.0.0"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("feed: %v out=%s", err, buf.String())
	}
	if _, err := os.Stat(filepath.Join(out, ".well-known", "agent-skills", "index.json")); err != nil {
		t.Fatalf("index.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "skills", "hello", "SKILL.md")); err != nil {
		t.Fatalf("skill copy missing: %v", err)
	}
	rootCmd.SetArgs([]string{})
}
