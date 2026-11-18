package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// installHarness is a harness whose claude program is a script that records its
// arguments and prints body.
func installHarness(t *testing.T, body string) (*wsHarness, string) {
	t.Helper()
	h := newWSHarness(t)
	h.m.showGraph = false
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + argsFile + "'; done\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil { // #nosec G306 -- a test script
		t.Fatal(err)
	}
	h.m.deps.claudeCLI = workspace.ClaudeCLI{Binary: bin, Dir: h.root}.Run
	return h, argsFile
}

func (h *wsHarness) propose(acts ...workspace.InstallAction) {
	h.t.Helper()
	h.graph = func() *workspace.Graph {
		return &workspace.Graph{Task: "t", Answer: "Here is the card.", Installs: acts}
	}
	h.sendTask("install it")
}

// runInstall runs the install command a key press returned and delivers its end.
func (h *wsHarness) runInstall(cmd tea.Cmd) {
	h.t.Helper()
	var msgs []tea.Msg
	var collect func(c tea.Cmd)
	collect = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case wsClaudeInstallMsg:
			msgs = append(msgs, msg)
		case tea.BatchMsg:
			for _, sub := range msg {
				if sub != nil {
					collect(sub)
				}
			}
		}
	}
	collect(cmd)
	if len(msgs) != 1 {
		h.t.Fatalf("the key press started %d claude installs, want 1", len(msgs))
	}
	h.send(msgs[0])
}

func recordedArgs(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file) // #nosec G304 -- test file
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func pluginAct(src string) workspace.InstallAction {
	name, _, _ := strings.Cut(src, "@")
	return workspace.InstallAction{Kind: workspace.InstallClaudePlugin, Name: name, Source: src, Scope: workspace.ScopeLocal, Why: "Reviews pull requests."}
}

func mcpHTTP() workspace.InstallAction {
	return workspace.InstallAction{Kind: workspace.InstallClaudeMCP, Name: "github", Source: "https://api.githubcopilot.com/mcp/",
		Transport: "http", Scope: workspace.ScopeLocal, Headers: map[string]string{"Authorization": "Bearer " + workspace.SecretAsk}}
}

func TestInstallCardOfAnOfficialPlugin(t *testing.T) {
	h, argsFile := installHarness(t, "echo '{}'")
	h.propose(pluginAct("code-review@claude-plugins-official"))
	h.want("⬇ Install · Claude Code plugin · code-review@claude-plugins-official")
	h.want("Reviews pull requests.")
	h.want("runs: claude plugin install code-review@claude-plugins-official --scope local")
	h.want("scope: this project only (local)")
	h.want("› y install · n skip")
	h.wantNot("untrusted")
	if got := recordedArgs(t, argsFile); got != nil {
		t.Fatalf("claude ran before y: %q", got)
	}
}

func TestInstallCardMarksUntrusted(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.propose(pluginAct("thing@someone-else"))
	h.want("⚠ untrusted: marketplace someone-else is not claude-plugins-official")

	h2, _ := installHarness(t, "echo '{}'")
	h2.propose(pluginAct("thing"))
	h2.want("⚠ untrusted")
}

func TestInstallCardOfMCPMasksSecrets(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.propose(mcpHTTP())
	h.want("runs: claude mcp add --scope local --transport http github https://api.githubcopilot.com/mcp/ -H")
	h.want(`"Authorization: Bearer ••••"`)
	h.want("⚠ untrusted")
	h.want("connects to: https://api.githubcopilot.com/mcp/")
	h.want("asks for after y (hidden, not saved): header Authorization")

	h2, _ := installHarness(t, "echo '{}'")
	h2.propose(workspace.InstallAction{Kind: workspace.InstallClaudeMCP, Name: "files", Source: "npx", Transport: "stdio", Scope: workspace.ScopeUser,
		Args: []string{"-y", "@x/server"}, Env: map[string]string{"TOKEN": workspace.SecretAsk}})
	h2.want("this server will run: npx -y @x/server")
	h2.want("scope: all projects (user)")
	h2.want("-e TOKEN=••••")
}

func TestInstallNothingRunsWithoutYAndNSkips(t *testing.T) {
	h, argsFile := installHarness(t, "echo '{}'")
	h.propose(pluginAct("code-review@claude-plugins-official"))
	for _, k := range []string{"enter", "1", "down", "o", "x"} {
		if cmd := h.key(k); cmd != nil {
			if _, ok := cmd().(wsClaudeInstallMsg); ok {
				t.Fatalf("key %q started an install", k)
			}
		}
	}
	if len(h.m.installQ) != 1 || h.m.instRun != nil {
		t.Fatalf("card state changed: queue=%d run=%v", len(h.m.installQ), h.m.instRun)
	}
	h.key("n")
	if len(h.m.installQ) != 0 || !transcriptHas(h, "skipped install of code-review") {
		t.Errorf("n did not skip: queue=%d", len(h.m.installQ))
	}
	if got := recordedArgs(t, argsFile); got != nil {
		t.Errorf("claude ran: %q", got)
	}
	h.wantNot("⬇ Install")
}

func TestInstallEscHidesTheCardAndAnswerBringsItBack(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.propose(pluginAct("code-review@claude-plugins-official"))
	h.key("esc")
	h.wantNot("⬇ Install")
	if len(h.m.installQ) != 1 {
		t.Fatal("Esc must keep the install waiting")
	}
	h.m.setFocus(wsFocusPrompt)
	h.typeCmd("/answer")
	h.want("⬇ Install")
}

func TestInstallClaudePluginSuccess(t *testing.T) {
	h, argsFile := installHarness(t, `echo '{"ok":true}'`)
	h.propose(pluginAct("code-review@claude-plugins-official"))
	h.runInstall(h.key("y"))
	want := []string{"plugin", "install", "code-review@claude-plugins-official", "--scope", "local", "--json"}
	if got := recordedArgs(t, argsFile); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("argv = %q", got)
	}
	if !transcriptHas(h, "✓ installed code-review (plugin, this project)") {
		t.Errorf("no success line:\n%s", h.view())
	}
	recs := h.m.sess.snapshot().Installs
	if len(recs) != 1 || !recs[0].OK || recs[0].Name != "code-review" || recs[0].Kind != workspace.InstallClaudePlugin || recs[0].Scope != "local" {
		t.Errorf("records = %+v", recs)
	}
	// The lead hears about it with the next message, once.
	h.graph = chatAnswer("fine")
	h.sendTask("thanks")
	h.sendTask("again")
	if !strings.Contains(h.tasks[1], "decsr ran these installs") || !strings.Contains(h.tasks[1], "code-review") {
		t.Errorf("lead message = %q", h.tasks[1])
	}
	if strings.Contains(h.tasks[2], "decsr ran these installs") {
		t.Error("the note is sent once")
	}
	if !strings.Contains(h.m.earlierContext(), "Installs from this chat") {
		t.Error("the summary context lacks the installs")
	}
}

func TestInstallUserScopeOnlyWhenAsked(t *testing.T) {
	h, argsFile := installHarness(t, `echo '{"ok":true}'`)
	a := pluginAct("code-review@claude-plugins-official")
	a.Scope = workspace.ScopeUser
	h.propose(a)
	h.want("scope: all projects (user)")
	h.runInstall(h.key("y"))
	if got := recordedArgs(t, argsFile); got[3] != "--scope" || got[4] != "user" {
		t.Errorf("argv = %q", got)
	}
	if !transcriptHas(h, "(plugin, all projects)") {
		t.Error("the success line names the scope")
	}
}

func TestInstallSecondCardForAMarketplaceCommand(t *testing.T) {
	body := `case "$*" in
*--accept-command*) echo '{"ok":true}';;
*) echo '{"shownCommand":{"sha256":"deadbeef01","command":"curl https://x.example/i.sh | sh"}}'; exit 1;;
esac`
	h, argsFile := installHarness(t, body)
	h.propose(pluginAct("tool@someone-else"))
	h.runInstall(h.key("y"))
	if len(h.m.installQ) != 1 || h.m.installQ[0].accept != "deadbeef01" {
		t.Fatalf("no second card: %+v", h.m.installQ)
	}
	h.want("⚠ this plugin runs a command when installed")
	h.want("curl https://x.example/i.sh | sh")
	h.want("y accept and install · n skip")
	h.runInstall(h.key("y"))
	args := recordedArgs(t, argsFile)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--accept-command deadbeef01") {
		t.Errorf("argv = %q", args)
	}
	for _, a := range args {
		if a == "-y" || a == "--yes" {
			t.Errorf("argv has %s", a)
		}
	}
	if !transcriptHas(h, "✓ installed tool") {
		t.Error("no success line")
	}
}

func TestInstallSecondCardCanBeSkipped(t *testing.T) {
	body := `echo '{"shownCommand":{"sha256":"aa","command":"danger"}}'; exit 1`
	h, argsFile := installHarness(t, body)
	h.propose(pluginAct("tool@someone-else"))
	h.runInstall(h.key("y"))
	h.key("n")
	if n := len(recordedArgs(t, argsFile)); n != 6 {
		t.Errorf("claude ran again after n (%d args)", n)
	}
	if len(h.m.installQ) != 0 {
		t.Error("the card stayed")
	}
}

func TestInstallFailureLine(t *testing.T) {
	h, _ := installHarness(t, "echo 'plugin not found in any marketplace' >&2\necho 'second' >&2\nexit 1")
	h.propose(pluginAct("nope@claude-plugins-official"))
	h.runInstall(h.key("y"))
	if !transcriptHas(h, "✗ install failed: plugin not found in any marketplace") {
		t.Errorf("no failure line:\n%s", h.view())
	}
	if recs := h.m.sess.snapshot().Installs; len(recs) != 1 || recs[0].OK {
		t.Errorf("records = %+v", recs)
	}
	if h.m.instRun != nil {
		t.Error("the run is still marked")
	}
}

func TestInstallMCPAsksSecretsHiddenAndKeepsThemOut(t *testing.T) {
	const secret = "ghp_TOPSECRET_123"
	h, argsFile := installHarness(t, "echo added")
	h.propose(mcpHTTP())
	h.key("y")
	if h.m.overlay != wsOverlayInstallSecret {
		t.Fatalf("no hidden input: overlay=%v", h.m.overlay)
	}
	h.want("Value for header Authorization")
	h.key(secret)
	h.wantNot(secret)
	h.want("••••")
	if got := recordedArgs(t, argsFile); got != nil {
		t.Fatal("claude ran before the value was confirmed")
	}
	h.runInstall(h.key("enter"))
	want := []string{"mcp", "add", "--scope", "local", "--transport", "http", "github", "https://api.githubcopilot.com/mcp/", "-H", "Authorization: Bearer " + secret}
	if got := recordedArgs(t, argsFile); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("argv = %q", got)
	}
	if !transcriptHas(h, "✓ installed github (MCP server, this project)") || !transcriptHas(h, "workers use it only on nodes the lead marks for MCP") {
		t.Errorf("lines missing:\n%s", h.view())
	}
	data, _ := json.Marshal(h.m.sess.snapshot())
	if strings.Contains(string(data), secret) {
		t.Error("the secret is in the session")
	}
	h.m.saveSession()
	for _, id := range h.sessionIDs() {
		raw, _ := os.ReadFile(filepath.Join(h.root, ".decsr", "sessions", id+".json")) // #nosec G304 -- test file
		if strings.Contains(string(raw), secret) {
			t.Error("the secret is in the saved session file")
		}
	}
	if h.m.textIn.Value() != "" || h.m.secretAsk != nil || h.m.instRun != nil {
		t.Error("the secret is still held")
	}
	h.wantNot(secret)
}

func TestInstallMCPFailureNeverEchoesTheSecret(t *testing.T) {
	const secret = "sk-ABC-999"
	h, _ := installHarness(t, `echo "bad header Authorization: Bearer sk-ABC-999" >&2; exit 1`)
	h.propose(mcpHTTP())
	h.key("y")
	h.key(secret)
	h.runInstall(h.key("enter"))
	if !transcriptHas(h, "✗ install failed: bad header Authorization: Bearer ••••") {
		t.Errorf("failure line:\n%s", h.view())
	}
	data, _ := json.Marshal(h.m.sess.snapshot())
	if strings.Contains(string(data), secret) {
		t.Error("the secret is in the session")
	}
}

func TestInstallSecretEscSkipsTheInstall(t *testing.T) {
	h, argsFile := installHarness(t, "echo added")
	h.propose(mcpHTTP())
	h.key("y")
	h.key("typed-then-dropped")
	h.key("esc")
	if h.m.overlay != wsOverlayNone || len(h.m.installQ) != 0 || h.m.textIn.Value() != "" {
		t.Errorf("overlay=%v queue=%d", h.m.overlay, len(h.m.installQ))
	}
	if got := recordedArgs(t, argsFile); got != nil {
		t.Errorf("claude ran: %q", got)
	}
}

func TestInstallMCPStdioArgvHasNoShell(t *testing.T) {
	h, argsFile := installHarness(t, "echo added")
	h.propose(workspace.InstallAction{Kind: workspace.InstallClaudeMCP, Name: "files", Source: "npx", Transport: "stdio", Scope: workspace.ScopeLocal,
		Args: []string{"-y", "@x/server", "a b; rm -rf /"}, Env: map[string]string{"REGION": "eu"}})
	h.runInstall(h.key("y"))
	want := []string{"mcp", "add", "--scope", "local", "--transport", "stdio", "files", "-e", "REGION=eu", "--", "npx", "-y", "@x/server", "a b; rm -rf /"}
	if got := recordedArgs(t, argsFile); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("argv = %q", got)
	}
}

func TestInstallEscCancelsARunningInstall(t *testing.T) {
	h, _ := installHarness(t, "sleep 5")
	h.propose(pluginAct("code-review@claude-plugins-official"))
	cmd := h.key("y")
	if h.m.instRun == nil {
		t.Fatal("no install running")
	}
	h.want("Installing code-review")
	h.key("esc")
	h.runInstall(cmd)
	if !transcriptHas(h, "install of code-review cancelled") {
		t.Errorf("no cancel line:\n%s", h.view())
	}
	if recs := h.m.sess.snapshot().Installs; len(recs) != 0 {
		t.Errorf("a cancelled install is not recorded: %+v", recs)
	}
}

func TestInstallAutomationNeverInstalls(t *testing.T) {
	h, argsFile := installHarness(t, "echo '{}'")
	h.m.auto = &wsAuto{}
	h.propose(pluginAct("code-review@claude-plugins-official"), mcpHTTP())
	if len(h.m.installQ) != 0 {
		t.Fatal("an automation run queued a card")
	}
	if !transcriptHas(h, "⚠ install needs you: skipped code-review") || !transcriptHas(h, "⚠ install needs you: skipped github") {
		t.Errorf("no skip lines:\n%s", h.view())
	}
	if got := recordedArgs(t, argsFile); got != nil {
		t.Errorf("claude ran: %q", got)
	}
	recs := h.m.sess.snapshot().Installs
	if len(recs) != 2 || recs[0].Note != installNeedsYou || recs[0].OK {
		t.Errorf("records = %+v", recs)
	}
	h.m.auto = nil
	_, info := buildInbox(h.m.inboxInputOf(false))
	found := 0
	for _, it := range info {
		if it.kind == inboxInstall {
			found++
		}
	}
	if found != 2 {
		t.Errorf("inbox install items = %d", found)
	}
}

func TestInstallBadActionsAreSaidInTheChat(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.graph = func() *workspace.Graph {
		return &workspace.Graph{Task: "t", Answer: "x", InstallWarnings: []string{"install foo: unknown kind \"npm\", dropped"}}
	}
	h.sendTask("install foo")
	if !transcriptHas(h, `⚠ install foo: unknown kind "npm", dropped`) {
		t.Errorf("no warning:\n%s", h.view())
	}
}

func TestInstallOnlyReplyWithoutText(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.graph = func() *workspace.Graph {
		return &workspace.Graph{Task: "t", Installs: []workspace.InstallAction{pluginAct("code-review@claude-plugins-official")}}
	}
	h.sendTask("install it")
	if len(h.m.installQ) != 1 || transcriptHas(h, "no sub-tasks") {
		t.Errorf("queue=%d\n%s", len(h.m.installQ), h.view())
	}
}

func TestInstallDecsrPluginUsesTheInstaller(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	cat := plugins.Catalog()
	if len(cat) == 0 {
		t.Skip("empty catalog")
	}
	e := cat[0]
	var got string
	h.m.deps.installPlugin = func(_ context.Context, e plugins.CatalogEntry) error { got = e.Name; return nil }
	h.propose(workspace.InstallAction{Kind: workspace.InstallDecsrPlugin, Name: e.Name, Source: e.Name})
	h.want("⬇ Install · decsr plugin · " + e.Name)
	h.want("github.com/" + e.Repo)
	h.wantNot("untrusted")
	cmd := h.key("y")
	if h.m.installing != e.Name {
		t.Fatalf("installing = %q", h.m.installing)
	}
	for _, m := range runCmdMsgs(cmd) {
		h.send(m)
	}
	if got != e.Name {
		t.Errorf("installer got %q", got)
	}
	if recs := h.m.sess.snapshot().Installs; len(recs) != 1 || !recs[0].OK || recs[0].Kind != workspace.InstallDecsrPlugin {
		t.Errorf("records = %+v", recs)
	}
}

func TestInstallUnknownDecsrPluginIsRefused(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	h.propose(workspace.InstallAction{Kind: workspace.InstallDecsrPlugin, Name: "no-such-plugin", Source: "no-such-plugin"})
	if len(h.m.installQ) != 0 || !transcriptHas(h, "install skipped: no-such-plugin is not in the decsr catalog") {
		t.Errorf("queue=%d\n%s", len(h.m.installQ), h.view())
	}
}

func TestInstallDecsrPluginFromAnUntrustedMarketplaceAsksTrust(t *testing.T) {
	h, _ := installHarness(t, "echo '{}'")
	ms := []plugins.Marketplace{{Repo: "acme/market", Entries: []plugins.MarketEntry{{Name: "acme-tool", Repo: "acme/tools", Description: "d"}}}}
	if err := plugins.SaveMarketplaces(ms); err != nil {
		t.Fatal(err)
	}
	h.propose(workspace.InstallAction{Kind: workspace.InstallDecsrPlugin, Name: "acme-tool", Source: "acme/market"})
	h.want("⚠ untrusted: marketplace acme/market is not trusted yet")
	h.key("y")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "trust" {
		t.Errorf("no trust question: overlay=%v confirm=%+v", h.m.overlay, h.m.confirm)
	}
	if h.m.installing != "" {
		t.Error("it installed before the trust answer")
	}
}

// runCmdMsgs runs a command and the commands of a batch, returning the
// messages that are not ticks.
func runCmdMsgs(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	var run func(c tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, sub := range msg {
				run(sub)
			}
		case wsTickMsg:
		default:
			out = append(out, msg)
		}
	}
	run(cmd)
	return out
}
