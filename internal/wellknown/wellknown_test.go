package wellknown

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeedURLCandidates(t *testing.T) {
	c := FeedURLCandidates("https://example.com/.well-known/agent-skills")
	if len(c) == 0 || !strings.HasSuffix(c[0], "/index.json") {
		t.Fatalf("candidates: %v", c)
	}
	c2 := FeedURLCandidates("example.com")
	found := false
	for _, u := range c2 {
		if u == "https://example.com/.well-known/agent-skills/index.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("bare domain candidates missing agent-skills: %v", c2)
	}
	c3 := FeedURLCandidates("well-known:https://example.com")
	if len(c3) == 0 {
		t.Fatalf("well-known: prefix yields no candidates")
	}
}

func TestFetchIndexAndSelect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-skills/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"baseUrl":"http://` + r.Host + `","skills":[{"name":"alpha","description":"a","version":"1.0.0"},{"name":"beta","description":"b","version":"2.0.0","url":"http://` + r.Host + `/custom/beta.md"}]}`))
	})
	mux.HandleFunc("/skills/alpha/SKILL.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Alpha\ncontent"))
	})
	mux.HandleFunc("/custom/beta.md", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# Beta\ncontent"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx := context.Background()
	idx, indexURL, err := FetchIndex(ctx, ts.URL+"/.well-known/agent-skills")
	if err != nil {
		t.Fatalf("FetchIndex: %v", err)
	}
	if len(idx.Skills) != 2 {
		t.Fatalf("skills: %+v", idx.Skills)
	}
	sel, err := Select(idx, []string{"alpha"}, "")
	if err != nil || len(sel) != 1 || sel[0].Name != "alpha" {
		t.Fatalf("select alpha: %v %+v", err, sel)
	}
	// Version pin.
	if _, err := Select(idx, []string{"alpha"}, "9.9.9"); err == nil {
		t.Fatalf("expected version mismatch error")
	}
	sel, err = Select(idx, []string{"beta"}, "2.0.0")
	if err != nil || len(sel) != 1 {
		t.Fatalf("select beta@2.0.0: %v", err)
	}
	md, err := FetchSkillMD(ctx, indexURL, sel[0])
	if err != nil || !strings.Contains(string(md), "Beta") {
		t.Fatalf("FetchSkillMD beta: %v %s", err, md)
	}
	md, err = FetchSkillMD(ctx, indexURL, idx.Skills[0])
	if err != nil || !strings.Contains(string(md), "Alpha") {
		t.Fatalf("FetchSkillMD alpha: %v %s", err, md)
	}
	if _, err := Select(idx, []string{"missing"}, ""); err == nil {
		t.Fatalf("expected missing skill error")
	}
}

func TestBuildAndWriteFeed(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "s1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "s1", "SKILL.md"), []byte("---\nname: s1\ndescription: first\nversion: 1.2.0\n---\n# S1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "s2"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "s2", "SKILL.md"), []byte("# S2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	idx, dirs, err := BuildFeed(src, "https://example.com", "9.9.9")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	if len(idx.Skills) != 2 {
		t.Fatalf("skills: %+v", idx.Skills)
	}
	for _, e := range idx.Skills {
		if e.Name == "s1" && e.Version != "1.2.0" {
			t.Fatalf("frontmatter version lost: %+v", e)
		}
		if e.Name == "s2" && e.Version != "9.9.9" {
			t.Fatalf("default version not applied: %+v", e)
		}
	}
	out := t.TempDir()
	indexPath, err := WriteFeed(out, idx, dirs)
	if err != nil {
		t.Fatalf("WriteFeed: %v", err)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, ".well-known", "skills", "index.json")); err != nil {
		t.Fatalf("legacy index missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "skills", "s1", "SKILL.md")); err != nil {
		t.Fatalf("skill copy missing: %v", err)
	}
	// Written index must be fetchable.
	parsed, err := parseIndex(mustRead(t, indexPath))
	if err != nil || len(parsed.Skills) != 2 {
		t.Fatalf("parse written index: %v %+v", err, parsed)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
