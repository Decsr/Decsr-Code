package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

var limitBase = time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)

// limitHarness is a Workspace at the given autonomy level (1..3) whose T1
// ran and hit the claude limit, resetting at reset (zero: unknown). The clock
// is fixed at limitBase.
func limitHarness(t *testing.T, level int, reset time.Time) (*wsHarness, *autoNotes, *time.Time) {
	t.Helper()
	h, notes := autoHarness(t, level)
	clock := limitBase
	h.m.now = func() time.Time { return clock }
	h.graph = wsSmallGraph
	h.toGraph()
	if level == autoManual {
		h.key("A")
		if err := h.eng.Run(h.m.runCtx, "T1"); err != nil {
			t.Fatal(err)
		}
	}
	h.hitLimit(reset)
	// The engine stamps EndedAt with the wall clock; pin it to the fake one so
	// the sidebar age does not depend on when the test runs.
	h.m.g.Node("T1").EndedAt = limitBase
	return h, notes, &clock
}

// hitLimit makes the running T1 end with a claude rate-limit error.
func (h *wsHarness) hitLimit(reset time.Time) {
	h.t.Helper()
	h.pump(func(m workspaceModel) bool {
		return m.g.Node("T1") != nil && m.g.Node("T1").Status == workspace.StatusRunning
	})
	h.exec.release("T1", &runner.RateLimitError{Provider: "claude", Detail: "5-hour limit reached", ResetAt: reset})
	h.pump(func(m workspaceModel) bool { return m.g.Node("T1").Status == workspace.StatusWaiting })
	h.drain()
}

func (h *wsHarness) tick(clock *time.Time, at time.Time) {
	h.t.Helper()
	*clock = at
	h.send(wsTickMsg{})
	h.drain()
}

func TestLimitClock(t *testing.T) {
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if got := limitClock(now, time.Date(2026, 11, 16, 14, 30, 0, 0, time.UTC)); got != "14:30" {
		t.Errorf("today = %q", got)
	}
	if got := limitClock(now, time.Date(2026, 11, 17, 9, 5, 0, 0, time.UTC)); got != "Nov 17 09:05" {
		t.Errorf("tomorrow = %q", got)
	}
	if got := limitClock(now, time.Date(2027, 11, 16, 9, 5, 0, 0, time.UTC)); got != "Nov 16 09:05" {
		t.Errorf("next year = %q", got)
	}
}

func TestLimitAutoRetryAtLevel2(t *testing.T) {
	reset := limitBase.Add(4*time.Hour + 30*time.Minute)
	for _, level := range []int{autoAssisted, autoUnattended} {
		h, notes, clock := limitHarness(t, level, reset)
		if level == autoUnattended {
			h.m.cfg.Workspace.BudgetUSD = 100
		}
		if !notes.has(notify.Info, "T1 waits for the claude limit — retrying after 14:30") && level == autoAssisted {
			t.Fatalf("no Info note: %+v", notes.notes)
		}
		n := h.m.g.Node("T1")
		if n.Limit == nil || h.m.g.CountWaiting() != 1 {
			t.Fatalf("node = %+v", n)
		}
		h.tick(clock, reset.Add(29*time.Second))
		if h.status("T1") != workspace.StatusWaiting {
			t.Fatalf("L%d retried before reset+30s", level)
		}
		h.tick(clock, reset.Add(30*time.Second))
		if level == autoAssisted {
			h.wantAuto("T1 retry 1/3 after the claude limit reset")
			if h.status("T1") != workspace.StatusRunning {
				t.Fatalf("T1 is %s after the retry", h.status("T1"))
			}
		}
	}
}

func TestLimitFailsAfterThreeAutomaticRetries(t *testing.T) {
	reset := limitBase.Add(time.Hour)
	h, notes, clock := limitHarness(t, autoAssisted, reset)
	for i := 1; i <= workspace.MaxLimitRetries; i++ {
		at := reset.Add(time.Duration(i) * time.Hour)
		h.tick(clock, at.Add(time.Minute))
		h.wantAuto("retry " + string(rune('0'+i)) + "/3 after the claude limit reset")
		h.hitLimit(at.Add(time.Hour))
		reset = at.Add(time.Hour)
	}
	h.tick(clock, reset.Add(time.Minute))
	n := h.m.g.Node("T1")
	if n.Status != workspace.StatusFailed || !strings.Contains(n.Error, "limit reached on claude") || !strings.Contains(n.Error, "3 automatic retries used") {
		t.Fatalf("node = %+v", n)
	}
	if !notes.has(notify.Failed, "T1 failed: limit reached on claude") {
		t.Errorf("no Failed note: %+v", notes.notes)
	}
}

func TestLimitNoAutoRetryAtLevel1AndRKey(t *testing.T) {
	reset := limitBase.Add(time.Hour)
	h, notes, clock := limitHarness(t, autoManual, reset)
	if !notes.has(notify.NeedsYou, "T1 hit the claude limit — resets 11:00, press r to retry") {
		t.Fatalf("no NeedsYou note: %+v", notes.notes)
	}
	h.tick(clock, reset.Add(time.Hour))
	if h.status("T1") != workspace.StatusWaiting {
		t.Fatal("level 1 retried by itself")
	}
	h.m.sel = "T1"
	h.m.focus = wsFocusGraph
	h.key("r")
	if h.status("T1") != workspace.StatusRunning {
		t.Fatalf("r did not retry: %s", h.status("T1"))
	}
	h.hitLimit(reset.Add(2 * time.Hour))
	if got := h.m.g.Node("T1").Limit.Retries; got != 0 {
		t.Errorf("a user retry counted: %d", got)
	}
}

func TestLimitUnknownResetNeverAutoRetries(t *testing.T) {
	h, notes, clock := limitHarness(t, autoAssisted, time.Time{})
	if !notes.has(notify.NeedsYou, "reset time unknown, press r to retry") {
		t.Fatalf("notes: %+v", notes.notes)
	}
	h.tick(clock, limitBase.Add(48*time.Hour))
	if h.status("T1") != workspace.StatusWaiting {
		t.Fatal("retried without a reset time")
	}
	h.m.sel, h.m.focus = "T1", wsFocusGraph
	h.want("reset time unknown")
}

func TestLimitNoAutoRetryWhenPausedOrBudgetReached(t *testing.T) {
	reset := limitBase.Add(time.Hour)
	h, _, clock := limitHarness(t, autoAssisted, reset)
	h.m.autoPaused = true
	h.tick(clock, reset.Add(time.Hour))
	if h.status("T1") != workspace.StatusWaiting {
		t.Fatal("paused automation retried")
	}
	h.m.autoPaused = false
	h.m.cfg.Workspace.BudgetUSD = 1
	if err := h.eng.Replace(func(g *workspace.Graph) {
		g.Node("T1").CostUSD, g.Node("T1").CostKnown, g.Node("T1").InputTokens = 2, true, 1000
	}); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.tick(clock, reset.Add(time.Hour))
	if h.status("T1") != workspace.StatusWaiting {
		t.Fatal("retried with the budget reached")
	}
}

func TestLimitRendering(t *testing.T) {
	reset := limitBase.Add(4*time.Hour + 30*time.Minute)
	for _, width := range []int{80, 120} {
		h, _, _ := limitHarness(t, autoManual, reset)
		h.m.showGraph = false
		h.size(width, 34)
		h.m.sel = "T1"
		h.m.notice = ""
		out := h.view()
		for _, want := range []string{"◷ waiting · resets 14:30", "5-hour limit reached", "limit · resets 14:30", "1 waiting", "◷ T1 waits for the claude limit (14:30)"} {
			if !strings.Contains(out, want) && width >= 120 {
				t.Errorf("width %d: missing %q in\n%s", width, want, out)
			}
		}
		if !strings.Contains(out, "◷ waiting · resets 14:30") {
			t.Errorf("width %d: card missing in\n%s", width, out)
		}
		for i, l := range strings.Split(h.m.View(), "\n") {
			if w := ansi.StringWidth(l); w > width {
				t.Errorf("width %d: line %d is %d cells wide: %q", width, i, w, ansi.Strip(l))
			}
		}
		bar := ansi.Strip(h.m.statusBar())
		if width >= 120 && !strings.Contains(bar, "claude · limited until 14:30") {
			t.Errorf("status bar = %q", bar)
		}
		if w := ansi.StringWidth(bar); w > width {
			t.Errorf("status bar is %d wide at %d", w, width)
		}
		h.m.showGraph = true
		h.size(width, 34)
		if g := h.view(); !strings.Contains(g, "waiting") {
			t.Errorf("graph at %d lacks the waiting word", width)
		}
	}
}

func init() {
	// Tests never depend on the login of the machine they run on.
	claudeAuthMode = func() string { return runner.AuthAPIKey }
}

func withClaudeAuth(t *testing.T, mode string) {
	t.Helper()
	old := claudeAuthMode
	claudeAuthMode = func() string { return mode }
	t.Cleanup(func() { claudeAuthMode = old })
}

func boolPtr(b bool) *bool { return &b }

func TestLimitAutoRetryOffOnSubscription(t *testing.T) {
	withClaudeAuth(t, runner.AuthSubscription)
	reset := limitBase.Add(time.Hour)
	h, notes, clock := limitHarness(t, autoAssisted, reset)
	if !notes.has(notify.NeedsYou, "auto-retry is off on a subscription login; press r to retry") {
		t.Fatalf("no explaining note: %+v", notes.notes)
	}
	if h.m.limitAutoRetries(h.m.g.Node("T1")) {
		t.Fatal("limitAutoRetries is true on a subscription")
	}
	h.tick(clock, reset.Add(time.Hour))
	if h.status("T1") != workspace.StatusWaiting {
		t.Fatalf("retried by itself on a subscription: %s", h.status("T1"))
	}
	h.m.sel = "T1"
	h.m.focus = wsFocusGraph
	h.key("r")
	if h.status("T1") != workspace.StatusRunning {
		t.Fatalf("r did not retry: %s", h.status("T1"))
	}
}

func TestLimitAutoRetryOnWithAPIKey(t *testing.T) {
	withClaudeAuth(t, runner.AuthAPIKey)
	reset := limitBase.Add(time.Hour)
	h, _, clock := limitHarness(t, autoAssisted, reset)
	if !h.m.limitAutoRetries(h.m.g.Node("T1")) {
		t.Fatal("limitAutoRetries is false with an API key")
	}
	h.tick(clock, reset.Add(time.Minute))
	if h.status("T1") != workspace.StatusRunning {
		t.Fatalf("no automatic retry with an API key: %s", h.status("T1"))
	}
}

func TestLimitAutoRetrySettingOverrides(t *testing.T) {
	withClaudeAuth(t, runner.AuthSubscription)
	h, _, _ := limitHarness(t, autoAssisted, limitBase.Add(time.Hour))
	n := h.m.g.Node("T1")
	h.m.cfg.Workspace.LimitAutoRetry = boolPtr(true)
	if !h.m.limitAutoRetries(n) {
		t.Error("limit_auto_retry: true did not turn the retry on for a subscription")
	}
	if h.m.limitAutoOffNote(n) != "" {
		t.Error("an explaining note with the retry on")
	}
	withClaudeAuth(t, runner.AuthAPIKey)
	h.m.cfg.Workspace.LimitAutoRetry = boolPtr(false)
	if h.m.limitAutoRetries(n) {
		t.Error("limit_auto_retry: false did not turn the retry off for an API key")
	}
}
