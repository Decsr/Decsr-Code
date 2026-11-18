package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

	cmd, stdout, stderr, err := r.startStreamingProcess(ctx, prompt, req.SystemPrompt, NormalizeEffort(req.Effort), req.Dir, req.Agent, req.MCP, req.AllowedTools, req.KeepSession || req.ResumeID != "", req.ResumeID)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 16)
	go streamClaudeOutput(cmd, stdout, stderr, req, prompt, ch)
	var out <-chan Token = ch
	if req.ResumeID != "" {
		out = resumeGuard(out)
	}
	// Claude streams tool-use steps, but stretches of silent work still happen;
	// a heartbeat keeps the UI alive so the run never looks frozen.
	return withHeartbeat(out, defaultHeartbeatInterval), nil
}

// claudeBaseArgs is the fixed part of the argument list. Text mode turns every
// tool off. Agent mode limits the built-in tools to the ones the allow list
// names and uses dontAsk, so anything not on the list is denied instead of
// prompting (and a user's own settings cannot widen it).
//
// Every run is lean: no MCP servers (unless the role sets mcp, so the user's
// own servers load) and only the project and local settings, so a worker does
// not pay for the user's global setup. --bare is not used: it breaks
// subscription logins.
//
// A one-shot call does not keep its session (--no-session-persistence); a call
// with keep set does, so a later one can resume it with --resume.
func claudeBaseArgs(agent, mcp bool, allowed []string, keep bool) []string {
	args := []string{"--output-format", "stream-json", "--verbose"}
	if !keep {
		args = append(args, "--no-session-persistence")
	}
	if !mcp {
		args = append(args, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`)
	}
	// User-scope MCP servers and claude.ai connectors are only read with the
	// user source, so a run that loads MCP needs it; every other run does not.
	sources := "project,local"
	if mcp {
		sources = "user,project,local"
	}
	args = append(args, "--setting-sources", sources)
	if !agent {
		return append(args, "--tools", "")
	}
	return append(args,
		"--tools", strings.Join(claudeToolNames(allowed), ","),
		"--allowedTools", strings.Join(allowed, ","),
		"--permission-mode", "dontAsk")
}

// MCPServerRule is the allow rule for every tool of one MCP server. Claude
// names a server's tools mcp__<server>__<tool>, with each character of the
// server name outside [A-Za-z0-9_-] replaced by "_" ("claude.ai Google Drive"
// is claude_ai_Google_Drive). The bare form mcp__<server> allows all of them.
func MCPServerRule(server string) string {
	var sb strings.Builder
	sb.WriteString("mcp__")
	for _, r := range server {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}

// MCPServerRules is MCPServerRule for each name, once each.
func MCPServerRules(servers []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range servers {
		if r := MCPServerRule(s); !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

// claudeToolNames is the distinct built-in tool names of an allow list, so
// "Bash(git status:*)" makes the Bash tool available. MCP rules are left out:
// --tools lists built-in tools only.
func claudeToolNames(allowed []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range allowed {
		name, _, _ := strings.Cut(a, "(")
		if strings.HasPrefix(name, "mcp__") {
			continue
		}
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// LeadTools is the allow list of the lead's turns: files can be read and
// searched and git can show history and changes. No file tool that writes, no
// build or test command, and Claude runs the lead without MCP servers. The
// claude plugin and MCP lookups only list: installing is done by decsr after
// the user's key press.
func LeadTools() []string {
	tools := []string{"Read", "Grep", "Glob"}
	for _, g := range []string{"log", "diff", "status", "show"} {
		tools = append(tools, "Bash(git "+g+":*)")
	}
	for _, c := range []string{"plugin list", "plugin marketplace list", "mcp list"} {
		tools = append(tools, "Bash(claude "+c+":*)")
	}
	return tools
}

// readOnlyBash are the shell commands that cannot write, allowed for every
// writing agent run so chained inspection commands are not refused.
var readOnlyBash = []string{"ls", "cat", "head", "tail", "wc", "pwd", "which", "file", "stat", "du", "tree"}

// AgentTools is the allow list of a Claude agent run: file tools (read-only
// for a read-only role), Bash limited to the programs of the given build and
// test commands, a few read-only shell commands and the extra command
// prefixes (never for a read-only role) and read-only git.
func AgentTools(commands, extra []string, readOnly bool) []string {
	tools := []string{"Read", "Glob", "Grep"}
	if !readOnly {
		tools = []string{"Read", "Edit", "Write", "Glob", "Grep"}
		seen := map[string]bool{}
		add := func(prefix string) {
			if prefix == "" || seen[prefix] || strings.ContainsAny(prefix, "()\n,") {
				return
			}
			seen[prefix] = true
			tools = append(tools, "Bash("+prefix+":*)")
		}
		for _, c := range commands {
			for _, p := range commandPrograms(c) {
				add(p)
			}
		}
		for _, p := range readOnlyBash {
			add(p)
		}
		for _, e := range extra {
			add(strings.TrimSpace(e))
		}
	}
	for _, g := range []string{"status", "diff", "log", "show"} {
		tools = append(tools, "Bash(git "+g+":*)")
	}
	return tools
}

// commandPrograms is the program of each part of a command line split on
// &&, ||, ; and |, skipping leading VAR=val assignments and "cd <dir>" parts.
// Parts with unsafe characters are dropped.
func commandPrograms(cmd string) []string {
	parts := strings.FieldsFunc(strings.NewReplacer("&&", ";", "||", ";", "|", ";").Replace(cmd), func(r rune) bool { return r == ';' })
	var out []string
	for _, part := range parts {
		words := strings.Fields(part)
		for len(words) > 0 && isAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) == 0 || words[0] == "cd" || strings.ContainsAny(words[0], "()\n,") {
			continue
		}
		out = append(out, words[0])
	}
	return out
}

// CommandPrograms is the program of each part of a command line (see
// commandPrograms), for the allow list prefixes a refused command needs.
func CommandPrograms(cmd string) []string { return commandPrograms(cmd) }

func isAssignment(w string) bool {
	i := strings.Index(w, "=")
	if i <= 0 {
		return false
	}
	for _, r := range w[:i] {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
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

func (r ClaudeRunner) startStreamingProcess(ctx context.Context, prompt, systemPrompt, effort, dir string, agent, mcp bool, allowed []string, keep bool, resumeID string) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	bin := r.Binary
	if bin == "" {
		bin = "claude"
	}

	args := claudeBaseArgs(agent, mcp, allowed, keep)
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	args = append(args, "--model", EffectiveModel("claude", r.Model))
	args = append(args, "--effort", mapClaudeEffort(EffectiveEffort("claude", effort)))
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	cmd := executil.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
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
	var liveUsage claudeLiveUsage
	// limited is set when the result event reports an error that reads as a
	// usage limit ("... limit reached ∙ resets 2pm"). The same text may also
	// arrive as assistant text, so it is reported even after text was emitted.
	var limited *RateLimitError
	var denials []Denial

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
		case "system":
			if evt.Subtype == "init" && (evt.Model != "" || evt.SessionID != "") {
				ch <- Token{Model: evt.Model, SessionID: evt.SessionID}
			}
		case "rate_limit_event":
			if evt.RateLimitInfo != nil {
				recordClaudeRateLimit(*evt.RateLimitInfo, time.Now())
			}
		case "assistant":
			if p := liveUsage.add(evt.Message); p != nil {
				ch <- Token{Progress: p}
			}
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
			if evt.IsError {
				if rl := ClassifyRateLimit("claude", evt.Result); rl != nil {
					limited = rl
				}
			}
			// The result text is only the last message, so it duplicates
			// assistant text already emitted; use it only as a fallback.
			if evt.Result != "" && emitted.Len() == 0 {
				delta := computeDelta(emitted.String(), evt.Result)
				if delta != "" {
					ch <- Token{Text: delta}
					emitted.WriteString(delta)
				}
			}
			denials = claudeDenials(evt.PermissionDenials)
			if in := evt.Usage.promptTokens(); in > 0 || evt.Usage.OutputTokens > 0 {
				finalUsage = TokenUsage{
					InputTokens:   in,
					CachedTokens:  evt.Usage.cachedTokens(),
					OutputTokens:  evt.Usage.OutputTokens,
					ContextTokens: liveUsage.lastPrompt,
				}
				gotUsage = true
			}
		}
	}

	waitErr := cmd.Wait()
	if limited != nil {
		ch <- Token{Error: limited}
		return
	}
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
	ch <- Token{Done: true, Usage: &finalUsage, Denials: denials}
}

// claudeDenials turns the refused tool calls of a result event into Denials,
// each tool and command once.
func claudeDenials(in []claudePermissionDenial) []Denial {
	var out []Denial
	seen := map[Denial]bool{}
	for _, d := range in {
		if d.ToolName == "" {
			continue
		}
		dn := Denial{Tool: d.ToolName}
		if d.ToolName == "Bash" {
			dn.Command = strings.TrimSpace(d.ToolInput.Command)
		}
		if !seen[dn] {
			seen[dn] = true
			out = append(out, dn)
		}
	}
	return out
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
	Type    string `json:"type"`
	Subtype string `json:"subtype"` // "init" on a system event
	Model   string `json:"model"`   // system init: the model in use
	// SessionID is set on the system init event: the id --resume takes.
	SessionID string                  `json:"session_id"`
	Message   claudeStreamMessage     `json:"message"`
	Result    string                  `json:"result"`
	IsError   bool                    `json:"is_error"`
	Usage     claudeStreamUsageFields `json:"usage"`
	// PermissionDenials is set on the result event: the tool calls the
	// permission mode refused.
	PermissionDenials []claudePermissionDenial `json:"permission_denials"`
	// RateLimitInfo is set on a rate_limit_event: the plan windows.
	RateLimitInfo *claudeRateLimitInfo `json:"rate_limit_info"`
}

type claudePermissionDenial struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

type claudeStreamMessage struct {
	ID      string                `json:"id"`
	Content []claudeStreamContent `json:"content"`
	// Usage is this message's own usage. The same message can arrive in
	// several assistant events (one per content block), each repeating it.
	Usage claudeStreamUsageFields `json:"usage"`
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
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// cachedTokens is the part of the prompt that was written to or read from the cache.
func (u claudeStreamUsageFields) cachedTokens() int {
	return u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// claudeLiveUsage adds up the usage of the assistant messages of a run. A
// message seen again (same ID) replaces its earlier entry instead of being
// added twice, so the sum matches what the result event reports at the end.
type claudeLiveUsage struct {
	byID  map[string]claudeStreamUsageFields
	extra []claudeStreamUsageFields // messages without an ID
	last  TokenUsage
	// lastPrompt is the prompt size of the newest assistant message.
	lastPrompt int
}

// add records one assistant message and returns the cumulative usage when it
// changed, else nil.
func (l *claudeLiveUsage) add(m claudeStreamMessage) *TokenUsage {
	u := m.Usage
	if u.promptTokens() == 0 && u.OutputTokens == 0 {
		return nil
	}
	if p := u.promptTokens(); p > 0 {
		l.lastPrompt = p
	}
	if m.ID != "" {
		if l.byID == nil {
			l.byID = map[string]claudeStreamUsageFields{}
		}
		l.byID[m.ID] = u
	} else {
		l.extra = append(l.extra, u)
	}
	var sum TokenUsage
	for _, v := range l.byID {
		sum.InputTokens += v.promptTokens()
		sum.CachedTokens += v.cachedTokens()
		sum.OutputTokens += v.OutputTokens
	}
	for _, v := range l.extra {
		sum.InputTokens += v.promptTokens()
		sum.CachedTokens += v.cachedTokens()
		sum.OutputTokens += v.OutputTokens
	}
	if sum == l.last {
		return nil
	}
	l.last = sum
	return &sum
}

// promptTokens is the full prompt size: uncached input plus cache writes and reads.
func (u claudeStreamUsageFields) promptTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
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
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
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
	inputTokens := result.Usage.InputTokens + result.Usage.CacheCreationInputTokens + result.Usage.CacheReadInputTokens
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
