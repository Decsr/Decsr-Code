package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── data builders ────────────────────────────────────────────────────────────

func usageNode(id, role string, in, out int, usd float64, known bool, ended time.Time) *workspace.Node {
	return &workspace.Node{ID: id, Role: role, Kind: workspace.KindWork, Status: workspace.StatusDone,
		InputTokens: in, OutputTokens: out, CostUSD: usd, CostKnown: known, EndedAt: ended}
}

func TestUsageHistoryAggregation(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local)
	c := config.DefaultConfig()
	day := func(back int, h int) time.Time { return time.Date(2026, 11, 18-back, h, 0, 0, 0, time.Local) }
	sessions := []workspace.Session{
		{ID: "a", UpdatedAt: day(0, 14), In: 100, Out: 50, Graph: &workspace.Graph{Nodes: []*workspace.Node{
			usageNode("T1", "coder", 1000, 200, 0.5, true, day(0, 13)),
			usageNode("T2", "qa", 400, 100, 0, false, day(0, 13)),
		}}},
		{ID: "b", UpdatedAt: day(3, 10), Graph: &workspace.Graph{Nodes: []*workspace.Node{
			usageNode("T1", "coder", 2000, 0, 1.0, true, day(3, 9)),
		}}},
		{ID: "c", UpdatedAt: day(20, 10), Graph: &workspace.Graph{Nodes: []*workspace.Node{
			usageNode("T1", "coder", 500, 500, 0.25, true, day(20, 9)),
		}}},
		{ID: "old", UpdatedAt: day(45, 10), Graph: &workspace.Graph{Nodes: []*workspace.Node{
			usageNode("T1", "coder", 9999, 0, 9, true, day(45, 9)),
		}}},
	}
	h := buildUsageHistory(sessions, now, c)
	if h.today.sessions != 1 || h.week.sessions != 2 || h.month.sessions != 3 {
		t.Errorf("sessions today/week/month = %d/%d/%d", h.today.sessions, h.week.sessions, h.month.sessions)
	}
	if h.today.tokens != 1850 || h.week.tokens != 3850 || h.month.tokens != 4850 {
		t.Errorf("tokens = %d/%d/%d", h.today.tokens, h.week.tokens, h.month.tokens)
	}
	if h.week.usd != 1.5 || h.month.usd != 1.75 {
		t.Errorf("usd = %v/%v", h.week.usd, h.month.usd)
	}
	if got := h.today.costCell(); !strings.HasPrefix(got, "≥") {
		t.Errorf("a day with an unpriced node must say so: %q", got)
	}
	if h.daily[29] != 1850 || h.daily[26] != 2000 || h.daily[9] != 1000 {
		t.Errorf("daily = %v", h.daily)
	}
	if len(h.agents) < 2 || h.agents[0].role != "coder" || h.agents[0].tokens != 4200 {
		t.Errorf("agents = %+v", h.agents)
	}
	only := buildUsageHistory([]workspace.Session{sessions[0]}, now, c)
	if only.week.costCell() == "–" {
		t.Error("a priced node gives a cost")
	}
	none := buildUsageHistory([]workspace.Session{{ID: "x", UpdatedAt: day(0, 1), Graph: &workspace.Graph{Nodes: []*workspace.Node{usageNode("T1", "qa", 10, 10, 0, false, day(0, 1))}}}}, now, c)
	if none.today.costCell() != "–" {
		t.Errorf("no price anywhere is a dash: %q", none.today.costCell())
	}
}

func TestBuildLimitsKeepsTheNewestPerRunner(t *testing.T) {
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local)
	mk := func(provider, msg string, ended time.Time, reset time.Time) *workspace.Node {
		return &workspace.Node{ID: "T1", EndedAt: ended, Limit: &workspace.LimitInfo{Provider: provider, Message: msg, ResetAt: reset}}
	}
	ss := []workspace.Session{
		{UpdatedAt: t0, Graph: &workspace.Graph{Nodes: []*workspace.Node{
			mk("claude", "old limit", t0.Add(-48*time.Hour), time.Time{}),
			mk("claude", "new limit", t0.Add(-time.Hour), t0.Add(2*time.Hour)),
			mk("codex", "codex limit", t0.Add(-3*time.Hour), time.Time{}),
			{ID: "T9"},
		}}},
		{UpdatedAt: t0},
	}
	got := buildLimits(ss)
	if len(got) != 2 || got[0].runner != "claude" || got[0].message != "new limit" || got[1].runner != "codex" {
		t.Fatalf("limits = %+v", got)
	}
	if s := limitState(t0, got[0]); !strings.Contains(s, "resets in 2h") {
		t.Errorf("state = %q", s)
	}
	if s := limitState(t0, got[1]); s != "no reset time given" {
		t.Errorf("state = %q", s)
	}
	if s := limitState(t0.Add(5*time.Hour), got[0]); !strings.Contains(s, "passed") {
		t.Errorf("state = %q", s)
	}
}

func TestSessionUsageRows(t *testing.T) {
	c := config.DefaultConfig()
	g := &workspace.Graph{RemovedCostUSD: 0.5, Nodes: []*workspace.Node{
		usageNode("T1", "coder", 100, 10, 0.1, true, time.Time{}),
		usageNode("T2", "coder", 100, 10, 0.1, true, time.Time{}),
		usageNode("T3", "qa", 50, 5, 0, false, time.Time{}),
		{ID: "T4"},
	}}
	u := buildSessionUsage(c, workspace.Session{In: 30, Out: 3}, g)
	if len(u.nodes) != 3 {
		t.Errorf("nodes = %d", len(u.nodes))
	}
	if u.total.in != 280 || u.total.out != 28 {
		t.Errorf("total = %+v", u.total)
	}
	if !u.total.unpriced || u.total.usd < 0.69 || u.total.usd > 0.71 {
		t.Errorf("a partly priced total keeps its known cost: %+v", u.total)
	}
	if !strings.HasPrefix(u.total.costCell(), "≥") {
		t.Errorf("cell = %q", u.total.costCell())
	}
	var coder *useRow
	for i := range u.roles {
		if u.roles[i].role == "coder" {
			coder = &u.roles[i]
		}
	}
	if coder == nil || coder.in != 200 {
		t.Errorf("two nodes of one role and agent share a row: %+v", u.roles)
	}
}

func TestStatusLinesRunnersProvidersAndWarnings(t *testing.T) {
	entries := []frEntry{
		{name: "claude", kind: frCLI, usable: true, status: "claude CLI found"},
		{name: "codex", kind: frCLI, status: "not installed"},
		{name: "dsh", kind: frCLI, status: "dsh found · needs a DeepSeek key", needsKey: true},
		{name: "deepseek", kind: frAPI, usable: true, status: "key saved"},
		{name: "openai", kind: frAPI, status: "needs an API key"},
		{name: "ollama", kind: frLocal},
	}
	sub := runnerStatuses(entries, runner.AuthSubscription)
	api := runnerStatuses(entries, runner.AuthAPIKey)
	if len(sub) != 4 {
		t.Fatalf("runners = %+v", sub)
	}
	if !strings.Contains(sub[0].state, "subscription (unattended runs need a key)") || api[0].state != "API key" {
		t.Errorf("claude auth: %q / %q", sub[0].state, api[0].state)
	}
	if sub[1].ok || sub[1].state != "not installed" || sub[3].state != "key saved" {
		t.Errorf("states = %+v", sub)
	}
	today := usageSpan{sessions: 2, tokens: 12345}
	today.add(0, 0, true)
	in := statusIn{version: "1.2.3", project: "demo", language: "go", title: "Add JSON", isolation: "shared checkout",
		runners: sub, runnersLoaded: true, providers: []provRow{{name: "deepseek", masked: "sk-…1234", source: "saved", saved: true}},
		pluginsOn: 2, pluginsOff: 1, routing: "manual", autonomy: "2 assisted", coordinator: "on", parallel: 3,
		budget: "$5.29 of $10.00 (53%)", today: &today, warnings: 2, doctorDone: true}
	text := ansi.Strip(strings.Join(statusLines(in), "\n"))
	for _, want := range []string{"1.2.3", "demo (go)", "Add JSON · shared checkout", "RUNNERS", "claude", "subscription (unattended runs need a key)",
		"PROVIDERS", "sk-…1234", "2 on · 1 off", "RUN", "manual", "53%", "TODAY", "2 sessions · 12,345 tokens", "⚠ 2 warnings · /doctor for details"} {
		if !strings.Contains(text, want) {
			t.Errorf("status lacks %q:\n%s", want, text)
		}
	}
	if n := countProblems([]doctor.Result{{Status: doctor.OK}, {Status: doctor.Warn}, {Status: doctor.Fail}}); n != 2 {
		t.Errorf("problems = %d", n)
	}
}

func TestContextSizes(t *testing.T) {
	c := config.DefaultConfig()
	big := strings.Repeat("x", 40_000)
	d := buildContext(c, big, strings.Repeat("o", 800), strings.Repeat("p", 400), "memory text")
	if d.rows[0].chars != 800 || d.rows[1].chars != 400 || d.rows[2].chars != 40_000 || d.rows[5].chars != len("memory text") {
		t.Errorf("rows = %+v", d.rows)
	}
	if d.rows[3].chars == 0 || d.rows[4].chars == 0 {
		t.Error("the prompt templates have a size")
	}
	if d.worker >= d.lead+40_000 || d.worker < 800+400+4000 {
		t.Errorf("a worker gets only the tail of the summary: lead %d worker %d", d.lead, d.worker)
	}
	if approxTokens(0) != 0 || approxTokens(4) != 1 || approxTokens(5) != 2 {
		t.Error("tokens are chars / 4, rounded up")
	}
	text := ansi.Strip(strings.Join(contextLines(d, 100), "\n"))
	for _, want := range []string{"Project overview", "Context pack", "Session summary", "Memory", "≈", "The lead gets"} {
		if !strings.Contains(text, want) {
			t.Errorf("context lacks %q:\n%s", want, text)
		}
	}
}

func TestDoctorLines(t *testing.T) {
	rs := []doctor.Result{
		{Name: "git", Status: doctor.OK, Detail: "git 2.40"},
		{Name: "claude auth", Status: doctor.Warn, Detail: "subscription", Hint: "set ANTHROPIC_API_KEY"},
		{Name: "backend", Status: doctor.Fail, Detail: "no runner", Hint: "install one"},
	}
	text := ansi.Strip(strings.Join(doctorLines(rs, 80), "\n"))
	for _, want := range []string{"✓ git", "⚠ claude auth", "✗ backend", "→ set ANTHROPIC_API_KEY", "2 of 3 checks need attention"} {
		if !strings.Contains(text, want) {
			t.Errorf("doctor lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(ansi.Strip(strings.Join(doctorLines(rs[:1], 80), "\n")), "all checks passed") {
		t.Error("a clean run says so")
	}
}

// ── panels ───────────────────────────────────────────────────────────────────

// runCmd runs a command and feeds its messages back, batches included.
func (h *wsHarness) runCmd(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.runCmd(c)
		}
	default:
		h.runCmd(h.send(msg))
	}
}

// openPanelCmd types a command and runs what it started.
func (h *wsHarness) openPanelCmd(line string) {
	h.t.Helper()
	h.m.input.SetValue(line)
	h.runCmd(h.key("enter"))
}

func panelHarness(t *testing.T) *wsHarness {
	h := newWSHarness(t)
	h.m.deps.doctor = func() []doctor.Result {
		return []doctor.Result{{Name: "git", Status: doctor.OK, Detail: "ok"}, {Name: "claude auth", Status: doctor.Warn, Detail: "subscription", Hint: "set a key"}}
	}
	h.m.deps.probeRunners = func() []frEntry {
		return []frEntry{{name: "claude", kind: frCLI, usable: true}, {name: "codex", kind: frCLI}}
	}
	h.m.deps.probeKey = func(string, string) ([]string, error) { return []string{"m1"}, nil }
	return h
}

func TestPanelsRenderAtBothWidthsWithAndWithoutColour(t *testing.T) {
	if err := credentials.Set("deepseek", credentials.Cred{APIKey: "sk-abcdefghijklmnop1234"}); err != nil {
		t.Skip(err)
	}
	cmds := []string{"/usage", "/usage budget", "/usage cost", "/status", "/model", "/model profile", "/provider", "/config", "/context", "/doctor", "/memory", "/memory search build", "/plugin"}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		for _, plain := range []bool{false, true} {
			for _, line := range cmds {
				name := fmt.Sprintf("%s %dx%d plain=%v", line, size[0], size[1], plain)
				t.Run(name, func(t *testing.T) {
					if plain {
						withColorProfile(t, termenv.Ascii)
					} else {
						withColorProfile(t, termenv.TrueColor)
					}
					h := panelHarness(t)
					h.size(size[0], size[1])
					h.toGraph()
					h.key("tab")
					h.openPanelCmd(line)
					if h.m.overlay != wsOverlayPanel {
						t.Fatalf("%s did not open a panel", line)
					}
					// every tab and a few scrolls
					for i := 0; i < 5; i++ {
						v := h.m.View()
						for _, l := range strings.Split(v, "\n") {
							if w := ansi.StringWidth(l); w > size[0] {
								t.Fatalf("line of %d cells on a %d wide screen: %q", w, size[0], ansi.Strip(l))
							}
						}
						if n := len(strings.Split(v, "\n")); n > size[1] {
							t.Fatalf("%d rows on a %d high screen", n, size[1])
						}
						if plain && hasColorEscape(h.m.panelBox()) {
							t.Fatalf("NO_COLOR panel carries colour:\n%q", h.m.panelBox())
						}
						h.key("tab", "down")
					}
					h.key("esc")
					if h.m.overlay != wsOverlayNone {
						t.Error("Esc closes the panel")
					}
				})
			}
		}
	}
}

func TestUsagePanelTabsAndBudgetEntry(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.toGraph()
	h.key("tab")
	h.openPanelCmd("/usage")
	for _, w := range []string{"Usage", "Session", "Budget", "History", "Limits", "This session", "nodes"} {
		h.want(w)
	}
	h.key("n")
	if !h.m.panel.perNode {
		t.Error("n shows the nodes")
	}
	h.key("tab")
	h.want("Spent")
	h.key("enter")
	if !h.m.panel.inputing {
		t.Fatal("Enter on the Budget tab types an amount")
	}
	for _, r := range "x" {
		h.key(string(r))
	}
	h.key("enter")
	h.want("type an amount")
	h.key("enter")
	for _, r := range "7.5" {
		h.key(string(r))
	}
	h.key("enter")
	if h.m.sess.snapshot().BudgetUSD != 7.5 {
		t.Errorf("budget = %v", h.m.sess.snapshot().BudgetUSD)
	}
	h.key("enter")
	for _, r := range "off" {
		h.key(string(r))
	}
	h.key("enter")
	if h.m.sess.snapshot().BudgetUSD != 0 {
		t.Error("off removes the override")
	}
	h.key("tab")
	h.want("Tokens per day")
	h.key("tab")
	h.want("No runner has reported a limit")
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Error("Esc closes")
	}
}

func TestUsageHistoryReadsSavedSessions(t *testing.T) {
	h := panelHarness(t)
	now := h.m.now()
	s := workspace.NewSession()
	s.Graph = &workspace.Graph{Nodes: []*workspace.Node{
		{ID: "T1", Title: "Add it", Status: workspace.StatusDone, Role: "coder", Kind: workspace.KindWork, InputTokens: 800, OutputTokens: 200, CostUSD: 0.2, CostKnown: true, EndedAt: now,
			Limit: &workspace.LimitInfo{Provider: "claude", Message: "5-hour limit", ResetAt: now.Add(time.Hour)}},
	}}
	s.UpdatedAt = now
	if err := s.Save(h.root); err != nil {
		t.Fatal(err)
	}
	h.size(120, 40)
	h.openPanelCmd("/usage")
	h.key("tab", "tab")
	h.want("BY AGENT")
	h.want("1,000")
	h.key("tab")
	h.want("claude")
	h.want("5-hour limit")
	h.want("asks no account or usage service")
}

func TestStatusPanelFillsInOffTheUIThread(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	next, cmd := h.m.cmdStatus("")
	h.m = next.(workspaceModel)
	h.want("checking…")
	h.want("running the doctor checks…")
	h.runCmd(cmd)
	h.want("claude")
	h.want("1 warning · /doctor for details")
	h.want("not installed")
	h.want("shared checkout")
}

func TestDoctorPanel(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.openPanelCmd("/doctor")
	h.want("Doctor")
	h.want("claude auth")
	h.want("→ set a key")
}

func TestModelRolesEditAndSave(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.openPanelCmd("/model")
	h.want("ROLE")
	h.want("coder")
	// move to coder and edit its effort
	roles := h.m.panelRoles()
	idx := -1
	for i, r := range roles {
		if r.Name == "coder" {
			idx = i
		}
	}
	for i := 0; i < idx; i++ {
		h.key("down")
	}
	h.key("enter")
	if !h.m.panel.editing {
		t.Fatal("Enter edits the row")
	}
	h.key("tab", "tab") // effort field
	if h.m.panel.field != apprFieldEffort {
		t.Fatalf("field = %d", h.m.panel.field)
	}
	before := h.m.roleAgent(roles[idx]).Effort
	h.key("right")
	after := h.m.roleAgent(roles[idx]).Effort
	opts := effortOptions(roles[idx].Agent.Runner, roles[idx].Agent.Model)
	if len(opts) > 1 && before == after {
		t.Errorf("→ cycles the effort: %q", after)
	}
	h.key("tab") // model field
	h.key("enter")
	if h.m.panel.editing {
		t.Error("Enter ends the edit")
	}
	h.key("s")
	if len(opts) > 1 {
		if len(h.roleCalls) != 1 || h.roleCalls[0].name != "coder" || h.roleCalls[0].ac.Effort != after {
			t.Errorf("saved: %+v", h.roleCalls)
		}
		h.want("saved in project.yaml")
	}
	h.key("s")
	h.want("nothing changed")
	h.key("esc")
}

func TestModelRankingsAndProfilesTabs(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.openPanelCmd("/model")
	h.key("tab")
	h.want("r fetches the rankings")
	h.key("tab")
	h.want("save current roles as…")
	h.m.deps.listProfiles = func() []string { return []string{"cheap"} }
	h.key("esc")
	h.openPanelCmd("/model profile")
	h.want("cheap")
}

func TestProviderPanelRemoveAndTest(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	if err := credentials.Set("deepseek", credentials.Cred{APIKey: "sk-abcdefghijklmnop1234"}); err != nil {
		t.Fatal(err)
	}
	h.openPanelCmd("/provider")
	h.want("deepseek")
	h.want("sk-…1234")
	h.wantNot("abcdefghijklmnop")
	h.runCmd(h.key("t"))
	h.want("the key works (1 models)")
	h.key("x")
	if h.m.overlay != wsOverlayConfirm {
		t.Fatal("x asks first")
	}
	h.key("n")
	if h.m.overlay != wsOverlayPanel {
		t.Error("n returns to the panel")
	}
	h.key("x", "y")
	if _, ok, _ := credentials.Get("deepseek"); ok {
		t.Error("the key is still saved")
	}
	if h.m.overlay != wsOverlayPanel {
		t.Error("the panel stays open")
	}
	h.want("no providers connected")
	h.key("a")
	if h.m.overlay != wsOverlayConnect {
		t.Error("a opens the key entry")
	}
}

func TestConfigPanelEditsWithTheChecksOfConfigSet(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.openPanelCmd("/config")
	all := config.Settings()
	target := -1
	for i, s := range all {
		if s.Key == "workspace.max_parallel" {
			target = i
		}
	}
	h.m.panel.sel = target
	h.key("enter")
	if !h.m.panel.inputing {
		t.Fatal("Enter edits")
	}
	for _, r := range "99" {
		h.key(string(r))
	}
	h.key("enter")
	h.want("whole number from 0 to 16")
	if config.LoadProject(h.root).Workspace.MaxParallel != 0 {
		t.Error("a refused value changed something")
	}
	h.key("enter")
	h.m.panelIn.SetValue("4")
	h.key("enter")
	if h.m.cfg.Workspace.MaxParallel != 4 || config.LoadProject(h.root).Workspace.MaxParallel != 4 {
		t.Errorf("max_parallel = %d", h.m.cfg.Workspace.MaxParallel)
	}
	h.want("saved in project.yaml")
}

func TestContextPanelShowsSizes(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.openPanelCmd("/context")
	h.want("Context")
	h.want("Lead prompt")
	h.want("The lead gets")
}

func TestMemoryPanelShowAddAndSearch(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.openPanelCmd("/memory")
	h.want("MEMORY.md is empty")
	h.key("esc")
	h.openPanelCmd("/memory add The build uses make")
	wantLine(t, h, "ok", "added to MEMORY.md")
	h.want("The build uses make")
	h.key("esc")
	h.openPanelCmd("/memory add The build uses make")
	wantLine(t, h, "system", "already in MEMORY.md")
	h.key("esc")
	h.openPanelCmd("/memory search build")
	h.want("Memory · build")
	h.key("esc")
	h.typeCmd("/memory bogus")
	wantLine(t, h, "error", "usage: /memory")
}

func TestDiffCommandOpensTheSessionDiff(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	h.typeCmd("/diff")
	if h.m.screen != wsScreenDiff || h.m.diff == nil {
		t.Fatalf("/diff opens the diff view: screen %v", h.m.screen)
	}
	h.want("Session diff")
	h.key("esc")
}

func TestPanelFilesAreNotWrittenByOpening(t *testing.T) {
	h := panelHarness(t)
	h.size(120, 40)
	for _, c := range []string{"/usage", "/status", "/model", "/provider", "/config", "/context", "/doctor"} {
		h.openPanelCmd(c)
		h.key("esc")
	}
	if _, err := os.Stat(filepath.Join(h.root, ".decsr", "project.yaml")); err == nil {
		t.Error("opening the panels must not write the project file")
	}
}
