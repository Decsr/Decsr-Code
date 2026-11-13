package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Decsr/Decsr-Code/internal/safefile"
)

// maxUserSkillBytes caps one file-based skill document.
const maxUserSkillBytes = 64 * 1024

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// knownRunners are the runner names runner.New accepts ("" means the default).
// It is kept here because the runner package imports config.
var knownRunners = map[string]bool{
	"": true, "opencode": true, "claude": true, "ollama": true, "mlx": true,
	"codex": true, "copilot": true, "lmstudio": true,
}

// KnownRunner reports whether name is a runner the factory accepts.
func KnownRunner(name string) bool { return knownRunners[name] }

// ValidName reports whether s is a valid profile, command or skill name.
func ValidName(s string) bool { return nameRe.MatchString(s) }

// Profile is a named set of role settings that can be applied to a project.
type Profile struct {
	Name   string                 `yaml:"-"`
	Agents map[string]AgentConfig `yaml:"agents"`
}

// ProfilesDir is ~/.decsr/profiles ("" when the home directory is unknown).
func ProfilesDir() string {
	d := globalConfigDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "profiles")
}

// ListProfiles returns the saved profile names, sorted.
func ListProfiles() []string {
	dir := ProfilesDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".yaml")
		if e.IsDir() || name == e.Name() || !ValidName(name) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// profileAgent keeps only the fields a profile stores.
func profileAgent(ac AgentConfig) AgentConfig {
	return AgentConfig{
		Runner: ac.Runner, Model: ac.Model, Effort: ac.Effort,
		Description: ac.Description, ReadOnly: ac.ReadOnly, UserSkills: ac.UserSkills,
	}
}

func profileFile(name string) (dir, file string, err error) {
	if !ValidName(name) {
		return "", "", fmt.Errorf("invalid profile name %q", name)
	}
	dir = ProfilesDir()
	if dir == "" {
		return "", "", errors.New("home directory not found")
	}
	return dir, name + ".yaml", nil
}

// LoadProfile reads a saved profile.
func LoadProfile(name string) (Profile, error) {
	dir, file, err := profileFile(name)
	if err != nil {
		return Profile{}, err
	}
	data, err := safefile.ReadFile(dir, file)
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", name, err)
	}
	p.Name = name
	for role, ac := range p.Agents {
		p.Agents[role] = profileAgent(ac)
	}
	return p, nil
}

// SaveProfile stores agents as profile name. Only runner, model, effort,
// description, read_only and skills are kept; roles with none of them set are
// left out.
func SaveProfile(name string, agents map[string]AgentConfig) error {
	dir, file, err := profileFile(name)
	if err != nil {
		return err
	}
	p := Profile{Agents: make(map[string]AgentConfig, len(agents))}
	for role, ac := range agents {
		ac = profileAgent(ac)
		if ac.Runner == "" && ac.Model == "" && ac.Effort == "" && ac.Description == "" && !ac.ReadOnly && len(ac.UserSkills) == 0 {
			continue
		}
		p.Agents[role] = ac
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return safefile.WriteFile(dir, file, data, 0o600)
}

// DeleteProfile removes a saved profile.
func DeleteProfile(name string) error {
	dir, file, err := profileFile(name)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, file))
}

// ApplyProfile writes the profile's roles into <root>/.decsr/project.yaml
// (roles not in the profile are left alone) and records the profile name. It
// fails without writing when the profile is invalid or project.yaml cannot be
// parsed.
func ApplyProfile(root, name string) error {
	p, err := LoadProfile(name)
	if err != nil {
		return err
	}
	roles := make([]string, 0, len(p.Agents))
	for role := range p.Agents {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		if !ValidRoleName(role) {
			return fmt.Errorf("profile %q: invalid role name %q", name, role)
		}
		if r := p.Agents[role].Runner; !KnownRunner(r) {
			return fmt.Errorf("profile %q: role %q uses unknown runner %q", name, role, r)
		}
	}
	cfg, err := loadFile(filepath.Join(root, GlobalDir), ProjectFilename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", ProjectFilename, err)
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]AgentConfig)
	}
	for _, role := range roles {
		pa := p.Agents[role]
		existing := cfg.Agents[role]
		// An empty runner, model, effort or description keeps the project value.
		for _, f := range []struct {
			dst *string
			src string
		}{
			{&existing.Runner, pa.Runner}, {&existing.Model, pa.Model},
			{&existing.Effort, pa.Effort}, {&existing.Description, pa.Description},
		} {
			if f.src != "" {
				*f.dst = f.src
			}
		}
		existing.ReadOnly, existing.UserSkills = pa.ReadOnly, pa.UserSkills
		cfg.Agents[role] = existing
	}
	cfg.Project.Profile = name
	return Save(root, cfg)
}

// LoadUserSkill returns the skill document <root>/.decsr/skills/<name>.md, or
// else ~/.decsr/skills/<name>.md. The name is validated and the file is capped
// at 64 KB.
func LoadUserSkill(root, name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid skill name %q", name)
	}
	dirs := []string{filepath.Join(root, GlobalDir, "skills")}
	if g := globalConfigDir(); g != "" {
		dirs = append(dirs, filepath.Join(g, "skills"))
	}
	var firstErr error
	for _, dir := range dirs {
		s, err := readSkill(dir, name+".md")
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", fmt.Errorf("skill %q: %w", name, firstErr)
}

func readSkill(dir, file string) (string, error) {
	f, err := safefile.OpenFile(dir, file, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxUserSkillBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxUserSkillBytes {
		return "", fmt.Errorf("skill file %s is larger than %d KB", file, maxUserSkillBytes/1024)
	}
	return string(data), nil
}
