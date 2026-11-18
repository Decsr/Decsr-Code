package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// maxImportBytes caps the size of a Claude Code plugin that is imported.
const maxImportBytes = 64 << 20

// ClaudePlugin is a plugin that Claude Code has installed on this machine.
type ClaudePlugin struct {
	Name        string
	Marketplace string
	Version     string
	Dir         string // below ~/.claude/plugins/cache
	Skills      int
	Agents      int
	Commands    int
}

// claudeCache is ~/.claude/plugins/cache, the only place of ~/.claude that is
// read. Nothing else under ~/.claude is opened (never the login files).
func claudeCache() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "plugins", "cache")
}

func claudeIndex() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
}

// ClaudeInstalled lists the plugins Claude Code has installed, from
// ~/.claude/plugins/installed_plugins.json. A missing or unreadable file, or
// an entry that does not parse or points outside the plugin cache, is left
// out. The result is sorted by name.
func ClaudeInstalled() []ClaudePlugin {
	idx, cache := claudeIndex(), claudeCache()
	if idx == "" || cache == "" {
		return nil
	}
	data, err := os.ReadFile(idx) // #nosec G304 -- fixed path of the Claude Code plugin index
	if err != nil || len(data) > 4<<20 {
		return nil
	}
	var file struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			Version     string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &file) != nil {
		return nil
	}
	root, err := filepath.EvalSymlinks(cache)
	if err != nil {
		return nil
	}
	var out []ClaudePlugin
	seen := map[string]bool{}
	for key, installs := range file.Plugins {
		name, market, _ := strings.Cut(key, "@")
		if !config.ValidName(name) || seen[name] {
			continue
		}
		for _, in := range installs {
			dir, ok := within(root, in.InstallPath)
			if !ok || !isDir(dir) {
				continue
			}
			seen[name] = true
			p := ClaudePlugin{Name: name, Marketplace: sanitize(market), Version: sanitize(in.Version), Dir: dir}
			p.Skills, p.Agents, p.Commands = countParts(dir)
			out = append(out, p)
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// within resolves p and reports whether it is a path below root.
func within(root, p string) (string, bool) {
	if p == "" || !filepath.IsAbs(p) {
		return "", false
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	r, err := filepath.Rel(root, real)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return real, true
}

// countParts counts the skills, agents and commands of a plugin directory,
// whether or not the plugin is enabled. A directory without a manifest is
// counted by its default folders.
func countParts(dir string) (skills, agents, commands int) {
	if p, ok := Inspect(dir); ok {
		return len(p.Skills), len(p.Agents), len(p.Commands)
	}
	if es, err := os.ReadDir(filepath.Join(dir, "skills")); err == nil {
		for _, e := range es {
			if e.IsDir() && isFile(filepath.Join(dir, "skills", e.Name(), "SKILL.md")) {
				skills++
			}
		}
	}
	return skills, len(markdownFiles(filepath.Join(dir, "agents"))), len(markdownFiles(filepath.Join(dir, "commands")))
}

// Inspect reads a plugin directory as if it were enabled.
func Inspect(dir string) (Plugin, bool) {
	p, _, ok := loadPlugin(dir, "", nil)
	if ok {
		p.Dir = dir
	}
	return p, ok
}

// ImportClaude copies the Claude Code plugin called name into
// ~/.decsr/plugins/<name>, in the layout Load reads, and returns what the
// copy holds. A plugin without .claude-plugin/plugin.json gets one written
// (taking the skill, agent and command paths of its marketplace.json entry
// when there is one). Nothing in the plugin is run; hooks and MCP servers
// stay unused. An existing ~/.decsr/plugins/<name> is never replaced.
func ImportClaude(name string) (Plugin, error) {
	var src ClaudePlugin
	for _, c := range ClaudeInstalled() {
		if c.Name == name {
			src = c
		}
	}
	if src.Dir == "" {
		return Plugin{}, fmt.Errorf("Claude Code has no plugin named %q installed", sanitize(name))
	}
	base := Dir()
	if base == "" {
		return Plugin{}, errors.New("home directory not found")
	}
	dst := filepath.Join(base, name)
	if _, err := os.Lstat(dst); err == nil {
		return Plugin{}, fmt.Errorf("%s is already in %s", name, base)
	}
	if size, err := treeSize(src.Dir); err != nil {
		return Plugin{}, err
	} else if size > maxImportBytes {
		return Plugin{}, fmt.Errorf("%s is larger than %d MB", name, maxImportBytes>>20)
	}
	if err := os.MkdirAll(base, 0o750); err != nil {
		return Plugin{}, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(base), ".import-*")
	if err != nil {
		return Plugin{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	staged := filepath.Join(tmp, "new")
	if err := copyTree(src.Dir, staged); err != nil {
		return Plugin{}, fmt.Errorf("%s: copy: %w", name, err)
	}
	if err := ensureManifest(staged, src); err != nil {
		return Plugin{}, err
	}
	p, ok := Inspect(staged)
	if !ok || p.Name != name {
		return Plugin{}, fmt.Errorf("%s: the copy has no usable %s", name, manifestPath)
	}
	if err := os.Rename(staged, dst); err != nil {
		return Plugin{}, err
	}
	p.Dir = dst
	return p, nil
}

// treeSize adds up the regular files of dir that copyTree would copy.
func treeSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n, err
}

// ensureManifest writes .claude-plugin/plugin.json into dir when the plugin
// has none. The name is the one Claude Code lists it under.
func ensureManifest(dir string, src ClaudePlugin) error {
	path := filepath.Join(dir, manifestPath)
	if isFile(path) {
		return nil
	}
	mf := map[string]any{"name": src.Name}
	if src.Version != "" {
		mf["version"] = src.Version
	}
	if data, ok, _ := readFile(filepath.Join(dir, ".claude-plugin", "marketplace.json")); ok {
		var m struct {
			Plugins []map[string]json.RawMessage `json:"plugins"`
		}
		if json.Unmarshal(data, &m) == nil {
			for _, e := range m.Plugins {
				var n string
				if json.Unmarshal(e["name"], &n) != nil || n != src.Name {
					continue
				}
				for _, k := range []string{"description", "skills", "agents", "commands"} {
					if v, ok := e[k]; ok {
						mf[k] = v
					}
				}
			}
		}
	}
	data, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
