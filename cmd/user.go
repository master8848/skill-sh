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

var userCmd = &cobra.Command{
	Use:     "user",
	Aliases: []string{"users", "owner"},
	Short:   "Pick repos then multiple skills from a GitHub user",
	Long: `Browse a GitHub owner's repos and skills interactively.

Input accepts owner | owner/repo | URL. A repo-level input
(owner/repo or https://github.com/owner/repo) preselects that
repo and skips the repo picker.

  mskill user add vercel-labs                  # select repos, then skills, then install
  mskill user add vercel-labs/agent-skills     # skip repo picker, select skills
  mskill user add https://github.com/vercel-labs/agent-skills
  mskill user show vercel-labs                 # select repos, then skills, preview only
  mskill user trust vercel-labs/agent-skills --skill my-skill`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

type userPicked struct{ repo, rel string }

// splitUserInput accepts owner | owner/repo | URL and returns
// (owner, preselected owner/repo or ""). Unparseable input is
// returned as-is for the search API to handle.
func splitUserInput(in string) (owner, repo string) {
	in = strings.Trim(strings.TrimSpace(in), "/")
	if in == "" {
		return "", ""
	}
	if r, err := resolve.ParseSkillRef(normalizeColonRef(in)); err == nil && r.Owner != "" {
		if r.Repo != "" {
			return r.Owner, r.Owner + "/" + r.Repo
		}
		return r.Owner, ""
	}
	if parts := strings.Split(in, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], in
	}
	return in, ""
}

// selectUserSkills implements step 1-4 shared by add and show:
// discover repos for owner, MultiSelect repos, discover skills,
// MultiSelect skills. Returns repo+rel pairs.
func selectUserSkills(cmd *cobra.Command, ctx context.Context, owner string, refFlag string, force bool, limit int) ([]userPicked, error) {
	repos, _ := cmd.Flags().GetStringArray("repo")
	skills, _ := cmd.Flags().GetStringArray("skill")

	// Accept owner | owner/repo | URL (e.g. https://github.com/owner/repo).
	// A repo-level input preselects that repo and skips the repo picker.
	owner, preselectRepo := splitUserInput(owner)

	if owner == "" {
		if !security.IsInteractiveTTY() || security.IsAgentEnv() {
			return nil, fmt.Errorf("owner required: mskill user add <owner> --repo <repo> --skill <skill>")
		}
		var ans string
		if err := survey.AskOne(&survey.Input{Message: "GitHub owner/user:"}, &ans); err != nil {
			return nil, err
		}
		owner = strings.TrimSpace(ans)
	}
	if owner == "" {
		return nil, fmt.Errorf("owner required")
	}
	if limit <= 0 {
		limit = 50
	}
	found, err := api.Search(ctx, owner, owner, limit)
	if err != nil {
		return nil, fmt.Errorf("search owner %q failed: %w", owner, err)
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
		return nil, fmt.Errorf("no repos found for owner %q", owner)
	}
	var allRepos []string
	for r := range byRepo {
		allRepos = append(allRepos, r)
	}
	sort.Strings(allRepos)

	var selRepos []string
	if preselectRepo != "" && len(repos) == 0 {
		selRepos = []string{preselectRepo}
	} else if len(repos) > 0 {
		for _, r := range repos {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
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
			return nil, err
		}
		selRepos = chosen
	} else if viper.GetBool("yes") {
		selRepos = allRepos
		fmt.Fprintf(os.Stderr, "› auto-selecting all %d repos (--yes).\n", len(allRepos))
	} else {
		return nil, fmt.Errorf("owner %s has %d repos; use --repo (repeatable) or -y. Available: %s", owner, len(allRepos), strings.Join(allRepos, ", "))
	}
	if len(selRepos) == 0 {
		return nil, fmt.Errorf("no repos selected")
	}

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
		return nil, fmt.Errorf("no skills found in selected repos")
	}

	var out []userPicked
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
				out = append(out, userPicked{c.repo, c.rel})
			}
		}
		if len(out) == 0 {
			var l []string
			for _, c := range cands {
				l = append(l, c.label)
			}
			return nil, fmt.Errorf("no matching --skill in selected repos. Available: %s", strings.Join(l, ", "))
		}
		return out, nil
	}
	if len(skills) == 1 && skills[0] == "*" || (viper.GetBool("yes") && len(skills) == 0 && !security.IsInteractiveTTY()) {
		for _, c := range cands {
			out = append(out, userPicked{c.repo, c.rel})
		}
		return out, nil
	}
	var labels []string
	for _, c := range cands {
		labels = append(labels, c.label)
	}
	var chosen []string
	p := &survey.MultiSelect{
		Message: "Select skills:",
		Options: labels,
		Help:    "Space selects, a toggles all.",
	}
	if err := survey.AskOne(p, &chosen); err != nil {
		return nil, err
	}
	sel := map[string]bool{}
	for _, c := range chosen {
		sel[c] = true
	}
	for _, c := range cands {
		if sel[c.label] {
			out = append(out, userPicked{c.repo, c.rel})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no skills selected")
	}
	return out, nil
}

var userAddSubCmd = &cobra.Command{
	Use:     "add [owner|owner/repo|URL]",
	Aliases: []string{"a", "install"},
	Short:   "Select repos then install multiple skills",
	Example: `  mskill user add vercel-labs
  mskill user add vercel-labs/agent-skills
  mskill user add https://github.com/vercel-labs/agent-skills
  mskill user add vercel-labs --repo agent-skills --skill "*"
  mskill user add my-org --repo r1 --repo r2 --skill s1 --skill s2 -y`,
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

		toInstall, err := selectUserSkills(cmd, ctx, owner, refFlag, force, limit)
		if err != nil {
			return err
		}
		for _, t := range toInstall {
			r, err := resolve.ParseSkillRef(t.repo)
			if err != nil {
				return err
			}
			sparse := []string{}
			if t.rel != "" && t.rel != "." {
				sparse = []string{t.rel}
			}
			cachePath, meta, err := cache.Ensure(ctx, paths, r, refFlag, sparse, force)
			if err != nil {
				return fmt.Errorf("cache %s: %w", t.repo, err)
			}
			effRef := refFlag
			if effRef == "" {
				effRef = r.Ref
			}
			cacheSkillPath := ensureSkillDir(ctx, cachePath, paths, r, effRef, t.rel, force)
			if !hasSkillMD(cacheSkillPath) {
				return fmt.Errorf("skill %q not found in %s", t.rel, t.repo)
			}
			slug := filepath.Base(t.rel)
			if t.rel == "" || t.rel == "." {
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
			skillName := resolve.SanitizeName(filepath.Base(t.rel))
			if t.rel == "" || t.rel == "." || skillName == "" {
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
			cmd.Printf("installed %s -> %s\n", t.repo+" / "+t.rel, skillName)
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

var userShowSubCmd = &cobra.Command{
	Use:     "show [owner|owner/repo|URL]",
	Aliases: []string{"see", "view", "ls", "list"},
	Short:   "Select repos then preview multiple skills without installing",
	Example: `  mskill user show vercel-labs
  mskill user show vercel-labs/agent-skills
  mskill user show https://github.com/vercel-labs/agent-skills
  mskill user show vercel-labs --repo agent-skills --skill vercel-optimize
  mskill user show my-org --repo r1 --skill s1 --file README.md`,
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
		refFlag, _ := cmd.Flags().GetString("ref")
		force, _ := cmd.Flags().GetBool("force")
		limit, _ := cmd.Flags().GetInt("limit")
		files, _ := cmd.Flags().GetStringArray("file")

		picked, err := selectUserSkills(cmd, ctx, owner, refFlag, force, limit)
		if err != nil {
			return err
		}
		targetFiles := files
		if len(targetFiles) == 0 {
			targetFiles = []string{"SKILL.md"}
		}
		for _, t := range picked {
			r, err := resolve.ParseSkillRef(t.repo)
			if err != nil {
				return err
			}
			sparse := []string{}
			if t.rel != "" && t.rel != "." {
				sparse = []string{t.rel}
			}
			cachePath, _, err := cache.Ensure(ctx, paths, r, refFlag, sparse, force)
			if err != nil {
				return fmt.Errorf("cache %s: %w", t.repo, err)
			}
			effRef := refFlag
			if effRef == "" {
				effRef = r.Ref
			}
			dir := ensureSkillDir(ctx, cachePath, paths, r, effRef, t.rel, force)
			if !hasSkillMD(dir) {
				return fmt.Errorf("skill %q not found in %s", t.rel, t.repo)
			}
			// Same audit gate as show.
			slug := filepath.Base(t.rel)
			if t.rel == "" || t.rel == "." {
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
			for _, f := range targetFiles {
				full := skillFilePath(dir, f)
				data, err := os.ReadFile(full)
				if err != nil {
					return fmt.Errorf("read %s in %s / %s: %w", f, t.repo, t.rel, err)
				}
				cmd.Printf("=== %s / %s : %s ===\n", t.repo, t.rel, f)
				cmdPrint(cmd, string(data))
				if !strings.HasSuffix(string(data), "\n") {
					cmdPrint(cmd, "\n")
				}
			}
		}
		return nil
	},
}

// userTrustSubCmd trusts picked skills individually so each bypasses
// the password gate via security.IsSkillTrusted.
var userTrustSubCmd = &cobra.Command{
	Use:     "trust [owner|owner/repo|URL]",
	Aliases: []string{"t", "allow"},
	Short:   "Select repos then trust multiple skills individually",
	Example: `  mskill user trust vercel-labs
  mskill user trust vercel-labs/agent-skills
  mskill user trust https://github.com/vercel-labs/agent-skills
  mskill user trust my-org --repo r1 --skill s1 -y`,
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
		refFlag, _ := cmd.Flags().GetString("ref")
		force, _ := cmd.Flags().GetBool("force")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		if yesFlag {
			viper.Set("yes", true)
		}
		limit, _ := cmd.Flags().GetInt("limit")

		picked, err := selectUserSkills(cmd, ctx, owner, refFlag, force, limit)
		if err != nil {
			return err
		}
		for _, t := range picked {
			slug := filepath.Base(t.rel)
			if t.rel == "" || t.rel == "." {
				if r, rerr := resolve.ParseSkillRef(t.repo); rerr == nil {
					slug = r.Repo
				} else {
					slug = t.repo
				}
			}
			if err := security.TrustSkill(slug, paths); err != nil {
				return err
			}
			cmd.Printf("trusted %s\n", slug)
		}
		return nil
	},
}

// userAddCompat keeps the old `mskill user-add` spelling working.
var userAddCompatCmd = &cobra.Command{
	Use:        "user-add [owner|owner/repo|URL]",
	Short:      "Deprecated: use `mskill user add`",
	Deprecated: "use `mskill user add` instead",
	Args:       cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// Forward to user add with same flags (compat registers the same
		// flags, so RunE reads them from cmd directly).
		return userAddSubCmd.RunE(cmd, args)
	},
}

func addUserFlags(c *cobra.Command, withLink bool) {
	c.Flags().StringArray("repo", []string{}, "repo filter (repeatable, short or owner/repo)")
	c.Flags().StringArray("skill", []string{}, "skill filter (repeatable, * = all)")
	c.Flags().String("ref", "", "git ref (branch, tag, or commit)")
	c.Flags().Bool("force", false, "force refetch")
	c.Flags().BoolP("yes", "y", false, "skip prompts (select all)")
	c.Flags().Int("limit", 50, "max search results for owner")
	if withLink {
		c.Flags().BoolP("global", "g", false, "install to global")
		c.Flags().BoolP("project", "p", false, "install to project")
		c.Flags().StringP("agent", "a", "", "comma-separated agents or *")
		c.Flags().Bool("copy", false, "copy instead of symlink")
		c.Flags().String("link-mode", "auto", "link mode: auto|symlink|copy")
	}
}

func init() {
	rootCmd.AddCommand(userCmd)
	userCmd.AddCommand(userAddSubCmd)
	userCmd.AddCommand(userShowSubCmd)
	userCmd.AddCommand(userTrustSubCmd)
	// Compat: old spelling still works.
	rootCmd.AddCommand(userAddCompatCmd)
	for _, alias := range []string{"uadd", "add-user", "adduser"} {
		_ = alias
	}
	userAddCompatCmd.Aliases = []string{"uadd", "add-user", "adduser"}

	addUserFlags(userAddSubCmd, true)
	addUserFlags(userShowSubCmd, false)
	userShowSubCmd.Flags().StringArray("file", []string{}, "file(s) to print (repeatable, default SKILL.md)")
	addUserFlags(userTrustSubCmd, false)

	addUserFlags(userAddCompatCmd, true)
}
