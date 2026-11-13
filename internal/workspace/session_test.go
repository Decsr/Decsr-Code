package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	s.Title = "Title"
	s.Summary = "sum"
	s.NextID = 4
	s.In, s.Out = 10, 20
	s.AddLine("you", "hello")
	s.Graph = &Graph{Task: "t", Nodes: []*Node{n("T1"), n("T2", "T1")}}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(SessionsDir(root), s.ID+".json")
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", st, err)
	}
	if s.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not set")
	}
	got, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != s.ID || got.Title != "Title" || got.Summary != "sum" || got.NextID != 4 || got.In != 10 || got.Out != 20 {
		t.Fatalf("got %+v", got)
	}
	if len(got.Transcript) != 1 || got.Transcript[0].Who != "you" || got.Transcript[0].Text != "hello" {
		t.Fatalf("transcript %+v", got.Transcript)
	}
	if !reflect.DeepEqual(got.Graph.Nodes[1].DependsOn, []string{"T1"}) {
		t.Fatalf("graph %+v", got.Graph)
	}
	entries, _ := os.ReadDir(SessionsDir(root))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestSessionIDFormatAndUniqueness(t *testing.T) {
	re := regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := NewSession()
		if !re.MatchString(s.ID) {
			t.Fatalf("bad id %q", s.ID)
		}
		if seen[s.ID] {
			t.Fatalf("duplicate id %q", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestLoadSessionRejectsBadID(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"../x", "", "a/b", "20261113-104512-zzzz", ".."} {
		if _, err := LoadSession(root, id); err == nil {
			t.Errorf("LoadSession(%q) should fail", id)
		}
		if err := DeleteSession(root, id); err == nil {
			t.Errorf("DeleteSession(%q) should fail", id)
		}
	}
}

func TestLoadSessionResetsRunning(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	a := n("T1")
	a.Status = StatusRunning
	s.Graph = &Graph{Nodes: []*Node{a}}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Graph.Nodes[0].Status != StatusTodo {
		t.Errorf("status = %s", got.Graph.Nodes[0].Status)
	}
}

func TestListSessions(t *testing.T) {
	root := t.TempDir()
	if got := ListSessions(root); len(got) != 0 {
		t.Fatalf("empty = %v", got)
	}
	mk := func(title string, at time.Time, statuses ...Status) *Session {
		s := NewSession()
		s.Title = title
		g := &Graph{}
		for i, st := range statuses {
			x := n("T" + string(rune('1'+i)))
			x.Status = st
			g.Nodes = append(g.Nodes, x)
		}
		s.Graph = g
		if err := s.Save(root); err != nil {
			t.Fatal(err)
		}
		// force a distinct, known UpdatedAt
		s.UpdatedAt = at
		writeSessionRaw(t, root, s)
		return s
	}
	now := time.Now()
	old := mk("old", now.Add(-time.Hour), StatusDone)
	nw := mk("new", now, StatusTodo, StatusFailed, StatusDone)
	dir := SessionsDir(root)
	if err := os.WriteFile(filepath.Join(dir, "20260101-000000-abcd.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ListSessions(root)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != nw.ID || got[1].ID != old.ID {
		t.Fatalf("order %v %v", got[0].ID, got[1].ID)
	}
	if got[0].Title != "new" || got[0].Todo != 1 || got[0].Failed != 1 || got[0].Done != 1 || got[0].Running != 0 {
		t.Errorf("info %+v", got[0])
	}
	if got[0].Path != filepath.Join(dir, nw.ID+".json") {
		t.Errorf("path %q", got[0].Path)
	}
	// Another project does not see them.
	if other := ListSessions(t.TempDir()); len(other) != 0 {
		t.Errorf("other project sees %v", other)
	}
}

// writeSessionRaw writes s as is (without touching UpdatedAt).
func writeSessionRaw(t *testing.T, root string, s *Session) {
	t.Helper()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(filepath.Join(SessionsDir(root), s.ID+".json"), data, ".session-*.tmp"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteSession(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSession(root, s.ID); err != nil {
		t.Fatal(err)
	}
	if len(ListSessions(root)) != 0 {
		t.Error("still listed")
	}
}

func TestTranscriptCap(t *testing.T) {
	s := NewSession()
	for i := 0; i < 600; i++ {
		s.AddLine("you", strings.Repeat("x", 1)+string(rune('a'+i%26)))
	}
	if len(s.Transcript) != 500 {
		t.Fatalf("len = %d", len(s.Transcript))
	}
	s.AddLine("lead", "last")
	if len(s.Transcript) != 500 || s.Transcript[499].Text != "last" {
		t.Fatalf("cap/last wrong: %d %q", len(s.Transcript), s.Transcript[499].Text)
	}
	if s.Transcript[499].At.IsZero() {
		t.Error("At not set")
	}
}

func writeLegacy(t *testing.T, root string, g *Graph) string {
	t.Helper()
	p := filepath.Join(root, ".decsr", FileName)
	if err := Save(p, g); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMigrateLegacy(t *testing.T) {
	root := t.TempDir()
	if s, err := MigrateLegacy(root); s != nil || err != nil {
		t.Fatalf("no legacy: %v %v", s, err)
	}
	p := writeLegacy(t, root, &Graph{Task: "do", Title: "My title", Nodes: []*Node{n("T1"), n("T7", "T1")}})
	s, err := MigrateLegacy(root)
	if err != nil || s == nil {
		t.Fatalf("migrate: %v %v", s, err)
	}
	if s.Title != "My title" || s.Graph == nil || len(s.Graph.Nodes) != 2 || s.NextID != 8 {
		t.Fatalf("session %+v", s)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("legacy file still there")
	}
	if _, err := os.Stat(p + ".migrated"); err != nil {
		t.Errorf("migrated file: %v", err)
	}
	if _, err := LoadSession(root, s.ID); err != nil {
		t.Errorf("not saved: %v", err)
	}
	if s2, err := MigrateLegacy(root); s2 != nil || err != nil {
		t.Fatalf("second call: %v %v", s2, err)
	}
}

func TestMigrateLegacySkippedWithExistingSessions(t *testing.T) {
	root := t.TempDir()
	if err := NewSession().Save(root); err != nil {
		t.Fatal(err)
	}
	p := writeLegacy(t, root, &Graph{Nodes: []*Node{n("T1")}})
	if s, err := MigrateLegacy(root); s != nil || err != nil {
		t.Fatalf("got %v %v", s, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("legacy file must stay: %v", err)
	}
}

func TestMigrateLegacyCorrupt(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".decsr", FileName)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err := MigrateLegacy(root); s != nil || err == nil {
		t.Fatalf("got %v %v", s, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("corrupt legacy file must stay")
	}
}

// --- Graph.Append ---

func TestAppendContinuesIDs(t *testing.T) {
	g := &Graph{Task: "first", Title: "A", Nodes: []*Node{n("T1"), n("T2", "T1")}}
	g.AddCheckDeferred()
	g.Node("CHECK").Status = StatusDone
	g.Node("CHECK").Error = "x"
	g.Node("CHECK").Summary = "ok"
	g.Node("CHECK").Warnings = []string{"w"}
	g.Node("T1").Status = StatusDone
	g.Node("T2").Status = StatusDone
	g.NewRoles = []NewRole{{Name: "a", Description: "da"}}

	add := &Graph{Task: "second", Nodes: []*Node{n("T1"), n("T2", "T1"), n("T3", "T1", "T2", "CHECK", "ghost")}}
	add.AddCheckDeferred()
	add.NewRoles = []NewRole{{Name: "a", Description: "dup"}, {Name: "b", Description: "db"}}

	next, idMap := g.Append(add, 5)
	if next != 8 {
		t.Fatalf("next = %d", next)
	}
	want := map[string]string{"T1": "T5", "T2": "T6", "T3": "T7"}
	if !reflect.DeepEqual(idMap, want) {
		t.Fatalf("idMap = %v", idMap)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := g.Node("T6").DependsOn; !reflect.DeepEqual(got, []string{"T5"}) {
		t.Errorf("T6 deps = %v", got)
	}
	if got := g.Node("T7").DependsOn; !reflect.DeepEqual(got, []string{"T5", "T6"}) {
		t.Errorf("T7 deps = %v", got)
	}
	// add must not be mutated.
	if add.Node("T1") == nil || add.Node("T5") != nil {
		t.Error("add graph was modified")
	}
	// CHECK: exactly one, last, depends on every work node, reset.
	checks := 0
	for _, x := range g.Nodes {
		if x.Kind == KindCheck {
			checks++
		}
	}
	c := g.Node("CHECK")
	if checks != 1 || g.Nodes[len(g.Nodes)-1] != c {
		t.Fatalf("check count %d, last %s", checks, g.Nodes[len(g.Nodes)-1].ID)
	}
	if c.Status != StatusTodo || c.Error != "" || c.Summary != "" || len(c.Warnings) != 0 {
		t.Errorf("check not reset: %+v", c)
	}
	if !reflect.DeepEqual(c.DependsOn, []string{"T1", "T2", "T5", "T6", "T7"}) {
		t.Errorf("check deps = %v", c.DependsOn)
	}
	if len(g.NewRoles) != 2 || g.NewRoles[0].Description != "da" || g.NewRoles[1].Name != "b" {
		t.Errorf("roles = %+v", g.NewRoles)
	}
	if g.Task != "first" || g.Title != "A" {
		t.Errorf("task/title overwritten: %q %q", g.Task, g.Title)
	}
}

func TestAppendAvoidsExistingIDs(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1"), n("T9")}}
	next, idMap := g.Append(&Graph{Nodes: []*Node{n("T1")}}, 2)
	if idMap["T1"] != "T10" || next != 11 {
		t.Fatalf("idMap %v next %d", idMap, next)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAppendFixNodeIncludedInCheck(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1")}}
	g.AddCheckDeferred()
	g.Node("CHECK").Status = StatusFailed
	g.Node("CHECK").Error = "boom"
	f, err := g.AddFix("CHECK", "boom")
	if err != nil {
		t.Fatal(err)
	}
	g.Append(&Graph{Nodes: []*Node{n("T1")}}, 2)
	deps := g.Node("CHECK").DependsOn
	found := false
	for _, d := range deps {
		if d == f.ID {
			found = true
		}
	}
	if !found || len(deps) != 3 {
		t.Errorf("check deps = %v", deps)
	}
}

func TestAppendOntoEmptyGraph(t *testing.T) {
	g := &Graph{}
	next, idMap := g.Append(&Graph{Task: "t", Title: "ti", Nodes: []*Node{n("A"), n("B", "A")}}, 1)
	if next != 3 || idMap["A"] != "T1" || idMap["B"] != "T2" {
		t.Fatalf("next %d map %v", next, idMap)
	}
	if g.Task != "t" || g.Title != "ti" {
		t.Errorf("task/title %q %q", g.Task, g.Title)
	}
	c := g.Node("CHECK")
	if c == nil || c.Status != StatusTodo || !reflect.DeepEqual(c.DependsOn, []string{"T1", "T2"}) {
		t.Fatalf("check %+v", c)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	// nextID below 1 still yields T1.
	g2 := &Graph{}
	if next, m := g2.Append(&Graph{Nodes: []*Node{n("A")}}, 0); next != 2 || m["A"] != "T1" {
		t.Errorf("zero nextID: %d %v", next, m)
	}
}

// --- Graph.Compact ---

func TestCompactMixedStates(t *testing.T) {
	a, b, c, d := n("T1"), n("T2", "T1"), n("T3"), n("T4", "T3")
	a.Status, b.Status = StatusDone, StatusDone
	c.Status = StatusFailed
	g := &Graph{Nodes: []*Node{a, b, c, d}}
	g.AddCheckDeferred()
	g.Node("CHECK").Status = StatusDone

	folded := g.Compact()
	if !reflect.DeepEqual(ids(folded), []string{"T1", "T2"}) {
		t.Fatalf("folded = %v", ids(folded))
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if g.Node("T1") != nil || g.Node("T2") != nil {
		t.Error("done nodes remain")
	}
	if got := g.Node("T3").DependsOn; len(got) != 0 {
		t.Errorf("T3 deps = %v", got)
	}
	if got := g.Node("T4").DependsOn; !reflect.DeepEqual(got, []string{"T3"}) {
		t.Errorf("T4 deps = %v", got)
	}
	if g.Node("CHECK") == nil {
		t.Error("check removed")
	}
	// The todo dependent of a removed done node is runnable-shaped (no blockers).
	if len(g.Blockers("T3")) != 0 {
		t.Errorf("blockers %v", g.Blockers("T3"))
	}
	// Twice is a no-op.
	if again := g.Compact(); len(again) != 0 {
		t.Errorf("second compact folded %v", ids(again))
	}
}

func TestCompactKeepsDoneNodeWithUnfinishedDependency(t *testing.T) {
	x, y, z := n("T1"), n("T2", "T1"), n("T3", "T2")
	x.Status = StatusFailed
	y.Status = StatusDone // done although its dependency failed (reset later)
	g := &Graph{Nodes: []*Node{x, y, z}}
	folded := g.Compact()
	if len(folded) != 0 {
		t.Fatalf("folded %v", ids(folded))
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCompactAllDone(t *testing.T) {
	a, b := n("T1"), n("T2", "T1")
	a.Status, b.Status = StatusDone, StatusDone
	g := &Graph{Nodes: []*Node{a, b}}
	g.AddCheckDeferred()
	folded := g.Compact()
	if len(folded) != 2 || len(g.Nodes) != 1 || g.Nodes[0].ID != "CHECK" || len(g.Nodes[0].DependsOn) != 0 {
		t.Fatalf("folded %v nodes %+v", ids(folded), g.Nodes)
	}
}

// --- CompactSummary ---

func TestCompactSummaryFormat(t *testing.T) {
	a := n("T1")
	a.Title = "Parser"
	a.Role = "coder"
	a.Summary = "wrote\nthe parser"
	a.Files = []string{"a.go", "b.go"}
	b := n("T2")
	b.Title = "Docs"
	b.Role = "qa"
	b.Summary = strings.Repeat("x", 600)
	got := CompactSummary("", []*Node{a, b})
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("got %q", got)
	}
	if lines[0] != "- T1 Parser [coder]: wrote the parser (files: a.go, b.go)" {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "- T2 Docs [qa]: "+strings.Repeat("x", 400) {
		t.Errorf("line 1 = %q", lines[1])
	}
	got2 := CompactSummary(got, []*Node{n("T3")})
	if !strings.HasPrefix(got2, got+"\n- T3 T3") {
		t.Errorf("prev not kept: %q", got2)
	}
	if CompactSummary("only prev", nil) != "only prev" {
		t.Error("prev only")
	}
}

func TestCompactSummaryCap(t *testing.T) {
	var prev string
	for i := 0; i < 100; i++ {
		x := n("T" + string(rune('A'+i%26)))
		x.Title = strings.Repeat("t", 100)
		x.Summary = strings.Repeat("s", 400)
		prev = CompactSummary(prev, []*Node{x})
	}
	if len(prev) > 12000 {
		t.Fatalf("len = %d", len(prev))
	}
	for _, l := range strings.Split(prev, "\n") {
		if !strings.HasPrefix(l, "- T") {
			t.Fatalf("partial line %q", l[:20])
		}
	}
	x := n("TZ")
	x.Title = "newest"
	if last := CompactSummary(prev, []*Node{x}); !strings.HasSuffix(last, "- TZ newest") {
		t.Errorf("newest missing: %q", last[len(last)-30:])
	}
}

// --- Engine.Replace / NewEngineWithSave ---

func TestEngineReplace(t *testing.T) {
	var saved []*Graph
	g := &Graph{Nodes: []*Node{n("T1")}}
	e := NewEngineWithSave(g, newFakeExec(), 2, func(g *Graph) error {
		saved = append(saved, g.Clone())
		return nil
	})
	if err := e.Replace(func(g *Graph) { g.Nodes = append(g.Nodes, n("T2")) }); err != nil {
		t.Fatal(err)
	}
	if len(e.Snapshot().Nodes) != 2 {
		t.Error("mutation not visible")
	}
	if len(saved) != 1 || len(saved[0].Nodes) != 2 {
		t.Errorf("saved %d", len(saved))
	}
}

func TestEngineReplaceRefusedWhileRunning(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1")}}
	g.Nodes[0].Role = "coder"
	f := newFakeExec()
	release := make(chan struct{})
	f.behavior["T1"] = blockUntil(release)
	saves := 0
	e := NewEngineWithSave(g, f, 1, func(*Graph) error { saves++; return nil })
	if err := e.Run(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}
	<-f.started
	called := false
	err := e.Replace(func(*Graph) { called = true })
	if err == nil || err.Error() != "stop the running nodes first" || called {
		t.Fatalf("err = %v called = %v", err, called)
	}
	close(release)
	e.Close()
}

func TestNewEngineKeepsFileSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	e := NewEngine(&Graph{Nodes: []*Node{n("T1")}}, newFakeExec(), 1, path)
	if err := e.Replace(func(g *Graph) { g.Task = "x" }); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Task != "x" {
		t.Fatalf("%v %v", got, err)
	}
}
