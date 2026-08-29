package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
)

func resetRootCmd(t *testing.T) {
	t.Helper()
	viper.Reset()
	viper.SetEnvPrefix("MSKILL")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	cfgFile = ""
	cacheDirOverride = ""
	verbose = false
	noColor = false
	paths = config.Paths{}
	rootCmd.SetArgs(nil)
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	// reset flags to defaults for root and all subcommands
	resetFlags(rootCmd)
	for _, c := range rootCmd.Commands() {
		resetFlags(c)
		// also reset sub-subcommands if any (trust etc.)
		for _, sub := range c.Commands() {
			resetFlags(sub)
		}
	}
}

func resetFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			// For slice/array flags, setting DefValue "[]" would corrupt (adds element "[]").
			// Only reset Changed and let next parse handle it; but ensure help bool is reset.
			if f.Value.Type() == "bool" || f.Name == "help" {
				_ = f.Value.Set(f.DefValue)
			} else if f.Value.Type() != "stringArray" && f.Value.Type() != "stringSlice" {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
	})
	c.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			if f.Value.Type() == "bool" || f.Name == "help" {
				_ = f.Value.Set(f.DefValue)
			} else if f.Value.Type() != "stringArray" && f.Value.Type() != "stringSlice" {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
	})
	// InheritedFlags are same underlying flags, already handled; no need to double-reset.
	// Also explicitly ensure help flag is false
	if f := c.Flags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
		f.Changed = false
	}
	if f := c.PersistentFlags().Lookup("help"); f != nil {
		_ = f.Value.Set("false")
		f.Changed = false
	}
}

func TestRootHelp(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"--help"})
	_ = rootCmd.Execute()
	out := buf.String()
	if !strings.Contains(out, "mskill") {
		t.Fatalf("help missing mskill: %s", out)
	}
	if !strings.Contains(out, "get") || !strings.Contains(out, "search") {
		t.Fatalf("help missing commands: %s", out)
	}
	// reset
	rootCmd.SetArgs([]string{})
}

func TestCachePathHonorsEnv(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	tmpCache := t.TempDir()
	t.Setenv("MSKILL_CACHE_DIR", tmpCache)
	t.Setenv("MSKILL_DOT_DIR", filepath.Join(t.TempDir(), ".mskill"))
	// also set XDG to ensure override wins
	p, err := config.ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.CacheDir != tmpCache {
		t.Fatalf("CacheDir override failed: got %q want %q", p.CacheDir, tmpCache)
	}
}

func TestSearchHelpFlags(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"search", "--help"})
	_ = rootCmd.Execute()
	out := buf.String()
	for _, flag := range []string{"--topic", "--official", "--owner", "--limit", "--header"} {
		if !strings.Contains(out, flag) {
			t.Fatalf("search --help missing %s: %s", flag, out)
		}
	}
	if strings.Contains(out, "--json") {
		t.Fatalf("search should not have --json flag")
	}
	rootCmd.SetArgs([]string{})
}

func TestGetHelpFlags(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"get", "--help"})
	_ = rootCmd.Execute()
	out := buf.String()
	for _, flag := range []string{"--global", "--project", "--agent", "--skill", "--ref", "--copy", "--show", "--file", "--force"} {
		if !strings.Contains(out, flag) {
			t.Fatalf("get --help missing %s: %s", flag, out)
		}
	}
	rootCmd.SetArgs([]string{})
}

func TestSearchIntegrationMock(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	skills := []map[string]interface{}{
		{"id": "1", "skillId": "vercel-optimize", "name": "optimize", "source": "vercel-labs/agent-skills", "installs": 100, "official": true, "topic": "nextjs", "description": "Next.js optimize"},
		{"id": "2", "skillId": "risky-skill", "name": "risky", "source": "evil/repo", "installs": 5, "official": false, "topic": "databases", "description": "evil"},
	}
	searchResp := map[string]interface{}{"skills": skills, "count": 2}
	searchBody, _ := json.Marshal(searchResp)
	auditBody := `{"vercel-optimize":{"ath":{"risk":"safe"},"socket":{"risk":"safe","alerts":0,"score":90},"snyk":{"risk":"low"},"zeroleaks":{"risk":"safe","score":95}},"risky-skill":{"ath":{"risk":"critical"},"socket":{"risk":"high","alerts":1,"score":30},"snyk":{"risk":"high"},"zeroleaks":{"risk":"high","score":40}}}`

	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(searchBody)
	})
	mux.HandleFunc("/audit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(auditBody))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	t.Setenv("SKILLS_API_URL", ts.URL)
	t.Setenv("AUDIT_URL", ts.URL)
	// isolate config/cache
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", cacheDir)
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	// need to ensure persistent prerun runs (it sets paths via env). Execute search.
	rootCmd.SetArgs([]string{"search", "vercel", "--limit", "2", "--header", "--no-color"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("search execute: %v out=%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "vercel-optimize") {
		t.Fatalf("search output missing vercel-optimize: %s", out)
	}
	if !strings.Contains(out, "SAFE") || !strings.Contains(out, "UNSAFE") {
		t.Fatalf("search output missing SAFE/UNSAFE: %s", out)
	}
	if !strings.Contains(out, "slug") {
		t.Fatalf("header missing when --header set: %s", out)
	}
	rootCmd.SetArgs([]string{})
}

func TestSearchTopicFilter(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	skills := []map[string]interface{}{
		{"id": "1", "skillId": "react-skill", "name": "react", "source": "owner/repo1", "installs": 100, "official": false, "topic": "react"},
		{"id": "2", "skillId": "next-skill", "name": "next", "source": "owner/repo2", "installs": 90, "official": false, "topic": "nextjs"},
	}
	searchResp := map[string]interface{}{"skills": skills, "count": 2}
	body, _ := json.Marshal(searchResp)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer ts.Close()
	// also audit mock for safe
	auditBody := `{"react-skill":{"ath":{"risk":"safe"},"socket":{"risk":"safe","alerts":0,"score":90},"snyk":{"risk":"low"},"zeroleaks":{"risk":"safe","score":95}}}`
	auditTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(auditBody))
	}))
	defer auditTS.Close()
	t.Setenv("SKILLS_API_URL", ts.URL)
	t.Setenv("AUDIT_URL", auditTS.URL)
	t.Setenv("MSKILL_DOT_DIR", filepath.Join(t.TempDir(), ".mskill"))
	t.Setenv("MSKILL_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("MSKILL_CONFIG", filepath.Join(t.TempDir(), ".mskill", "config.yaml"))

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"search", "--topic", "react", "--limit", "10", "--no-color"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("search topic: %v out=%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "react-skill") {
		t.Fatalf("filtered output missing react-skill: %s", out)
	}
	if strings.Contains(out, "next-skill") {
		t.Fatalf("filtered output should not contain next-skill when topic=react: %s", out)
	}
	rootCmd.SetArgs([]string{})
}

func TestTrustStatusNoHash(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", cacheDir)
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))
	// ensure clean
	_ = os.MkdirAll(dot, 0700)
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"trust", "status"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("trust status: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Trust:") || !strings.Contains(out, "Password:") {
		t.Fatalf("trust status output wrong: %s", out)
	}
	if strings.Contains(strings.ToLower(out), "hash") {
		t.Fatalf("trust status should not print hash: %s", out)
	}
	rootCmd.SetArgs([]string{})
}

func TestCachePathCommand(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "mycache")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", cacheDir)
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"cache", "path"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("cache path: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, cacheDir) {
		t.Fatalf("cache path output missing cacheDir %s: %s", cacheDir, out)
	}
	rootCmd.SetArgs([]string{})
}

func TestGetShowLocal(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	// create local skill dir with SKILL.md
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Hello Skill\nContent here"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "README.md"), []byte("readme"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", cacheDir)
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	// mskill get ./my-skill --show --file SKILL.md (local)
	localPath := filepath.Join(dir, "my-skill")
	rootCmd.SetArgs([]string{"get", localPath, "--show"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("get local show: %v out=%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "Hello Skill") {
		t.Fatalf("get local show missing content: %s", out)
	}
	rootCmd.SetArgs([]string{})
}

func TestShowListLocal(t *testing.T) {
	resetRootCmd(t)
	defer resetRootCmd(t)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill-a")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("a"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "extra.txt"), []byte("extra"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	dot := filepath.Join(t.TempDir(), ".mskill")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("MSKILL_DOT_DIR", dot)
	t.Setenv("MSKILL_CACHE_DIR", cacheDir)
	t.Setenv("MSKILL_CONFIG", filepath.Join(dot, "config.yaml"))
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"show", skillDir, "--list"})
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("show list local: %v out=%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "SKILL.md") || !strings.Contains(out, "extra.txt") {
		t.Fatalf("show list missing files: %s", out)
	}
	rootCmd.SetArgs([]string{})
}
