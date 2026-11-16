package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// ClaudeRunner executes the Claude CLI (claude) as an LLM backend.
type ClaudeRunner struct {
	Binary string
	Model  string
}

// Complete implements LLMRunner by streaming the Claude CLI output incrementally.
// Uses `--output-format stream-json --verbose` so the TUI shows progress live
// instead of a single burst at the end of generation.
func (r ClaudeRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	prompt := buildClaudePrompt(req.Messages)

	cmd, stdout, stderr, err := r.startStreamingProcess(ctx, prompt, req.SystemPrompt, NormalizeEffort(req.Effort), req.Dir, req.Agent, req.AllowedTools)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 16)
	go streamClaudeOutput(cmd, stdout, stderr, req, prompt, ch)
	// Claude streams tool-use steps, but stretches of silent work still happen;
	// a heartbeat keeps the UI alive so the run never looks frozen.
	return withHeartbeat(ch, defaultHeartbeatInterval), nil
}

// claudeBaseArgs is the fixed part of the argument list. Text mode turns every
// tool off. Agent mode limits the built-in tools to the ones the allow list
// names and uses dontAsk, so anything not on the list is denied instead of
// prompting (and a user's own settings cannot widen it).
func claudeBaseArgs(agent bool, allowed []string) []string {
	args := []string{"--output-format", "stream-json", "--verbose", "--no-session-persistence"}
	if !agent {
		return append(args, "--allowedTools", "")
	}
	return append(args,
		"--tools", strings.Join(claudeToolNames(allowed), ","),
		"--allowedTools", strings.Join(allowed, ","),
		"--permission-mode", "dontAsk")
}

// claudeToolNames is the distinct tool names of an allow list, so "Bash(git
// status:*)" makes the Bash tool available.
func claudeToolNames(allowed []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range allowed {
		name, _, _ := strings.Cut(a, "(")
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// AgentTools is the allow list of a Claude agent run: file tools (read-only
// for a read-only role), Bash limited to the given build and test commands
// (never for a read-only role) and read-only git.
func AgentTools(commands []string, readOnly bool) []string {
	tools := []string{"Read", "Glob", "Grep"}
	if !readOnly {
		tools = []string{"Read", "Edit", "Write", "Glob", "Grep"}
		seen := map[string]bool{}
		for _, c := range commands {
			c = strings.TrimSpace(c)
			if c == "" || seen[c] || strings.ContainsAny(c, "()\n,") {
				continue
			}
			seen[c] = true
			tools = append(tools, "Bash("+c+":*)")
		}
	}
	for _, g := range []string{"status", "diff", "log", "show"} {
		tools = append(tools, "Bash(git "+g+":*)")
	}
	return tools
}

func buildClaudePrompt(messages []ConvMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(":\n")
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

func (r ClaudeRunner) startStreamingProcess(ctx context.Context, prompt, systemPrompt, effort, dir string, agent bool, allowed []string) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	bin := r.Binary
	if bin == "" {
		bin = "claude"
	}

	args := claudeBaseArgs(agent, allowed)
	args = append(args, "--model", EffectiveModel("claude", r.Model))
	args = append(args, "--effort", mapClaudeEffort(EffectiveEffort("claude", effort)))
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	cmd := executil.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("claude: stdout pipe: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("claude: start: %w", err)
	}

	// Best-effort: stop the process if the caller's context is cancelled.
	if ctx != nil {
		go func() {
			<-ctx.Done()
			_ = cmd.Process.Kill()
		}()
	}

	return cmd, stdout, stderr, nil
}

// streamClaudeOutput parses NDJSON events from the Claude CLI and emits
// Tokens incrementally. Each `assistant` event carries the cumulative
// message text; we emit only the new suffix so the channel sees deltas.
func streamClaudeOutput(cmd *exec.Cmd, stdout io.ReadCloser, stderr *bytes.Buffer, req CompletionRequest, prompt string, ch chan<- Token) {
	defer close(ch)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var emitted strings.Builder
	var finalUsage TokenUsage
	gotUsage := false
	acts := newClaudeActivities()

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		evt, err := parseClaudeStreamEvent(line)
		if err != nil {
			continue
		}
		switch evt.Type {
		case "assistant":
			text := assistantText(evt.Message.Content)
			delta := computeDelta(emitted.String(), text)
			if delta != "" {
				ch <- Token{Text: delta}
				emitted.WriteString(delta)
			}
			// Surface tool invocations (read/edit/bash/…) as display-only
			// notices so the user sees the agent is actively working during
			// the (otherwise silent) tool phase.
			for _, note := range toolUseNotices(evt.Message.Content) {
				ch <- Token{Reasoning: note}
			}
			for _, a := range acts.assistant(evt.Message, text, time.Now()) {
				ch <- Token{Activity: a}
			}
		case "user":
			for _, a := range acts.toolResults(evt.Message.Content, time.Now()) {
				ch <- Token{Activity: a}
			}
		case "result":
			// The result text is only the last message, so it duplicates
			// assistant text already emitted; use it only as a fallback.
			if evt.Result != "" && emitted.Len() == 0 {
				delta := computeDelta(emitted.String(), evt.Result)
				if delta != "" {
					ch <- Token{Text: delta}
					emitted.WriteString(delta)
				}
			}
			if evt.Usage.InputTokens > 0 || evt.Usage.OutputTokens > 0 {
				finalUsage = TokenUsage{
					InputTokens:  evt.Usage.InputTokens,
					OutputTokens: evt.Usage.OutputTokens,
				}
				gotUsage = true
			}
		}
	}

	waitErr := cmd.Wait()
	if waitErr != nil && emitted.Len() == 0 {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		if rl := ClassifyRateLimit("claude", errMsg); rl != nil {
			ch <- Token{Error: rl}
		} else {
			ch <- Token{Error: fmt.Errorf("claude: %s", errMsg)}
		}
	}

	if !gotUsage {
		finalUsage = TokenUsage{
			InputTokens:  tokenutil.EstimateTokens(req.SystemPrompt + prompt),
			OutputTokens: tokenutil.EstimateTokens(emitted.String()),
			Estimated:    true,
		}
	}
	ch <- Token{Done: true, Usage: &finalUsage}
}

// computeDelta returns the suffix of next that is not already in already.
// Handles both cumulative (full snapshot each time) and pure-delta event
// streams. If next is not a prefix-extension of already, the entire next
// is treated as a fresh chunk.
func computeDelta(already, next string) string {
	if next == "" {
		return ""
	}
	if strings.HasPrefix(next, already) {
		return next[len(already):]
	}
	return next
}

// claudeStreamEvent is one NDJSON line from `--output-format stream-json`.
type claudeStreamEvent struct {
	Type    string                  `json:"type"`
	Message claudeStreamMessage     `json:"message"`
	Result  string                  `json:"result"`
	Usage   claudeStreamUsageFields `json:"usage"`
}

type claudeStreamMessage struct {
	ID      string                `json:"id"`
	Content []claudeStreamContent `json:"content"`
}

// claudeStreamContent is one content block. The block types and fields follow
// the Anthropic Messages API as surfaced by Claude Code's stream-json output;
// the types are documented in anthropics/claude-agent-sdk-python,
// src/claude_agent_sdk/types.py (TextBlock, ThinkingBlock, ToolUseBlock,
// ToolResultBlock) and _internal/message_parser.py (assistant and user
// messages, is_error on tool_result).
type claudeStreamContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`    // Type == "thinking"
	ID        string          `json:"id"`          // tool call ID when Type == "tool_use"
	Name      string          `json:"name"`        // tool name when Type == "tool_use"
	Input     json.RawMessage `json:"input"`       // tool input when Type == "tool_use"
	ToolUseID string          `json:"tool_use_id"` // Type == "tool_result"
	IsError   bool            `json:"is_error"`    // Type == "tool_result"
	Content   json.RawMessage `json:"content"`     // Type == "tool_result": string or [{type:text,text}]
}

type claudeStreamUsageFields struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func parseClaudeStreamEvent(line []byte) (claudeStreamEvent, error) {
	var evt claudeStreamEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		return claudeStreamEvent{}, err
	}
	return evt, nil
}

func assistantText(content []claudeStreamContent) string {
	var sb strings.Builder
	for _, c := range content {
		if c.Type == "text" || c.Type == "" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}

// toolUseNotices returns a short, display-only line for each tool_use block in
// an assistant event (e.g. "⚙ Read path/to/file.go"), so the UI reflects the
// agent's silent tool activity during running/fixing.
func toolUseNotices(content []claudeStreamContent) []string {
	var out []string
	for _, c := range content {
		if c.Type != "tool_use" || c.Name == "" {
			continue
		}
		notice := "⚙ " + c.Name
		if detail := toolInputSummary(c.Input); detail != "" {
			notice += " " + detail
		}
		out = append(out, notice+"\n")
	}
	return out
}

// toolInputSummary extracts a compact, human-meaningful detail from a tool's
// input JSON (file path or command), or "" when none is obvious.
func toolInputSummary(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var fields struct {
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		Command     string `json:"command"`
		Pattern     string `json:"pattern"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &fields); err != nil {
		return ""
	}
	switch {
	case fields.FilePath != "":
		return fields.FilePath
	case fields.Path != "":
		return fields.Path
	case fields.Command != "":
		return truncateInline(fields.Command, 60)
	case fields.Pattern != "":
		return truncateInline(fields.Pattern, 60)
	case fields.Description != "":
		return truncateInline(fields.Description, 60)
	}
	return ""
}

// truncateInline shortens s to max runes, appending "…" when cut.
func truncateInline(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// claudeJSONResult is the shape of claude --output-format json stdout.
// Kept for the non-streaming fallback parser used by tests.
type claudeJSONResult struct {
	Result string `json:"result"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	// Fallback top-level fields present in some CLI versions.
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
}

// parseClaudeJSONResponse extracts result text and token usage from Claude CLI JSON output.
// Falls back gracefully to treating the raw bytes as plain text with estimated usage.
func parseClaudeJSONResponse(raw []byte) (text string, usage TokenUsage, err error) {
	var result claudeJSONResult
	if jsonErr := json.Unmarshal(bytes.TrimSpace(raw), &result); jsonErr != nil {
		// Not valid JSON — treat as plain text, estimate tokens.
		text = strings.TrimSpace(string(raw))
		usage = TokenUsage{
			OutputTokens: tokenutil.EstimateTokens(text),
			Estimated:    true,
		}
		return text, usage, nil
	}

	text = result.Result

	// Prefer usage sub-object; fall back to top-level total fields.
	inputTokens := result.Usage.InputTokens
	outputTokens := result.Usage.OutputTokens
	if inputTokens == 0 {
		inputTokens = result.TotalInputTokens
	}
	if outputTokens == 0 {
		outputTokens = result.TotalOutputTokens
	}

	if inputTokens == 0 && outputTokens == 0 {
		// JSON parsed but no usage fields — estimate from text.
		usage = TokenUsage{
			OutputTokens: tokenutil.EstimateTokens(text),
			Estimated:    true,
		}
		return text, usage, nil
	}

	usage = TokenUsage{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		Estimated:    false,
	}
	return text, usage, nil
}

// claudeActivities turns Claude content blocks into Activities and remembers
// the tool calls that still wait for their result.
type claudeActivities struct {
	pending map[string]Activity // tool_use id -> the running Activity
	msgIDs  map[string]string   // message id -> ID of its message Activity
}

func newClaudeActivities() *claudeActivities {
	return &claudeActivities{pending: map[string]Activity{}, msgIDs: map[string]string{}}
}

// assistant returns the Activities of an assistant event: one per thinking
// block, one for the prose (updated in place while the message grows) and one
// running item per tool_use block.
func (c *claudeActivities) assistant(msg claudeStreamMessage, text string, now time.Time) []*Activity {
	var out []*Activity
	for _, b := range msg.Content {
		switch b.Type {
		case "thinking":
			if b.Thinking == "" {
				continue
			}
			n := len([]rune(b.Thinking))
			out = append(out, &Activity{
				ID: newActivityID("think"), Kind: ActThinking, State: ActDone,
				Title: fmt.Sprintf("Thinking (%d chars)", n), Detail: Clip(b.Thinking, thinkingExcerpt), At: now,
			})
		case "tool_use":
			if b.Name == "" {
				continue
			}
			ti := classifyTool(b.Name, b.Input)
			a := Activity{ID: b.ID, Kind: ti.Kind, State: ActRunning, Title: ti.Title, Detail: ti.Detail,
				Added: ti.Added, Removed: ti.Removed, At: now}
			if a.ID == "" {
				a.ID = newActivityID("tool")
			}
			if a.Title == "" {
				a.Title = oneLine(b.Name)
			}
			c.pending[a.ID] = a
			cp := a
			out = append(out, &cp)
		}
	}
	if strings.TrimSpace(text) != "" {
		id := c.msgIDs[msg.ID]
		if id == "" {
			id = newActivityID("msg")
			if msg.ID != "" {
				c.msgIDs[msg.ID] = id
			}
		}
		out = append([]*Activity{{
			ID: id, Kind: ActMessage, State: ActDone, Title: oneLine(text), Detail: Clip(text, maxActivityDetail), At: now,
		}}, out...)
	}
	return out
}

// toolResults completes the pending tool calls named by tool_result blocks of
// a user event. Results of unknown calls are ignored.
func (c *claudeActivities) toolResults(content []claudeStreamContent, now time.Time) []*Activity {
	var out []*Activity
	for _, b := range content {
		if b.Type != "tool_result" {
			continue
		}
		a, ok := c.pending[b.ToolUseID]
		if !ok {
			continue
		}
		delete(c.pending, b.ToolUseID)
		a.State = ActDone
		if b.IsError {
			a.State = ActFailed
		}
		a.Detail = Clip(claudeResultText(b.Content), maxActivityDetail)
		a.Duration = now.Sub(a.At)
		a.At = now
		out = append(out, &a)
	}
	return out
}

// claudeResultText flattens a tool_result content value, which is either a
// string or a list of {type:"text", text} blocks.
func claudeResultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
