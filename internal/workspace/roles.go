package workspace

import (
	"sort"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// Role is a named agent persona: a description plus the runner settings.
type Role struct {
	Name        string
	Description string
	Builtin     bool
	ReadOnly    bool // reviews and reports only; never writes files
	Agent       config.AgentConfig
}

var builtinDescriptions = map[string]string{
	"pm":          "Plans work, splits tasks and arbitrates feedback.",
	"coder":       "Writes and changes production code.",
	"coder_fixer": "Fixes build and test failures.",
	"qa":          "Writes tests and reviews code quality and edge cases.",
	"ux_reviewer": "Reviews user-facing flows, wording and usability.",
	"security":    "Audits code for vulnerabilities and unsafe patterns.",
}

// readOnlyBuiltin lists the built-in roles that only review and report,
// whatever the config says.
var readOnlyBuiltin = map[string]bool{"ux_reviewer": true, "security": true}

// Roles returns the built-in roles in config.BuiltinRoles order, then custom
// roles sorted by name. A custom role without runner/model inherits the pm
// role's runner, model and effort.
func Roles(cfg config.Config) []Role {
	pm := cfg.Agents["pm"]
	var out []Role
	for _, name := range config.BuiltinRoles {
		ac := cfg.Agents[name]
		desc := builtinDescriptions[name]
		out = append(out, Role{Name: name, Description: desc, Builtin: true, ReadOnly: readOnlyBuiltin[name], Agent: ac})
	}
	var custom []string
	for name := range cfg.Agents {
		// "pr" is the pull-request agent, not a workspace role.
		if !config.IsBuiltinRole(name) && name != "pr" {
			custom = append(custom, name)
		}
	}
	sort.Strings(custom)
	for _, name := range custom {
		ac := cfg.Agents[name]
		if ac.Runner == "" {
			ac.Runner = pm.Runner
		}
		if ac.Model == "" {
			ac.Model = pm.Model
		}
		if ac.Effort == "" {
			ac.Effort = pm.Effort
		}
		ac.Skills = nil
		out = append(out, Role{Name: name, Description: ac.Description, ReadOnly: ac.ReadOnly, Agent: ac})
	}
	return out
}

// FindRole looks a role up by name.
func FindRole(cfg config.Config, name string) (Role, bool) {
	for _, r := range Roles(cfg) {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}

// MaxParallel is the number of nodes that may run at once: the configured
// value, default 3, clamped to 1..8.
func MaxParallel(cfg config.Config) int {
	n := cfg.Workspace.MaxParallel
	if n <= 0 {
		return 3
	}
	if n > 8 {
		return 8
	}
	return n
}

// Reconcile clears the assigned role of nodes whose role no longer exists.
func Reconcile(g *Graph, cfg config.Config) {
	for _, n := range g.Nodes {
		if n.Role == "" {
			continue
		}
		if _, ok := FindRole(cfg, n.Role); !ok {
			n.Role = ""
		}
	}
}
