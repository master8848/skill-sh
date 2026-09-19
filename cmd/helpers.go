package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/cache"
	"skill.sh/mskill/internal/config"
	"skill.sh/mskill/internal/names"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/security"
)

// normalizeColonRef replaces first ":" with "/" when prefix contains "/" (e.g. owner/repo:skill -> owner/repo/skill).
// Single source of truth (previously duplicated as normalizeColonRef and normalizeColonRefGet).
func normalizeColonRef(raw string) string {
	if resolve.IsLocalPath(raw) {
		return raw
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	if strings.HasPrefix(raw, "github:") || strings.HasPrefix(raw, "gitlab:") {
		return raw
	}
	idx := strings.Index(raw, ":")
	if idx == -1 {
		return raw
	}
	prefix := raw[:idx]
	if !strings.Contains(prefix, "/") {
		return raw
	}
	return prefix + "/" + raw[idx+1:]
}

// expandHome delegates to internal/names.ExpandHome (single source; keeps package-local wrapper for cmd callers).
func expandHome(p string) string { return names.ExpandHome(p) }

// hasSkillMD reports whether dir contains SKILL.md or skill.md (case-insensitive).
func hasSkillMD(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "skill.md")); err == nil {
		return true
	}
	// Case-insensitive directory scan (covers SKILL.MD / Skill.md on
	// case-sensitive filesystems).
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(e.Name(), "SKILL.md") {
				return true
			}
		}
	}
	return false
}

// skillFilePath resolves requested file inside skill dir case-insensitively.
// For SKILL.md/skill.md it tries exact, case variants, then directory scan.
func skillFilePath(dir, requested string) string {
	requested = strings.ReplaceAll(requested, "\\", "/")
	requested = strings.Trim(requested, "/")
	if requested == "" {
		requested = "SKILL.md"
	}
	exact := filepath.Join(dir, filepath.FromSlash(requested))
	if _, err := os.Stat(exact); err == nil {
		return exact
	}
	// SKILL.md fallback chain (case-insensitive per spec).
	if strings.EqualFold(path.Base(requested), "SKILL.md") {
		for _, cand := range []string{"SKILL.md", "skill.md"} {
			p := filepath.Join(dir, filepath.FromSlash(path.Dir(requested)), cand)
			// path.Dir("SKILL.md") == "." -> Join(dir, ".", cand) is fine.
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		parent := filepath.Join(dir, filepath.FromSlash(path.Dir(requested)))
		if entries, err := os.ReadDir(parent); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.EqualFold(e.Name(), "SKILL.md") {
					return filepath.Join(parent, e.Name())
				}
			}
		}
		return exact
	}
	// Generic case-insensitive leaf match for other files.
	parent := filepath.Join(dir, filepath.FromSlash(path.Dir(requested)))
	base := path.Base(requested)
	if entries, err := os.ReadDir(parent); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(e.Name(), base) {
				return filepath.Join(parent, e.Name())
			}
		}
	}
	return exact
}

// readSkillFile reads requested file from skill dir with case-insensitive fallback.
func readSkillFile(dir, requested string) ([]byte, error) {
	return os.ReadFile(skillFilePath(dir, requested))
}

// resolveSkillRel maps a requested skill rel (bare name like "find-skills" or
// nested "skills/find-skills") to the full git-relative path present in the
// repo. It prefers an on-disk match, then git ls-tree (sparse-aware).
func resolveSkillRel(ctx context.Context, cachePath, rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.Trim(rel, "/")
	if rel == "" || rel == "." {
		return ""
	}
	rel = path.Clean(rel)
	candidate := filepath.Join(cachePath, filepath.FromSlash(rel))
	if hasSkillMD(candidate) {
		return rel
	}
	base := path.Base(rel)
	if found := discoverSkillInCache(cachePath, base); found != "" && hasSkillMD(found) {
		if r, err := filepath.Rel(cachePath, found); err == nil {
			return filepath.ToSlash(r)
		}
	}
	if gitPath := discoverSkillViaGit(ctx, cachePath, base); gitPath != "" {
		return filepath.ToSlash(path.Clean(gitPath))
	}
	// Also try full rel via git in case caller passed nested subpath with
	// different casing or prefix.
	if gitPath := cache.FindSkillGitPath(ctx, cachePath, rel); gitPath != "" {
		return filepath.ToSlash(path.Clean(gitPath))
	}
	return rel
}

// ensureSkillDir materializes and returns the on-disk skill directory for rel.
// It resolves bare names to full git paths (skills/find-skills style),
// unions the sparse checkout via cache.Materialize, and syncs meta best-effort.
func ensureSkillDir(ctx context.Context, cachePath string, p config.Paths, r *resolve.Resolved, ref, rel string, force bool) string {
	if rel == "" || rel == "." {
		return cachePath
	}
	fullRel := resolveSkillRel(ctx, cachePath, rel)
	_ = cache.Materialize(ctx, cachePath, fullRel)
	// Best-effort meta sync so later runs know the full path.
	if r != nil {
		if np, _, err := cache.Ensure(ctx, p, r, ref, []string{fullRel}, force); err == nil {
			_ = np
		}
	}
	candidate := filepath.Join(cachePath, filepath.FromSlash(fullRel))
	if hasSkillMD(candidate) {
		return candidate
	}
	return candidate
}

// allDisplay maps skill paths to display names ("" -> ".").
func allDisplay(all []string) []string {
	out := make([]string, len(all))
	for i, p := range all {
		if p == "" {
			out[i] = "."
		} else {
			out[i] = p
		}
	}
	return out
}

// cloneURL builds https clone URL for host/owner/repo.
func cloneURL(host, owner, repo string) string {
	return fmt.Sprintf("https://%s/%s/%s.git", host, owner, repo)
}

// friendlyFetchError classifies fetch/clone failures into short actionable errors:
// repo not found / not accessible (incl. private needing auth), bad ref,
// or network. Keeps messages to 1-2 lines.
func friendlyFetchError(raw, owner, repo, ref string, err error) error {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	low := strings.ToLower(msg)
	isAuth := strings.Contains(low, "authentication failed") ||
		strings.Contains(low, "could not read username") ||
		strings.Contains(low, "401") || strings.Contains(low, "403") ||
		strings.Contains(low, "permission denied") ||
		strings.Contains(low, "access denied")
	isNotFound := strings.Contains(low, "repository not found") ||
		strings.Contains(low, "not accessible") ||
		strings.Contains(low, "404") ||
		strings.Contains(low, "could not find repository") ||
		strings.Contains(low, "does not exist")
	isBadRef := strings.Contains(low, "couldn't find remote ref") ||
		strings.Contains(low, "unknown revision") ||
		strings.Contains(low, "bad revision") ||
		strings.Contains(low, "remote branch") && strings.Contains(low, "not found")
	isNetwork := strings.Contains(low, "timeout") ||
		strings.Contains(low, "connection refused") ||
		strings.Contains(low, "could not resolve") ||
		strings.Contains(low, "no such host") ||
		strings.Contains(low, "network is unreachable") ||
		strings.Contains(low, "tls")
	repoRef := strings.Trim(owner+"/"+repo, "/")
	if repoRef == "" || repoRef == "/" {
		repoRef = raw
	}
	switch {
	case isAuth:
		return fmt.Errorf("repo %s not accessible (auth failed). Private repo? Set GITHUB_TOKEN/GH_TOKEN and retry (%w)", repoRef, err)
	case isNotFound:
		return fmt.Errorf("repo %s not found or not accessible. Check spelling or visibility (%w)", repoRef, err)
	case isBadRef:
		return fmt.Errorf("ref %q not found in %s. Try --ref main (%w)", ref, repoRef, err)
	case isNetwork:
		return fmt.Errorf("network error fetching %s. Check connection or try --offline (%w)", repoRef, err)
	default:
		if strings.TrimSpace(ref) != "" {
			return fmt.Errorf("cannot fetch %s at ref %q: %v. Try --ref main (%w)", repoRef, ref, err, err)
		}
		return fmt.Errorf("cannot fetch %s: %v. Check repo exists (%w)", repoRef, err, err)
	}
}
func cmdPrint(cmd *cobra.Command, s string) {
	if cmd.OutOrStdout() != nil {
		fmt.Fprint(cmd.OutOrStdout(), s)
	} else {
		fmt.Fprint(os.Stdout, s)
	}
}

// isProjectDirectory reports whether cwd looks like a project (has .agents or .git).
func isProjectDirectory() bool {
	for _, p := range []string{".agents", ".git", ".claude", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// printStep emits a tasteful dim step to stderr when verbose or TTY (so piped TSV stays clean).
func printStep(cmd *cobra.Command, phase, msg string) {
	if !viper.GetBool("verbose") && !security.IsInteractiveTTY() {
		return
	}
	w := os.Stderr
	if cmd != nil && cmd.ErrOrStderr() != nil {
		// prefer cobra's stderr if it's not stdout, but keep stderr for taste
		// we still write to os.Stderr to avoid polluting pipe output
		_ = cmd.ErrOrStderr()
	}
	// dim + arrow: › Resolving ...
	dim := "\x1b[2m"
	reset := "\x1b[0m"
	// avoid color when NO_COLOR or dumb term
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" || viper.GetBool("no-color") {
		dim = ""
		reset = ""
	}
	fmt.Fprintf(w, "%s› %s %s%s\n", dim, phase, msg, reset)
}
