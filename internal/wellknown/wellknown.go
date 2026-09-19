package wellknown

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"skill.sh/mskill/internal/names"
)

const (
	AgentSkillsPath = "/.well-known/agent-skills/index.json"
	SkillsPath      = "/.well-known/skills/index.json"
	maxIndexBytes   = 2 << 20
	maxSkillBytes   = 1 << 20
	httpTimeout     = 15 * time.Second
)

// SkillFile is an inline file (skills.sh v1 style files[]).
type SkillFile struct {
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// SkillEntry is one skill in a feed index. Tolerant field names.
type SkillEntry struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Version     string      `json:"version"`
	Path        string      `json:"path"`
	URL         string      `json:"url"`
	Files       []SkillFile `json:"files"`
}

// FeedIndex is the published feed a website serves.
type FeedIndex struct {
	Version int          `json:"version"`
	Name    string       `json:"name,omitempty"`
	BaseURL string       `json:"baseUrl,omitempty"`
	Updated string       `json:"updatedAt,omitempty"`
	Skills  []SkillEntry `json:"skills"`
}

// Slug returns sanitized skill directory name.
func (e SkillEntry) Slug() string {
	n := e.Name
	if n == "" {
		n = e.Path
		if i := strings.LastIndex(n, "/"); i >= 0 {
			n = n[i+1:]
		}
	}
	s := names.SanitizeName(n)
	if s == "" {
		s = names.SanitizeName(e.Description)
	}
	return s
}

// FeedKey derives a stable short key for a feed URL for cache partitioning.
func FeedKey(feedURL string) string {
	h := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(feedURL))))
	return hex.EncodeToString(h[:])[:12]
}

// stripFragment removes #fragment and ?query used for ref/skill filters.
func stripFragment(s string) string {
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	return s
}

// NormalizeInput strips well-known: prefix and whitespace.
func NormalizeInput(input string) string {
	s := strings.TrimSpace(input)
	if strings.HasPrefix(strings.ToLower(s), "well-known:") {
		s = strings.TrimSpace(s[len("well-known:"):])
	}
	return s
}

// FeedURLCandidates expands user input into ordered index.json URLs to try.
// Accepts full index URLs, well-known dir URLs, bare origins, or bare domains.
func FeedURLCandidates(input string) []string {
	s := stripFragment(NormalizeInput(input))
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + strings.TrimPrefix(strings.TrimPrefix(s, "//"), "/")
	}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		for _, e := range out {
			if e == u {
				return
			}
		}
		out = append(out, u)
	}
	low := strings.ToLower(s)
	if strings.HasSuffix(low, "/index.json") {
		add(s)
		return out
	}
	if strings.Contains(s, ".well-known/") {
		add(strings.TrimSuffix(s, "/") + "/index.json")
		// Also try sibling variant agent-skills <-> skills.
		if strings.Contains(s, ".well-known/agent-skills") {
			add(strings.Replace(s, ".well-known/agent-skills", ".well-known/skills", 1) + "/index.json")
		} else if strings.Contains(s, ".well-known/skills") {
			add(strings.Replace(s, ".well-known/skills", ".well-known/agent-skills", 1) + "/index.json")
		}
		return out
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil
	}
	origin := u.Scheme + "://" + u.Host
	// If input has a non-root path that is not well-known, treat path as
	// feed root first (website may serve feed under subpath), then origin fallbacks.
	p := strings.TrimSuffix(u.Path, "/")
	if p != "" && p != "/" {
		add(origin + p + "/.well-known/agent-skills/index.json")
		add(origin + p + "/.well-known/skills/index.json")
		if strings.HasSuffix(p, ".json") {
			add(origin + p)
		}
	}
	add(origin + AgentSkillsPath)
	add(origin + SkillsPath)
	return out
}

// fetchOne GETs a URL with size cap.
func fetchOne(ctx context.Context, client *http.Client, u string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GET %s: status %d: %s", u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxBytes {
		return nil, fmt.Errorf("GET %s: response exceeds %d bytes", u, maxBytes)
	}
	return b, nil
}

// parseIndex tolerates {skills, data, items} wrappers and bare arrays.
func parseIndex(body []byte) (*FeedIndex, error) {
	var idx FeedIndex
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	// First try strict FeedIndex shape via generic map to detect wrappers.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		// Maybe bare array.
		var arr []SkillEntry
		if err2 := json.Unmarshal(body, &arr); err2 != nil {
			return nil, fmt.Errorf("invalid feed index JSON: %w", err)
		}
		return &FeedIndex{Version: 1, Skills: arr}, nil
	}
	listKeys := []string{"skills", "data", "items"}
	for _, k := range listKeys {
		if v, ok := raw[k]; ok {
			var skills []json.RawMessage
			if err := json.Unmarshal(v, &skills); err != nil {
				continue
			}
			entries := make([]SkillEntry, 0, len(skills))
			for _, sm := range skills {
				e, err := parseEntry(sm)
				if err != nil {
					continue
				}
				entries = append(entries, e)
			}
			_ = json.Unmarshal(raw["version"], &idx.Version)
			if idx.Version == 0 {
				idx.Version = 1
			}
			var meta struct {
				Name    string `json:"name"`
				BaseURL string `json:"baseUrl"`
				Updated string `json:"updatedAt"`
			}
			_ = json.Unmarshal(body, &meta)
			idx.Name, idx.BaseURL, idx.Updated = meta.Name, meta.BaseURL, meta.Updated
			idx.Skills = entries
			return &idx, nil
		}
	}
	// No wrapper key: try direct FeedIndex with skills field via lenient decode.
	var direct struct {
		Version int          `json:"version"`
		Name    string       `json:"name"`
		BaseURL string       `json:"baseUrl"`
		Updated string       `json:"updatedAt"`
		Skills  []SkillEntry `json:"skills"`
	}
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, fmt.Errorf("invalid feed index JSON: %w", err)
	}
	if direct.Skills == nil {
		return nil, fmt.Errorf("invalid feed index JSON: missing \"skills\" array")
	}
	if direct.Version == 0 {
		direct.Version = 1
	}
	return &FeedIndex{Version: direct.Version, Name: direct.Name, BaseURL: direct.BaseURL, Updated: direct.Updated, Skills: direct.Skills}, nil
}

// parseEntry tolerates alias keys: slug/id, desc, version/ref/tag, path/dir, url/skillMdUrl/installUrl/href.
func parseEntry(raw json.RawMessage) (SkillEntry, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return SkillEntry{}, err
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				var s string
				if err := json.Unmarshal(v, &s); err == nil {
					if strings.TrimSpace(s) != "" {
						return strings.TrimSpace(s)
					}
				}
			}
		}
		return ""
	}
	e := SkillEntry{
		Name:        str("name", "slug", "id"),
		Description: str("description", "desc"),
		Version:     str("version", "ref", "tag"),
		Path:        str("path", "dir", "skillPath"),
		URL:         str("url", "skillMdUrl", "installUrl", "href", "skillUrl"),
	}
	if e.Name == "" {
		return SkillEntry{}, fmt.Errorf("skill entry missing name/slug")
	}
	// files[] inline (skills.sh v1 style): [{path, contents|content}]
	if v, ok := m["files"]; ok {
		var files []map[string]json.RawMessage
		if err := json.Unmarshal(v, &files); err == nil {
			for _, f := range files {
				var p, c string
				_ = json.Unmarshal(f["path"], &p)
				if err := json.Unmarshal(f["contents"], &c); err != nil {
					_ = json.Unmarshal(f["content"], &c)
				}
				if strings.TrimSpace(p) == "" {
					continue
				}
				e.Files = append(e.Files, SkillFile{Path: p, Contents: c})
			}
		}
	}
	return e, nil
}

// FetchIndex tries candidates in order and returns parsed index + winning URL.
func FetchIndex(ctx context.Context, input string) (*FeedIndex, string, error) {
	cands := FeedURLCandidates(input)
	if len(cands) == 0 {
		return nil, "", fmt.Errorf("invalid well-known feed %q: expected https://host/.well-known/agent-skills or bare domain", input)
	}
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	client := &http.Client{Timeout: httpTimeout}
	var lastErr error
	for _, u := range cands {
		body, err := fetchOne(ctx, client, u, maxIndexBytes)
		if err != nil {
			lastErr = err
			continue
		}
		idx, err := parseIndex(body)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", u, err)
			continue
		}
		return idx, u, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidates")
	}
	return nil, "", fmt.Errorf("fetch well-known index: %w (tried %s)", lastErr, strings.Join(cands, ", "))
}

// FetchSkillMD downloads SKILL.md bytes for an entry, honoring inline files[].
func FetchSkillMD(ctx context.Context, indexURL string, e SkillEntry) ([]byte, error) {
	for _, f := range e.Files {
		if strings.EqualFold(strings.TrimSpace(f.Path), "SKILL.md") || strings.HasSuffix(strings.ToLower(f.Path), "/skill.md") {
			if f.Contents != "" {
				return []byte(f.Contents), nil
			}
		}
		// bare filename without dir also counts
		if strings.EqualFold(strings.TrimSpace(f.Path), "SKILL.md") {
			return []byte(f.Contents), nil
		}
	}
	u := SkillFileURL(indexURL, e)
	if u == "" {
		return nil, fmt.Errorf("skill %q has no url/path and no inline files[]", e.Name)
	}
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/markdown, text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("GET %s: status %d: %s", u, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > maxSkillBytes {
		return nil, fmt.Errorf("skill %q exceeds %d bytes", e.Name, maxSkillBytes)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, fmt.Errorf("skill %q is empty at %s", e.Name, u)
	}
	return b, nil
}

// SkillFileURL resolves the SKILL.md URL for an entry relative to indexURL.
func SkillFileURL(indexURL string, e SkillEntry) string {
	if e.URL != "" {
		if strings.Contains(e.URL, "://") {
			return e.URL
		}
		base, err := url.Parse(indexURL)
		if err == nil {
			ref, err := url.Parse(e.URL)
			if err == nil {
				return base.ResolveReference(ref).String()
			}
		}
		return e.URL
	}
	base, err := url.Parse(indexURL)
	if err != nil || base.Host == "" {
		return ""
	}
	origin := base.Scheme + "://" + base.Host
	slug := e.Slug()
	if slug == "" {
		slug = names.SanitizeName(e.Name)
	}
	if e.Path != "" {
		p := strings.Trim(strings.ReplaceAll(e.Path, "\\", "/"), "/")
		if strings.HasSuffix(strings.ToLower(p), "skill.md") {
			if strings.Contains(p, "://") {
				return p
			}
			if strings.HasPrefix(p, "/") {
				return origin + p
			}
			// relative to feed root (origin), not index dir
			return origin + "/" + p
		}
		return origin + "/" + strings.Trim(p, "/") + "/SKILL.md"
	}
	return origin + "/skills/" + slug + "/SKILL.md"
}

// Select filters entries by name filters (exact slug or name, case-insensitive)
// and version ref. Empty filters select all. Ref "" or "HEAD" disables version check.
func Select(idx *FeedIndex, filters []string, ref string) ([]SkillEntry, error) {
	if idx == nil || len(idx.Skills) == 0 {
		return nil, fmt.Errorf("feed contains no skills")
	}
	needVersion := strings.TrimSpace(ref) != "" && ref != "HEAD"
	var wanted []string
	for _, f := range filters {
		f = strings.TrimSpace(f)
		if f == "" || f == "." {
			continue
		}
		wanted = append(wanted, f)
	}
	matchName := func(e SkillEntry, f string) bool {
		f = strings.TrimSpace(f)
		if f == "*" {
			return true
		}
		if strings.EqualFold(e.Name, f) || strings.EqualFold(e.Slug(), names.SanitizeName(f)) {
			return true
		}
		// allow path suffix match (feed path or skills/<slug>)
		fp := strings.ReplaceAll(strings.Trim(f, "/"), "\\", "/")
		if e.Path != "" && (strings.EqualFold(e.Path, fp) || strings.HasSuffix(strings.ToLower(e.Path), "/"+strings.ToLower(fp))) {
			return true
		}
		return false
	}
	var out []SkillEntry
	if len(wanted) == 0 {
		out = append(out, idx.Skills...)
	} else {
		hasStar := false
		for _, w := range wanted {
			if w == "*" {
				hasStar = true
			}
		}
		if hasStar {
			out = append(out, idx.Skills...)
		} else {
			for _, w := range wanted {
				found := false
				for _, e := range idx.Skills {
					if matchName(e, w) {
						out = append(out, e)
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("skill %q not found in feed (available: %s)", w, availableNames(idx))
				}
			}
		}
	}
	if needVersion {
		var kept []SkillEntry
		for _, e := range out {
			if e.Version == "" || e.Version == ref {
				kept = append(kept, e)
				continue
			}
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("no selected skills at version %q (available: %s)", ref, availableVersions(out))
		}
		out = kept
	}
	return out, nil
}

// ListNames returns display names for errors and --list.
func ListNames(idx *FeedIndex) []string {
	var out []string
	for _, e := range idx.Skills {
		d := e.Slug()
		if d == "" {
			d = e.Name
		}
		out = append(out, d)
	}
	return out
}

func availableNames(idx *FeedIndex) string {
	return strings.Join(ListNames(idx), ", ")
}

func availableVersions(entries []SkillEntry) string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		v := e.Version
		if v == "" {
			v = "(unversioned)"
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, e.Slug()+"@"+v)
		}
	}
	if len(out) == 0 {
		return "(none)"
	}
	return strings.Join(out, ", ")
}
