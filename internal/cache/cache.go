package cache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
	"skill.sh/mskill/internal/git"
	"skill.sh/mskill/internal/resolve"
)

// Meta describes cached repo metadata persisted as .mskill-meta.json
type Meta struct {
	Host        string    `json:"host"`
	Owner       string    `json:"owner"`
	Repo        string    `json:"repo"`
	Ref         string    `json:"ref"`
	CloneURL    string    `json:"cloneUrl"`
	CommitSHA   string    `json:"commitSha"`
	SparsePaths []string  `json:"sparsePaths"`
	Shallow     bool      `json:"shallow"`
	Depth       int       `json:"depth"`
	Filter      string    `json:"filter"`
	LastFetch   time.Time `json:"lastFetch"`
	LastAccess  time.Time `json:"lastAccess"`
	CreatedAt   time.Time `json:"createdAt"`
}

// CacheKey sanitizes ref and hashes host/owner/repo/ref.
func CacheKey(host, owner, repo, ref string) string {
	if ref == "" {
		ref = "HEAD"
	}
	sanitized := strings.ReplaceAll(ref, "/", "-")
	h := sha256.Sum256([]byte(host + "/" + owner + "/" + repo + "/" + ref))
	hash8 := hex.EncodeToString(h[:])[:8]
	return sanitized + "--" + hash8
}

// MetaPath returns path to .mskill-meta.json for given coordinates.
func MetaPath(cacheDir, host, owner, repo, ref string) string {
	key := CacheKey(host, owner, repo, ref)
	return filepath.Join(cacheDir, "repos", host, owner, repo, key, ".mskill-meta.json")
}

// CacheDirPath returns directory containing the meta file.
func CacheDirPath(cacheDir, host, owner, repo, ref string) string {
	return filepath.Dir(MetaPath(cacheDir, host, owner, repo, ref))
}

// LoadMeta reads meta JSON from path.
func LoadMeta(path string) (*Meta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveMeta writes meta JSON to path (0644, ensure dir 0755).
func SaveMeta(path string, m *Meta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		return err
	}
	return nil
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// fallback to time-based
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i * 3))
		}
	}
	return hex.EncodeToString(b)[:n]
}

func isSHA(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for _, c := range ref {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func parseSize(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 2 * 1024 * 1024 * 1024
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "gb"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "gb")
	case strings.HasSuffix(s, "g"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "mb"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "m"):
		mult = 1024 * 1024
		s = strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "kb"):
		mult = 1024
		s = strings.TrimSuffix(s, "kb")
	case strings.HasSuffix(s, "k"):
		mult = 1024
		s = strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "b"):
		s = strings.TrimSuffix(s, "b")
	}
	s = strings.TrimSpace(s)
	var v int64
	_, _ = fmt.Sscan(s, &v)
	if v == 0 {
		return 2 * 1024 * 1024 * 1024
	}
	return v * mult
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		// file
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// Ensure resolves effective ref and ensures sparse cache is populated.
// Returns cachePath and meta.
func Ensure(ctx context.Context, paths config.Paths, resolved *resolve.Resolved, ref string, skillPaths []string, force bool) (string, *Meta, error) {
	if resolved == nil {
		return "", nil, fmt.Errorf("resolved is nil")
	}
	// Website skill feed: no git clone, fetch JSON index + SKILL.md over HTTP.
	if resolved.Type == "well-known" {
		return EnsureWellKnown(ctx, paths, resolved, ref, skillPaths, force)
	}
	effectiveRef := ref
	if effectiveRef == "" {
		effectiveRef = resolved.Ref
		if effectiveRef == "" {
			effectiveRef = "HEAD"
		}
	}
	host := resolved.Host
	owner := resolved.Owner
	repo := resolved.Repo
	if host == "" {
		host = "github.com"
	}
	cachePath := CacheDirPath(paths.CacheDir, host, owner, repo, effectiveRef)
	metaPath := MetaPath(paths.CacheDir, host, owner, repo, effectiveRef)

	// Try to load existing meta
	existingMeta, err := LoadMeta(metaPath)
	exists := err == nil && existingMeta != nil

	// Check for corrupt .git early if cachePath exists but meta missing? We'll handle inside lock.
	// Default is auto-fetch (cheap `git fetch --depth 1`); only use TTL-gated cache when --offline/--cache is set.
	offline := viper.GetBool("offline") || viper.GetBool("cache.offline") || viper.GetBool("cache") || viper.GetBool("use-cache") || viper.GetBool("cache.use_cache")
	if exists && !force && offline {
		ttl := viper.GetDuration("cache.ttl")
		if ttl == 0 {
			ttl = 24 * time.Hour
		}
		fresh := time.Since(existingMeta.LastFetch) < ttl
		if fresh {
			// Check cachePath/.git exists
			if _, statErr := os.Stat(filepath.Join(cachePath, ".git")); statErr == nil {
				// Check sparse expansion needed
				missing := missingPaths(skillPaths, existingMeta.SparsePaths)
				if len(missing) == 0 {
					existingMeta.LastAccess = time.Now()
					_ = SaveMeta(metaPath, existingMeta)
					return cachePath, existingMeta, nil
				}
				// Need sparse expansion
				// Attempt to expand without full fetch
				// Check corrupt via git status
				if out, gerr := git.Run(ctx, cachePath, "status", "--porcelain"); gerr != nil {
					if strings.Contains(string(out), "not a git repository") || strings.Contains(gerr.Error(), "not a git repository") {
						// corrupt -> remove and fall through to re-clone
						_ = os.RemoveAll(cachePath)
					} else {
						// other error, try to expand anyway or fall through
					}
				} else {
					_ = out
					allPaths := dedup(append(append([]string{}, existingMeta.SparsePaths...), missing...))
					if len(allPaths) > 0 {
						expErr := withLock(cachePath, func() error {
							if err := applySparseCheckout(ctx, cachePath, allPaths); err != nil {
								if isSparseUnsupported(err.Error()) {
									fmt.Fprintf(os.Stderr, "warning: sparse-checkout unsupported, using full checkout\n")
									return nil
								}
								return err
							}
							return nil
						})
						if expErr == nil {
							existingMeta.SparsePaths = ExpandSparsePaths(ctx, cachePath, allPaths)
							if len(existingMeta.SparsePaths) == 0 {
								existingMeta.SparsePaths = dedup(allPaths)
							}
							existingMeta.LastAccess = time.Now()
							_ = SaveMeta(metaPath, existingMeta)
							return cachePath, existingMeta, nil
						}
						// if expand failed, fall through to fetch path
					} else {
						existingMeta.LastAccess = time.Now()
						_ = SaveMeta(metaPath, existingMeta)
						return cachePath, existingMeta, nil
					}
				}
			}
		}
	}

	// Need lock + clone/fetch
	var resultMeta *Meta
	var resultPath string
	lockErr := withLock(cachePath, func() error {
		// Inside lock, re-check if another process already updated
		// Load meta again
		m, loadErr := LoadMeta(metaPath)
		hasMeta := loadErr == nil
		// Check corrupt .git
		gitDir := filepath.Join(cachePath, ".git")
		hasGit := false
		if _, err := os.Stat(gitDir); err == nil {
			hasGit = true
			// Check corrupt via git status
			if out, err := git.Run(ctx, cachePath, "status", "--porcelain"); err != nil {
				if strings.Contains(string(out), "not a git repository") || strings.Contains(err.Error(), "not a git repository") {
					_ = os.RemoveAll(cachePath)
					hasGit = false
					hasMeta = false
				} else if strings.Contains(string(out), "fatal") {
					// treat as corrupt
					_ = os.RemoveAll(cachePath)
					hasGit = false
					hasMeta = false
				}
			}
		}
		// If hasGit, do fetch flow
		if hasGit {
			// Tag pin skip: if commit SHA exists and not force, we could skip fetch if ref is tag?
			// Simple: if hasMeta and m.CommitSHA != "" && !force && m.Ref == effectiveRef && effectiveRef != "HEAD" && effectiveRef != "main" && effectiveRef != "master" {
			// But we don't know if tag; we'll just fetch anyway unless caller wants skip.
			// Perform fetch
			var fetchArgs []string
			if effectiveRef == "HEAD" {
				fetchArgs = []string{"fetch", "--depth", "1", "origin"}
			} else {
				fetchArgs = []string{"fetch", "--depth", "1", "origin", effectiveRef}
			}
			out, err := git.Run(ctx, cachePath, fetchArgs...)
			if err != nil {
				// Network/404 fallback: serve stale if exists
				if hasMeta && m.CommitSHA != "" {
					fmt.Fprintf(os.Stderr, "warning: fetch failed, serving stale cache: %s\n", string(out))
					// Expand sparse if needed
					if len(skillPaths) > 0 {
						missing := missingPaths(skillPaths, m.SparsePaths)
						if len(missing) > 0 {
							allPaths := dedup(append(append([]string{}, m.SparsePaths...), missing...))
							if len(allPaths) > 0 {
								if err := applySparseCheckout(ctx, cachePath, allPaths); err != nil {
									if isSparseUnsupported(err.Error()) {
										fmt.Fprintf(os.Stderr, "warning: sparse-checkout unsupported, using full checkout\n")
									}
								} else {
									m.SparsePaths = ExpandSparsePaths(ctx, cachePath, allPaths)
									if len(m.SparsePaths) == 0 {
										m.SparsePaths = allPaths
									}
								}
							}
						}
					}
					m.LastAccess = time.Now()
					_ = SaveMeta(metaPath, m)
					resultMeta = m
					resultPath = cachePath
					return nil
				}
				return fmt.Errorf("fetch failed: %w: %s", err, string(out))
			}
			// reset
			if out2, err2 := git.Run(ctx, cachePath, "reset", "--hard", "FETCH_HEAD"); err2 != nil {
				return fmt.Errorf("reset failed: %w: %s", err2, string(out2))
			}
			// Handle sparse expansion if needed
			if len(skillPaths) > 0 {
				// Determine merged paths
				var basePaths []string
				if hasMeta {
					basePaths = m.SparsePaths
				}
				missing := missingPaths(skillPaths, basePaths)
				if len(missing) > 0 {
					allPaths := dedup(append(append([]string{}, basePaths...), missing...))
					if len(allPaths) > 0 {
						if err := applySparseCheckout(ctx, cachePath, allPaths); err != nil {
							if isSparseUnsupported(err.Error()) {
								fmt.Fprintf(os.Stderr, "warning: sparse-checkout unsupported, using full checkout\n")
							} else {
								return err
							}
						} else {
							if hasMeta {
								expanded := ExpandSparsePaths(ctx, cachePath, allPaths)
								if len(expanded) > 0 {
									m.SparsePaths = expanded
								} else {
									m.SparsePaths = allPaths
								}
							}
						}
					}
				} else if hasMeta {
					// Even when nothing is missing, normalize short names to full paths.
					if expanded := ExpandSparsePaths(ctx, cachePath, m.SparsePaths); len(expanded) > 0 {
						m.SparsePaths = expanded
					}
				}
			}
			// rev-parse HEAD
			out3, err3 := git.Run(ctx, cachePath, "rev-parse", "HEAD")
			commitSHA := ""
			if err3 == nil {
				commitSHA = strings.TrimSpace(string(out3))
			}
			// Update meta
			if !hasMeta {
				m = &Meta{}
				m.CreatedAt = time.Now()
			}
			m.Host = host
			m.Owner = owner
			m.Repo = repo
			m.Ref = effectiveRef
			m.CloneURL = resolved.CloneURL
			if commitSHA != "" {
				m.CommitSHA = commitSHA
			}
			if len(skillPaths) > 0 {
				expanded := ExpandSparsePaths(ctx, cachePath, skillPaths)
				if len(expanded) == 0 {
					expanded = dedup(skillPaths)
				}
				m.SparsePaths = dedup(append(m.SparsePaths, expanded...))
				// remove empty
				tmp := []string{}
				for _, p := range m.SparsePaths {
					if p != "" {
						tmp = append(tmp, p)
					}
				}
				m.SparsePaths = tmp
			}
			m.Shallow = true
			m.Depth = 1
			if m.Filter == "" {
				m.Filter = "blob:none"
			}
			now := time.Now()
			m.LastFetch = now
			m.LastAccess = now
			if m.CreatedAt.IsZero() {
				m.CreatedAt = now
			}
			if err := SaveMeta(metaPath, m); err != nil {
				return err
			}
			resultMeta = m
			resultPath = cachePath
			return nil
		}

		// Clone path: no .git, need fresh clone
		// Ensure parent dirs
		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
			return err
		}
		// If cachePath exists as empty dir (created for lock), remove it before rename
		if _, err := os.Stat(cachePath); err == nil {
			// Remove empty directory to allow rename; if it has content, remove
			_ = os.RemoveAll(cachePath)
		}
		tmpBase := filepath.Join(paths.CacheDir, "tmp")
		if err := os.MkdirAll(tmpBase, 0755); err != nil {
			return err
		}
		tmpDir := filepath.Join(tmpBase, "clone-"+randomString(8))
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return err
		}
		// Ensure cleanup on failure
		success := false
		defer func() {
			if !success {
				_ = os.RemoveAll(tmpDir)
			}
		}()

		cloneURL := resolved.CloneURL
		if cloneURL == "" {
			cloneURL = fmt.Sprintf("https://%s/%s/%s.git", host, owner, repo)
		}
		// Build clone args
		cloneArgs := []string{"clone", "--filter=blob:none", "--sparse", "--depth", "1", "--single-branch"}
		useBranch := effectiveRef != "" && effectiveRef != "HEAD" && !isSHA(effectiveRef)
		if useBranch {
			cloneArgs = append(cloneArgs, "--branch", effectiveRef)
		}
		cloneArgs = append(cloneArgs, cloneURL, tmpDir)

		// Remove empty tmpDir before clone (git clone expects non-existing)
		_ = os.RemoveAll(tmpDir)

		out, err := git.Run(ctx, "", cloneArgs...)
		filterUsed := "blob:none"
		if err != nil {
			s := string(out)
			if strings.Contains(s, "unknown option --filter") || strings.Contains(s, "unknown option") && strings.Contains(s, "--filter") {
				// Retry without --filter
				filterUsed = ""
				cloneArgs2 := []string{"clone", "--sparse", "--depth", "1", "--single-branch"}
				if useBranch {
					cloneArgs2 = append(cloneArgs2, "--branch", effectiveRef)
				}
				cloneArgs2 = append(cloneArgs2, cloneURL, tmpDir)
				_ = os.RemoveAll(tmpDir)
				out2, err2 := git.Run(ctx, "", cloneArgs2...)
				if err2 != nil {
					s2 := string(out2)
					if strings.Contains(s2, "sparse") && strings.Contains(strings.ToLower(s2), "unknown") {
						fmt.Fprintf(os.Stderr, "warning: sparse-checkout unsupported, using full checkout\n")
						// fallback to full checkout
						cloneArgs3 := []string{"clone", "--depth", "1", "--single-branch"}
						if useBranch {
							cloneArgs3 = append(cloneArgs3, "--branch", effectiveRef)
						}
						cloneArgs3 = append(cloneArgs3, cloneURL, tmpDir)
						_ = os.RemoveAll(tmpDir)
						if out3, err3 := git.Run(ctx, "", cloneArgs3...); err3 != nil {
							return fmt.Errorf("clone failed: %w: %s", err3, string(out3))
						}
						filterUsed = ""
					} else {
						return fmt.Errorf("clone failed: %w: %s", err2, s2)
					}
				}
			} else if strings.Contains(s, "sparse") && strings.Contains(strings.ToLower(s), "unknown") {
				fmt.Fprintf(os.Stderr, "warning: sparse-checkout unsupported, using full checkout\n")
				cloneArgs2 := []string{"clone", "--filter=blob:none", "--depth", "1", "--single-branch"}
				if useBranch {
					cloneArgs2 = append(cloneArgs2, "--branch", effectiveRef)
				}
				cloneArgs2 = append(cloneArgs2, cloneURL, tmpDir)
				_ = os.RemoveAll(tmpDir)
				out2, err2 := git.Run(ctx, "", cloneArgs2...)
				if err2 != nil {
					s2 := string(out2)
					if strings.Contains(s2, "unknown option --filter") {
						filterUsed = ""
						cloneArgs3 := []string{"clone", "--depth", "1", "--single-branch"}
						if useBranch {
							cloneArgs3 = append(cloneArgs3, "--branch", effectiveRef)
						}
						cloneArgs3 = append(cloneArgs3, cloneURL, tmpDir)
						_ = os.RemoveAll(tmpDir)
						if out3, err3 := git.Run(ctx, "", cloneArgs3...); err3 != nil {
							return fmt.Errorf("clone failed: %w: %s", err3, string(out3))
						}
					} else {
						return fmt.Errorf("clone failed: %w: %s", err2, s2)
					}
				}
			} else {
				// If useBranch failed and isSHA? try clone without branch then fetch sha
				if useBranch {
					// maybe branch not found, try generic clone without branch
				}
				return fmt.Errorf("clone failed: %w: %s", err, s)
			}
		}

		// If ref is SHA, fetch sha and checkout
		if isSHA(effectiveRef) && effectiveRef != "HEAD" {
			// fetch sha
			if out4, err4 := git.Run(ctx, tmpDir, "fetch", "--depth", "1", "origin", effectiveRef); err4 != nil {
				// try fallback: maybe sha not fetch via origin? try directly checkout?
				fmt.Fprintf(os.Stderr, "warning: fetch sha failed: %s\n", string(out4))
			} else {
				if out5, err5 := git.Run(ctx, tmpDir, "checkout", "--detach", effectiveRef); err5 != nil {
					// try FETCH_HEAD
					if out6, err6 := git.Run(ctx, tmpDir, "reset", "--hard", "FETCH_HEAD"); err6 != nil {
						fmt.Fprintf(os.Stderr, "warning: checkout sha failed: %s %s\n", string(out5), string(out6))
					}
				}
			}
		}

		// Sparse-checkout set
		if len(skillPaths) > 0 {
			if err := applySparseCheckout(ctx, tmpDir, skillPaths); err != nil {
				if isSparseUnsupported(err.Error()) {
					fmt.Fprintf(os.Stderr, "warning: sparse-checkout unsupported, using full checkout\n")
				} else {
					return err
				}
			}
		}

		// rev-parse HEAD
		out7, err7 := git.Run(ctx, tmpDir, "rev-parse", "HEAD")
		commitSHA := ""
		if err7 == nil {
			commitSHA = strings.TrimSpace(string(out7))
		}

		// Ensure parent of cachePath exists
		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
			return err
		}
		// Atomic rename; handle cross-device
		if err := os.Rename(tmpDir, cachePath); err != nil {
			// Fallback copy
			if strings.Contains(err.Error(), "cross-device") || strings.Contains(err.Error(), "invalid cross-device") {
				if err2 := copyDir(tmpDir, cachePath); err2 != nil {
					return fmt.Errorf("rename fallback copy failed: %w", err2)
				}
				_ = os.RemoveAll(tmpDir)
			} else {
				// If target exists (should have been removed), try remove then rename
				_ = os.RemoveAll(cachePath)
				if err2 := os.Rename(tmpDir, cachePath); err2 != nil {
					if err3 := copyDir(tmpDir, cachePath); err3 != nil {
						return fmt.Errorf("rename failed: %w", err3)
					}
					_ = os.RemoveAll(tmpDir)
				}
			}
		}
		success = true

		// Create meta
		now := time.Now()
		expandedMeta := ExpandSparsePaths(ctx, cachePath, skillPaths)
		if len(expandedMeta) == 0 && len(dedup(skillPaths)) > 0 {
			expandedMeta = dedup(skillPaths)
		}
		mNew := &Meta{
			Host:        host,
			Owner:       owner,
			Repo:        repo,
			Ref:         effectiveRef,
			CloneURL:    cloneURL,
			CommitSHA:   commitSHA,
			SparsePaths: expandedMeta,
			Shallow:     true,
			Depth:       1,
			Filter:      filterUsed,
			LastFetch:   now,
			LastAccess:  now,
			CreatedAt:   now,
		}
		// Remove empty strings from SparsePaths
		tmp2 := []string{}
		for _, p := range mNew.SparsePaths {
			if p != "" {
				tmp2 = append(tmp2, p)
			}
		}
		mNew.SparsePaths = tmp2
		if err := SaveMeta(metaPath, mNew); err != nil {
			return err
		}
		resultMeta = mNew
		resultPath = cachePath
		return nil
	})
	if lockErr != nil {
		return "", nil, lockErr
	}
	if resultMeta == nil {
		// Try loading again
		m, err := LoadMeta(metaPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load meta after ensure: %w", err)
		}
		return resultPath, m, nil
	}
	return resultPath, resultMeta, nil
}

func missingPaths(wanted, existing []string) []string {
	set := make(map[string]bool, len(existing))
	baseSet := make(map[string]bool, len(existing))
	for _, e := range existing {
		set[e] = true
		// Basename cover: "skills/find-skills" covers "find-skills".
		e = strings.ReplaceAll(e, "\\", "/")
		e = strings.Trim(e, "/")
		if e != "" {
			baseSet[strings.ToLower(path.Base(e))] = true
			set[strings.ToLower(e)] = true
		}
	}
	var missing []string
	for _, w := range wanted {
		if w == "" {
			continue
		}
		wn := strings.ReplaceAll(w, "\\", "/")
		wn = strings.Trim(wn, "/")
		if set[w] || set[wn] || set[strings.ToLower(wn)] {
			continue
		}
		if !strings.Contains(wn, "/") && baseSet[strings.ToLower(path.Base(wn))] {
			continue
		}
		missing = append(missing, w)
	}
	return missing
}

func dedup(in []string) []string {
	seen := make(map[string]bool)
	out := []string{}
	for _, v := range in {
		if v == "" {
			continue
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func isSparseUnsupported(out string) bool {
	return strings.Contains(strings.ToLower(out), "unknown")
}

func applySparseCheckout(ctx context.Context, dir string, paths []string) error {
	filtered := dedup(paths)
	if len(filtered) == 0 {
		return nil
	}
	// Expand bare skill names (e.g. "find-skills") to full git paths
	// (e.g. "skills/find-skills") so cone-mode checkout materializes them.
	filtered = ExpandSparsePaths(ctx, dir, filtered)
	if len(filtered) == 0 {
		return nil
	}
	out, err := git.Run(ctx, dir, append([]string{"sparse-checkout", "set", "--cone"}, filtered...)...)
	if err != nil {
		return fmt.Errorf("sparse-checkout set failed: %w: %s", err, string(out))
	}
	return nil
}

// FindSkillGitPath locates the full git-relative dir for a skill basename
// using `git ls-tree` (works even when sparse checkout hasn't materialized
// the working tree). Match is case-insensitive on the leaf dir name.
// Returns "" when not found. Path uses forward slashes.
func FindSkillGitPath(ctx context.Context, dir, slug string) string {
	slug = strings.Trim(strings.ReplaceAll(slug, "\\", "/"), "/")
	if slug == "" {
		return ""
	}
	base := slug
	if strings.Contains(slug, "/") {
		base = path.Base(slug)
	}
	out, err := git.Run(ctx, dir, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return ""
	}
	// Exact leaf match first.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.EqualFold(path.Base(line), "SKILL.md") {
			continue
		}
		d := path.Dir(line)
		if strings.EqualFold(path.Base(d), base) {
			if d == "." {
				return ""
			}
			return path.Clean(d)
		}
	}
	// Substring fallback (slug appears in skill path).
	low := strings.ToLower(base)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.EqualFold(path.Base(line), "SKILL.md") {
			continue
		}
		if strings.Contains(strings.ToLower(line), low) {
			d := path.Dir(line)
			if d == "." {
				return ""
			}
			return path.Clean(d)
		}
	}
	return ""
}

// ExpandSparsePaths maps bare skill names to full git-relative paths.
// Entries already containing "/" are kept as-is (cleaned); bare names are
// resolved via FindSkillGitPath when possible.
func ExpandSparsePaths(ctx context.Context, dir string, wanted []string) []string {
	var out []string
	for _, w := range wanted {
		w = strings.ReplaceAll(w, "\\", "/")
		w = strings.Trim(w, "/")
		if w == "" || w == "." {
			continue
		}
		w = path.Clean(w)
		if !strings.Contains(w, "/") {
			if full := FindSkillGitPath(ctx, dir, w); full != "" {
				out = append(out, full)
				continue
			}
		}
		out = append(out, w)
	}
	return dedup(out)
}

// Materialize ensures gitPaths exist in the working tree, preserving existing
// sparse rules. It prefers `sparse-checkout add` (union semantics) and falls
// back to list+set union, then full checkout.
func Materialize(ctx context.Context, dir string, gitPaths ...string) error {
	var wanted []string
	for _, p := range gitPaths {
		p = strings.ReplaceAll(p, "\\", "/")
		p = strings.Trim(p, "/")
		if p == "" || p == "." {
			continue
		}
		wanted = append(wanted, path.Clean(p))
	}
	wanted = ExpandSparsePaths(ctx, dir, wanted)
	if len(wanted) == 0 {
		return nil
	}
	// Fast path: already materialized (SKILL.md present, case-insensitive).
	allPresent := true
	for _, rel := range wanted {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if _, err := os.Stat(filepath.Join(full, "SKILL.md")); err != nil {
			if _, err2 := os.Stat(filepath.Join(full, "skill.md")); err2 != nil {
				allPresent = false
				break
			}
		}
	}
	if allPresent {
		return nil
	}
	// Prefer `add` which unions with existing cone rules.
	addOK := true
	for _, rel := range wanted {
		if out, err := git.Run(ctx, dir, "sparse-checkout", "add", rel); err != nil {
			if isSparseUnsupported(string(out)) {
				return nil // old git: full checkout already present
			}
			addOK = false
			break
		}
	}
	if addOK {
		for _, rel := range wanted {
			full := filepath.Join(dir, filepath.FromSlash(rel))
			if _, err := os.Stat(filepath.Join(full, "SKILL.md")); err == nil {
				continue
			}
			if _, err := os.Stat(filepath.Join(full, "skill.md")); err == nil {
				continue
			}
			addOK = false
			break
		}
		if addOK {
			return nil
		}
	}
	// Fallback: union current list + wanted via `set --cone`.
	cur := []string{}
	if out, err := git.Run(ctx, dir, "sparse-checkout", "list"); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if t := strings.TrimSpace(l); t != "" {
				cur = append(cur, t)
			}
		}
	}
	union := dedup(append(append([]string{}, cur...), wanted...))
	if len(union) > 0 {
		if out, err := git.Run(ctx, dir, append([]string{"sparse-checkout", "set", "--cone"}, union...)...); err == nil {
			return nil
		} else if isSparseUnsupported(string(out)) {
			return nil
		}
	}
	// Last resort: full checkout.
	_, _ = git.Run(ctx, dir, "sparse-checkout", "disable")
	_, _ = git.Run(ctx, dir, "read-tree", "-mu", "HEAD")
	return nil
}

// GC scans repos and removes expired or LRU entries.
func GC(paths config.Paths, dryRun bool) ([]string, error) {
	reposDir := filepath.Join(paths.CacheDir, "repos")
	tmpDir := filepath.Join(paths.CacheDir, "tmp")
	var deleted []string

	// Prune tmp/* >1h
	if entries, err := os.ReadDir(tmpDir); err == nil {
		for _, e := range entries {
			p := filepath.Join(tmpDir, e.Name())
			info, err := e.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > time.Hour {
				if dryRun {
					deleted = append(deleted, p)
				} else {
					_ = os.RemoveAll(p)
					deleted = append(deleted, p)
				}
			}
		}
	}

	// Scan repos/*/*/*/*
	maxAge := viper.GetDuration("cache.gc.max_age")
	if maxAge == 0 {
		maxAge = 30 * 24 * time.Hour
	}
	maxSizeStr := viper.GetString("cache.gc.max_size")
	maxSize := parseSize(maxSizeStr)

	type entry struct {
		path       string
		meta       *Meta
		size       int64
		lastAccess time.Time
	}
	var entries []entry
	var totalSize int64

	// Walk reposDir: host/owner/repo/key
	if _, err := os.Stat(reposDir); os.IsNotExist(err) {
		return deleted, nil
	}
	_ = filepath.Walk(reposDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == ".mskill-meta.json" {
			m, err := LoadMeta(p)
			if err != nil {
				// treat as candidate for deletion if unreadable and old?
				return nil
			}
			dir := filepath.Dir(p)
			sz, _ := dirSize(dir)
			totalSize += sz
			entries = append(entries, entry{path: dir, meta: m, size: sz, lastAccess: m.LastAccess})
		}
		return nil
	})

	// Check max_age expiry
	for _, e := range entries {
		if time.Since(e.lastAccess) > maxAge {
			// try lock
			if release, err := tryLock(e.path); err == nil {
				if dryRun {
					deleted = append(deleted, e.path)
					release()
				} else {
					release()
					_ = os.RemoveAll(e.path)
					deleted = append(deleted, e.path)
					totalSize -= e.size
				}
			}
		}
	}
	// If total still over maxSize, LRU eviction
	if totalSize > maxSize {
		// filter remaining entries not yet deleted
		remaining := []entry{}
		deletedSet := make(map[string]bool, len(deleted))
		for _, d := range deleted {
			deletedSet[d] = true
		}
		for _, e := range entries {
			if !deletedSet[e.path] {
				// re-check if path still exists
				if _, err := os.Stat(e.path); err == nil {
					remaining = append(remaining, e)
				}
			}
		}
		sort.Slice(remaining, func(i, j int) bool {
			return remaining[i].lastAccess.Before(remaining[j].lastAccess)
		})
		for _, e := range remaining {
			if totalSize <= maxSize {
				break
			}
			if release, err := tryLock(e.path); err == nil {
				if dryRun {
					deleted = append(deleted, e.path)
					release()
				} else {
					release()
					_ = os.RemoveAll(e.path)
					deleted = append(deleted, e.path)
				}
				totalSize -= e.size
			}
		}
	}

	return deleted, nil
}

// CachedEntry describes a cached repo on disk.
type CachedEntry struct {
	Path string
	Meta *Meta
	Size int64
}

// List enumerates all cached repos under paths.CacheDir/repos.
// It reads .mskill-meta.json in each cache dir, computes dir size, and sorts by LastAccess descending.
func List(paths config.Paths) ([]CachedEntry, error) {
	reposDir := filepath.Join(paths.CacheDir, "repos")
	var out []CachedEntry
	if _, err := os.Stat(reposDir); os.IsNotExist(err) {
		return out, nil
	}
	err := filepath.Walk(reposDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == ".mskill-meta.json" {
			m, err := LoadMeta(p)
			if err != nil {
				return nil
			}
			dir := filepath.Dir(p)
			sz, _ := dirSize(dir)
			out = append(out, CachedEntry{Path: dir, Meta: m, Size: sz})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Meta.LastAccess.After(out[j].Meta.LastAccess)
	})
	return out, nil
}

// ListSkills discovers skill directories inside a cached repo.
// It uses `git ls-tree -r --name-only HEAD` (works with sparse/filter checkout) to find
// SKILL.md / skill.md files, plus a filesystem walk fallback for depth 5.
// Returns sorted unique relative dir paths (e.g. "skills/my-skill") that contain SKILL.md.
func ListSkills(ctx context.Context, cachePath string) ([]string, error) {
	found := map[string]bool{}
	// 1) git ls-tree enumeration (sparse-aware: reads tree object, not working dir)
	if out, err := git.Run(ctx, cachePath, "ls-tree", "-r", "--name-only", "HEAD"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			base := path.Base(line)
			if !strings.EqualFold(base, "SKILL.md") && !strings.EqualFold(base, "skill.md") {
				continue
			}
			dir := path.Dir(line)
			if dir == "." {
				dir = ""
			}
			// Normalize to filepath separators for display but keep forward slash for git path?
			// Return as path-style (forward slash) relative; caller joins with filepath.Join which normalizes.
			dir = path.Clean(dir)
			if dir == "." {
				dir = ""
			}
			if dir == "" {
				// Root SKILL.md => skill at repo root (use "." or "" to signal root)
				// Represent as "." so caller can handle; but many repos place skills in subdirs.
				// Include "" only if not already? We map "" to repo root skill.
				found[""] = true
			} else {
				found[dir] = true
			}
		}
	}
	// 2) filesystem walk fallback / supplement (covers untracked or shallow edge cases + local repos)
	_ = filepath.WalkDir(cachePath, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), ".git") {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(cachePath, p)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 5 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(d.Name(), "SKILL.md") && !strings.EqualFold(d.Name(), "skill.md") {
			return nil
		}
		dir := filepath.Dir(p)
		rel, err := filepath.Rel(cachePath, dir)
		if err != nil {
			return nil
		}
		if rel == "." {
			found[""] = true
		} else {
			// Normalize to forward slashes for consistency with git path
			rel = filepath.ToSlash(rel)
			found[rel] = true
		}
		return nil
	})
	var out []string
	for k := range found {
		// normalize empty "" to "" (root). Caller treats "" as repo root.
		out = append(out, k)
	}
	sort.Strings(out)
	// Filter empty "" handling: if repo has single root skill and also subdir skills, keep both.
	// Remove duplicate due to path variants (e.g. "./")
	return out, nil
}

// Clean removes all cached repos.
func Clean(paths config.Paths) error {
	reposDir := filepath.Join(paths.CacheDir, "repos")
	if _, err := os.Stat(reposDir); os.IsNotExist(err) {
		return nil
	}
	// Remove glob repos/*
	entries, err := os.ReadDir(reposDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(reposDir, e.Name())
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}
