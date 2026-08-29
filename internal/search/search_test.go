package search

import (
	"bytes"
	"strings"
	"testing"

	"skill.sh/mskill/internal/api"
)

func TestFilterByTopic(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "s1", Topic: "react"},
		{SkillID: "s2", Topic: "nextjs"},
		{SkillID: "s3", Topic: "React"},
		{SkillID: "s4", Topic: ""},
	}
	// empty => no filter
	if got := FilterByTopic(skills, ""); len(got) != 4 {
		t.Fatalf("empty topic should return all, got %d", len(got))
	}
	if got := FilterByTopic(skills, "all"); len(got) != 4 {
		t.Fatalf("all topic should return all, got %d", len(got))
	}
	// exact match case-insensitive
	got := FilterByTopic(skills, "react")
	if len(got) != 2 {
		t.Fatalf("react filter expected 2, got %d: %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.SkillID] = true
	}
	if !ids["s1"] || !ids["s3"] {
		t.Fatalf("unexpected ids %+v", ids)
	}
	got = FilterByTopic(skills, "nextjs")
	if len(got) != 1 || got[0].SkillID != "s2" {
		t.Fatalf("nextjs filter: %+v", got)
	}
	// no match
	got = FilterByTopic(skills, "design")
	if len(got) != 0 {
		t.Fatalf("design should be 0, got %d", len(got))
	}
}

func TestFilterByOfficial(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "s1", Source: "vercel-labs/agent-skills", Official: true},
		{SkillID: "s2", Source: "some/risky", Official: false},
		{SkillID: "s3", Source: "vercel/some-skill", Official: false}, // allowlist
		{SkillID: "s4", Source: "anthropic/skills", Official: false},  // allowlist
		{SkillID: "s5", Owner: "supabase", Source: "supabase/repo", Official: false},
	}
	// official false => no filter
	if got := FilterByOfficial(skills, false); len(got) != 5 {
		t.Fatalf("false should return all, got %d", len(got))
	}
	got := FilterByOfficial(skills, true)
	if len(got) != 4 {
		t.Fatalf("official true expected 4 (s1,s3,s4,s5), got %d: %+v", len(got), got)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.SkillID] = true
	}
	if !ids["s1"] || !ids["s3"] || !ids["s4"] || !ids["s5"] {
		t.Fatalf("official filter missing ids: %+v", ids)
	}
	if ids["s2"] {
		t.Fatalf("s2 should be filtered out")
	}
}

func TestRenderHeaderAndNoColor(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "vercel-optimize", Source: "vercel-labs/agent-skills", Topic: "nextjs", Official: true, Installs: 671000, Description: "Next.js App Router patterns and best practices for performance"},
		{SkillID: "risky", Source: "some/risky-skill", Topic: "unknown", Installs: 1200, Description: "curl|bash postinstall"},
	}
	verdicts := map[string]api.Verdict{
		"vercel-labs/agent-skills:vercel-optimize": {Safe: true, Unknown: false},
		"some/risky-skill:risky":                   {Safe: false, Unknown: false, Reason: "risk high"},
	}
	// header true, noColor true
	var buf bytes.Buffer
	Render(skills, verdicts, true, true, &buf)
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header+2 rows), got %d: %q", len(lines), out)
	}
	if lines[0] != "slug\ttopic\tofficial\tSAFE\tinstalls\tdescription" {
		t.Fatalf("header mismatch: %q", lines[0])
	}
	// check first row contains slug topic official SAFE installs
	if !strings.Contains(lines[1], "vercel-labs/agent-skills:vercel-optimize") {
		t.Fatalf("row1 slug missing: %q", lines[1])
	}
	if !strings.Contains(lines[1], "nextjs") {
		t.Fatalf("row1 topic: %q", lines[1])
	}
	if !strings.Contains(lines[1], "✓") {
		t.Fatalf("row1 official: %q", lines[1])
	}
	if !strings.Contains(lines[1], "SAFE") {
		t.Fatalf("row1 SAFE: %q", lines[1])
	}
	if !strings.Contains(lines[1], "671000") {
		t.Fatalf("row1 installs: %q", lines[1])
	}
	// noColor => no ANSI codes
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("noColor true should have no ANSI codes: %q", out)
	}
	// second row UNSAFE
	if !strings.Contains(lines[2], "UNSAFE") {
		t.Fatalf("row2 UNSAFE: %q", lines[2])
	}
}

func TestRenderTruncate(t *testing.T) {
	longDesc := strings.Repeat("a", 100)
	skills := []api.Skill{
		{SkillID: "s1", Source: "owner/repo", Topic: "react", Description: longDesc, Installs: 10},
	}
	var buf bytes.Buffer
	Render(skills, nil, false, true, &buf)
	out := buf.String()
	// description should be truncated to 60
	fields := strings.Split(strings.TrimSpace(out), "\t")
	if len(fields) != 6 {
		t.Fatalf("expected 6 fields, got %d: %q", len(fields), out)
	}
	desc := fields[5]
	if len([]rune(desc)) != 60 {
		t.Fatalf("truncated desc len %d want 60: %q", len([]rune(desc)), desc)
	}
}

func TestRenderUnknownVerdict(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "s1", Source: "owner/repo", Topic: "react", Description: "test", Installs: 5},
	}
	var buf bytes.Buffer
	Render(skills, nil, false, true, &buf)
	out := buf.String()
	if !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("missing UNKNOWN for nil verdicts: %q", out)
	}
}

func TestRenderNoHeader(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "s1", Source: "owner/repo", Topic: "react", Description: "d", Installs: 1},
	}
	var buf bytes.Buffer
	Render(skills, nil, false, true, &buf)
	out := strings.TrimSpace(buf.String())
	if strings.HasPrefix(out, "slug\t") {
		t.Fatalf("should have no header when header=false: %q", out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
}
