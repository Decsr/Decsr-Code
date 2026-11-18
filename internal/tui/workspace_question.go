package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// Actions of a card option.
const (
	askActAnswer = "answer" // reply with the option text
	askActOwn    = "own"    // fill the prompt with "@T2 "
	askActAllow  = "allow"  // allow the programs for the node, then retry
	askActAlways = "always" // allow the programs in project.yaml, then retry
	askActNo     = "no"     // hide the card
)

// askOpt is one choice of a card.
type askOpt struct {
	Label, Reason string
	Act           string
}

// askCard is a card that waits for a key press about one node: the question
// of a worker or the commands its runner refused.
type askCard struct {
	Key   string // node, kind and attempt: a hidden card stays hidden
	Node  string
	Head  string
	Warn  bool // drawn in the warning colour
	Body  []string
	Opts  []askOpt
	Rec   int      // index of the recommended option; -1 none
	Progs []string // askActAllow and askActAlways: the programs to allow
	Cmds  []string // the refused commands
}

// askOptionSplit cuts "8080 - matches the dev server" into label and reason.
func askOptionSplit(s string) (label, reason string) {
	s = oneLine(workspace.SanitizeText(s))
	if i := strings.Index(s, " - "); i > 0 {
		return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:])
	}
	return s, ""
}

// questionCard is the card of a node that waits with a question.
func questionCard(n *workspace.Node) askCard {
	r := n.Report
	c := askCard{
		Key: fmt.Sprintf("ask:%s:%d", n.ID, n.AttemptNumber()), Node: n.ID, Rec: -1,
		Head: n.ID + " asks · " + oneLine(workspace.Sanitize(n.Title)),
		Body: []string{workspace.SanitizeText(r.Ask())},
	}
	for _, o := range r.Options {
		if label, reason := askOptionSplit(o); label != "" {
			c.Opts = append(c.Opts, askOpt{Label: label, Reason: reason, Act: askActAnswer})
		}
	}
	if r.Recommended >= 1 && r.Recommended <= len(c.Opts) {
		c.Rec = r.Recommended - 1
	}
	c.Opts = append(c.Opts, askOpt{Label: "Type your own answer", Act: askActOwn})
	return c
}

// askCards are the cards that wait for the user, oldest node first; for a
// node the question comes before the refused commands. A card hidden with Esc
// is left out.
func (m workspaceModel) askCards() []askCard {
	if m.g == nil {
		return nil
	}
	nodes := make([]*workspace.Node, 0, len(m.g.Nodes))
	for _, n := range m.g.Nodes {
		if n.Kind == workspace.KindWork {
			nodes = append(nodes, n)
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].EndedAt.Before(nodes[j].EndedAt) })
	var out []askCard
	add := func(c askCard) {
		if !m.askHidden[c.Key] {
			out = append(out, c)
		}
	}
	for _, n := range nodes {
		if n.Status == workspace.StatusWaiting && !n.Limited() && n.Report != nil && n.Report.Status == workspace.ReportQuestion {
			add(questionCard(n))
		}
		if c, ok := m.deniedCard(n); ok {
			add(c)
		}
	}
	return out
}

// activeAsk is the card the keys act on, with the cursor in effect.
func (m workspaceModel) activeAsk() (c askCard, sel int, ok bool) {
	cards := m.askCards()
	if len(cards) == 0 {
		return askCard{}, 0, false
	}
	c = cards[0]
	sel = max(c.Rec, 0)
	if m.askKey == c.Key {
		sel = min(max(m.askSel, 0), len(c.Opts)-1)
	}
	return c, sel, true
}

// askVisible is true when the card is on screen and takes keys: the feed is
// shown and the prompt is not in focus.
func (m workspaceModel) askVisible() bool {
	return m.screen == wsScreenMain && !m.showGraph && m.focus != wsFocusPrompt && m.overlay == wsOverlayNone && !m.splitting
}

// renderAskCard draws the card in at most width cells.
func renderAskCard(c askCard, sel, width int) []string {
	iw := max(width-4, 4)
	dim := lipgloss.NewStyle().Foreground(ui.Muted)
	text := lipgloss.NewStyle().Foreground(ui.Text)
	acc := lipgloss.NewStyle().Foreground(ui.Accent)
	col := ui.Accent
	if c.Warn {
		col = ui.Warn
	}
	rows := []string{lipgloss.NewStyle().Foreground(col).Bold(true).Render(truncateOneLine(c.Head, iw))}
	for _, b := range c.Body {
		for _, l := range hardWrap(b, iw) {
			rows = append(rows, text.Render(l))
		}
	}
	for i, o := range c.Opts {
		mark, st := "  ", text
		if i == sel {
			mark, st = acc.Render("› "), acc.Bold(true)
		}
		label := fmt.Sprintf("%d. %s", i+1, o.Label)
		if i == c.Rec {
			label += " (Recommended)"
		}
		line := mark + st.Render(label)
		if o.Reason != "" {
			line += dim.Render(" - " + o.Reason)
		}
		rows = append(rows, truncateOneLine(line, iw))
	}
	keys := fmt.Sprintf("↑↓ move · 1-%d pick · Enter choose · Esc later", min(len(c.Opts), 9))
	rows = append(rows, "", dim.Render(keys+"  (/answer brings it back)"))

	b := lipgloss.NewStyle().Foreground(col)
	out := make([]string, 0, len(rows)+2)
	out = append(out, b.Render("╭"+strings.Repeat("─", iw+2)+"╮"))
	for _, row := range rows {
		out = append(out, b.Render("│")+" "+padRight(ansi.Truncate(row, iw, "…"), iw)+" "+b.Render("│"))
	}
	return append(out, b.Render("╰"+strings.Repeat("─", iw+2)+"╯"))
}

// askKeyPress handles a key for the card; handled is false when the key is
// not one of the card's, so the screen takes it as usual.
func (m workspaceModel) askKeyPress(msg tea.KeyMsg) (next tea.Model, cmd tea.Cmd, handled bool) {
	if !m.askVisible() {
		return m, nil, false
	}
	c, sel, ok := m.activeAsk()
	if !ok {
		return m, nil, false
	}
	k := msg.String()
	move := func(d int) (tea.Model, tea.Cmd, bool) {
		m.askKey, m.askSel = c.Key, min(max(sel+d, 0), len(c.Opts)-1)
		return m, nil, true
	}
	switch k {
	case "up":
		return move(-1)
	case "down":
		return move(1)
	case "esc":
		m.hideAsk(c)
		return m, nil, true
	case "enter":
		nm, cmd := m.askChoose(c, sel)
		return nm, cmd, true
	}
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		if i := int(k[0] - '1'); i < len(c.Opts) {
			nm, cmd := m.askChoose(c, i)
			return nm, cmd, true
		}
	}
	return m, nil, false
}

func (m *workspaceModel) hideAsk(c askCard) {
	hidden := make(map[string]bool, len(m.askHidden)+1)
	for k, v := range m.askHidden {
		hidden[k] = v
	}
	hidden[c.Key] = true
	m.askHidden = hidden
}

// askChoose runs the option i of card c.
func (m workspaceModel) askChoose(c askCard, i int) (tea.Model, tea.Cmd) {
	o := c.Opts[i]
	n := m.g.Node(c.Node)
	m.askKey = ""
	switch o.Act {
	case askActAnswer:
		return m.replyKeepingDraft(c.Node, "Answer: "+o.Label)
	case askActOwn:
		if n != nil {
			m = m.replyKey(n)
		}
		return m, nil
	case askActNo:
		m.hideAsk(c)
		return m, nil
	case askActAllow, askActAlways:
		return m.allowAndRetry(c, o.Act == askActAlways)
	}
	return m, nil
}

// replyKeepingDraft is sendReply that leaves a draft in the prompt alone.
func (m workspaceModel) replyKeepingDraft(id, msg string) (tea.Model, tea.Cmd) {
	draft := m.input.Value()
	next, cmd := m.sendReply(id, msg)
	nm := next.(workspaceModel)
	if draft != "" {
		nm.input.SetValue(draft)
	}
	return nm, cmd
}

// cmdAnswer is /answer: it shows the cards that were hidden with Esc.
func (m workspaceModel) cmdAnswer(string) (tea.Model, tea.Cmd) {
	m.askHidden = nil
	m.askKey = ""
	cards := m.askCards()
	if len(cards) == 0 {
		m.setNotice("No question or refused command waits for you", false)
		return m, nil
	}
	m.showGraph = false
	m.setFocus(wsFocusGraph)
	m.feedFollow = true
	return m, nil
}

// unhideNodeAsk shows the cards of a node again (the inbox opens them).
func (m workspaceModel) unhideNodeAsk(id string) workspaceModel {
	if len(m.askHidden) == 0 {
		return m
	}
	hidden := make(map[string]bool, len(m.askHidden))
	for k, v := range m.askHidden {
		if !strings.Contains(k, ":"+id+":") {
			hidden[k] = v
		}
	}
	m.askHidden = hidden
	return m
}

// askAfterEvent says what a node that just ended asks, and moves the focus to
// the feed so the card takes keys when the prompt is empty.
func (m workspaceModel) askAfterEvent(ev workspace.Event) workspaceModel {
	if m.g == nil {
		return m
	}
	n := m.g.Node(ev.NodeID)
	if n == nil || n.Kind != workspace.KindWork {
		return m
	}
	switch ev.Type {
	case workspace.EventWaiting:
		if n.Report == nil {
			return m
		}
		switch n.Report.Status {
		case workspace.ReportQuestion:
			m.say("lead", n.ID+" asks: "+oneLine(workspace.SanitizeText(n.Report.Ask())))
			if m.focus == wsFocusPrompt && m.input.Value() == "" && !m.splitting {
				m.setFocus(wsFocusGraph)
			}
		case workspace.ReportBlocked:
			what := n.Report.Summary
			if what == "" {
				what = n.Report.Ask()
			}
			m.say("lead", n.ID+" is blocked: "+oneLine(workspace.SanitizeText(what)))
		}
	}
	switch ev.Type {
	case workspace.EventDone, workspace.EventWaiting, workspace.EventFailed:
		m.sayRefusal(n)
	}
	return m
}
