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
)

// WorkerRequest is one workspace sub-task for a generic worker.
type WorkerRequest struct {
	RolePrompt         string // role description or empty when Skills carry it
	Skills             []string
	Model              string
	Title, Description string
	Context            string // project context + dependency summaries + current files
	ReadOnly           bool   // report only: file blocks are never written
}

// WorkerResult is what a worker produced.
type WorkerResult struct {
	Output, Summary string
	Files           []string
	Skipped         []string // file blocks that could not be written, each "path: reason"
	Usage           runner.TokenUsage
}

// IsBinaryContent reports whether data looks like binary (non-text) content.
func IsBinaryContent(data []byte) bool { return isBinaryContent(data) }

const workerSummaryMarker = "===SUMMARY==="

const (
	workerModeWrite    = "To create or change a file, write its path in bold on its own line, followed by a fenced\ncode block with the COMPLETE file content:\n\n**internal/example/example.go**\n```go\npackage example\n// complete file content\n```\n\nEvery code block MUST be preceded by the file path in bold on its own line, otherwise\nthe file is not saved. Never output partial files or placeholders.\n\nIf your role reviews or analyses (no files to change), write a report instead and do not\noutput file blocks."
	workerModeReadOnly = "You are in read-only mode: you MUST NOT change any files. Anything you output as a file block\nis discarded. Only report: write your findings as plain text (you may quote code in\nfenced blocks, but do not put a file path in bold before them)."
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
	// Task and context last: they are user-controlled and may contain
	// placeholder text.
	sys := prompts.MustLoad("ws-worker")
	mode := workerModeWrite
	if req.ReadOnly {
		mode = workerModeReadOnly
	}
	sys = strings.Replace(sys, "{{MODE}}", mode, 1)
	sys = strings.Replace(sys, "{{ROLE}}", role, 1)
	sys = strings.Replace(sys, "{{TASK}}", task, 1)
	sys = strings.Replace(sys, "{{CONTEXT}}", req.Context, 1)

	ch, err := r.Complete(ctx, runner.CompletionRequest{
		SystemPrompt: sys,
		Skills:       req.Skills,
		Model:        req.Model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: task}},
	})
	if err != nil {
		return res, fmt.Errorf("worker: runner: %w", err)
	}
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return res, fmt.Errorf("worker: stream: %w", tok.Error)
		}
		// Usage snapshots from streaming runners are cumulative: keep the latest.
		if tok.Usage != nil {
			res.Usage = *tok.Usage
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
	if err := ctx.Err(); err != nil {
		return res, err
	}
	res.Output = sb.String()
	if strings.TrimSpace(res.Output) == "" {
		return res, errors.New("worker: empty response")
	}

	res.Summary = workerSummary(res.Output)
	if req.ReadOnly {
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
		err := writeProjectFile(root, b.Path, b.Content, true, cfg.Project.Scope)
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
