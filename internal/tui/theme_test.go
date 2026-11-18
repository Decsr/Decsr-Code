package tui

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

func TestThemeIsGalaxyBlue(t *testing.T) {
	checks := []struct{ name, got, want string }{
		{"bg", string(crt.bg), "#0b1426"},
		{"border", string(crt.border), "#22335a"},
		{"bright", string(crt.bright), "#e8f1ff"},
		{"focusBorder", string(focusBorder), "#4a86ff"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
}

func TestRoleColorFollowsTheSharedRoles(t *testing.T) {
	for _, r := range []string{"pm", "coder", "coder_fixer", "reviewer", "qa", "security", "ux_reviewer", "pr"} {
		if got, want := roleColor(r), ui.Dark(ui.RoleColor(r)); got != want {
			t.Errorf("roleColor(%q) = %v, want %v", r, got, want)
		}
	}
	for old, now := range map[string]string{"architect": "pm", "planner": "pm", "tester": "qa"} {
		if roleColor(old) != roleColor(now) {
			t.Errorf("deprecated role %q does not map to %q", old, now)
		}
	}
	if roleColor("system") != crt.dim {
		t.Error("system is not dim")
	}
}
