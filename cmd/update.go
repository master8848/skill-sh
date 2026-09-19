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
)

var updateCmd = &cobra.Command{
	Use:     "update [skills...]",
	Aliases: []string{"upgrade"},
	Short:   "Update skills to latest (re-fetch + re-link)",
	Long:    "Re-fetch from git and re-link. Without args, updates all installed skills. With args, updates named skills (bare slug or owner/repo/skill).",
	Example: `  mskill update
  mskill update anki-import-cli --global -y
  mskill update master8848/Anki-import --skill anki-import-cli --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		globalOnly, _ := cmd.Flags().GetBool("global")
		projectOnly, _ := cmd.Flags().GetBool("project")
		force, _ := cmd.Flags().GetBool("force")
		yesFlag, _ := cmd.Flags().GetBool("yes")
		if yesFlag {
			viper.Set("yes", true)
		}
		_ = force

		// 1. No args: update all installed skills
		if len(args) == 0 {
			names := installedSkillNames(globalOnly, projectOnly)
			if len(names) == 0 {
				cmd.Println("No skills installed")
				return nil
			}
			sort.Strings(names)
			hadError := false
			for _, n := range names {
				r, err := findResolvedForBareSlug(n, globalOnly, projectOnly)
				if err != nil {
					cmd.Printf("skip %s: %v\n", n, err)
					continue
				}
				ref := r.Ref
				var skillPaths []string
				if r.SkillPath != "" {
					skillPaths = []string{r.SkillPath}
				}
				cachePath, _, err := cache.Ensure(ctx, paths, r, ref, skillPaths, true)
				if err != nil {
					cmd.Printf("update fetch %q: %v\n", n, err)
					hadError = true
					continue
				}
				cacheSkillPath := cachePath
				if r.SkillPath != "" {
					cacheSkillPath = filepath.Join(cachePath, r.SkillPath)
				}
				if _, err := os.Stat(cacheSkillPath); err != nil {
					cacheSkillPath = cachePath
				}
				skillName := r.Slug
				if skillName == "" {
					skillName = filepath.Base(r.SkillPath)
				}
				if skillName == "" {
					skillName = r.Repo
				}
				if skillName == "" {
					skillName = n
				}
				opts := link.InstallOpts{Global: globalOnly, Project: projectOnly, Copy: false}
				if err := link.Install(cacheSkillPath, skillName, opts); err != nil {
					cmd.Printf("update install %q: %v\n", n, err)
					hadError = true
					continue
				}
				cmd.Printf("updated %s (%s)\n", n, cachePath)
			}
			if hadError {
				return fmt.Errorf("some skills failed to update")
			}
			return nil
		}
		for _, raw := range args {
			var r *resolve.Resolved
			if strings.Contains(raw, "/") {
				parsed, err := resolve.ParseSkillRef(raw)
				if err != nil {
					return fmt.Errorf("resolve %q: %w", raw, err)
				}
				if parsed.Owner == "" || parsed.Repo == "" {
					return fmt.Errorf("update requires fully qualified owner/repo/skill, got %q", raw)
				}
				r = parsed
			} else {
				// bare slug: resolve via installed lookup
				resolved, err := findResolvedForBareSlug(raw, globalOnly, projectOnly)
				if err != nil {
					return fmt.Errorf("cannot resolve %q for update: %w. Tip: try mskill update %s/%s or check installed with mskill list", raw, err, raw, raw)
				}
				r = resolved
			}
			ref := r.Ref
			var skillPaths []string
			if r.SkillPath != "" {
				skillPaths = []string{r.SkillPath}
			}
			cachePath, _, err := cache.Ensure(ctx, paths, r, ref, skillPaths, true)
			if err != nil {
				return fmt.Errorf("update fetch %q: %w", raw, err)
			}
			// Re-link
			cacheSkillPath := cachePath
			if r.SkillPath != "" {
				cacheSkillPath = filepath.Join(cachePath, r.SkillPath)
			}
			if _, err := os.Stat(cacheSkillPath); err != nil {
				cacheSkillPath = cachePath
			}
			skillName := r.Slug
			if skillName == "" {
				skillName = filepath.Base(r.SkillPath)
			}
			if skillName == "" {
				skillName = r.Repo
			}
			// Determine link opts: respect global/project flags
			opts := link.InstallOpts{Global: globalOnly, Project: projectOnly, Copy: false}
			if err := link.Install(cacheSkillPath, skillName, opts); err != nil {
				return fmt.Errorf("link %q to target failed: %w. Tip: check destination writable or try --copy", skillName, err)
			}
			cmd.Printf("updated %s (%s)\n", raw, cachePath)
		}
		return nil
	},
}

func installedSkillNames(globalOnly, projectOnly bool) []string {
	agents := link.ResolveDestinations("*", false, false)
	if len(agents) == 0 {
		agents = link.ResolveDestinations("", false, false)
	}
	found := map[string]bool{}
	var names []string
	for _, ag := range agents {
		var dirs []string
		doGlobal := false
		doProject := false
		if globalOnly && !projectOnly {
			doGlobal = true
		} else if projectOnly && !globalOnly {
			doProject = true
		} else if globalOnly && projectOnly {
			doGlobal = true
			doProject = true
		} else {
			doGlobal = true
			doProject = true
		}
		if doGlobal {
			g := expandHome(ag.GlobalDir)
			if g != "" {
				dirs = append(dirs, g)
			}
		}
		if doProject {
			p := ag.ProjectDir
			if filepath.IsAbs(p) {
				p = expandHome(p)
			} else {
				wd, _ := os.Getwd()
				p = filepath.Join(wd, p)
			}
			dirs = append(dirs, p)
		}
		for _, d := range dirs {
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
					name := e.Name()
					sanitized := link.SanitizeName(name)
					if sanitized == "" {
						continue
					}
					if found[sanitized] {
						continue
					}
					found[sanitized] = true
					names = append(names, sanitized)
				}
			}
		}
	}
	return names
}

func findResolvedForBareSlug(slug string, globalOnly, projectOnly bool) (*resolve.Resolved, error) {
	sanitized := link.SanitizeName(slug)
	if sanitized == "" {
		sanitized = link.SanitizeName(strings.TrimSpace(slug))
	}
	if sanitized == "" {
		return nil, fmt.Errorf("invalid skill name %q", slug)
	}
	// First try to resolve via installed symlink target
	agents := link.ResolveDestinations("*", false, false)
	if len(agents) == 0 {
		agents = link.ResolveDestinations("", false, false)
	}
	for _, ag := range agents {
		var dirs []string
		doGlobal := false
		doProject := false
		if globalOnly && !projectOnly {
			doGlobal = true
		} else if projectOnly && !globalOnly {
			doProject = true
		} else if globalOnly && projectOnly {
			doGlobal = true
			doProject = true
		} else {
			doGlobal = true
			doProject = true
		}
		if doGlobal {
			g := expandHome(ag.GlobalDir)
			if g != "" {
				dirs = append(dirs, g)
			}
		}
		if doProject {
			p := ag.ProjectDir
			if filepath.IsAbs(p) {
				p = expandHome(p)
			} else {
				wd, _ := os.Getwd()
				p = filepath.Join(wd, p)
			}
			dirs = append(dirs, p)
		}
		for _, d := range dirs {
			installedPath := filepath.Join(d, sanitized)
			if _, err := os.Lstat(installedPath); err != nil {
				// try original slug case (sanitized already lowercased, but check direct)
				alt := filepath.Join(d, slug)
				if alt != installedPath {
					if _, err2 := os.Lstat(alt); err2 == nil {
						installedPath = alt
					} else {
						continue
					}
				} else {
					continue
				}
			}
			// Try symlink target
			target, err := os.Readlink(installedPath)
			if err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(installedPath), target)
				}
				if abs, aerr := filepath.Abs(target); aerr == nil {
					target = abs
				}
				// Walk up to find .mskill-meta.json
				dir := target
				for i := 0; i < 10; i++ {
					metaPath := filepath.Join(dir, ".mskill-meta.json")
					if _, serr := os.Stat(metaPath); serr == nil {
						m, lerr := cache.LoadMeta(metaPath)
						if lerr == nil {
							rel, _ := filepath.Rel(dir, target)
							if rel == "." {
								rel = ""
							} else {
								rel = filepath.ToSlash(rel)
							}
							r := &resolve.Resolved{
								Host:      m.Host,
								Owner:     m.Owner,
								Repo:      m.Repo,
								Ref:       m.Ref,
								CloneURL:  m.CloneURL,
								SkillPath: rel,
								Slug:      sanitized,
								Source:    fmt.Sprintf("%s/%s", m.Owner, m.Repo),
								Type:      "git",
							}
							if r.Host == "" {
								r.Host = "github.com"
							}
							if r.CloneURL == "" && r.Owner != "" && r.Repo != "" {
								r.CloneURL = fmt.Sprintf("https://%s/%s/%s.git", r.Host, r.Owner, r.Repo)
							}
							// Validate owner/repo present
							if r.Owner != "" && r.Repo != "" {
								return r, nil
							}
						}
						break
					}
					parent := filepath.Dir(dir)
					if parent == dir {
						break
					}
					dir = parent
				}
			} else {
				// Not a symlink (copy mode). Fall through to cache scan.
				// But we know installed skill exists, so we should try cache fallback.
				// We break out of agent loop to go to cache fallback rather than continue scanning other agents blindly.
				// However we should still try other agents' symlinks first; so we continue scanning.
				// We'll handle cache fallback after full loop.
			}
		}
	}
	// Fallback: scan cache for matching skill
	ctx := context.Background()
	entries, err := cache.List(paths)
	if err == nil {
		for _, e := range entries {
			// Check SparsePaths
			for _, sp := range e.Meta.SparsePaths {
				base := link.SanitizeName(filepath.Base(sp))
				if base == sanitized {
					rel := filepath.ToSlash(sp)
					r := &resolve.Resolved{
						Host:      e.Meta.Host,
						Owner:     e.Meta.Owner,
						Repo:      e.Meta.Repo,
						Ref:       e.Meta.Ref,
						CloneURL:  e.Meta.CloneURL,
						SkillPath: rel,
						Slug:      sanitized,
						Source:    fmt.Sprintf("%s/%s", e.Meta.Owner, e.Meta.Repo),
						Type:      "git",
					}
					if r.Host == "" {
						r.Host = "github.com"
					}
					if r.CloneURL == "" && r.Owner != "" && r.Repo != "" {
						r.CloneURL = fmt.Sprintf("https://%s/%s/%s.git", r.Host, r.Owner, r.Repo)
					}
					return r, nil
				}
			}
			// Check ListSkills (covers sparse/filter cases)
			skills, _ := cache.ListSkills(ctx, e.Path)
			for _, sk := range skills {
				// sk may be "" for root skill; skip
				if sk == "" {
					continue
				}
				base := link.SanitizeName(filepath.Base(sk))
				if base == sanitized {
					r := &resolve.Resolved{
						Host:      e.Meta.Host,
						Owner:     e.Meta.Owner,
						Repo:      e.Meta.Repo,
						Ref:       e.Meta.Ref,
						CloneURL:  e.Meta.CloneURL,
						SkillPath: filepath.ToSlash(sk),
						Slug:      sanitized,
						Source:    fmt.Sprintf("%s/%s", e.Meta.Owner, e.Meta.Repo),
						Type:      "git",
					}
					if r.Host == "" {
						r.Host = "github.com"
					}
					if r.CloneURL == "" && r.Owner != "" && r.Repo != "" {
						r.CloneURL = fmt.Sprintf("https://%s/%s/%s.git", r.Host, r.Owner, r.Repo)
					}
					return r, nil
				}
			}
			// Check repo-name fallback (single skill repo where skill name == repo)
			if link.SanitizeName(e.Meta.Repo) == sanitized {
				r := &resolve.Resolved{
					Host:      e.Meta.Host,
					Owner:     e.Meta.Owner,
					Repo:      e.Meta.Repo,
					Ref:       e.Meta.Ref,
					CloneURL:  e.Meta.CloneURL,
					SkillPath: "",
					Slug:      sanitized,
					Source:    fmt.Sprintf("%s/%s", e.Meta.Owner, e.Meta.Repo),
					Type:      "git",
				}
				if r.Host == "" {
					r.Host = "github.com"
				}
				if r.CloneURL == "" && r.Owner != "" && r.Repo != "" {
					r.CloneURL = fmt.Sprintf("https://%s/%s/%s.git", r.Host, r.Owner, r.Repo)
				}
				return r, nil
			}
		}
	}
	return nil, fmt.Errorf("skill %q not found installed; try mskill update owner/repo/skill", slug)
}

func init() {
	rootCmd.AddCommand(updateCmd)
	updateCmd.Flags().BoolP("global", "g", false, "only global")
	updateCmd.Flags().BoolP("project", "p", false, "only project")
	updateCmd.Flags().BoolP("yes", "y", false, "skip confirm")
	updateCmd.Flags().Bool("force", false, "force re-fetch even if tag SHA matches")
}
