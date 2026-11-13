package tui

import (
	"fmt"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	wsSuggestFix     = "fix the failing build and tests"
	wsSuggestReview  = "review the changes and add any missing tests"
	wsSuggestCompact = "/compact"
	wsSuggestResume  = "/resume"

	// wsSuggestCompactAt is the number of finished nodes from which /compact
	// is suggested instead of a review.
	wsSuggestCompactAt = 6
)

// suggestion is the next prompt worth typing, from the graph and session
// state alone ("" for none). It is shown as ghost text in the empty prompt.
func (m workspaceModel) suggestion() string {
	if m.screen != wsScreenMain || m.focus != wsFocusPrompt || m.overlay != wsOverlayNone ||
		m.showHelp || m.splitting || m.running() > 0 || m.input.Value() != "" {
		return ""
	}
	text := m.suggestionText()
	if text == "" || m.sugDismissed == m.suggestionKey(text) {
		return ""
	}
	return text
}

func (m workspaceModel) suggestionText() string {
	g := m.g
	if g != nil && len(g.Nodes) > 0 {
		if c := g.Node("CHECK"); c != nil && c.Status == workspace.StatusFailed && !hasOpenFixNode(g, c) {
			return wsSuggestFix
		}
		if _, _, done, _ := g.Counts(); done == len(g.Nodes) {
			if done >= wsSuggestCompactAt {
				return wsSuggestCompact
			}
			return wsSuggestReview
		}
		return ""
	}
	if m.nSaved > 0 && !m.sess.hasContent() {
		return wsSuggestResume
	}
	return ""
}

// hasOpenFixNode is true when check depends on a fix node (F<n>) that is not
// done yet; a finished fix followed by a failed check calls for another one.
func hasOpenFixNode(g *workspace.Graph, check *workspace.Node) bool {
	for _, id := range check.DependsOn {
		if n := g.Node(id); n != nil && strings.HasPrefix(id, "F") && n.Status != workspace.StatusDone {
			return true
		}
	}
	return false
}

// suggestionKey identifies the state a suggestion belongs to, so a dismissed
// or accepted one stays hidden until the state changes.
func (m workspaceModel) suggestionKey(text string) string {
	todo, running, done, failed := m.g.Counts()
	return fmt.Sprintf("%s|%d|%d,%d,%d,%d|%d", text, len(m.g.Nodes), todo, running, done, failed, m.nSaved)
}

// dismissSuggestion hides the current suggestion until the state changes.
func (m *workspaceModel) dismissSuggestion(text string) {
	m.sugDismissed = m.suggestionKey(text)
}
