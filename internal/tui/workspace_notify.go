package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsNotifyFunc delivers a note to the channels of the notification config.
type wsNotifyFunc = func(config.NotifyConfig, notify.Note)

// wsNoteMax is the longest note body, in characters.
const wsNoteMax = 140

func wsSendNote(c config.NotifyConfig, n notify.Note) { notify.Send(c, n) }

// wsNotifyWait lets the last notes leave before the Workspace exits.
func wsNotifyWait() { notify.Wait(2 * time.Second) }

// notifyTitle is "decsr" or "decsr · <project>".
func (m workspaceModel) notifyTitle() string {
	if p := m.project(); p != "" {
		return "decsr · " + p
	}
	return "decsr"
}

// notifyNote sends a short note ("needs you", failed, done) through the
// configured channels. It is best-effort, never blocks, and does nothing when
// the model has no sender (tests). Other Workspace code can call it directly.
func (m workspaceModel) notifyNote(level notify.Level, body string) {
	if m.deps.notify == nil {
		return
	}
	m.deps.notify(m.cfg.Notifications, notify.Note{Title: m.notifyTitle(), Body: wsShort(body), Level: level})
}

// wsShort keeps the first line of s, collapses blanks and cuts it to wsNoteMax.
func wsShort(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = oneLine(s)
	if utf8.RuneCountInString(s) > wsNoteMax {
		r := []rune(s)
		s = string(r[:wsNoteMax-1]) + "…"
	}
	return s
}

// notifyEvent reports a finished or failed node and the end of a run. m.g must
// already be the snapshot taken after ev. Starts and output chunks are silent.
func (m workspaceModel) notifyEvent(ev workspace.Event) {
	if m.deps.notify == nil || m.g == nil {
		return
	}
	n := m.g.Node(ev.NodeID)
	switch ev.Type {
	case workspace.EventDone:
		if n != nil && n.Kind == workspace.KindCheck {
			body := "Build & test passed"
			if m.iso != nil {
				body += " — /merge to apply"
			}
			m.notifyNote(notify.Done, body)
		}
	case workspace.EventWaiting:
		if n != nil && n.Report != nil && n.Report.Status == workspace.ReportBlocked {
			m.notifyNote(notify.NeedsYou, ev.NodeID+" is blocked: "+wsShort(ev.Text))
		} else {
			m.notifyNote(notify.NeedsYou, ev.NodeID+" asks: "+wsShort(ev.Text))
		}
	case workspace.EventFailed:
		if ev.Text == "stopped" {
			return
		}
		if n != nil && n.Kind == workspace.KindCheck {
			m.notifyNote(notify.Failed, "Build & test failed: "+wsShort(ev.Text))
		} else {
			m.notifyNote(notify.Failed, ev.NodeID+" failed: "+wsShort(ev.Text))
		}
	default:
		return
	}
	if _, running, done, failed := m.g.Counts(); running == 0 && len(m.g.Runnable()) == 0 {
		body := fmt.Sprintf("Run finished: %d done, %d failed", done, failed)
		if w := m.g.CountWaiting(); w > 0 {
			body += fmt.Sprintf(", %d waiting", w)
		}
		m.notifyNote(notify.Done, body)
	}
}

// notifySplitDone tells the user that new sub-tasks wait for roles.
func (m workspaceModel) notifySplitDone() {
	if m.g == nil {
		return
	}
	need := 0
	for _, n := range m.g.Nodes {
		if n.Kind == workspace.KindWork && n.Role == "" && n.Status == workspace.StatusTodo {
			need++
		}
	}
	if need > 0 {
		m.notifyNote(notify.NeedsYou, fmt.Sprintf("Assign roles: %d sub-tasks are waiting", need))
	}
}

// notifyPlugin tells the user that a plugin question blocks the lead's plan.
func (m workspaceModel) notifyPlugin(name, verb string) {
	m.notifyNote(notify.NeedsYou, fmt.Sprintf("Plugin question: %s %s? Answer y or n", verb, name))
}
