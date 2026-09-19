package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/api"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/history"
	"skill.sh/mskill/internal/link"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/security"
)

var userAddCmd = &cobra.Command{
	Use:     "user-add [owner]",
	Aliases: []string{"uadd", "add-user", "adduser"},
	Short:   "Pick repos then multiple skills from a GitHub user",
	Long: `Interactive multi-select: choose repos for an owner, then choose
multiple skills to install.

Non-interactive: pass --repo repeatedly and --skill repeatedly (or "*").

Examples:
  mskill user-add vercel-labs
  mskill user-add vercel-labs --repo agent-skills --skill "*"
  mskill user-add my-org --repo r1 --repo r2 --skill s1 --skill s2 -y`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		owner := ""
		if len(args) > 0 {
			owner = strings.TrimSpace(args[0])
		}
		repos, _ := cmd.Flags().GetStringArray("repo")
		skills, _ := cmd.Flags().GetStringArray("skill")
		refFlag, _ := cmd.Flags().GetString("ref")
		force, _ := cmd.Flags().GetBool("force")
		global, _ := cmd.Flags().GetBool("global")
		project, _ := cmd.Flags().GetBool("project")
		agentFilter, _ := cmd.Flags().GetString("agent")
		copyFlag, _ := cmd.Flags().GetBool("copy")
		linkMode, _ := cmd.Flags().GetString("link-mode")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		if yesFlag {
			viper.Set("yes", true)
		}
		limit, _ := cmd.Flags().GetInt("limit")

		if owner == "" {
			if !security.IsInteractiveTTY() || security.IsAgentEnv() {
				return fmt.Errorf("owner required: mskill user-add <owner> --repo <repo> --skill <skill>")
			}
			var ans string
			if err := survey.AskOne(&survey.Input{Message: "GitHub owner/user:"}, &ans); err != nil {
				return err
			}
			owner = strings.TrimSpace(ans)
		}
		if owner == "" {
			return fmt.Errorf("owner required")
		}

		// 1. Discover repos via search API (owner filter), group by source.
		if limit <= 0 {
			limit = 50
		}
		found, err := api.Search(ctx, owner, owner, limit)
		if err != nil {
			return fmt.Errorf("search owner %q failed: %w", owner, err)
		}
		byRepo := map[string][]api.Skill{}
		for _, s := range found {
			src := strings.TrimSpace(s.Source)
			if src == "" && s.Owner != "" {
				src = s.Owner
			}
			if src == "" {
				continue
			}
			byRepo[src] = append(byRepo[src], s)
		}
		if len(byRepo) == 0 {
			return fmt.Errorf("no repos found for owner %q", owner)
		}
		var allRepos []string
		for r := range byRepo {
			allRepos = append(allRepos, r)
		}
		sort.Strings(allRepos)

		// 2. Select repos.
		var selRepos []string
		if len(repos) > 0 {
			for _, r := range repos {
				r = strings.TrimSpace(r)
				if r == "" {
					continue
				}
				// Allow short "repo" or full "owner/repo".
				if !strings.Contains(r, "/") {
					r = owner + "/" + r
				}
				selRepos = append(selRepos, r)
			}
		} else if security.IsInteractiveTTY() && !security.IsAgentEnv() && !viper.GetBool("yes") {
			var chosen []string
			p := &survey.MultiSelect{
				Message: fmt.Sprintf("Select repos for %s:", owner),
				Options: allRepos,
				Help:    "Space selects, a toggles all.",
			}
			if err := survey.AskOne(p, &chosen); err != nil {
				return err
			}
			selRepos = chosen
		} else if viper.GetBool("yes") {
			selRepos = allRepos
			fmt.Fprintf(os.Stderr, "› auto-selecting all %d repos (--yes).\n", len(allRepos))
		} else {
			return fmt.Errorf("owner %s has %d repos; use --repo (repeatable) or -y. Available: %s", owner, len(allRepos), strings.Join(allRepos, ", "))
		}
		if len(selRepos) == 0 {
			return fmt.Errorf("no repos selected")
		}

		// 3. Collect candidate skills per repo from cache discovery.
		type cand struct{ repo, rel, label string }
		var cands []cand
		for _, rp := range selRepos {
			r, perr := resolve.ParseSkillRef(rp)
			if perr != nil {
				fmt.Fprintf(os.Stderr, "warning: skip %q: %v\n", rp, perr)
				continue
			}
			cp, _, cerr := cache.Ensure(ctx, paths, r, refFlag, nil, force)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "warning: skip %q: cache failed: %v\n", rp, cerr)
				continue
			}
			all, _ := cache.ListSkills(ctx, cp)
			if len(all) == 0 {
				fmt.Fprintf(os.Stderr, "warning: no skills in %s (missing SKILL.md)\n", rp)
				continue
			}
			for _, rel := range all {
				disp := rel
				if disp == "" {
					disp = "."
				}
				cands = append(cands, cand{rp, rel, rp + " / " + disp})
			}
		}
		if len(cands) == 0 {
			return fmt.Errorf("no skills found in selected repos")
		}

		// 4. Select skills (multi).
		type picked struct{ repo, rel string }
		var toInstall []picked
		if len(skills) > 0 && !(len(skills) == 1 && skills[0] == "*") {
			want := map[string]bool{}
			for _, s := range skills {
				for _, p := range strings.Split(s, ",") {
					if t := strings.TrimSpace(p); t != "" {
						want[strings.ToLower(t)] = true
					}
				}
			}
			for _, c := range cands {
				base := strings.ToLower(filepath.Base(c.rel))
				if want[strings.ToLower(c.rel)] || want[base] {
					toInstall = append(toInstall, picked{c.repo, c.rel})
				}
			}
			if len(toInstall) == 0 {
				return fmt.Errorf("no matching --skill in selected repos. Available: %s", strings.Join(func() []string {
					var l []string
					for _, c := range cands {
						l = append(l, c.label)
					}
					return l
				}(), ", "))
			}
		} else if len(skills) == 1 && skills[0] == "*" || (viper.GetBool("yes") && len(skills) == 0 && len(cands) > 0 && !security.IsInteractiveTTY()) {
			for _, c := range cands {
				toInstall = append(toInstall, picked{c.repo, c.rel})
			}
		} else {
			var labels []string
			for _, c := range cands {
				labels = append(labels, c.label)
			}
			var chosen []string
			p := &survey.MultiSelect{
				Message: "Select skills to install:",
				Options: labels,
				Help:    "Space selects, a toggles all.",
			}
			if err := survey.AskOne(p, &chosen); err != nil {
				return err
			}
			sel := map[string]bool{}
			for _, c := range chosen {
				sel[c] = true
			}
			for _, c := range cands {
				if sel[c.label] {
					toInstall = append(toInstall, picked{c.repo, c.rel})
				}
			}
		}
		if len(toInstall) == 0 {
			return fmt.Errorf("no skills selected")
		}

		// 5. Install each via same Cache→Audit→Link path as get.
		for _, t := range toInstall {
			r, err := resolve.ParseSkillRef(t.repo)
			if err != nil {
				return err
			}
			rel := t.rel
			sparse := []string{}
			if rel != "" && rel != "." {
				sparse = []string{rel}
			}
			cachePath, meta, err := cache.Ensure(ctx, paths, r, refFlag, sparse, force)
			if err != nil {
				return fmt.Errorf("cache %s: %w", t.repo, err)
			}
			effRef := refFlag
			if effRef == "" {
				effRef = r.Ref
			}
			skillRel := rel
			cacheSkillPath := ensureSkillDir(ctx, cachePath, paths, r, effRef, skillRel, force)
			if !hasSkillMD(cacheSkillPath) {
				return fmt.Errorf("skill %q not found in %s", skillRel, t.repo)
			}
			slug := filepath.Base(skillRel)
			if skillRel == "" || skillRel == "." {
				slug = r.Repo
			}
			src := strings.Trim(r.Owner+"/"+r.Repo, "/")
			if slug != "" && src != "" {
				if verdicts, _ := api.Audit(ctx, src, []string{slug}); verdicts != nil {
					if v, ok := verdicts[slug]; ok && (!v.Safe || v.Unknown) {
						if !security.IsTrustEnabled(paths) {
							if err := security.RequirePassword(slug, v.Reason, paths); err != nil {
								return err
							}
						}
					}
				}
			}
			skillName := resolve.SanitizeName(filepath.Base(skillRel))
			if skillRel == "" || skillRel == "." || skillName == "" {
				skillName = resolve.SanitizeName(r.Repo)
			}
			if err := link.Install(cacheSkillPath, skillName, link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}); err != nil {
				return fmt.Errorf("link %q: %w", skillName, err)
			}
			if h, herr := link.SkillFolderHash(cacheSkillPath); herr == nil {
				_ = link.UpdateLockfile(global || (!global && !project), skillName, h)
				if project || (!global && !project) {
					_ = link.UpdateLockfile(false, skillName, h)
				}
			}
			cmd.Printf("installed %s -> %s\n", t.repo+" / "+skillRel, skillName)
			recRef := effRef
			if recRef == "" {
				recRef = meta.Ref
			}
			_ = history.Append(paths, history.Entry{Repo: src, Skill: skillName, Ref: recRef, CommitSHA: meta.CommitSHA})
			warnIfPinned(src, skillName, meta.CommitSHA)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(userAddCmd)
	userAddCmd.Flags().StringArray("repo", []string{}, "repo filter (repeatable, short or owner/repo)")
	userAddCmd.Flags().StringArray("skill", []string{}, "skill filter (repeatable, * = all)")
	userAddCmd.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	userAddCmd.Flags().Bool("force", false, "force refetch")
	userAddCmd.Flags().BoolP("global", "g", false, "install to global")
	userAddCmd.Flags().BoolP("project", "p", false, "install to project")
	userAddCmd.Flags().StringP("agent", "a", "", "comma-separated agents or *")
	userAddCmd.Flags().Bool("copy", false, "copy instead of symlink")
	userAddCmd.Flags().String("link-mode", "auto", "link mode: auto|symlink|copy")
	userAddCmd.Flags().BoolP("yes", "y", false, "skip prompts (select all)")
	userAddCmd.Flags().Int("limit", 50, "max search results for owner")
}
