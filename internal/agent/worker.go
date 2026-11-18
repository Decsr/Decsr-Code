package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/prompts"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// WorkerRequest is one workspace sub-task for a generic worker.
type WorkerRequest struct {
	RolePrompt         string // role description or empty when Skills carry it
	Skills             []string
	Model              string
	Title, Description string
	Context            string // project context + dependency summaries + current files
	ReadOnly           bool   // report only: file blocks are never written
	// Mode is config.ModeText (or empty) or config.ModeAgent. In agent mode the
	// runner edits files itself and file blocks in its text are ignored.
	Mode string
	// MCP keeps the user's own MCP servers for a Claude run.
	MCP bool
	// MCPServers are the MCP servers the lead marked for this node (Claude
	// only). The run loads the user's servers and may use only these.
	MCPServers []string
	// BuildCommands are the project's build and test commands an agent-mode
	// Claude run may execute.
	BuildCommands []string
	// AllowedCommands are extra command prefixes (project.allowed_commands)
	// an agent-mode Claude run may execute.
	AllowedCommands []string
	// OnActivity, when set, receives the runner's activity (thinking, tools,
	// commands) and the file blocks the worker writes. Nil-safe.
	OnActivity func(runner.Activity)
	// OnContext, when set, is called once before the runner starts with the
	// estimated token count of the full prompt (system prompt and messages).
	// Nil-safe.
	OnContext func(tokens int)
	// OnUsage, when set, receives the cumulative usage while the runner
	// works, as often as the runner reports it (the caller throttles). It is
	// advisory: WorkerResult.Usage is the final value. Nil-safe.
	OnUsage func(runner.TokenUsage)
	// OnModel, when set, receives the model the runner reports it is using,
	// once. Nil-safe.
	OnModel func(model string)
	// KeepSession keeps the runner's own conversation so a later call can
	// resume it; OnSession, when set, receives its id once the runner reports
	// it. Nil-safe.
	KeepSession bool
	OnSession   func(id string)
	// ResumeID resumes the runner's conversation with this id. The call then
	// sends ResumeMessage only: the conversation already holds the task and the
	// context.
	ResumeID      string
	ResumeMessage string
}

// WorkerResult is what a worker produced.
type WorkerResult struct {
	Output, Summary string
	Files           []string
	Skipped         []string // file blocks that could not be written, each "path: reason"
	// Report is the worker's closing REPORT block. Without one it is done with
	// the extracted summary.
	Report Report
	Usage  runner.TokenUsage
	// Denials are the tool calls the runner refused during the run.
	Denials []runner.Denial
	// Body is the reply without the closing REPORT block.
	Body string
}

// IsBinaryContent reports whether data looks like binary (non-text) content.
func IsBinaryContent(data []byte) bool { return isBinaryContent(data) }

const workerSummaryMarker = "===SUMMARY==="

const (
	// workerTail ends the text-mode prompts.
	workerTail = "\n\nFiles shown under \"Current files\" in the project context are the latest versions on disk,\nincluding changes made by earlier agents. When you change one of them, output the\nCOMPLETE updated file and keep everything unrelated to your sub-task intact. Never\nre-create a file from memory.\n\nEnd your answer with:\n\n===SUMMARY===\n1 to 5 lines: what you did and anything the next agent must know." + reportInstruction
	// workerAgentTail ends the agent-mode prompts.
	workerAgentTail = "\n\nEnd your answer with:\n\n===SUMMARY===\n1 to 5 lines: what you changed (or found) and anything the next agent must know." + reportInstruction

	workerModeWrite    = "To create or change a file, write its path in bold on its own line, followed by a fenced\ncode block with the COMPLETE file content:\n\n**internal/example/example.go**\n```go\npackage example\n// complete file content\n```\n\nEvery code block MUST be preceded by the file path in bold on its own line, otherwise\nthe file is not saved. Never output partial files or placeholders.\n\nIf your role reviews or analyses (no files to change), write a report instead and do not\noutput file blocks." + workerTail
	workerModeReadOnly = "You are in read-only mode: you MUST NOT change any files. Anything you output as a file block\nis discarded. Only report: write your findings as plain text (you may quote code in\nfenced blocks, but do not put a file path in bold before them)." + workerTail

	workerModeAgent         = "You are running as a coding agent in the project working directory. Change files directly\nwith your tools. Do NOT print file contents as fenced file blocks: they are not saved.\nFiles shown under \"Current files\" are on disk and you can read them. Keep everything\nunrelated to your sub-task intact. When build or test commands are available to you, run\nthem to check your work." + workerAgentTail
	workerModeAgentReadOnly = "You are running as a read-only agent in the project working directory. Use your tools only\nto read and search: you MUST NOT create, change, move or delete any file, and must not run\ncommands that do. Report your findings as plain text." + workerAgentTail
)

// RunWorker runs one LLM call for a sub-task, writes the file blocks of the
// answer under root with the Coder's path safety rules and returns a summary.
func RunWorker(ctx context.Context, r runner.LLMRunner, root string, cfg config.Config, req WorkerRequest, onToken func(string)) (WorkerResult, error) {
	var res WorkerResult
	task := req.Title
	if strings.TrimSpace(req.Description) != "" {
		task += "\n\n" + req.Description
	}
	role := req.RolePrompt
	if strings.TrimSpace(role) == "" {
		role = "Follow the role instructions you were given."
	}
	agentMode := req.Mode == config.ModeAgent
	mode := workerModeWrite
	switch {
	case agentMode && req.ReadOnly:
		mode = workerModeAgentReadOnly
	case agentMode:
		mode = workerModeAgent
	case req.ReadOnly:
		mode = workerModeReadOnly
	}
	// One pass, so placeholder text inside a role, skill, task or context is
	// never substituted.
	sys := strings.NewReplacer(
		"{{MODE}}", mode,
		"{{ROLE}}", role,
		"{{CONTEXT}}", req.Context,
	).Replace(prompts.MustLoad("ws-worker"))

	msgs := []runner.ConvMessage{{Role: "user", Content: task}}
	if req.ResumeID != "" {
		msgs = []runner.ConvMessage{{Role: "user", Content: req.ResumeMessage}}
	}
	if req.OnContext != nil {
		total := tokenutil.EstimateTokens(sys)
		for _, m := range msgs {
			total += tokenutil.EstimateTokens(m.Content)
		}
		req.OnContext(total)
	}
	creq := runner.CompletionRequest{
		SystemPrompt: sys,
		Skills:       req.Skills,
		Model:        req.Model,
		Dir:          root,
		Messages:     msgs,
		Agent:        agentMode,
		MCP:          req.MCP,
		ReadOnly:     req.ReadOnly,
		KeepSession:  req.KeepSession,
		ResumeID:     req.ResumeID,
	}
	if agentMode {
		creq.AllowedTools = runner.AgentTools(req.BuildCommands, req.AllowedCommands, req.ReadOnly)
		if rules := runner.MCPServerRules(req.MCPServers); len(rules) > 0 {
			creq.MCP = true
			creq.AllowedTools = append(creq.AllowedTools, rules...)
		}
	}
	ch, err := r.Complete(ctx, creq)
	if err != nil {
		return res, fmt.Errorf("worker: runner: %w", err)
	}
	var sb strings.Builder
	writes := newWriteTracker(root, req.OnActivity)
	watch := &blockWatcher{}
	if !req.ReadOnly && !agentMode {
		watch.onStart = writes.begin
	}
	var live *runner.TokenUsage // latest Progress
	for tok := range ch {
		if tok.Error != nil {
			writes.finish()
			return res, fmt.Errorf("worker: stream: %w", tok.Error)
		}
		if tok.Activity != nil && req.OnActivity != nil {
			req.OnActivity(*tok.Activity)
		}
		if tok.SessionID != "" && req.OnSession != nil {
			req.OnSession(tok.SessionID)
		}
		if tok.Model != "" && req.OnModel != nil {
			req.OnModel(tok.Model)
		}
		if tok.Progress != nil {
			p := *tok.Progress
			live = &p
			if req.OnUsage != nil {
				req.OnUsage(p)
			}
		}
		// Usage tokens are added up: most runners send one on the Done token,
		// copilot sends deltas before it. A Done token without usage adds
		// nothing, so it never zeroes what came before.
		if tok.Usage != nil {
			res.Usage.InputTokens += tok.Usage.InputTokens
			res.Usage.CachedTokens += tok.Usage.CachedTokens
			res.Usage.OutputTokens += tok.Usage.OutputTokens
			res.Usage.Estimated = res.Usage.Estimated || tok.Usage.Estimated
		}
		if len(tok.Denials) > 0 {
			res.Denials = tok.Denials
		}
		if tok.Text != "" {
			sb.WriteString(tok.Text)
			if req.OnActivity != nil && !req.ReadOnly && !agentMode {
				watch.feed(tok.Text)
			}
			if onToken != nil {
				onToken(tok.Text)
			}
		}
		if tok.Done {
			break
		}
	}
	// A runner that sent progress but no final usage keeps its last progress.
	if res.Usage.InputTokens == 0 && res.Usage.OutputTokens == 0 && live != nil {
		res.Usage = *live
	}
	if err := ctx.Err(); err != nil {
		writes.finish()
		return res, err
	}
	res.Output = sb.String()
	if strings.TrimSpace(res.Output) == "" {
		writes.finish()
		return res, errors.New("worker: empty response")
	}

	rep, body, found := ParseReport(res.Output)
	res.Body = strings.TrimSpace(body)
	res.Summary = workerSummary(body)
	if found {
		res.Report = rep
		if res.Summary == "" {
			res.Summary = rep.Summary
		}
	} else {
		res.Report = Report{Status: ReportDone, Summary: res.Summary}
	}
	// Agent mode: the runner changed the files itself, so any fenced block is
	// just text. The caller finds the changes by snapshot or worktree commit.
	if req.ReadOnly || agentMode {
		writes.finish()
		return res, nil
	}
	// Only blocks with an explicit path are written: a path guessed from the
	// content is usually an illustration, or a file an agentic runner already
	// created itself.
	var blocks []FileBlock
	for _, b := range ExtractFileBlocks(res.Output) {
		if !b.Guessed {
			blocks = append(blocks, b)
		}
	}
	var firstErr error
	for _, b := range blocks {
		err := writes.write(b.Path, b.Content, func() error {
			return writeProjectFile(root, b.Path, b.Content, true, cfg.Project.Scope)
		})
		if err != nil && !errors.Is(err, errNoOpRewrite) {
			res.Skipped = append(res.Skipped, b.Path+": "+skipReason(err))
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", b.Path, err)
			}
			continue
		}
		// A no-op rewrite counts as written.
		res.Files = append(res.Files, b.Path)
	}
	writes.finish()
	res.Files = dedupePaths(res.Files)
	res.Skipped = dedupePaths(res.Skipped)
	if len(blocks) > 0 && len(res.Files) == 0 && firstErr != nil {
		return res, fmt.Errorf("worker: no file could be written: %w", firstErr)
	}
	return res, nil
}

// workerSummary returns the text after the summary marker, else the last five
// non-empty lines of the output that are outside code fences.
func workerSummary(output string) string {
	if i := strings.Index(output, workerSummaryMarker); i >= 0 {
		return strings.TrimSpace(output[i+len(workerSummaryMarker):])
	}
	var lines []string
	inFence := false
	for _, l := range strings.Split(output, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" {
			continue
		}
		lines = append(lines, t)
	}
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "\n")
}

// skipReason drops the "refusing to write <path>: " prefix of a writer error
// so the path is not repeated in a "path: reason" entry.
func skipReason(err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "refusing to write ") {
		if _, rest, ok := strings.Cut(msg, ": "); ok {
			return rest
		}
	}
	return msg
}

// dedupePaths drops repeated entries (compared as cleaned, slash-normalised
// paths) and keeps the order of first appearance.
func dedupePaths(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, p := range in {
		k := filepath.ToSlash(filepath.Clean(p))
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}
