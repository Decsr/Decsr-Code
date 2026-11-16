package ui

import "testing"

// TestRenderSample logs plain-text samples of a few components (go test -v).
func TestRenderSample(t *testing.T) {
	body := []string{"coder · claude:sonnet", "writes the parser"}
	t.Logf("\n%s", plain(Card(CardOpts{Title: "T1 Parse the flags", Meta: "▖ running", Body: body, Width: 60, Selected: true})))
	t.Logf("\n%s", plain(Card(CardOpts{Title: "T2 Build the output", Meta: "! failed", Body: body, Width: 60, Failed: true})))
	t.Logf("\n%s", plain(HintBar([]Hint{{"Enter", "send"}, {"Esc", "close"}, {"Tab", "focus"}, {"↑↓", "cards"}, {"?", "help"}}, 50)))
}
