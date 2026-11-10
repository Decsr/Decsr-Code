package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestInspectorToggleHidesRunPane(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	if !strings.Contains(c.View(), "RUN") {
		t.Fatal("inspector on: RUN pane should be visible")
	}
	c.ToggleInspector()
	v := c.View()
	if strings.Contains(v, "RUN") {
		t.Fatal("inspector off: RUN pane should be hidden")
	}
	for _, line := range strings.Split(v, "\n") {
		if w := ansi.StringWidth(line); w > 150 {
			t.Fatalf("line wider than terminal: %d", w)
		}
	}
	c.ToggleInspector()
	if !strings.Contains(c.View(), "RUN") {
		t.Fatal("inspector back on: RUN pane should be visible")
	}
}

func TestInspectorOffMovesFocusAway(t *testing.T) {
	c, _ := cockpitFixture(t)
	c.SetSize(150, 40)
	c.focus = cockpitFocusInspector
	c.ToggleInspector()
	if c.focus == cockpitFocusInspector {
		t.Fatal("focus stayed on hidden inspector")
	}
	c.focus = cockpitFocusInspector
	c.SetSize(100, 40)
	if c.focus == cockpitFocusInspector {
		t.Fatal("focus stayed on inspector after narrowing")
	}
}

func replayEvents(c *CockpitModel, clock *time.Time, steps []struct {
	after time.Duration
	from  bus.AgentRole
	ev    string
}) {
	for _, s := range steps {
		*clock = clock.Add(s.after)
		c.Observe(bus.NewMessage(s.from, "", bus.MsgEvent, s.ev))
	}
}

func TestStageTimersFollowRealEventSequence(t *testing.T) {
	c, _ := cockpitFixture(t)
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return now }
	c.LoadPlan()
	sys, cod, qa := bus.RoleSystem, bus.RoleCoder, bus.RoleQA
	replayEvents(&c, &now, []struct {
		after time.Duration
		from  bus.AgentRole
		ev    string
	}{
		{0, sys, "planning"},
		{1 * time.Minute, sys, "security"}, // plan review, must not enter Review
		{1 * time.Minute, sys, "qa_review"},
		{1 * time.Minute, sys, "qa_tests"},
		{0, sys, "── Tests 1/5: Add session model ──"},
		{3 * time.Minute, sys, "── Sub-task 1/5 (b1): Add session model ──"},
		{3 * time.Minute, sys, "coder"},
		{1 * time.Minute, sys, "running build and tests…"},
		{0, cod, "done"}, // coder log line, must not finish the run
		{2 * time.Second, sys, "all tests passed"},
		{1 * time.Second, sys, "qa_review"},
		{1 * time.Second, sys, "QA reviewing code quality…"},
		{4 * time.Minute, sys, "UX reviewing…"},
		{2 * time.Minute, sys, "Security reviewing…"},
		{2 * time.Minute, sys, "quality review passed — all checks clean"},
		{0, qa, "qa complete"},
		{0, sys, "done"},
		{0, sys, "task complete"},
	})
	dur := map[string]time.Duration{}
	for _, p := range c.phases {
		dur[p.id] = p.duration
	}
	if dur["build"] < 6*time.Minute {
		t.Errorf("build = %v, want >= 6m", dur["build"])
	}
	if dur["verify"] < 2*time.Second || dur["verify"] > 10*time.Second {
		t.Errorf("verify = %v, want a few seconds", dur["verify"])
	}
	if dur["review"] < 8*time.Minute {
		t.Errorf("review = %v, want >= 8m", dur["review"])
	}
	if dur["plan"] < 2*time.Minute {
		t.Errorf("plan = %v, want >= 2m (plan review stays in plan)", dur["plan"])
	}
}

func TestCockpitTickMsgRefreshesWithoutEvents(t *testing.T) {
	m := New(nil, t.TempDir(), "", nil, config.Config{})
	m.width, m.height = 120, 40
	m.layout()
	updated, cmd := m.Update(TaskRunnerReadyMsg{})
	m = updated.(Model)
	if cmd == nil || !m.cockpitTicking {
		t.Fatal("runner start should begin the cockpit tick loop")
	}
	// A second ready message (or resume) must not start another loop.
	_, cmd2 := m.Update(TaskRunnerReadyMsg{})
	if cmd2 != nil {
		t.Fatal("duplicate tick loop started")
	}
	updated, cmd = m.Update(cockpitTickMsg{})
	m = updated.(Model)
	if cmd == nil || !m.cockpitTicking {
		t.Fatal("tick should reschedule while the pipeline runs")
	}
	m.pipelineDone = true
	updated, cmd = m.Update(cockpitTickMsg{})
	m = updated.(Model)
	if cmd != nil || m.cockpitTicking {
		t.Fatal("tick should stop once the pipeline is done")
	}
}

func TestCockpitTickSurvivesOverlay(t *testing.T) {
	m := New(nil, t.TempDir(), "", nil, config.Config{})
	m.cockpitTicking = true
	m.overlay = overlayChat
	_, cmd := m.Update(cockpitTickMsg{})
	if cmd == nil {
		t.Fatal("overlay swallowed the cockpit tick")
	}
}

func TestResumeSeedsUsageFromRunLog(t *testing.T) {
	dir := t.TempDir()
	log := strings.Join([]string{
		`{"from":"pm","type":"usage","payload":{"input_tokens":999,"output_tokens":1}}`,
		`{"from":"system","type":"event","payload":"task complete"}`,
		`{"from":"pm","type":"usage","payload":{"input_tokens":100,"output_tokens":50,"estimated":true}}`,
		`{"from":"qa","type":"usage","payload":{"input_tokens":30,"output_tokens":20}}`,
		`{"from":"pm","type":"event","payload":"not usage"}`,
		`not json`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "runlog.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(nil, dir, dir, nil, config.Config{}).WithTaskInput("")
	if got := m.cockpit.totalUsage.InputTokens + m.cockpit.totalUsage.OutputTokens; got != 200 {
		t.Fatalf("seeded total = %d, want 200 (only the current task)", got)
	}
	if m.cockpit.agents[bus.RolePM].calls != 1 || m.cockpit.agents[bus.RoleQA].calls != 1 {
		t.Fatalf("seeded calls pm=%d qa=%d, want 1/1", m.cockpit.agents[bus.RolePM].calls, m.cockpit.agents[bus.RoleQA].calls)
	}
	if m.cockpit.callSeq != 2 {
		t.Fatalf("callSeq = %d, want 2", m.cockpit.callSeq)
	}
	// A fresh task never inherits earlier usage.
	fresh := New(nil, dir, dir, nil, config.Config{}).WithTaskInput("new task")
	if fresh.cockpit.totalUsage.InputTokens != 0 {
		t.Fatal("fresh task seeded from run log")
	}
}

var _ = tea.KeyMsg{}
