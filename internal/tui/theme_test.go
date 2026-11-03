package tui

import "testing"

func TestThemeIsGalaxyBlue(t *testing.T) {
	checks := []struct{ name, got, want string }{
		{"bg", string(crt.bg), "#0b1426"},
		{"border", string(crt.border), "#22335a"},
		{"bright", string(crt.bright), "#e8f1ff"},
		{"focusBorder", string(focusBorder), "#4a86ff"},
		{"pm", string(roleColor("pm")), "#4a86ff"},
		{"coder", string(roleColor("coder")), "#5ce1e6"},
		{"qa", string(roleColor("qa")), "#7cb8ff"},
		{"ux_reviewer", string(roleColor("ux_reviewer")), "#bfe3ff"},
		{"security", string(roleColor("security")), "#ffd166"},
		{"coder_fixer", string(roleColor("coder_fixer")), "#3fb8c0"},
		{"pr", string(roleColor("pr")), "#9ee8ff"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %s, want %s", c.name, c.got, c.want)
		}
	}
}
