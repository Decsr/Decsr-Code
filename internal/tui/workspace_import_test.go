package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const importTasks = `# Tasks: Login

## Phase 1: Setup

- [x] T001 Create project structure
- [ ] T002 Initialize module
- [ ] T003 [P] Configure linting

## Phase 2: User Story 1

- [ ] T004 [P] [US1] Contract test in internal/auth/login_test.go
- [ ] T005 [US1] Implement login in internal/auth/login.go
`

func writeSpecFeature(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "specs", "003-login")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, s := range map[string]string{"tasks.md": importTasks, "spec.md": "# Feature Specification: Login\n"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func transcriptHas(h *wsHarness, sub string) bool {
	for _, l := range h.m.sess.snapshot().Transcript {
		if strings.Contains(l.Text, sub) {
			return true
		}
	}
	return false
}

func TestWorkspaceImportSpecKit(t *testing.T) {
	h := newWSHarness(t)
	writeSpecFeature(t, h.root)
	h.m.cfg.Agents = map[string]config.AgentConfig{"qa": {Description: "tests"}}
	h.command("/import speckit")
	if !transcriptHas(h, "Using the most recently modified tasks.md: specs/003-login/tasks.md") {
		t.Fatalf("no latest-file line: %+v", h.m.sess.snapshot().Transcript)
	}
	want := "Imported 4 tasks from specs/003-login/tasks.md (1 done task skipped, 2 phases). Assign roles with A, then R to run."
	if !transcriptHas(h, want) {
		t.Fatalf("no summary line: %+v", h.m.sess.snapshot().Transcript)
	}
	g := h.m.g
	if g.Title != "Login" {
		t.Errorf("title = %q", g.Title)
	}
	n1, n3 := g.Node("T1"), g.Node("T3")
	if n1 == nil || n3 == nil || !strings.HasPrefix(n1.Title, "T002 ") || n1.Role != "" {
		t.Fatalf("nodes = %+v", g.Nodes)
	}
	if n3.SuggestedRole != "qa" || n1.SuggestedRole != "coder" {
		t.Errorf("roles = %q %q", n1.SuggestedRole, n3.SuggestedRole)
	}
	if !strings.Contains(n1.Description, "Spec: specs/003-login/spec.md, plan: specs/003-login/plan.md") {
		t.Errorf("description = %q", n1.Description)
	}
	if c := g.Node("CHECK"); c == nil || len(c.DependsOn) != 4 {
		t.Errorf("check node = %+v", c)
	}
}

func TestWorkspaceImportPhaseAndAppend(t *testing.T) {
	h := newWSHarness(t)
	writeSpecFeature(t, h.root)
	h.toGraph()
	before := len(h.m.g.Nodes)
	h.command("/import speckit --phase 2 specs/003-login")
	if !transcriptHas(h, "Imported 2 tasks from specs/003-login/tasks.md (0 done tasks skipped, 1 phase)") {
		t.Fatalf("no summary: %+v", h.m.sess.snapshot().Transcript)
	}
	if got := len(h.m.g.Nodes); got != before+2 {
		t.Fatalf("nodes %d -> %d", before, got)
	}
	if transcriptHas(h, "most recently") {
		t.Error("explicit path must not say latest")
	}
}

func TestWorkspaceImportRefusals(t *testing.T) {
	h := newWSHarness(t)
	writeSpecFeature(t, h.root)
	h.toGraph()
	h.key("A", "R")
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	n := len(h.m.g.Nodes)
	h.command("/import speckit")
	h.want(msgStopFirst)
	if len(h.m.g.Nodes) != n {
		t.Fatal("nodes added while running")
	}

	h2 := newWSHarness(t)
	h2.command("/import speckit")
	if !transcriptHas(h2, "no specs/*/tasks.md found") {
		t.Fatalf("missing-file error: %+v", h2.m.sess.snapshot().Transcript)
	}
	h2.command("/import foo")
	if !transcriptHas(h2, importUsage) {
		t.Fatal("no usage line")
	}
}

func TestWorkspaceImportListedInCommands(t *testing.T) {
	h := newWSHarness(t)
	if _, ok := h.m.lookup("/import"); !ok {
		t.Fatal("/import is not registered")
	}
}

func importHarness(t *testing.T, level int) (*wsHarness, *autoNotes) {
	t.Helper()
	h, notes := autoHarness(t, level)
	writeSpecFeature(t, h.root)
	h.m.cfg.Agents = map[string]config.AgentConfig{"qa": {Description: "tests"}, "coder": {Description: "code"}}
	return h, notes
}

func TestWorkspaceImportLevel1DoesNotRun(t *testing.T) {
	h, notes := importHarness(t, 1)
	h.command("/import speckit")
	if !h.m.hasNodes() || h.m.running() != 0 || len(h.autoLines()) != 0 {
		t.Fatalf("level 1: nodes %v, running %d, lines %q", h.m.hasNodes(), h.m.running(), h.autoLines())
	}
	if !notes.has(notify.NeedsYou, "Assign roles: 4 sub-tasks are waiting") {
		t.Errorf("no assign-roles note: %+v", notes.notes)
	}
}

func TestWorkspaceImportLevel2AcceptsAndRuns(t *testing.T) {
	h, notes := importHarness(t, 2)
	h.command("/import speckit")
	h.wantAuto("accepted")
	if h.m.g.Node("T1").Role != "coder" || h.m.g.Node("T3").Role != "qa" {
		t.Errorf("roles = %q %q", h.m.g.Node("T1").Role, h.m.g.Node("T3").Role)
	}
	if h.m.running() == 0 {
		t.Error("the run did not start")
	}
	if notes.has(notify.NeedsYou, "Assign roles") {
		t.Errorf("level 2 sent the split-done note: %+v", notes.notes)
	}
}

func TestWorkspaceImportBudgetReachedDoesNotRun(t *testing.T) {
	h, _ := importHarness(t, 1)
	h.m.cfg.Workspace.BudgetUSD = 1
	h.command("/import speckit --phase 1")
	h.m.cfg.Workspace.Autonomy = 2
	if err := h.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 2, true, 1000
	}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.command("/import speckit --phase 2")
	h.wantAuto("budget reached, the run was not started")
	if h.m.running() != 0 {
		t.Error("the run started with the budget reached")
	}
}
