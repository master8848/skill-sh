package resolve

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"skill.sh/mskill/internal/names"
)

// Resolved represents a parsed skill source.
type Resolved struct {
	Host      string
	Owner     string
	Repo      string
	SkillPath string
	Slug      string
	Ref       string
	Subpath   string
	CloneURL  string
	Source    string
	Type      string
	IsLocal   bool
}

// SOURCE_ALIASES maps shorthand aliases to github paths.
var SOURCE_ALIASES = map[string]string{
	"coinbase/agentWallet":     "github:coinbase/agentWallet",
	"vercel-labs/agent-skills": "github:vercel-labs/agent-skills",
	"coinbase/agentkit":        "github:coinbase/agentkit",
	"anthropics/skills":        "github:anthropics/skills",
}

// regex for github tree and bare repo
var (
	githubTreeRe       = regexp.MustCompile(`^github\.com/([^/]+)/([^/]+)/tree/([^/]+)(?:/(.*))?$`)
	githubBareRe       = regexp.MustCompile(`^github\.com/([^/]+)/([^/]+)/?$`)
	gitlabTreeRe       = regexp.MustCompile(`^gitlab\.com/([^/]+)/([^/]+)/-/tree/([^/]+)(?:/(.*))?$`)
	gitlabTreeSimpleRe = regexp.MustCompile(`^gitlab\.com/([^/]+)/([^/]+)/tree/([^/]+)(?:/(.*))?$`)
	gitlabBareRe       = regexp.MustCompile(`^gitlab\.com/([^/]+)/([^/]+)/?$`)
)

// SanitizeSubpath cleans and validates a subpath, rejecting ".." segments.
func SanitizeSubpath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	// Normalize Windows separators before validation to prevent `a\..\b` bypass.
	p = strings.ReplaceAll(p, "\\", "/")
	// Reject ".." segments in original
	origParts := strings.Split(p, "/")
	for _, part := range origParts {
		if part == ".." {
			return "", fmt.Errorf("invalid subpath %q: contains \"..\" — subpath must stay inside skill directory. Tip: use a relative path without \"..\" (e.g., \"skillname\" or \"path/to/skill\")", p)
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." {
		return "", nil
	}
	// After clean, check for ".." again
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("invalid subpath %q: contains \"..\" — subpath must stay inside skill directory. Tip: use a relative path without \"..\"", p)
	}
	parts := strings.Split(cleaned, "/")
	for _, part := range parts {
		if part == ".." {
			return "", fmt.Errorf("invalid subpath %q: contains \"..\" — subpath must stay inside skill directory", p)
		}
	}
	// Must be inside skill: no absolute
	cleaned = strings.TrimPrefix(cleaned, "/")
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." {
		return "", nil
	}
	// final check
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("invalid subpath %q: contains \"..\" — subpath must stay inside skill directory", p)
	}
	return cleaned, nil
}

// SanitizeName delegates to internal/names (single source of truth).
func SanitizeName(name string) string { return names.SanitizeName(name) }

// cloneURL builds https clone URL for host/owner/repo.
func cloneURL(host, owner, repo string) string {
	return fmt.Sprintf("https://%s/%s/%s.git", host, owner, repo)
}

func parseColonPrefix(host, workingInput, atFilter, fragmentRef, orig string) (*Resolved, error) {
	shortHost := strings.Split(host, ".")[0]
	prefix := shortHost + ":"
	// also accept host+":" (e.g. "github.com:") for completeness
	if !strings.HasPrefix(workingInput, prefix) && !strings.HasPrefix(workingInput, host+":") {
		return nil, nil
	}
	remainder := workingInput
	if strings.HasPrefix(remainder, prefix) {
		remainder = strings.TrimPrefix(remainder, prefix)
	} else {
		remainder = strings.TrimPrefix(remainder, host+":")
	}
	remainder = strings.TrimPrefix(remainder, "/")
	remainder = strings.Trim(remainder, "/")
	remainder = strings.TrimSuffix(remainder, ".git")
	parts := strings.Split(remainder, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil
	}
	owner, repo := parts[0], parts[1]
	skillPath := ""
	if len(parts) > 2 {
		skillPath = strings.Join(parts[2:], "/")
	}
	if skillPath == "" && atFilter != "" {
		skillPath = atFilter
	}
	if skillPath != "" {
		sanitized, err := SanitizeSubpath(skillPath)
		if err != nil {
			return nil, err
		}
		skillPath = sanitized
	}
	slug := ""
	if skillPath != "" {
		slug = SanitizeName(path.Base(skillPath))
	} else if atFilter != "" {
		slug = SanitizeName(atFilter)
	}
	return &Resolved{
		Host:      host,
		Owner:     owner,
		Repo:      repo,
		SkillPath: skillPath,
		Subpath:   skillPath,
		Slug:      slug,
		Ref:       fragmentRef,
		CloneURL:  cloneURL(host, owner, repo),
		Source:    orig,
		Type:      "git",
		IsLocal:   false,
	}, nil
}

// GetOwnerRepo extracts owner and repo from a source string.
func GetOwnerRepo(source string) (owner, repo string) {
	s := strings.TrimSpace(source)
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	// strip scheme if URL
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil {
			s = strings.Trim(u.Path, "/")
		}
	}
	// strip github: / gitlab: prefix
	if strings.HasPrefix(s, "github:") {
		s = strings.TrimPrefix(s, "github:")
		s = strings.TrimPrefix(s, "/")
	} else if strings.HasPrefix(s, "gitlab:") {
		s = strings.TrimPrefix(s, "gitlab:")
		s = strings.TrimPrefix(s, "/")
	}
	s = strings.Trim(s, "/")
	// strip fragment and filter
	if idx := strings.Index(s, "#"); idx != -1 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "@"); idx != -1 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "/tree/"); idx != -1 {
		s = s[:idx]
	}
	// also handle gitlab /-/tree
	if idx := strings.Index(s, "/-/tree/"); idx != -1 {
		s = s[:idx]
	}
	parts := strings.Split(s, "/")
	// Need to skip host if present like github.com/owner/repo
	if len(parts) >= 3 && (parts[0] == "github.com" || parts[0] == "gitlab.com" || strings.Contains(parts[0], ".")) {
		// if first part looks like host, shift
		if len(parts) >= 3 {
			owner = parts[1]
			repo = parts[2]
			return owner, repo
		}
	}
	if len(parts) >= 2 {
		owner = parts[0]
		repo = parts[1]
	}
	return owner, repo
}

// GetGHHost returns GH_HOST env or github.com
func GetGHHost() string {
	h := strings.TrimSpace(os.Getenv("GH_HOST"))
	if h == "" {
		return "github.com"
	}
	// strip scheme if present
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	h = strings.TrimSuffix(h, "/")
	h = strings.TrimSpace(h)
	if h == "" {
		return "github.com"
	}
	return h
}

// IsLocalPath reports whether input looks like a local filesystem path.
func IsLocalPath(input string) bool {
	if input == "" {
		return false
	}
	if strings.HasPrefix(input, "./") || strings.HasPrefix(input, "../") || strings.HasPrefix(input, "~/") {
		return true
	}
	if strings.HasPrefix(input, "/") || strings.HasPrefix(input, `\`) {
		return true
	}
	if filepath.IsAbs(input) {
		return true
	}
	// Windows absolute C:\ or C:/
	if len(input) >= 3 && ((input[0] >= 'a' && input[0] <= 'z') || (input[0] >= 'A' && input[0] <= 'Z')) && input[1] == ':' && (input[2] == '\\' || input[2] == '/') {
		return true
	}
	// Also handle input like "C:\path" without slash check?
	if len(input) >= 2 && ((input[0] >= 'a' && input[0] <= 'z') || (input[0] >= 'A' && input[0] <= 'Z')) && input[1] == ':' {
		return true
	}
	return false
}

// isLocalPath unexported alias
func isLocalPath(input string) bool { return IsLocalPath(input) }

// IsHostedArtifactUrl checks if URL is a hosted artifact.
func IsHostedArtifactUrl(u string) bool {
	if strings.Contains(u, "raw.githubusercontent.com") {
		return true
	}
	if strings.Contains(u, "codeload.github.com") {
		return true
	}
	if strings.Contains(u, "github.com") && (strings.Contains(u, "/archive/") || strings.Contains(u, "/raw/") || strings.Contains(u, "/releases/") || strings.Contains(u, "/archive")) {
		// more precise: check path contains those segments
		// Use simple contains for now
		if strings.Contains(u, "/archive/") || strings.Contains(u, "/raw/") || strings.Contains(u, "/releases/") {
			return true
		}
	}
	return false
}

func isHostedArtifactUrl(u string) bool { return IsHostedArtifactUrl(u) }

// IsWellKnownUrl checks for well-known skills URLs.
func IsWellKnownUrl(u string) bool {
	return strings.Contains(u, ".well-known/skills") || strings.Contains(u, ".well-known/agent-skills")
}

func isWellKnownUrl(u string) bool { return IsWellKnownUrl(u) }

// isWellKnown helper already

// ParseSkillRef parses a skill reference according to whitepaper precedence.
func ParseSkillRef(input string) (*Resolved, error) {
	orig := strings.TrimSpace(input)
	if orig == "" {
		return nil, fmt.Errorf("empty skill reference: expected owner/repo or owner/repo/skill. Try: mskill get owner/repo --skill <name> --list or mskill search <keywords>")
	}

	// 1. Local path → Type=local
	if IsLocalPath(orig) {
		slugBase := path.Base(orig)
		// also handle filepath base for windows?
		if slugBase == "." || slugBase == "/" || slugBase == `\` {
			slugBase = ""
		}
		slug := SanitizeName(slugBase)
		// For local, Subpath/SkillPath empty, but could treat? Keep empty.
		return &Resolved{
			Host:     "local",
			Source:   orig,
			CloneURL: orig,
			Type:     "local",
			IsLocal:  true,
			Slug:     slug,
		}, nil
	}

	// 2. Fragment #ref handling
	fragmentRef := ""
	fragmentFilter := ""
	baseInput := orig
	if idx := strings.Index(orig, "#"); idx != -1 {
		base := orig[:idx]
		after := orig[idx+1:]
		isGitSource := strings.Contains(base, "/") || strings.HasPrefix(base, "github:") || strings.HasPrefix(base, "gitlab:") || strings.HasPrefix(base, "https://") || strings.HasPrefix(base, "http://")
		if isGitSource {
			if atIdx := strings.Index(after, "@"); atIdx != -1 {
				fragmentRef = after[:atIdx]
				fragmentFilter = after[atIdx+1:]
			} else {
				fragmentRef = after
			}
			baseInput = base
		}
	}

	workingInput := baseInput
	atFilter := fragmentFilter

	// Handle suffix "@skillFilter" if not already from fragment
	if fragmentFilter == "" {
		// Only if not a URL (no "://") to avoid mis-parsing URLs with userinfo
		if strings.Contains(workingInput, "@") && !strings.Contains(workingInput, "://") {
			// Find last "@"
			if idx := strings.LastIndex(workingInput, "@"); idx != -1 {
				before := workingInput[:idx]
				after := workingInput[idx+1:]
				// before must look like git source (contains "/" or github:/gitlab:)
				isGitSource := strings.Contains(before, "/") || strings.HasPrefix(before, "github:") || strings.HasPrefix(before, "gitlab:")
				if isGitSource && after != "" && !strings.Contains(after, "/") && !strings.Contains(after, ":") && !strings.Contains(after, "#") && !strings.Contains(after, "?") {
					atFilter = after
					workingInput = before
				} else if isGitSource && after != "" {
					// Allow after containing "/"? For safety, if after contains "/" but before is github:/gitlab: or contains "/", treat as filter only if after is slug-like (no slash). Otherwise keep as is.
					// If after contains "/" we consider it part of skill path, not filter. So don't split.
				}
			}
		}
	}

	// Normalize .git suffix on workingInput (but not on atFilter/fragment)
	// Trim trailing .git if present and workingInput not already alias?
	trimmedForAlias := strings.TrimSuffix(workingInput, ".git")
	// Check aliases before trimming? Use trimmedForAlias for alias lookup
	aliasKey := workingInput
	aliasKeyTrimmed := trimmedForAlias
	// 3. Aliases
	if expanded, ok := SOURCE_ALIASES[aliasKey]; ok {
		workingInput = expanded
	} else if expanded, ok := SOURCE_ALIASES[aliasKeyTrimmed]; ok {
		workingInput = expanded
	} else {
		// Use trimmed version for further processing
		workingInput = trimmedForAlias
	}

	// For remaining steps, ensure workingInput also trimmed of .git (already)
	workingInput = strings.TrimSuffix(workingInput, ".git")

	// 4. github: / gitlab: prefix
	if r, err := parseColonPrefix("github.com", workingInput, atFilter, fragmentRef, orig); err != nil {
		return nil, err
	} else if r != nil {
		return r, nil
	}
	if r, err := parseColonPrefix("gitlab.com", workingInput, atFilter, fragmentRef, orig); err != nil {
		return nil, err
	} else if r != nil {
		return r, nil
	}

	// 5. Hosted artifact URL
	if IsHostedArtifactUrl(workingInput) || IsHostedArtifactUrl(orig) {
		// Determine host from URL
		host := ""
		cloneURL := orig
		if u, err := url.Parse(workingInput); err == nil && u.Host != "" {
			host = u.Host
		} else if u, err := url.Parse(orig); err == nil && u.Host != "" {
			host = u.Host
		}
		// Try to extract owner/repo if possible for artifact? Not required, but attempt
		owner, repo := GetOwnerRepo(workingInput)
		return &Resolved{
			Host:     host,
			Owner:    owner,
			Repo:     repo,
			Source:   orig,
			CloneURL: cloneURL,
			Type:     "download",
			Ref:      fragmentRef,
			IsLocal:  false,
		}, nil
	}

	// Helper for GH_HOST
	ghHost := GetGHHost()

	// 7. Regex for github.com / gitlab.com / generic GH_HOST tree/bare via https URLs
	if strings.HasPrefix(workingInput, "http://") || strings.HasPrefix(workingInput, "https://") {
		// Well-known check before tree? But priority says well-known is 9 after shorthand 8, but for http URLs we can check tree first, then well-known.
		// First try tree/bare matching
		// Strip scheme for regex matching
		u, err := url.Parse(workingInput)
		if err == nil {
			host := u.Host
			// Remove port if present
			if idx := strings.Index(host, ":"); idx != -1 {
				host = host[:idx]
			}
			pathPart := strings.Trim(u.Path, "/")
			combined := host + "/" + pathPart

			// Try github tree
			if m := githubTreeRe.FindStringSubmatch(combined); m != nil {
				owner := m[1]
				repo := strings.TrimSuffix(m[2], ".git")
				ref := m[3]
				sub := ""
				if len(m) > 4 {
					sub = m[4]
				}
				// fragmentRef overrides if present?
				if fragmentRef != "" {
					ref = fragmentRef
				}
				// atFilter as subpath if sub empty
				if sub == "" && atFilter != "" {
					sub = atFilter
				}
				// Sanitize sub
				if sub != "" {
					sanitized, err := SanitizeSubpath(sub)
					if err != nil {
						return nil, err
					}
					sub = sanitized
				}
				slug := ""
				if sub != "" {
					slug = SanitizeName(path.Base(sub))
				}
				cloneURL := cloneURL(host, owner, repo)
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: sub,
					Subpath:   sub,
					Slug:      slug,
					Ref:       ref,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
			if m := githubBareRe.FindStringSubmatch(combined); m != nil {
				owner := m[1]
				repo := strings.TrimSuffix(m[2], ".git")
				sub := atFilter
				if sub != "" {
					sanitized, err := SanitizeSubpath(sub)
					if err != nil {
						return nil, err
					}
					sub = sanitized
				}
				slug := ""
				if sub != "" {
					slug = SanitizeName(path.Base(sub))
				}
				cloneURL := cloneURL(host, owner, repo)
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: sub,
					Subpath:   sub,
					Slug:      slug,
					Ref:       fragmentRef,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
			// GitLab tree
			if m := gitlabTreeRe.FindStringSubmatch(combined); m != nil {
				owner := m[1]
				repo := strings.TrimSuffix(m[2], ".git")
				ref := m[3]
				sub := ""
				if len(m) > 4 {
					sub = m[4]
				}
				if fragmentRef != "" {
					ref = fragmentRef
				}
				if sub == "" && atFilter != "" {
					sub = atFilter
				}
				if sub != "" {
					sanitized, err := SanitizeSubpath(sub)
					if err != nil {
						return nil, err
					}
					sub = sanitized
				}
				slug := ""
				if sub != "" {
					slug = SanitizeName(path.Base(sub))
				}
				cloneURL := cloneURL(host, owner, repo)
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: sub,
					Subpath:   sub,
					Slug:      slug,
					Ref:       ref,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
			if m := gitlabTreeSimpleRe.FindStringSubmatch(combined); m != nil {
				owner := m[1]
				repo := strings.TrimSuffix(m[2], ".git")
				ref := m[3]
				sub := ""
				if len(m) > 4 {
					sub = m[4]
				}
				if fragmentRef != "" {
					ref = fragmentRef
				}
				if sub == "" && atFilter != "" {
					sub = atFilter
				}
				if sub != "" {
					sanitized, err := SanitizeSubpath(sub)
					if err != nil {
						return nil, err
					}
					sub = sanitized
				}
				slug := ""
				if sub != "" {
					slug = SanitizeName(path.Base(sub))
				}
				cloneURL := cloneURL(host, owner, repo)
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: sub,
					Subpath:   sub,
					Slug:      slug,
					Ref:       ref,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
			if m := gitlabBareRe.FindStringSubmatch(combined); m != nil {
				owner := m[1]
				repo := strings.TrimSuffix(m[2], ".git")
				sub := atFilter
				if sub != "" {
					sanitized, err := SanitizeSubpath(sub)
					if err != nil {
						return nil, err
					}
					sub = sanitized
				}
				slug := ""
				if sub != "" {
					slug = SanitizeName(path.Base(sub))
				}
				cloneURL := cloneURL(host, owner, repo)
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: sub,
					Subpath:   sub,
					Slug:      slug,
					Ref:       fragmentRef,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
			// Generic GH_HOST handling: if GH_HOST != github.com and host == GH_HOST, treat similar to github tree/bare
			if ghHost != "github.com" && host == ghHost {
				// generic git host tree: host/owner/repo/tree/<ref>/<subpath>
				genericTreeRe := regexp.MustCompile(`^` + regexp.QuoteMeta(host) + `/([^/]+)/([^/]+)/tree/([^/]+)(?:/(.*))?$`)
				if m := genericTreeRe.FindStringSubmatch(combined); m != nil {
					owner := m[1]
					repo := strings.TrimSuffix(m[2], ".git")
					ref := m[3]
					sub := ""
					if len(m) > 4 {
						sub = m[4]
					}
					if fragmentRef != "" {
						ref = fragmentRef
					}
					if sub == "" && atFilter != "" {
						sub = atFilter
					}
					if sub != "" {
						sanitized, err := SanitizeSubpath(sub)
						if err != nil {
							return nil, err
						}
						sub = sanitized
					}
					slug := ""
					if sub != "" {
						slug = SanitizeName(path.Base(sub))
					}
					cloneURL := cloneURL(host, owner, repo)
					return &Resolved{
						Host:      host,
						Owner:     owner,
						Repo:      repo,
						SkillPath: sub,
						Subpath:   sub,
						Slug:      slug,
						Ref:       ref,
						CloneURL:  cloneURL,
						Source:    orig,
						Type:      "git",
						IsLocal:   false,
					}, nil
				}
				genericBareRe := regexp.MustCompile(`^` + regexp.QuoteMeta(host) + `/([^/]+)/([^/]+)/?$`)
				if m := genericBareRe.FindStringSubmatch(combined); m != nil {
					owner := m[1]
					repo := strings.TrimSuffix(m[2], ".git")
					sub := atFilter
					if sub != "" {
						sanitized, err := SanitizeSubpath(sub)
						if err != nil {
							return nil, err
						}
						sub = sanitized
					}
					slug := ""
					if sub != "" {
						slug = SanitizeName(path.Base(sub))
					}
					cloneURL := cloneURL(host, owner, repo)
					return &Resolved{
						Host:      host,
						Owner:     owner,
						Repo:      repo,
						SkillPath: sub,
						Subpath:   sub,
						Slug:      slug,
						Ref:       fragmentRef,
						CloneURL:  cloneURL,
						Source:    orig,
						Type:      "git",
						IsLocal:   false,
					}, nil
				}
			}
			// If http URL not matched tree/bare but is GH_HOST generic bare? Could fallback to Type git with host/owner/repo parsing
			// Check well-known before shorthand fallback for http URLs
			if IsWellKnownUrl(workingInput) || IsWellKnownUrl(orig) {
				// Well-known URL → Type=well-known
				h := host
				if h == "" {
					h = ghHost
				}
				return &Resolved{
					Host:     h,
					Source:   orig,
					CloneURL: orig,
					Type:     "well-known",
					Ref:      fragmentRef,
					IsLocal:  false,
				}, nil
			}
			// Hosted artifact already handled earlier, but double check
			if IsHostedArtifactUrl(workingInput) {
				return &Resolved{
					Host:     host,
					Source:   orig,
					CloneURL: orig,
					Type:     "download",
					Ref:      fragmentRef,
					IsLocal:  false,
				}, nil
			}
			// For any other http(s) URL that looks like git host, try generic fallback: extract owner/repo from path
			parts := strings.Split(pathPart, "/")
			if len(parts) >= 2 {
				owner := parts[0]
				repo := strings.TrimSuffix(parts[1], ".git")
				sub := ""
				if len(parts) > 2 {
					sub = strings.Join(parts[2:], "/")
					// Remove tree prefix if present but not matched earlier? Could be extra.
					if strings.HasPrefix(sub, "tree/") {
						// try to parse ref
						// This is fallback, not tree-matched, so just treat as subpath
					}
				}
				if sub == "" && atFilter != "" {
					sub = atFilter
				}
				if sub != "" {
					// sanitize but if contains ".." reject
					cleaned, err := SanitizeSubpath(sub)
					if err != nil {
						return nil, err
					}
					sub = cleaned
				}
				slug := ""
				if sub != "" {
					slug = SanitizeName(path.Base(sub))
				}
				cloneURL := cloneURL(host, owner, repo)
				// If no clear owner/repo, treat as Type download or git? Fallback to git
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: sub,
					Subpath:   sub,
					Slug:      slug,
					Ref:       fragmentRef,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
		}
	}

	// 8. Shorthand owner/repo@filter and owner/repo[/subpath]
	// Uses github.com vs generic git per GH_HOST
	if !strings.Contains(workingInput, "://") && strings.Contains(workingInput, "/") && !strings.HasPrefix(workingInput, "github:") && !strings.HasPrefix(workingInput, "gitlab:") {
		// Must not be local, not URL, contains "/"
		trimmed := strings.Trim(workingInput, "/")
		// Reject if looks like URL path with dots? But shorthand is owner/repo
		parts := strings.Split(trimmed, "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			// Validate owner/repo not containing "." like host? But owner may contain dot? Simplify allow.
			// Ensure parts[0] and parts[1] are not empty and not containing ":" etc
			valid := true
			for _, p := range parts[:2] {
				if strings.Contains(p, ":") || strings.Contains(p, "?") || strings.Contains(p, "#") {
					valid = false
				}
			}
			if valid {
				owner := parts[0]
				repo := parts[1]
				repo = strings.TrimSuffix(repo, ".git")
				skillPath := ""
				if len(parts) > 2 {
					skillPath = strings.Join(parts[2:], "/")
				}
				if skillPath == "" && atFilter != "" {
					skillPath = atFilter
				}
				// If both skillPath and atFilter present, skillPath takes precedence (as per /subpath vs @filter)
				if skillPath != "" {
					sanitized, err := SanitizeSubpath(skillPath)
					if err != nil {
						return nil, err
					}
					skillPath = sanitized
				}
				slug := ""
				if skillPath != "" {
					slug = SanitizeName(path.Base(skillPath))
				}
				subpath := skillPath
				host := ghHost
				cloneURL := cloneURL(host, owner, repo)
				return &Resolved{
					Host:      host,
					Owner:     owner,
					Repo:      repo,
					SkillPath: skillPath,
					Subpath:   subpath,
					Slug:      slug,
					Ref:       fragmentRef,
					CloneURL:  cloneURL,
					Source:    orig,
					Type:      "git",
					IsLocal:   false,
				}, nil
			}
		}
	}

	// 9. Well-known URL (https://…/.well-known/(agent-)skills) → Type=well-known
	if IsWellKnownUrl(workingInput) || IsWellKnownUrl(orig) {
		host := ghHost
		cloneURL := orig
		if u, err := url.Parse(workingInput); err == nil && u.Host != "" {
			host = u.Host
		} else if u, err := url.Parse(orig); err == nil && u.Host != "" {
			host = u.Host
		}
		return &Resolved{
			Host:     host,
			Source:   orig,
			CloneURL: cloneURL,
			Type:     "well-known",
			Ref:      fragmentRef,
			IsLocal:  false,
		}, nil
	}

	// 10. Fallback Type=git
	// Try to extract owner/repo via GetOwnerRepo
	owner, repo := GetOwnerRepo(workingInput)
	host := ghHost
	outCloneURL := workingInput
	skillPath := ""
	subpath := ""
	slug := ""
	ref := fragmentRef
	if owner != "" && repo != "" {
		outCloneURL = cloneURL(host, owner, repo)
		// Try to extract skillPath from parts beyond owner/repo
		trimmed := strings.Trim(workingInput, "/")
		parts := strings.Split(trimmed, "/")
		// Find owner index
		idxOwner := -1
		for i, p := range parts {
			if p == owner && i+1 < len(parts) && parts[i+1] == repo {
				idxOwner = i
				break
			}
		}
		if idxOwner != -1 && len(parts) > idxOwner+2 {
			skillPath = strings.Join(parts[idxOwner+2:], "/")
			if skillPath != "" {
				sanitized, err := SanitizeSubpath(skillPath)
				if err != nil {
					return nil, err
				}
				skillPath = sanitized
			}
			if skillPath != "" {
				slug = SanitizeName(path.Base(skillPath))
			}
			subpath = skillPath
		} else if atFilter != "" {
			skillPath = atFilter
			subpath = skillPath
			slug = SanitizeName(path.Base(skillPath))
		}
		// If fallback has no owner/repo but atFilter, use it
		if skillPath == "" && atFilter != "" {
			skillPath = atFilter
			subpath = skillPath
			slug = SanitizeName(path.Base(skillPath))
		}
	} else {
		// No owner/repo found, treat whole as maybe git URL?
		outCloneURL = workingInput
		if atFilter != "" {
			skillPath = atFilter
			subpath = skillPath
			slug = SanitizeName(path.Base(skillPath))
		}
	}
	return &Resolved{
		Host:      host,
		Owner:     owner,
		Repo:      repo,
		SkillPath: skillPath,
		Subpath:   subpath,
		Slug:      slug,
		Ref:       ref,
		CloneURL:  outCloneURL,
		Source:    orig,
		Type:      "git",
		IsLocal:   false,
	}, nil
}
