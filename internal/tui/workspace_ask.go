package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const planPickKey = "plan:pick"

// leadAskCard is the card of the question the lead asked; ok is false when it
// asked none. The options carry no Hot key: only the number, the arrows and
// Enter choose.
func (m workspaceModel) leadAskCard() (askCard, bool) {
	a := m.leadAsk
	if a == nil {
		return askCard{}, false
	}
	c := askCard{
		Key: fmt.Sprintf("lead:%d", m.leadAskSeq), Rec: -1,
		Head: "lead asks",
		Body: []string{workspace.SanitizeText(a.Question)},
	}
	for _, o := range a.Options {
		if label, reason := askOptionSplit(o); label != "" {
			c.Opts = append(c.Opts, askOpt{Label: label, Reason: reason, Act: askActLead})
		}
	}
	if a.Recommended >= 1 && a.Recommended <= len(c.Opts) {
		c.Rec = a.Recommended - 1
	}
	c.Opts = append(c.Opts, askOpt{Label: "Type your own answer", Act: askActLeadOwn})
	return c, true
}

// queueAsk keeps the question of the lead's reply for a card. An automation
// run has nobody to ask: the run ends as it does for any answer in the chat.
func (m workspaceModel) queueAsk(g *workspace.Graph) workspaceModel {
	if g == nil || g.Ask == nil {
		return m
	}
	if m.auto != nil {
		m.say("system", "⚠ the lead asks a question: needs you")
		return m
	}
	m.leadAsk = g.Ask
	m.leadAskSeq++
	if m.focus == wsFocusPrompt && m.input.Value() == "" && !m.splitting {
		m.setFocus(wsFocusGraph)
	}
	return m
}

// answerLead sends the chosen option to the lead as a message the user typed.
// A draft in the prompt is left alone.
func (m workspaceModel) answerLead(label string) (tea.Model, tea.Cmd) {
	task := "Answer: " + label
	draft := m.input.Value()
	next, cmd := m.sendTask(task, task)
	nm := next.(workspaceModel)
	if draft != "" {
		nm.input.SetValue(draft)
	}
	return nm, cmd
}

// planPickCard is the small picker that Enter opens on the plan card. Agents
// can be edited only while a node waits for its approval.
func (m workspaceModel) planPickCard() askCard {
	c := askCard{
		Key: planPickKey, Head: "Plan", Rec: 0,
		Hint: "↑↓ move · Enter choose · y approve · n cancel · Esc later",
		Opts: []askOpt{{Label: "Approve", Act: askActPlanOK, Hot: "y"}},
	}
	if m.g != nil && len(m.g.PendingApproval()) > 0 {
		c.Hint = "↑↓ move · Enter choose · y approve · e edit agents · n cancel · Esc later"
		c.Opts = append(c.Opts, askOpt{Label: "Edit agents", Act: askActPlanEdit, Hot: "e"})
	}
	c.Opts = append(c.Opts, askOpt{Label: "Cancel", Act: askActPlanNo, Hot: "n"})
	return c
}

// openPlanPick shows the plan picker.
func (m workspaceModel) openPlanPick() workspaceModel {
	m.planPick = true
	m.askKey = ""
	return m
}
