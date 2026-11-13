// Package plugins loads Claude-format plugins (commands, skills and agents)
// from <project>/.decsr/plugins and ~/.decsr/plugins. Hooks and MCP servers
// are detected but never used.
package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
)

const (
	// maxFileBytes caps one plugin file (manifest, command, skill, agent).
	maxFileBytes = 64 * 1024
	// maxAgentPromptBytes caps the role prompt taken from an agent file.
	maxAgentPromptBytes = 16 * 1024

	manifestPath = ".claude-plugin/plugin.json"
	disabledFile = "disabled"
)

// Command is a plugin command, run as /<plugin>:<name>.
type Command struct {
	Plugin, Name, Description, ArgHint, Body string
}

// Skill is a plugin skill with the id <plugin>:<name>.
type Skill struct {
	ID, Plugin, Name, Description string
	path                          string // the SKILL.md file
}

// Agent is a plugin agent offered as the workspace role <plugin>:<name>.
type Agent struct {
	Plugin, Name string
	config.PluginAgent
}

// Role is the workspace role name of the agent.
func (a Agent) Role() string { return a.Plugin + ":" + a.Name }

// Plugin is one discovered plugin. A disabled plugin has no parts.
type Plugin struct {
	Name, Description, Version string
	Scope                      string // "project" or "user"
	Enabled                    bool
	Commands                   []Command
	Skills                     []Skill
	Agents                     []Agent
	HasHooks, HasMCP           bool
}

// Set is the result of a scan.
type Set struct {
	Plugins  []Plugin
	Warnings []string
}

// Commands returns the commands of all enabled plugins, sorted by name.
func (s Set) Commands() []Command {
	var out []Command
	for _, p := range s.Plugins {
		out = append(out, p.Commands...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Plugin+":"+out[i].Name < out[j].Plugin+":"+out[j].Name
	})
	return out
}

// AgentRoles returns the agents of all enabled plugins keyed by role name.
func (s Set) AgentRoles() map[string]config.PluginAgent {
	out := map[string]config.PluginAgent{}
	for _, p := range s.Plugins {
		for _, a := range p.Agents {
			out[a.Role()] = a.PluginAgent
		}
	}
	return out
}

type manifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	Commands    json.RawMessage `json:"commands"`
	Skills      json.RawMessage `json:"skills"`
	Agents      json.RawMessage `json:"agents"`
	Hooks       json.RawMessage `json:"hooks"`
	MCPServers  json.RawMessage `json:"mcpServers"`
}

// Dir is the user plugin directory, ~/.decsr/plugins ("" without a home).
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, config.GlobalDir, "plugins")
}

// Load scans <projectRoot>/.decsr/plugins and then ~/.decsr/plugins. A project
// plugin wins over a user plugin of the same name. The result is sorted by name.
func Load(projectRoot string) Set {
	var set Set
	seen := map[string]bool{}
	disabled := readDisabled()
	scan := func(base, scope string) {
		entries, err := os.ReadDir(base)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() { // symlinks are not directories here
				continue
			}
			dir := filepath.Join(base, e.Name())
			p, warn, ok := loadPlugin(dir, scope, disabled)
			if warn != "" {
				set.Warnings = append(set.Warnings, warn)
			}
			if !ok || seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			set.Plugins = append(set.Plugins, p)
		}
	}
	scan(filepath.Join(projectRoot, config.GlobalDir, "plugins"), "project")
	scan(Dir(), "user")
	sort.Slice(set.Plugins, func(i, j int) bool { return set.Plugins[i].Name < set.Plugins[j].Name })
	return set
}

// loadPlugin reads one plugin directory. ok is false when it is not a plugin
// (no manifest) or its manifest is unusable; warn then says why, if it is
// worth saying.
func loadPlugin(dir, scope string, disabled map[string]bool) (p Plugin, warn string, ok bool) {
	if !isDir(filepath.Join(dir, ".claude-plugin")) { // a symlink is not a directory here
		return p, "", false
	}
	data, found, err := readFile(filepath.Join(dir, manifestPath))
	if !found {
		if err != nil {
			return p, fmt.Sprintf("plugin %s: %v", filepath.Base(dir), err), false
		}
		return p, "", false
	}
	var mf manifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return p, fmt.Sprintf("plugin %s: plugin.json is not valid JSON", filepath.Base(dir)), false
	}
	if !config.ValidName(mf.Name) {
		return p, fmt.Sprintf("plugin %s: invalid plugin name %q", filepath.Base(dir), sanitize(mf.Name)), false
	}
	p = Plugin{
		Name: mf.Name, Description: sanitize(mf.Description), Version: sanitize(mf.Version),
		Scope: scope, Enabled: !disabled[mf.Name],
		HasHooks: len(mf.Hooks) > 0 || isDir(filepath.Join(dir, "hooks")),
		HasMCP:   len(mf.MCPServers) > 0 || isFile(filepath.Join(dir, ".mcp.json")),
	}
	if !p.Enabled {
		return p, "", true
	}
	p.Commands = loadCommands(dir, p.Name, mf.Commands)
	p.Skills = loadSkills(dir, p.Name, mf.Skills)
	p.Agents = loadAgents(dir, p.Name, mf.Agents)
	return p, "", true
}

// scanPaths lists the directories to scan: the default one and the listed
// ones. Absolute paths and paths that leave the plugin are dropped.
func scanPaths(dir, def string, raw json.RawMessage) []string {
	paths := []string{def}
	var one string
	var many []string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		many = []string{one}
	} else {
		_ = json.Unmarshal(raw, &many)
	}
	for _, rel := range many {
		rel = filepath.Clean(rel)
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		paths = append(paths, rel)
	}
	var out []string
	for _, rel := range paths {
		if p, ok := inside(dir, rel); ok {
			out = append(out, p)
		}
	}
	return out
}

// inside resolves dir/rel and reports whether it stays inside dir once
// symlinks are followed.
func inside(dir, rel string) (string, bool) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	p, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return "", false
	}
	if r, err := filepath.Rel(root, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

// markdownFiles lists the *.md regular files of dir, or path itself when it is
// one .md file.
func markdownFiles(path string) []string {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if fi.Mode().IsRegular() && strings.HasSuffix(path, ".md") {
		return []string{path}
	}
	if !fi.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, filepath.Join(path, e.Name()))
		}
	}
	return out
}

func loadCommands(dir, plugin string, raw json.RawMessage) []Command {
	var out []Command
	seen := map[string]bool{}
	for _, sp := range scanPaths(dir, "commands", raw) {
		for _, f := range markdownFiles(sp) {
			name := strings.TrimSuffix(filepath.Base(f), ".md")
			if !config.ValidName(name) || seen[name] {
				continue
			}
			text, ok, _ := readFile(f)
			if !ok {
				continue
			}
			desc, hint, body := SplitCommand(string(text))
			if body == "" {
				continue
			}
			seen[name] = true
			out = append(out, Command{Plugin: plugin, Name: name, Description: desc, ArgHint: hint, Body: body})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadSkills(dir, plugin string, raw json.RawMessage) []Skill {
	var out []Skill
	seen := map[string]bool{}
	for _, sp := range scanPaths(dir, "skills", raw) {
		entries, err := os.ReadDir(sp)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !config.ValidName(e.Name()) || seen[e.Name()] {
				continue
			}
			file := filepath.Join(sp, e.Name(), "SKILL.md")
			text, ok, _ := readFile(file)
			if !ok {
				continue
			}
			fm, body := ParseFrontmatter(string(text))
			if strings.TrimSpace(body) == "" {
				continue
			}
			seen[e.Name()] = true
			out = append(out, Skill{ID: plugin + ":" + e.Name(), Plugin: plugin, Name: e.Name(), Description: fm.String("description"), path: file})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func loadAgents(dir, plugin string, raw json.RawMessage) []Agent {
	var out []Agent
	seen := map[string]bool{}
	for _, sp := range scanPaths(dir, "agents", raw) {
		for _, f := range markdownFiles(sp) {
			name := strings.TrimSuffix(filepath.Base(f), ".md")
			if !config.ValidName(name) || seen[name] {
				continue
			}
			text, ok, _ := readFile(f)
			if !ok {
				continue
			}
			fm, body := ParseFrontmatter(string(text))
			body = strings.TrimSpace(body)
			if body == "" {
				continue
			}
			if len(body) > maxAgentPromptBytes {
				body = strings.ToValidUTF8(body[:maxAgentPromptBytes], "")
			}
			seen[name] = true
			out = append(out, Agent{Plugin: plugin, Name: name, PluginAgent: config.PluginAgent{
				Description: fm.String("description"), Prompt: body, ReadOnly: readOnlyTools(fm),
			}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readOnlyTools is true when the agent lists its tools and none can write.
func readOnlyTools(fm Frontmatter) bool {
	tools, ok := fm.List("tools")
	if !ok {
		return false
	}
	for _, t := range tools {
		switch t {
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
			return false
		}
	}
	return true
}

// LoadSkill returns the body (without frontmatter) of the skill id
// "<plugin>:<skill>". It wraps os.ErrNotExist when the plugin or skill is
// missing or the plugin is disabled.
func LoadSkill(projectRoot, id string) (string, error) {
	if !config.ValidQualifiedName(id) {
		return "", fmt.Errorf("invalid skill name %q", id)
	}
	for _, p := range Load(projectRoot).Plugins {
		for _, s := range p.Skills {
			if s.ID != id {
				continue
			}
			text, ok, err := readFile(s.path)
			if !ok {
				if err == nil {
					err = os.ErrNotExist
				}
				return "", fmt.Errorf("skill %q: %w", id, err)
			}
			_, body := ParseFrontmatter(string(text))
			return strings.TrimSpace(body), nil
		}
	}
	return "", fmt.Errorf("skill %q: %w", id, os.ErrNotExist)
}

// readFile reads a regular file (not a link) of at most 64 KB without NUL
// bytes. found is false when it is missing or unusable; err explains an
// unusable file.
func readFile(path string) (data []byte, found bool, err error) {
	fi, statErr := os.Lstat(path)
	if statErr != nil || !fi.Mode().IsRegular() {
		return nil, false, nil
	}
	f, openErr := os.Open(path) // #nosec G304 -- regular file below a scanned plugin directory
	if openErr != nil {
		return nil, false, nil
	}
	defer func() { _ = f.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if readErr != nil {
		return nil, false, nil
	}
	if len(data) > maxFileBytes {
		return nil, false, fmt.Errorf("%s is larger than %d KB", filepath.Base(path), maxFileBytes/1024)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, false, fmt.Errorf("%s contains a NUL byte", filepath.Base(path))
	}
	return data, true, nil
}

func isDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

func disabledPath() string {
	d := Dir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, disabledFile)
}

// readDisabled returns the names listed in ~/.decsr/plugins/disabled.
func readDisabled() map[string]bool {
	out := map[string]bool{}
	p := disabledPath()
	if p == "" {
		return out
	}
	data, ok, _ := readFile(p)
	if !ok {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			out[name] = true
		}
	}
	return out
}

// SetEnabled adds name to or removes it from the disabled file.
func SetEnabled(name string, enabled bool) error {
	if !config.ValidName(name) {
		return fmt.Errorf("invalid plugin name %q", name)
	}
	p := disabledPath()
	if p == "" {
		return errors.New("home directory not found")
	}
	set := readDisabled()
	if enabled {
		delete(set, name)
	} else {
		set[name] = true
	}
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.Join(names, "\n")+"\n"), 0o600)
}
