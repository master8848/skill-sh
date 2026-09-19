package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/link"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/security"
	"skill.sh/mskill/internal/wellknown"
)

// runGetWellKnown implements Resolve → Cache → Link for website feeds (no git).
func runGetWellKnown(cmd *cobra.Command, ctx context.Context, raw string, r *resolve.Resolved, skillFilters []string, effectiveRef string, isList, isShow bool, files []string, force, global, project bool, agentFilter, linkMode string, copyFlag bool) error {
	verbose := viper.GetBool("verbose")
	printStep(cmd, "Caching", "feed "+raw+"…")
	// Discovery pass when filters are empty or wildcard or --list.
	needDiscover := isList || len(skillFilters) == 0
	hasStar := false
	for _, sf := range skillFilters {
		if sf == "*" {
			hasStar = true
		}
	}
	if hasStar {
		needDiscover = true
	}
	if r.SkillPath == "" && r.Slug == "" && !needDiscover && len(skillFilters) == 0 {
		needDiscover = true
	}
	if needDiscover {
		cachePath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
		if err != nil {
			return fmt.Errorf("Cannot fetch feed %q: %v. Tip: check the site serves /.well-known/agent-skills/index.json (%w)", raw, err, err)
		}
		skills, _ := cache.ListSkills(ctx, cachePath)
		if len(skills) == 0 {
			return fmt.Errorf("no skills found in feed %s (missing SKILL.md). Tip: check index.json lists skills with fetchable SKILL.md", raw)
		}
		if isList {
			for _, s := range skills {
				disp := s
				if disp == "" {
					disp = "."
				}
				cmd.Printf("%s\n", disp)
			}
			cmdPrint(cmd, fmt.Sprintf("\n---\ncached at: %s\n", cachePath))
			return nil
		}
		if hasStar || len(skillFilters) == 0 {
			if len(skills) == 1 {
				skillFilters = skills
				if skillFilters[0] == "" {
					skillFilters = []string{""}
				}
			} else if hasStar {
				// keep star: EnsureWellKnown expands via Select(*)
			} else if security.IsInteractiveTTY() && !security.IsAgentEnv() && !viper.GetBool("yes") {
				chosen, err := promptSkillSelection(raw, skills)
				if err != nil {
					return fmt.Errorf("skill selection failed: %w. Tip: use --skill <name> or --skill \"*\" -y to skip prompt", err)
				}
				if len(chosen) == 0 {
					return fmt.Errorf("no skills selected. Tip: use --skill <name> or --skill \"*\" -y. Available: %s", strings.Join(allDisplay(skills), ", "))
				}
				skillFilters = chosen
			} else {
				if viper.GetBool("yes") {
					skillFilters = []string{"*"}
					fmt.Fprintf(os.Stderr, "\x1b[2m› auto-selecting all %d skills (--yes).\x1b[0m\n", len(skills))
				} else {
					return fmt.Errorf("feed %s contains %d skills; use --skill <name> (repeatable), --skill \"*\" for all, or --list to discover. Available: %s\nTip: mskill get %s --skill \"*\" -y to install all", raw, len(skills), strings.Join(allDisplay(skills), ", "), raw)
				}
			}
		}
	}
	// Normalize filters for feed matching (slugs, not subpaths).
	var normalized []string
	for _, sf := range skillFilters {
		for _, part := range strings.Split(sf, ",") {
			if t := strings.TrimSpace(part); t != "" {
				normalized = append(normalized, t)
			}
		}
	}
	if r.SkillPath != "" && len(normalized) == 0 {
		normalized = []string{r.SkillPath}
	} else if r.Slug != "" && len(normalized) == 0 {
		normalized = []string{r.Slug}
	}
	printStep(cmd, "Caching", "feed "+raw+" → "+strings.Join(allDisplay(normalized), ", ")+"…")
	cachePath, meta, err := cache.Ensure(ctx, paths, r, effectiveRef, normalized, force)
	if err != nil {
		return fmt.Errorf("Cannot fetch feed %q at version %q: %v. Tip: try --list to discover skills, --ref for version (%w)", raw, effectiveRef, err, err)
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "cache: %s filter %s\n", cachePath, meta.Filter)
	}
	if isShow {
		targetFiles := files
		if len(targetFiles) == 0 {
			targetFiles = []string{"SKILL.md"}
		}
		selected, _ := cache.ListSkills(ctx, cachePath)
		if len(selected) == 0 {
			selected = []string{""}
		}
		sort.Strings(selected)
		for _, skillRel := range selected {
			baseSkillDir := cachePath
			if skillRel != "" && skillRel != "." {
				baseSkillDir = filepath.Join(cachePath, skillRel)
			}
			for _, f := range targetFiles {
				f = strings.ReplaceAll(f, "\\", "/")
				sanitized, err := resolve.SanitizeSubpath(f)
				if err != nil {
					return fmt.Errorf("invalid --file %q: %v (%w)", f, err, err)
				}
				full := filepath.Join(baseSkillDir, sanitized)
				data, err := os.ReadFile(full)
				if err != nil {
					return fmt.Errorf("read %s at %q: %w. Tip: try mskill show %s --list", f, full, err, raw)
				}
				cmdPrint(cmd, string(data))
				if !strings.HasSuffix(string(data), "\n") {
					cmdPrint(cmd, "\n")
				}
				if len(selected) > 1 || len(targetFiles) > 1 {
					cmdPrint(cmd, "\n---\n")
				}
			}
			cmdPrint(cmd, fmt.Sprintf("\n---\nskill: %s\ncached at: %s\nskill dir: %s\n", skillRel, cachePath, baseSkillDir))
		}
		return nil
	}
	selected, _ := cache.ListSkills(ctx, cachePath)
	if len(selected) == 0 {
		return fmt.Errorf("no skills found in feed %s after fetch. Tip: check index.json", raw)
	}
	sort.Strings(selected)
	printStep(cmd, "Linking", fmt.Sprintf("%d skill(s) → %s…", len(selected), agentFilter))
	for _, skillRel := range selected {
		cacheSkillPath := cachePath
		if skillRel != "" && skillRel != "." {
			cacheSkillPath = filepath.Join(cachePath, skillRel)
		}
		skillName := resolve.SanitizeName(filepath.Base(skillRel))
		if skillRel == "" || skillRel == "." || skillName == "" {
			skillName = r.Slug
			if skillName == "" {
				skillName = filepath.Base(cacheSkillPath)
			}
		}
		opts := link.InstallOpts{Global: global, Project: project, Agents: agentFilter, LinkMode: linkMode, Copy: copyFlag}
		if err := link.Install(cacheSkillPath, skillName, opts); err != nil {
			return fmt.Errorf("link %q to %s: %w. Tip: check destination writable or try --copy", skillName, agentFilter, err)
		}
		if h, err := link.SkillFolderHash(cacheSkillPath); err == nil {
			doGlobalLock := global || (!global && !project)
			doProjectLock := project || (!global && !project)
			if global && !project {
				doGlobalLock, doProjectLock = true, false
			} else if project && !global {
				doGlobalLock, doProjectLock = false, true
			}
			if doGlobalLock {
				_ = link.UpdateLockfile(true, skillName, h)
			}
			if doProjectLock {
				_ = link.UpdateLockfile(false, skillName, h)
			}
		}
		cmdPrint(cmd, fmt.Sprintf("installed %s -> %s\n", raw, skillName))
		cmdPrint(cmd, fmt.Sprintf("  cache: %s\n", cacheSkillPath))
	}
	return nil
}

// runShowWellKnown implements Resolve → Cache → cat for website feeds.
func runShowWellKnown(cmd *cobra.Command, ctx context.Context, raw string, r *resolve.Resolved, effectiveRef string, files []string, list, force bool) error {
	cachePath, _, err := cache.Ensure(ctx, paths, r, effectiveRef, nil, force)
	if err != nil {
		return fmt.Errorf("Cannot fetch feed %q: %v. Tip: check the site serves /.well-known/agent-skills/index.json (%w)", raw, err, err)
	}
	// Feed-level skill filter from resolve (SkillPath/Slug) if present.
	want := ""
	if r.SkillPath != "" {
		want = r.SkillPath
	} else if r.Slug != "" {
		want = r.Slug
	}
	skills, _ := cache.ListSkills(ctx, cachePath)
	if len(skills) == 0 {
		return fmt.Errorf("no skills found in feed %s (missing SKILL.md)", raw)
	}
	if want != "" {
		// Narrow to matching skill dir.
		matched := ""
		for _, s := range skills {
			if s == want || filepath.Base(s) == filepath.Base(want) || strings.EqualFold(resolve.SanitizeName(filepath.Base(s)), resolve.SanitizeName(want)) {
				matched = s
				break
			}
		}
		if matched == "" {
			return fmt.Errorf("skill %q not found in feed %s. Available: %s. Tip: mskill show %s --list", want, raw, strings.Join(allDisplay(skills), ", "), raw)
		}
		skills = []string{matched}
	}
	if list && want == "" && len(skills) > 1 {
		for _, s := range skills {
			disp := s
			if disp == "" {
				disp = "."
			}
			cmdPrint(cmd, disp+"\n")
		}
		cmdPrint(cmd, fmt.Sprintf("\n---\ncached at: %s\n", cachePath))
		return nil
	}
	baseSkillDir := cachePath
	if len(skills) == 1 && skills[0] != "" {
		baseSkillDir = filepath.Join(cachePath, skills[0])
	} else if want != "" && len(skills) == 1 {
		if skills[0] != "" {
			baseSkillDir = filepath.Join(cachePath, skills[0])
		}
	}
	if list {
		entries, err := os.ReadDir(baseSkillDir)
		if err != nil {
			return fmt.Errorf("list %q: %w", baseSkillDir, err)
		}
		for _, e := range entries {
			cmdPrint(cmd, e.Name()+"\n")
		}
		cmdPrint(cmd, fmt.Sprintf("\n---\nskill dir: %s\n", baseSkillDir))
		return nil
	}
	targetFiles := files
	if len(targetFiles) == 0 {
		targetFiles = []string{"SKILL.md"}
	}
	for _, f := range targetFiles {
		f = strings.ReplaceAll(f, "\\", "/")
		sanitized, err := resolve.SanitizeSubpath(f)
		if err != nil {
			return fmt.Errorf("invalid --file %q: %v (%w)", f, err, err)
		}
		full := filepath.Join(baseSkillDir, sanitized)
		data, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("read %s at %q: %w. Tip: try mskill show %s --list", f, full, err, raw)
		}
		out := string(data)
		if strings.EqualFold(filepath.Base(sanitized), "SKILL.md") {
			out = stripYAMLFrontmatter(out)
		}
		cmdPrint(cmd, out)
		if !strings.HasSuffix(out, "\n") {
			cmdPrint(cmd, "\n")
		}
	}
	feedHint := raw
	_ = wellknown.AgentSkillsPath
	cmdPrint(cmd, fmt.Sprintf("\n---\ncached at: %s\nskill dir: %s\nexplore: ls %s  |  mskill show %s --list\n", cachePath, baseSkillDir, baseSkillDir, feedHint))
	return nil
}
