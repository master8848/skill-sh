package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"skill.sh/mskill/internal/config"
)

// Entry records a single skill install with version pinning info.
type Entry struct {
	Repo      string    `json:"repo"` // owner/repo
	Skill     string    `json:"skill"`
	Ref       string    `json:"ref"`
	CommitSHA string    `json:"commitSha"`
	Time      time.Time `json:"time"`
}

// FilePath returns history file location using existing config paths.
func FilePath(p config.Paths) string {
	return filepath.Join(p.DotDir, "history.json")
}

// Load reads history entries (newest first). Missing file returns empty.
func Load(p config.Paths) ([]Entry, error) {
	b, err := os.ReadFile(FilePath(p))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// Append records an install. Exact repo+skill+commit duplicates update
// time/ref instead of growing the file. Caps at 200 entries.
func Append(p config.Paths, e Entry) error {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	entries, _ := Load(p)
	updated := false
	for i, ex := range entries {
		if ex.Repo == e.Repo && ex.Skill == e.Skill && ex.CommitSHA == e.CommitSHA && e.CommitSHA != "" {
			entries[i].Time = e.Time
			if e.Ref != "" {
				entries[i].Ref = e.Ref
			}
			updated = true
			break
		}
		// Also dedupe empty-SHA legacy entries by repo+skill+ref
		if e.CommitSHA == "" && ex.Repo == e.Repo && ex.Skill == e.Skill && ex.Ref == e.Ref {
			entries[i].Time = e.Time
			updated = true
			break
		}
	}
	if !updated {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Time.After(entries[j].Time) })
	if len(entries) > 200 {
		entries = entries[:200]
	}
	if err := os.MkdirAll(filepath.Dir(FilePath(p)), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(FilePath(p), b, 0o600)
}

// LatestSHA returns the newest CommitSHA for repo+skill, or "" if unknown.
func LatestSHA(entries []Entry, repo, skill string) string {
	for _, e := range entries {
		if e.Repo == repo && (skill == "" || e.Skill == skill) && e.CommitSHA != "" {
			return e.CommitSHA
		}
	}
	return ""
}
