package names

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ExpandHome expands leading "~/" or "~\" and lone "~" using os.UserHomeDir.
// Single source of truth (previously duplicated in cmd/helpers and internal/link).
func ExpandHome(p string) string {
	if len(p) >= 2 && p[0] == '~' && (p[1] == '/' || p[1] == '\\') {
		home, _ := os.UserHomeDir()
		if home == "" {
			return p[2:]
		}
		return filepath.Join(home, p[2:])
	}
	if p == "~" {
		home, _ := os.UserHomeDir()
		if home != "" {
			return home
		}
		return p
	}
	return p
}

// SanitizeName lowercases and replaces [^a-z0-9._] with "-", trims "-".
// Single source of truth for skill name sanitization (previously duplicated in resolve and link).
func SanitizeName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	s := b.String()
	s = strings.Trim(s, "-")
	return s
}

// SanitizeSubpath cleans and validates a subpath, rejecting ".." segments.
func SanitizeSubpath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	// shares validation logic; delegates to resolve's implementation via path.Clean checks
	// duplicated here to avoid import cycle - keep in sync with resolve.SanitizeSubpath
	p = strings.ReplaceAll(p, "\\", "/")
	origParts := strings.Split(p, "/")
	for _, part := range origParts {
		if part == ".." {
			return "", &invalidSubpathError{p}
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", &invalidSubpathError{p}
	}
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		if part == ".." {
			return "", &invalidSubpathError{p}
		}
	}
	cleaned = strings.TrimPrefix(cleaned, "/")
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", &invalidSubpathError{p}
	}
	return cleaned, nil
}

type invalidSubpathError struct{ p string }

func (e *invalidSubpathError) Error() string { return "invalid subpath " + `"` + e.p + `"` + `: contains ".."` }
