package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Skill represents a skill from search.
type Skill struct {
	ID          string `json:"id"`
	SkillID     string `json:"skillId"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Topic       string `json:"topic"`
	Owner       string `json:"owner"`
	Installs    int    `json:"installs"`
	Official    bool   `json:"official"`
}

// SearchResponse mirrors server response.
type SearchResponse struct {
	Skills []Skill `json:"skills"`
	Count  int     `json:"count"`
}

// AuditResult raw audit map.
type AuditResult map[string]struct {
	Ath struct {
		Risk string `json:"risk"`
	} `json:"ath"`
	Socket struct {
		Risk   string `json:"risk"`
		Alerts int    `json:"alerts"`
		Score  int    `json:"score"`
	} `json:"socket"`
	Snyk struct {
		Risk string `json:"risk"`
	} `json:"snyk"`
	Zeroleaks struct {
		Risk  string `json:"risk"`
		Score int    `json:"score"`
	} `json:"zeroleaks"`
}

// Verdict computed from audit.
type Verdict struct {
	Safe    bool
	Unknown bool
	Reason  string
}

func skillsAPIBase() string {
	if v := os.Getenv("SKILLS_API_URL"); v != "" {
		return strings.TrimSuffix(strings.TrimSpace(v), "/")
	}
	return "https://skills.sh"
}

func auditBase() string {
	if v := os.Getenv("AUDIT_URL"); v != "" {
		return strings.TrimSuffix(strings.TrimSpace(v), "/")
	}
	if v := os.Getenv("SKILLS_AUDIT_URL"); v != "" {
		return strings.TrimSuffix(strings.TrimSpace(v), "/")
	}
	return "https://add-skill.vercel.sh"
}

// Search queries skills.sh/api/search
func Search(ctx context.Context, query, owner string, limit int) ([]Skill, error) {
	base := skillsAPIBase()
	u, err := url.Parse(base + "/api/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	if query != "" {
		q.Set("q", query)
	}
	if owner != "" {
		q.Set("owner", owner)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	} else {
		q.Set("limit", "20")
	}
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		} else if tok := os.Getenv("GH_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			// retry on network error if context not canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("search status %d: %s", resp.StatusCode, string(body))
			if resp.StatusCode >= 500 && attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
				continue
			}
			return nil, lastErr
		}
		var sr SearchResponse
		if err := json.Unmarshal(body, &sr); err != nil {
			// Try alternative: skills directly as array?
			var alt struct {
				Skills []Skill `json:"skills"`
				Count  int     `json:"count"`
			}
			// Try raw skills array?
			var arr []Skill
			if err2 := json.Unmarshal(body, &arr); err2 == nil {
				return arr, nil
			}
			_ = alt
			return nil, fmt.Errorf("unmarshal search: %w body: %s", err, string(body))
		}
		return sr.Skills, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("search failed after retries")
}

// Audit checks slugs for given source.
func Audit(ctx context.Context, source string, slugs []string) (map[string]Verdict, error) {
	result := make(map[string]Verdict, len(slugs))
	for _, s := range slugs {
		result[s] = Verdict{Unknown: true, Safe: false, Reason: "audit unavailable"}
	}
	if source == "" || len(slugs) == 0 {
		return result, nil
	}
	base := auditBase()
	// audit endpoint is /audit?source=owner/repo&skills=slug1,slug2
	u, err := url.Parse(base + "/audit")
	if err != nil {
		return result, nil
	}
	q := u.Query()
	q.Set("source", source)
	q.Set("skills", strings.Join(slugs, ","))
	u.RawQuery = q.Encode()

	ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctxTimeout, http.MethodGet, u.String(), nil)
	if err != nil {
		return result, nil
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// timeout or network => UNKNOWN for all
		return result, nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return result, nil
	}
	var raw AuditResult
	if err := json.Unmarshal(body, &raw); err != nil {
		return result, nil
	}
	for _, slug := range slugs {
		if entry, ok := raw[slug]; ok {
			v := evaluate(entry)
			result[slug] = v
		} else {
			result[slug] = Verdict{Unknown: true, Safe: false, Reason: "audit unavailable"}
		}
	}
	return result, nil
}

func evaluate(entry struct {
	Ath struct {
		Risk string `json:"risk"`
	} `json:"ath"`
	Socket struct {
		Risk   string `json:"risk"`
		Alerts int    `json:"alerts"`
		Score  int    `json:"score"`
	} `json:"socket"`
	Snyk struct {
		Risk string `json:"risk"`
	} `json:"snyk"`
	Zeroleaks struct {
		Risk  string `json:"risk"`
		Score int    `json:"score"`
	} `json:"zeroleaks"`
}) Verdict {
	risks := []string{entry.Ath.Risk, entry.Socket.Risk, entry.Snyk.Risk, entry.Zeroleaks.Risk}
	for _, r := range risks {
		low := strings.ToLower(strings.TrimSpace(r))
		if low == "critical" || low == "high" {
			return Verdict{Safe: false, Unknown: false, Reason: fmt.Sprintf("risk %s", low)}
		}
	}
	if entry.Socket.Alerts > 0 {
		return Verdict{Safe: false, Unknown: false, Reason: fmt.Sprintf("socket alerts %d", entry.Socket.Alerts)}
	}
	if entry.Socket.Score != 0 && entry.Socket.Score < 80 {
		return Verdict{Safe: false, Unknown: false, Reason: fmt.Sprintf("socket score %d < 80", entry.Socket.Score)}
	}
	if entry.Zeroleaks.Score != 0 && entry.Zeroleaks.Score < 80 {
		return Verdict{Safe: false, Unknown: false, Reason: fmt.Sprintf("zeroleaks score %d < 80", entry.Zeroleaks.Score)}
	}
	return Verdict{Safe: true, Unknown: false, Reason: "safe"}
}

// DownloadMeta fetches GET https://skills.sh/api/download/{owner}/{repo}/{slug}
func DownloadMeta(ctx context.Context, owner, repo, slug string) ([]byte, error) {
	base := skillsAPIBase()
	u := fmt.Sprintf("%s/api/download/%s/%s/%s", base, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(slug))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
