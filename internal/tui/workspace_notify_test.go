package tui

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

type wsNoteLog struct {
	mu    sync.Mutex
	notes []notify.Note
}

func (l *wsNoteLog) send(_ config.NotifyConfig, n notify.Note) {
	l.mu.Lock()
	l.notes = append(l.notes, n)
	l.mu.Unlock()
}

func (l *wsNoteLog) bodies() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, n := range l.notes {
		out = append(out, n.Body)
	}
	return out
}

func (l *wsNoteLog) find(prefix string) (notify.Note, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, n := range l.notes {
		if strings.HasPrefix(n.Body, prefix) {
			return n, true
		}
	}
	return notify.Note{}, false
}

func newNotifyHarness(t *testing.T) (*wsHarness, *wsNoteLog) {
	t.Helper()
	h := newWSHarness(t)
	log := &wsNoteLog{}
	h.m.deps.notify = log.send
	return h, log
}

func oneWorkGraph() *workspace.Graph {
	return &workspace.Graph{Task: "t", Title: "T", Nodes: []*workspace.Node{
		{ID: "T1", Title: "One", Kind: workspace.KindWork, Status: workspace.StatusTodo, SuggestedRole: "coder"},
	}}
}

// pumpNote feeds engine events until a note starting with prefix was sent.
func pumpNote(h *wsHarness, log *wsNoteLog, prefix string) {
	h.t.Helper()
	h.pump(func(workspaceModel) bool { _, ok := log.find(prefix); return ok })
}

func TestNotify_SplitNeedsRoles(t *testing.T) {
	h, log := newNotifyHarness(t)
	h.toGraph()
	n, ok := log.find("Assign roles")
	if !ok {
		t.Fatalf("no assign-roles note, got %v", log.bodies())
	}
	if n.Body != "Assign roles: 5 sub-tasks are waiting" || n.Level != notify.NeedsYou {
		t.Errorf("note = %+v", n)
	}
	if !strings.HasPrefix(n.Title, "decsr") {
		t.Errorf("title = %q", n.Title)
	}
}

func TestNotify_NodeFailedThenRunFinished(t *testing.T) {
	h, log := newNotifyHarness(t)
	h.graph = oneWorkGraph
	h.toGraph()
	if err := h.m.eng.Assign("T1", "coder"); err != nil {
		t.Fatal(err)
	}
	h.m.eng.RunAll(h.m.runCtx)
	h.exec.release("T1", errors.New("boom happened\nsecond line"))
	pumpNote(h, log, "Run finished")
	n, ok := log.find("T1 failed")
	if !ok || n.Body != "T1 failed: boom happened" || n.Level != notify.Failed {
		t.Fatalf("failure note = %+v (%v)", n, log.bodies())
	}
	if n, ok := log.find("Run finished"); !ok || n.Body != "Run finished: 0 done, 1 failed" {
		t.Errorf("run-finished note = %+v (%v)", n, log.bodies())
	}
	for _, b := range log.bodies() {
		if strings.Contains(b, "working on") {
			t.Errorf("output chunk notified: %q", b)
		}
	}
}

func TestNotify_BuildAndTestPassed(t *testing.T) {
	for _, iso := range []bool{false, true} {
		h, log := newNotifyHarness(t)
		h.graph = oneWorkGraph
		h.toGraph()
		if iso {
			h.m.iso = &workspace.Isolator{}
		}
		if err := h.m.eng.Assign("T1", "coder"); err != nil {
			t.Fatal(err)
		}
		h.m.eng.RunAll(h.m.runCtx)
		h.exec.release("T1", nil)
		h.exec.release("CHECK", nil)
		pumpNote(h, log, "Build & test")
		want := "Build & test passed"
		if iso {
			want += " — /merge to apply"
		}
		n, ok := log.find("Build & test passed")
		if !ok || n.Body != want || n.Level != notify.Done {
			t.Errorf("iso=%v note = %+v (%v)", iso, n, log.bodies())
		}
		if n, ok := log.find("Run finished"); !ok || n.Body != "Run finished: 2 done, 0 failed" {
			t.Errorf("iso=%v run-finished = %+v", iso, n)
		}
	}
}

func TestNotify_BuildAndTestFailed(t *testing.T) {
	h, log := newNotifyHarness(t)
	h.graph = oneWorkGraph
	h.toGraph()
	_ = h.m.eng.Assign("T1", "coder")
	h.m.eng.RunAll(h.m.runCtx)
	h.exec.release("T1", nil)
	h.exec.release("CHECK", errors.New("go test failed"))
	pumpNote(h, log, "Build & test")
	if n, ok := log.find("Build & test failed"); !ok || n.Level != notify.Failed || !strings.Contains(n.Body, "go test failed") {
		t.Errorf("note = %+v (%v)", n, log.bodies())
	}
}

func TestNotify_StoppedIsSilent(t *testing.T) {
	h, log := newNotifyHarness(t)
	h.graph = oneWorkGraph
	h.toGraph()
	before := len(log.bodies())
	h.m.notifyEvent(workspace.Event{NodeID: "T1", Type: workspace.EventFailed, Text: "stopped"})
	h.m.notifyEvent(workspace.Event{NodeID: "T1", Type: workspace.EventStarted})
	h.m.notifyEvent(workspace.Event{NodeID: "T1", Type: workspace.EventOutput, Text: "x"})
	if got := len(log.bodies()); got != before {
		t.Errorf("silent events sent notes: %v", log.bodies())
	}
}

func TestNotify_NoSenderIsSafeAndBodyIsShort(t *testing.T) {
	h := newWSHarness(t)
	h.m.notifyNote(notify.Info, "nobody listens") // deps.notify is nil: must not panic
	log := &wsNoteLog{}
	h.m.deps.notify = log.send
	h.m.notifyNote(notify.Failed, strings.Repeat("é", 400)+"\nsecond")
	b := log.bodies()[0]
	if got := len([]rune(b)); got > wsNoteMax || strings.Contains(b, "second") {
		t.Errorf("body not shortened: %d runes %q", got, b)
	}
}

func TestNotifyTitle(t *testing.T) {
	h := newWSHarness(t)
	h.m.cfg.Project.Name = "shop"
	if got := h.m.notifyTitle(); got != "decsr · shop" {
		t.Errorf("title = %q", got)
	}
}

func TestNotify_PluginQuestionNeedsYou(t *testing.T) {
	h, log := newNotifyHarness(t)
	h.graph = requestGraph("modern-cpp")
	h.toGraph()
	if h.m.overlay != wsOverlayConfirm {
		t.Skip("plugin dialog not opened in this environment")
	}
	n, ok := log.find("Plugin question")
	if !ok || n.Level != notify.NeedsYou || !strings.Contains(n.Body, "modern-cpp") {
		t.Errorf("note = %+v (%v)", n, log.bodies())
	}
}
