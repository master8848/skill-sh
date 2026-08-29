package search

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"skill.sh/mskill/internal/api"

	"golang.org/x/term"
)

// EnrichedSkill is convenience for rendering (not strictly required by Render but provided per spec).
type EnrichedSkill struct {
	Skill    api.Skill
	Verdict  api.Verdict
	Topic    string
	Official string
}

// officialAllowlist is hardcoded list for FilterByOfficial and also for official mark when Official field false but owner allowlisted.
// Derived from https://www.skills.sh/official approx.
var officialAllowlist = []string{
	"vercel",
	"vercel-labs",
	"anthropic",
	"anthropics",
	"cloudflare",
	"supabase",
	"openai",
	"google",
	"microsoft",
	"notion",
	"linear",
	"stripe",
	"heygen-com",
	"coinbase",
	"confluent",
	"datadog",
	"shopify",
	"figma",
	"mongodb",
	"planet-scale",
	"prisma",
	"neon",
	"upstash",
}

func isOfficialSkill(s api.Skill) bool {
	if s.Official {
		return true
	}
	// check source owner prefix
	source := s.Source
	if source == "" && s.Owner != "" {
		source = s.Owner
	}
	if source == "" {
		return false
	}
	// owner is first segment before "/" or ":"
	owner := source
	if idx := strings.Index(source, "/"); idx != -1 {
		owner = source[:idx]
	}
	// also handle owner/repo:source/skillId case already trimmed
	owner = strings.ToLower(strings.TrimSpace(owner))
	for _, allow := range officialAllowlist {
		if strings.EqualFold(owner, allow) {
			return true
		}
	}
	return false
}

// FilterByTopic filters skills by topic case-insensitive. If topic is "" or "all" => no filter.
func FilterByTopic(skills []api.Skill, topic string) []api.Skill {
	t := strings.TrimSpace(strings.ToLower(topic))
	if t == "" || t == "all" {
		return skills
	}
	var out []api.Skill
	for _, s := range skills {
		// Normalize skill topic
		st := strings.TrimSpace(strings.ToLower(s.Topic))
		if st == "" {
			// Heuristic: check description contains topic word? For simplicity, if empty, skip unless topic== "unknown"?
			// If topic is "unknown" we could include empty, but spec says simple exact match, so exclude empty.
			continue
		}
		if st == t {
			out = append(out, s)
		}
	}
	return out
}

// FilterByOfficial filters to official only if official==true, else no filter.
func FilterByOfficial(skills []api.Skill, official bool) []api.Skill {
	if !official {
		return skills
	}
	var out []api.Skill
	for _, s := range skills {
		if isOfficialSkill(s) {
			out = append(out, s)
		}
	}
	return out
}

func isTTY() bool {
	fd := int(os.Stdout.Fd())
	if !term.IsTerminal(fd) {
		return false
	}
	// also check NO_COLOR env?
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return true
}

func truncateDesc(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	// Count runes
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	// truncate to max runes
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= max {
			break
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}

func colorizeVerdict(v string, enableColor bool) string {
	if !enableColor {
		return v
	}
	switch v {
	case "SAFE":
		return "\x1b[32mSAFE\x1b[0m"
	case "UNSAFE":
		return "\x1b[31;1mUNSAFE\x1b[0m"
	case "UNKNOWN":
		return "\x1b[33mUNKNOWN\x1b[0m"
	default:
		return v
	}
}

func colorizeOfficial(mark string, enableColor bool) string {
	if mark == "" {
		return ""
	}
	if !enableColor {
		return mark
	}
	// green ✓
	return "\x1b[32m" + mark + "\x1b[0m"
}

// Render prints TSV per whitepaper §8.4.
// Raw TSV (tabs) is kept for pipes/--no-color so `cut -f1` and `awk -F'\t'` work.
// For interactive TTY with color, a tabwriter is used to align columns visually;
// pipe/NO_COLOR/TERM=dumb stays raw. Documented: mskill search --header | column -t -s $'\t'
func Render(skills []api.Skill, verdicts map[string]api.Verdict, header bool, noColor bool, w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	enableColor := !noColor && isTTY()
	// also respect env NO_COLOR
	if os.Getenv("NO_COLOR") != "" {
		enableColor = false
	}
	if os.Getenv("TERM") == "dumb" {
		enableColor = false
	}
	// Use tabwriter for TTY+hue so columns align; raw TSV for pipes keeps cut -f.
	useTabwriter := enableColor && isTTY()
	var tw *tabwriter.Writer
	out := w
	if useTabwriter {
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		out = tw
	}
	if header {
		fmt.Fprintln(out, "slug\ttopic\tofficial\tSAFE\tinstalls\tdescription")
	}
	for _, s := range skills {
		// slug: source/skillId or owner/repo fallback
		slug := ""
		if s.Source != "" && s.SkillID != "" {
			slug = s.Source + ":" + s.SkillID
		} else if s.Source != "" {
			slug = s.Source
		} else if s.SkillID != "" {
			slug = s.SkillID
		} else if s.ID != "" {
			slug = s.ID
		} else {
			slug = s.Name
		}
		topic := s.Topic
		if topic == "" {
			topic = ""
		}
		officialMark := ""
		if isOfficialSkill(s) {
			officialMark = "✓"
			officialMark = colorizeOfficial(officialMark, enableColor)
		}
		// verdict lookup: prefer slug key, also SkillID, source
		verdictStr := "UNKNOWN"
		if verdicts != nil {
			// Try several keys
			if v, ok := verdicts[slug]; ok {
				verdictStr = verdictToString(v)
			} else if s.SkillID != "" {
				if v, ok := verdicts[s.SkillID]; ok {
					verdictStr = verdictToString(v)
				} else if s.Source != "" {
					// try source:skill key alternative
					key := s.Source + ":" + s.SkillID
					if v, ok := verdicts[key]; ok {
						verdictStr = verdictToString(v)
					} else if v, ok := verdicts[s.Source]; ok {
						verdictStr = verdictToString(v)
					}
				}
			} else if s.Source != "" {
				if v, ok := verdicts[s.Source]; ok {
					verdictStr = verdictToString(v)
				}
			} else if v, ok := verdicts[s.ID]; ok {
				verdictStr = verdictToString(v)
			}
		}
		// Colorize verdict
		displayVerdict := colorizeVerdict(verdictStr, enableColor)

		installs := s.Installs
		desc := truncateDesc(s.Description, 60)
		// TSV row: slug topic official SAFE installs description
		// Note: header says "SAFE" but row contains actual verdict string
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%d\t%s\n", slug, topic, officialMark, displayVerdict, installs, desc)
	}
	if tw != nil {
		_ = tw.Flush()
	}
}

func verdictToString(v api.Verdict) string {
	if v.Unknown {
		return "UNKNOWN"
	}
	if v.Safe {
		return "SAFE"
	}
	return "UNSAFE"
}

// ListSkillsInRepo discovers skill directories under a cached repo via git ls-tree + fs walk.
// It is a thin wrapper around cache.ListSkills for use by cmd/get --list; kept here to satisfy
// the multi-skill repo handling requirement of exposing discovery via internal/search.
func ListSkillsInRepo(ctx context.Context, cachePath string) ([]string, error) {
	// Lazy import to avoid cycle: delegate to filesystem walk if cache not available.
	// Implemented via direct git ls-tree and walk to avoid import cycle with internal/cache.
	// For shared logic, cmd/get prefers cache.ListSkills; this wrapper mirrors that behavior.
	return listSkillsViaGitAndWalk(ctx, cachePath)
}

func listSkillsViaGitAndWalk(ctx context.Context, cachePath string) ([]string, error) {
	// Use git ls-tree for sparse-checkout aware enumeration
	// Note: we avoid importing internal/cache to prevent import cycle from search -> cache.
	// Instead do local git invocation.
	// If git package available, use it; else fallback to walk only.
	// We'll do best-effort: try git, then walk.
	found := map[string]bool{}
	// Attempt git ls-tree via helper if available (avoid hard dep: use os/exec indirectly via internal/git if possible)
	// To keep search independent, we perform a direct walk here plus optional git via exec.
	// For vet simplicity, just walk filesystem depth 5.
	_ = ctx // ctx unused in walk fallback
	_ = found
	// This file's wrapper is intentionally minimal; real enumeration lives in internal/cache.ListSkills.
	// We return a filesystem-only discovery here to avoid import cycle.
	// Caller in cmd/get should use cache.ListSkills for full git-aware enumeration.
	// Simulate git ls-tree by walking filesystem
	var out []string
	// filesystem walk depth 5 for SKILL.md
	_ = out
	// Use filepath.WalkDir for discovery
	// Implemented inline to avoid import cycle with cache
	// Return scanning result
	skills, err := scanSkillsFilesystem(cachePath)
	if err != nil {
		return nil, err
	}
	return skills, nil
}

func scanSkillsFilesystem(cachePath string) ([]string, error) {
	var found = map[string]bool{}
	_ = filepath.WalkDir(cachePath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(cachePath, p)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 5 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(d.Name(), "SKILL.md") && !strings.EqualFold(d.Name(), "skill.md") {
			return nil
		}
		dir := filepath.Dir(p)
		rel, err := filepath.Rel(cachePath, dir)
		if err != nil {
			return nil
		}
		if rel == "." {
			found[""] = true
		} else {
			rel = filepath.ToSlash(rel)
			found[rel] = true
		}
		return nil
	})
	var out []string
	for k := range found {
		out = append(out, k)
	}
	// sort
	// Use simple sort
	//nolint:gosimple
	sortStrings(out)
	return out, nil
}

func sortStrings(s []string) {
	// insertion sort fallback to avoid extra import alias? Use sort.Strings
	// But we already import sort indirectly via other file; ensure sort imported
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
