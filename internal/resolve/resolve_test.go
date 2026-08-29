package resolve

import (
	"os"
	"testing"
)

func TestParseSkillRef_Table(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantRepo  string
		wantSkill string
		wantSlug  string
		wantRef   string
		wantHost  string
		wantType  string
		wantLocal bool
	}{
		{
			name:      "shorthand owner/repo/skill",
			input:     "vercel-labs/agent-skills/vercel-optimize",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantSkill: "vercel-optimize",
			wantSlug:  "vercel-optimize",
			wantHost:  "github.com",
			wantType:  "git",
		},
		{
			name:      "shorthand owner/repo",
			input:     "vercel-labs/agent-skills",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantSkill: "",
			wantSlug:  "",
			wantHost:  "github.com",
			wantType:  "git",
		},
		{
			name:      "github prefix",
			input:     "github:vercel-labs/agent-skills",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantHost:  "github.com",
			wantType:  "git",
		},
		{
			name:      "gitlab prefix",
			input:     "gitlab:mygroup/myrepo",
			wantOwner: "mygroup",
			wantRepo:  "myrepo",
			wantHost:  "gitlab.com",
			wantType:  "git",
		},
		{
			name:      "local path ./my-skill",
			input:     "./my-skill",
			wantHost:  "local",
			wantType:  "local",
			wantLocal: true,
		},
		{
			name:      "http tree URL",
			input:     "https://github.com/vercel-labs/agent-skills/tree/main/skills/vercel-optimize",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantSkill: "skills/vercel-optimize",
			wantSlug:  "vercel-optimize",
			wantRef:   "main",
			wantHost:  "github.com",
			wantType:  "git",
		},
		{
			name:     "hosted artifact URL raw",
			input:    "https://raw.githubusercontent.com/vercel-labs/agent-skills/main/SKILL.md",
			wantHost: "raw.githubusercontent.com",
			wantType: "download",
		},
		{
			name:     "hosted artifact codeload",
			input:    "https://codeload.github.com/vercel-labs/agent-skills/zip/main",
			wantHost: "codeload.github.com",
			wantType: "download",
		},
		{
			name:     "hosted artifact archive",
			input:    "https://github.com/vercel-labs/agent-skills/archive/refs/heads/main.zip",
			wantType: "download",
		},
		{
			name:      "alias coinbase/agentWallet",
			input:     "coinbase/agentWallet",
			wantOwner: "coinbase",
			wantRepo:  "agentWallet",
			wantHost:  "github.com",
			wantType:  "git",
		},
		{
			name:      "shorthand with at filter",
			input:     "vercel-labs/agent-skills@my-skill",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantSkill: "my-skill",
			wantSlug:  "my-skill",
			wantType:  "git",
		},
		{
			name:      "fragment ref handling",
			input:     "vercel-labs/agent-skills#main",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantRef:   "main",
			wantType:  "git",
		},
		{
			name:      "fragment ref with skill filter",
			input:     "vercel-labs/agent-skills#main@my-skill",
			wantOwner: "vercel-labs",
			wantRepo:  "agent-skills",
			wantSkill: "my-skill",
			wantSlug:  "my-skill",
			wantRef:   "main",
			wantType:  "git",
		},
		{
			name:     "well-known url",
			input:    "https://example.com/.well-known/skills",
			wantType: "well-known",
			wantHost: "example.com",
		},
		{
			name:     "well-known agent-skills",
			input:    "https://example.com/.well-known/agent-skills",
			wantType: "well-known",
			wantHost: "example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSkillRef(tc.input)
			if err != nil {
				t.Fatalf("ParseSkillRef(%q) error: %v", tc.input, err)
			}
			if tc.wantOwner != "" && got.Owner != tc.wantOwner {
				t.Errorf("Owner got %q want %q", got.Owner, tc.wantOwner)
			}
			if tc.wantRepo != "" && got.Repo != tc.wantRepo {
				t.Errorf("Repo got %q want %q", got.Repo, tc.wantRepo)
			}
			if tc.wantSkill != "" && got.SkillPath != tc.wantSkill {
				t.Errorf("SkillPath got %q want %q", got.SkillPath, tc.wantSkill)
			}
			if tc.wantSlug != "" && got.Slug != tc.wantSlug {
				t.Errorf("Slug got %q want %q", got.Slug, tc.wantSlug)
			}
			if tc.wantRef != "" && got.Ref != tc.wantRef {
				t.Errorf("Ref got %q want %q", got.Ref, tc.wantRef)
			}
			if tc.wantHost != "" && got.Host != tc.wantHost {
				t.Errorf("Host got %q want %q", got.Host, tc.wantHost)
			}
			if tc.wantType != "" && got.Type != tc.wantType {
				t.Errorf("Type got %q want %q", got.Type, tc.wantType)
			}
			if got.IsLocal != tc.wantLocal {
				t.Errorf("IsLocal got %v want %v", got.IsLocal, tc.wantLocal)
			}
			// Safety: SkillPath must be sanitized
			if got.SkillPath != "" {
				if _, err := SanitizeSubpath(got.SkillPath); err != nil {
					t.Errorf("SkillPath %q not sanitized: %v", got.SkillPath, err)
				}
			}
		})
	}
}

func TestSanitizeSubpath_RejectDotDot(t *testing.T) {
	cases := []string{"../etc", "a/../b", "skill/../../etc", "..", "a/b/..", "foo/../bar"}
	for _, c := range cases {
		if _, err := SanitizeSubpath(c); err == nil {
			t.Errorf("SanitizeSubpath(%q) expected error, got nil", c)
		}
	}
	// valid cases should not error
	valid := []string{"skills/vercel-optimize", "a/b/c", "", "skill", "a/./b"}
	for _, c := range valid {
		if _, err := SanitizeSubpath(c); err != nil {
			t.Errorf("SanitizeSubpath(%q) unexpected error: %v", c, err)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"My Skill!", "my-skill"},
		{"Hello_World", "hello_world"},
		{"--foo--", "foo"},
		{"Test.Name", "test.name"},
		{"ABC", "abc"},
	}
	for _, c := range cases {
		got := SanitizeName(c.in)
		if got != c.want {
			t.Errorf("SanitizeName(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestGetOwnerRepo(t *testing.T) {
	cases := []struct {
		src       string
		wantOwner string
		wantRepo  string
	}{
		{"vercel-labs/agent-skills", "vercel-labs", "agent-skills"},
		{"https://github.com/vercel-labs/agent-skills/tree/main/foo", "vercel-labs", "agent-skills"},
		{"github:vercel-labs/agent-skills", "vercel-labs", "agent-skills"},
		{"vercel-labs/agent-skills.git", "vercel-labs", "agent-skills"},
	}
	for _, c := range cases {
		o, r := GetOwnerRepo(c.src)
		if o != c.wantOwner || r != c.wantRepo {
			t.Errorf("GetOwnerRepo(%q)=%q/%q want %q/%q", c.src, o, r, c.wantOwner, c.wantRepo)
		}
	}
}

func TestGHHostHandling(t *testing.T) {
	orig := os.Getenv("GH_HOST")
	defer os.Setenv("GH_HOST", orig)
	os.Setenv("GH_HOST", "github.example.com")
	got, err := ParseSkillRef("myowner/myrepo/myskill")
	if err != nil {
		t.Fatalf("ParseSkillRef error: %v", err)
	}
	if got.Host != "github.example.com" {
		t.Errorf("GH_HOST Host got %q want %q", got.Host, "github.example.com")
	}
	if got.CloneURL != "https://github.example.com/myowner/myrepo.git" {
		t.Errorf("CloneURL got %q want %q", got.CloneURL, "https://github.example.com/myowner/myrepo.git")
	}
	// reset to default
	os.Unsetenv("GH_HOST")
	got2, err := ParseSkillRef("myowner/myrepo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got2.Host != "github.com" {
		t.Errorf("default Host got %q want github.com", got2.Host)
	}
}

func TestLocalPaths(t *testing.T) {
	cases := []string{"./my-skill", "../my-skill", "/absolute/path", "~/skills/foo"}
	for _, c := range cases {
		got, err := ParseSkillRef(c)
		if err != nil {
			t.Errorf("ParseSkillRef(%q) error: %v", c, err)
			continue
		}
		if !got.IsLocal || got.Type != "local" || got.Host != "local" {
			t.Errorf("ParseSkillRef(%q) expected local, got IsLocal=%v Type=%q Host=%q", c, got.IsLocal, got.Type, got.Host)
		}
	}
}

func TestSuffixGitTrim(t *testing.T) {
	got, err := ParseSkillRef("vercel-labs/agent-skills.git")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if got.Repo != "agent-skills" {
		t.Errorf("Repo got %q want agent-skills", got.Repo)
	}
	if got.Owner != "vercel-labs" {
		t.Errorf("Owner got %q", got.Owner)
	}
}

func TestHostedArtifactTypes(t *testing.T) {
	if !IsHostedArtifactUrl("https://raw.githubusercontent.com/a/b/main/file") {
		t.Error("expected hosted artifact")
	}
	if !IsHostedArtifactUrl("https://codeload.github.com/a/b/zip/main") {
		t.Error("expected codeload hosted")
	}
	if !IsHostedArtifactUrl("https://github.com/a/b/archive/main.zip") {
		t.Error("expected archive hosted")
	}
	if IsHostedArtifactUrl("https://github.com/a/b") {
		t.Error("should not be hosted artifact")
	}
}

func TestWellKnown(t *testing.T) {
	if !IsWellKnownUrl("https://example.com/.well-known/skills") {
		t.Error("expected well-known")
	}
	if !IsWellKnownUrl("https://example.com/.well-known/agent-skills") {
		t.Error("expected well-known agent")
	}
	if IsWellKnownUrl("https://github.com/a/b") {
		t.Error("should not be well-known")
	}
}
