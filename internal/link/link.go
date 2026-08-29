package link

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Agent describes per-agent skill directories.
type Agent struct {
	Name       string
	GlobalDir  string
	ProjectDir string
	Universal  bool
}

// Agents map covers essential agents plus aliases. Universal agents use ~/.agents/skills
var Agents = map[string]Agent{
	"claude":       {Name: "claude", GlobalDir: "~/.claude/skills", ProjectDir: ".claude/skills", Universal: false},
	"claude-code":  {Name: "claude", GlobalDir: "~/.claude/skills", ProjectDir: ".claude/skills", Universal: false},
	"agents":       {Name: "agents", GlobalDir: "~/.agents/skills", ProjectDir: ".agents/skills", Universal: true},
	"project":      {Name: "agents", GlobalDir: "~/.agents/skills", ProjectDir: ".agents/skills", Universal: true},
	"codex":        {Name: "codex", GlobalDir: "~/.codex/skills", ProjectDir: ".codex/skills", Universal: false},
	"cursor":       {Name: "cursor", GlobalDir: "~/.cursor/skills", ProjectDir: ".cursor/skills", Universal: false},
	"windsurf":     {Name: "windsurf", GlobalDir: "~/.windsurf/skills", ProjectDir: ".windsurf/skills", Universal: false},
	"zed":          {Name: "zed", GlobalDir: "~/.zed/skills", ProjectDir: ".zed/skills", Universal: false},
	"opencode":     {Name: "opencode", GlobalDir: "~/.opencode/skills", ProjectDir: ".opencode/skills", Universal: false},
	"continue":     {Name: "continue", GlobalDir: "~/.continue/skills", ProjectDir: ".continue/skills", Universal: false},
	"aider":        {Name: "aider", GlobalDir: "~/.aider/skills", ProjectDir: ".aider/skills", Universal: false},
	"cline":        {Name: "cline", GlobalDir: "~/.cline/skills", ProjectDir: ".cline/skills", Universal: false},
	"copilot":      {Name: "copilot", GlobalDir: "~/.copilot/skills", ProjectDir: ".copilot/skills", Universal: false},
	"roo":          {Name: "roo", GlobalDir: "~/.roo/skills", ProjectDir: ".roo/skills", Universal: false},
	"antigravity":  {Name: "antigravity", GlobalDir: "~/.antigravity/skills", ProjectDir: ".antigravity/skills", Universal: false},
	"code":         {Name: "code", GlobalDir: "~/.code/skills", ProjectDir: ".code/skills", Universal: false},
}

// InstallOpts controls Install behavior.
type InstallOpts struct {
	Global   bool
	Project  bool
	Agents   string
	LinkMode string
	Copy     bool
	DryRun   bool
}

// SanitizeName lowercases and replaces [^a-z0-9._] with "-", trims "-".
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

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		if home != "" {
			return filepath.Join(home, p[2:])
		}
		return p[2:]
	}
	if p == "~" {
		home, _ := os.UserHomeDir()
		if home != "" {
			return home
		}
		return p
	}
	// Also handle plain "~/.xxx" already handled; handle "~/.agents" case above.
	// If path starts with "~/" we already did. If it is exactly expanded variant with no slash, handle.
	return p
}

// ResolveDestinations parses agentFilter comma-separated, "*" => all, "" => default from viper.
func ResolveDestinations(agentFilter string, globalOnly, projectOnly bool) []Agent {
	_ = globalOnly
	_ = projectOnly
	var names []string
	filter := strings.TrimSpace(agentFilter)
	if filter == "" {
		// try viper
		if v := viper.GetStringSlice("link.targets"); len(v) > 0 {
			names = v
		} else if vs := viper.GetString("link.targets"); vs != "" {
			// viper may store as string?
			for _, part := range strings.Split(vs, ",") {
				if s := strings.TrimSpace(part); s != "" {
					names = append(names, s)
				}
			}
		}
		if len(names) == 0 {
			names = []string{"claude", "agents", "project"}
		}
	} else if filter == "*" {
		// all
		seen := map[string]bool{}
		var out []Agent
		for _, a := range Agents {
			if seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			out = append(out, a)
		}
		// sort by name for determinism
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	} else {
		// comma separated
		parts := strings.Split(filter, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			// "*" inside list => expand all and ignore rest? Treat as all
			if p == "*" {
				seen := map[string]bool{}
				var out []Agent
				for _, a := range Agents {
					if seen[a.Name] {
						continue
					}
					seen[a.Name] = true
					out = append(out, a)
				}
				sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
				return out
			}
			names = append(names, p)
		}
	}

	// dedup and resolve
	seenNames := map[string]bool{}
	var result []Agent
	for _, n := range names {
		key := strings.ToLower(strings.TrimSpace(n))
		if key == "" {
			continue
		}
		// Normalize alias: "claude-code" -> already in map, "project" -> agents
		ag, ok := Agents[key]
		if !ok {
			// Try fallback: if key is "claude-code" etc already mapped; otherwise create generic
			// For unknown agent, synthesize dirs ~/.<name>/skills
			ag = Agent{Name: key, GlobalDir: "~/" + "." + key + "/skills", ProjectDir: "." + key + "/skills", Universal: false}
			// Alternative: "~/.<name>/skills"
			// If key already has dot? keep.
			// For unknown, ensure GlobalDir sensible
			if !strings.HasPrefix(ag.GlobalDir, "~/") {
				ag.GlobalDir = "~/" + ag.GlobalDir
			}
		}
		// dedupe by canonical Name (e.g., claude and claude-code share same Name)
		dedupeKey := ag.Name
		if seenNames[dedupeKey] {
			continue
		}
		seenNames[dedupeKey] = true
		result = append(result, ag)
	}
	// If result empty (e.g., filtering removed all), return default?
	if len(result) == 0 {
		// return default agents? keep empty
	}
	return result
}

// SkillFolderHash computes sha256 over sorted file paths + contents.
func SkillFolderHash(dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", dir)
	}
	var files []string
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// skip .git and __pycache__ directories
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		// skip files inside those dirs (already skipped via SkipDir) and also skip .git files
		if strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) || rel == ".git" {
			return nil
		}
		if strings.Contains(rel, "__pycache__") {
			return nil
		}
		// For symlink handling, we dereference: if entry is symlink, we still hash target content?
		// WalkDir follows symlink? By default it does not follow. We'll hash the file content as is.
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		// Normalize path to use "/" for determinism across OS
		normalized := filepath.ToSlash(rel)
		_, _ = h.Write([]byte(normalized))
		_, _ = h.Write([]byte{0})
		full := filepath.Join(dir, rel)
		// dereference if symlink
		// Try to read file content; if symlink, ReadFile follows link
		data, err := os.ReadFile(full)
		if err != nil {
			// If symlink broken, skip?
			continue
		}
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyDirectory copies src to dst dereferencing symlinks, skipping .git/__pycache__, preserving perms.
func CopyDirectory(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source not a directory: %s", src)
	}
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// skip .git and __pycache__
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for _, p := range parts {
			if p == ".git" || p == "__pycache__" {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dst, rel)

		// Handle symlinks: dereference
		if info.Mode()&os.ModeSymlink != 0 {
			// Resolve symlink target
			eval, err := filepath.EvalSymlinks(path)
			if err != nil {
				// Skip broken symlink
				return nil
			}
			evalInfo, err := os.Stat(eval)
			if err != nil {
				return nil
			}
			if evalInfo.IsDir() {
				// copy directory contents recursively
				// Ensure target dir
				if err := os.MkdirAll(target, 0755); err != nil {
					return err
				}
				// Walk eval dir and copy its contents into target
				return filepath.Walk(eval, func(epath string, einfo os.FileInfo, eerr error) error {
					if eerr != nil {
						return eerr
					}
					erel, err := filepath.Rel(eval, epath)
					if err != nil {
						return err
					}
					if erel == "." {
						return nil
					}
					etarget := filepath.Join(target, erel)
					if einfo.IsDir() {
						return os.MkdirAll(etarget, 0755)
					}
					if einfo.Mode()&os.ModeSymlink != 0 {
						// dereference again: read link target content
						eeval, err := filepath.EvalSymlinks(epath)
						if err != nil {
							return nil
						}
						data, err := os.ReadFile(eeval)
						if err != nil {
							return nil
						}
						if err := os.MkdirAll(filepath.Dir(etarget), 0755); err != nil {
							return err
						}
						return os.WriteFile(etarget, data, 0644)
					}
					if err := os.MkdirAll(filepath.Dir(etarget), 0755); err != nil {
						return err
					}
					in, err := os.Open(epath)
					if err != nil {
						return err
					}
					defer in.Close()
					out, err := os.OpenFile(etarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
					if err != nil {
						return err
					}
					defer out.Close()
					_, err = io.Copy(out, in)
					return err
				})
			}
			// file symlink -> copy file content
			data, err := os.ReadFile(eval)
			if err != nil {
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			return os.WriteFile(target, data, 0644)
		}

		if info.IsDir() {
			// Preserve perms -> use 0755
			return os.MkdirAll(target, 0755)
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
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		// chmod 0644 regardless of source perms (as spec: file 0644)
		_ = os.Chmod(target, 0644)
		return nil
	})
}

// CreateSymlink creates a relative symlink from link to target, handling Windows junction and fallback to copy.
func CreateSymlink(target, link string) error {
	// Ensure parent dir exists
	if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
		return err
	}
	// Compute relative target for symlink (Unix style)
	rel := target
	if absTarget, err := filepath.Abs(target); err == nil {
		if absLinkDir, err := filepath.Abs(filepath.Dir(link)); err == nil {
			if r, err := filepath.Rel(absLinkDir, absTarget); err == nil {
				rel = r
			}
		}
	}

	var err error
	if runtime.GOOS == "windows" {
		// On Windows, use absolute target and let Go handle junction if needed.
		// For directories, junction is preferred. Try symlink with target as is.
		err = os.Symlink(target, link)
	} else {
		err = os.Symlink(rel, link)
	}
	if err != nil {
		// Fallback to copy
		fmt.Fprintf(os.Stderr, "warning: symlink failed (%v), falling back to copy\n", err)
		// Remove failed link if partially created
		_ = os.Remove(link)
		return CopyDirectory(target, link)
	}
	return nil
}

// Install installs skill from cacheSkillPath to agent destinations.
func Install(cacheSkillPath, skillName string, opts InstallOpts) error {
	if strings.TrimSpace(cacheSkillPath) == "" {
		return fmt.Errorf("cacheSkillPath is empty")
	}
	info, err := os.Stat(cacheSkillPath)
	if err != nil {
		return fmt.Errorf("cacheSkillPath does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cacheSkillPath is not a directory: %s", cacheSkillPath)
	}
	if strings.TrimSpace(skillName) == "" {
		skillName = filepath.Base(cacheSkillPath)
	}
	sanitized := SanitizeName(skillName)
	if sanitized == "" {
		return fmt.Errorf("invalid skill name %q", skillName)
	}

	// Resolve destinations
	dests := ResolveDestinations(opts.Agents, opts.Global, opts.Project)
	if len(dests) == 0 {
		return fmt.Errorf("no destinations resolved")
	}

	// Determine mode
	mode := strings.ToLower(strings.TrimSpace(opts.LinkMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(viper.GetString("link.mode")))
	}
	if mode == "" {
		mode = "auto"
	}
	if opts.Copy {
		mode = "copy"
	}
	if mode == "auto" {
		if runtime.GOOS == "windows" {
			mode = "copy"
		} else {
			mode = "symlink"
		}
	}
	if mode != "symlink" && mode != "copy" {
		// fallback to symlink/copy detection
		if mode == "junction" {
			mode = "symlink"
		} else {
			mode = "symlink"
		}
	}

	// Compute absolute cache path
	cacheAbs, err := filepath.Abs(cacheSkillPath)
	if err != nil {
		cacheAbs = cacheSkillPath
	}

	// For each agent, compute dests based on Global/Project flags
	for _, ag := range dests {
		var targets []string
		doGlobal := false
		doProject := false
		if opts.Global && !opts.Project {
			doGlobal = true
		} else if opts.Project && !opts.Global {
			doProject = true
		} else if opts.Global && opts.Project {
			doGlobal = true
			doProject = true
		} else {
			// neither set => both (respecting default behavior)
			doGlobal = true
			doProject = true
		}
		if doGlobal {
			g := expandHome(ag.GlobalDir)
			if g != "" {
				targets = append(targets, filepath.Join(g, sanitized))
			}
		}
		if doProject {
			p := ag.ProjectDir
			// ProjectDir is relative to cwd
			if filepath.IsAbs(p) {
				// expand home if needed
				p = expandHome(p)
			}
			// For project, join with current working dir via filepath.Join (relative)
			// If ProjectDir is ".agents/skills", we keep relative.
			targets = append(targets, filepath.Join(p, sanitized))
		}

		for _, dest := range targets {
			if opts.DryRun {
				fmt.Fprintf(os.Stderr, "dry-run: would install %s -> %s (mode=%s)\n", cacheAbs, dest, mode)
				continue
			}
			// If dest exists, remove
			if _, err := os.Lstat(dest); err == nil {
				_ = os.RemoveAll(dest)
			}
			// Ensure parent dir
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return fmt.Errorf("failed to create parent dir %s: %w", filepath.Dir(dest), err)
			}
			if mode == "symlink" {
				// Try symlink, fallback to copy on failure
				rel := cacheAbs
				if absDestDir, err := filepath.Abs(filepath.Dir(dest)); err == nil {
					if r, err := filepath.Rel(absDestDir, cacheAbs); err == nil {
						rel = r
					}
				}
				var linkErr error
				if runtime.GOOS == "windows" {
					linkErr = os.Symlink(cacheAbs, dest)
				} else {
					linkErr = os.Symlink(rel, dest)
				}
				if linkErr != nil {
					fmt.Fprintf(os.Stderr, "warning: symlink failed for %s -> %s (%v), falling back to copy\n", dest, cacheAbs, linkErr)
					_ = os.RemoveAll(dest)
					if err := CopyDirectory(cacheAbs, dest); err != nil {
						return fmt.Errorf("copy fallback failed for %s: %w", dest, err)
					}
				}
			} else {
				if err := CopyDirectory(cacheAbs, dest); err != nil {
					return fmt.Errorf("copy failed for %s: %w", dest, err)
				}
			}
		}
	}

	// Optionally update lockfile hash (caller can call UpdateLockfile separately)
	// Compute hash for potential use but not automatically writing unless desired.
	// We do not auto-write lockfile here to keep Install side-effect limited.

	return nil
}

// UpdateLockfile updates lockfile with skill hash.
// global=true => ~/.agents/.skill-lock.json v3 else ./skills-lock.json v1
func UpdateLockfile(isGlobal bool, skillName, hash string) error {
	sanitized := SanitizeName(skillName)
	if sanitized == "" {
		return fmt.Errorf("invalid skill name %q", skillName)
	}
	var lockPath string
	var version int
	if isGlobal {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "."
		}
		// check XDG_STATE_HOME first
		if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
			lockPath = filepath.Join(xdg, "skills", ".skill-lock.json")
		} else {
			lockPath = filepath.Join(home, ".agents", ".skill-lock.json")
		}
		version = 3
	} else {
		// Project lock in current directory
		lockPath = filepath.Join(".", "skills-lock.json")
		version = 1
	}

	// Read existing
	var data map[string]interface{}
	if b, err := os.ReadFile(lockPath); err == nil {
		if err := json.Unmarshal(b, &data); err != nil {
			data = make(map[string]interface{})
		}
	} else {
		data = make(map[string]interface{})
	}
	if data == nil {
		data = make(map[string]interface{})
	}
	// Ensure version field
	data["version"] = version

	// Skills map
	var skillsMap map[string]interface{}
	if v, ok := data["skills"]; ok {
		if m, ok := v.(map[string]interface{}); ok {
			skillsMap = m
		}
	}
	if skillsMap == nil {
		skillsMap = make(map[string]interface{})
	}
	if isGlobal {
		skillsMap[sanitized] = map[string]interface{}{
			"hash":      hash,
			"updatedAt": time.Now().UTC().Format(time.RFC3339),
		}
	} else {
		skillsMap[sanitized] = hash
	}
	data["skills"] = skillsMap

	// Ensure parent dir exists
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	// Write with 0644
	if err := os.WriteFile(lockPath, b, 0644); err != nil {
		return err
	}
	return nil
}



