package runner

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// dshParser turns the NDJSON stream of `dsh --profile headless --json` into
// Tokens and keeps the state needed for the final text, usage and outcome.
//
// Schema source (deepseek-harness, packages/bundle/headless/src/json-stream.ts):
// one JSON object per line with type
//   - session  {sessionId, cwd}
//   - status   {phase: turn_start | step_start | step_end | turn_end, turn,
//     step, usage {inputTokens, outputTokens, totalTokens?, cacheReadTokens?,
//     cacheWriteTokens?, reasoningTokens?} on step_end, reason on turn_end}
//   - text, thinking  {text}, written when the step commits
//   - tool_call   {callId, tool, input}
//   - tool_result {callId, status: completed | error, result, error?
//     {name, code, reason?}}
//   - final {sessionId, reason {kind, ...} | null, text}, the last event
//   - error {message}, a runner failure outside a turn (no final follows)
//
// Strings are capped at 8 KiB and flagged truncated, except final.text.
type dshParser struct {
	text    strings.Builder
	sawText bool

	usage    TokenUsage
	hasUsage bool

	tools map[string]dshTool // calls waiting for their result

	final      bool
	finalText  string
	kind       string // final.reason.kind; "" when the reason is null
	reasonMsg  string // final.reason.error message
	reasonCode string
	errMsg     string // message of an error event
}

// dshTool is a started tool call.
type dshTool struct {
	info  toolInfo
	start time.Time
}

func newDshParser() *dshParser { return &dshParser{tools: map[string]dshTool{}} }

// feed parses one NDJSON line and returns the Tokens it produces. Malformed and
// unknown lines return nothing.
func (p *dshParser) feed(line []byte) []Token {
	var ev struct {
		Type   string          `json:"type"`
		Phase  string          `json:"phase"`
		Text   string          `json:"text"`
		CallID string          `json:"callId"`
		Tool   string          `json:"tool"`
		Input  json.RawMessage `json:"input"`
		Status string          `json:"status"`
		Result string          `json:"result"`
		Error  *struct {
			Name   string `json:"name"`
			Code   string `json:"code"`
			Reason string `json:"reason"`
		} `json:"error"`
		Message string `json:"message"`
		Usage   *struct {
			InputTokens      int `json:"inputTokens"`
			OutputTokens     int `json:"outputTokens"`
			CacheReadTokens  int `json:"cacheReadTokens"`
			CacheWriteTokens int `json:"cacheWriteTokens"`
		} `json:"usage"`
		Reason *struct {
			Kind  string `json:"kind"`
			Error *struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		} `json:"reason"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil
	}
	now := time.Now()

	switch ev.Type {
	case "status":
		if ev.Phase == "step_end" && ev.Usage != nil {
			// InputTokens is the full prompt: uncached plus cache read and write.
			p.usage.InputTokens += ev.Usage.InputTokens + ev.Usage.CacheReadTokens + ev.Usage.CacheWriteTokens
			p.usage.OutputTokens += ev.Usage.OutputTokens
			p.hasUsage = true
		}
	case "text":
		if ev.Text == "" {
			return nil
		}
		text := ev.Text
		// Each text event is one committed message: keep two of them apart.
		if p.sawText && !strings.HasSuffix(p.text.String(), "\n") {
			text = "\n\n" + text
		}
		p.text.WriteString(text)
		p.sawText = true
		return []Token{{Text: text}}
	case "thinking":
		if ev.Text == "" {
			return nil
		}
		return activityTokens(&Activity{
			ID: newActivityID("think"), Kind: ActThinking, State: ActDone,
			Title:  "Thinking (" + strconv.Itoa(len([]rune(ev.Text))) + " chars)",
			Detail: Clip(ev.Text, thinkingExcerpt), At: now,
		})
	case "tool_call":
		if ev.Tool == "" {
			return nil
		}
		ti := classifyTool(ev.Tool, ev.Input)
		if ti.Title == "" {
			ti.Title = oneLine(ev.Tool)
		}
		id := ev.CallID
		if id == "" {
			id = newActivityID("tool")
		}
		p.tools[id] = dshTool{info: ti, start: now}
		return activityTokens(&Activity{
			ID: id, Kind: ti.Kind, State: ActRunning, Title: ti.Title, Detail: ti.Detail,
			Added: ti.Added, Removed: ti.Removed, At: now,
		})
	case "tool_result":
		t, ok := p.tools[ev.CallID]
		if !ok {
			return nil
		}
		delete(p.tools, ev.CallID)
		a := &Activity{
			ID: ev.CallID, Kind: t.info.Kind, State: ActDone, Title: t.info.Title, Detail: t.info.Detail,
			Added: t.info.Added, Removed: t.info.Removed, At: now, Duration: now.Sub(t.start),
		}
		if ev.Status == "error" || ev.Error != nil {
			a.State = ActFailed
			detail := ev.Result
			if ev.Error != nil {
				detail = joinNonEmptyLines(ev.Error.Code, ev.Error.Reason, detail)
			}
			a.Detail = Clip(detail, maxActivityDetail)
		} else if t.info.Kind != ActRead && t.info.Kind != ActEdit && t.info.Kind != ActWrite {
			a.Detail = Clip(ev.Result, maxActivityDetail)
		}
		return activityTokens(a)
	case "final":
		p.final = true
		p.finalText = ev.Text
		if ev.Reason != nil {
			p.kind = ev.Reason.Kind
			if ev.Reason.Error != nil {
				p.reasonMsg, p.reasonCode = ev.Reason.Error.Message, ev.Reason.Error.Code
			}
		}
	case "error":
		p.errMsg = ev.Message
		msg := ev.Message
		if msg == "" {
			msg = "dsh error"
		}
		return activityTokens(&Activity{
			ID: newActivityID("err"), Kind: ActError, State: ActFailed,
			Title: oneLine(msg), Detail: Clip(string(line), maxActivityDetail), At: now,
		})
	}
	return nil
}

// failure maps the end of the process to an error, or nil for a run that
// succeeded: exit 0 and a final event whose reason is "completed". A rate
// limit is detected on the failure text only, never on a successful answer.
func (p *dshParser) failure(waitErr error, stderr string) error {
	if waitErr == nil && p.final && p.kind == "completed" {
		return nil
	}
	reason := p.kind
	if reason == "" {
		reason = "no completed turn"
	}
	if p.reasonMsg != "" {
		reason += ": " + p.reasonMsg
	}
	if p.errMsg != "" {
		reason = p.errMsg
	}
	if rl := ClassifyRateLimit("dsh", stderr, p.errMsg, p.reasonMsg, p.reasonCode); rl != nil {
		return rl
	}
	msg := "dsh: " + reason
	var ee interface{ ExitCode() int }
	if errors.As(waitErr, &ee) {
		msg += " (exit " + strconv.Itoa(ee.ExitCode()) + ")"
	} else if waitErr != nil {
		msg += ": " + waitErr.Error()
	}
	if stderr != "" {
		msg += ": " + Clip(stderr, 500)
	}
	return errors.New(msg)
}

// finish returns the final text (final.text when no text event was seen) and
// the usage: the summed step usage, else an estimate.
func (p *dshParser) finish(inputText string) (string, TokenUsage) {
	text := p.text.String()
	if text == "" {
		text = p.finalText
	}
	if p.hasUsage {
		return text, p.usage
	}
	return text, TokenUsage{
		InputTokens:  tokenutil.EstimateTokens(inputText),
		OutputTokens: tokenutil.EstimateTokens(text),
		Estimated:    true,
	}
}

// joinNonEmptyLines joins the non-empty parts with a newline.
func joinNonEmptyLines(parts ...string) string {
	var out []string
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}
