package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// LeadConv is the lead's own CLI conversation: its id and the agent that
// holds it. The zero value is no conversation.
type LeadConv struct {
	SessionID, Runner, Model string
}

// LeadTurn is one message of the user to the lead.
type LeadTurn struct {
	Task  string
	Roles []Role
	// Opts has the plugins, the routing flag and the live nodes (for CHANGES).
	Opts     SplitOptions
	Existing []string // IDs of the nodes already in the graph
	Next     int      // first number for new sub-tasks; 0 = after the highest
	// Runner and Model are the lead's agent; Dir is the project.
	Runner, Model, Dir string
	// Tools runs the lead as an agent with read-only tools, in one conversation
	// that later turns resume. False is a plain call without tools: every turn
	// then carries the project context and the summary.
	Tools bool
	Conv  LeadConv
	// Summary is the session summary, sent on the first turn of a conversation.
	Summary string
	// Context is the project map (Tools) or the project context, sent on the
	// first turn of a conversation.
	Context string
	// Activity receives what the lead's tools do while it works; nil = not tracked.
	Activity *LeadActivity
}

// LeadResult is what a lead turn returned.
type LeadResult struct {
	// Graph is the parsed reply: a chat answer (Answer), new nodes, changes.
	Graph *Graph
	Usage runner.TokenUsage
	// Conv is the conversation to keep for the next turn; it has no SessionID
	// when the runner has none.
	Conv LeadConv
	// Context is the prompt size of the turn in tokens.
	Context int
	// ResumeFailed is set when the conversation could not be resumed and the
	// turn started a new one; AgentChanged when the lead's agent is not the one
	// that holds the conversation, so a new one began.
	ResumeFailed, AgentChanged bool
}

// leadMessage is the user message of a turn. A first turn carries the project
// and the summary; every turn carries the graph when it has nodes.
func leadMessage(t LeadTurn, first bool) string {
	var sb strings.Builder
	sb.WriteString(t.Task)
	if first {
		if c := strings.TrimSpace(t.Context); c != "" {
			head := "## Project context\n"
			if t.Tools {
				head = "## Project map\n"
			}
			sb.WriteString("\n\n" + head + c)
		}
		if s := strings.TrimSpace(t.Summary); s != "" {
			sb.WriteString("\n\n## Summary of the earlier conversation\n" + s)
		}
	}
	if f := followUpMessage(t.Existing, t.Next, t.Opts.Live); f != "" {
		sb.WriteString("\n\n" + f)
	}
	return sb.String()
}

// RunLead sends one message to the lead and parses its reply. With tools the
// lead keeps one conversation: the first turn starts it, later turns resume it
// and send only the new message. A conversation that cannot be resumed, or
// that belongs to another agent, starts again with the summary.
func RunLead(ctx context.Context, r runner.LLMRunner, t LeadTurn, onToken func(string)) (LeadResult, error) {
	var res LeadResult
	keep := t.Tools && runner.CanResume(t.Runner)
	resume := ""
	if keep && t.Conv.SessionID != "" {
		if t.Conv.Runner == t.Runner && t.Conv.Model == t.Model {
			resume = t.Conv.SessionID
		} else {
			res.AgentChanged = true
		}
	}
	if t.Activity == nil {
		t.Activity = &LeadActivity{}
	}
	req := runner.CompletionRequest{
		SystemPrompt: buildSplitPrompt(t.Roles, t.Opts),
		Model:        t.Model,
		Dir:          t.Dir,
		Messages:     []runner.ConvMessage{{Role: "user", Content: leadMessage(t, true)}},
	}
	if t.Tools {
		// Codex has no tool allow list: its read-only sandbox is the text mode.
		req.Agent = t.Runner != "codex"
		req.ReadOnly = true
		req.AllowedTools = runner.LeadTools()
		req.KeepSession = keep
	}
	if resume != "" {
		req.ResumeID = resume
		req.Messages = []runner.ConvMessage{{Role: "user", Content: leadMessage(t, false)}}
	}
	call, err := leadComplete(ctx, r, req, onToken, t.Activity)
	var re *runner.ResumeError
	if err != nil && resume != "" && errors.As(err, &re) && ctx.Err() == nil {
		res.ResumeFailed = true
		req.ResumeID = ""
		req.Messages = []runner.ConvMessage{{Role: "user", Content: leadMessage(t, true)}}
		call, err = leadComplete(ctx, r, req, onToken, t.Activity)
	}
	if err != nil {
		return res, err
	}
	res.Usage = call.usage
	res.Conv = leadConvOf(t, keep, call.sessionID)
	res.Context = contextOf(call.usage)

	g, err := ParseSplitOpts(t.Task, call.text, t.Roles, t.Existing, t.Opts)
	if err != nil {
		return res, err
	}
	res.Graph = g
	if g.Answer != "" || len(g.Nodes) < 2 || anySuggestedRole(g) {
		return res, nil
	}

	// Several nodes and no role at all: ask once more, in the same conversation
	// when there is one.
	retry := req
	if call.sessionID != "" && keep {
		retry.ResumeID = call.sessionID
		retry.Messages = []runner.ConvMessage{{Role: "user", Content: splitRetryMsg}}
	} else {
		retry.ResumeID = ""
		retry.Messages = append(append([]runner.ConvMessage(nil), req.Messages...),
			runner.ConvMessage{Role: "assistant", Content: call.text},
			runner.ConvMessage{Role: "user", Content: splitRetryMsg})
	}
	call2, err := leadComplete(ctx, r, retry, onToken, t.Activity)
	res.Usage.InputTokens += call2.usage.InputTokens
	res.Usage.CachedTokens += call2.usage.CachedTokens
	res.Usage.OutputTokens += call2.usage.OutputTokens
	res.Usage.Estimated = res.Usage.Estimated || call2.usage.Estimated
	if err != nil {
		if ctx.Err() != nil {
			return res, err
		}
		return res, nil
	}
	res.Conv = leadConvOf(t, keep, call2.sessionID)
	res.Context = contextOf(call2.usage)
	if g2, err := ParseSplitOpts(t.Task, call2.text, t.Roles, t.Existing, t.Opts); err == nil && anySuggestedRole(g2) {
		res.Graph = g2
	}
	return res, nil
}

func leadConvOf(t LeadTurn, keep bool, id string) LeadConv {
	if !keep || id == "" {
		return LeadConv{}
	}
	return LeadConv{SessionID: id, Runner: t.Runner, Model: t.Model}
}

// contextOf is the prompt size of a call: what the runner reports for its
// last model call, else the whole input.
func contextOf(u runner.TokenUsage) int {
	if u.ContextTokens > 0 {
		return u.ContextTokens
	}
	return u.InputTokens
}

type leadCall struct {
	text      string
	usage     runner.TokenUsage
	sessionID string
}

// leadComplete runs one completion, streaming the text to onToken and the tool
// activity to act.
func leadComplete(ctx context.Context, r runner.LLMRunner, req runner.CompletionRequest, onToken func(string), act *LeadActivity) (leadCall, error) {
	var call leadCall
	ch, err := r.Complete(ctx, req)
	if err != nil {
		return call, fmt.Errorf("split: runner: %w", err)
	}
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return call, fmt.Errorf("split: stream: %w", tok.Error)
		}
		if tok.Usage != nil {
			call.usage = *tok.Usage
		}
		if tok.SessionID != "" {
			call.sessionID = tok.SessionID
		}
		if tok.Activity != nil {
			act.Note(*tok.Activity)
		}
		if tok.Text != "" {
			sb.WriteString(tok.Text)
			if onToken != nil {
				onToken(tok.Text)
			}
		}
		if tok.Done {
			break
		}
	}
	call.text = sb.String()
	return call, nil
}

// LeadActivity follows what the lead's tools do during a turn: the current step
// for the live line, and counts for the summary line. Safe for use from the
// goroutine of the turn and the UI.
type LeadActivity struct {
	// Root is trimmed from the front of the paths shown.
	Root string

	mu                        sync.Mutex
	seen                      map[string]bool
	cur                       string
	reads, searches, commands int
}

// Note records one activity of the runner. Reads, searches and commands count
// once each, however often their state changes.
func (a *LeadActivity) Note(act runner.Activity) {
	var step string
	title := strings.TrimSpace(act.Title)
	switch act.Kind {
	case runner.ActRead:
		step = "reading " + a.rel(title)
	case runner.ActSearch:
		step = fmt.Sprintf("searching %q", title)
	case runner.ActCommand:
		step = title
	case runner.ActThinking:
		if act.State == runner.ActDone {
			return
		}
		step = "thinking"
	default:
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if act.Kind != runner.ActThinking && act.ID != "" {
		if a.seen == nil {
			a.seen = map[string]bool{}
		}
		if !a.seen[act.ID] {
			a.seen[act.ID] = true
			switch act.Kind {
			case runner.ActRead:
				a.reads++
			case runner.ActSearch:
				a.searches++
			case runner.ActCommand:
				a.commands++
			}
		}
	}
	a.cur = step
}

func (a *LeadActivity) rel(p string) string {
	if a.Root == "" {
		return p
	}
	root := strings.TrimSuffix(a.Root, "/") + "/"
	return strings.TrimPrefix(p, root)
}

// Line is the current step as "lead · reading internal/x.go"; "" before the
// lead did anything.
func (a *LeadActivity) Line() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == "" {
		return ""
	}
	return "lead · " + a.cur
}

// Summary is what the lead's tools did: "read 4 files · 2 searches"; "" when
// it used none.
func (a *LeadActivity) Summary() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var parts []string
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	if a.reads > 0 {
		parts = append(parts, "read "+plural(a.reads, "file", "files"))
	}
	if a.searches > 0 {
		parts = append(parts, plural(a.searches, "search", "searches"))
	}
	if a.commands > 0 {
		parts = append(parts, plural(a.commands, "command", "commands"))
	}
	return strings.Join(parts, " · ")
}
