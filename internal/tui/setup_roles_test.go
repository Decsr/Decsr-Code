package tui

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestProjectSetupListsCustomRoles(t *testing.T) {
	root := langSetupRoot(t, "")
	if err := config.SaveProjectRole(root, "docs_writer", config.AgentConfig{Description: "writes docs", Runner: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProjectRole(root, "api_designer", config.AgentConfig{Description: "designs APIs"}); err != nil {
		t.Fatal(err)
	}
	projectAgents := config.LoadProject(root).Agents

	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "", "proj", projectAgents)
	m.root = root

	n := len(config.BuiltinRoles)
	if len(m.agentRoles) != n+2 {
		t.Fatalf("roles = %v, want built-ins plus 2 custom", m.agentRoles)
	}
	for i, r := range config.BuiltinRoles {
		if m.agentRoles[i] != r {
			t.Fatalf("role %d = %q, want built-in %q", i, m.agentRoles[i], r)
		}
	}
	if m.agentRoles[n] != "api_designer" || m.agentRoles[n+1] != "docs_writer" {
		t.Fatalf("custom roles not sorted after built-ins: %v", m.agentRoles)
	}
	// Set a model for docs_writer and save; the description must survive.
	m.agentOverrides["docs_writer"] = agentSetupOverride{runner: "codex", model: "gpt-5"}
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	got := config.LoadProject(root).Agents["docs_writer"]
	if got.Runner != "codex" || got.Model != "gpt-5" {
		t.Fatalf("override not saved: %+v", got)
	}
	if got.Description != "writes docs" {
		t.Fatalf("description lost: %+v", got)
	}
	// A custom role without overrides must not vanish from the project file.
	if d := config.LoadProject(root).Agents["api_designer"].Description; d != "designs APIs" {
		t.Fatalf("custom role dropped on save: %q", d)
	}
}
