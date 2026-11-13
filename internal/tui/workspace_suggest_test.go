package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsSuggestGraph builds n work nodes plus CHECK, all with status st.
func wsSuggestGraph(n int, st workspace.Status) *workspace.Graph {
	g := &workspace.Graph{}
	var deps []string
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("T%d", i)
		g.Nodes = append(g.Nodes, &workspace.Node{ID: id, Title: id, Kind: workspace.KindWork, Status: st})
		deps = append(deps, id)
	}
	g.Nodes = append(g.Nodes, &workspace.Node{ID: "CHECK", Title: "Build & test", Kind: workspace.KindCheck, Status: st, DependsOn: deps})
	return g
}

func TestWorkspaceSuggestionRules(t *testing.T) {
	t.Run("failed check without fix", func(t *testing.T) {
		h := newWSHarness(t)
		h.m.g = wsSuggestGraph(2, workspace.StatusDone)
		h.m.g.Node("CHECK").Status = workspace.StatusFailed
		if got := h.m.suggestion(); got != "fix the failing build and tests" {
			t.Fatalf("got %q", got)
		}
		h.m.g.Node("CHECK").DependsOn = append(h.m.g.Node("CHECK").DependsOn, "F1")
		h.m.g.Nodes = append(h.m.g.Nodes, &workspace.Node{ID: "F1", Kind: workspace.KindWork, Status: workspace.StatusTodo})
		if got := h.m.suggestion(); got == "fix the failing build and tests" {
			t.Fatal("an open fix node exists, rule 1 must not match")
		}
		h.m.g.Node("F1").Status = workspace.StatusDone
		if got := h.m.suggestion(); got != "fix the failing build and tests" {
			t.Fatalf("check failed again after F1 done, got %q", got)
		}
	})
	t.Run("all done", func(t *testing.T) {
		h := newWSHarness(t)
		h.m.g = wsSuggestGraph(2, workspace.StatusDone)
		if got := h.m.suggestion(); got != "review the changes and add any missing tests" {
			t.Fatalf("got %q", got)
		}
		h.m.g = wsSuggestGraph(5, workspace.StatusDone) // 6 done nodes with CHECK
		if got := h.m.suggestion(); got != "/compact" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("not all done", func(t *testing.T) {
		h := newWSHarness(t)
		h.m.g = wsSuggestGraph(2, workspace.StatusDone)
		h.m.g.Node("T1").Status = workspace.StatusTodo
		if got := h.m.suggestion(); got != "" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("fresh session with saved sessions", func(t *testing.T) {
		h := newWSHarness(t)
		if got := h.m.suggestion(); got != "" {
			t.Fatalf("got %q", got)
		}
		h.m.nSaved = 2
		if got := h.m.suggestion(); got != "/resume" {
			t.Fatalf("got %q", got)
		}
		h.m.sess.addLine("you", "hello")
		if got := h.m.suggestion(); got != "" {
			t.Fatalf("session has content, got %q", got)
		}
	})
}

func TestWorkspaceSuggestionHiddenWhen(t *testing.T) {
	h := newWSHarness(t)
	h.m.nSaved = 1
	if h.m.suggestion() != "/resume" {
		t.Fatal("setup")
	}
	h.m.input.SetValue("x")
	if h.m.suggestion() != "" {
		t.Fatal("input not empty")
	}
	h.m.input.Reset()
	h.m.splitting = true
	if h.m.suggestion() != "" {
		t.Fatal("splitting")
	}
	h.m.splitting = false
	h.m.overlay = wsOverlayConfirm
	if h.m.suggestion() != "" {
		t.Fatal("overlay")
	}
	h.m.overlay = wsOverlayNone
	h.m.showHelp = true
	if h.m.suggestion() != "" {
		t.Fatal("help")
	}
	h.m.showHelp = false
	h.m.focus = wsFocusGraph
	if h.m.suggestion() != "" {
		t.Fatal("graph focus")
	}
}

func TestWorkspaceSuggestionNotWhileRunning(t *testing.T) {
	h := newWSHarness(t)
	h.m.nSaved = 1
	h.toGraph()
	h.m.sel = "T1"
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return m.running() > 0 })
	h.m.setFocus(wsFocusPrompt)
	if h.m.suggestion() != "" {
		t.Fatal("suggestion while a node runs")
	}
	h.exec.release("T1", nil)
}

func TestWorkspaceSuggestionRenderAcceptDismiss(t *testing.T) {
	h := newWSHarness(t)
	h.m.nSaved = 1
	h.want("/resume")
	h.want("→ accept suggestion")
	h.wantNot("Type a task or / for commands")

	// Right accepts into the input without sending it.
	h.key("right")
	if got := h.m.input.Value(); got != "/resume" {
		t.Fatalf("input = %q", got)
	}
	h.want("Enter run")
	h.wantNot("→ accept suggestion")

	// Clearing the input does not bring the same suggestion back.
	h.m.input.Reset()
	if h.m.suggestion() != "" {
		t.Fatal("suggestion came back right after accepting")
	}

	// Esc dismisses; a state change shows it again.
	h2 := newWSHarness(t)
	h2.m.nSaved = 1
	h2.key("esc")
	h2.wantNot("→ accept suggestion")
	h2.want("Type a task or / for commands")
	h2.key("right")
	if h2.m.input.Value() != "" {
		t.Fatal("Right accepted a dismissed suggestion")
	}
	h2.m.nSaved = 2
	h2.want("/resume")
}

func TestWorkspaceRightKeepsCursorWhenNoSuggestion(t *testing.T) {
	h := newWSHarness(t)
	h.m.nSaved = 1
	h.m.input.SetValue("abc")
	h.key("left", "right")
	if h.m.input.Value() != "abc" {
		t.Fatalf("input = %q", h.m.input.Value())
	}
}

func TestWorkspaceGhostTextIsItalic(t *testing.T) {
	withColor(t)
	h := newWSHarness(t)
	h.m.nSaved = 1
	if h.m.suggestion() != "/resume" {
		t.Fatal("setup")
	}
	if got := strings.Join(h.m.promptBox(), "\n"); !strings.Contains(got, ";3;") && !strings.Contains(got, "[3;") && !strings.Contains(got, "[3m") {
		t.Errorf("ghost text is not italic: %q", got)
	}
}
