package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// Actions of an install card.
const (
	askActInstall   = "install"   // run the install
	askActInstallNo = "installno" // drop the install
)

// installNeedsYou is the note of an install an automation run skipped.
const installNeedsYou = "needs you"

// wsInstallItem is an install that waits for the user's key press.
type wsInstallItem struct {
	act workspace.InstallAction
	// row says where a decsr plugin comes from.
	row discoverRow
	// accept is the sha256 of the marketplace-declared command the second card
	// asks the user to confirm, command its text; both are empty on the first
	// card.
	accept, command string
}

// wsInstallRun is a claude install in flight. vals are the secrets the user
// typed; they live here and nowhere else, and go with the argv only.
type wsInstallRun struct {
	item   wsInstallItem
	cancel context.CancelFunc
	gen    int
	vals   map[string]string
}

// wsSecretAsk is the hidden input that asks for the values marked <ask>.
type wsSecretAsk struct {
	item  wsInstallItem
	slots []string
	idx   int
	vals  map[string]string
	err   string
}

// wsClaudeInstallMsg is the end of a claude plugin or MCP install.
type wsClaudeInstallMsg struct {
	gen         int
	out, errOut string
	err         error
}

func installKindLabel(kind string) string {
	switch kind {
	case workspace.InstallDecsrPlugin:
		return "decsr plugin"
	case workspace.InstallClaudePlugin:
		return "Claude Code plugin"
	}
	return "MCP server (Claude Code)"
}

// installScopeText is the scope as the card shows it.
func installScopeText(a workspace.InstallAction) string {
	switch {
	case a.Kind == workspace.InstallDecsrPlugin:
		return "all your projects (~/.decsr/plugins)"
	case a.Scope == workspace.ScopeUser:
		return "all projects (user)"
	}
	return "this project only (local)"
}

func installScopeShort(a workspace.InstallAction) string {
	switch {
	case a.Kind == workspace.InstallDecsrPlugin:
		return ""
	case a.Scope == workspace.ScopeUser:
		return ", all projects"
	}
	return ", this project"
}

// installLabel is "Claude Code plugin · code-review@claude-plugins-official".
func installLabel(a workspace.InstallAction) string {
	what := a.Name
	if a.Kind == workspace.InstallClaudePlugin {
		what = a.Source
	}
	return installKindLabel(a.Kind) + " · " + what
}

// installUntrusted says why the install carries the untrusted mark; "" when it
// does not.
func installUntrusted(it wsInstallItem) string {
	a := it.act
	switch a.Kind {
	case workspace.InstallClaudePlugin:
		_, market, _ := strings.Cut(a.Source, "@")
		if market != workspace.OfficialMarketplace {
			if market == "" {
				market = "no marketplace named"
			}
			return "marketplace " + market + " is not " + workspace.OfficialMarketplace
		}
	case workspace.InstallDecsrPlugin:
		if it.row.market != "" && !it.row.trusted {
			return "marketplace " + it.row.market + " is not trusted yet; you are asked to trust it first"
		}
	case workspace.InstallClaudeMCP:
		return "an MCP server runs or connects to third-party code"
	}
	return ""
}

// installCard is the card of one install. It takes only y and n.
func installCard(it wsInstallItem) askCard {
	a := it.act
	c := askCard{
		Key:    fmt.Sprintf("install:%s:%s:%s", a.Kind, a.Name, it.accept),
		Head:   "⬇ Install · " + installLabel(a),
		Rec:    -1,
		Strict: true,
		Hint:   "y install · n skip",
		Opts: []askOpt{
			{Label: "Install", Act: askActInstall, Hot: "y"},
			{Label: "Skip", Act: askActInstallNo, Hot: "n"},
		},
	}
	add := func(s string) { c.Body = append(c.Body, workspace.SanitizeText(s)) }
	if a.Why != "" {
		add(a.Why)
	}
	if why := installUntrusted(it); why != "" {
		c.Warn = true
		add("⚠ untrusted: " + why)
	}
	if it.accept != "" {
		c.Warn = true
		c.Hint = "y accept and install · n skip"
		add("⚠ this plugin runs a command when installed:")
		add(it.command)
	}
	switch a.Kind {
	case workspace.InstallDecsrPlugin:
		src := "github.com/" + it.row.e.Repo
		if it.row.e.Path != "" {
			src += "/" + it.row.e.Path
		}
		add("runs: decsr copies " + src + " into ~/.decsr/plugins/" + a.Name)
	case workspace.InstallClaudePlugin:
		line := "runs: " + a.Command()
		if it.accept != "" {
			line += " --accept-command " + it.accept
		}
		add(line)
	case workspace.InstallClaudeMCP:
		add("runs: " + a.Command())
		if a.Transport == "stdio" {
			add("this server will run: " + strings.TrimSpace(a.Source+" "+maskedArgs(a)))
		} else {
			add("connects to: " + a.Source)
		}
	}
	add("scope: " + installScopeText(a))
	if slots := a.SecretSlots(); len(slots) > 0 {
		add("asks for after y (hidden, not saved): " + strings.Join(slots, ", "))
	}
	return c
}

// maskedArgs are the arguments of a stdio server with the secrets masked.
func maskedArgs(a workspace.InstallAction) string {
	out := make([]string, len(a.Args))
	for i, s := range a.Args {
		out[i] = strings.ReplaceAll(s, workspace.SecretAsk, workspace.SecretMask)
	}
	return strings.Join(out, " ")
}

// redactSecrets replaces every secret the user typed in s.
func redactSecrets(s string, vals map[string]string) string {
	for _, v := range vals {
		if v != "" {
			s = strings.ReplaceAll(s, v, workspace.SecretMask)
		}
	}
	return s
}

// installCards is the card of the install that waits first. It is left out
// while another install runs.
func (m workspaceModel) installCards() []askCard {
	if len(m.installQ) == 0 || m.instRun != nil || m.installing != "" {
		return nil
	}
	return []askCard{installCard(m.installQ[0])}
}

// resolveInstall checks an action against what decsr knows and builds the item
// the card shows; the text is the reason it cannot be installed.
func (m workspaceModel) resolveInstall(a workspace.InstallAction) (wsInstallItem, string) {
	it := wsInstallItem{act: a}
	if a.Kind != workspace.InstallDecsrPlugin {
		return it, ""
	}
	r, ok := findDiscover(discoverRows(plugins.LoadMarketplaces()), a.Name)
	if !ok {
		return it, fmt.Sprintf("%s is not in the decsr catalog or an added marketplace (/plugin marketplace add owner/repo adds one)", a.Name)
	}
	if plugins.ValidRepo(a.Source) && a.Source != r.market && a.Source != r.e.Repo {
		return it, fmt.Sprintf("%s is not in %s", a.Name, workspace.Sanitize(a.Source))
	}
	it.row = r
	return it, ""
}

// queueInstalls shows the warnings of the lead's INSTALL section and puts its
// actions in line for a card. An automation run never installs.
func (m workspaceModel) queueInstalls(g *workspace.Graph) workspaceModel {
	if g == nil {
		return m
	}
	for _, w := range g.InstallWarnings {
		m.say("system", "⚠ "+workspace.Sanitize(w))
	}
	queued := false
	for _, a := range g.Installs {
		name := workspace.Sanitize(a.Name)
		if m.auto != nil {
			m.say("system", "⚠ install needs you: skipped "+name)
			m.recordInstall(a, false, installNeedsYou)
			continue
		}
		it, bad := m.resolveInstall(a)
		if bad != "" {
			m.say("error", "install skipped: "+workspace.Sanitize(bad))
			continue
		}
		m.installQ = append(append([]wsInstallItem(nil), m.installQ...), it)
		m.unhideInstalls()
		m.say("system", "⬇ install proposed: "+installLabel(a)+" (y installs, n skips)")
		queued = true
	}
	if queued && m.focus == wsFocusPrompt && m.input.Value() == "" && !m.splitting {
		m.setFocus(wsFocusGraph)
	}
	return m
}

// unhideInstalls shows the install cards that Esc hid.
func (m *workspaceModel) unhideInstalls() {
	hidden := make(map[string]bool, len(m.askHidden))
	for k, v := range m.askHidden {
		if !strings.HasPrefix(k, "install:") {
			hidden[k] = v
		}
	}
	m.askHidden = hidden
}

// recordInstall keeps the outcome of an install in the session.
func (m *workspaceModel) recordInstall(a workspace.InstallAction, ok bool, note string) {
	rec := workspace.InstallRecord{Name: workspace.Sanitize(a.Name), Kind: a.Kind, Scope: a.Scope, OK: ok, Note: note, At: m.now()}
	m.sess.update(func(s *workspace.Session) { s.Installs = append(s.Installs, rec) })
	if note == "" {
		m.installNotes = append(m.installNotes, installsContext([]workspace.InstallRecord{rec})[2:])
	}
	m.saveSession()
}

// takeInstallNotes is the text in front of the next message to the lead that
// tells it what decsr installed since its last reply; it clears the notes.
func (m *workspaceModel) takeInstallNotes() string {
	if len(m.installNotes) == 0 {
		return ""
	}
	s := "decsr ran these installs since your last reply:\n- " + strings.Join(m.installNotes, "\n- ") + "\n\n"
	m.installNotes = nil
	return s
}

// installsContext is what the lead is told about the installs of the session.
func installsContext(recs []workspace.InstallRecord) string {
	var lines []string
	for _, r := range recs {
		res := "installed"
		switch {
		case r.Note != "":
			res = "skipped (" + r.Note + ")"
		case !r.OK:
			res = "failed"
		}
		line := fmt.Sprintf("- %s (%s", r.Name, installKindLabel(r.Kind))
		if r.Scope != "" {
			line += ", scope " + r.Scope
		}
		lines = append(lines, line+"): "+res)
	}
	return strings.Join(lines, "\n")
}

// installBack gives the prompt back when a chat without nodes is left with the
// focus on the cards.
func (m workspaceModel) installBack() workspaceModel {
	if len(m.installQ) == 0 && !m.hasNodes() && m.focus == wsFocusGraph {
		m.setFocus(wsFocusPrompt)
	}
	return m
}

// installNo drops the install of the card.
func (m workspaceModel) installNo() workspaceModel {
	if len(m.installQ) == 0 {
		return m
	}
	m.say("system", "skipped install of "+workspace.Sanitize(m.installQ[0].act.Name))
	m.installQ = append([]wsInstallItem(nil), m.installQ[1:]...)
	return m.installBack()
}

// installYes runs the install of the card; secrets are asked for first.
func (m workspaceModel) installYes() (tea.Model, tea.Cmd) {
	if len(m.installQ) == 0 || m.instRun != nil || m.installing != "" {
		return m, nil
	}
	it := m.installQ[0]
	if slots := it.act.SecretSlots(); len(slots) > 0 {
		m.secretAsk = &wsSecretAsk{item: it, slots: slots, vals: map[string]string{}}
		return m.openSecret(), nil
	}
	m.installQ = append([]wsInstallItem(nil), m.installQ[1:]...)
	return m.runInstallItem(it, nil)
}

// runInstallItem starts the install in the background.
func (m workspaceModel) runInstallItem(it wsInstallItem, vals map[string]string) (tea.Model, tea.Cmd) {
	a := it.act
	m = m.installBack()
	if a.Kind == workspace.InstallDecsrPlugin {
		return m.runDecsrItem(it)
	}
	argv := a.Argv(vals, it.accept)
	ctx, cancel := context.WithCancel(m.runCtx)
	m.instGen++
	gen := m.instGen
	m.instRun = &wsInstallRun{item: it, cancel: cancel, gen: gen, vals: vals}
	run := m.deps.claudeCLI
	cmd := func() tea.Msg {
		out, errOut, err := run(ctx, argv)
		return wsClaudeInstallMsg{gen: gen, out: out, errOut: errOut, err: err}
	}
	return m, tea.Batch(cmd, m.startTick())
}

// runDecsrItem installs a decsr plugin through the installer of /plugin. A
// marketplace that is not trusted asks its trust question first.
func (m workspaceModel) runDecsrItem(it wsInstallItem) (tea.Model, tea.Cmd) {
	name, r := it.act.Name, it.row
	switch m.plug.State(name) {
	case plugins.StateEnabled:
		m.say("system", "plugin "+workspace.Sanitize(name)+" is already installed")
		return m.installBack(), nil
	case plugins.StateDisabled:
		m.instLeadName = name
		return m.pluginInstallCmd(name)
	}
	m.instLeadName = name
	switch {
	case r.market == "":
		return m.startInstall(r.e)
	case !r.trusted:
		return m.pluginInstallCmd(name)
	}
	m.marketPick = pluginPick{market: r.market, e: r.e}
	install := m.deps.installMarket
	return m.runInstall(r.e, func(ctx context.Context) error { return install(ctx, r.market, r.e) })
}

// onClaudeInstall reports the end of a claude plugin or MCP install.
func (m workspaceModel) onClaudeInstall(msg wsClaudeInstallMsg) (tea.Model, tea.Cmd) {
	run := m.instRun
	if run == nil || msg.gen != run.gen {
		return m, nil
	}
	m.instRun = nil
	run.cancel()
	a := run.item.act
	name := workspace.Sanitize(a.Name)
	if errors.Is(msg.err, context.Canceled) {
		m.say("system", "install of "+name+" cancelled")
		return m.installBack(), nil
	}
	res := workspace.ParseClaudeInstall(msg.out, msg.errOut, msg.err, run.item.accept)
	if res.Confirm && a.Kind == workspace.InstallClaudePlugin {
		second := run.item
		second.accept, second.command = res.SHA, res.Command
		m.installQ = append([]wsInstallItem{second}, m.installQ...)
		m.say("system", "⚠ "+name+" declares a command that runs when it installs: confirm it on the card")
		if m.focus == wsFocusPrompt && m.input.Value() == "" && !m.splitting {
			m.setFocus(wsFocusGraph)
		}
		return m, nil
	}
	if res.Confirm || !res.OK {
		line := res.Err
		if res.Confirm {
			line = "the plugin's command was not accepted"
		}
		m.say("error", "✗ install failed: "+redactSecrets(line, run.vals))
		m.recordInstall(a, false, "")
		return m.installBack(), nil
	}
	label := "plugin"
	if a.Kind == workspace.InstallClaudeMCP {
		label = "MCP server"
	}
	m.say("ok", fmt.Sprintf("✓ installed %s (%s%s)", name, label, installScopeShort(a)))
	if a.Kind == workspace.InstallClaudeMCP {
		m.say("system", "workers use it only on nodes the lead marks for MCP")
	}
	m.recordInstall(a, true, "")
	return m.installBack(), m.pluginRefreshCmd()
}

// cancelInstallRun stops the install that runs, when one does.
func (m workspaceModel) cancelInstallRun() (workspaceModel, bool) {
	switch {
	case m.instRun != nil:
		m.instRun.cancel()
		return m, true
	case m.installing != "" && m.instLeadName != "" && m.cancelInstall != nil:
		m.cancelInstall()
		return m, true
	}
	return m, false
}

// installRunLine is the status text while a claude install runs.
func (m workspaceModel) installRunLine() string {
	spin := pixelSpinner.Frames[m.spin%len(pixelSpinner.Frames)]
	return spin + " Installing " + workspace.Sanitize(m.instRun.item.act.Name) + "… (Esc cancels)"
}

// ── hidden input for the values marked <ask> ────────────────────────────────

func (m workspaceModel) openSecret() workspaceModel {
	m.overlay = wsOverlayInstallSecret
	m.textIn.SetValue("")
	m.textIn.Placeholder = "paste the value"
	m.textIn.EchoMode = textinput.EchoPassword
	m.textIn.EchoCharacter = '•'
	m.textIn.Focus()
	return m
}

func (m workspaceModel) closeSecret() workspaceModel {
	m.overlay = wsOverlayNone
	m.textIn.SetValue("")
	m.textIn.EchoMode = textinput.EchoNormal
	m.textIn.Blur()
	m.secretAsk = nil
	return m
}

func (m workspaceModel) secretKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sa := m.secretAsk
	if sa == nil {
		m.overlay = wsOverlayNone
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m = m.closeSecret()
		return m.installNo(), nil
	case "enter":
		v := m.textIn.Value()
		if strings.TrimSpace(v) == "" {
			sa.err = "Type the value, or press Esc to skip the install"
			return m, nil
		}
		sa.vals[sa.slots[sa.idx]] = v
		sa.idx++
		m.textIn.SetValue("")
		if sa.idx < len(sa.slots) {
			return m, nil
		}
		it, vals := sa.item, sa.vals
		m = m.closeSecret()
		m.installQ = append([]wsInstallItem(nil), m.installQ[1:]...)
		return m.runInstallItem(it, vals)
	}
	var cmd tea.Cmd
	m.textIn, cmd = m.textIn.Update(msg)
	sa.err = ""
	return m, cmd
}

func (m workspaceModel) secretBox() string {
	sa := m.secretAsk
	if sa == nil {
		return ""
	}
	boxW := min(70, m.width-2)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	body := []string{
		dim.Render(fmt.Sprintf("Value for %s (%d of %d)", workspace.Sanitize(sa.slots[sa.idx]), sa.idx+1, len(sa.slots))),
		m.textIn.View(),
	}
	if sa.err != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(ui.Fail).Render("! "+sa.err))
	}
	body = append(body, "", dim.Render("Hidden. It goes to the claude command and is never saved by decsr."), dim.Render("Enter confirm · Esc skip the install"))
	return ui.Overlay("Install · "+workspace.Sanitize(sa.item.act.Name), body, nil, boxW, ui.Warn)
}
