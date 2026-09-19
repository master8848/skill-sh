package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	return false
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

// cmdPrint prints to cobra's OutOrStdout (fallback to os.Stdout).
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
