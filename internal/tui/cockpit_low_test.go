package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/bus"
)

func TestFilesTabIgnoresPreexistingMentions(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.addActivity(bus.RoleCoder, "created .beads/config.yaml")
	c.addActivity(bus.RoleCoder, "found .claude/settings.json")
	c.addActivity(bus.RoleCoder, "⚙ Read internal/old.go")
	c.addActivity(bus.RoleCoder, "wrote internal/new.go")
	c.addActivity(bus.RoleCoder, "⚙ Edit internal/edited.go")
	if len(c.files) != 2 {
		t.Fatalf("want 2 changed files, got %v", c.files)
	}
	out := ansi.Strip(strings.Join(c.fileLines(80, 20), "\n"))
	for _, want := range []string{"CHANGED FILES  2", "internal/new.go", "internal/edited.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, ".beads") || strings.Contains(out, "old.go") {
		t.Fatalf("pre-existing file listed: %q", out)
	}
}

func TestFilesTabEllipsisKeepsExtension(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.addActivity(bus.RoleCoder, "wrote .beads/some/deep/dir/metadata.json")
	out := ansi.Strip(strings.Join(c.fileLines(30, 20), "\n"))
	if !strings.Contains(out, "…") || !strings.Contains(out, "metadata.json") {
		t.Fatalf("want ellipsis and full extension, got %q", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Fatalf("line too wide: %q", l)
		}
	}
}

func TestTestsTabLabelsOnce(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.subtasks = append(c.subtasks, cockpitSubTask{key: "T1", title: "Implement thing", testStatus: "done", implStatus: "active", verifyStatus: "pending"})
	out := ansi.Strip(strings.Join(c.testLines(100, 20), "\n"))
	for _, l := range []string{"tests", "impl", "verify"} {
		if n := strings.Count(out, l); n != 1 {
			t.Fatalf("%q appears %d times in %q", l, n, out)
		}
	}
}

func TestActivityFollowsNewestUnlessScrolled(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(120, 30)
	for i := 0; i < 80; i++ {
		c.addActivity(bus.RoleCoder, fmt.Sprintf("line-%03d", i))
	}
	view := func() string { return ansi.Strip(strings.Join(c.activityLines(100, 10), "\n")) }
	if v := view(); !strings.Contains(v, "line-079") || strings.Contains(v, "line-000") {
		t.Fatalf("not following: %q", v)
	}
	c.focus = cockpitFocusCenter
	c, _ = c.Update(keyMsgFor("up"))
	c, _ = c.Update(keyMsgFor("up"))
	for i := 80; i < 90; i++ {
		c.addActivity(bus.RoleCoder, fmt.Sprintf("line-%03d", i))
	}
	v := view()
	if strings.Contains(v, "line-089") || !strings.Contains(v, "line-077") {
		t.Fatalf("should stay put when scrolled up: %q", v)
	}
	c, _ = c.Update(keyMsgFor("end"))
	if v := view(); !strings.Contains(v, "line-089") {
		t.Fatalf("end should resume following: %q", v)
	}
	c.addActivity(bus.RoleCoder, "line-090")
	if v := view(); !strings.Contains(v, "line-090") {
		t.Fatalf("not following after end: %q", v)
	}
}

func TestOutputFollowsNewest(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.activeRole = bus.RoleCoder
	b := &strings.Builder{}
	for i := 0; i < 50; i++ {
		fmt.Fprintf(b, "out-%03d\n", i)
	}
	c.raw[bus.RoleCoder] = b
	v := ansi.Strip(strings.Join(c.outputLines(100, 10), "\n"))
	if !strings.Contains(v, "out-049") && !strings.Contains(v, "out-048") {
		t.Fatalf("output not at bottom: %q", v)
	}
}

func keyMsgFor(k string) tea.KeyMsg {
	switch k {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}
