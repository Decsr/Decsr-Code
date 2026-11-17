package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

// DshRunner executes the DeepSeek Harness CLI (dsh) in its headless profile as
// an LLM backend: `dsh --profile headless --json -` reads the task from stdin
// and writes one JSON event per line to stdout.
type DshRunner struct {
	Binary string
	Model  string // "provider/model"; empty keeps the profile default
}

const (
	// dshDefaultProvider and dshDefaultModel are the headless profile's own
	// default route. They fill in a patch that only sets an effort, because a
	// patched row replaces the whole config of the row.
	dshDefaultProvider = "deepseek-official"
	dshDefaultModel    = "deepseek-flash"
	dshKeyEnv          = "DEEPSEEK_API_KEY"
)

// Complete implements LLMRunner. The system prompt and the messages go to dsh
// as one task on stdin, like the opencode runner.
func (r DshRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
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
	prompt := sb.String()

	model := req.Model
	if model == "" {
		model = r.Model
	}
	effort := NormalizeEffort(req.Effort)

	opts := dshOptions{Agent: req.Agent, ReadOnly: req.ReadOnly}
	if req.Agent && !req.ReadOnly {
		opts.GitDir = dshGitCommonDir(req.Dir)
	}
	patchPath := ""
	if patch := dshPatch(model, effort); patch != "" {
		f, err := os.CreateTemp("", "decsr-dsh-*.yml")
		if err != nil {
			return nil, fmt.Errorf("dsh: patch file: %w", err)
		}
		patchPath = f.Name()
		_, werr := f.WriteString(patch)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(patchPath)
			return nil, fmt.Errorf("dsh: patch file: %w", werr)
		}
		opts.PatchFile = patchPath
	}

	ch := make(chan Token, 16)
	go func() {
		defer close(ch)
		if patchPath != "" {
			defer func() { _ = os.Remove(patchPath) }()
		}
		parser := newDshParser(model)
		err := r.run(ctx, prompt, req.Dir, opts, func(line []byte) {
			for _, tok := range parser.feed(line) {
				ch <- tok
			}
		}, parser)
		if err != nil {
			ch <- Token{Error: err}
			ch <- Token{Done: true}
			return
		}
		text, usage := parser.finish(prompt)
		if !parser.sawText && text != "" {
			ch <- Token{Text: text}
		}
		ch <- Token{Done: true, Usage: &usage}
	}()
	// A step's text arrives only when the step commits, so a long step is
	// silent; a heartbeat keeps the UI alive.
	return withHeartbeat(ch, defaultHeartbeatInterval), nil
}

// dshOptions are the inputs of the dsh argument list.
type dshOptions struct {
	Agent     bool   // the runner edits files itself
	ReadOnly  bool   // the role reports only
	GitDir    string // absolute shared git directory granted to a writing agent; "" = none
	PatchFile string // YAML patch overlay; "" = none
}

// dshArgs builds the argument list. --profile and --patch belong to the
// launcher and come before the app arguments. A writing agent gets the
// workspace-write preset (and the shared git directory, so it can commit);
// everything else, text mode included, gets the read-only preset. Approvals
// are never asked for: nobody could answer.
func dshArgs(o dshOptions) []string {
	args := []string{"--profile", "headless"}
	if o.PatchFile != "" {
		args = append(args, "--patch", o.PatchFile)
	}
	args = append(args, "--json")
	if o.Agent && !o.ReadOnly {
		args = append(args, "--permission-mode", "workspace-write", "--approval-policy", "never")
		if o.GitDir != "" {
			args = append(args, "--add-dir", o.GitDir)
		}
	} else {
		args = append(args, "--permission-mode", "read-only", "--approval-policy", "never")
	}
	// The task is piped via stdin to avoid ARG_MAX limits on large prompts.
	return append(args, "-")
}

// dshGitCommonDir is the physical absolute git common directory of dir, or ""
// when dir is not in a git repository.
func dshGitCommonDir(dir string) string {
	cmd := executil.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(string(out))
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

// dshPatch is the YAML overlay that selects the model and effort of the
// default-model row, or "" when neither is set. model is "provider/model"; a
// model without a provider uses the profile's default provider. JSON strings
// are valid YAML scalars, so values are quoted with json.Marshal.
func dshPatch(model, effort string) string {
	effort = mapDshEffort(effort)
	if model == "" && effort == "" {
		return ""
	}
	provider, id := dshDefaultProvider, dshDefaultModel
	if model != "" {
		if p, m, ok := strings.Cut(model, "/"); ok && p != "" && m != "" {
			provider, id = p, m
		} else {
			id = model
		}
	}
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var sb strings.Builder
	sb.WriteString("- id: agent-default-model\n")
	sb.WriteString("  name: \"@deepseek-ai/dsh-agent-default-model\"\n")
	sb.WriteString("  config:\n")
	sb.WriteString("    provider: " + q(provider) + "\n")
	sb.WriteString("    model: " + q(id) + "\n")
	if effort != "" {
		sb.WriteString("    reasoningEffort: " + q(effort) + "\n")
	}
	return sb.String()
}

// run starts dsh, feeds every stdout line to onLine as soon as it is written
// and maps the exit to an error: a run succeeds only when dsh exits 0 and the
// final event says the turn completed.
func (r DshRunner) run(ctx context.Context, prompt, dir string, o dshOptions, onLine func([]byte), p *dshParser) error {
	bin := r.Binary
	if bin == "" {
		bin = "dsh"
	}
	cmd := executil.CommandContext(ctx, bin, dshArgs(o)...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = os.Environ()
	// The headless profile ignores keys saved by the GUI and reads the key from
	// the environment, so a key saved by decsr is handed over this way.
	if os.Getenv(dshKeyEnv) == "" {
		if key, _, err := ResolveKey("deepseek", ""); err == nil && key != "" {
			cmd.Env = append(cmd.Env, dshKeyEnv+"="+key)
		}
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("dsh: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("dsh: %w", err)
	}
	br := bufio.NewReader(stdout)
	for {
		line, readErr := br.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			onLine(t)
		}
		if readErr != nil {
			break
		}
	}
	waitErr := cmd.Wait()
	return p.failure(waitErr, strings.TrimSpace(stderr.String()))
}

// MapDshEffort is mapDshEffort for callers outside the package.
func MapDshEffort(effort string) string { return mapDshEffort(effort) }

// mapDshEffort maps a decsr effort to the DeepSeek vocabulary (off, low, high,
// max); "" stays unset.
func mapDshEffort(effort string) string {
	switch effort {
	case "":
		return ""
	case "off", "none", "minimal":
		return "off"
	case "low":
		return "low"
	case "medium", "high":
		return "high"
	default: // xhigh, max
		return "max"
	}
}
