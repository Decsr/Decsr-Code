package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
)

func cockpitFixture(t *testing.T) (CockpitModel, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := `{"tasks":[
		{"key":"T1","title":"Add session model","description":"model","priority":2},
		{"key":"T2","title":"Wire login handler","description":"handler","priority":2,"depends_on":["T1"]},
		{"key":"T3","title":"Add auth middleware","description":"middleware","priority":2,"depends_on":["T2"]},
		{"key":"T4","title":"Integration tests","description":"tests","priority":2,"depends_on":["T2"]},
		{"key":"T5","title":"Update docs","description":"docs","priority":3,"depends_on":["T1","T3"]}
	],"id_by_key":{"T1":"b1","T2":"b2","T3":"b3","T4":"b4","T5":"b5"}}`
	if err := os.WriteFile(filepath.Join(dir, ".decsr", "sub_tasks.json"), []byte(plan), 0o600); err != nil {
		t.Fatal(err)
	}
	c := NewCockpit(config.Config{}, dir, filepath.Join(dir, ".decsr"))
	c.SetTaskInput("Add JWT auth to the login endpoint")
	c.SetStarted(time.Now().Add(-12 * time.Minute))
	return c, dir
}

func TestCockpitObservesWorkflow(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)

	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "pipeline: green"))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "planning"))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "decomposed into 5 sub-tasks"))
	c.LoadPlan()
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "── Sub-task 1/5 (b1): Add session model ──"))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 1200, OutputTokens: 300}))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgEvent, bus.TokenPayload{Text: "⚙ edit internal/auth/middleware.go +48 -12\n$ go test ./internal/auth/...\n"}))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "── Sub-task 2/5 (b2): Wire login handler ──"))
	c.Observe(bus.NewMessage(bus.RoleCoder, "", bus.MsgUsage, bus.AgentUsage{InputTokens: 2200, OutputTokens: 400}))
	c.Observe(bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, "all tests passed"))

	if len(c.subtasks) != 5 {
		t.Fatalf("subtasks = %d, want 5", len(c.subtasks))
	}
	if c.subtasks[0].status != "done" {
		t.Errorf("T1 status = %q, want done", c.subtasks[0].status)
	}
	if c.subtasks[1].status != "active" {
		t.Errorf("T2 status = %q, want active", c.subtasks[1].status)
	}
	if c.tests.status != "pass" {
		t.Errorf("tests = %q, want pass", c.tests.status)
	}
	if c.totalUsage.InputTokens != 3400 {
		t.Errorf("input tokens = %d, want 3400", c.totalUsage.InputTokens)
	}
	if c.subtasks[0].tokens == 0 || c.subtasks[1].tokens == 0 {
		t.Errorf("expected per-subtask tokens, got %d/%d", c.subtasks[0].tokens, c.subtasks[1].tokens)
	}

	view := c.View()
	for _, want := range []string{"WORKFLOW", "Add session model", "TOKENS", "TESTS", "T2"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestCockpitLoadPlanIsIdempotent(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.LoadPlan()
	c.LoadPlan()
	if len(c.subtasks) != 5 {
		t.Fatalf("subtasks = %d, want 5 after two loads", len(c.subtasks))
	}
}

func TestCockpitCompactView(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(80, 24)
	c.LoadPlan()
	view := c.View()
	if !strings.Contains(view, "WORKFLOW") {
		t.Fatalf("compact view missing workflow pane:\n%s", view)
	}
	if !strings.Contains(view, "tok ") {
		t.Fatalf("compact footer missing token summary:\n%s", view)
	}
}

func TestCockpitTabAndFocus(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 40)
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyTab})
	if c.focus != cockpitFocusCenter {
		t.Fatalf("focus = %v, want center", c.focus)
	}
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	if c.tab != 3 {
		t.Fatalf("tab = %d, want 3 (Tokens)", c.tab)
	}
}

func TestModelCockpitObservesBusMessages(t *testing.T) {
	c, root := cockpitFixture(t)
	_ = c
	m := New(nil, root, filepath.Join(root, ".decsr"), nil, config.Config{}).WithTaskInput("Add JWT auth to the login endpoint")
	m.width, m.height = 120, 40
	m.layout()
	m.cockpit.LoadPlan()

	for _, ev := range []string{
		"pipeline: green",
		"planning",
		"decomposed into 5 sub-tasks",
		"── Sub-task 1/5 (b1): Add session model ──",
	} {
		updated, _ := m.Update(BusMessageMsg{Msg: bus.NewMessage(bus.RoleSystem, "", bus.MsgEvent, ev)})
		m = updated.(Model)
	}
	m.cockpit.LoadPlan()
	view := m.View()
	for _, want := range []string{"WORKFLOW", "Add session model", "TOKENS", "TESTS"} {
		if !strings.Contains(view, want) {
			t.Fatalf("model view missing %q:\n%s", want, view)
		}
	}
}
