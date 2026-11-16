package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// launcherHome builds a Home over a valid project with fake recent projects
// and sessions, sized w x h.
func launcherHome(t *testing.T, w, h int) HomeModel {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestConfig()
	cfg.Workspace.BudgetUSD = 5
	m := NewHomeModel(cfg, root)
	m.width, m.height = w, h
	m.syncViewport()
	other := t.TempDir()
	m.recentProjects = []RecentProject{
		{Path: root, Name: "decsr_code"},
		{Path: other, Name: "ar_coating"},
		{Path: t.TempDir(), Name: "decsr"},
	}
	now := time.Now()
	m.sessions = []workspace.SessionInfo{
		{ID: "s1", Title: "Add json output and a long title that has to be cut somewhere", UpdatedAt: now, Todo: 3, Done: 2, Waiting: 1},
		{ID: "s2", Title: "Refactor config", UpdatedAt: now.Add(-48 * time.Hour), Done: 5},
		{ID: "s3", Title: "Fix flaky CI", UpdatedAt: now.Add(-5 * 24 * time.Hour), Todo: 3},
	}
	return m
}

var capsWord = regexp.MustCompile(`\b[A-Z]{4,}\b`)

func TestLauncherRendering(t *testing.T) {
	for _, sz := range [][2]int{{80, 24}, {120, 34}, {160, 46}, {60, 20}} {
		w, h := sz[0], sz[1]
		m := launcherHome(t, w, h)
		out := m.View()
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Errorf("%dx%d: %d lines, want %d", w, h, len(lines), h)
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > w {
				t.Errorf("%dx%d: line is %d wide: %q", w, h, ansi.StringWidth(l), stripANSI(l))
			}
		}
		plain := stripANSI(out)
		for _, bad := range []string{"┐┌", "││", "┘└"} {
			if strings.Contains(plain, bad) {
				t.Errorf("%dx%d: double border %q", w, h, bad)
			}
		}
		if caps := capsWord.FindString(plain); caps != "" {
			t.Errorf("%dx%d: ALL-CAPS label %q", w, h, caps)
		}
		if w < 60 {
			continue
		}
		for _, want := range []string{"decsr · test-project", "? help", "Start", "Workspace", "Classic pipeline (frozen)"} {
			if !strings.Contains(plain, want) {
				t.Errorf("%dx%d: missing %q", w, h, want)
			}
		}
	}
}

func TestLauncherWideShowsAllSections(t *testing.T) {
	plain := stripANSI(launcherHome(t, 120, 34).View())
	for _, want := range []string{"Projects", "Sessions · test-project", "Project", "language", "lead", "autonomy",
		"Settings", "More", "Open project…", "Global settings", "Project setup", "Connect a provider", "Model memory",
		"Reset artifacts", "Quit", "decsr_code", "ar_coating", "Refactor config", "2/5 · 1 waiting", "5/5 · 2d",
		"codex · gpt-5.4", "1 manual · budget $5.00", "go"} {
		if !strings.Contains(plain, want) {
			t.Errorf("launcher missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "│") {
		t.Error("wide layout needs the column divider")
	}
}

func TestLauncherNarrowStacksActionsFirst(t *testing.T) {
	plain := stripANSI(launcherHome(t, 80, 60).View())
	start, projects := strings.Index(plain, "Start"), strings.Index(plain, "Projects")
	if start < 0 || projects < 0 || start > projects {
		t.Fatalf("actions should come before projects when stacked:\n%s", plain)
	}
	if strings.Contains(plain, "│") {
		t.Error("stacked layout has no column divider")
	}
}

func TestLauncherStackedScrollKeepsSelectionVisible(t *testing.T) {
	m := launcherHome(t, 80, 24)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	plain := stripANSI(m.View())
	if !strings.Contains(plain, "Fix flaky CI") {
		t.Fatalf("selected last session scrolled out of view:\n%s", plain)
	}
	if n := len(strings.Split(plain, "\n")); n != 24 {
		t.Fatalf("%d lines", n)
	}
}

func TestLauncherTabUpDownEnter(t *testing.T) {
	m := launcherHome(t, 120, 34)
	if m.col != homeColRight {
		t.Fatal("actions column should have focus first")
	}
	// Enter on the first action opens the Workspace.
	_, cmd := m.Update(homeKey("enter"))
	if msg := cmd().(homeSelectedMsg); msg.action != homeActionWorkspace || msg.session != "" {
		t.Fatalf("msg = %+v", msg)
	}
	// Down moves in the actions column.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 || m.leftCur != 0 {
		t.Fatalf("cursor=%d left=%d", m.cursor, m.leftCur)
	}

	// Tab moves to projects; the first row is the current project.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.col != homeColLeft {
		t.Fatal("tab should focus the left column")
	}
	_, cmd = m.Update(homeKey("enter"))
	if msg := cmd().(homeSelectedMsg); msg.action != homeActionWorkspace {
		t.Fatalf("current project should open the workspace, got %+v", msg)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(homeKey("enter"))
	msg := cmd().(homeSelectedMsg)
	if msg.action != homeActionSwitchProject || msg.path != m.recentProjects[1].Path {
		t.Fatalf("msg = %+v", msg)
	}

	// Down past the projects reaches the sessions.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.Update(homeKey("enter"))
	msg = cmd().(homeSelectedMsg)
	if msg.action != homeActionWorkspace || msg.session != "s1" {
		t.Fatalf("msg = %+v", msg)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	_, cmd = m.Update(homeKey("enter"))
	if msg = cmd().(homeSelectedMsg); msg.session != "s3" {
		t.Fatalf("end should select the last session: %+v", msg)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.leftCur != len(m.leftRows())-1 {
		t.Fatalf("down must stop at the last row, got %d", m.leftCur)
	}

	// Tab and Shift+Tab come back; the left selection is kept.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.col != homeColRight || m.leftCur == 0 {
		t.Fatalf("col=%d leftCur=%d", m.col, m.leftCur)
	}
}

func TestLauncherMissingProjectIsNotSwitchedTo(t *testing.T) {
	m := launcherHome(t, 120, 34)
	m.recentProjects[1].Path = filepath.Join(t.TempDir(), "gone")
	m.col, m.leftCur = homeColLeft, 1
	m, cmd := m.Update(homeKey("enter"))
	if cmd != nil || !strings.Contains(m.status, "not found") {
		t.Fatalf("cmd=%v status=%q", cmd, m.status)
	}
}

func TestLauncherActionKeysDispatch(t *testing.T) {
	cases := map[string]homeAction{
		"w": homeActionWorkspace, "o": homeActionOpenProject, "g": homeActionGlobalSettings,
		"s": homeActionSetup, "m": homeActionModelMemory, "1": homeActionNewTask, "c": homeActionClean,
		"2": homeActionOpenProject, "3": homeActionGlobalSettings, "4": homeActionSetup, "5": homeActionClean,
	}
	for key, want := range cases {
		for _, col := range []int{homeColLeft, homeColRight} {
			m := launcherHome(t, 120, 34)
			m.col = col
			_, cmd := m.Update(homeKey(key))
			if cmd == nil {
				t.Fatalf("%q (col %d): no command", key, col)
			}
			if got := cmd().(homeSelectedMsg).action; got != want {
				t.Errorf("%q (col %d): action %d, want %d", key, col, got, want)
			}
		}
	}
	for _, key := range []string{"q", "6"} {
		m, _ := launcherHome(t, 120, 34).Update(homeKey(key))
		if !m.confirmQuit {
			t.Errorf("%q should ask to quit", key)
		}
		if v := stripANSI(m.View()); !strings.Contains(v, "Quit decsr?") {
			t.Errorf("%q: quit dialog not drawn:\n%s", key, v)
		}
	}
}

func TestLauncherEnterRunsEveryAction(t *testing.T) {
	m := launcherHome(t, 120, 34)
	seen := map[homeAction]bool{}
	for i := range m.items {
		mm := m
		mm.cursor = i
		mm, cmd := mm.Update(homeKey("enter"))
		switch {
		case mm.conn != nil:
			seen[homeActionConnect] = true
		case mm.confirmQuit:
			seen[homeActionQuit] = true
		case cmd != nil:
			seen[cmd().(homeSelectedMsg).action] = true
		default:
			t.Errorf("item %q did nothing on Enter", m.items[i].label)
		}
	}
	for _, it := range m.items {
		if !seen[it.action] {
			t.Errorf("%q: action %d never dispatched", it.label, it.action)
		}
	}
}

func TestLauncherClassicPipelineIsFrozen(t *testing.T) {
	m := launcherHome(t, 120, 34)
	var label string
	for _, it := range m.items {
		if it.action == homeActionNewTask {
			label = it.label
		}
	}
	if label != "Classic pipeline (frozen)" {
		t.Fatalf("label = %q", label)
	}
}

func TestLauncherConnectOverlay(t *testing.T) {
	m := launcherHome(t, 120, 34)
	m, _ = m.Update(homeKey("k"))
	if m.conn == nil {
		t.Fatal("k should open the connect overlay")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "Connect a provider") {
		t.Fatalf("overlay not drawn:\n%s", v)
	}
	// Keys go to the overlay, not to the launcher.
	m, _ = m.Update(homeKey("q"))
	if m.confirmQuit {
		t.Fatal("q must not reach the launcher while the overlay is open")
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should close the overlay")
	}
	m, _ = m.Update(cmd())
	if m.conn != nil {
		t.Fatal("overlay should be gone")
	}
	m, _ = m.Update(connectSavedMsg{name: "deepseek"})
	if !strings.Contains(m.status, "deepseek") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestLauncherNoProject(t *testing.T) {
	m := NewHomeModel(newTestConfig(), t.TempDir())
	m.width, m.height = 120, 34
	m.recentProjects = nil
	plain := stripANSI(m.View())
	for _, want := range []string{"decsr · no project", "no project here", "no recent projects", "open a project first"} {
		if !strings.Contains(plain, want) {
			t.Errorf("missing %q:\n%s", want, plain)
		}
	}
	_, cmd := m.Update(homeKey("w"))
	if msg := cmd().(homeSelectedMsg); msg.action != homeActionOpenProject {
		t.Fatalf("w without a project should open the picker, got %+v", msg)
	}
}

func TestLauncherNeverOverflows(t *testing.T) {
	for w := 1; w <= 130; w += 7 {
		for _, h := range []int{1, 4, 10, 30} {
			m := launcherHome(t, w, h)
			for _, v := range []string{m.View(), func() string { m.showHelp = true; return m.View() }()} {
				for _, l := range strings.Split(v, "\n") {
					if ansi.StringWidth(l) > w {
						t.Fatalf("%dx%d: line is %d wide: %q", w, h, ansi.StringWidth(l), stripANSI(l))
					}
				}
			}
		}
	}
}

func TestSessionKindAndMeta(t *testing.T) {
	now := time.Now()
	cases := []struct {
		s    workspace.SessionInfo
		kind string
		meta string
	}{
		{workspace.SessionInfo{Todo: 2, Done: 2, Waiting: 1, UpdatedAt: now}, "waiting", "2/4 · 1 waiting"},
		{workspace.SessionInfo{Done: 5, UpdatedAt: now.Add(-48 * time.Hour)}, "done", "5/5 · 2d"},
		{workspace.SessionInfo{Todo: 3, UpdatedAt: now.Add(-5 * 24 * time.Hour)}, "todo", "0/3 · 5d"},
		{workspace.SessionInfo{Done: 1, Failed: 1, UpdatedAt: now}, "failed", "1/2 · 1 failed"},
		{workspace.SessionInfo{Running: 1, UpdatedAt: now}, "running", "0/1 · 1 running"},
		{workspace.SessionInfo{UpdatedAt: now.Add(-3 * time.Hour)}, "todo", "3h"},
	}
	for _, c := range cases {
		if got := sessionKind(c.s); got != c.kind {
			t.Errorf("%+v: kind %q, want %q", c.s, got, c.kind)
		}
		if got := sessionMeta(c.s, now); got != c.meta {
			t.Errorf("%+v: meta %q, want %q", c.s, got, c.meta)
		}
	}
}

func TestLauncherReadsSessionsOfTheProject(t *testing.T) {
	root := t.TempDir()
	_ = createProjectMarkers(root)
	s := workspace.NewSession()
	s.Title = "saved one"
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	m := NewHomeModel(config.Config{}, root)
	if len(m.sessions) != 1 || m.sessions[0].Title != "saved one" {
		t.Fatalf("sessions = %+v", m.sessions)
	}
}

func TestStartupHomeSwitchesProjectAndOpensSession(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { _ = RemoveRecentProject(root) })
	m := newStartupModel(root, filepath.Join(root, ".decsr", "requirements.md"), newTestConfig())
	updated, cmd := m.Update(homeSelectedMsg{action: homeActionWorkspace, session: "s9"})
	s := updated.(startupModel)
	if cmd == nil || !s.workspace || s.workspaceSession != "s9" {
		t.Fatalf("workspace=%v session=%q", s.workspace, s.workspaceSession)
	}
}

// TestLauncherSamples logs plain renders of the restyled screens
// (go test -v -run TestLauncherSamples).
func TestLauncherSamples(t *testing.T) {
	t.Logf("launcher 120x34\n%s", stripANSI(launcherHome(t, 120, 34).View()))
	t.Logf("launcher 80x24\n%s", stripANSI(launcherHome(t, 80, 24).View()))
	root := t.TempDir()
	for _, n := range []string{"alpha", "beta", "gamma"} {
		_ = os.MkdirAll(filepath.Join(root, n), 0o755)
	}
	_ = os.MkdirAll(filepath.Join(root, "alpha", ".git"), 0o755)
	p := NewProjectPicker(root)
	p.projects = []RecentProject{{Path: root, Name: "decsr_code"}, {Path: filepath.Join(root, "alpha"), Name: "alpha"}}
	p.width, p.height = 100, 30
	t.Logf("project picker 100x30\n%s", stripANSI(p.View()))
	d := DirBrowserModel{cwd: root, width: 100, height: 30}
	_ = d.reload()
	t.Logf("directory browser 100x30\n%s", stripANSI(d.View()))
	t.Logf("help 100x30\n%s", stripANSI(renderHelp(100, 30, "Keyboard — home", homeHelp())))
}

func TestPickerAndHelpRendering(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "proj", ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "plain"), 0o755)
	p := NewProjectPicker(root)
	p.projects = []RecentProject{{Path: filepath.Join(root, "proj"), Name: "proj"}, {Path: filepath.Join(root, "gone"), Name: "gone"}}
	d := DirBrowserModel{cwd: root}
	if err := d.reload(); err != nil {
		t.Fatal(err)
	}
	for _, sz := range [][2]int{{100, 30}, {60, 12}, {30, 8}} {
		w, h := sz[0], sz[1]
		p.width, p.height = w, h
		d.SetSize(w, h)
		views := map[string]string{
			"picker": p.View(), "browser": d.View(),
			"help": renderHelp(w, h, "Keyboard — home", homeHelp()),
		}
		for name, v := range views {
			lines := strings.Split(v, "\n")
			if len(lines) > h {
				t.Errorf("%s %dx%d: %d lines", name, w, h, len(lines))
			}
			for _, l := range lines {
				if ansi.StringWidth(l) > w {
					t.Errorf("%s %dx%d: line is %d wide", name, w, h, ansi.StringWidth(l))
				}
			}
			plain := stripANSI(v)
			if strings.Contains(plain, "││") || strings.Contains(plain, "┐┌") {
				t.Errorf("%s %dx%d: double border", name, w, h)
			}
			if caps := capsWord.FindString(plain); caps != "" {
				t.Errorf("%s %dx%d: ALL-CAPS label %q", name, w, h, caps)
			}
		}
	}
	p.width, p.height = 100, 30
	plain := stripANSI(p.View())
	for _, want := range []string{"Start", "Use current directory", "Open project…", "Recent projects", "proj", "not found", "Enter select"} {
		if !strings.Contains(plain, want) {
			t.Errorf("picker missing %q:\n%s", want, plain)
		}
	}
	d.SetSize(100, 30)
	plain = stripANSI(d.View())
	for _, want := range []string{"Directories", "◆ proj/", "project", "▪ plain/", "Esc cancel"} {
		if !strings.Contains(plain, want) {
			t.Errorf("browser missing %q:\n%s", want, plain)
		}
	}
	plain = stripANSI(renderHelp(100, 30, "Keyboard — home", homeHelp()))
	for _, want := range []string{"Keyboard — home", "Navigate", "Settings", "connect a provider", "classic pipeline (frozen)", "close"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help missing %q:\n%s", want, plain)
		}
	}
}
