package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/api"
	"skill.sh/mskill/internal/search"
)

var searchCmd = &cobra.Command{
	Use:     "search [query]",
	Aliases: []string{"find"},
	Short:   "Search skills with SAFE/UNSAFE/UNKNOWN and topic/official filters",
	Long:    "Search skills.sh with compact TSV output. Web parity: --topic mirrors https://www.skills.sh/topic and --official mirrors https://www.skills.sh/official.\nOutput is raw TSV (tabs) for pipes: use --no-color for plain TSV and `| column -t -s $'\\t'` or `| cut -f1,4` / `awk -F'\\t'`. Interactive TTY aligns columns via tabwriter; --owner alone uses owner as query (API requires q>=2).",
	Args:    cobra.ArbitraryArgs,
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
			if !allowed[strings.ToLower(strings.TrimSpace(topic))] {
				return fmt.Errorf("invalid --topic %q: must be one of react|nextjs|design|mobile|agent-workflows|databases|testing|marketing|all", topic)
			}
		}
		if owner != "" {
			// validate owner per spec ^[a-z0-9](?:[a-z0-9-]{0,38})$
			low := strings.ToLower(owner)
			valid := len(low) >= 1 && len(low) <= 39 && low[0] >= 'a' && low[0] <= 'z' || (low[0] >= '0' && low[0] <= '9')
			if !valid {
				// simple check: first char alnum, rest alnum or -
				for i, r := range low {
					if i == 0 {
						if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
							return fmt.Errorf("invalid --owner %q", owner)
						}
					} else {
						if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
							return fmt.Errorf("invalid --owner %q", owner)
						}
					}
				}
			}
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
				// also check TTY for nicer hint but always error; no silent 1 with empty out/err.
				return fmt.Errorf("query required (at least 2 characters); try: mskill search <query>  or  mskill search --topic react --header | column -t -s $'\\t'")
			}
			// Topic/official/owner present but q still short (e.g. owner \"a\") -> use broad
			if len(strings.TrimSpace(query)) < 2 {
				query = "skill"
			}
		}

		if limit <= 0 {
			limit = 20
		}
		// Over-fetch limit*2 for client-side topic/official filtering
		fetchLimit := limit * 2
		if topic != "" || official {
			fetchLimit = limit * 2
		}
		if fetchLimit < limit {
			fetchLimit = limit
		}

		ctx := context.Background()
		skills, err := api.Search(ctx, query, owner, fetchLimit)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}

		// Client-side filters
		filtered := search.FilterByTopic(skills, topic)
		filtered = search.FilterByOfficial(filtered, official)
		// If query non-empty, also filter locally if server didn't? Keep all.
		// Sort already by installs server side; trim to limit
		if len(filtered) > limit {
			filtered = filtered[:limit]
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
					// also set for SkillID key
					verdicts[source] = v
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
