package runner

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// openCodeParser turns the NDJSON stream of `opencode run --format json` into
// Tokens and keeps the state needed for the final text and usage.
//
// Schema sources (sst/opencode on GitHub):
//   - packages/cli/src/run/noninteractive.ts (renderer: the emit() calls print
//     one JSON object per line: {type, timestamp, sessionID, ...} with types
//     text, reasoning, tool_use, step_start, step_finish, retry,
//     background_wait and error; text/reasoning carry part.text, tool_use
//     carries part.tool and part.state {status, input, output, title, error,
//     metadata, time}, step_finish carries part.tokens {input, output,
//     reasoning, cache {read, write}}, error carries error {type|name, message|data.message}).
//   - packages/core/src/tool/plugin/{edit,write,read,shell,grep,glob,patch,
//     webfetch,websearch,subagent}.ts (tool names and input keys).
//
// A tool_use line is written once a tool has finished (status completed or
// error); the pending and running states of the part model are handled too.
type openCodeParser struct {
	text    strings.Builder
	sawText bool
	// toolSinceText is set by a tool call after text: the next text is a new turn.
	toolSinceText bool

	usage      TokenUsage
	foundUsage bool
	stepIn     int
	stepCached int
	stepOut    int

	sessionID string // sessionID of the events: the id --session takes
}

func newOpenCodeParser() *openCodeParser { return &openCodeParser{} }

// feed parses one NDJSON line and returns the Tokens it produces. Malformed
// and unknown lines return nothing.
func (p *openCodeParser) feed(line []byte) []Token {
	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Part      struct {
			Text string `json:"text"`
		} `json:"part"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return nil
	}
	if p.sessionID == "" && event.SessionID != "" && event.Type != "error" {
		p.sessionID = event.SessionID
		return append([]Token{{SessionID: p.sessionID}}, p.feed(line)...)
	}
	now := time.Now()

	switch event.Type {
	case "text":
		if event.Part.Text == "" {
			return nil
		}
		text := event.Part.Text
		newTurn := p.sawText && p.toolSinceText
		// Text after a tool call is a new paragraph.
		if newTurn && !strings.HasSuffix(p.text.String(), "\n") {
			text = "\n\n" + text
		}
		p.text.WriteString(text)
		p.sawText = true
		p.toolSinceText = false
		return []Token{{Text: text, NewTurn: newTurn}}
	case "usage":
		if event.Usage != nil {
			p.usage = TokenUsage{
				InputTokens:  event.Usage.InputTokens,
				OutputTokens: event.Usage.OutputTokens,
			}
			p.foundUsage = true
		}
	case "reasoning":
		if event.Part.Text == "" {
			return nil
		}
		var part struct {
			Part struct {
				ID string `json:"id"`
			} `json:"part"`
		}
		_ = json.Unmarshal(line, &part)
		id := part.Part.ID
		if id == "" {
			id = newActivityID("think")
		}
		return activityTokens(&Activity{
			ID: id, Kind: ActThinking, State: ActDone,
			Title:  "Thinking (" + strconv.Itoa(len([]rune(event.Part.Text))) + " chars)",
			Detail: Clip(event.Part.Text, thinkingExcerpt), At: now,
		})
	case "step_finish":
		var sf struct {
			Part struct {
				Tokens struct {
					Input  int `json:"input"`
					Output int `json:"output"`
					Cache  struct {
						Read  int `json:"read"`
						Write int `json:"write"`
					} `json:"cache"`
				} `json:"tokens"`
			} `json:"part"`
		}
		if json.Unmarshal(line, &sf) == nil {
			cached := sf.Part.Tokens.Cache.Read + sf.Part.Tokens.Cache.Write
			p.stepIn += sf.Part.Tokens.Input + cached
			p.stepCached += cached
			p.stepOut += sf.Part.Tokens.Output
			return []Token{{Progress: &TokenUsage{InputTokens: p.stepIn, CachedTokens: p.stepCached, OutputTokens: p.stepOut}}}
		}
	case "tool_use":
		p.toolSinceText = true
		return activityTokens(openCodeToolActivity(line, now))
	case "error":
		return activityTokens(&Activity{
			ID: newActivityID("err"), Kind: ActError, State: ActFailed,
			Title: oneLine(openCodeErrorMessage(line)), Detail: Clip(string(line), maxActivityDetail), At: now,
		})
	case "retry":
		var r struct {
			Attempt int `json:"attempt"`
			Error   struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(line, &r)
		title := "Retrying"
		if r.Attempt > 0 {
			title += " (attempt " + strconv.Itoa(r.Attempt) + ")"
		}
		return activityTokens(&Activity{
			ID: newActivityID("wait"), Kind: ActWaiting, State: ActRunning,
			Title: oneLine(title), Detail: Clip(r.Error.Message, maxActivityDetail), At: now,
		})
	case "background_wait":
		return activityTokens(&Activity{
			ID: newActivityID("wait"), Kind: ActWaiting, State: ActRunning,
			Title: "Waiting for background jobs", At: now,
		})
	}
	return nil
}

// finish returns the final text (the fallback is the raw output when no text
// event was seen) and the usage: an exact "usage" event, else the summed
// step_finish tokens, else an estimate.
func (p *openCodeParser) finish(raw []byte, inputText string) (string, TokenUsage) {
	text := p.text.String()
	if text == "" {
		text = strings.TrimSpace(string(raw))
	}
	usage := p.usage
	switch {
	case p.foundUsage:
	case p.stepIn > 0 || p.stepOut > 0:
		usage = TokenUsage{InputTokens: p.stepIn, CachedTokens: p.stepCached, OutputTokens: p.stepOut}
	default:
		usage = TokenUsage{
			InputTokens:  tokenutil.EstimateTokens(inputText),
			OutputTokens: tokenutil.EstimateTokens(text),
			Estimated:    true,
		}
	}
	return text, usage
}

func activityTokens(a *Activity) []Token {
	if a == nil {
		return nil
	}
	return []Token{{Activity: a}}
}

// openCodeToolActivity maps a tool_use line to an Activity.
func openCodeToolActivity(line []byte, now time.Time) *Activity {
	var ev struct {
		Part struct {
			ID     string `json:"id"`
			CallID string `json:"callID"`
			Tool   string `json:"tool"`
			State  struct {
				Status   string                     `json:"status"`
				Input    json.RawMessage            `json:"input"`
				Output   json.RawMessage            `json:"output"`
				Title    string                     `json:"title"`
				Error    json.RawMessage            `json:"error"`
				Metadata map[string]json.RawMessage `json:"metadata"`
				Time     struct {
					Start int64 `json:"start"`
					End   int64 `json:"end"`
				} `json:"time"`
			} `json:"state"`
		} `json:"part"`
	}
	if json.Unmarshal(line, &ev) != nil || ev.Part.Tool == "" {
		return nil
	}
	p := ev.Part
	ti := classifyTool(p.Tool, p.State.Input)
	a := &Activity{ID: p.ID, Kind: ti.Kind, Title: ti.Title, Detail: ti.Detail, Added: ti.Added, Removed: ti.Removed, At: now}
	if a.ID == "" {
		a.ID = p.CallID
	}
	if a.ID == "" {
		a.ID = newActivityID("tool")
	}
	if a.Title == "" {
		a.Title = oneLine(p.State.Title)
	}
	if a.Title == "" {
		a.Title = oneLine(p.Tool)
	}

	// v1 reports the real change size of an edit in metadata.filediff.
	if fd, ok := p.State.Metadata["filediff"]; ok && ti.Kind == ActEdit {
		var d struct {
			Additions *int `json:"additions"`
			Deletions *int `json:"deletions"`
		}
		if json.Unmarshal(fd, &d) == nil && d.Additions != nil && d.Deletions != nil {
			a.Added, a.Removed = *d.Additions, *d.Deletions
		}
	}

	output := jsonText(p.State.Output)
	switch p.State.Status {
	case "completed":
		a.State = ActDone
		if ti.Kind != ActRead && ti.Kind != ActEdit && ti.Kind != ActWrite {
			a.Detail = Clip(output, maxActivityDetail)
		}
		if ti.Kind == ActCommand {
			for _, k := range []string{"exit", "exitCode"} {
				var n int
				if raw, ok := p.State.Metadata[k]; ok && json.Unmarshal(raw, &n) == nil {
					a.ExitCode = &n
					if n != 0 {
						a.State = ActFailed
					}
					break
				}
			}
		}
	case "error":
		a.State = ActFailed
		a.Detail = Clip(jsonText(p.State.Error), maxActivityDetail)
	default: // pending, running or unknown
		a.State = ActRunning
	}
	if a.State != ActRunning && p.State.Time.End >= p.State.Time.Start && p.State.Time.Start > 0 {
		a.Duration = time.Duration(p.State.Time.End-p.State.Time.Start) * time.Millisecond
	}
	return a
}

// jsonText returns a JSON string value as text, any other JSON value as its
// raw text, and "" for null or absent.
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// openCodeErrorMessage extracts the human message of an error event.
func openCodeErrorMessage(line []byte) string {
	var ev struct {
		Error struct {
			Name    string `json:"name"`
			Message string `json:"message"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	_ = json.Unmarshal(line, &ev)
	switch {
	case ev.Error.Data.Message != "":
		return ev.Error.Data.Message
	case ev.Error.Message != "":
		return ev.Error.Message
	case ev.Error.Name != "":
		return ev.Error.Name
	}
	return "opencode error"
}
