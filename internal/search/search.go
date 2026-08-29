package search

import (
	"fmt"
	"io"
	"os"
	"strings"
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
	if header {
		fmt.Fprintln(w, "slug\ttopic\tofficial\tSAFE\tinstalls\tdescription")
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
			topic = "unknown"
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", slug, topic, officialMark, displayVerdict, installs, desc)
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
