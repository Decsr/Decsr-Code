package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsWriteCommand writes <root>/.decsr/commands/<name>.md.
func wsWriteCommand(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, ".decsr", "commands")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// wsMarkDone sets node statuses on the live graph and refreshes the model.
func (h *wsHarness) wsMarkDone(ids ...string) {
	h.t.Helper()
	err := h.eng.Replace(func(g *workspace.Graph) {
		for _, id := range ids {
			n := g.Node(id)
			n.Status = workspace.StatusDone
			n.Role = "coder"
			n.Summary = "finished " + id
			n.Files = []string{id + ".go"}
		}
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
}

func (h *wsHarness) sessionIDs() []string {
	var ids []string
	for _, s := range workspace.ListSessions(h.root) {
		ids = append(ids, s.ID)
	}
	return ids
}

// ── typing and focus ─────────────────────────────────────────────────────────

func TestWorkspacePromptFocusTypesEveryKey(t *testing.T) {
	h := newWSHarness(t)
	for _, r := range "qarRsfn?/x" {
		h.key(string(r))
	}
	if got := h.m.input.Value(); got != "qarRsfn?/x" {
		t.Fatalf("input = %q", got)
	}
	if h.m.splitting || h.m.overlay != wsOverlayNone || h.m.showHelp || h.m.quitting || h.m.eng != nil {
		t.Fatal("typing triggered an action")
	}
	// The same keys also type with a graph on screen and prompt focus.
	h2 := newWSHarness(t)
	h2.toGraph()
	h2.key("tab")
	for _, r := range "aARrSsfoiqn" {
		h2.key(string(r))
	}
	if got := h2.m.input.Value(); got != "aARrSsfoiqn" {
		t.Fatalf("input = %q", got)
	}
	if h2.eng.Running() != 0 || h2.m.quitting || h2.m.overlay != wsOverlayNone || h2.m.screen != wsScreenMain {
		t.Fatal("typing triggered a graph action")
	}
	for _, n := range h2.eng.Snapshot().Nodes {
		if n.Role != "" {
			t.Fatalf("%s was assigned by typing", n.ID)
		}
	}
}

func TestWorkspaceTabTogglesFocusAndHints(t *testing.T) {
	h := newWSHarness(t)
	promptHints := h.flat()
	h.want("Tab graph")
	h.key("tab")
	if h.m.focus != wsFocusGraph {
		t.Fatal("Tab did not move to the graph")
	}
	h.want("Tab prompt")
	h.wantNot("Enter send")
	h.key("tab")
	if h.m.focus != wsFocusPrompt {
		t.Fatal("Tab did not move back")
	}
	if promptHints != h.flat() {
		t.Fatal("prompt view changed after a round trip")
	}
	h.key("shift+tab")
	if h.m.focus != wsFocusGraph {
		t.Fatal("Shift+Tab did not move to the graph")
	}
}

func TestWorkspaceGraphKeysOnlyWithGraphFocus(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("n") // the old new-task key is gone
	if h.m.overlay != wsOverlayNone || !h.m.hasNodes() {
		t.Fatal("n must do nothing")
	}
	h.key("A")
	if h.eng.Snapshot().Node("T1").Role != "coder" {
		t.Fatal("A works with graph focus")
	}
	h.key("c")
	if h.m.focus != wsFocusPrompt {
		t.Fatal("c must move to the prompt")
	}
	h.key("tab")
	h.key("/")
	if h.m.focus != wsFocusPrompt || h.m.input.Value() != "/" {
		t.Fatalf("/ must move to the prompt and type the slash: focus=%v input=%q", h.m.focus, h.m.input.Value())
	}
	h.want("/compact")
}

func TestWorkspaceEscMovesToGraphOnlyWithNodes(t *testing.T) {
	h := newWSHarness(t)
	h.key("esc")
	if h.m.focus != wsFocusPrompt {
		t.Fatal("Esc with an empty graph must stay in the prompt")
	}
	h.toGraph()
	h.key("tab", "/", "c")
	h.key("esc") // closes the palette first
	if h.m.focus != wsFocusPrompt || h.m.input.Value() != "" {
		t.Fatalf("Esc must close the palette: focus=%v input=%q", h.m.focus, h.m.input.Value())
	}
	h.key("esc")
	if h.m.focus != wsFocusGraph {
		t.Fatal("Esc must move to the graph")
	}
}

func TestWorkspaceHelpOnlyWhenEmpty(t *testing.T) {
	h := newWSHarness(t)
	h.key("?")
	if !h.m.showHelp {
		t.Fatal("? on an empty prompt opens help")
	}
	h.want("COMMANDS")
	h.want("/compact")
	h.key("esc")
	h.key("a", "?")
	if h.m.showHelp || h.m.input.Value() != "a?" {
		t.Fatal("? mid-text must type")
	}
}

// ── sessions ─────────────────────────────────────────────────────────────────

func TestWorkspaceSplitFocusesGraphAndSavesSession(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	if h.m.focus != wsFocusGraph || h.m.sel != "T1" {
		t.Fatalf("focus=%v sel=%q", h.m.focus, h.m.sel)
	}
	ids := h.sessionIDs()
	if len(ids) != 1 {
		t.Fatalf("sessions = %v", ids)
	}
	s, err := workspace.LoadSession(h.root, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "Todo CLI" || s.Graph == nil || len(s.Graph.Nodes) != 6 || s.NextID != 6 {
		t.Fatalf("session: title=%q next=%d graph=%v", s.Title, s.NextID, s.Graph)
	}
	if len(s.Transcript) != 2 || s.Transcript[0].Who != "you" || s.Transcript[1].Who != "lead" ||
		!strings.Contains(s.Transcript[1].Text, "split into 5 nodes: T1, T2, T3, T4, T5") {
		t.Fatalf("transcript: %+v", s.Transcript)
	}
	if s.In != 100 || s.Out != 50 {
		t.Fatalf("lead usage = %d/%d", s.In, s.Out)
	}
	// An assignment is saved through the engine.
	h.key("A")
	s, _ = workspace.LoadSession(h.root, ids[0])
	if s.Graph.Node("T1").Role != "coder" {
		t.Fatal("the engine did not save the assignment into the session")
	}
}

func TestWorkspaceSecondTaskAppends(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.wsMarkDone("T1")
	h.key("tab")
	h.graph = wsSmallGraph
	h.sendTask("Add search")
	g := h.eng.Snapshot()
	for _, id := range []string{"T6", "T7"} {
		if g.Node(id) == nil {
			t.Fatalf("%s missing after the append", id)
		}
	}
	if g.Node("T6").Title != "Search index" || len(g.Node("T7").DependsOn) != 1 || g.Node("T7").DependsOn[0] != "T6" {
		t.Fatalf("appended nodes wrong: %+v %+v", g.Node("T6"), g.Node("T7"))
	}
	check := g.Node("CHECK")
	if check.Status != workspace.StatusTodo || len(check.DependsOn) != 7 || g.Nodes[len(g.Nodes)-1].ID != "CHECK" {
		t.Fatalf("CHECK = %+v", check)
	}
	if h.m.sel != "T6" || h.m.focus != wsFocusGraph {
		t.Fatalf("sel=%q focus=%v", h.m.sel, h.m.focus)
	}
	h.want("split into 2 nodes: T6, T7")
	if !strings.Contains(h.earlier[1], "T1 Data model [done]") {
		t.Fatalf("the lead was not told about the current nodes: %q", h.earlier[1])
	}
	ids := h.sessionIDs()
	s, _ := workspace.LoadSession(h.root, ids[0])
	if s.NextID != 8 || s.Graph.Node("T7") == nil {
		t.Fatalf("saved session: next=%d", s.NextID)
	}
}

func TestWorkspaceTaskRefusedWhileRunning(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.key("tab")
	h.m.input.SetValue("more")
	h.key("enter")
	if h.m.splitting {
		t.Fatal("a task must not be sent while nodes run")
	}
	h.want("Stop the running nodes first")
	if h.m.input.Value() != "more" {
		t.Fatal("the text must stay in the box")
	}
}

func TestWorkspaceCompactFoldsAndFeedsNextSplit(t *testing.T) {
	h := newWSHarness(t)
	h.graph = func() *workspace.Graph { // T1 and T3 are needed by nobody
		g := wsFixtureGraph()
		g.Node("T2").DependsOn, g.Node("T3").DependsOn = nil, nil
		g.Node("T4").DependsOn = []string{"T2"}
		return g
	}
	h.toGraph()
	h.wsMarkDone("T1", "T3")
	h.key("tab")
	h.typeCmd("/compact")
	h.want("compacted 2 finished nodes")
	g := h.eng.Snapshot()
	if g.Node("T1") != nil || g.Node("T3") != nil || g.Node("T2") == nil || g.Node("CHECK") == nil {
		t.Fatalf("graph after compact: %+v", g.Nodes)
	}
	if len(g.Node("T2").DependsOn) != 0 {
		t.Fatalf("T2 still depends on a folded node: %v", g.Node("T2").DependsOn)
	}
	if h.m.g.Node("T1") != nil || h.m.g.Node(h.m.sel) == nil {
		t.Fatal("the model did not refresh its graph and selection")
	}
	s, _ := workspace.LoadSession(h.root, h.sessionIDs()[0])
	if !strings.Contains(s.Summary, "T1 Data model [coder]: finished T1") || !strings.Contains(s.Summary, "T3") {
		t.Fatalf("summary = %q", s.Summary)
	}
	h.graph = wsSmallGraph
	h.sendTask("Add search")
	if !strings.Contains(h.earlier[1], "finished T1") || !strings.Contains(h.earlier[1], "T2 Write docs") {
		t.Fatalf("the split prompt lacks the summary: %q", h.earlier[1])
	}
	if g := h.eng.Snapshot(); g.Node("T6") == nil || g.Node("T1") != nil {
		t.Fatalf("IDs must continue after compaction: %+v", g.Nodes)
	}
	// Nothing finished any more: compacting again is a no-op.
	h.key("tab")
	h.typeCmd("/compact")
	h.want("nothing to compact")
}

func TestWorkspaceCompactNothingToFold(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/compact")
	h.want("nothing to compact")
	h.toGraph()
	h.key("tab")
	h.typeCmd("/compact")
	h.want("nothing to compact")
}

func TestWorkspaceBusyCommandsRefused(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	h.key("A", "r")
	h.pump(func(m workspaceModel) bool { return len(m.outputs["T1"]) > 0 })
	h.key("tab")
	for _, c := range []string{"/compact", "/clear", "/resume", "/project"} {
		h.typeCmd(c)
		h.want("! stop the running nodes first")
		if h.m.overlay != wsOverlayNone || !h.m.hasNodes() || h.m.quitting {
			t.Fatalf("%s was not refused", c)
		}
		h.m.sess.update(func(s *workspace.Session) { s.Transcript = nil })
	}
	if h.eng.Running() != 1 {
		t.Fatal("refused commands must leave the node running")
	}
}

func TestWorkspaceClearStartsNewSessionAndResumeListsOld(t *testing.T) {
	h := newWSHarness(t)
	h.toGraph()
	first := h.m.sess.snapshot().ID
	h.key("tab")
	h.typeCmd("/clear")
	h.key("y")
	second := h.m.sess.snapshot().ID
	if second == first || h.m.hasNodes() {
		t.Fatal("clear must start a new, empty session")
	}
	h.want("new session")
	h.want("↺ 1 saved session — /resume")
	h.typeCmd("/resume")
	h.want("Todo CLI · 0/6 done")
	h.key("enter")
	if h.m.sess.snapshot().ID != first || !h.m.hasNodes() || h.m.focus != wsFocusGraph {
		t.Fatalf("resume did not load the first session:\n%s", h.view())
	}
	h.want("Data model")
}

func TestWorkspaceClearWithoutUnfinishedNodesDoesNotAsk(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/clear")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("an empty session needs no confirmation")
	}
	h.want("New session started")
}

func TestWorkspaceResumePickerIsPerProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	other := t.TempDir()
	so := workspace.NewSession()
	so.Title = "Other project work"
	so.AddLine("you", "x")
	if err := so.Save(other); err != nil {
		t.Fatal(err)
	}
	h := newWSHarnessAt(t, t.TempDir())
	h.toGraph()
	h.key("tab")
	h.m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	h.typeCmd("/resume")
	h.want("Todo CLI · 0/6 done · 2h ago (current)")
	h.wantNot("Other project work")
	h.key("esc")

	// Two sessions: delete one with confirmation.
	h.typeCmd("/clear")
	h.key("y")
	h.sendTask("second task")
	h.key("tab")
	if n := len(h.sessionIDs()); n != 2 {
		t.Fatalf("sessions = %d", n)
	}
	h.typeCmd("/resume")
	h.key("down", "d")
	h.want("Delete this session? y/n")
	h.key("n")
	if len(h.sessionIDs()) != 2 || h.m.overlay != wsOverlaySessions {
		t.Fatal("n must keep the session and the list")
	}
	h.key("d", "y")
	if len(h.sessionIDs()) != 1 {
		t.Fatalf("delete left %d sessions", len(h.sessionIDs()))
	}
	h.want("Resume a session")
}

func TestWorkspaceResumeOptionOpensPicker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	s := workspace.NewSession()
	s.Title = "Earlier work"
	s.AddLine("you", "x")
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	h := &wsHarness{t: t, root: root}
	h.m = newWorkspaceModel(root, config.DefaultConfig(), wsDeps{}, WorkspaceOptions{Resume: true})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	if h.m.overlay != wsOverlaySessions {
		t.Fatalf("overlay = %v", h.m.overlay)
	}
	h.want("Earlier work")
	// Without the option the picker stays closed.
	m2 := newWorkspaceModel(root, config.DefaultConfig(), wsDeps{})
	if m2.overlay != wsOverlayNone {
		t.Fatal("picker opened without the option")
	}
}

func TestWorkspaceLegacyGraphMigratesToASession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	g := wsFixtureGraph()
	if err := workspace.Save(filepath.Join(root, ".decsr", workspace.FileName), g); err != nil {
		t.Fatal(err)
	}
	h := newWSHarnessAt(t, root)
	h.want("↺ 1 saved session — /resume")
	if _, err := os.Stat(filepath.Join(root, ".decsr", workspace.FileName+".migrated")); err != nil {
		t.Fatalf("legacy file not renamed: %v", err)
	}
	h.typeCmd("/resume")
	h.key("enter")
	if !h.m.hasNodes() || h.m.g.Node("T5") == nil {
		t.Fatal("the migrated graph did not load")
	}
}

// ── profiles ─────────────────────────────────────────────────────────────────

func TestWorkspaceProfileSaveAndApplyRoundTrip(t *testing.T) {
	h := newWSHarness(t)
	// Save the current roles.
	h.typeCmd("/profile")
	h.want("save current roles as…")
	h.key("enter")
	h.want("Save profile")
	h.key("B", "a", "d")
	h.key("enter")
	h.want("Name must be 1-32 characters")
	h.m.textIn.SetValue("base")
	h.key("enter")
	h.want(`profile "base" saved`)
	p, err := config.LoadProfile("base")
	if err != nil || p.Agents["coder"].Runner != h.m.cfg.Agents["coder"].Runner || p.Agents["pm"].Model != h.m.cfg.Agents["pm"].Model {
		t.Fatalf("saved profile: %+v err=%v", p, err)
	}

	// Apply a different profile: node model labels follow.
	if err := config.SaveProfile("cheap", map[string]config.AgentConfig{"coder": {Runner: "claude", Model: "haiku-x"}}); err != nil {
		t.Fatal(err)
	}
	h.toGraph()
	h.key("A")
	h.wantNot("claude:haiku-x")
	h.key("tab")
	h.typeCmd("/profile")
	h.want("base")
	h.want("cheap")
	h.key("down") // base, cheap
	h.key("enter")
	h.want(`profile "cheap" applied`)
	if h.m.cfg.Project.Profile != "cheap" || h.m.cfg.Agents["coder"].Model != "haiku-x" {
		t.Fatalf("cfg not reloaded: %+v", h.m.cfg.Project)
	}
	h.want("profile: cheap")
	h.want("claude:haiku-x")
	if y, err := os.ReadFile(filepath.Join(h.root, ".decsr", "project.yaml")); err != nil || !strings.Contains(string(y), "profile: cheap") {
		t.Fatalf("project.yaml: %s %v", y, err)
	}
}

func TestWorkspaceProfileUnknownRunnerRejectedAndDelete(t *testing.T) {
	h := newWSHarness(t)
	if err := config.SaveProfile("bad", map[string]config.AgentConfig{"coder": {Runner: "nope"}}); err != nil {
		t.Fatal(err)
	}
	h.typeCmd("/profile")
	h.key("enter")
	h.want(`unknown runner "nope"`)
	if h.m.cfg.Project.Profile != "" {
		t.Fatal("a rejected profile must not be recorded")
	}
	h.typeCmd("/profile")
	h.key("d")
	h.want(`Delete profile "bad"? y/n`)
	h.key("y")
	if len(config.ListProfiles()) != 0 || h.m.overlay != wsOverlayProfiles {
		t.Fatalf("profiles = %v overlay=%v", config.ListProfiles(), h.m.overlay)
	}
}

// ── /project ─────────────────────────────────────────────────────────────────

func TestWorkspaceProjectSwitchSavesSessionAndQuits(t *testing.T) {
	h := newWSHarness(t)
	other, gone := t.TempDir(), filepath.Join(t.TempDir(), "gone")
	h.recent = []RecentProject{{Path: gone, Name: "gone"}, {Path: other, Name: "otherproj"}}
	h.toGraph()
	h.key("tab")
	h.typeCmd("/project")
	h.want("(current)")
	h.want("otherproj")
	h.want("(missing)")
	h.want("open path…")
	// current is first; the cursor starts on the first other project, which
	// is missing and cannot be chosen.
	h.key("enter")
	if h.m.overlay != wsOverlayProjects || h.m.quitting {
		t.Fatal("a missing directory must not be selectable")
	}
	h.key("down")
	cmd := h.key("enter")
	if h.m.switchTo != other || cmd == nil {
		t.Fatalf("switchTo=%q cmd=%v", h.m.switchTo, cmd != nil)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("did not quit")
	}
	if len(h.sessionIDs()) != 1 {
		t.Fatal("the session must be saved before switching")
	}
}

func TestWorkspaceProjectOpenPath(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/project")
	h.key("down", "enter") // "open path…" is the last row (current + open path)
	h.want("Path of the project directory")
	h.m.textIn.SetValue(filepath.Join(h.root, "nope"))
	h.key("enter")
	h.want("! Not a directory")
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, "proj"), 0o750); err != nil {
		t.Fatal(err)
	}
	h.m.textIn.SetValue("~/proj")
	cmd := h.key("enter")
	if h.m.switchTo != filepath.Join(home, "proj") || cmd == nil {
		t.Fatalf("switchTo = %q", h.m.switchTo)
	}
}

func TestWorkspaceProjectCurrentRowDoesNotSwitch(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/project")
	h.key("up", "enter")
	if h.m.switchTo != "" || h.m.quitting {
		t.Fatal("choosing the current project must not switch")
	}
}

// ── status, cost, export ─────────────────────────────────────────────────────

func TestWorkspaceStatusCostExport(t *testing.T) {
	h := newWSHarness(t)
	wsWriteCommand(t, h.root, "review", "# Review the change\nReview $ARGUMENTS")
	if err := os.MkdirAll(filepath.Join(h.root, ".decsr", "skills"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.root, ".decsr", "skills", "style.md"), []byte("be tidy"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.toGraph()
	h.wsMarkDone("T1")
	h.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").InputTokens, g.Node("T1").OutputTokens, g.Node("T1").Estimated = 1200, 300, true
		g.Node("T3").Role, g.Node("T3").InputTokens, g.Node("T3").OutputTokens = "qa", 5000, 800
	})
	h.m.g = h.eng.Snapshot()
	h.key("tab")

	h.typeCmd("/status")
	h.want("Status")
	h.want(h.root)
	h.want("5 todo")
	h.want("Parallel")
	h.want("/review (project)")
	h.want("style")
	h.want("coder")
	h.key("esc")

	h.typeCmd("/cost")
	h.want("Cost")
	h.want("T1")
	h.want("~1.2k")
	h.want("By role")
	h.want("Lead")
	h.want("Total")
	h.want("~ marks estimates")
	h.key("esc")

	h.typeCmd("/export")
	rel := filepath.Join(".decsr", "exports", h.m.sess.snapshot().ID+".md")
	h.want("exported to " + rel)
	b, err := os.ReadFile(filepath.Join(h.root, rel))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Todo CLI", "Build a todo CLI", "### T1 Data model", "finished T1", "Role: coder", "Files: T1.go", "split into 5 nodes"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("export lacks %q:\n%s", want, b)
		}
	}
}

// ── custom commands ──────────────────────────────────────────────────────────

func TestWorkspaceCustomCommands(t *testing.T) {
	h := newWSHarness(t)
	wsWriteCommand(t, h.root, "review", "# Review the change\nPlease review $ARGUMENTS carefully.")
	wsWriteCommand(t, h.root, "help", "# Fake help\nshadow")
	h.key("/")
	h.want("/review")
	h.want("Review the change (project)")
	h.wantNot("Fake help")
	h.key("esc")

	h.typeCmd("/review the login flow")
	if !h.m.splitting {
		t.Fatalf("the custom command did not start a split:\n%s", h.view())
	}
	h.want("you › /review the login flow")
	h.finishSplit()
	if h.m.focus != wsFocusGraph || len(h.tasks) != 1 || !strings.Contains(h.tasks[0], "Please review the login flow carefully.") {
		t.Fatalf("tasks = %q", h.tasks)
	}
}

func TestWorkspaceCustomCommandCannotShadowBuiltin(t *testing.T) {
	h := newWSHarness(t)
	wsWriteCommand(t, h.root, "help", "# Fake help\nshadow")
	h.typeCmd("/help")
	if !h.m.showHelp || h.m.splitting {
		t.Fatal("/help must stay the built-in")
	}
}

func TestWorkspaceAgoText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now", 5 * time.Minute: "5m ago", 2 * time.Hour: "2h ago", 50 * time.Hour: "2d ago",
	} {
		if got := agoText(d); got != want {
			t.Errorf("agoText(%v) = %q, want %q", d, got, want)
		}
	}
}
