package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const (
	// autoCompactTenths is the share of the context window (in tenths) that the
	// lead's last turn may use before the next message compacts the conversation.
	autoCompactTenths = 7

	wsCompactingHint = "The lead is compacting the conversation…"
)

// wsLeadReq is one message to the lead. summary is the session summary the
// first turn of a conversation carries; conv is the conversation to resume.
type wsLeadReq struct {
	task, summary string
	existing      []string
	live          []workspace.LiveNode
	next          int
	routing       bool
	conv          workspace.LeadConv
}

// wsAfterCompact is the message that waits for an automatic compaction.
type wsAfterCompact struct{ task, shown string }

// leadRunnerName is the runner of the lead's agent.
func leadRunnerName(pm config.AgentConfig) string {
	if pm.Runner == "" {
		return "opencode"
	}
	return pm.Runner
}

// keepLead stores what a lead turn returned: its usage, its conversation and
// the size of its prompt.
func (m *workspaceModel) keepLead(res workspace.LeadResult) {
	m.sess.update(func(s *workspace.Session) {
		s.In += res.Usage.InputTokens
		s.Out += res.Usage.OutputTokens
		s.LeadSessionID, s.LeadRunner, s.LeadModel = res.Conv.SessionID, res.Conv.Runner, res.Conv.Model
		s.LeadContext = res.Context
	})
}

// sayLeadNotes says that the lead's conversation began again.
func (m *workspaceModel) sayLeadNotes(res workspace.LeadResult) {
	switch {
	case res.ResumeFailed:
		m.say("system", "⚠ lead: could not resume the conversation, started fresh with the summary")
	case res.AgentChanged:
		m.say("system", "⚠ lead: the lead agent changed, started a new conversation with the summary")
	}
}

// sayTools is the line that replaces the live step once the lead has answered:
// what its tools did, nothing when it used none.
func (m *workspaceModel) sayTools() {
	if m.stream == nil {
		return
	}
	if sum := m.stream.act.Summary(); sum != "" {
		m.say("system", sum)
	}
}

// leadLabel is the busy line of the lead while it works.
func (m workspaceModel) leadLabel() string {
	if m.leadKind != "" {
		return "compacting the conversation…"
	}
	return "thinking…"
}

// autoCompactDue is true when the lead's conversation has grown past 70% of the
// model's context window and workspace.auto_compact is on. It is asked only in
// front of a message the user sent.
func (m workspaceModel) autoCompactDue() bool {
	if m.auto != nil || m.deps.compact == nil {
		return false
	}
	c := m.liveCfg()
	if !c.Workspace.AutoCompactOn() {
		return false
	}
	s := m.sess.snapshot()
	pm := c.Agents["pm"]
	rn := leadRunnerName(pm)
	if s.LeadSessionID == "" || s.LeadContext <= 0 || s.LeadRunner != rn || s.LeadModel != pm.Model {
		return false
	}
	win := runner.ContextWindow(rn, pm.Model)
	if pm.MaxContextTokens > 0 {
		win = pm.MaxContextTokens
	}
	return s.LeadContext*10 > win*autoCompactTenths
}

// compactInput is the text the compaction call summarizes.
func (m workspaceModel) compactInput(focus string) string {
	s := m.sess.snapshot()
	return workspace.CompactInput(s.Summary, s.Transcript, s.LeadSince, m.g, focus)
}

// cmdCompact asks the lead to summarize the conversation, then starts a new one
// from the summary and folds the finished nodes.
func (m workspaceModel) cmdCompact(args string) (tea.Model, tea.Cmd) {
	if m.busy() {
		return m.refuseBusy()
	}
	if m.reviewing || m.splitting {
		m.say("system", "wait for the lead: it is still working on the last request (Esc cancels)")
		return m, nil
	}
	in := m.compactInput(args)
	if in == "" || m.deps.compact == nil {
		m.say("system", "nothing to compact")
		return m, nil
	}
	return m.startCompact(in, "compact", m.now(), nil)
}

// startCompact runs the compaction call. after is the message to send once an
// automatic compaction has ended.
func (m workspaceModel) startCompact(input, kind string, at time.Time, after *wsAfterCompact) (tea.Model, tea.Cmd) {
	shown := ""
	if after != nil {
		shown = after.shown
	}
	ctx, gen := m.beginLead(shown)
	m.leadKind, m.afterCompact, m.compactAt = kind, after, at
	m.input.Placeholder = wsCompactingHint
	m.compactBefore = m.sess.snapshot().LeadContext
	if m.compactBefore <= 0 {
		m.compactBefore = tokenutil.EstimateTokens(input)
	}
	compact, stream := m.deps.compact, m.stream
	cmd := func() tea.Msg {
		sum, u, err := compact(ctx, input, stream.add)
		return wsCompactDoneMsg{gen: gen, summary: sum, usage: u, err: err}
	}
	return m, tea.Batch(cmd, m.startTick())
}

func (m workspaceModel) onCompactDone(msg wsCompactDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.splitGen || !m.splitting {
		return m, nil
	}
	after := m.afterCompact
	m.splitting, m.cancelSplit, m.leadKind, m.afterCompact = false, nil, "", nil
	m.input.Placeholder = wsInputHint
	if msg.err != nil {
		if after != nil { // the message is not lost: it goes on in the old conversation
			m.autoSay("could not compact the conversation: " + oneLine(msg.err.Error()))
			m.input.Placeholder = wsSplittingHint
			return m.startLead(after.task, after.shown)
		}
		m.say("error", oneLine(msg.err.Error()))
		return m, nil
	}
	m.sess.update(func(s *workspace.Session) {
		s.Summary = msg.summary
		s.LeadSessionID, s.LeadRunner, s.LeadModel, s.LeadContext = "", "", "", 0
		s.LeadSince = m.compactAt
		s.In += msg.usage.InputTokens
		s.Out += msg.usage.OutputTokens
	})
	folded, kept := 0, 0
	if m.eng != nil { // the finished nodes join the summary the workers read
		var err error
		if folded, kept, err = m.applyCompact(); err != nil {
			m.say("error", oneLine(err.Error()))
		}
	}
	sizes := fmt.Sprintf("≈%s → %s tokens", approxK(m.compactBefore), approxK(tokenutil.EstimateTokens(msg.summary)))
	if after != nil {
		m.autoSay("compacted the conversation (" + sizes + ")")
	} else {
		m.say("system", "compacted · "+sizes)
	}
	if folded > 0 {
		line := fmt.Sprintf("folded %d finished %s", folded, pluralize(folded, "node", "nodes"))
		if kept > 0 {
			line += fmt.Sprintf("; kept %d that unfinished nodes still depend on", kept)
		}
		m.say("system", line)
	}
	m.saveSession()
	if after != nil {
		m.input.Placeholder = wsSplittingHint
		return m.startLead(after.task, after.shown)
	}
	return m, nil
}

// approxK is a token count as the compaction line shows it: "42K", "800".
func approxK(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%dK", (n+500)/1000)
}
