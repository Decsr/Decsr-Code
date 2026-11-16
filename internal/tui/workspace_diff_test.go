package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func diffGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func diffWrite(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func numbered(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s line %d\n", prefix, i)
	}
	return b.String()
}

// diffHarness is a harness over a git repository whose two files have
// uncommitted edits that belong to the done node T1 (shared checkout).
func diffHarness(t *testing.T) *wsHarness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	diffGit(t, repo, "init", "-q")
	diffGit(t, repo, "config", "user.name", "tester")
	diffGit(t, repo, "config", "user.email", "t@example.com")
	diffWrite(t, repo, "a.go", numbered("a", 40))
	diffWrite(t, repo, "b.go", numbered("b", 10))
	diffGit(t, repo, "add", "-A")
	diffGit(t, repo, "commit", "-q", "-m", "init")
	a := strings.Replace(numbered("a", 40), "a line 5\n", "a line 5 changed\nextra\n", 1)
	a = strings.Replace(a, "a line 30\n", "a line 30 changed with a very long tail "+strings.Repeat("x", 200)+"\n", 1)
	diffWrite(t, repo, "a.go", a)
	diffWrite(t, repo, "b.go", strings.Replace(numbered("b", 10), "b line 2\n", "", 1))
	h := newWSHarnessAt(t, repo)
	h.toGraph()
	h.wsMarkDone("T1")
	if err := h.eng.Replace(func(g *workspace.Graph) { g.Node("T1").Files = []string{"a.go", "b.go"} }); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.m.sel = "T1"
	return h
}

func (h *wsHarness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.key(string(r))
	}
}

func TestDiffOpensWithD(t *testing.T) {
	h := diffHarness(t)
	h.key("d")
	if h.m.screen != wsScreenDiff {
		t.Fatalf("screen = %v\n%s", h.m.screen, h.view())
	}
	h.want("T1 · Data model · 2 files")
	h.want("Esc back")
	h.want("a line 5 changed")
	h.want("Shared checkout")
	h.key("esc")
	if h.m.screen != wsScreenMain {
		t.Fatal("Esc should go back")
	}
}

func TestDiffOpensInActivityFeedAndFromSidebar(t *testing.T) {
	h := diffHarness(t)
	h.m.showGraph = false
	h.key("d")
	if h.m.screen != wsScreenDiff {
		t.Fatalf("feed mode: screen = %v", h.m.screen)
	}
	h.key("esc")
	h.m.showGraph = true
	h.m.setFocus(wsFocusSidebar)
	h.m.sbSess = -1
	h.key("d")
	if h.m.screen != wsScreenDiff {
		t.Fatalf("sidebar focus: screen = %v", h.m.screen)
	}
}

func TestDiffRendersWithinWidth(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {160, 40}} {
		h := diffHarness(t)
		h.size(size[0], size[1])
		h.key("d")
		for i := 0; i < 3; i++ {
			for j, line := range strings.Split(h.m.View(), "\n") {
				if w := ansi.StringWidth(line); w > size[0] {
					t.Fatalf("%dx%d: line %d is %d wide: %q", size[0], size[1], j, w, ansi.Strip(line))
				}
			}
			if n := len(strings.Split(h.m.View(), "\n")); n > size[1] {
				t.Fatalf("%dx%d: %d rows", size[0], size[1], n)
			}
			h.key("j", "j", "j", "right")
		}
		h.want("▌")
		if size[0] >= 100 {
			h.want("a.go +")
			h.wantNot("[ file")
		} else {
			h.want("[ file 1/2: a.go ]")
		}
		h.want("@@")
		h.want("+3 −3")
	}
}

func TestDiffLongLineTruncatedAndPanned(t *testing.T) {
	h := diffHarness(t)
	h.size(100, 30)
	h.key("d")
	h.key("G")
	h.want("…")
	h.wantNot(strings.Repeat("x", 150))
	h.key("right", "right", "right")
	if h.m.diff.hx != 3*wsDiffHScroll {
		t.Fatalf("hx = %d", h.m.diff.hx)
	}
	h.key("left", "left", "left", "left")
	if h.m.diff.hx != 0 {
		t.Fatalf("hx = %d", h.m.diff.hx)
	}
}

func TestDiffCursorHunkAndFileKeys(t *testing.T) {
	h := diffHarness(t)
	h.key("d")
	d := h.m.diff
	if d.cur != 0 {
		t.Fatalf("cur = %d", d.cur)
	}
	h.key("j", "j")
	if d.cur != 2 {
		t.Fatalf("cur after jj = %d", d.cur)
	}
	h.key("k")
	if d.cur != 1 {
		t.Fatalf("cur after k = %d", d.cur)
	}
	h.key("n")
	if d.rowsOf()[d.cur].line != -1 || d.cur == 0 {
		t.Fatalf("n should land on the second hunk header, cur = %d", d.cur)
	}
	h.key("p")
	if d.cur != 0 {
		t.Fatalf("p cur = %d", d.cur)
	}
	h.key("G")
	if d.cur != len(d.rowsOf())-1 {
		t.Fatalf("G cur = %d", d.cur)
	}
	h.key("g")
	if d.cur != 0 {
		t.Fatalf("g cur = %d", d.cur)
	}
	h.key("]")
	if d.file != 1 || d.cur != 0 {
		t.Fatalf("] file = %d", d.file)
	}
	h.want("b line 3")
	h.key("]")
	if d.file != 1 {
		t.Fatal("] past the last file should stay")
	}
	h.key("[")
	if d.file != 0 {
		t.Fatalf("[ file = %d", d.file)
	}
	h.key("pgdown")
	if d.cur == 0 {
		t.Fatal("pgdown should move the cursor")
	}
	h.key("pgup")
	if d.cur != 0 {
		t.Fatalf("pgup cur = %d", d.cur)
	}
}

// cursorTo puts the cursor on the row of the line with the given new number.
func cursorTo(t *testing.T, h *wsHarness, newNo int) {
	t.Helper()
	d := h.m.diff
	f := d.curFile()
	for i, r := range d.rowsOf() {
		if l := rowLine(f, r); l != nil && l.NewNo == newNo && l.Kind != '-' {
			d.cur = i
			return
		}
	}
	t.Fatalf("no line %d", newNo)
}

func TestDiffNotesAddEditDeleteAndPersist(t *testing.T) {
	h := diffHarness(t)
	h.key("d")
	cursorTo(t, h, 5) // the "a line 5 changed" row
	h.key("c")
	if !h.m.diff.editing {
		t.Fatal("c should open the note input")
	}
	h.typeText("Also reject PO boxes")
	h.key("esc")
	if len(h.m.sess.snapshot().Notes) != 0 || h.m.diff.editing {
		t.Fatal("Esc must cancel without saving")
	}
	h.key("c")
	h.typeText("Also reject PO boxes")
	h.key("enter")
	notes := h.m.sess.snapshot().Notes
	if len(notes) != 1 || notes[0].Text != "Also reject PO boxes" || notes[0].Line != 5 || notes[0].Side != "new" || notes[0].Path != "a.go" || notes[0].NodeID != "T1" {
		t.Fatalf("notes = %+v", notes)
	}
	h.want("Note · line 5")
	h.want("Also reject PO boxes")
	h.want("╭")
	h.want("1 note")

	// Persisted in the session JSON.
	data, err := os.ReadFile(filepath.Join(h.root, ".decsr", "sessions", h.m.sess.snapshot().ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved workspace.Session
	if err := json.Unmarshal(data, &saved); err != nil || len(saved.Notes) != 1 {
		t.Fatalf("saved notes = %+v, %v", saved.Notes, err)
	}

	// Editing replaces the text.
	h.key("c")
	h.key("!")
	h.key("enter")
	if got := h.m.sess.snapshot().Notes; len(got) != 1 || got[0].Text != "Also reject PO boxes!" {
		t.Fatalf("edited = %+v", got)
	}

	// x on a line without a note changes nothing; on the noted line deletes.
	h.key("j", "x")
	if len(h.m.sess.snapshot().Notes) != 1 {
		t.Fatal("x on another line must keep the note")
	}
	h.key("k", "x")
	if len(h.m.sess.snapshot().Notes) != 0 {
		t.Fatal("x should delete the note")
	}
	h.wantNot("Note · line 5")
}

func TestDiffNoteOnRemovedLineUsesOldSideAndHeaderIsRefused(t *testing.T) {
	h := diffHarness(t)
	h.key("d", "]")
	d := h.m.diff
	for i, r := range d.rowsOf() {
		if l := rowLine(d.curFile(), r); l != nil && l.Kind == '-' {
			d.cur = i
		}
	}
	h.key("c")
	h.typeText("why removed")
	h.key("enter")
	n := h.m.sess.snapshot().Notes
	if len(n) != 1 || n[0].Side != "old" || n[0].Line != 2 || n[0].Path != "b.go" {
		t.Fatalf("notes = %+v", n)
	}
	d.cur = 0 // hunk header
	h.key("c")
	if d.editing {
		t.Fatal("no note on a hunk header")
	}
	h.want("code line")
}

func TestDiffSendNotesCreatesOneFollowUpNode(t *testing.T) {
	h := diffHarness(t)
	h.key("d")
	cursorTo(t, h, 5)
	h.key("c")
	h.typeText("first note")
	h.key("enter")
	h.key("]")
	h.key("j", "j", "j")
	h.key("c")
	h.typeText("second note")
	h.key("enter")
	h.key("[")
	h.key("S")
	if h.m.screen != wsScreenMain {
		t.Fatalf("S should return to the main view:\n%s", h.view())
	}
	if h.m.sel != "T6" {
		t.Fatalf("sel = %s", h.m.sel)
	}
	n := h.m.g.Node("T6")
	if n == nil || n.Title != "Address review notes on T1" || len(n.DependsOn) != 0 {
		t.Fatalf("node = %+v", n)
	}
	if n.Role != "coder" {
		t.Errorf("role = %q, want the role of T1", n.Role)
	}
	for _, want := range []string{"a.go:5 — first note", "b.go:", "— second note", "  > ", "+ a line 5 changed"} {
		if !strings.Contains(n.Description, want) {
			t.Errorf("description lacks %q:\n%s", want, n.Description)
		}
	}
	if got := len(h.m.sess.snapshot().Notes); got != 0 {
		t.Errorf("notes left = %d", got)
	}
	if h.m.sess.snapshot().NextID != 7 {
		t.Errorf("NextID = %d", h.m.sess.snapshot().NextID)
	}
	h.want("Added T6 from 2 review notes — press r to run")
}

func TestDiffSendNotesUnassignedAndEmpty(t *testing.T) {
	h := diffHarness(t)
	h.key("d", "S")
	h.want("No notes to send")
	if h.m.screen != wsScreenDiff {
		t.Fatal("stay in the diff when there is nothing to send")
	}
	if err := h.eng.Replace(func(g *workspace.Graph) { g.Node("T1").Role = "" }); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	cursorTo(t, h, 5)
	h.key("c")
	h.typeText("one")
	h.key("enter", "S")
	h.want("Added T6 from 1 review note — assign a role (a), then press r to run")
	if h.m.g.Node("T6").Role != "" {
		t.Error("an unassigned source gives an unassigned node")
	}
}

func TestDiffEmptyAndErrorStates(t *testing.T) {
	h := diffHarness(t)
	if err := h.eng.Replace(func(g *workspace.Graph) { g.Node("T1").Files = nil }); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.key("d")
	h.want("No changes in this node.")
	h.key("c")
	h.key("esc")

	// An isolated node whose commit is gone shows one clear line.
	iso := workspace.NewIsolator(h.root, h.m.sess.snapshot().ID, "")
	if iso == nil {
		t.Fatal("no isolator")
	}
	h.m.iso = iso
	h.m.g.Node("T1").Commit = strings.Repeat("0", 40)
	h.key("d")
	h.want("! commit 0000000 of T1 is not available any more")
	h.key("j", "k", "n", "]", "c", "x", "S", "G")
	h.key("esc")
	if h.m.screen != wsScreenMain {
		t.Fatal("Esc must leave the error view")
	}
}

func TestDiffIsolatedNodeAndSessionDiff(t *testing.T) {
	h := diffHarness(t)
	diffGit(t, h.root, "checkout", "-q", "--", ".") // drop the uncommitted edits
	iso := workspace.NewIsolator(h.root, h.m.sess.snapshot().ID, "")
	if iso == nil {
		t.Fatal("no isolator")
	}
	h.m.iso = iso
	wt, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	diffWrite(t, wt, "c.go", "package c\n\nvar C = 1\n")
	_, commit, err := iso.FinishCommit("T1", "add c")
	if err != nil || commit == "" {
		t.Fatalf("commit %q %v", commit, err)
	}
	h.m.g.Node("T1").Commit = commit
	h.m.g.Node("T1").FileStats = []workspace.FileStat{{Path: "c.go", Added: 3, New: true}}
	h.key("d")
	h.want("T1 · Data model · 1 file +3 −0")
	h.want("var C = 1")
	h.wantNot("Shared checkout")
	h.key("esc", "D")
	h.want("Session diff · 1 file")
	h.want("var C = 1")
	h.key("c")
	h.want("Notes go on a node's diff")
	h.key("esc")
	if h.m.diff != nil {
		t.Error("the diff state should be dropped on leave")
	}
}

func TestDiffRunningNodeShowsWorktreeChanges(t *testing.T) {
	h := diffHarness(t)
	diffGit(t, h.root, "checkout", "-q", "--", ".")
	iso := workspace.NewIsolator(h.root, h.m.sess.snapshot().ID, "")
	h.m.iso = iso
	wt, _, err := iso.Start("T1")
	if err != nil {
		t.Fatal(err)
	}
	diffWrite(t, wt, "fresh.go", "package fresh\n")
	h.m.g.Node("T1").Status = workspace.StatusRunning
	h.key("d")
	h.want("package fresh")
}

func TestDiffCardAndHintsOfferDiff(t *testing.T) {
	h := diffHarness(t)
	h.m.showGraph = false
	h.m.g.Node("T1").FileStats = []workspace.FileStat{{Path: "a.go", Added: 1}}
	h.want("d diff")
	h.m.showGraph = true
	h.m.g.Node("T1").FileStats = nil
	h.m.g.Node("T1").Commit = ""
	h.wantNot("d diff")
}

func TestDiffCheckNodeHasNoDiff(t *testing.T) {
	h := diffHarness(t)
	h.m.sel = "CHECK"
	h.key("d")
	if h.m.screen == wsScreenDiff {
		t.Fatal("Build & test has no diff")
	}
	h.want("Build & test has no diff")
}
