package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/history"
	"skill.sh/mskill/internal/link"
)

var cleanCmd = &cobra.Command{
	Use:     "clean <owner|owner/repo>",
	Aliases: []string{"purge"},
	Short:   "Remove all installed skills from a GitHub owner or repo",
	Long: `Remove every installed skill whose source matches <owner> or <owner/repo>.

Source is resolved from the install symlink target
(~/.cache/mskill/repos/<host>/<owner>/<repo>/...), with install
history (~/.mskill/history.json) as fallback for --copy installs.

Examples:
  mskill clean vercel-labs
  mskill clean vercel-labs/agent-skills
  mskill clean vercel-labs/agent-skills --global -y
  mskill clean vercel-labs --cache   # also evict cache for those repos`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := strings.Trim(strings.TrimSpace(args[0]), "/")
		parts := strings.Split(src, "/")
		if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
			return fmt.Errorf("invalid source %q: want owner or owner/repo", args[0])
		}
		owner := strings.ToLower(parts[0])
		repo := ""
		if len(parts) == 2 {
			repo = strings.ToLower(parts[1])
		}
		globalOnly, _ := cmd.Flags().GetBool("global")
		agentFilter, _ := cmd.Flags().GetString("agent")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		withCache, _ := cmd.Flags().GetBool("cache")

		matches := func(h, o, r string) bool {
			o = strings.ToLower(o)
			r = strings.ToLower(r)
			if o != owner {
				return false
			}
			if repo != "" && r != repo {
				return false
			}
			return true
		}

		// Collect installed dirs across agent destinations.
		type hit struct{ name, path string }
		var hits []hit
		seen := map[string]bool{}
		for _, ag := range link.ResolveDestinations(agentFilter, globalOnly, false) {
			var dirs []string
			if globalOnly {
				dirs = []string{expandHome(ag.GlobalDir)}
			} else {
				dirs = []string{expandHome(ag.GlobalDir), ag.ProjectDir}
			}
			for _, d := range dirs {
				check := d
				if !filepath.IsAbs(d) {
					if wd, err := os.Getwd(); err == nil {
						check = filepath.Join(wd, d)
					}
				}
				entries, err := os.ReadDir(check)
				if err != nil {
					continue
				}
				for _, e := range entries {
					dest := filepath.Join(check, e.Name())
					key := dest
					if seen[key] {
						continue
					}
					target, terr := filepath.EvalSymlinks(dest)
					var h, o, r string
					if terr == nil {
						h, o, r = ownerRepoFromCachePath(target)
					}
					matched := h != "" && matches(h, o, r)
					if !matched {
						// Fallback: copy installs have no symlink into cache;
						// match by history repo -> skill name.
						if histMatches(e.Name(), owner, repo) {
							matched = true
						}
					}
					if matched {
						seen[key] = true
						hits = append(hits, hit{e.Name(), dest})
					}
				}
			}
		}

		if len(hits) == 0 {
			if repo == "" {
				cmd.Printf("no installed skills from owner %s\n", owner)
			} else {
				cmd.Printf("no installed skills from %s/%s\n", owner, repo)
			}
		} else {
			if !yesFlag {
				cmd.Printf("will remove %d skill(s):\n", len(hits))
				for _, h := range hits {
					cmd.Printf("  %s (%s)\n", h.name, h.path)
				}
				// Non-interactive without -y: stop and ask for -y.
				if !isTTYForClean() {
					return fmt.Errorf("refusing without -y in non-interactive mode: re-run with -y")
				}
				var confirm string
				fmt.Fprintf(os.Stderr, "Remove %d skill(s)? [y/N]: ", len(hits))
				_, _ = fmt.Scanln(&confirm)
				confirm = strings.ToLower(strings.TrimSpace(confirm))
				if confirm != "y" && confirm != "yes" {
					cmd.Println("aborted")
					return nil
				}
			}
			for _, h := range hits {
				if err := os.RemoveAll(h.path); err != nil {
					return fmt.Errorf("remove %s: %w", h.path, err)
				}
				cmd.Printf("removed %s (%s)\n", h.name, h.path)
			}
		}

		if withCache {
			entries, err := cache.List(paths)
			if err != nil {
				return fmt.Errorf("cache list failed: %w", err)
			}
			n := 0
			for _, e := range entries {
				if e.Meta == nil || !matches(e.Meta.Host, e.Meta.Owner, e.Meta.Repo) {
					continue
				}
				if err := os.RemoveAll(e.Path); err != nil {
					return fmt.Errorf("remove cache %s: %w", e.Path, err)
				}
				cmd.Printf("evicted cache %s\n", e.Path)
				n++
			}
			if n == 0 {
				cmd.Println("no cache entries matched")
			}
		}
		return nil
	},
}

// ownerRepoFromCachePath extracts host/owner/repo from
// .../repos/<host>/<owner>/<repo>/<ref>--<hash>/... .
func ownerRepoFromCachePath(p string) (host, owner, repo string) {
	parts := strings.Split(filepath.ToSlash(p), "/")
	for i := 0; i+4 < len(parts); i++ {
		if parts[i] == "repos" {
			return parts[i+1], parts[i+2], parts[i+3]
		}
	}
	return "", "", ""
}

func histMatches(skill, owner, repo string) bool {
	entries, err := history.Load(paths)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		ro := strings.ToLower(strings.TrimSpace(e.Repo))
		sl := strings.Split(ro, "/")
		if len(sl) != 2 {
			continue
		}
		if sl[0] != owner {
			continue
		}
		if repo != "" && sl[1] != repo {
			continue
		}
		if strings.EqualFold(e.Skill, skill) {
			return true
		}
	}
	return false
}

func isTTYForClean() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func init() {
	rootCmd.AddCommand(cleanCmd)
	cleanCmd.Flags().BoolP("global", "g", false, "only global installs")
	cleanCmd.Flags().StringP("agent", "a", "", "filter by agent")
	cleanCmd.Flags().BoolP("yes", "y", false, "skip confirmation")
	cleanCmd.Flags().Bool("cache", false, "also evict matching cache entries")
}
