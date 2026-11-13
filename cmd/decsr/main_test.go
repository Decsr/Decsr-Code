package main

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/tui"
)

func TestResolveUIMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "tui"},
		{"auto", "tui"},
		{"tui", "tui"},
		{"plain", "plain"},
		{"tmux", "tui"},
		{"cmux", "tui"},
		{"cmux-internal", "tui"},
		{"unknown", "tui"},
	}
	for _, tt := range tests {
		if got := resolveUIMode(tt.input); got != tt.want {
			t.Errorf("resolveUIMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReturnsToWorkspace(t *testing.T) {
	tests := []struct {
		name string
		in   tui.StartupResult
		want bool
	}{
		{"workspace", tui.StartupResult{Workspace: true}, true},
		{"quit", tui.StartupResult{}, false},
		{"classic chat", tui.StartupResult{ChatMode: true}, false},
		{"resume", tui.StartupResult{Resume: true}, false},
		{"review", tui.StartupResult{ReviewProject: true}, false},
		{"requirements", tui.StartupResult{ReqPath: "r.md"}, false},
		{"workspace plus chat", tui.StartupResult{Workspace: true, ChatMode: true}, false},
	}
	for _, tt := range tests {
		if got := returnsToWorkspace(tt.in); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestWorkspaceNext(t *testing.T) {
	tests := []struct {
		name     string
		in       tui.WorkspaceResult
		wantStep workspaceStep
		wantPath string
	}{
		{"empty result quits", tui.WorkspaceResult{}, stepQuit, ""},
		{"menu", tui.WorkspaceResult{OpenMenu: true}, stepMenu, ""},
		{"switch", tui.WorkspaceResult{SwitchProject: "/p"}, stepSwitch, "/p"},
		{"switch beats menu", tui.WorkspaceResult{SwitchProject: "/p", OpenMenu: true}, stepSwitch, "/p"},
	}
	for _, tt := range tests {
		step, path := workspaceNext(tt.in)
		if step != tt.wantStep || path != tt.wantPath {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", tt.name, step, path, tt.wantStep, tt.wantPath)
		}
	}
}

func TestSubcommandWorkspaceOptions(t *testing.T) {
	if opts, ok := subcommandWorkspaceOptions("resume"); !ok || !opts.Resume {
		t.Errorf("resume: got (%+v, %v), want Resume=true", opts, ok)
	}
	for _, sub := range []string{"run", "task", "report", ""} {
		if _, ok := subcommandWorkspaceOptions(sub); ok {
			t.Errorf("%q must not be a workspace entry", sub)
		}
	}
}
