package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// lpHarness has plugin "sp" installed (skills tdd and debug) in HOME.
func lpHarness(t *testing.T) *wsHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsWritePlugin(t, home, "sp", map[string]string{
		"skills/tdd/SKILL.md":   "---\ndescription: Write the test first\n---\nbody",
		"skills/debug/SKILL.md": "---\ndescription: Find the cause\n---\nbody",
	})
	return newWSHarnessAt(t, t.TempDir())
}

// fakeInstall records installs and writes the plugin into HOME like the real
// one would.
type fakeInstall struct {
	t     *testing.T
	calls []string
	fail  map[string]error
}

func (f *fakeInstall) install(_ context.Context, e plugins.CatalogEntry) error {
	f.calls = append(f.calls, e.Name)
	if err := f.fail[e.Name]; err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".decsr", "plugins", e.Name)
	for rel, body := range map[string]string{
		".claude-plugin/plugin.json": `{"name":"` + e.Name + `"}`,
		"skills/s1/SKILL.md":         "---\ndescription: d\n---\nbody",
	} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	return nil
}

// runMsg runs cmd (possibly a batch with a tick) and returns the first
// wsInstallMsg it produces.
func runInstall(t *testing.T, cmd tea.Cmd) wsInstallMsg {
	t.Helper()
	var find func(c tea.Cmd) (wsInstallMsg, bool)
	find = func(c tea.Cmd) (wsInstallMsg, bool) {
		if c == nil {
			return wsInstallMsg{}, false
		}
		switch msg := c().(type) {
		case wsInstallMsg:
			return msg, true
		case tea.BatchMsg:
			for _, sub := range msg {
				if out, ok := find(sub); ok {
					return out, true
				}
			}
		}
		return wsInstallMsg{}, false
	}
	msg, ok := find(cmd)
	if !ok {
		t.Fatal("no install result")
	}
	return msg
}

func requestGraph(names ...string) func() *workspace.Graph {
	return func() *workspace.Graph {
		g := wsSmallGraph()
		for _, n := range names {
			g.PluginRequests = append(g.PluginRequests, workspace.PluginRequest{Name: n, Reason: "needed for " + n, Status: workspace.PluginPending})
		}
		return g
	}
}

func TestInspectorSkillsSection(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents["coder"] = config.AgentConfig{Description: "c", UserSkills: []string{"sp:tdd", "notes"}}
	set := plugins.Set{Plugins: []plugins.Plugin{{Name: "sp", Enabled: true, Skills: []plugins.Skill{{ID: "sp:tdd"}, {ID: "sp:debug"}}}}}
	n := &workspace.Node{ID: "T1", Title: "Do", Kind: workspace.KindWork, Status: workspace.StatusTodo, Role: "coder",
		Skills: []string{"sp:debug", "sp:tdd", "sp:ghost"}}
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	out := ansi.Strip(renderInspector(g, n, cfg, set, 46, 30, nil))
	for _, want := range []string{"SKILLS", "sp:tdd (role)", "notes (role)", "sp:debug (lead)", "sp:ghost (lead) (not installed)"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspector lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "sp:tdd") != 1 {
		t.Errorf("a skill of the role and the node is listed twice:\n%s", out)
	}
	// No skills, no section.
	n2 := &workspace.Node{ID: "T2", Title: "Do", Kind: workspace.KindWork, Status: workspace.StatusTodo}
	if out := ansi.Strip(renderInspector(&workspace.Graph{Nodes: []*workspace.Node{n2}}, n2, cfg, set, 46, 30, nil)); strings.Contains(out, "SKILLS") {
		t.Errorf("empty SKILLS section:\n%s", out)
	}
}

func TestInspectorSkillsSanitizedAndBounded(t *testing.T) {
	n := &workspace.Node{ID: "T1", Title: "Do", Kind: workspace.KindWork, Status: workspace.StatusTodo}
	for i := 0; i < 12; i++ {
		n.Skills = append(n.Skills, fmt.Sprintf("p:s%02d", i))
	}
	g := &workspace.Graph{Nodes: []*workspace.Node{n}}
	out := ansi.Strip(renderInspector(g, n, config.DefaultConfig(), plugins.Set{}, 40, 40, nil))
	if !strings.Contains(out, "+") || strings.Count(out, "(lead)") > 6 {
		t.Errorf("skills not bounded:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 40 {
			t.Errorf("line too wide: %q", l)
		}
	}
}

func TestSkillPickerEditsNodeSkills(t *testing.T) {
	h := lpHarness(t)
	h.toGraph()
	if err := h.eng.Assign("T1", "coder"); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.key("K")
	if h.m.overlay != wsOverlaySkills {
		t.Fatalf("K did not open the skill picker:\n%s", h.view())
	}
	h.want("sp:debug")
	h.want("sp:tdd")
	h.want("Write the test first")
	h.want("[ ] sp:debug")
	h.key("space") // toggles the first row (sp:debug)
	h.want("[x] sp:debug")
	h.key("down", "space")
	h.want("[x] sp:tdd")
	h.key("space")
	h.want("[ ] sp:tdd")
	h.key("space", "enter")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("Enter did not close the picker")
	}
	if got := h.m.g.Node("T1").Skills; !reflect.DeepEqual(got, []string{"sp:debug", "sp:tdd"}) {
		t.Errorf("skills = %v", got)
	}
	// Esc discards the edits.
	h.key("K", "space", "esc")
	if got := h.m.g.Node("T1").Skills; len(got) != 2 {
		t.Errorf("Esc changed the skills: %v", got)
	}
}

func TestSkillPickerRoleSkillsAreFixed(t *testing.T) {
	h := lpHarness(t)
	cfg := h.m.cfg
	agents := map[string]config.AgentConfig{}
	for k, v := range cfg.Agents {
		agents[k] = v
	}
	c := agents["coder"]
	c.UserSkills = []string{"sp:tdd"}
	agents["coder"] = c
	cfg.Agents = agents
	h.m.cfg = cfg
	h.toGraph()
	if err := h.eng.Assign("T1", "coder"); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.key("K")
	h.want("[x] sp:tdd")
	h.want("(role)")
	h.key("space") // first row is the role skill: stays on
	h.want("[x] sp:tdd")
	h.key("enter")
	if got := h.m.g.Node("T1").Skills; len(got) != 0 {
		t.Errorf("a role skill leaked into the node skills: %v", got)
	}
}

func TestSkillPickerKeepsMissingSkillsRemovable(t *testing.T) {
	h := lpHarness(t)
	h.graph = func() *workspace.Graph {
		g := wsSmallGraph()
		g.Nodes[0].Skills = []string{"gone:skill"}
		return g
	}
	h.toGraph()
	h.key("K")
	h.want("[x] gone:skill")
	h.want("(not installed)")
	// The node skill is the only checked one; toggle it off and save.
	for i := 0; i < 5 && !strings.Contains(h.flat(), "▸ [x] gone:skill"); i++ {
		h.key("down")
	}
	h.key("space", "enter")
	if got := h.m.g.Node("T1").Skills; len(got) != 0 {
		t.Errorf("skills = %v", got)
	}
}

func TestSkillPickerRefusedForDoneRunningAndCheck(t *testing.T) {
	h := lpHarness(t)
	h.toGraph()
	h.eng.Replace(func(g *workspace.Graph) { g.Node("T1").Status = workspace.StatusDone })
	h.m.g = h.eng.Snapshot()
	h.m.sel = "T1"
	h.key("K")
	if h.m.overlay != wsOverlayNone {
		t.Error("picker opened for a done node")
	}
	h.want("T1 is done")
	h.m.sel = "CHECK"
	h.key("K")
	if h.m.overlay != wsOverlayNone {
		t.Error("picker opened for the check node")
	}
}

func TestPluginRequestDialogFlow(t *testing.T) {
	h := lpHarness(t)
	h.graph = requestGraph("modern-cpp", "modern-python", "feature-dev")
	fake := &fakeInstall{t: t}
	h.m.deps.installPlugin = fake.install
	h.toGraph()
	if err := h.eng.Assign("T1", "coder"); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()

	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "plugin" {
		t.Fatalf("no plugin dialog after the split:\n%s", h.view())
	}
	h.want("Confirm")
	wantText(t, h.m.confirm.text, "The lead wants plugin modern-cpp: needed for modern-cpp.", "Install from github.com/trailofbits/skills?")
	h.want("y install")
	h.want("n skip")
	h.want("a skip all")

	// Nodes do not run while the dialog is open.
	h.key("R", "r")
	for _, n := range h.m.g.Nodes {
		if n.Status != workspace.StatusTodo {
			t.Fatalf("node %s changed while the dialog was open", n.ID)
		}
	}
	if h.m.overlay != wsOverlayConfirm {
		t.Fatal("unrelated keys closed the dialog")
	}

	// y installs; the status line shows it and the next question waits for the result.
	cmd := h.key("y")
	if h.m.installing != "modern-cpp" {
		t.Fatalf("installing = %q", h.m.installing)
	}
	h.want("Installing plugin modern-cpp")
	h.key("r") // running during an install is refused
	h.want("install")
	if h.status("T1") != workspace.StatusTodo {
		t.Error("a node ran during the install")
	}
	h.send(runInstall(t, cmd))
	if !reflect.DeepEqual(fake.calls, []string{"modern-cpp"}) {
		t.Fatalf("installs = %v", fake.calls)
	}
	if st := h.m.plug.State("modern-cpp"); st != plugins.StateEnabled {
		t.Errorf("plugin not reloaded: %v", st)
	}
	h.want("plugin modern-cpp installed")
	// Next question.
	if h.m.overlay != wsOverlayConfirm {
		t.Fatalf("no second dialog:\n%s", h.view())
	}
	wantText(t, h.m.confirm.text, "plugin modern-python")
	h.key("n")
	wantText(t, h.m.confirm.text, "plugin feature-dev")
	h.key("a")
	if h.m.overlay != wsOverlayNone {
		t.Error("dialog still open after skip all")
	}
	want := map[string]string{"modern-cpp": workspace.PluginInstalled, "modern-python": workspace.PluginDeclined, "feature-dev": workspace.PluginDeclined}
	for _, r := range h.m.g.PluginRequests {
		if want[r.Name] != r.Status {
			t.Errorf("%s = %s, want %s", r.Name, r.Status, want[r.Name])
		}
	}
	if len(fake.calls) != 1 {
		t.Errorf("installs = %v", fake.calls)
	}
	// The answers are saved with the session.
	if err := h.m.sess.save(); err != nil {
		t.Fatal(err)
	}
	s, err := workspace.LoadSession(h.root, h.m.sess.snapshot().ID)
	if err != nil || len(s.Graph.PluginRequests) != 3 || len(s.Graph.PendingPlugins()) != 0 {
		t.Errorf("saved requests: %v %+v", err, s.Graph)
	}
	// Nodes run again.
	h.key("R")
	if h.status("T1") == workspace.StatusTodo {
		t.Error("nodes still blocked after the dialogs")
	}
}

func TestPluginRequestEscSkipsAndInstallErrorReported(t *testing.T) {
	h := lpHarness(t)
	h.graph = requestGraph("modern-cpp", "modern-python")
	fake := &fakeInstall{t: t, fail: map[string]error{"modern-python": errors.New("git clone: network down\x1b[31m")}}
	h.m.deps.installPlugin = fake.install
	h.toGraph()
	h.key("esc")
	wantText(t, h.m.confirm.text, "plugin modern-python")
	cmd := h.key("y")
	h.send(runInstall(t, cmd))
	if h.m.overlay != wsOverlayNone || h.m.installing != "" {
		t.Errorf("state after failed install: overlay=%v installing=%q", h.m.overlay, h.m.installing)
	}
	h.want("could not install modern-python")
	h.want("network down")
	if strings.Contains(h.view(), "\x1b[31m") {
		t.Error("escape sequence from the error reached the screen")
	}
	for _, r := range h.m.g.PluginRequests {
		if r.Status != workspace.PluginDeclined {
			t.Errorf("%s = %s", r.Name, r.Status)
		}
	}
	if h.m.plug.State("modern-python") != plugins.StateMissing {
		t.Error("failed install shows as installed")
	}
}

func TestPluginRequestForDisabledPluginEnables(t *testing.T) {
	h := lpHarness(t)
	// A catalog plugin that is installed but disabled.
	wsWritePlugin(t, os.Getenv("HOME"), "modern-cpp", map[string]string{"skills/modern-cpp/SKILL.md": "---\ndescription: d\n---\nbody"})
	if err := plugins.SetEnabled("modern-cpp", false); err != nil {
		t.Fatal(err)
	}
	h.m = h.m.reloadPlugins()
	h.graph = requestGraph("modern-cpp")
	fake := &fakeInstall{t: t}
	h.m.deps.installPlugin = fake.install
	h.toGraph()
	wantText(t, h.m.confirm.text, "It is installed but disabled")
	h.want("y enable")
	h.wantNot("Install from")
	h.key("y")
	if len(fake.calls) != 0 {
		t.Errorf("enable went through the installer: %v", fake.calls)
	}
	if h.m.plug.State("modern-cpp") != plugins.StateEnabled {
		t.Error("plugin not enabled")
	}
	h.want("plugin modern-cpp enabled")
	if h.m.g.PluginRequests[0].Status != workspace.PluginInstalled {
		t.Errorf("status = %s", h.m.g.PluginRequests[0].Status)
	}
}

func TestPluginRequestUnknownNameNeverAsked(t *testing.T) {
	h := lpHarness(t)
	h.graph = requestGraph("not-in-catalog")
	fake := &fakeInstall{t: t}
	h.m.deps.installPlugin = fake.install
	h.toGraph()
	if h.m.overlay == wsOverlayConfirm {
		t.Fatal("asked to install a plugin that is not in the catalog")
	}
	if len(fake.calls) != 0 || h.m.g.PluginRequests[0].Status != workspace.PluginDeclined {
		t.Errorf("calls=%v status=%s", fake.calls, h.m.g.PluginRequests[0].Status)
	}
}

func TestPluginRequestReasonIsSanitized(t *testing.T) {
	h := lpHarness(t)
	h.graph = func() *workspace.Graph {
		g := wsSmallGraph()
		g.PluginRequests = []workspace.PluginRequest{{Name: "modern-cpp", Reason: "C++\x1b]0;evil\x07 code", Status: workspace.PluginPending}}
		return g
	}
	h.toGraph()
	if strings.Contains(h.m.View(), "\x1b]0;") || strings.ContainsRune(h.m.confirm.text, 0x1b) {
		t.Error("control sequence from the reason reached the dialog")
	}
}

func TestSplitWarningsShownInChat(t *testing.T) {
	h := lpHarness(t)
	h.graph = func() *workspace.Graph {
		g := wsSmallGraph()
		g.SplitWarnings = []string{"T1: skill ghost:x is not installed or suggested, dropped"}
		return g
	}
	h.toGraph()
	h.want("ghost:x is not installed or suggested")
}

func TestFollowUpSplitAsksAboutNewRequest(t *testing.T) {
	h := lpHarness(t)
	h.toGraph()
	h.graph = requestGraph("modern-python")
	h.m.deps.installPlugin = (&fakeInstall{t: t}).install
	h.key("tab")
	h.sendTask("add python bits")
	if h.m.overlay != wsOverlayConfirm || !strings.Contains(h.m.confirm.text, "modern-python") {
		t.Fatalf("no dialog after the follow-up:\n%s", h.view())
	}
}

func TestPluginAvailableListsCatalogWithState(t *testing.T) {
	h := lpHarness(t)
	wsWritePlugin(t, os.Getenv("HOME"), "modern-cpp", map[string]string{"skills/modern-cpp/SKILL.md": "---\ndescription: d\n---\nbody"})
	wsWritePlugin(t, os.Getenv("HOME"), "modern-python", map[string]string{"skills/modern-python/SKILL.md": "---\ndescription: d\n---\nbody"})
	if err := plugins.SetEnabled("modern-python", false); err != nil {
		t.Fatal(err)
	}
	h.m = h.m.reloadPlugins()
	h.typeCmd("/plugin available")
	h.want("Plugin catalog")
	lines := h.m.catalogLines()
	for _, want := range []string{"modern-cpp · installed", "modern-python · installed, disabled", "superpowers · not installed"} {
		if !containsLine(lines, want) {
			t.Errorf("catalog lacks %q", want)
		}
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 76 {
			t.Errorf("line of %d cells: %q", ansi.StringWidth(l), l)
		}
	}
}

func TestPluginInstallCommand(t *testing.T) {
	h := lpHarness(t)
	fake := &fakeInstall{t: t}
	h.m.deps.installPlugin = fake.install

	h.typeCmd("/plugin install no-such-plugin")
	h.want("not in the catalog")
	if h.m.overlay == wsOverlayConfirm {
		t.Fatal("confirm opened for an unknown plugin")
	}
	h.typeCmd("/plugin install")
	h.want("usage: /plugin")

	h.typeCmd("/plugin install modern-cpp")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "plugin" {
		t.Fatalf("no confirm:\n%s", h.view())
	}
	wantText(t, h.m.confirm.text, "Install plugin modern-cpp from github.com/trailofbits/skills?")
	h.key("n")
	if len(fake.calls) != 0 || h.m.overlay != wsOverlayNone {
		t.Fatal("n installed or kept the dialog")
	}

	h.typeCmd("/plugin install modern-cpp")
	cmd := h.key("y")
	h.send(runInstall(t, cmd))
	if !reflect.DeepEqual(fake.calls, []string{"modern-cpp"}) {
		t.Fatalf("installs = %v", fake.calls)
	}
	h.want("plugin modern-cpp installed")

	h.typeCmd("/plugin install modern-cpp")
	h.want("already installed")
	if h.m.overlay == wsOverlayConfirm {
		t.Error("confirm opened for an installed plugin")
	}
}

func TestPluginInstallRefusedWhileNodesRun(t *testing.T) {
	h := lpHarness(t)
	h.toGraph()
	h.m.deps.installPlugin = (&fakeInstall{t: t}).install
	if err := h.eng.Assign("T1", "coder"); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.key("r")
	if h.m.running() == 0 {
		t.Fatal("node did not start")
	}
	h.key("tab")
	h.typeCmd("/plugin install modern-cpp")
	h.want(msgStopFirst)
	h.eng.StopAll()
}

func TestSuggestionForPendingPluginRequest(t *testing.T) {
	h := lpHarness(t)
	h.toGraph()
	h.key("tab")
	h.eng.Replace(func(g *workspace.Graph) {
		g.PluginRequests = []workspace.PluginRequest{{Name: "modern-cpp", Status: workspace.PluginPending}, {Name: "x", Status: workspace.PluginPending}}
	})
	h.m.g = h.eng.Snapshot()
	if got := h.m.suggestion(); got != "/plugin install modern-cpp" {
		t.Errorf("suggestion = %q", got)
	}
	h.eng.Replace(func(g *workspace.Graph) {
		g.SetPluginStatus("modern-cpp", workspace.PluginDeclined)
		g.SetPluginStatus("x", workspace.PluginDeclined)
	})
	h.m.g = h.eng.Snapshot()
	if got := h.m.suggestion(); strings.HasPrefix(got, "/plugin") {
		t.Errorf("declined request still suggested: %q", got)
	}
}

func wantText(t *testing.T, text string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(text, s) {
			t.Errorf("%q does not contain %q", text, s)
		}
	}
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
