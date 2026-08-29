package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchWithMock(t *testing.T) {
	// Mock server
	skills := []Skill{
		{ID: "1", SkillID: "vercel-optimize", Name: "optimize", Source: "vercel-labs/agent-skills", Installs: 100, Official: true},
		{ID: "2", SkillID: "other", Name: "other", Source: "some/repo", Installs: 10},
	}
	resp := SearchResponse{Skills: skills, Count: 2}
	b, _ := json.Marshal(resp)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query().Get("q")
		if q != "vercel" {
			// still respond
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	defer ts.Close()
	t.Setenv("SKILLS_API_URL", ts.URL)

	got, err := Search(context.Background(), "vercel", "", 20)
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(got))
	}
	if got[0].SkillID != "vercel-optimize" {
		t.Fatalf("first skill mismatch: %+v", got[0])
	}
}

func TestAuditUnknownOnNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	defer ts.Close()
	t.Setenv("AUDIT_URL", ts.URL)

	res, err := Audit(context.Background(), "owner/repo", []string{"my-skill"})
	if err != nil {
		t.Fatalf("Audit err: %v", err)
	}
	v := res["my-skill"]
	if !v.Unknown {
		t.Fatalf("expected unknown on 500, got %+v", v)
	}
	if v.Safe {
		t.Fatalf("expected not safe on unknown")
	}
}

func TestAuditSafeVerdict(t *testing.T) {
	raw := `{"my-skill":{"ath":{"risk":"safe"},"socket":{"risk":"safe","alerts":0,"score":90},"snyk":{"risk":"low"},"zeroleaks":{"risk":"safe","score":95}}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer ts.Close()
	t.Setenv("AUDIT_URL", ts.URL)

	res, _ := Audit(context.Background(), "owner/repo", []string{"my-skill"})
	v := res["my-skill"]
	if v.Unknown || !v.Safe {
		t.Fatalf("expected safe, got %+v", v)
	}
}

func TestAuditUnsafeOnCritical(t *testing.T) {
	raw := `{"my-skill":{"ath":{"risk":"critical"},"socket":{"risk":"safe","alerts":0,"score":90},"snyk":{"risk":"low"},"zeroleaks":{"risk":"safe","score":95}}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer ts.Close()
	t.Setenv("AUDIT_URL", ts.URL)

	res, _ := Audit(context.Background(), "owner/repo", []string{"my-skill"})
	v := res["my-skill"]
	if v.Safe || v.Unknown {
		t.Fatalf("expected unsafe on critical, got %+v", v)
	}
}

func TestAuditUnsafeOnAlerts(t *testing.T) {
	raw := `{"my-skill":{"ath":{"risk":"safe"},"socket":{"risk":"safe","alerts":1,"score":90},"snyk":{"risk":"low"},"zeroleaks":{"risk":"safe","score":95}}}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(raw))
	}))
	defer ts.Close()
	t.Setenv("AUDIT_URL", ts.URL)

	res, _ := Audit(context.Background(), "owner/repo", []string{"my-skill"})
	v := res["my-skill"]
	if v.Safe {
		t.Fatalf("expected unsafe on alerts, got %+v", v)
	}
}
