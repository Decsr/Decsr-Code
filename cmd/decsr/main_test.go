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
