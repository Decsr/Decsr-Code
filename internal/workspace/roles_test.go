package workspace

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func testCfg() config.Config {
	cfg := config.DefaultConfig()
	pm := cfg.Agents["pm"]
	pm.Runner, pm.Model, pm.Effort = "claude", "big", "high"
	cfg.Agents["pm"] = pm
	cfg.Agents["zeta"] = config.AgentConfig{Description: "Z role"}
	cfg.Agents["alpha"] = config.AgentConfig{Description: "A role", Runner: "codex", Model: "small"}
	return cfg
}

func TestRolesOrderAndInheritance(t *testing.T) {
	roles := Roles(testCfg())
	var names []string
	for _, r := range roles {
		names = append(names, r.Name)
	}
	want := []string{"pm", "coder", "coder_fixer", "qa", "ux_reviewer", "security", "alpha", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("names = %v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	if !roles[0].Builtin || roles[6].Builtin {
		t.Error("Builtin flag wrong")
	}
	if roles[0].Description != "Plans work, splits tasks and arbitrates feedback." {
		t.Errorf("pm description = %q", roles[0].Description)
	}
	alpha, zeta := roles[6], roles[7]
	if alpha.Agent.Runner != "codex" || alpha.Agent.Model != "small" || alpha.Agent.Effort != "high" {
		t.Errorf("alpha = %+v", alpha.Agent)
	}
	if zeta.Agent.Runner != "claude" || zeta.Agent.Model != "big" || zeta.Agent.Effort != "high" {
		t.Errorf("zeta should inherit pm: %+v", zeta.Agent)
	}
	if len(zeta.Agent.Skills) != 0 {
		t.Errorf("custom skills = %v", zeta.Agent.Skills)
	}
}

func TestFindRole(t *testing.T) {
	cfg := testCfg()
	if r, ok := FindRole(cfg, "qa"); !ok || r.Name != "qa" {
		t.Errorf("qa: %+v %v", r, ok)
	}
	if r, ok := FindRole(cfg, "zeta"); !ok || r.Description != "Z role" {
		t.Errorf("zeta: %+v %v", r, ok)
	}
	if _, ok := FindRole(cfg, "nope"); ok {
		t.Error("nope should be missing")
	}
}

func TestMaxParallel(t *testing.T) {
	for in, want := range map[int]int{0: 3, 5: 5, 99: 8, -1: 3, 1: 1, 8: 8} {
		cfg := config.Config{Workspace: config.WorkspaceConfig{MaxParallel: in}}
		if got := MaxParallel(cfg); got != want {
			t.Errorf("MaxParallel(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestReconcileClearsUnknownRole(t *testing.T) {
	a, b, c := n("A"), n("B"), n("C")
	a.Role, b.Role = "zeta", "gone"
	g := &Graph{Nodes: []*Node{a, b, c}}
	Reconcile(g, testCfg())
	if a.Role != "zeta" || b.Role != "" || c.Role != "" {
		t.Errorf("roles = %q %q %q", a.Role, b.Role, c.Role)
	}
}

func TestRolesReadOnly(t *testing.T) {
	cfg := testCfg()
	cfg.Agents["auditor"] = config.AgentConfig{Description: "Audits", ReadOnly: true}
	// A config value cannot make a built-in reviewer writable.
	cfg.Agents["security"] = config.AgentConfig{ReadOnly: false}
	got := map[string]bool{}
	for _, r := range Roles(cfg) {
		got[r.Name] = r.ReadOnly
	}
	for name, want := range map[string]bool{"ux_reviewer": true, "security": true, "auditor": true, "coder": false, "qa": false, "alpha": false} {
		if got[name] != want {
			t.Errorf("%s ReadOnly = %v, want %v", name, got[name], want)
		}
	}
}
