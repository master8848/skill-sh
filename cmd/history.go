package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/history"
)

var historyCmd = &cobra.Command{
	Use:     "history [filter]",
	Aliases: []string{"hist"},
	Short:   "Show install history with pinned versions",
	Long:    "List recorded installs (repo, skill, ref, commit, time). Filter by repo or skill substring.",
	Example: `  mskill history
  mskill history anki-import-cli
  mskill get owner/repo --history`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		filter := ""
		if len(args) > 0 {
			filter = strings.ToLower(strings.TrimSpace(args[0]))
		}
		showHeader, _ := cmd.Flags().GetBool("header")
		entries, _ := history.Load(paths)
		var filtered []history.Entry
		for _, e := range entries {
			if filter != "" && !strings.Contains(strings.ToLower(e.Repo), filter) && !strings.Contains(strings.ToLower(e.Skill), filter) {
				continue
			}
			filtered = append(filtered, e)
		}
		if len(filtered) == 0 {
			cmd.Println("no install history")
			return nil
		}
		if showHeader {
			cmd.Println("time\trepo\tskill\tref\tcommit")
		}
		for _, e := range filtered {
			commit := e.CommitSHA
			if len(commit) > 7 {
				commit = commit[:7]
			}
			if commit == "" {
				commit = "-"
			}
			ref := e.Ref
			if ref == "" {
				ref = "-"
			}
			ts := e.Time.Format(time.RFC3339)
			if e.Time.IsZero() {
				ts = "-"
			}
			cmd.Printf("%s\t%s\t%s\t%s\t%s\n", ts, e.Repo, e.Skill, ref, commit)
		}
		return nil
	},
}

// newerCommitExists reports a newer known commit for repo+skill.
// It checks install history (newest entry) and cache metas (most recently
// fetched). Returns "" when current is latest or unknown.
func newerCommitExists(repo, skill, currentSHA string) string {
	if currentSHA == "" {
		return ""
	}
	entries, _ := history.Load(paths)
	latest := history.LatestSHA(entries, repo, skill)
	if latest != "" && latest != currentSHA {
		return latest
	}
	// Fall back to cache metas: newest LastFetch for same repo wins.
	cached, err := cache.List(paths)
	if err != nil || len(cached) == 0 {
		return ""
	}
	parts := strings.SplitN(repo, "/", 2)
	owner, name := "", ""
	if len(parts) == 2 {
		owner, name = parts[0], parts[1]
	} else {
		name = repo
	}
	sort.Slice(cached, func(i, j int) bool {
		return cached[i].Meta.LastFetch.After(cached[j].Meta.LastFetch)
	})
	for _, c := range cached {
		m := c.Meta
		if name != "" && m.Repo != name {
			continue
		}
		if owner != "" && m.Owner != owner {
			continue
		}
		if m.CommitSHA != "" && m.CommitSHA != currentSHA {
			return m.CommitSHA
		}
	}
	return ""
}

// warnIfPinned prints "pinned to older version, newer exists" when current
// is not the newest known commit for repo+skill.
func warnIfPinned(repo, skill, currentSHA string) {
	if newer := newerCommitExists(repo, skill, currentSHA); newer != "" {
		short := newer
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Fprintf(os.Stderr, "warning: pinned to older version, newer exists (%s)\n", short)
	}
}

func init() {
	rootCmd.AddCommand(historyCmd)
	historyCmd.Flags().Bool("header", false, "print TSV header")
}
