package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

// OpenCodeRunner executes the OpenCode CLI in non-interactive mode as an LLM backend.
// Uses `opencode run` with --format json for structured output.
type OpenCodeRunner struct {
	Binary string
	Model  string
}

// Complete implements LLMRunner by running the OpenCode CLI with `opencode run`.
func (r OpenCodeRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	var sb strings.Builder
	if req.SystemPrompt != "" {
		sb.WriteString(req.SystemPrompt)
		sb.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		sb.WriteString(strings.ToUpper(m.Role))
		sb.WriteString(":\n")
		sb.WriteString(m.Content)
		sb.WriteString("\n\n")
	}

	model := req.Model
	if model == "" {
		model = r.Model
	}

	ch := make(chan Token, 16)
	go func() {
		defer close(ch)
		prompt := sb.String()
		parser := newOpenCodeParser()
		raw, err := r.runStream(ctx, prompt, model, NormalizeEffort(req.Effort), req.Dir, func(line []byte) {
			for _, tok := range parser.feed(line) {
				ch <- tok
			}
		})
		if err != nil {
			ch <- Token{Error: err}
			ch <- Token{Done: true}
			return
		}
		text, usage := parser.finish(raw, prompt)
		if !parser.sawText && text != "" {
			ch <- Token{Text: text}
		}
		ch <- Token{Done: true, Usage: &usage}
	}()
	return ch, nil
}

// runRaw runs opencode to completion and returns its whole stdout.
func (r OpenCodeRunner) runRaw(ctx context.Context, prompt, model, effort, dir string) ([]byte, error) {
	return r.runStream(ctx, prompt, model, effort, dir, nil)
}

// runStream runs opencode and calls onLine for every stdout line as soon as it
// is written. It returns the whole stdout once the process has exited.
func (r OpenCodeRunner) runStream(ctx context.Context, prompt, model, effort, dir string, onLine func([]byte)) ([]byte, error) {
	bin := r.Binary
	if bin == "" {
		bin = "opencode"
	}

	if model == "" {
		model = "qwen2.5-coder:3b"
	}

	args := []string{"run", "--format", "json"}

	// OpenCode requires "provider/model" format.
	// For local Ollama models (no provider prefix), add the ollama/ prefix.
	local := !strings.Contains(model, "/") || strings.HasPrefix(model, "ollama/")
	if local {
		if !strings.HasPrefix(model, "ollama/") {
			model = "ollama/" + model
		}
	} else if effort != "" && !strings.Contains(model, "#") {
		// Cloud models use #variant for provider-specific reasoning effort.
		model += "#" + effort
	}

	args = append(args, "-m", model)
	// Prompt is piped via stdin to avoid macOS ARG_MAX limits on large prompts.
	args = append(args, "-")

	cmd := executil.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("opencode: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("opencode: %w", err)
	}

	var out bytes.Buffer
	br := bufio.NewReader(stdout)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			out.Write(line)
			if onLine != nil {
				if t := bytes.TrimSpace(line); len(t) > 0 {
					onLine(t)
				}
			}
		}
		if readErr != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if rl := ClassifyRateLimit("opencode", stderrStr, out.String()); rl != nil {
			return nil, rl
		}
		return nil, fmt.Errorf("opencode: %w: %s", err, stderrStr)
	}
	// On success only error events are scanned; the model's own answer text
	// may legitimately mention "rate limit" or "429".
	if rl := ClassifyRateLimit("opencode", openCodeErrorFragments(out.Bytes())...); rl != nil {
		return nil, rl
	}
	return out.Bytes(), nil
}

// openCodeErrorFragments returns the raw error payloads of "error" events in an
// opencode NDJSON stream, ignoring text parts.
func openCodeErrorFragments(data []byte) []string {
	var frags []string
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Type  string          `json:"type"`
			Error json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type == "error" || len(event.Error) > 0 {
			frags = append(frags, string(line))
		}
	}
	return frags
}

// extractOpenCodeResponseWithUsage parses NDJSON event stream from opencode --format json.
// Returns concatenated text from all "text" type events, plus token usage.
// If a "usage" event is present, returns exact counts (Estimated: false).
// Otherwise estimates both input and output tokens from text length so local
// models (run via opencode) still report usage in the monitor instead of zero.
func extractOpenCodeResponseWithUsage(data []byte, inputText string) (string, TokenUsage) {
	p := newOpenCodeParser()
	for _, line := range bytes.Split(data, []byte("\n")) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			p.feed(line)
		}
	}
	return p.finish(data, inputText)
}

// OpenCodeAvailableModels is a fallback list used when the CLI is unavailable.
var OpenCodeAvailableModels = []string{
	"qwen2.5-coder:3b",
	"qwen2.5-coder:latest",
	"llama3.2:latest",
	"deepseek-coder-v2:latest",
	"opencode/big-pickle",
	"opencode/gpt-5-nano",
}

// OpenCodeListModels returns available models from the OpenCode CLI.
func OpenCodeListModels() ([]string, error) {
	var models []string

	cmd := executil.Command("opencode", "models")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Run(); err == nil {
		for _, line := range strings.Split(out.String(), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				models = append(models, line)
			}
		}
	}

	if len(models) == 0 {
		return nil, fmt.Errorf("no models found (is opencode installed?)")
	}
	return models, nil
}
