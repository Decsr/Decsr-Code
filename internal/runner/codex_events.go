package runner

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// codexParser turns the JSONL stream of `codex exec --json` into Activities
// and collects the final text and usage.
//
// Schema source (openai/codex on GitHub): codex-rs/exec/src/exec_events.rs.
// Events: thread.started, turn.started, turn.completed {usage}, turn.failed
// {error}, item.started / item.updated / item.completed {item}, error
// {message}. Item types (snake_case "type"): agent_message {text}, reasoning
// {text}, command_execution {command, aggregated_output, exit_code, status},
// file_change {changes [{path, kind add|delete|update}], status}, mcp_tool_call
// {server, tool, arguments, result, error, status}, collab_tool_call {tool,
// status}, web_search {query}, todo_list {items [{text, completed}]}, error
// {message}.
type codexParser struct {
	started map[string]time.Time // item id -> start time

	lastMessage string // plain `codex exec` prints only the last agent message
	sawMessage  bool
	plain       strings.Builder // non-JSON stdout lines, used when no JSON was seen
	sawJSON     bool

	usage     TokenUsage
	gotUsage  bool
	errorMsgs []string
}

func newCodexParser() *codexParser {
	return &codexParser{started: map[string]time.Time{}}
}

type codexItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Message          string          `json:"message"`
	Query            string          `json:"query"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Error            *struct {
		Message string `json:"message"`
	} `json:"error"`
	Changes []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
	Items []struct {
		Text      string `json:"text"`
		Completed bool   `json:"completed"`
	} `json:"items"`
}

// feed parses one stdout line and returns the Activities it produces.
// Malformed lines are kept as plain text and produce nothing.
func (p *codexParser) feed(line []byte, now time.Time) []*Activity {
	var ev struct {
		Type  string    `json:"type"`
		Item  codexItem `json:"item"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &ev); err != nil || ev.Type == "" {
		p.plain.Write(line)
		p.plain.WriteByte('\n')
		return nil
	}
	p.sawJSON = true
	switch ev.Type {
	case "turn.completed":
		if ev.Usage != nil {
			p.usage.InputTokens += ev.Usage.InputTokens
			p.usage.OutputTokens += ev.Usage.OutputTokens
			p.gotUsage = true
		}
	case "turn.failed":
		msg := ""
		if ev.Error != nil {
			msg = ev.Error.Message
		}
		return p.errorActivity(msg, now)
	case "error":
		return p.errorActivity(ev.Message, now)
	case "item.started", "item.updated", "item.completed":
		return p.item(ev.Type, ev.Item, now)
	}
	return nil
}

func (p *codexParser) errorActivity(msg string, now time.Time) []*Activity {
	if msg == "" {
		msg = "codex error"
	}
	p.errorMsgs = append(p.errorMsgs, msg)
	return []*Activity{{
		ID: newActivityID("err"), Kind: ActError, State: ActFailed,
		Title: oneLine(msg), Detail: Clip(msg, maxActivityDetail), At: now,
	}}
}

func (p *codexParser) item(evType string, it codexItem, now time.Time) []*Activity {
	if it.ID == "" {
		it.ID = newActivityID("item")
	}
	if _, ok := p.started[it.ID]; !ok {
		p.started[it.ID] = now
	}
	completed := evType == "item.completed"
	base := Activity{ID: it.ID, State: ActRunning, At: now}
	if completed {
		base.State = ActDone
		base.Duration = now.Sub(p.started[it.ID])
	}
	switch it.Type {
	case "agent_message":
		if completed {
			p.lastMessage, p.sawMessage = it.Text, true
		}
		base.Kind, base.Title, base.Detail = ActMessage, oneLine(it.Text), Clip(it.Text, maxActivityDetail)
		return []*Activity{&base}
	case "reasoning":
		base.Kind = ActThinking
		base.Title = "Thinking (" + strconv.Itoa(len([]rune(it.Text))) + " chars)"
		base.Detail = Clip(it.Text, thinkingExcerpt)
		return []*Activity{&base}
	case "command_execution":
		base.Kind, base.Title = ActCommand, oneLine(it.Command)
		base.Detail = Clip(it.AggregatedOutput, maxActivityDetail)
		base.ExitCode = it.ExitCode
		switch it.Status {
		case "failed", "declined":
			base.State = ActFailed
		case "completed":
			base.State = ActDone
			if it.ExitCode != nil && *it.ExitCode != 0 {
				base.State = ActFailed
			}
		default:
			base.State = ActRunning
		}
		base.Duration = 0
		if base.State != ActRunning {
			base.Duration = now.Sub(p.started[it.ID])
		}
		return []*Activity{&base}
	case "file_change":
		var out []*Activity
		for i, c := range it.Changes {
			a := base
			a.ID = it.ID + "#" + strconv.Itoa(i)
			a.Kind = ActEdit
			if c.Kind == "add" {
				a.Kind = ActWrite
			}
			a.Title = oneLine(c.Path)
			if c.Kind == "delete" {
				a.Detail = "deleted"
			}
			if it.Status == "failed" {
				a.State = ActFailed
			}
			out = append(out, &a)
		}
		return out
	case "mcp_tool_call":
		base.Kind = ActTool
		base.Title = oneLine(it.Server + "." + it.Tool)
		base.Detail = Clip(jsonText(it.Arguments), maxActivityDetail)
		if it.Status == "failed" || it.Error != nil {
			base.State = ActFailed
			if it.Error != nil {
				base.Detail = Clip(it.Error.Message, maxActivityDetail)
			}
		}
		return []*Activity{&base}
	case "collab_tool_call":
		base.Kind, base.Title = ActTool, oneLine("collab: "+it.Tool)
		if it.Status == "failed" {
			base.State = ActFailed
		}
		return []*Activity{&base}
	case "web_search":
		base.Kind, base.Title = ActSearch, oneLine(it.Query)
		return []*Activity{&base}
	case "todo_list":
		done := 0
		var sb strings.Builder
		for _, t := range it.Items {
			mark := "[ ] "
			if t.Completed {
				mark, done = "[x] ", done+1
			}
			sb.WriteString(mark + t.Text + "\n")
		}
		base.Kind = ActTool
		base.Title = "Todo list (" + strconv.Itoa(done) + "/" + strconv.Itoa(len(it.Items)) + " done)"
		base.Detail = Clip(strings.TrimRight(sb.String(), "\n"), maxActivityDetail)
		return []*Activity{&base}
	case "error":
		msg := it.Message
		if msg == "" {
			msg = "codex error"
		}
		base.Kind, base.State, base.Title, base.Detail = ActError, ActFailed, oneLine(msg), Clip(msg, maxActivityDetail)
		return []*Activity{&base}
	}
	return nil
}

// text is the final text: the last agent message, which is what plain
// `codex exec` prints. Output that was not JSON at all is used as is.
func (p *codexParser) text() string {
	if p.sawMessage {
		if strings.TrimSpace(p.lastMessage) == "" {
			return ""
		}
		return strings.TrimRight(p.lastMessage, "\n") + "\n"
	}
	if !p.sawJSON {
		return p.plain.String()
	}
	return ""
}
