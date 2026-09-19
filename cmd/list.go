package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/link"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List installed skills",
	Long:    "List installed skills as TSV: name<TAB>path<TAB>agent. Use --header for a header row. Filter with --global / --agent.",
	Example: `  mskill list
  mskill list --header | column -t -s $'\t'
  mskill list --global --agent claude
  mskill list --agent project`,
	RunE: func(cmd *cobra.Command, args []string) error {
		globalOnly, _ := cmd.Flags().GetBool("global")
		agentFilter, _ := cmd.Flags().GetString("agent")
		showHeader, _ := cmd.Flags().GetBool("header")

		agents := link.ResolveDestinations(agentFilter, globalOnly, false)
		// if no filter and not globalOnly, list both global and project per agent?
		found := map[string]bool{}
		var results []string
		for _, ag := range agents {
			var dirs []string
			if !globalOnly {
				// project dirs relative
				dirs = append(dirs, ag.ProjectDir)
			}
			if globalOnly || agentFilter == "" || len(agents) > 0 {
				// also global
				dirs = append(dirs, expandHome(ag.GlobalDir))
			}
			// dedupe handling: if globalOnly true, only global
			if globalOnly {
				dirs = []string{expandHome(ag.GlobalDir)}
			}
			for _, d := range dirs {
				// d may be relative for project; ensure absolute for check
				check := d
				if !filepath.IsAbs(d) && !strings.HasPrefix(d, "~/") {
					// project relative
					wd, _ := os.Getwd()
					check = filepath.Join(wd, d)
				} else {
					check = expandHome(d)
				}
				entries, err := os.ReadDir(check)
				if err != nil {
					continue
				}
				for _, e := range entries {
					if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
						key := e.Name() + "|" + check
						if found[key] {
							continue
						}
						found[key] = true
						results = append(results, e.Name()+"\t"+check+"/"+e.Name()+"\t"+ag.Name)
					}
				}
			}
		}
		if len(results) == 0 {
			cmd.Println("no skills installed")
			return nil
		}
		if showHeader {
			cmd.Println("name\tpath\tagent")
		}
		for _, r := range results {
			cmd.Println(r)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolP("global", "g", false, "only global installs")
	listCmd.Flags().StringP("agent", "a", "", "filter by agent")
	listCmd.Flags().Bool("header", false, "print TSV header")
}
