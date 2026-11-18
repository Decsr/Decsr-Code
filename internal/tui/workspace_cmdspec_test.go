package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// withColorProfile switches the colour profile for one test.
func withColorProfile(t *testing.T, p termenv.Profile) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// hasColorEscape reports a styling escape other than the plain reset that
// overlays append.
func hasColorEscape(s string) bool {
	return strings.Contains(strings.ReplaceAll(s, "\x1b[0m", ""), "\x1b[")
}

// lastChatLine is the newest line of the chat.
func lastChatLine(h *wsHarness) workspace.Line {
	ls := h.m.sess.snapshot().Transcript
	if len(ls) == 0 {
		h.t.Fatal("the chat is empty")
	}
	return ls[len(ls)-1]
}

func wantLine(t *testing.T, h *wsHarness, who, sub string) {
	t.Helper()
	l := lastChatLine(h)
	if l.Who != who || !strings.Contains(l.Text, sub) {
		t.Fatalf("last line = %s %q, want %s containing %q", l.Who, l.Text, who, sub)
	}
}

// ── the command table ────────────────────────────────────────────────────────

func TestBuiltinCommandsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range builtinCommands() {
		if !strings.HasPrefix(c.name, "/") || c.desc == "" || c.run == nil || c.example == "" || len(c.forms) == 0 {
			t.Errorf("%s: name, desc, run, forms and example are required", c.name)
		}
		if !strings.HasPrefix(c.example, c.name) {
			t.Errorf("%s: the example %q must use the command", c.name, c.example)
		}
		known := false
		for _, g := range cmdGroups[:5] {
			known = known || g == c.group
		}
		if !known {
			t.Errorf("%s: group %q", c.name, c.group)
		}
		for _, n := range append([]string{c.name}, c.aliases...) {
			if seen[n] {
				t.Errorf("%s is defined twice", n)
			}
			seen[n] = true
			if _, gone := removedCommands[n]; gone {
				t.Errorf("%s is both a command and a removed command", n)
			}
		}
		for _, f := range c.forms {
			if !strings.HasPrefix(f.usage, c.name) || f.does == "" {
				t.Errorf("%s: form %+v", c.name, f)
			}
		}
	}
}

func TestBuiltinCommandsFollowTheGroups(t *testing.T) {
	want := map[string][]string{
		ui.GroupNameSession: {"/clear", "/compact", "/resume", "/rename", "/export", "/copy", "/quit"},
		ui.GroupNameRun:     {"/answer", "/check", "/stop", "/retry", "/merge", "/diff", "/pr"},
		ui.GroupNameAgents:  {"/roles", "/model", "/routing", "/autonomy", "/coordinator"},
		ui.GroupNameUsage:   {"/usage", "/status", "/context"},
		ui.GroupNameProject: {"/project", "/config", "/provider", "/memory", "/doctor", "/plugin", "/issue", "/import", "/menu"},
	}
	cmds := builtinCommands()
	order := map[string]int{}
	for i, g := range cmdGroups {
		order[g] = i
	}
	for i := 1; i < len(cmds); i++ {
		if order[cmds[i].group] < order[cmds[i-1].group] {
			t.Errorf("%s comes after %s: the groups are out of order", cmds[i].name, cmds[i-1].name)
		}
	}
	for g, names := range want {
		got := map[string]bool{}
		for _, c := range groupMembers(cmds, g) {
			got[c.name] = true
		}
		for _, n := range names {
			if !got[n] {
				t.Errorf("%s is missing from %s", n, g)
			}
		}
	}
	byName := map[string]wsCommand{}
	for _, c := range cmds {
		byName[c.name] = c
	}
	if byName["/roles"].aliases[0] != "/agents" || byName["/plugin"].aliases[0] != "/plugins" || byName["/quit"].aliases[0] != "/exit" {
		t.Error("the aliases /agents /plugins /exit are kept")
	}
}

func TestRemovedCommandsPrintOneLineAndRunNothing(t *testing.T) {
	want := map[string]string{
		"/approve": "use y / n on the plan card (/answer shows it again)",
		"/reject":  "use y / n on the plan card (/answer shows it again)",
		"/cost":    "use /usage cost",
		"/budget":  "use /usage budget [amount|off]",
		"/rank":    "use /model rank",
		"/connect": "use /provider add <name>",
		"/review":  "use /coordinator now",
		"/profile": "use /model profile save|apply <name>",
	}
	if len(removedCommands) != len(want) {
		t.Errorf("%d removed commands", len(removedCommands))
	}
	h := newWSHarness(t)
	for name, hint := range want {
		before := len(h.m.sess.snapshot().Transcript)
		h.typeCmd(name + " 12")
		if h.m.overlay != wsOverlayNone || h.m.eng != nil || h.m.splitting {
			t.Fatalf("%s ran something", name)
		}
		if got := len(h.m.sess.snapshot().Transcript); got != before+1 {
			t.Fatalf("%s printed %d lines", name, got-before)
		}
		wantLine(t, h, "system", name+" has moved: "+hint)
	}
	for _, n := range []string{"/approve", "/cost", "/review"} {
		if _, ok := h.m.lookup(n); ok {
			t.Errorf("%s is still a command", n)
		}
		if len(h.m.cmdMatches(n)) != 0 {
			t.Errorf("the palette still offers %s", n)
		}
	}
}

func TestCustomCommandsKeepWorkingInTheirOwnGroup(t *testing.T) {
	h := newWSHarness(t)
	wsWriteCommand(t, h.root, "ship", "---\ndescription: Ship it\n---\nShip $ARGUMENTS")
	h.m.custom = h.m.deps.customCommands()
	c, ok := h.m.lookup("/ship")
	if !ok || c.group != ui.GroupNamePlugin {
		t.Fatalf("custom command: %+v %v", c, ok)
	}
	wsWriteCommand(t, h.root, "cost", "shadow")
	h.m.custom = h.m.deps.customCommands()
	if _, ok := h.m.lookup("/cost"); ok {
		t.Error("a custom command must not take a removed name")
	}
}

// ── matching ─────────────────────────────────────────────────────────────────

func TestCmdMatchPositions(t *testing.T) {
	cases := []struct {
		name, q string
		tier    int
		pos     []int
		ok      bool
	}{
		{"plugin", "pl", 0, []int{0, 1}, true},
		{"export", "xp", 1, []int{1, 2}, true},
		{"coordinator", "cdr", 2, []int{0, 4, 10}, true},
		{"plugin", "zz", 0, nil, false},
		{"plugin", "", 0, nil, true},
	}
	for _, c := range cases {
		tier, pos, ok := cmdMatchPositions(c.name, c.q)
		if ok != c.ok || (ok && tier != c.tier) || fmt.Sprint(pos) != fmt.Sprint(c.pos) {
			t.Errorf("%s/%s: tier %d pos %v ok %v", c.name, c.q, tier, pos, ok)
		}
	}
}

func TestRankCommands(t *testing.T) {
	cmds := builtinCommands()
	names := func(typed string) []string {
		var out []string
		for _, c := range rankCommands(cmds, typed) {
			out = append(out, c.name)
		}
		return out
	}
	if got := names("/pl"); len(got) != 1 || got[0] != "/plugin" {
		t.Errorf("/pl = %v", got)
	}
	if got := names("/"); len(got) != len(cmds) {
		t.Errorf("bare / lists %d of %d", len(got), len(cmds))
	}
	if got := names("/re"); len(got) < 3 || !strings.HasPrefix(got[0], "/re") || !strings.HasPrefix(got[1], "/re") {
		t.Errorf("/re starts with prefix matches: %v", got)
	}
	got := names("/agents") // an alias finds its command
	if len(got) == 0 || got[0] != "/roles" {
		t.Errorf("/agents = %v", got)
	}
	if got := names("/xyz"); len(got) != 0 {
		t.Errorf("/xyz = %v", got)
	}
	// Prefix matches come before looser ones.
	p := names("/p")
	if len(p) < 3 || !strings.HasPrefix(p[0], "/p") {
		t.Errorf("/p = %v", p)
	}
}

// ── the palette ──────────────────────────────────────────────────────────────

func TestCmdPaletteShowsTheMockup(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	lines := renderCmdPalette(rankCommands(builtinCommands(), "/pl"), "/pl", 0, 100, 12)
	if len(lines) != cmdPalMaxRows+3 {
		t.Fatalf("%d lines: %q", len(lines), lines)
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(lines[0], "╭") || !strings.HasPrefix(last, "╰") {
		t.Errorf("not a rounded box: %q", lines)
	}
	row := lines[1]
	if !strings.HasPrefix(row, "│ › /plugin") || !strings.HasSuffix(row, "Project │") {
		t.Errorf("row = %q", row)
	}
	if !strings.Contains(row, "[list|search|inst…") {
		t.Errorf("args hint missing: %q", row)
	}
	if ansi.StringWidth(lines[0]) != ansi.StringWidth(row) || ansi.StringWidth(row) != ansi.StringWidth(last) {
		t.Errorf("ragged box: %d %d %d", ansi.StringWidth(lines[0]), ansi.StringWidth(row), ansi.StringWidth(last))
	}
}

func palDescCol(l string) int {
	r := []rune(ansi.Strip(l))
	for _, k := range []string{"write this", "set the title", "manage"} {
		if i := strings.Index(string(r), k); i >= 0 {
			return ansi.StringWidth(string(r)[:i])
		}
	}
	return -1
}

func TestCmdPaletteAlignsNameArgsAndDescription(t *testing.T) {
	for _, mode := range []termenv.Profile{termenv.TrueColor, termenv.Ascii} {
		withColorProfile(t, mode)
		for _, w := range []int{80, 120} {
			cmds := []wsCommand{
				{name: "/export", desc: "write this session to a markdown file", group: "Session"},
				{name: "/rename", args: "<title>", desc: "set the title of this session", group: "Session"},
				{name: "/plugin", args: "[list|search|install|…|remove]", desc: "manage plugins", group: "Project"},
			}
			lines := renderCmdPalette(cmds, "/", 0, w-2, 20)
			col := -1
			rows := 0
			for _, l := range lines {
				if ansi.StringWidth(l) > w-2 {
					t.Errorf("w=%d: %d cells: %q", w, ansi.StringWidth(l), ansi.Strip(l))
				}
				if c := palDescCol(l); c >= 0 {
					rows++
					if col >= 0 && c != col {
						t.Errorf("w=%d: description column %d != %d:\n%s", w, c, col, ansi.Strip(strings.Join(lines, "\n")))
					}
					col = c
				}
			}
			if rows != 3 {
				t.Fatalf("rows = %d", rows)
			}
			text := ansi.Strip(strings.Join(lines, "\n"))
			if !strings.Contains(text, "<title>") || !strings.Contains(text, "[list|search|inst…") || strings.Contains(text, "remove]") {
				t.Errorf("args column wrong:\n%s", text)
			}
		}
	}
}

func TestCmdPaletteKeepsTheArgsColumnWithoutArgs(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	cmds := []wsCommand{
		{name: "/export", desc: "write this session to a markdown file", group: "Session"},
		{name: "/rename", args: "<title>", desc: "set the title of this session", group: "Session"},
	}
	lines := renderCmdPalette(cmds, "/", 0, 100, 12)
	if a, b := palDescCol(lines[2]), palDescCol(lines[3]); a < 0 || a != b {
		t.Errorf("description columns %d and %d:\n%s", a, b, ansi.Strip(strings.Join(lines, "\n")))
	}
}

func TestCmdPaletteMatchedLettersAreUnderlinedInTheGroupColour(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	cmds := rankCommands(builtinCommands(), "/pl")
	row := renderCmdPalette(cmds, "/pl", 0, 100, 12)[1]
	want := lipgloss.NewStyle().Foreground(ui.GroupProject).Underline(true).Bold(true).Background(ui.Sel).Render("p")
	if !strings.Contains(row, want) {
		t.Errorf("matched letter not underlined in the group colour: %q", row)
	}
	if strings.Contains(row, lipgloss.NewStyle().Foreground(ui.GroupProject).Underline(true).Bold(true).Background(ui.Sel).Render("u")) {
		t.Error("an unmatched letter is underlined")
	}
}

func TestCmdPaletteBareListsGroupsWithHeaders(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	cmds := rankCommands(builtinCommands(), "/")
	lines := renderCmdPalette(cmds, "/", 0, 100, 30)
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "│ SESSION") {
		t.Errorf("no group header:\n%s", text)
	}
	if strings.Contains(text, "Session │") {
		t.Errorf("a bare palette has no group tags at the right:\n%s", text)
	}
	if !strings.Contains(lines[len(lines)-2], "↓") || !strings.Contains(lines[len(lines)-2], "more · ↑↓ Tab") {
		t.Errorf("more line: %q", lines[len(lines)-2])
	}
	if n := len(lines) - 2 - 1; n > cmdPalMaxRows {
		t.Errorf("%d rows shown", n)
	}
}

func TestCmdPaletteScrollsAndCountsHiddenRows(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	cmds := rankCommands(builtinCommands(), "/")
	last := len(cmds) - 1
	lines := strings.Join(renderCmdPalette(cmds, "/", last, 100, 30), "\n")
	if !strings.Contains(lines, "› "+cmds[last].name) {
		t.Errorf("the last command is not visible:\n%s", lines)
	}
	if !strings.Contains(lines, "above · ↑↓ Tab") {
		t.Errorf("above marker missing:\n%s", lines)
	}
}

func TestCmdPaletteFitsEveryWidth(t *testing.T) {
	for _, mode := range []termenv.Profile{termenv.TrueColor, termenv.Ascii} {
		withColorProfile(t, mode)
		for _, w := range []int{50, 80, 120} {
			for _, q := range []string{"/", "/p", "/co", "/zz"} {
				cmds := rankCommands(builtinCommands(), q)
				for _, l := range renderCmdPalette(cmds, q, 0, w-2, 12) {
					if ansi.StringWidth(l) > w-2 {
						t.Errorf("w=%d q=%s: %d cells: %q", w, q, ansi.StringWidth(l), ansi.Strip(l))
					}
				}
			}
		}
	}
	if renderCmdPalette(builtinCommands(), "/", 0, 80, 3) != nil {
		t.Error("no room draws nothing")
	}
}

func TestPaletteInTheWorkspaceAt80And120(t *testing.T) {
	for _, sz := range [][2]int{{80, 30}, {120, 40}} {
		h := newWSHarness(t)
		h.size(sz[0], sz[1])
		h.key("/")
		h.want("SESSION")
		h.want("› /help")
		for _, l := range strings.Split(h.view(), "\n") {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%v: a line of %d cells", sz, ansi.StringWidth(l))
			}
		}
		h.key("p", "l")
		h.want("› /plugin")
		h.want("Project │")
		h.wantNot("SESSION")
		h.key("tab")
		if h.m.input.Value() != "/plugin" {
			t.Errorf("Tab completes to %q", h.m.input.Value())
		}
	}
}

func TestPaletteNoColourHasNoEscapes(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	h := newWSHarness(t)
	h.size(80, 30)
	h.key("/", "p", "l")
	if hasColorEscape(h.m.View()) {
		t.Error("NO_COLOR output has colour sequences")
	}
	h.want("› /plugin")
}

// ── /help ────────────────────────────────────────────────────────────────────

func TestHelpListsTheGroupsAt80And120(t *testing.T) {
	for _, sz := range [][2]int{{80, 30}, {120, 40}} {
		h := newWSHarness(t)
		h.size(sz[0], sz[1])
		h.typeCmd("/help")
		if h.m.overlay != wsOverlayHelp {
			t.Fatal("/help opens the panel")
		}
		for _, w := range []string{"Help", "Session", "Run", "Agents", "Usage", "Project", "/clear", "/stop", "/coordinator", "/usage", "/provider",
			"/help <command> shows its forms and examples"} {
			h.want(w)
		}
		for _, l := range strings.Split(h.view(), "\n") {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%v: a line of %d cells", sz, ansi.StringWidth(l))
			}
		}
		h.key("esc")
		if h.m.overlay != wsOverlayNone {
			t.Error("Esc closes /help")
		}
	}
}

func TestHelpGroupNamesUseTheGroupColour(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	lines := helpOverview(builtinCommands(), 90)
	if !strings.Contains(lines[0], lipgloss.NewStyle().Foreground(ui.GroupSession).Bold(true).Render("Session")[:20]) {
		t.Errorf("Session is not in the session colour: %q", lines[0])
	}
	if !strings.Contains(lines[2], fgPrefix(ui.GroupAgents)) {
		t.Errorf("the Agents line is not pink: %q", lines[2])
	}
}

func TestHelpCommandShowsFormsAndExample(t *testing.T) {
	for _, sz := range [][2]int{{80, 30}, {120, 40}} {
		h := newWSHarness(t)
		h.size(sz[0], sz[1])
		h.typeCmd("/help plugin")
		for _, w := range []string{"/plugin", "alias /plugins", "Form", "What it does", "/plugin install <name>", "install a plugin of the catalog", "Example", "/plugin install superpowers"} {
			h.want(w)
		}
		for _, l := range strings.Split(h.view(), "\n") {
			if ansi.StringWidth(l) > sz[0] {
				t.Errorf("%v: a line of %d cells", sz, ansi.StringWidth(l))
			}
		}
	}
	h := newWSHarness(t)
	h.typeCmd("/help /usage")
	h.want("/usage budget <amount|off>")
	h.key("esc")
	h.typeCmd("/help nope")
	wantLine(t, h, "error", "no command /nope")
	h.typeCmd("/help cost")
	wantLine(t, h, "system", "/cost has moved")
}

func TestHelpNoColourHasNoEscapes(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	h := newWSHarness(t)
	h.typeCmd("/help usage")
	if hasColorEscape(h.m.View()) {
		t.Error("colour sequences without colour")
	}
	h.want("/usage cost")
}

func TestHelpScrollsOnASmallScreen(t *testing.T) {
	h := newWSHarness(t)
	h.size(60, 16)
	h.typeCmd("/help model")
	h.want("1-")
	for range 20 {
		h.key("down")
	}
	h.want("Example")
	h.wantNot("alias")
	h.key("up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up")
	h.want("What it does")
}

// ── the new forms ────────────────────────────────────────────────────────────

func TestRenameSetsTheTitle(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/rename")
	wantLine(t, h, "error", "usage: /rename <title>")
	h.typeCmd("/rename Add JSON output")
	wantLine(t, h, "ok", "session renamed to Add JSON output")
	if h.m.sess.snapshot().Title != "Add JSON output" {
		t.Errorf("title = %q", h.m.sess.snapshot().Title)
	}
	h.toGraph()
	h.key("c")
	h.typeCmd("/rename Add JSON output")
	s, err := workspace.LoadSession(h.root, h.m.sess.snapshot().ID)
	if err != nil || s.Title != "Add JSON output" {
		t.Errorf("not saved: %v %v", s, err)
	}
	h.typeCmd("/rename " + strings.Repeat("x", 200))
	if n := len([]rune(h.m.sess.snapshot().Title)); n != maxTitleRunes {
		t.Errorf("title of %d runes", n)
	}
}

func TestOSC52Sequence(t *testing.T) {
	if got := osc52("hi", false); got != "\x1b]52;c;aGk=\x07" {
		t.Errorf("plain = %q", got)
	}
	if got := osc52("hi", true); got != "\x1bPtmux;\x1b\x1b]52;c;aGk=\x07\x1b\\" {
		t.Errorf("tmux = %q", got)
	}
}

func TestCopyWritesTheLastAnswer(t *testing.T) {
	var out strings.Builder
	prev := clipboardOut
	clipboardOut = &out
	t.Cleanup(func() { clipboardOut = prev })

	h := newWSHarness(t)
	h.typeCmd("/copy")
	wantLine(t, h, "error", "nothing to copy")

	h.m.say("lead", "the first answer")
	h.m.say("lead", "the second answer")
	h.m.say("you", "thanks")
	h.m.deps.getenv = func(string) string { return "" }
	h.m.input.SetValue("/copy")
	cmd := h.key("enter")
	wantLine(t, h, "ok", "copied the last answer of the lead")
	if cmd == nil {
		t.Fatal("no clipboard command")
	}
	cmd()
	if out.String() != osc52("the second answer", false) {
		t.Errorf("written %q", out.String())
	}

	out.Reset()
	h.m.deps.getenv = func(k string) string {
		if k == "TMUX" {
			return "/tmp/tmux-1000/default,1,0"
		}
		return ""
	}
	h.m.input.SetValue("/copy")
	cmd = h.key("enter")
	cmd()
	if out.String() != osc52("the second answer", true) {
		t.Errorf("under tmux %q", out.String())
	}
}

func TestStopAndRetryNodes(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/stop")
	wantLine(t, h, "system", "nothing is running")
	h.typeCmd("/retry T1")
	wantLine(t, h, "error", "no graph yet")

	h.toGraph()
	h.key("c")
	h.typeCmd("/retry")
	wantLine(t, h, "error", "usage: /retry <node>")
	h.typeCmd("/retry T99")
	wantLine(t, h, "error", "no node T99")
	h.typeCmd("/stop T1")
	wantLine(t, h, "error", "T1 is not running")
	h.typeCmd("/stop")
	wantLine(t, h, "system", "nothing is running")
	h.typeCmd("/stop T99")
	wantLine(t, h, "error", "no node T99")

	h.key("esc") // to the graph
	h.key("A")   // accepts the suggestions
	h.key("c")
	h.typeCmd("/retry t1")
	wantLine(t, h, "ok", "started T1")
	h.pump(func(m workspaceModel) bool {
		return m.status("T1") == workspace.StatusRunning || m.status("T1") == workspace.StatusDone
	})
}

func TestModelForms(t *testing.T) {
	h := newWSHarness(t)
	h.m.deps.saveProfile = func(name string, _ map[string]config.AgentConfig, _ config.RoutingProfile) error {
		if name == "boom" {
			return os.ErrPermission
		}
		return nil
	}
	h.typeCmd("/model")
	if h.m.overlay != wsOverlayPanel || h.m.panel.kind != panelModel || h.m.panel.ps.Tab != modelTabRoles {
		t.Error("/model opens the roles tab")
	}
	h.key("esc")
	h.typeCmd("/model profile")
	if h.m.overlay != wsOverlayPanel || h.m.panel.ps.Tab != modelTabProfiles {
		t.Error("/model profile opens the profiles tab")
	}
	h.key("esc")
	h.typeCmd("/model profile save fast")
	wantLine(t, h, "ok", `profile "fast" saved`)
	h.typeCmd("/model profile save Bad Name")
	wantLine(t, h, "error", "usage: /model")
	h.typeCmd("/model profile save BAD")
	wantLine(t, h, "error", "Name must be")
	h.typeCmd("/model profile apply nosuch")
	if l := lastChatLine(h); l.Who != "error" {
		t.Errorf("applying an unknown profile: %+v", l)
	}
	h.typeCmd("/model nonsense")
	wantLine(t, h, "error", "usage: /model")
	h.typeCmd("/model rank")
	if h.m.overlay != wsOverlayPanel || h.m.panel.ps.Tab != modelTabRank {
		t.Errorf("/model rank opens the rankings tab: %v %d", h.m.overlay, h.m.panel.ps.Tab)
	}
}

func TestUsageForms(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/usage")
	if h.m.overlay != wsOverlayPanel || h.m.panel.kind != panelUsage || h.m.panel.ps.Tab != usageTabSession || h.m.panel.perNode {
		t.Errorf("/usage: %v %+v", h.m.overlay, h.m.panel)
	}
	h.key("esc")
	h.typeCmd("/usage cost")
	if h.m.panel.kind != panelUsage || !h.m.panel.perNode {
		t.Errorf("/usage cost: %+v", h.m.panel)
	}
	h.key("esc")
	h.typeCmd("/usage budget")
	if h.m.panel.kind != panelUsage || h.m.panel.ps.Tab != usageTabBudget {
		t.Errorf("/usage budget: %+v", h.m.panel)
	}
	h.key("esc")
	h.typeCmd("/usage budget 12")
	if h.m.sess.snapshot().BudgetUSD != 12 {
		t.Errorf("budget = %v", h.m.sess.snapshot().BudgetUSD)
	}
	h.typeCmd("/usage budget off")
	if h.m.sess.snapshot().BudgetUSD != 0 {
		t.Error("budget off")
	}
	h.typeCmd("/usage budget x")
	wantLine(t, h, "error", "usage: /usage budget")
	h.typeCmd("/usage weird")
	wantLine(t, h, "error", "usage: /usage")
}

func TestCoordinatorNowStartsAReview(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/coordinator now")
	wantLine(t, h, "system", "nothing to review")
	h.typeCmd("/coordinator on")
	wantLine(t, h, "system", "Coordinator of this session is on")
}

func TestConfigGetSetAndList(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/config")
	if h.m.overlay != wsOverlayPanel || h.m.panel.kind != panelConfig {
		t.Fatalf("/config opens the panel: %v", h.m.overlay)
	}
	h.want("workspace.max_parallel")
	h.want("Workspace")
	h.want("Interface")
	h.key("esc")
	h.typeCmd("/config list")
	h.key("esc")

	h.typeCmd("/config get workspace.max_parallel")
	wantLine(t, h, "system", "workspace.max_parallel = (not set)")
	h.typeCmd("/config set workspace.max_parallel 4")
	wantLine(t, h, "ok", "workspace.max_parallel = 4")
	if h.m.cfg.Workspace.MaxParallel != 4 {
		t.Error("the live config did not change")
	}
	if config.LoadProject(h.root).Workspace.MaxParallel != 4 {
		t.Error("project.yaml was not written")
	}
	h.typeCmd("/config get workspace.max_parallel")
	wantLine(t, h, "system", "workspace.max_parallel = 4")

	h.typeCmd("/config set workspace.max_parallel 99")
	wantLine(t, h, "error", "whole number from 0 to 16")
	if h.m.cfg.Workspace.MaxParallel != 4 || config.LoadProject(h.root).Workspace.MaxParallel != 4 {
		t.Error("a refused value changed something")
	}
	h.typeCmd("/config set nope.key 1")
	wantLine(t, h, "error", "no setting")
	h.typeCmd("/config get nope.key")
	wantLine(t, h, "error", "no setting")
	h.typeCmd("/config set workspace.coordinator")
	wantLine(t, h, "error", "usage: /config")
	h.typeCmd("/config set project.build_cmd go build ./...")
	if config.LoadProject(h.root).Project.BuildCmd != "go build ./..." {
		t.Error("a value with spaces is kept whole")
	}
	h.typeCmd("/config set ui.theme dark")
	wantLine(t, h, "ok", "takes effect at the next start")
	h.typeCmd("/config set routing.mode auto-role")
	if h.m.cfg.Routing.Mode != "auto-role" {
		t.Error("routing.mode goes through /routing")
	}
}

func TestProviderForms(t *testing.T) {
	h := newWSHarness(t) // HOME is a temp directory
	h.typeCmd("/provider list")
	wantLine(t, h, "system", "no providers connected")
	if err := credentials.Set("deepseek", credentials.Cred{APIKey: "sk-abcdefghijklmnop1234"}); err != nil {
		t.Fatal(err)
	}
	h.typeCmd("/provider list")
	wantLine(t, h, "system", "deepseek")
	if l := lastChatLine(h); strings.Contains(l.Text, "abcdefghijklmnop") || !strings.Contains(l.Text, "sk-…1234") {
		t.Errorf("the key must be masked: %q", l.Text)
	}
	h.typeCmd("/provider remove nosuch")
	wantLine(t, h, "error", "no saved key")
	h.typeCmd("/provider remove deepseek")
	if h.m.overlay != wsOverlayConfirm {
		t.Fatal("remove asks first")
	}
	h.key("n")
	if _, ok, _ := credentials.Get("deepseek"); !ok {
		t.Error("n kept the key")
	}
	h.typeCmd("/provider remove deepseek")
	h.key("y")
	wantLine(t, h, "ok", "deleted the saved key for deepseek")
	if _, ok, _ := credentials.Get("deepseek"); ok {
		t.Error("the key is still saved")
	}
	h.typeCmd("/provider test nosuch")
	wantLine(t, h, "error", "unknown provider")
	h.typeCmd("/provider test deepseek")
	wantLine(t, h, "error", "no API key")
	h.typeCmd("/provider bogus")
	wantLine(t, h, "error", "usage: /provider")

	h.typeCmd("/provider add groq")
	if h.m.overlay != wsOverlayConnect || h.m.conn == nil {
		t.Errorf("/provider add opens the connect flow: %v", h.m.overlay)
	}
}

func TestProviderTestUsesTheSavedKey(t *testing.T) {
	h := newWSHarness(t)
	if err := credentials.Set("groq", credentials.Cred{APIKey: "gsk-abcdefghijklmnop5678"}); err != nil {
		t.Fatal(err)
	}
	var gotBase, gotKey string
	h.m.deps.probeKey = func(base, key string) ([]string, error) {
		gotBase, gotKey = base, key
		return []string{"a", "b"}, nil
	}
	h.m.input.SetValue("/provider test groq")
	cmd := h.key("enter")
	if cmd == nil {
		t.Fatal("no probe command")
	}
	h.send(cmd())
	wantLine(t, h, "ok", "groq: the key works (2 models)")
	if gotKey != "gsk-abcdefghijklmnop5678" || !strings.Contains(gotBase, "groq") {
		t.Errorf("probed %q with %q", gotBase, gotKey)
	}
	h.send(providerTestMsg{name: "groq", err: os.ErrDeadlineExceeded})
	wantLine(t, h, "error", "groq:")
}

func TestPaletteRunNowUsesTheNewNames(t *testing.T) {
	for _, n := range []string{"/help", "/status", "/usage", "/roles", "/resume", "/project"} {
		c, ok := newWSHarness(t).m.lookup(n)
		if !ok || !runsNow(c) {
			t.Errorf("%s runs at once from the palette", n)
		}
	}
	c, _ := newWSHarness(t).m.lookup("/provider")
	if runsNow(c) {
		t.Error("/provider must be edited in the prompt")
	}
}

// ── colours on the graph, the plan card and the sidebar ──────────────────────

func TestGraphNodeCardsColourTheRole(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	g := &workspace.Graph{Nodes: []*workspace.Node{
		{ID: "T1", Title: "Parse", Kind: workspace.KindWork, Status: workspace.StatusTodo, Role: "coder"},
		{ID: "T2", Title: "Audit", Kind: workspace.KindWork, Status: workspace.StatusTodo, Role: "security", DependsOn: []string{"T1"}},
		{ID: "T3", Title: "Docs", Kind: workspace.KindWork, Status: workspace.StatusTodo, SuggestedRole: "reviewer", DependsOn: []string{"T1"}},
	}}
	out := renderCanvas(g, canvasOpts{width: 100, height: 20, runners: map[string]string{"coder": "claude", "security": "codex"}, models: map[string]string{}})
	for _, tc := range []struct{ role, text string }{{"coder", "coder"}, {"security", "security"}, {"reviewer", "reviewer"}} {
		if !strings.Contains(ansi.Strip(out), tc.text) {
			t.Errorf("role %s missing", tc.role)
		}
	}
	if fgPrefix(ui.RoleColor("coder")) == fgPrefix(ui.RoleColor("security")) {
		t.Fatal("roles share a colour")
	}
	for _, c := range []lipgloss.TerminalColor{ui.RoleColor("coder"), ui.RoleColor("security"), ui.RoleColor("reviewer"), ui.Claude, ui.Codex} {
		if !strings.Contains(out, fgPrefix(c)) {
			t.Errorf("colour %v missing from the canvas", c)
		}
	}
}

// fgPrefix is the escape sequence that starts a foreground colour.
func fgPrefix(c lipgloss.TerminalColor) string {
	out := lipgloss.NewStyle().Foreground(c).Render("x")
	return out[:strings.Index(out, "x")]
}

func TestPlanCardColoursRolesAndRunners(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	card := approvalCard{Mode: "manual", Rows: []approvalRow{
		{ID: "T1", Title: "Parse", Grade: "medium", Agent: "claude · sonnet", Runner: "claude", Role: "coder", Access: "writes", Reason: "fits"},
		{ID: "T2", Title: "Docs", Grade: "small", Agent: "ollama · llama3", Runner: "ollama", Role: "docs", NewRole: true, Access: "writes"},
	}}
	for _, w := range []int{80, 120} {
		out := strings.Join(renderApprovalCard(card, w), "\n")
		for _, c := range []lipgloss.TerminalColor{ui.RoleColor("coder"), ui.RoleColor("docs"), ui.Claude, ui.Local} {
			if !strings.Contains(out, fgPrefix(c)) {
				t.Errorf("w=%d: colour %v missing from the plan card", w, c)
			}
		}
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > w {
				t.Errorf("w=%d: line of %d cells", w, ansi.StringWidth(l))
			}
		}
		if p := ansi.Strip(out); !strings.Contains(p, "coder · writes · fits") || !strings.Contains(p, "docs (new · uses the pm's agent) · writes") {
			t.Errorf("plan card text changed:\n%s", p)
		}
	}
	withColorProfile(t, termenv.Ascii)
	if hasColorEscape(strings.Join(renderApprovalCard(card, 100), "\n")) {
		t.Error("NO_COLOR plan card has escapes")
	}
}

func TestRunnerColourDelegatesToTheTheme(t *testing.T) {
	for _, r := range []string{"claude", "dsh", "deepseek", "codex", "openai", "opencode", "ollama", "mlx", "lmstudio", ""} {
		if runnerColor(r) != ui.RunnerColor(r) {
			t.Errorf("runnerColor(%q)", r)
		}
	}
}

func TestResultLinesInTheChat(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	h := newWSHarness(t)
	for _, l := range []struct{ who, kind, text string }{{"ok", ui.ResultOK, "done thing"}, {"warn", ui.ResultWarn, "careful"}, {"error", ui.ResultError, "broke it"}, {"system", ui.ResultInfo, "fyi"}} {
		h.m.say(l.who, l.text)
		g, _ := ui.ResultGlyph(l.kind)
		h.want(g + " " + l.text)
	}
	h.m.showGraph = false
	h.want("fyi")
	if !strings.Contains(ansi.Strip(strings.Join(renderFeedMsg("ok", "done thing", time.Time{}, 40), "\n")), "✓ done thing") {
		t.Error("the feed shows the check mark")
	}
}

func TestConfigListIsHermetic(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/config")
	lines := ansi.Strip(strings.Join(h.m.panelOpts().Body, "\n"))
	for _, s := range config.Settings() {
		if !strings.Contains(lines, s.Key) {
			t.Errorf("/config does not list %s", s.Key)
		}
	}
	if _, err := os.Stat(filepath.Join(h.root, ".decsr", "project.yaml")); err == nil {
		t.Error("listing must not write the project file")
	}
}

// palShape is the line count and the widths of a palette.
func palShape(lines []string) (n, minW, maxW int) {
	minW = 1 << 30
	for _, l := range lines {
		w := ansi.StringWidth(l)
		minW, maxW = min(minW, w), max(maxW, w)
	}
	return len(lines), minW, maxW
}

func TestCmdPaletteKeepsItsSize(t *testing.T) {
	for _, mode := range []termenv.Profile{termenv.TrueColor, termenv.Ascii} {
		withColorProfile(t, mode)
		cmds := builtinCommands()
		cmds = append(cmds, wsCommand{name: "/insecure-defaults:audit", args: "[path]", desc: "audit the defaults of this project", group: "Plugin commands", tag: "(plugin:insecure-defaults)"})
		for _, w := range []int{60, 100, 128} {
			var shapes [][3]int
			for _, q := range []string{"/", "/us", "/zzz", "/insecure"} {
				ranked := rankCommands(cmds, q)
				lines := renderCmdPalette(ranked, q, 0, w, 14)
				n, lo, hi := palShape(lines)
				if lo != w || hi != w {
					t.Errorf("w=%d q=%s: widths %d..%d", w, q, lo, hi)
				}
				shapes = append(shapes, [3]int{n, lo, hi})
			}
			for _, s := range shapes {
				if s != shapes[0] {
					t.Errorf("w=%d: shapes differ: %v", w, shapes)
				}
			}
		}
	}
}

func TestCmdPaletteNoMatchAndLongNames(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	lines := renderCmdPalette(nil, "/xyz", 0, 100, 14)
	if !strings.Contains(lines[1], "no command matches /xyz") {
		t.Errorf("first row: %q", lines[1])
	}
	cmds := []wsCommand{
		{name: "/insecure-defaults:audit-all", args: "[path]", desc: "audit", group: "Plugin commands"},
		{name: "/plan", args: "[x]", desc: "audit", group: "Run"},
	}
	l := renderCmdPalette(cmds, "/", 0, 100, 14)
	a, b := ansi.StringWidth(strings.SplitN(ansi.Strip(l[2]), "[path]", 2)[0]), ansi.StringWidth(strings.SplitN(ansi.Strip(l[4]), "[x]", 2)[0])
	if a < 0 || a != b {
		t.Errorf("args columns %d and %d:\n%s", a, b, ansi.Strip(strings.Join(l, "\n")))
	}
	if !strings.Contains(ansi.Strip(l[2]), "…") {
		t.Errorf("long name not cut: %q", ansi.Strip(l[2]))
	}
}
