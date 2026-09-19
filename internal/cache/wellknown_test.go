package cache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"skill.sh/mskill/internal/config"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/wellknown"
)

func TestEnsureWellKnownRoundTrip(t *testing.T) {
	src := t.TempDir()
	skillDir := filepath.Join(src, "demo")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: demo skill\nversion: 1.0.0\n---\n# Demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Build feed with placeholder origin, then fix up after server starts.
	idx, dirs, err := wellknown.BuildFeed(src, "https://example.com", "1.0.0")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	pub := t.TempDir()
	// Temporarily build with dummy, then rewrite URLs after server URL known.
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.FileServer(http.Dir(pub)).ServeHTTP(w, r)
	}))
	defer ts.Close()
	idx2, dirs2, err := wellknown.BuildFeed(src, ts.URL, "1.0.0")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	_ = idx
	_ = dirs
	if _, err := wellknown.WriteFeed(pub, idx2, dirs2); err != nil {
		t.Fatalf("WriteFeed: %v", err)
	}

	paths := config.Paths{CacheDir: t.TempDir(), DotDir: t.TempDir(), ConfigFile: filepath.Join(t.TempDir(), "config.yaml")}
	r, err := resolve.ParseSkillRef(ts.URL + "/.well-known/agent-skills")
	if err != nil {
		t.Fatalf("ParseSkillRef: %v", err)
	}
	if r.Type != "well-known" {
		t.Fatalf("type = %q, want well-known", r.Type)
	}
	ctx := context.Background()
	cachePath, meta, err := Ensure(ctx, paths, r, "", []string{"demo"}, false)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(cachePath, "demo", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md missing in cache: %v", err)
	}
	if string(b) == "" || !containsStr(string(b), "Demo") {
		t.Fatalf("unexpected SKILL.md: %q", string(b))
	}
	if meta.Filter != "http" {
		t.Fatalf("meta filter = %q, want http", meta.Filter)
	}
	skills, err := ListSkills(ctx, cachePath)
	if err != nil || len(skills) == 0 {
		t.Fatalf("ListSkills: %v %v", skills, err)
	}
	// Version pin mismatch must fail.
	if _, _, err := Ensure(ctx, paths, r, "9.9.9", []string{"demo"}, true); err == nil {
		t.Fatalf("expected version mismatch error")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
