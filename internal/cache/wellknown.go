package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
	"skill.sh/mskill/internal/resolve"
	"skill.sh/mskill/internal/wellknown"
)

// EnsureWellKnown fetches a website skill feed (no git clone) and materializes
// selected skills under the persistent cache. Versioning: effectiveRef matches
// feed entry version ("" / "HEAD" = any version).
func EnsureWellKnown(ctx context.Context, paths config.Paths, resolved *resolve.Resolved, ref string, skillFilters []string, force bool) (string, *Meta, error) {
	if resolved == nil {
		return "", nil, fmt.Errorf("resolved is nil")
	}
	effectiveRef := strings.TrimSpace(ref)
	if effectiveRef == "" {
		effectiveRef = strings.TrimSpace(resolved.Ref)
		if effectiveRef == "" {
			effectiveRef = "HEAD"
		}
	}
	feedInput := strings.TrimSpace(resolved.CloneURL)
	if feedInput == "" {
		feedInput = strings.TrimSpace(resolved.Source)
	}
	host := strings.TrimSpace(resolved.Host)
	if host == "" {
		host = "well-known"
	}
	feedKey := wellknown.FeedKey(feedInput)
	cachePath := CacheDirPath(paths.CacheDir, host, "_wellknown", feedKey, effectiveRef)
	metaPath := MetaPath(paths.CacheDir, host, "_wellknown", feedKey, effectiveRef)

	offline := viper.GetBool("offline") || viper.GetBool("cache.offline") || viper.GetBool("cache") || viper.GetBool("use-cache") || viper.GetBool("cache.use_cache")
	if existing, err := LoadMeta(metaPath); err == nil && existing != nil && !force && offline {
		ttl := viper.GetDuration("cache.ttl")
		if ttl == 0 {
			ttl = 24 * time.Hour
		}
		if time.Since(existing.LastFetch) < ttl {
			if _, statErr := os.Stat(filepath.Join(cachePath, ".mskill-meta.json")); statErr == nil {
				existing.LastAccess = time.Now()
				_ = SaveMeta(metaPath, existing)
				return cachePath, existing, nil
			}
		}
	}

	// Fetch feed index over HTTP.
	idx, indexURL, err := wellknown.FetchIndex(ctx, feedInput)
	if err != nil {
		// Serve stale on network failure when possible.
		if existing, lerr := LoadMeta(metaPath); lerr == nil && existing != nil && existing.CommitSHA != "" {
			fmt.Fprintf(os.Stderr, "warning: feed fetch failed, serving stale cache: %v\n", err)
			existing.LastAccess = time.Now()
			_ = SaveMeta(metaPath, existing)
			return cachePath, existing, nil
		}
		return "", nil, err
	}

	// Skill selection: resolved subpath/slug acts as implicit filter.
	filters := append([]string{}, skillFilters...)
	if resolved.SkillPath != "" {
		filters = append(filters, resolved.SkillPath)
	} else if resolved.Slug != "" {
		filters = append(filters, resolved.Slug)
	}
	selected, err := wellknown.Select(idx, filters, effectiveRef)
	if err != nil {
		return "", nil, err
	}

	var resultMeta *Meta
	var resultPath string
	lockErr := withLock(cachePath, func() error {
		tmpBase := filepath.Join(paths.CacheDir, "tmp")
		if err := os.MkdirAll(tmpBase, 0755); err != nil {
			return err
		}
		tmpDir := filepath.Join(tmpBase, "wellknown-"+randomString(8))
		_ = os.RemoveAll(tmpDir)
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return err
		}
		success := false
		defer func() {
			if !success {
				_ = os.RemoveAll(tmpDir)
			}
		}()
		var slugs []string
		for _, e := range selected {
			slug := e.Slug()
			if slug == "" {
				return fmt.Errorf("feed entry %q has no usable name", e.Name)
			}
			slugs = append(slugs, slug)
			skillDir := filepath.Join(tmpDir, slug)
			if err := os.MkdirAll(skillDir, 0755); err != nil {
				return err
			}
			// Prefer inline files[] when present (multi-file skill without extra roundtrips).
			if len(e.Files) > 0 {
				for _, f := range e.Files {
					rel := strings.ReplaceAll(strings.Trim(strings.TrimSpace(f.Path), "/"), "\\", "/")
					if rel == "" {
						continue
					}
					// Reject traversal.
					if rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
						return fmt.Errorf("feed skill %q file %q outside skill directory", slug, f.Path)
					}
					full := filepath.Join(skillDir, filepath.FromSlash(rel))
					if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(skillDir)) {
						return fmt.Errorf("feed skill %q file %q outside skill directory", slug, f.Path)
					}
					if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
						return err
					}
					if err := os.WriteFile(full, []byte(f.Contents), 0644); err != nil {
						return err
					}
				}
				// Ensure SKILL.md exists.
				if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
					if _, err2 := os.Stat(filepath.Join(skillDir, "skill.md")); err2 != nil {
						return fmt.Errorf("feed skill %q has files[] but no SKILL.md", slug)
					}
				}
				continue
			}
			md, err := wellknown.FetchSkillMD(ctx, indexURL, e)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), md, 0644); err != nil {
				return err
			}
		}
		_ = os.RemoveAll(cachePath)
		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
			return err
		}
		if err := os.Rename(tmpDir, cachePath); err != nil {
			if err2 := copyDir(tmpDir, cachePath); err2 != nil {
				return fmt.Errorf("rename fallback copy failed: %w", err2)
			}
			_ = os.RemoveAll(tmpDir)
		}
		success = true
		now := time.Now()
		mNew := &Meta{
			Host:        host,
			Owner:       "_wellknown",
			Repo:        feedKey,
			Ref:         effectiveRef,
			CloneURL:    feedInput,
			CommitSHA:   "http-" + feedKey,
			SparsePaths: dedup(slugs),
			Shallow:     false,
			Depth:       0,
			Filter:      "http",
			LastFetch:   now,
			LastAccess:  now,
			CreatedAt:   now,
		}
		if prev, err := LoadMeta(metaPath); err == nil && prev != nil && !prev.CreatedAt.IsZero() {
			mNew.CreatedAt = prev.CreatedAt
		}
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
		m, err := LoadMeta(metaPath)
		if err != nil {
			return "", nil, fmt.Errorf("failed to load meta after well-known ensure: %w", err)
		}
		return resultPath, m, nil
	}
	return resultPath, resultMeta, nil
}
