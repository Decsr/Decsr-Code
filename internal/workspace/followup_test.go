package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const followUp = `===TASKS===
[{"key":"T1","title":"New","description":"d","role":"coder","depends_on":["T3"]},
 {"key":"T2","title":"Next","description":"d","role":"coder","depends_on":["T1"]}]
===END===`

func TestParseSplitWithExternalDependency(t *testing.T) {
	g, err := ParseSplitWith("t", followUp, testRoles(), []string{"T1", "T2", "T3", "CHECK"})
	if err != nil {
		t.Fatal(err)
	}
	// T1 is also an existing ID, but the new task's own key wins.
	if got := g.Node("T1").DependsOn; !reflect.DeepEqual(got, []string{"T3"}) {
		t.Errorf("T1 deps = %v", got)
	}
	if got := g.Node("T2").DependsOn; !reflect.DeepEqual(got, []string{"T1"}) {
		t.Errorf("T2 deps = %v", got)
	}
}

func TestParseSplitWithUnknownStillRejected(t *testing.T) {
	_, err := ParseSplitWith("t", followUp, testRoles(), []string{"T1", "T2"})
	if err == nil || !strings.Contains(err.Error(), "missing node T3") {
		t.Fatalf("err = %v", err)
	}
	// CHECK is never an acceptable external dependency.
	out := strings.Replace(followUp, `"T3"`, `"CHECK"`, 1)
	if _, err := ParseSplitWith("t", out, testRoles(), []string{"CHECK"}); err == nil {
		t.Fatal("dependency on CHECK accepted")
	}
	// Without existing IDs ParseSplit behaves as before.
	if _, err := ParseSplit("t", followUp, testRoles()); err == nil {
		t.Fatal("ParseSplit accepted an unknown dependency")
	}
}

func TestAppendKeepsExistingDependency(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1"), n("T2", "T1"), n("T3", "T2")}}
	g.AddCheckDeferred()
	add, err := ParseSplitWith("t", followUp, testRoles(), []string{"T1", "T2", "T3"})
	if err != nil {
		t.Fatal(err)
	}
	_, idMap := g.Append(add, 4)
	if idMap["T1"] != "T4" || idMap["T2"] != "T5" {
		t.Fatalf("idMap = %v", idMap)
	}
	if got := g.Node("T4").DependsOn; !reflect.DeepEqual(got, []string{"T3"}) {
		t.Errorf("T4 deps = %v (must keep the old T3)", got)
	}
	if got := g.Node("T5").DependsOn; !reflect.DeepEqual(got, []string{"T4"}) {
		t.Errorf("T5 deps = %v (must follow the renumbered T1)", got)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSplitWithPromptAndNumbering(t *testing.T) {
	fake := &chunkRunner{chunks: []string{followUp}}
	_, _, err := SplitNext(t.Context(), fake, "m", "task", testRoles(), "ctx", []string{"T1", "T2", "T3"}, 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := fake.req.SystemPrompt
	for _, want := range []string{"T1, T2, T3", "`T7`", "Never reuse", "MAY list an existing ID"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Contains(p, "{{") {
		t.Error("unreplaced placeholder")
	}
	fake2 := &chunkRunner{chunks: []string{followUp}}
	_, _, _ = SplitWith(t.Context(), fake2, "m", "task", testRoles(), "ctx", []string{"T1", "T3"}, nil)
	if !strings.Contains(fake2.req.SystemPrompt, "`T4`") {
		t.Error("SplitWith must number after the highest T")
	}
	if p := BuildSplitPrompt("x", testRoles(), "c"); strings.Contains(p, "Follow-up") {
		t.Error("fresh split must not mention follow-ups")
	}
}

func TestSplitPromptNoPlaceholderCapture(t *testing.T) {
	p := BuildSplitPrompt("{{PROJECT_CONTEXT}}", testRoles(), "CTX")
	if strings.Count(p, "CTX") != 1 {
		t.Errorf("context substituted into the task text")
	}
}

func TestCompactKeepsNodesUnfinishedNeed(t *testing.T) {
	a, b, c, d := n("T1"), n("T2", "T1"), n("T3"), n("T4", "T3")
	a.Status, b.Status, c.Status = StatusDone, StatusDone, StatusDone
	g := &Graph{Nodes: []*Node{a, b, c, d}}
	g.AddCheckDeferred()
	folded := g.Compact()
	if !reflect.DeepEqual(ids(folded), []string{"T1", "T2"}) {
		t.Fatalf("folded = %v", ids(folded))
	}
	if g.Node("T3") == nil || !reflect.DeepEqual(g.Node("T4").DependsOn, []string{"T3"}) {
		t.Error("T3 must stay for the todo T4")
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFixSeqNeverReused(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1")}}
	g.AddCheckDeferred()
	c := g.Node("CHECK")
	c.Status = StatusFailed
	f1, _ := g.AddFix("CHECK", "x")
	if f1.ID != "F1" {
		t.Fatalf("first fix %s", f1.ID)
	}
	f1.Status = StatusDone
	g.Node("T1").Status = StatusDone
	g.Compact()
	if g.Node("F1") != nil {
		t.Fatal("F1 not compacted")
	}
	c.Status = StatusFailed
	f2, _ := g.AddFix("CHECK", "y")
	if f2.ID != "F2" || g.FixSeq != 2 {
		t.Fatalf("second fix %s seq %d", f2.ID, g.FixSeq)
	}
}

func TestLoadInitialisesFixSeq(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1"), n("F4")}}
	p := filepath.Join(t.TempDir(), "w.json")
	if err := Save(p, g); err != nil {
		t.Fatal(err)
	}
	l, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if l.FixSeq != 4 {
		t.Fatalf("FixSeq = %d", l.FixSeq)
	}
}

func TestSanitize(t *testing.T) {
	in := "a\x1b[2Jb\x1b]52;c;ZXZpbA==\x07c\x07d\r\ne\tf"
	if got := Sanitize(in); strings.ContainsAny(got, "\x1b\x07\r\n\t") || got != "abcd e    f" {
		t.Errorf("Sanitize = %q", got)
	}
	if got := SanitizeText("x\x1b[31my\nz"); got != "xy\nz" {
		t.Errorf("SanitizeText = %q", got)
	}
}

func TestParseSplitSanitizesLeadText(t *testing.T) {
	out := "===TITLE===\nT\x1b[2Jitle\x1b]52;c;QQ==\x07\n===TASKS===\n" +
		`[{"key":"T1","title":"a\u001b[31mb","description":"x\u001b]0;pwn\u0007y","role":"coder"}]` + "\n===END==="
	g, err := ParseSplit("t", out, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{g.Title, g.Nodes[0].Title, g.Nodes[0].Description} {
		if strings.ContainsRune(s, '\x1b') || strings.ContainsRune(s, '\a') {
			t.Errorf("unsanitized %q", s)
		}
	}
}

func TestSessionLoadSanitizes(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	s.Title = "t\x1b[2Jx\x1b]52;c;QQ==\x07"
	s.AddLine("lead", "hello\x1b[31m red")
	writeSessionRaw(t, root, s)
	l, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(l.Title, '\x1b') || strings.ContainsRune(l.Transcript[0].Text, '\x1b') || l.Title != "tx" {
		t.Errorf("title %q line %q", l.Title, l.Transcript[0].Text)
	}
}

func TestSessionSizeCapAndCount(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	big := NewSession()
	p := filepath.Join(SessionsDir(root), big.ID+".json")
	if err := os.WriteFile(p, []byte(`{"id":"`+big.ID+`","title":"`+strings.Repeat("x", 9<<20)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(SessionsDir(root), "notes.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(root, big.ID); err == nil {
		t.Error("oversized session loaded")
	}
	if got := ListSessions(root); len(got) != 1 || got[0].ID != s.ID {
		t.Errorf("list = %+v", got)
	}
	if got := CountSessions(root); got != 2 {
		t.Errorf("count = %d (valid ids only, content not read)", got)
	}
}

func TestCustomCommandSanitizeAndNUL(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeCmd(t, root, "evil.md", "# Do\x1b[2J it\x1b]52;c;QQ==\x07\nbody")
	writeCmd(t, root, "nul.md", "# x\nbo\x00dy")
	cs := LoadCustomCommands(root)
	if len(cs) != 1 || cs[0].Name != "evil" || strings.ContainsRune(cs[0].Description, '\x1b') || cs[0].Description != "Do it" {
		t.Fatalf("commands = %+v", cs)
	}
}

func TestRoleDescriptionSanitized(t *testing.T) {
	cfg := testCfg()
	ac := cfg.Agents["alpha"]
	ac.Description = "A\x1b[2J role\x1b]52;c;QQ==\x07"
	cfg.Agents["alpha"] = ac
	r, _ := FindRole(cfg, "alpha")
	if r.Description != "A role" {
		t.Errorf("description %q", r.Description)
	}
}

func TestTailLines(t *testing.T) {
	s := "aaaa\nbbbb\ncccc"
	if got := tailLines(s, 9); got != "cccc" {
		t.Errorf("got %q", got)
	}
	if got := tailLines(s, 100); got != s {
		t.Errorf("got %q", got)
	}
}

func TestWorkContextHasSessionSummary(t *testing.T) {
	x := &executor{projectContext: "PC", summary: func() string { return "- T1 did things" }}
	g := &Graph{Nodes: []*Node{n("T2")}}
	got := x.workContext(g, g.Node("T2"))
	if !strings.Contains(got, "## Earlier in this session\n- T1 did things") {
		t.Errorf("context = %q", got)
	}
	x.summary = nil
	if strings.Contains(x.workContext(g, g.Node("T2")), "Earlier in this session") {
		t.Error("section without summary")
	}
}
