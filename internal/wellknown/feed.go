package wellknown

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"skill.sh/mskill/internal/names"
)

// copySkillDir copies a skill directory excluding .git.
func copySkillDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0755)
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for _, part := range parts {
			if part == ".git" {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0644)
	})
}

// discoverSkills walks srcDir (depth 5) for SKILL.md/skill.md.
func discoverSkills(srcDir string) ([]string, error) {
	found := map[string]bool{}
	err := filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.EqualFold(d.Name(), ".git") {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(srcDir, p)
			if rel != "." && strings.Count(rel, string(os.PathSeparator)) >= 5 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(d.Name(), "SKILL.md") && !strings.EqualFold(d.Name(), "skill.md") {
			return nil
		}
		dir := filepath.Dir(p)
		rel, err := filepath.Rel(srcDir, dir)
		if err != nil {
			return nil
		}
		if rel == "." {
			found["."] = true
		} else {
			found[filepath.ToSlash(rel)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// parseFrontmatter extracts name/description/version from SKILL.md frontmatter.
func parseFrontmatter(body, fallback string) (name, desc, version string) {
	name = fallback
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return name, "", ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			break
		}
		line := lines[i]
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(line[:idx]))
		v := strings.TrimSpace(line[idx+1:])
		v = strings.Trim(v, `"'`)
		switch k {
		case "name":
			if v != "" {
				name = v
			}
		case "description":
			desc = v
		case "version":
			version = v
		}
	}
	return name, desc, version
}

// BuildFeed scans srcDir and builds a feed index. baseURL is the public origin
// (e.g. https://example.com); defaultVersion fills entries lacking frontmatter version.
func BuildFeed(srcDir, baseURL, defaultVersion string) (*FeedIndex, map[string]string, error) {
	info, err := os.Stat(srcDir)
	if err != nil {
		return nil, nil, fmt.Errorf("source dir %q: %w", srcDir, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("source %q is not a directory", srcDir)
	}
	skills, err := discoverSkills(srcDir)
	if err != nil {
		return nil, nil, err
	}
	if len(skills) == 0 {
		return nil, nil, fmt.Errorf("no skills found in %s (missing SKILL.md)", srcDir)
	}
	origin := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if origin == "" {
		origin = "https://example.com"
	}
	idx := &FeedIndex{Version: 1, BaseURL: origin, Updated: time.Now().UTC().Format(time.RFC3339)}
	dirs := map[string]string{} // slug -> abs source dir
	for _, rel := range skills {
		abs := srcDir
		dispRel := rel
		if rel != "" && rel != "." {
			abs = filepath.Join(srcDir, filepath.FromSlash(rel))
		} else {
			dispRel = "."
		}
		skillMD, err := os.ReadFile(filepath.Join(abs, "SKILL.md"))
		if err != nil {
			skillMD, err = os.ReadFile(filepath.Join(abs, "skill.md"))
			if err != nil {
				return nil, nil, fmt.Errorf("read SKILL.md in %q: %w", abs, err)
			}
		}
		fallback := filepath.Base(abs)
		if rel == "." || rel == "" {
			fallback = filepath.Base(filepath.Clean(srcDir))
		}
		_ = dispRel
		name, desc, ver := parseFrontmatter(string(skillMD), fallback)
		if ver == "" {
			ver = defaultVersion
		}
		slug := names.SanitizeName(name)
		if slug == "" {
			slug = names.SanitizeName(fallback)
		}
		if slug == "" {
			return nil, nil, fmt.Errorf("cannot derive skill name for %q", abs)
		}
		if _, dup := dirs[slug]; dup {
			return nil, nil, fmt.Errorf("duplicate skill slug %q (from %q)", slug, abs)
		}
		dirs[slug] = abs
		idx.Skills = append(idx.Skills, SkillEntry{
			Name:        slug,
			Description: desc,
			Version:     ver,
			Path:        "skills/" + slug,
			URL:         origin + "/skills/" + slug + "/SKILL.md",
		})
	}
	sort.Slice(idx.Skills, func(i, j int) bool { return idx.Skills[i].Name < idx.Skills[j].Name })
	return idx, dirs, nil
}

// WriteFeed writes index.json + skill file trees under outDir:
// <out>/.well-known/agent-skills/index.json (+ legacy skills copy) and <out>/skills/<slug>/...
func WriteFeed(outDir string, idx *FeedIndex, dirs map[string]string) (indexPath string, err error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	wellKnown := filepath.Join(outDir, ".well-known", "agent-skills")
	if err := os.MkdirAll(wellKnown, 0755); err != nil {
		return "", err
	}
	legacy := filepath.Join(outDir, ".well-known", "skills")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		return "", err
	}
	data, err := MarshalIndex(idx)
	if err != nil {
		return "", err
	}
	indexPath = filepath.Join(wellKnown, "index.json")
	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		return "", err
	}
	// Legacy v0.1 alias for older clients.
	if err := os.WriteFile(filepath.Join(legacy, "index.json"), data, 0644); err != nil {
		return "", err
	}
	for _, e := range idx.Skills {
		slug := e.Slug()
		src, ok := dirs[slug]
		if !ok {
			// entry from inline data without source dir (should not happen in BuildFeed)
			continue
		}
		dst := filepath.Join(outDir, "skills", slug)
		_ = os.RemoveAll(dst)
		if err := copySkillDir(src, dst); err != nil {
			return "", fmt.Errorf("copy skill %q: %w", slug, err)
		}
	}
	return indexPath, nil
}
