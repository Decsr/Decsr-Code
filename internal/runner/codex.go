package runner

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/safefile"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// CodexRunner executes the OpenAI Codex CLI as an LLM backend.
// Uses `codex --quiet` for non-interactive text output.
type CodexRunner struct {
	Binary string
	Model  string
}

// CodexModels lists known Codex-compatible model names.
var CodexModels = []string{
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
	"gpt-5.3-codex",
	"gpt-5.2-codex",
	"gpt-5.2",
	"gpt-5.1-codex-max",
	"gpt-5.1-codex-mini",
}

// CodexListModels returns available models by merging the currently configured
// model from ~/.codex/config.toml with the known models list.
// No API keys or network calls required.
func CodexListModels() ([]string, error) {
	configured := readCodexConfigModel()
	if configured == "" {
		return CodexModels, nil
	}

	// Put configured model first, then append others (skip duplicates).
	models := []string{configured}
	for _, m := range CodexModels {
		if m != configured {
			models = append(models, m)
		}
	}
	return models, nil
}

// readCodexConfigModel reads the top-level "model" value from ~/.codex/config.toml.
func readCodexConfigModel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	codexDir := filepath.Join(home, ".codex")
	data, err := safefile.ReadFile(codexDir, "config.toml")
	if err != nil {
		return ""
	}
	return parseCodexConfigModel(string(data))
}

// parseCodexConfigModel extracts the top-level `model` value from a Codex
// config.toml. It matches the exact `model` key only — sibling keys such as
// `model_provider` or `model_reasoning_effort` must not be mistaken for the
// model (that bug surfaced foreign values like "omlx" in the Codex model list).
func parseCodexConfigModel(data string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		// Stop at the first section header — only the top-level table counts.
		if strings.HasPrefix(line, "[") {
			break
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		if strings.TrimSpace(parts[0]) != "model" {
			continue
		}
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if val != "" {
			return val
		}
	}
	return ""
}

// Complete implements LLMRunner by running the Codex CLI with the given prompt.
// Stdout is streamed incrementally so the TUI shows progress live instead of
// a single burst at the end of generation.
func (r CodexRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	var userContent strings.Builder
	for _, m := range req.Messages {
		userContent.WriteString(strings.ToUpper(m.Role))
		userContent.WriteString(":\n")
		userContent.WriteString(m.Content)
		userContent.WriteString("\n\n")
	}

	model := req.Model
	if model == "" {
		model = r.Model
	}

	cmd, stdout, stderr, err := r.startStreamingProcess(ctx, userContent.String(), req.SystemPrompt, model, NormalizeEffort(req.Effort), req.Dir, req.Agent)
	if err != nil {
		return nil, err
	}

	ch := make(chan Token, 16)
	fullInput := req.SystemPrompt + "\n\n" + userContent.String()
	go streamCodexOutput(cmd, stdout, stderr, model, fullInput, ch)
	return ch, nil
}

func (r CodexRunner) startStreamingProcess(ctx context.Context, prompt, systemPrompt, model, effort, dir string, agent bool) (*exec.Cmd, io.ReadCloser, *bytes.Buffer, error) {
	bin := r.Binary
	if bin == "" {
		bin = "codex"
	}

	// Merge system prompt and user prompt into one block for stdin piping.
	fullPrompt := prompt
	if systemPrompt != "" {
		fullPrompt = systemPrompt + "\n\n" + prompt
	}

	// Text mode must not let codex edit files (decsr writes them), so its
	// sandbox is read-only. Agent mode may write inside the working directory.
	sandbox := "read-only"
	if agent {
		sandbox = "workspace-write"
	}
	args := []string{"exec", "--sandbox", sandbox, "--json"}
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "-c", `model_reasoning_effort="`+mapCodexEffort(effort)+`"`)
	}
	// Prompt is piped via stdin to avoid macOS ARG_MAX limits on large prompts.

	cmd := executil.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(fullPrompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("codex: start: %w", err)
	}
	return cmd, stdout, stderr, nil
}

// streamCodexOutput reads the JSONL event stream of `codex exec --json` while
// the CLI runs, emits an Activity per item, and delivers the last agent message
// (what plain `codex exec` prints) as Text when the stream ends. The final
// Done token carries the usage reported by turn.completed.
func streamCodexOutput(cmd *exec.Cmd, stdout io.ReadCloser, stderr *bytes.Buffer, model, fullInput string, ch chan<- Token) {
	defer close(ch)

	p := newCodexParser()
	br := bufio.NewReader(stdout)
	for {
		line, readErr := br.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			for _, a := range p.feed(t, time.Now()) {
				ch <- Token{Activity: a}
			}
		}
		if readErr != nil {
			break
		}
	}

	waitErr := cmd.Wait()
	output := p.text()
	// A non-zero exit with a rate-limit stderr is a failure even if partial
	// output was streamed first.
	if waitErr != nil && (strings.TrimSpace(output) == "" || ClassifyRateLimit("codex", stderr.String()) != nil) {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = strings.Join(p.errorMsgs, "; ")
		}
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		if rl := ClassifyRateLimit("codex", errMsg, output); rl != nil {
			ch <- Token{Error: rl}
		} else {
			ch <- Token{Error: fmt.Errorf("codex: %w: %s", waitErr, errMsg)}
		}
		return
	}

	if output != "" {
		ch <- Token{Text: output}
	}
	usage := countCodexTokens(model, fullInput, output)
	if p.gotUsage {
		usage = p.usage
		usage.Estimated = false
	}
	ch <- Token{Done: true, Usage: &usage}
}

// countCodexTokens uses tiktoken to count input and output tokens exactly.
// Falls back to char-based estimation if the model encoding is unknown.
func countCodexTokens(model, inputText, outputText string) TokenUsage {
	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// Unknown model — fall back to cl100k_base (GPT-4 encoding).
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			// tiktoken completely unavailable — use char heuristic.
			return TokenUsage{
				InputTokens:  tokenutil.EstimateTokens(inputText),
				OutputTokens: tokenutil.EstimateTokens(outputText),
				Estimated:    true,
			}
		}
		// cl100k_base fallback — counts are approximate for this model.
		inputToks := enc.Encode(inputText, nil, nil)
		outputToks := enc.Encode(outputText, nil, nil)
		return TokenUsage{
			InputTokens:  len(inputToks),
			OutputTokens: len(outputToks),
			Estimated:    true,
		}
	}

	inputToks := enc.Encode(inputText, nil, nil)
	outputToks := enc.Encode(outputText, nil, nil)

	return TokenUsage{
		InputTokens:  len(inputToks),
		OutputTokens: len(outputToks),
		Estimated:    false,
	}
}
