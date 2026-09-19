package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/link"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage cache",
	Long:  "Inspect and maintain the sparse-git cache (~/.cache/mskill). Subcommands: list, gc, path, clean.",
	Example: `  mskill cache list --header | column -t -s $'\t'
  mskill cache path
  mskill cache gc --dry-run
  mskill cache gc`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cacheGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage collect expired cache entries",
	Long:  "Remove stale cache entries by LRU (lastAccess) and max_size. Respects flock .lock.",
	Example: `  mskill cache gc --dry-run
  mskill cache gc`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		deleted, err := cache.GC(paths, dryRun)
		if err != nil {
			return fmt.Errorf("cache gc failed: %w. Tip: check cache dir writable (%s)", err, paths.CacheDir)
		}
		if dryRun {
			if len(deleted) == 0 {
				cmd.Println("dry-run: nothing to gc")
			} else {
				for _, p := range deleted {
					cmd.Printf("would remove %s\n", p)
				}
			}
		} else {
			for _, p := range deleted {
				fmt.Fprintf(os.Stderr, "removed %s\n", p)
			}
			cmd.Printf("gc removed %d entries\n", len(deleted))
		}
		return nil
	},
}

var cachePathCmd = &cobra.Command{
	Use:     "path",
	Short:   "Print cache and dot directories",
	Long:    "Print resolved cache, dot, and config paths (honors --cache-dir / --config / env).",
	Example: `  mskill cache path`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Println("cache:", paths.CacheDir)
		cmd.Println("dot:", paths.DotDir)
		cmd.Println("config:", paths.ConfigFile)
		return nil
	},
}

var cacheCleanCmd = &cobra.Command{
	Use:     "clean",
	Short:   "Remove all cached repos",
	Long:    "Remove all cached repos under --cache-dir (irreversible). Use 'cache gc' for LRU cleanup instead.",
	Example: `  mskill cache clean`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cache.Clean(paths); err != nil {
			return fmt.Errorf("cache clean failed: %w. Tip: check cache dir writable (%s)", err, paths.CacheDir)
		}
		cmd.Println("cache cleaned")
		return nil
	},
}

var cacheListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List cached repos",
	Long:    "List all repos currently cached under --cache-dir (default ~/.cache/mskill/repos). Shows source, ref, commit, size, last access, installed status, skills and path. Installed=yes means a symlink in an agent skills dir points into the cache (copy installs show as no).",
	Example: `  mskill cache list --header | column -t -s $'\t'
  mskill cache ls
  mskill cache list --header | cut -f1,6`,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := cache.List(paths)
		if err != nil {
			return fmt.Errorf("cache list failed: %w. Tip: check cache dir (%s)", err, paths.CacheDir)
		}
		if len(entries) == 0 {
			cmd.Println("no cached repos")
			return nil
		}
		showHeader, _ := cmd.Flags().GetBool("header")
		if showHeader {
			cmd.Println("source\tref\tcommit\tsize\tlastAccess\tinstalled\tskills\tpath")
		}
		for _, e := range entries {
			m := e.Meta
			source := strings.Trim(m.Host+"/"+m.Owner+"/"+m.Repo, "/")
			if m.Host == "" && m.Owner == "" && m.Repo == "" {
				source = "-"
			} else if m.Host == "" {
				source = m.Owner + "/" + m.Repo
			}
			commit := m.CommitSHA
			if len(commit) > 7 {
				commit = commit[:7]
			}
			if commit == "" {
				commit = "-"
			}
			skills := strings.Join(m.SparsePaths, ",")
			if skills == "" {
				skills = "-"
			}
			sizeStr := formatSize(e.Size)
			last := "-"
			if !m.LastAccess.IsZero() {
				last = m.LastAccess.Format(time.RFC3339)
			} else if !m.LastFetch.IsZero() {
				last = m.LastFetch.Format(time.RFC3339)
			}
			installed := "no"
			if isCacheInstalled(e.Path) {
				installed = "yes"
			}
			cmd.Printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", source, m.Ref, commit, sizeStr, last, installed, skills, e.Path)
		}
		return nil
	},
}

func isCacheInstalled(cachePath string) bool {
	absCache, _ := filepath.Abs(cachePath)
	if eval, err := filepath.EvalSymlinks(absCache); err == nil {
		absCache = eval
	}
	agents := link.ResolveDestinations("", false, false)
	seenDirs := map[string]bool{}
	for _, ag := range agents {
		candidates := []string{}
		// global dir (expand ~)
		g := expandHome(ag.GlobalDir)
		if eval, err := filepath.EvalSymlinks(g); err == nil {
			g = eval
		}
		candidates = append(candidates, g)
		// project dir (relative to cwd)
		p := ag.ProjectDir
		if filepath.IsAbs(p) {
			p = expandHome(p)
		} else {
			wd, _ := os.Getwd()
			p = filepath.Join(wd, p)
		}
		if eval, err := filepath.EvalSymlinks(p); err == nil {
			// only use eval if p exists and is not same as original; keep original if eval fails
			_ = eval
			// do not replace p with eval because p may be non-existent; but for prefix check we use eval'd candidate dir when reading
		}
		candidates = append(candidates, p)
		for _, d := range candidates {
			if d == "" || seenDirs[d] {
				continue
			}
			seenDirs[d] = true
			entries, err := os.ReadDir(d)
			if err != nil {
				continue
			}
			for _, ent := range entries {
				dest := filepath.Join(d, ent.Name())
				info, err := os.Lstat(dest)
				if err != nil {
					continue
				}
				if info.Mode()&os.ModeSymlink == 0 {
					continue
				}
				target, err := filepath.EvalSymlinks(dest)
				if err != nil {
					continue
				}
				absTarget, _ := filepath.Abs(target)
				if eval, err := filepath.EvalSymlinks(absTarget); err == nil {
					absTarget = eval
				}
				if absTarget == absCache || strings.HasPrefix(absTarget, absCache+string(os.PathSeparator)) {
					return true
				}
			}
		}
	}
	return false
}

func formatSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	}
	if n < 1024*1024*1024 {
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.2fGB", float64(n)/(1024*1024*1024))
}

func init() {
	rootCmd.AddCommand(cacheCmd)
	cacheCmd.AddCommand(cacheGCCmd)
	cacheCmd.AddCommand(cachePathCmd)
	cacheCmd.AddCommand(cacheCleanCmd)
	cacheCmd.AddCommand(cacheListCmd)

	cacheGCCmd.Flags().Bool("dry-run", false, "show what would be removed without deleting")
	cacheListCmd.Flags().Bool("header", false, "print TSV header")
	_ = fmt.Sprintf
}
