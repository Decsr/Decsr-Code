package config

import (
	"testing"
)

func boolp(b bool) *bool { return &b }

func TestRoutingDefaults(t *testing.T) {
	var r RoutingConfig
	if r.EffectiveMode() != RoutingManual || !r.ApproveUnattended() {
		t.Fatalf("defaults: %q %v", r.EffectiveMode(), r.ApproveUnattended())
	}
	r = RoutingConfig{Mode: " Auto-Role ", AutoApprove: boolp(false)}
	if r.EffectiveMode() != RoutingAutoRole || r.ApproveUnattended() {
		t.Fatalf("set: %q %v", r.EffectiveMode(), r.ApproveUnattended())
	}
	if NormalizeRoutingMode("nonsense") != RoutingManual {
		t.Fatal("an unknown mode must be manual")
	}
}

func TestRoutingMergeLayers(t *testing.T) {
	dst := Config{Routing: RoutingConfig{Mode: RoutingAutoRole, AutoApprove: boolp(false)}}
	merge(&dst, Config{}) // an empty layer changes nothing
	if dst.Routing.Mode != RoutingAutoRole || dst.Routing.AutoApprove == nil || *dst.Routing.AutoApprove {
		t.Fatalf("empty layer: %+v", dst.Routing)
	}
	merge(&dst, Config{Routing: RoutingConfig{Mode: RoutingAutoConnected, AutoApprove: boolp(true)}})
	if dst.Routing.Mode != RoutingAutoConnected || !dst.Routing.ApproveUnattended() {
		t.Fatalf("project layer must win: %+v", dst.Routing)
	}
}

func TestProfileRoutingRoundTripAndApply(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if err := SaveProfileRouting("fast", testAgents(), RoutingProfile{Mode: "auto-connected", AutoApprove: boolp(false)}); err != nil {
		t.Fatal(err)
	}
	p, err := LoadProfile("fast")
	if err != nil {
		t.Fatal(err)
	}
	if p.Routing == nil || p.Routing.Mode != RoutingAutoConnected || p.Routing.AutoApprove == nil || *p.Routing.AutoApprove {
		t.Fatalf("profile routing = %+v", p.Routing)
	}
	// A profile saved without routing keeps the project's routing.
	if err := Save(root, Config{Routing: RoutingConfig{Mode: RoutingAutoRole}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveProfile("plain", testAgents()); err != nil {
		t.Fatal(err)
	}
	if err := ApplyProfile(root, "plain"); err != nil {
		t.Fatal(err)
	}
	if got := LoadProject(root).Routing.Mode; got != RoutingAutoRole {
		t.Fatalf("plain profile changed the mode to %q", got)
	}
	if err := ApplyProfile(root, "fast"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Routing.EffectiveMode() != RoutingAutoConnected || cfg.Routing.ApproveUnattended() {
		t.Fatalf("applied routing = %+v", cfg.Routing)
	}
}
