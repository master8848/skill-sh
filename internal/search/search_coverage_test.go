package search

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"skill.sh/mskill/internal/api"
)

func TestTSVParseabilityCutF(t *testing.T) {
	// Simulate mskill search TSV without header -> cut -f1,4 should work
	skills := []api.Skill{
		{SkillID: "vercel-optimize", Source: "vercel-labs/agent-skills", Topic: "nextjs", Official: true, Installs: 671000, Description: "Next.js best practices"},
		{SkillID: "risky", Source: "evil/repo", Topic: "unknown", Installs: 1200, Description: "curl|bash"},
		{SkillID: "react-skill", Source: "owner/repo", Topic: "react", Installs: 100, Description: "react desc"},
	}
	verdicts := map[string]api.Verdict{
		"vercel-labs/agent-skills:vercel-optimize": {Safe: true},
		"evil/repo:risky":                          {Safe: false},
		"owner/repo:react-skill":                   {Unknown: true},
	}
	var buf bytes.Buffer
	Render(skills, verdicts, false, true, &buf)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 TSV lines, got %d: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			t.Fatalf("line %d: expected 6 TSV fields, got %d: %q", i, len(fields), line)
		}
		// verify no header contamination, first field should contain colon/slash
		if fields[0] == "slug" {
			t.Fatalf("line %d should not be header when header=false", i)
		}
		// simulate cut -f1 (slug)
		if !strings.Contains(fields[0], "/") && !strings.Contains(fields[0], ":") {
			t.Fatalf("slug field invalid: %q", fields[0])
		}
		// cut -f4 should be SAFE/UNSAFE/UNKNOWN
		if fields[3] != "SAFE" && fields[3] != "UNSAFE" && fields[3] != "UNKNOWN" {
			t.Fatalf("field 4 SAFE value invalid: %q", fields[3])
		}
		// cut -f1,4 via awk simulation
		slug := fields[0]
		verdict := fields[3]
		if slug == "" || verdict == "" {
			t.Fatalf("cut simulation failed")
		}
	}
	// with header true, first line is header
	var buf2 bytes.Buffer
	Render(skills, verdicts, true, true, &buf2)
	lines2 := strings.Split(strings.TrimSpace(buf2.String()), "\n")
	if lines2[0] != "slug\ttopic\tofficial\tSAFE\tinstalls\tdescription" {
		t.Fatalf("header mismatch %q", lines2[0])
	}
	// verify header also TSV parseable (6 fields)
	if got := len(strings.Split(lines2[0], "\t")); got != 6 {
		t.Fatalf("header TSV fields %d", got)
	}
}

func TestTSVTopicOfficialFiltersCombined(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "s1", Source: "vercel-labs/agent-skills", Topic: "react", Official: true, Installs: 100, Description: "react official"},
		{SkillID: "s2", Source: "owner/repo", Topic: "react", Official: false, Installs: 50, Description: "react unofficial"},
		{SkillID: "s3", Source: "vercel/something", Topic: "nextjs", Official: false, Installs: 200, Description: "nextjs via allowlist"},
	}
	// topic filter react + official filter: should give only s1
	filtered := FilterByTopic(skills, "react")
	filtered = FilterByOfficial(filtered, true)
	if len(filtered) != 1 || filtered[0].SkillID != "s1" {
		t.Fatalf("combined filter failed: got %v", filtered)
	}
	// only official, no topic => s1 and s3 (s3 allowlisted)
	officialOnly := FilterByOfficial(skills, true)
	if len(officialOnly) != 2 {
		t.Fatalf("official only expected 2, got %d", len(officialOnly))
	}
}

func TestRenderColorAndTruncateAndNoColorEnv(t *testing.T) {
	skills := []api.Skill{
		{SkillID: "s1", Source: "owner/repo", Topic: "react", Official: true, Description: strings.Repeat("a", 100), Installs: 10},
		{SkillID: "s2", Source: "owner2/repo2", Topic: "", Description: "short", Installs: 5},
	}
	verdicts := map[string]api.Verdict{
		"owner/repo:s1":   {Safe: true},
		"owner2/repo2:s2": {Safe: false},
	}
	// with NO_COLOR env, should disable color even if noColor false
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	Render(skills, verdicts, false, false, &buf)
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("NO_COLOR should disable ANSI: %q", buf.String())
	}
	t.Setenv("NO_COLOR", "")
	// with TERM=dumb
	t.Setenv("TERM", "dumb")
	buf.Reset()
	Render(skills, verdicts, false, false, &buf)
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("TERM=dumb should disable color")
	}
	t.Setenv("TERM", "")
	// topic empty should stay empty (not "unknown") to keep TSV clean for `cut -f`/`awk -F'\t'`
	if strings.Contains(buf.String(), "\tunknown\t") {
		t.Fatalf("empty topic should not be unknown placeholder: %q", buf.String())
	}
	// verify second skill (empty topic) has empty field 2
	linesEmpty := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(linesEmpty) >= 2 {
		f2 := strings.Split(linesEmpty[1], "\t")
		if len(f2) >= 2 && f2[1] != "" {
			t.Fatalf("empty topic should render empty tab, got %q in %q", f2[1], linesEmpty[1])
		}
	}
	// description truncation 60
	fields := strings.Split(strings.TrimSpace(buf.String()), "\n")
	firstFields := strings.Split(fields[0], "\t")
	if len([]rune(firstFields[5])) != 60 {
		t.Fatalf("truncated len %d", len([]rune(firstFields[5])))
	}
	// test colorize helpers directly
	if got := colorizeVerdict("SAFE", true); !strings.Contains(got, "\x1b[32m") {
		// only when isTTY? But colorizeVerdict doesn't check TTY, so should still colorize when enableColor true
		t.Fatalf("colorize SAFE enable failed %q", got)
	}
	if got := colorizeVerdict("UNSAFE", false); got != "UNSAFE" {
		t.Fatalf("no color should return plain")
	}
	if got := colorizeOfficial("✓", false); got != "✓" {
		t.Fatalf("no color official %q", got)
	}
	if got := colorizeOfficial("", true); got != "" {
		t.Fatalf("empty official %q", got)
	}
}

func TestIsTTYAndOfficialSkill(t *testing.T) {
	// In test, stdout is not terminal, so isTTY should be false
	_ = isTTY()
	// Test official allowlist via helper
	_ = isOfficialSkill(api.Skill{Source: "vercel-labs/agent-skills", Official: false})
	if !isOfficialSkill(api.Skill{Source: "vercel/test", Official: false}) {
		t.Fatalf("vercel should be official via allowlist")
	}
	if isOfficialSkill(api.Skill{Source: "unknown/repo", Official: false}) {
		t.Fatalf("unknown should not be official")
	}
	if !isOfficialSkill(api.Skill{Official: true, Source: "any/repo"}) {
		t.Fatalf("explicit official true")
	}
	if isOfficialSkill(api.Skill{Source: "", Owner: ""}) {
		t.Fatalf("empty should not be official")
	}
	if !isOfficialSkill(api.Skill{Source: "", Owner: "supabase", Official: false}) {
		t.Fatalf("owner supabase")
	}
}

func TestRenderVerdictFallbackKeys(t *testing.T) {
	skills := []api.Skill{
		{ID: "id1", Name: "fallback", Topic: "react", Installs: 1},
		{Source: "owner/repo", SkillID: "my-skill", Topic: "react", Installs: 2},
	}
	verdicts := map[string]api.Verdict{
		"id1":      {Safe: true},
		"my-skill": {Safe: false},
	}
	var buf bytes.Buffer
	Render(skills, verdicts, false, true, &buf)
	out := buf.String()
	if !strings.Contains(out, "SAFE") || !strings.Contains(out, "UNSAFE") {
		t.Fatalf("fallback keys: %q", out)
	}
	// also test unknown when no entry
	var buf2 bytes.Buffer
	Render([]api.Skill{{Source: "a/b", SkillID: "c", Topic: "react", Installs: 1}}, nil, false, true, &buf2)
	if !strings.Contains(buf2.String(), "UNKNOWN") {
		t.Fatalf("nil verdicts should be UNKNOWN")
	}
}

func TestTruncateDescEdge(t *testing.T) {
	if got := truncateDesc("a\nb\tc", 0); got != "a b c" {
		t.Fatalf("zero max should return full cleaned: %q", got)
	}
	if got := truncateDesc("hello", 10); got != "hello" {
		t.Fatalf("short no trunc %q", got)
	}
	if got := truncateDesc("   hello world  ", 5); got != "hello" {
		t.Fatalf("trim/trunc %q", got)
	}
}

func TestVerdictToString(t *testing.T) {
	if verdictToString(api.Verdict{Unknown: true}) != "UNKNOWN" {
		t.Fatalf("unknown")
	}
	if verdictToString(api.Verdict{Safe: true}) != "SAFE" {
		t.Fatalf("safe")
	}
	if verdictToString(api.Verdict{Safe: false, Unknown: false}) != "UNSAFE" {
		t.Fatalf("unsafe")
	}
}

func TestRenderOutputNilWriter(t *testing.T) {
	// should not panic when w is nil (falls back to os.Stdout)
	// we can't capture os.Stdout easily, but ensure no panic
	orig := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	Render([]api.Skill{{Source: "a/b", SkillID: "c", Topic: "react"}}, nil, false, true, nil)
	w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	r.Close()
	// not checking content, just ensured no panic
}
