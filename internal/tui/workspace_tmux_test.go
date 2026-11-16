package tui

import "testing"

func TestDetachHintText(t *testing.T) {
	cases := map[string]string{"C-b": "Ctrl+b d", "C-a": "Ctrl+a d", "M-x": "Alt+x d", "F12": "F12 d"}
	for in, want := range cases {
		if got := detachHintText(in); got != want {
			t.Errorf("detachHintText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetachHintsOffOutsideTmux(t *testing.T) {
	t.Setenv("DECSR_TMUX", "")
	in := []keyHint{{"a", "b"}}
	if got := detachHints(in); len(got) != 1 {
		t.Errorf("hints changed outside a detached session: %v", got)
	}
}
