package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
)

func homeKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// homeWithPlan builds a Home model over a project whose workspace holds a plan.
func homeWithPlan(t *testing.T, withPlan bool) (HomeModel, string) {
	t.Helper()
	root := t.TempDir()
	_ = createProjectMarkers(root)
	ws := filepath.Join(root, artifacts.DirName)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(ws, artifacts.ImplementationPlanFile)
	if withPlan {
		if err := os.WriteFile(plan, []byte("# plan"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewHomeModel(newTestConfig(), root)
	m.width, m.height = 120, 40
	m.syncViewport()
	return m, plan
}

func cursorOn(m *HomeModel, a homeAction) {
	for i, it := range m.items {
		if it.action == a {
			m.cursor = i
		}
	}
}

func TestHomeNewTask_NoArtifactsNoDialog(t *testing.T) {
	m, _ := homeWithPlan(t, false)
	cursorOn(&m, homeActionNewTask)
	m, cmd := m.Update(homeKey("enter"))
	if m.confirmAction != nil {
		t.Fatal("no dialog expected when nothing would be lost")
	}
	if cmd == nil {
		t.Fatal("expected action command")
	}
	if msg, ok := cmd().(homeSelectedMsg); !ok || msg.action != homeActionNewTask {
		t.Fatalf("unexpected msg %#v", msg)
	}
}

func TestHomeNewTask_ConfirmCancelKeepsArtifacts(t *testing.T) {
	for _, k := range []string{"n", "esc"} {
		m, plan := homeWithPlan(t, true)
		cursorOn(&m, homeActionNewTask)
		m, cmd := m.Update(homeKey("enter"))
		if cmd != nil || m.confirmAction == nil {
			t.Fatalf("%s: expected dialog and no command", k)
		}
		if v := m.viewport.View(); !strings.Contains(v, "Start a new task?") || !strings.Contains(v, "any other key to cancel") {
			t.Fatalf("dialog not rendered:\n%s", v)
		}
		m, cmd = m.Update(homeKey(k))
		if cmd != nil || m.confirmAction != nil {
			t.Fatalf("%s: expected cancel", k)
		}
		if _, err := os.Stat(plan); err != nil {
			t.Fatalf("%s: plan must survive: %v", k, err)
		}
	}
}

func TestHomeNewTask_ConfirmYesProceedsAndDeletes(t *testing.T) {
	for _, k := range []string{"y", "enter"} {
		m, plan := homeWithPlan(t, true)
		cursorOn(&m, homeActionNewTask)
		m, _ = m.Update(homeKey("enter"))
		m, cmd := m.Update(homeKey(k))
		if cmd == nil {
			t.Fatalf("%s: expected action", k)
		}
		msg, ok := cmd().(homeSelectedMsg)
		if !ok || msg.action != homeActionNewTask {
			t.Fatalf("%s: unexpected msg", k)
		}
		cfg := newTestConfig()
		cfg.Project.Language = "python"
		su := newStartupModel(m.root, filepath.Join(m.root, ".decsr", "requirements.md"), cfg)
		t.Cleanup(func() { _ = RemoveRecentProject(m.root) })
		su.Update(msg)
		if _, err := os.Stat(plan); !os.IsNotExist(err) {
			t.Fatalf("%s: plan should be deleted", k)
		}
	}
}

func TestHomeReset_ConfirmThenStatus(t *testing.T) {
	m, plan := homeWithPlan(t, true)
	m, cmd := m.Update(homeKey("c"))
	if cmd != nil || m.confirmAction == nil {
		t.Fatal("expected reset dialog")
	}
	if v := m.viewport.View(); !strings.Contains(v, "Reset artifacts?") {
		t.Fatalf("dialog not rendered:\n%s", v)
	}
	m, _ = m.Update(homeKey("n"))
	if _, err := os.Stat(plan); err != nil {
		t.Fatal("cancel must keep plan")
	}
	m, _ = m.Update(homeKey("c"))
	m, cmd = m.Update(homeKey("y"))
	if cmd == nil {
		t.Fatal("expected clean action")
	}
	msg := cmd().(homeSelectedMsg)
	su := newStartupModel(m.root, filepath.Join(m.root, ".decsr", "requirements.md"), newTestConfig())
	t.Cleanup(func() { _ = RemoveRecentProject(m.root) })
	updated, _ := su.Update(msg)
	got := updated.(startupModel)
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatal("plan should be deleted")
	}
	if !strings.Contains(got.home.status, "artifacts reset") {
		t.Fatalf("status = %q", got.home.status)
	}
	if !strings.Contains(got.home.View(), "artifacts reset") {
		t.Fatal("status not rendered")
	}
	h, _ := got.home.Update(homeKey("j"))
	if h.status != "" {
		t.Fatal("status should clear on next key")
	}
}

func TestHomeResumeShortcut(t *testing.T) {
	m, _ := homeWithPlan(t, true)
	if !strings.Contains(m.renderFooter(), "resume") {
		t.Fatal("footer should advertise r resume")
	}
	_, cmd := m.Update(homeKey("r"))
	if cmd == nil {
		t.Fatal("r should trigger resume")
	}
	if msg := cmd().(homeSelectedMsg); msg.action != homeActionResumeProject {
		t.Fatalf("action = %v", msg.action)
	}

	m, _ = homeWithPlan(t, false)
	if strings.Contains(m.renderFooter(), "resume") {
		t.Fatal("footer must not show r when nothing to resume")
	}
	if _, cmd := m.Update(homeKey("r")); cmd != nil {
		t.Fatal("r should be ignored with no resumable item")
	}
}

func TestHomeHomeEndKeys(t *testing.T) {
	m, _ := homeWithPlan(t, false)
	m, _ = m.Update(homeKey("end"))
	if m.cursor != len(m.items)-1 {
		t.Fatalf("end: cursor %d", m.cursor)
	}
	m, _ = m.Update(homeKey("home"))
	if m.cursor != 0 {
		t.Fatalf("home: cursor %d", m.cursor)
	}
}

func TestHomeNewTask_MemoryOnlyNoDialog(t *testing.T) {
	m, _ := homeWithPlan(t, false)
	mem := filepath.Join(m.root, artifacts.DirName, artifacts.MemoryDirName)
	if err := os.MkdirAll(mem, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mem, "2026-01-01-fact.md"), []byte("fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	m = NewHomeModel(newTestConfig(), m.root)
	m.width, m.height = 120, 40
	m.syncViewport()
	cursorOn(&m, homeActionNewTask)
	m, cmd := m.Update(homeKey("enter"))
	if m.confirmAction != nil || cmd == nil {
		t.Fatal("memory alone must not trigger the dialog")
	}
}

func TestHomeNewTask_DialogByArtifactKind(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"task_plan", map[string]string{"task_plan.md": "x"}, true},
		{"research_report", map[string]string{"research_report.md": "x"}, true},
		{"memory only", map[string]string{"memory/a.md": "x"}, false},
		{"config and logs", map[string]string{artifacts.ProjectConfigFile: "p: 1", "decsr.log": "x", "runlog.jsonl": "x"}, false},
	}
	for _, tc := range cases {
		m, _ := homeWithPlan(t, false)
		for name, body := range tc.files {
			p := filepath.Join(m.wsPath, name)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		m = NewHomeModel(newTestConfig(), m.root)
		m.width, m.height = 120, 40
		m.syncViewport()
		cursorOn(&m, homeActionNewTask)
		m, _ = m.Update(homeKey("enter"))
		if got := m.confirmAction != nil; got != tc.want {
			t.Errorf("%s: dialog=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestWouldCleanMatchesCleanWorkspace(t *testing.T) {
	cases := []map[string]string{
		{},
		{"task_plan.md": "x"},
		{"research_report.md": "x"},
		{"sub_tasks.json": "[]", "task_spec.json": "{}"},
		{"memory/a.md": "x"},
		{artifacts.MemoryDBFile: "x", artifacts.MemoryDBFile + "-wal": "x"},
		{artifacts.ProjectConfigFile: "x", "decsr.log": "x", "runlog.jsonl": "x"},
		{artifacts.RequirementsFile: "x"},
		{artifacts.RequirementsFile: "x", "task_plan.md": "x"},
		{"sub/dir/file.txt": "x"},
	}
	for i, files := range cases {
		for _, preserve := range []bool{false, true} {
			ws := t.TempDir()
			for name, body := range files {
				p := filepath.Join(ws, name)
				_ = os.MkdirAll(filepath.Dir(p), 0o755)
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := countEntries(t, ws)
			would := wouldClean(ws, preserve)
			cleanWorkspace(ws, preserve)
			removed := countEntries(t, ws) < before
			if would != removed {
				t.Errorf("case %d preserve=%v: wouldClean=%v removed=%v", i, preserve, would, removed)
			}
		}
	}
}

func countEntries(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(dir, func(string, os.DirEntry, error) error { n++; return nil })
	return n
}
