package workspace

import (
	"reflect"
	"strings"
	"testing"
)

func n(id string, deps ...string) *Node {
	return &Node{ID: id, Title: id, Kind: KindWork, Status: StatusTodo, DependsOn: deps}
}

func ids(ns []*Node) []string {
	out := []string{}
	for _, x := range ns {
		out = append(out, x.ID)
	}
	return out
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		nodes []*Node
		want  string // substring of the error; "" = ok
	}{
		{"ok", []*Node{n("T1"), n("T2", "T1")}, ""},
		{"duplicate", []*Node{n("T1"), n("T1")}, "T1"},
		{"empty id", []*Node{n("")}, "empty"},
		{"missing dep", []*Node{n("T1", "T9")}, "T9"},
		{"self dep", []*Node{n("T1", "T1")}, "T1"},
		{"cycle", []*Node{n("T1", "T3"), n("T2", "T1"), n("T3", "T2")}, "T"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Graph{Nodes: tc.nodes}).Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateCycleNamesNode(t *testing.T) {
	err := (&Graph{Nodes: []*Node{n("A", "C"), n("B", "A"), n("C", "B")}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v", err)
	}
}

func TestLayers(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1"), n("T2", "T1"), n("T3", "T1"), n("T4", "T2", "T3")}}
	var got [][]string
	for _, l := range g.Layers() {
		got = append(got, ids(l))
	}
	want := [][]string{{"T1"}, {"T2", "T3"}, {"T4"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layers = %v, want %v", got, want)
	}
}

func TestRunnable(t *testing.T) {
	assigned := func(id string, st Status, deps ...string) *Node {
		x := n(id, deps...)
		x.Role = "coder"
		x.Status = st
		return x
	}
	chk := &Node{ID: "CHECK", Kind: KindCheck, Status: StatusTodo, DependsOn: []string{"A"}}
	g := &Graph{Nodes: []*Node{
		assigned("A", StatusDone),
		n("U"), // unassigned
		assigned("B", StatusTodo, "U"),
		assigned("C", StatusRunning),
		assigned("D", StatusFailed),
		assigned("E", StatusTodo, "A"),
		chk,
	}}
	got := ids(g.Runnable())
	want := []string{"E", "CHECK"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runnable = %v, want %v", got, want)
	}
}

func TestBlockers(t *testing.T) {
	a, b := n("A"), n("B")
	a.Status = StatusDone
	c := n("C", "B", "A")
	g := &Graph{Nodes: []*Node{a, b, c}}
	if got := g.Blockers("C"); !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("blockers = %v", got)
	}
	if got := g.Blockers("A"); len(got) != 0 {
		t.Fatalf("blockers = %v", got)
	}
}

func TestAddCheck(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1"), n("T2", "T1")}}
	if g.AddCheck(nil) != nil || len(g.Nodes) != 2 {
		t.Fatal("empty commands must be a no-op")
	}
	c := g.AddCheck([]string{"go test ./..."})
	if c == nil || c.ID != "CHECK" || c.Kind != KindCheck || c.Title != "Build & test" {
		t.Fatalf("check = %+v", c)
	}
	if !reflect.DeepEqual(c.DependsOn, []string{"T1", "T2"}) || !g.Assigned(c) {
		t.Fatalf("deps = %v", c.DependsOn)
	}
	if g.AddCheck([]string{"x"}) != nil || len(g.Nodes) != 3 {
		t.Fatal("must be idempotent")
	}
}

func TestAddFix(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1")}}
	c := g.AddCheck([]string{"go test"})
	c.Status = StatusFailed
	c.Error = "boom"
	long := strings.Repeat("x", 5000) + "TAIL"
	f, err := g.AddFix("CHECK", long)
	if err != nil {
		t.Fatal(err)
	}
	if f.ID != "F1" || f.Kind != KindWork || f.SuggestedRole != "coder_fixer" || f.Title != "Fix: Build & test" {
		t.Fatalf("fix = %+v", f)
	}
	if !strings.Contains(f.Description, "TAIL") || strings.Count(f.Description, "x") > 4000 {
		t.Fatalf("failure not truncated to last 4000 bytes (len %d)", len(f.Description))
	}
	if !reflect.DeepEqual(f.DependsOn, []string{"T1"}) {
		t.Fatalf("fix deps = %v", f.DependsOn)
	}
	if c.Status != StatusTodo || c.Error != "" || !reflect.DeepEqual(c.DependsOn, []string{"T1", "F1"}) {
		t.Fatalf("check = %+v", c)
	}
	f2, err := g.AddFix("CHECK", "again")
	if err != nil || f2.ID != "F2" {
		t.Fatalf("f2 = %+v, %v", f2, err)
	}
	if _, err := g.AddFix("T1", "x"); err == nil {
		t.Fatal("expected error for non-check node")
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCloneIsDeep(t *testing.T) {
	g := &Graph{Task: "t", Nodes: []*Node{n("T1"), n("T2", "T1")}, NewRoles: []NewRole{{Name: "a"}}}
	c := g.Clone()
	c.Nodes[1].Title = "changed"
	c.Nodes[1].DependsOn[0] = "ZZ"
	c.NewRoles[0].Name = "b"
	if g.Nodes[1].Title != "T2" || g.Nodes[1].DependsOn[0] != "T1" || g.NewRoles[0].Name != "a" {
		t.Fatal("clone shares state")
	}
}

func TestCounts(t *testing.T) {
	a, b, c := n("A"), n("B"), n("C")
	a.Status, b.Status, c.Status = StatusDone, StatusFailed, StatusRunning
	todo, run, done, failed := (&Graph{Nodes: []*Node{a, b, c, n("D")}}).Counts()
	if todo != 1 || run != 1 || done != 1 || failed != 1 {
		t.Fatalf("%d %d %d %d", todo, run, done, failed)
	}
}

func TestAddCheckDeferred(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1"), n("T2", "T1")}}
	c := g.AddCheckDeferred()
	if c == nil || c.ID != "CHECK" || c.Kind != KindCheck || len(c.Commands) != 0 {
		t.Fatalf("check = %+v", c)
	}
	if !reflect.DeepEqual(c.DependsOn, []string{"T1", "T2"}) || !g.Assigned(c) {
		t.Fatalf("deps = %v", c.DependsOn)
	}
	if g.AddCheckDeferred() != nil || g.AddCheck([]string{"x"}) != nil || len(g.Nodes) != 3 {
		t.Fatal("must be a no-op once CHECK exists")
	}
}

func TestCloneCopiesWarnings(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Warnings: []string{"w"}}}}
	c := g.Clone()
	c.Nodes[0].Warnings[0] = "changed"
	if g.Nodes[0].Warnings[0] != "w" {
		t.Fatal("clone shares Warnings")
	}
}
