package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsFakeExec is an Executor whose nodes finish when the test releases them.
type wsFakeExec struct {
	mu    sync.Mutex
	gates map[string]chan error
}

func (f *wsFakeExec) gate(id string) chan error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gates == nil {
		f.gates = map[string]chan error{}
	}
	if f.gates[id] == nil {
		f.gates[id] = make(chan error, 1)
	}
	return f.gates[id]
}

func (f *wsFakeExec) release(id string, err error) { f.gate(id) <- err }

func (f *wsFakeExec) Run(ctx context.Context, _ *workspace.Graph, n *workspace.Node, emit func(workspace.Event)) (workspace.Result, error) {
	emit(workspace.Event{NodeID: n.ID, Type: workspace.EventOutput, Text: "working on " + n.ID + "\n"})
	select {
	case err := <-f.gate(n.ID):
		if err != nil {
			return workspace.Result{}, err
		}
		return workspace.Result{Summary: "ok " + n.ID}, nil
	case <-ctx.Done():
		return workspace.Result{}, ctx.Err()
	}
}

type wsRoleCall struct {
	name string
	ac   config.AgentConfig
}

type wsHarness struct {
	t         *testing.T
	m         workspaceModel
	root      string
	exec      *wsFakeExec
	eng       *workspace.Engine
	roleCalls []wsRoleCall
	splitErr  error
	graph     func() *workspace.Graph
	checks    []string
	tasks     []string      // task text of every split call
	earlier   []string      // "earlier in this session" text of every split call
	existing  [][]string    // node IDs passed to every split call
	nexts     []int         // first new number passed to every split call
	summary   func() string // session summary hook given to the engine
	recent    []RecentProject
	splitCmd  tea.Cmd // command of the split that started last
}

func wsFixtureGraph() *workspace.Graph {
	mk := func(id, title, role string, deps ...string) *workspace.Node {
		return &workspace.Node{ID: id, Title: title, Description: "Do " + title, Kind: workspace.KindWork,
			Status: workspace.StatusTodo, SuggestedRole: role, DependsOn: deps}
	}
	return &workspace.Graph{
		Task:  "Build a todo CLI",
		Title: "Todo CLI",
		Nodes: []*workspace.Node{
			mk("T1", "Data model", "coder"),
			mk("T2", "Write docs", "docs_writer", "T1"),
			mk("T3", "Storage tests", "qa", "T1"),
			mk("T4", "Command parser", "coder", "T2", "T3"),
			mk("T5", "Security pass", "security", "T4"),
		},
		NewRoles: []workspace.NewRole{{Name: "docs_writer", Description: "Writes user docs"}},
	}
}

// wsSmallGraph is a two-node split used for follow-up tasks.
func wsSmallGraph() *workspace.Graph {
	return &workspace.Graph{
		Task: "Add search", Title: "Search",
		Nodes: []*workspace.Node{
			{ID: "T1", Title: "Search index", Kind: workspace.KindWork, Status: workspace.StatusTodo, SuggestedRole: "coder"},
			{ID: "T2", Title: "Search tests", Kind: workspace.KindWork, Status: workspace.StatusTodo, SuggestedRole: "qa", DependsOn: []string{"T1"}},
		},
	}
}

// newWSHarnessAt builds a harness for a given project root (and the HOME that
// the caller already set).
func newWSHarnessAt(t *testing.T, root string) *wsHarness {
	t.Helper()
	h := &wsHarness{t: t, root: root, exec: &wsFakeExec{}, graph: wsFixtureGraph, checks: []string{"go build ./...", "go test ./..."}}
	deps := wsDeps{
		split: func(_ context.Context, task, earlier string, existing []string, next int, _ func(string)) (*workspace.Graph, runner.TokenUsage, error) {
			h.tasks = append(h.tasks, task)
			h.existing = append(h.existing, existing)
			h.nexts = append(h.nexts, next)
			h.earlier = append(h.earlier, earlier)
			if h.splitErr != nil {
				return nil, runner.TokenUsage{}, h.splitErr
			}
			return h.graph(), runner.TokenUsage{InputTokens: 100, OutputTokens: 50}, nil
		},
		newEngine: func(g *workspace.Graph, save func(*workspace.Graph) error, summary func() string) *workspace.Engine {
			h.summary = summary
			h.eng = workspace.NewEngineWithSave(g, h.exec, 3, save)
			return h.eng
		},
		saveRole: func(name string, ac config.AgentConfig) (config.Config, error) {
			h.roleCalls = append(h.roleCalls, wsRoleCall{name, ac})
			cfg := h.m.cfg
			agents := map[string]config.AgentConfig{}
			for k, v := range cfg.Agents {
				agents[k] = v
			}
			agents[name] = ac
			cfg.Agents = agents
			return cfg, nil
		},
		checkCmds: func() []string { return h.checks },
		recent:    func() []RecentProject { return h.recent },
	}
	h.m = newWorkspaceModel(root, config.DefaultConfig(), deps)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 34})
	t.Cleanup(func() {
		h.m.runCancel()
		if h.eng != nil {
			h.eng.StopAll()
		}
	})
	return h
}

// newWSHarness is hermetic: HOME and the project root are temp directories.
func newWSHarness(t *testing.T) *wsHarness {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return newWSHarnessAt(t, t.TempDir())
}

func wsKeyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func (h *wsHarness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	was := h.m.splitting
	next, cmd := h.m.Update(msg)
	h.m = next.(workspaceModel)
	if h.m.splitting && !was {
		h.splitCmd = cmd
	}
	return cmd
}

func (h *wsHarness) key(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = h.send(wsKeyMsg(k))
	}
	return cmd
}

func (h *wsHarness) size(w, hh int) { h.send(tea.WindowSizeMsg{Width: w, Height: hh}) }

func (h *wsHarness) view() string { return ansi.Strip(h.m.View()) }

// flat collapses all whitespace so keycap padding does not matter.
func (h *wsHarness) flat() string { return strings.Join(strings.Fields(h.view()), " ") }

func (h *wsHarness) want(sub string) {
	h.t.Helper()
	if !strings.Contains(h.flat(), sub) {
		h.t.Fatalf("view does not contain %q:\n%s", sub, h.view())
	}
}

func (h *wsHarness) wantNot(sub string) {
	h.t.Helper()
	if strings.Contains(h.flat(), sub) {
		h.t.Fatalf("view unexpectedly contains %q:\n%s", sub, h.view())
	}
}

// toGraph sends a task and feeds back the split result.
func (h *wsHarness) toGraph() {
	h.t.Helper()
	h.sendTask("Build a todo CLI")
	if h.m.focus != wsFocusGraph || !h.m.hasNodes() {
		h.t.Fatalf("no graph after the split:\n%s", h.view())
	}
}

// finishSplit runs the split command of the pending send (so the split seam
// is called) and delivers its result to the model.
func (h *wsHarness) finishSplit() {
	h.t.Helper()
	if h.splitCmd == nil {
		h.t.Fatal("no split is pending")
	}
	h.send(splitResult(h.t, h.splitCmd))
}

// splitResult runs cmd (a split command, possibly in a batch with a tick) and
// returns the wsSplitDoneMsg it produces.
func splitResult(t *testing.T, cmd tea.Cmd) wsSplitDoneMsg {
	t.Helper()
	out := make(chan wsSplitDoneMsg, 1)
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch msg := c().(type) {
			case wsSplitDoneMsg:
				select {
				case out <- msg:
				default:
				}
			case tea.BatchMsg:
				for _, sub := range msg {
					run(sub)
				}
			}
		}()
	}
	run(cmd)
	select {
	case msg := <-out:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("the split command produced no result")
	}
	return wsSplitDoneMsg{}
}

// sendTask types text, sends it and delivers the split result.
func (h *wsHarness) sendTask(text string) {
	h.t.Helper()
	h.m.input.SetValue(text)
	h.key("enter")
	if !h.m.splitting {
		h.t.Fatalf("Enter did not start a split:\n%s", h.view())
	}
	h.finishSplit()
}

// typeCmd types a command line (one key per rune) and presses Enter.
func (h *wsHarness) typeCmd(line string) {
	h.t.Helper()
	for _, r := range line {
		h.key(string(r))
	}
	h.key("enter")
}

// pump feeds engine events to the model until cond holds.
func (h *wsHarness) pump(cond func(m workspaceModel) bool) {
	h.t.Helper()
	deadline := time.After(3 * time.Second)
	for !cond(h.m) {
		select {
		case ev := <-h.eng.Events():
			h.send(wsEventMsg{eng: h.eng, ev: ev})
		case <-deadline:
			h.t.Fatalf("timed out waiting for engine events:\n%s", h.view())
		}
	}
}

func (h *wsHarness) status(id string) workspace.Status { return h.m.g.Node(id).Status }

func TestWorkspaceEmptyStateIsTheWholeScreen(t *testing.T) {
	h := newWSHarness(t)
	h.want("WORKSPACE")
	h.want("GRAPH")
	h.want("INSPECTOR")
	h.want("No nodes yet. Describe a task below")
	h.want("Type a task or / for commands")
	h.want("Enter send")
	h.want("Tab graph")
	h.want("? help")
	h.want("new session")
	h.wantNot("saved session")
	if h.m.focus != wsFocusPrompt {
		t.Fatal("focus must start on the prompt")
	}
}

func TestWorkspaceSendSplitsAndShowsGraph(t *testing.T) {
	h := newWSHarness(t)
	h.m.input.SetValue("Build a todo CLI")
	h.key("enter")
	h.want("splitting the task")
	h.want("you › Build a todo CLI")
	h.finishSplit()
	h.want("Data model")
	h.want("? suggested: coder")
	h.want("split into 5 nodes: T1, T2, T3, T4, T5")
	if h.eng.Snapshot().Node("CHECK") == nil {
		t.Fatal("no CHECK node was added")
	}
}

func TestWorkspaceSplitErrorKeepsInput(t *testing.T) {
	h := newWSHarness(t)
	h.m.input.SetValue("Build a todo CLI")
	h.key("enter")
	h.send(wsSplitDoneMsg{gen: h.m.splitGen, err: errors.New("split: no sub-tasks found")})
	h.want("! split: no sub-tasks found")
	if h.m.hasNodes() || h.m.input.Value() != "Build a todo CLI" {
		t.Fatalf("nodes=%v input=%q", h.m.hasNodes(), h.m.input.Value())
	}
}

func TestWorkspaceEscCancelsSplit(t *testing.T) {
	h := newWSHarness(t)
	h.m.input.SetValue("Build a todo CLI")
	h.key("enter")
	stale := h.m.splitGen
	h.key("esc")
	if h.m.splitting {
		t.Fatal("still splitting")
	}
	h.send(wsSplitDoneMsg{gen: stale, g: h.graph()})
	if h.m.hasNodes() || h.m.input.Value() != "Build a todo CLI" {
		t.Fatal("a cancelled split must not open the graph")
	}
}

func TestWorkspaceNothingRunsAfterSplit(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	if h.eng.Running() != 0 {
		t.Fatalf("running = %d", h.eng.Running())
	}
	for _, n := range h.eng.Snapshot().Nodes {
		if n.Status != workspace.StatusTodo {
			t.Fatalf("%s is %s", n.ID, n.Status)
		}
	}
	h.want("5 nodes need an agent")
}

func TestWorkspaceAcceptAllAssignsDefinedOnly(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A")
	g := h.eng.Snapshot()
	for id, want := range map[string]string{"T1": "coder", "T2": "", "T3": "qa", "T4": "coder", "T5": "security"} {
		if got := g.Node(id).Role; got != want {
			t.Errorf("%s role = %q, want %q", id, got, want)
		}
	}
	h.want("assigned 4 nodes; 1 need a new role")
}

func TestWorkspaceAssignPickerListsRolesAndNewRole(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("a")
	v := h.view()
	if !strings.Contains(v, "Assign T1 · Data model") {
		t.Fatalf("no picker title:\n%s", v)
	}
	v = v[strings.Index(v, "Assign T1"):]
	iStar := strings.Index(v, "★ suggested")
	iQA := strings.Index(v, " qa ")
	if iStar < 0 || iQA < 0 || iStar > iQA {
		t.Fatalf("suggested role is not first:\n%s", v)
	}
	if !strings.Contains(h.flat(), `＋ create "docs_writer"`) {
		t.Fatalf("no create row:\n%s", v)
	}
	h.want("＋ new role…")
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("Esc did not close the picker")
	}
}

func TestWorkspacePickerAssignsAndUnassigns(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("a", "enter") // cursor starts on the suggested role
	if got := h.eng.Snapshot().Node("T1").Role; got != "coder" {
		t.Fatalf("role = %q", got)
	}
	h.key("a")
	h.want("– unassign")
	for i := 0; i < 20; i++ {
		h.key("down")
	}
	h.key("enter")
	if got := h.eng.Snapshot().Node("T1").Role; got != "" {
		t.Fatalf("role = %q after unassign", got)
	}
}

func TestWorkspaceCreateRoleAssigns(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.m.sel = "T2"
	h.key("a", "enter") // cursor starts on the suggested create row
	if h.m.overlay != wsOverlayForm {
		t.Fatalf("form not open:\n%s", h.view())
	}
	h.want("Runner / model: inherits")
	h.key("enter")
	if len(h.roleCalls) != 1 || h.roleCalls[0].name != "docs_writer" || h.roleCalls[0].ac.Description != "Writes user docs" {
		t.Fatalf("saveRole calls: %+v", h.roleCalls)
	}
	if got := h.eng.Snapshot().Node("T2").Role; got != "docs_writer" {
		t.Fatalf("T2 role = %q", got)
	}
	h.want(`role "docs_writer" created`)
}

func TestWorkspaceRoleFormValidates(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("a")
	for i := 0; i < 20; i++ {
		h.key("down")
	}
	h.key("enter")
	if h.m.overlay != wsOverlayForm {
		t.Fatalf("form not open:\n%s", h.view())
	}
	h.key("enter")
	h.want("! Name must be 2-32 characters")

	h.key("B", "a", "d")
	h.key("tab", "x")
	h.key("enter")
	h.want("! Name must be 2-32 characters")

	h.m.formName.SetValue("coder")
	h.key("enter")
	h.want(`! A role named "coder" already exists`)

	h.m.formName.SetValue("tool_smith")
	h.m.formDesc.SetValue("")
	h.key("enter")
	h.want("! Describe what this role does")

	if len(h.roleCalls) != 0 {
		t.Fatalf("saveRole was called: %+v", h.roleCalls)
	}
}

func TestWorkspaceRunSelected(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.want("running")
	h.exec.release("T1", nil)
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusDone })
	h.want("✓ done")
}

func (m workspaceModel) status(id string) workspace.Status { return m.g.Node(id).Status }

func TestWorkspaceRunBlockedShowsReason(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A")
	h.m.sel = "T3"
	h.key("r")
	h.want("blocked by T1")
	if h.eng.Running() != 0 {
		t.Fatal("a blocked node must not run")
	}
}

func TestWorkspaceRunAllWarnsUnassigned(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "R")
	h.want("1 nodes are unassigned and will wait")
	if !h.eng.Auto() {
		t.Fatal("auto mode is off")
	}
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	h.want("AUTO")
}

func TestWorkspaceRunAllWithNothingAssignedDoesNotStart(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("R")
	h.want("Nothing can run yet")
	if h.eng.Auto() {
		t.Fatal("auto mode must stay off")
	}
}

func TestWorkspaceFixNodeOnFailedCheck(t *testing.T) {
	h := newWSHarness(t)
	h.checks = nil
	h.graph = func() *workspace.Graph {
		g := wsFixtureGraph()
		g.NewRoles = nil
		g.Nodes = g.Nodes[:1]
		g.Nodes[0].Status = workspace.StatusDone
		g.Nodes[0].Role = "coder"
		c := g.AddCheck([]string{"go test ./..."})
		c.Status = workspace.StatusFailed
		c.Error = "FAIL: TestParse"
		return g
	}
	h.toGraph()
	h.m.sel = "CHECK"
	h.want("f add fix")
	h.key("f")
	if h.m.sel != "F1" {
		t.Fatalf("selected %q, want F1", h.m.sel)
	}
	g := h.eng.Snapshot()
	if g.Node("CHECK").Status != workspace.StatusTodo || len(g.Blockers("CHECK")) == 0 {
		t.Fatalf("CHECK must wait for F1: %+v", g.Node("CHECK"))
	}
	h.want("Added F1")
}

func TestWorkspaceFixIgnoredOnOtherNodes(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("f")
	h.want("f adds a fix node")
}

func TestWorkspaceOutputViewFollowsTail(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	for i := 1; i <= 100; i++ {
		h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "T1", Type: workspace.EventOutput, Text: fmt.Sprintf("line %d\n", i)}})
	}
	h.key("o")
	if h.m.screen != wsScreenOutput {
		t.Fatal("output view did not open")
	}
	h.want("T1 · Data model · running")
	h.want("line 100")
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "T1", Type: workspace.EventOutput, Text: "line 101\n"}})
	h.want("line 101")
	h.key("pgup")
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "T1", Type: workspace.EventOutput, Text: "line 102\n"}})
	h.wantNot("line 102")
	h.key("esc")
	if h.m.screen != wsScreenMain {
		t.Fatal("Esc did not return to the graph")
	}
}

func TestWorkspaceOutputKeepsTwoThousandLines(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	var sb strings.Builder
	for i := 0; i < 2500; i++ {
		fmt.Fprintf(&sb, "l%d\n", i)
	}
	h.send(wsEventMsg{eng: h.eng, ev: workspace.Event{NodeID: "T1", Type: workspace.EventOutput, Text: sb.String()}})
	lines := h.m.outputs["T1"]
	if len(lines) != wsMaxOutput || lines[len(lines)-1] != "l2499" || lines[0] != "l500" {
		t.Fatalf("kept %d lines, first %q last %q", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestWorkspaceSlashMenuOpensMenu(t *testing.T) {
	for _, line := range []string{"/menu", "/config"} {
		h := newWSHarness(t)
		for _, r := range line {
			h.key(string(r))
		}
		h.want("/config")
		cmd := h.key("enter")
		if !h.m.openMenu {
			t.Fatalf("%s: OpenMenu not set", line)
		}
		if cmd == nil {
			t.Fatal("no quit command")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatal("command did not quit")
		}
	}
}

func TestWorkspaceSlashPaletteFilters(t *testing.T) {
	h := newWSHarness(t)
	h.key("/")
	h.want("/help")
	h.want("/compact")
	h.want("/quit")
	h.key("r", "o")
	h.want("/roles")
	h.wantNot("/quit")
	h.key("enter")
	h.want("＋ new role…")
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("roles list did not close")
	}
}

func TestWorkspaceUnknownCommand(t *testing.T) {
	h := newWSHarness(t)
	h.key("/", "z")
	h.key("enter")
	h.want("! unknown command /z — /help lists them")
	h.m.input.Reset()
	h.m.input.SetValue("/nope with args")
	h.key("enter")
	h.want("! unknown command /nope — /help lists them")
}

func TestWorkspaceResumeLoadsSaved(t *testing.T) {
	h := newWSHarness(t)
	g := wsFixtureGraph()
	g.Nodes[0].Role = "coder"
	g.Nodes[0].Status = workspace.StatusRunning
	g.Nodes[1].Status = workspace.StatusDone
	s := workspace.NewSession()
	s.Title = g.Title
	s.Graph = g
	if err := s.Save(h.root); err != nil {
		t.Fatal(err)
	}
	h.m = newWorkspaceModel(h.root, h.m.cfg, h.m.deps)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 34})
	h.want("↺ 1 saved session — /resume")
	h.typeCmd("/resume")
	h.want("Todo CLI · 1/5 done")
	h.key("enter")
	if !h.m.hasNodes() || h.m.focus != wsFocusGraph {
		t.Fatalf("resume did not open the graph:\n%s", h.view())
	}
	if h.status("T1") != workspace.StatusTodo {
		t.Fatalf("T1 = %s, want todo", h.status("T1"))
	}
	h.want("○ ready")
}

func TestWorkspaceResumeWithNothingSaved(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/resume")
	h.want("no saved sessions in this project")
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("Esc did not close the picker")
	}
}

func TestWorkspaceQuitConfirmsWhenRunning(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	if cmd := h.key("q"); cmd != nil {
		t.Fatal("q must ask first while a node runs")
	}
	h.want("1 node(s) are running")
	h.key("n")
	if h.m.quitting || h.eng.Running() != 1 {
		t.Fatal("n must cancel the quit")
	}
	cmd := h.key("q", "y")
	if cmd == nil {
		t.Fatal("y did not quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("command did not quit")
	}
	if h.eng.Running() != 0 {
		t.Fatal("quit left nodes running")
	}
}

func TestWorkspaceQuitWithoutRunningIsImmediate(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	cmd := h.key("q")
	if cmd == nil || !h.m.quitting {
		t.Fatal("q should quit when nothing runs")
	}
}

func TestWorkspaceCtrlCTwice(t *testing.T) {
	h := newWSHarness(t)
	now := time.Now()
	h.m.now = func() time.Time { return now }
	if cmd := h.key("ctrl+c"); cmd != nil {
		t.Fatal("first Ctrl+C must not quit")
	}
	h.want("Press Ctrl+C again to quit")
	now = now.Add(3 * time.Second)
	if cmd := h.key("ctrl+c"); cmd != nil {
		t.Fatal("a late second Ctrl+C only re-arms")
	}
	now = now.Add(time.Second)
	if cmd := h.key("ctrl+c"); cmd == nil {
		t.Fatal("second Ctrl+C within 2s must quit")
	}
}

func TestWorkspaceClearConfirmsWhenUnfinished(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("tab")
	h.typeCmd("/clear")
	h.want("Start a new session? y/n")
	h.key("n")
	if !h.m.hasNodes() {
		t.Fatal("n should cancel")
	}
	h.typeCmd("/clear")
	h.key("y")
	if h.m.hasNodes() || h.m.eng != nil || h.m.focus != wsFocusPrompt {
		t.Fatalf("y should start a new session:\n%s", h.view())
	}
	h.want("No nodes yet")
}

func TestWorkspaceInspectorToggle(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.want("DESCRIPTION")
	h.key("i")
	h.wantNot("DESCRIPTION")
	h.key("i")
	h.want("DESCRIPTION")
	h.size(80, 24)
	h.wantNot("DESCRIPTION")
	h.key("i")
	h.want("DESCRIPTION")
}

func assertFits(t *testing.T, name, view string, w, h int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) > h {
		t.Errorf("%s: %d lines > %d\n%s", name, len(lines), h, strings.Join(lines, "\n"))
		return
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Errorf("%s: line %d is %d cells > %d: %q", name, i, ansi.StringWidth(l), w, l)
			return
		}
	}
}

func TestWorkspaceViewsFitSize(t *testing.T) {
	states := map[string]func(h *wsHarness){
		"empty":      func(h *wsHarness) {},
		"empty-text": func(h *wsHarness) { h.m.input.SetValue("line one\nline two\nline three\nline four") },
		"empty-split": func(h *wsHarness) {
			h.m.input.SetValue("Build a todo CLI with a long description that goes past the end of the line")
			h.key("enter")
		},
		"empty-palette": func(h *wsHarness) { h.key("/") },
		"empty-error": func(h *wsHarness) {
			h.m.say("you", strings.Repeat("word ", 40))
			h.m.say("error", strings.Repeat("bad ", 60))
			h.m.say("lead", "ok")
		},
		"empty-saved": func(h *wsHarness) { h.m.nSaved = 3 },
		"graph":       func(h *wsHarness) { h.toGraph() },
		"graph-sel":   func(h *wsHarness) { h.toGraph(); h.m.sel = "CHECK" },
		"graph-prompt": func(h *wsHarness) {
			h.toGraph()
			h.key("tab")
		},
		"picker": func(h *wsHarness) { h.toGraph(); h.key("a") },
		"form": func(h *wsHarness) {
			h.toGraph()
			h.m.sel = "T2"
			h.key("a", "enter", "x", "enter")
		},
		"output": func(h *wsHarness) { h.toGraph(); h.key("o") },
		"wide-titles": func(h *wsHarness) {
			h.graph = func() *workspace.Graph {
				g := wsFixtureGraph()
				g.Nodes[0].Title = "修复登录 bug 🚀 for the login handler"
				g.Nodes[0].Description = strings.Repeat("修复登录 bug 🚀 ", 30)
				g.Nodes[0].Warnings = []string{"警告 ⚠ something"}
				return g
			}
			h.toGraph()
		},
		"form-ro": func(h *wsHarness) {
			h.toGraph()
			h.m.sel = "T2"
			h.key("a", "enter", "tab", "right")
		},
		"help":    func(h *wsHarness) { h.toGraph(); h.key("?") },
		"cmds":    func(h *wsHarness) { h.toGraph(); h.key("/") },
		"cmds-c":  func(h *wsHarness) { h.toGraph(); h.key("/", "c") },
		"roles":   func(h *wsHarness) { h.key("/", "r", "o", "enter") },
		"confirm": func(h *wsHarness) { h.toGraph(); h.key("tab"); h.typeCmd("/clear") },
		"resume": func(h *wsHarness) {
			h.toGraph()
			h.key("tab")
			h.typeCmd("/resume")
		},
		"project": func(h *wsHarness) { h.key("/", "p", "r", "o", "j", "enter") },
		"profile": func(h *wsHarness) { h.typeCmd("/profile") },
		"profile-name": func(h *wsHarness) {
			h.typeCmd("/profile")
			h.key("enter")
			h.key("x", "enter")
		},
		"status": func(h *wsHarness) { h.toGraph(); h.key("tab"); h.typeCmd("/status") },
		"cost":   func(h *wsHarness) { h.toGraph(); h.key("tab"); h.typeCmd("/cost") },
		"custom": func(h *wsHarness) {
			wsWriteCommand(t, h.root, "review", "# Review the change\nReview $ARGUMENTS")
			h.key("/")
		},
	}
	for name, setup := range states {
		for _, sz := range [][2]int{{60, 16}, {80, 24}, {120, 40}, {100, 20}} {
			h := newWSHarness(t)
			h.size(sz[0], sz[1])
			setup(h)
			assertFits(t, fmt.Sprintf("%s %dx%d", name, sz[0], sz[1]), h.m.View(), sz[0], sz[1])
		}
	}
}

func TestWorkspaceTooSmall(t *testing.T) {
	h := newWSHarness(t)
	h.size(50, 10)
	h.want("Terminal too small")
	h.size(60, 15)
	h.want("Terminal too small (need 60×16)")
}

func TestWorkspaceLiveStatusLines(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "R")
	h.pump(func(m workspaceModel) bool { return m.status("T1") == workspace.StatusRunning })
	h.want("1 running")
	h.exec.release("T1", errors.New("compile error in model.go"))
	h.pump(func(m workspaceModel) bool { return strings.HasPrefix(m.notice, "T1 failed") })
	h.want("T1 failed")
	h.want("compile error in model.go")
	h.key("right")
	h.key("left")
	h.want("r retry")
}

func TestWriteSplitRaw(t *testing.T) {
	root := t.TempDir()
	writeSplitRaw(root, "first answer")
	writeSplitRaw(root, "second answer")
	p := filepath.Join(root, ".decsr", "workspace_split.txt")
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "second answer" {
		t.Fatalf("content = %q err=%v", b, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}
