package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/api"
	"skill.sh/mskill/internal/search"
	"skill.sh/mskill/internal/security"
)

var searchCmd = &cobra.Command{
	Use:     "search [query]",
	Aliases: []string{"find"},
	Short:   "Search skills with SAFE/UNSAFE/UNKNOWN and topic/official filters",
	Long: `Search skills.sh — compact TSV for humans and pipes.

Web parity: --topic mirrors https://www.skills.sh/topic and --official
mirrors https://www.skills.sh/official. Output is raw TSV (tabs): pipe
with --plain and 'column -t -s $'\t'' / 'cut -f1,4'. TTY aligns columns
via tabwriter; --owner alone uses owner as query (API requires q>=2).`,
	Example: `  mskill search anki --limit 5 --header | column -t -s $'\t'
  mskill search react --topic react --official --limit 10
  mskill search --topic databases --limit 10 --header
  mskill search --owner vercel --limit 5
  # install top hit:
  mskill get master8848/Anki-import --skill anki-import-cli --project`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) > 0 {
			query = strings.Join(args, " ")
		}
		topic, _ := cmd.Flags().GetString("topic")
		official, _ := cmd.Flags().GetBool("official")
		owner, _ := cmd.Flags().GetString("owner")
		limit, _ := cmd.Flags().GetInt("limit")
		header, _ := cmd.Flags().GetBool("header")

		if topic != "" {
			allowed := map[string]bool{"react": true, "nextjs": true, "design": true, "mobile": true, "agent-workflows": true, "databases": true, "testing": true, "marketing": true, "all": true}
			aliases := map[string]string{
				"typescript": "all", "tailwind": "design", "css": "design",
				"deploy": "databases", "docker": "databases", "kubernetes": "databases", "ci-cd": "databases", "ci/cd": "databases",
				"jest": "testing", "playwright": "testing", "e2e": "testing",
				"docs": "all", "readme": "all", "changelog": "all", "api-docs": "all",
				"review": "all", "lint": "all",
				"workflow": "agent-workflows", "automation": "agent-workflows",
			}
			norm := strings.ToLower(strings.TrimSpace(topic))
			if !allowed[norm] {
				if mapped, ok := aliases[norm]; ok {
					topic = mapped
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: unknown --topic %q: treating as keyword filter\n", topic)
				}
			} else {
				topic = norm
			}
		}
		if owner != "" {
			// validate owner per spec ^[a-z0-9](?:[a-z0-9-]{0,38})$  (case-insensitive, normalized to lower)
			low := strings.ToLower(strings.TrimSpace(owner))
			if len(low) < 1 || len(low) > 39 {
				return fmt.Errorf("invalid --owner %q: must be 1-39 lowercase alphanumeric or hyphen, starting with alphanumeric. Try: --owner vercel (hint: use lowercase, e.g. --owner %q)", owner, low)
			}
			if !((low[0] >= 'a' && low[0] <= 'z') || (low[0] >= '0' && low[0] <= '9')) {
				return fmt.Errorf("invalid --owner %q: must start with letter or digit. Try: --owner vercel", owner)
			}
			for i, r := range low {
				if i == 0 {
					if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
						return fmt.Errorf("invalid --owner %q: must start with letter or digit. Try: --owner vercel", owner)
					}
				} else {
					if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
						return fmt.Errorf("invalid --owner %q: only lowercase letters, digits and hyphen allowed. Try: --owner %q", owner, low)
					}
				}
			}
			// normalize for API (case-insensitive)
			owner = low
		}

		// Validate query length: skills.sh API requires q>=2 chars or it returns
		// 400 {"error":"Query must be at least 2 characters"}. With SilenceErrors:false
		// that surfaced as silent exit 1 (no stdout/stderr) — now surfaced via cobra.
		// Provide hint and auto-fill q from --owner/--topic/--official for parity:
		//   mskill search --owner vercel        -> q=vercel
		//   mskill search --topic react         -> q=react
		//   mskill search --official            -> q=skill
		trimmed := strings.TrimSpace(query)
		if len(trimmed) < 2 {
			if strings.TrimSpace(owner) != "" && len(strings.TrimSpace(owner)) >= 2 {
				query = strings.TrimSpace(owner)
				trimmed = query
			} else if strings.TrimSpace(topic) != "" && strings.ToLower(strings.TrimSpace(topic)) != "all" && len(strings.TrimSpace(topic)) >= 2 {
				query = strings.TrimSpace(topic)
				trimmed = query
			} else if strings.ToLower(strings.TrimSpace(topic)) == "all" || official {
				query = "skill"
				trimmed = query
			}
		}
		if len(strings.TrimSpace(query)) < 2 {
			if strings.TrimSpace(topic) == "" && !official && strings.TrimSpace(owner) == "" {
				if security.IsInteractiveTTY() && !security.IsAgentEnv() {
					dim := "\x1b[2m"
					reset := "\x1b[0m"
					if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
						dim, reset = "", ""
					}
					fmt.Fprintf(os.Stderr, "%sSearch skills.sh — try: react, anki, typescript%s\n", dim, reset)
					var ans string
					prompt := &survey.Input{
						Message: "Search skills.sh (try: react, anki, typescript):",
						Help:    "Enter at least 2 characters. Examples: react, anki, typescript. Use --topic or --official to filter.",
						Default: "",
					}
					if err := survey.AskOne(prompt, &ans); err != nil {
						return fmt.Errorf("search prompt failed: %w. Try: mskill search anki --limit 10", err)
					}
					trimAns := strings.TrimSpace(ans)
					if len(trimAns) < 2 {
						fmt.Fprintln(os.Stderr, "Search requires at least 2 characters.")
						fmt.Fprintln(os.Stderr, "Try: mskill search anki --limit 10 | column -t -s $'\\t'")
						fmt.Fprintln(os.Stderr, "     mskill search react --topic react --limit 20")
						fmt.Fprintln(os.Stderr, "     mskill search --help")
						// suggestions instead of bare error
						fmt.Fprintln(os.Stderr, "Suggestions: react, anki, typescript, deploy")
						return fmt.Errorf("Search requires at least 2 characters. Try: mskill search anki --limit 10 (or mskill search --help)")
					}
					query = trimAns
				} else {
					return fmt.Errorf("Search requires at least 2 characters. Try: mskill search anki --limit 10 (or mskill search --help for --topic/--official filters)")
				}
			} else {
				// Topic/official/owner present but q still short (e.g. owner \"a\") -> use broad
				if len(strings.TrimSpace(query)) < 2 {
					query = "skill"
				}
			}
		}

		if limit <= 0 {
			limit = 20
		}
		// Over-fetch for client-side topic/official filtering: fetch limit*3 when filters present to avoid truncation of official results
		fetchLimit := limit * 2
		if topic != "" && strings.ToLower(topic) != "all" {
			fetchLimit = limit * 3
		}
		if official {
			fetchLimit = limit * 3
		}
		if owner != "" && topic != "" {
			fetchLimit = limit * 3
		}
		if fetchLimit < limit {
			fetchLimit = limit
		}
		// cap to avoid excessive fetch
		if fetchLimit > 100 {
			fetchLimit = 100
		}

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		skills, err := api.Search(ctx, query, owner, fetchLimit)
		if err != nil {
			return fmt.Errorf("search failed for %q (owner=%q topic=%q): %w. Tip: check network or try mskill search --help", query, owner, topic, err)
		}

		// Client-side filters
		origLen := len(skills)
		filtered := search.FilterByTopic(skills, topic)
		filtered = search.FilterByOfficial(filtered, official)
		// enrich filtered skills with topic for Render when API topic missing but filter matched heuristic
		if topic != "" && strings.ToLower(topic) != "all" {
			for i := range filtered {
				if strings.TrimSpace(filtered[i].Topic) == "" {
					filtered[i].Topic = strings.ToLower(strings.TrimSpace(topic))
				}
			}
		}
		// Ensure client-side sort by installs descending before trimming (legacy API may not guarantee sort)
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Installs > filtered[j].Installs })
		// hint if filtering truncated results
		if len(filtered) < limit && origLen == fetchLimit && len(filtered) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "Note: filtered to %d/%d (fetchLimit %d). Try broader --topic all or remove --official to see more.\n", len(filtered), origLen, fetchLimit)
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
		if len(filtered) == 0 {
			if origLen > 0 {
				// filtered away all results - give actionable hint
				hint := ""
				if topic != "" && strings.ToLower(topic) != "all" {
					hint += fmt.Sprintf(" --topic %q", topic)
				}
				if official {
					hint += " --official"
				}
				if hint != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "No results after filtering by%s. Try --topic all or remove --official. Showing unfiltered would have %d results.\n", hint, origLen)
					fmt.Fprintf(cmd.OutOrStdout(), "Try: mskill search %q --topic all --limit %d --plain | column -t -s $'\\t'\n", query, limit)
					fmt.Fprintln(cmd.OutOrStdout(), "Leaderboard: https://skills.sh")
					return nil
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "No skills found for %q. Try broader keywords (e.g., react, typescript, deploy) or check popular sources: vercel-labs/agent-skills, ComposioHQ/awesome-claude-skills. You can create your own with: npx skills init <name> or mskill get <owner/repo> --show\n", query)
			fmt.Fprintln(cmd.OutOrStdout(), "Leaderboard: https://skills.sh")
			return nil
		}

		// Batch audits per source (owner/repo), top-20 if >10 sources
		verdicts := map[string]api.Verdict{}
		// Group by source
		group := map[string][]string{}
		skillKey := map[string]api.Skill{}
		for _, s := range filtered {
			source := s.Source
			if source == "" && s.Owner != "" {
				source = s.Owner
			}
			if source == "" {
				source = "unknown"
			}
			// slugs are skillIds or names
			slug := s.SkillID
			if slug == "" {
				slug = s.Name
			}
			if slug == "" {
				slug = s.ID
			}
			if slug == "" {
				continue
			}
			group[source] = append(group[source], slug)
			key := source + ":" + slug
			skillKey[key] = s
			if _, ok := verdicts[slug]; !ok {
				verdicts[slug] = api.Verdict{Unknown: true, Safe: false, Reason: "pending"}
			}
		}
		// For >10 sources, audit top-20 by installs
		if len(group) > 10 {
			// sort filtered by installs descending and take top 20
			// skills already sorted? filter keeps order, assume descending
			top := filtered
			if len(top) > 20 {
				top = top[:20]
			}
			group = map[string][]string{}
			for _, s := range top {
				source := s.Source
				if source == "" {
					source = "unknown"
				}
				slug := s.SkillID
				if slug == "" {
					slug = s.Name
				}
				group[source] = append(group[source], slug)
			}
		}
		for source, slugs := range group {
			if source == "unknown" {
				continue
			}
			m, _ := api.Audit(ctx, source, slugs)
			for _, slug := range slugs {
				if v, ok := m[slug]; ok {
					verdicts[slug] = v
					verdicts[source+":"+slug] = v
					verdicts[source+"/"+slug] = v
					// keep slug-specific keys; do not overwrite generic source key per-iteration with different verdicts - store first only
					if _, exists := verdicts[source]; !exists {
						verdicts[source] = v
					}
				}
			}
		}

		noColor := viper.GetBool("no-color") || os.Getenv("NO_COLOR") != ""
		// also respect flag --no-color via viper? Already set in root PersistentPreRun. Use viper.
		search.Render(filtered, verdicts, header, noColor, cmd.OutOrStdout())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().String("topic", "", "filter by topic (react|nextjs|design|mobile|agent-workflows|databases|testing|marketing|all)")
	searchCmd.Flags().Bool("official", false, "only official skills (https://www.skills.sh/official)")
	searchCmd.Flags().String("owner", "", "filter by owner")
	searchCmd.Flags().Int("limit", 20, "max results")
	searchCmd.Flags().Bool("header", false, "print TSV header")
}
