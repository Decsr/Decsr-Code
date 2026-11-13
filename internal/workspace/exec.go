package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/skills"
)

// EventType classifies an Event.
type EventType string

const (
	EventStarted EventType = "started"
	EventOutput  EventType = "output"
	EventUsage   EventType = "usage"
	EventDone    EventType = "done"
	EventFailed  EventType = "failed"
)

// Event is a progress notification for one node. NodeID "" is used for
// engine-level messages (for example a failed save).
type Event struct {
	NodeID string
	Type   EventType
	Text   string // output chunk, or the error text for EventFailed
	// EventUsage: cumulative for the node.
	In, Out   int
	Estimated bool
}

// Result is the outcome of a successful node run.
type Result struct {
	Summary  string
	Files    []string // written from the worker's explicit file blocks
	Observed []string // new or changed on disk during the run, by the agent's own tools
	Warnings []string
}

// Executor runs one node. It must return promptly when ctx is cancelled.
type Executor interface {
	Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error)
}

// maxCheckErrorBytes caps the failure text of a check node.
const maxCheckErrorBytes = 4000

const (
	// checkTimeout bounds each check command (builds and test suites can be slow).
	checkTimeout = 15 * time.Minute
	// maxContextFileBytes caps one file in the "Current files" section.
	maxContextFileBytes = 24 * 1024
	// maxContextFilesBytes caps the whole "Current files" section.
	maxContextFilesBytes = 120 * 1024

	noCheckCommandsMsg = "no build or test command is known for this project yet (set project.test_cmd or pick a language in Project Setup)"
)

type executor struct {
	root           string
	cfg            config.Config
	projectContext string
	newRunner      func(config.AgentConfig) (runner.LLMRunner, error)
	checkCommands  func() []string // commands for a check node without Commands; may be nil
	checkTimeout   time.Duration   // 0 = checkTimeout
	summary        func() string   // session summary for the work context; may be nil
}

// maxSessionSummaryChars caps the session summary given to a worker.
const maxSessionSummaryChars = 4000

// NewExecutor returns the real Executor: work nodes go through the generic
// worker, check nodes run their shell commands.
func NewExecutor(root string, cfg config.Config, projectContext string) Executor {
	return NewExecutorWithChecks(root, cfg, projectContext, nil)
}

// NewExecutorWithChecks is NewExecutor with a lazy source of check commands:
// a check node without Commands calls checkCommands when it runs.
func NewExecutorWithChecks(root string, cfg config.Config, projectContext string, checkCommands func() []string) Executor {
	return NewExecutorWithSummary(root, cfg, projectContext, checkCommands, nil)
}

// NewExecutorWithSummary is NewExecutorWithChecks plus a source of the session
// summary, which work nodes get as an "Earlier in this session" section.
func NewExecutorWithSummary(root string, cfg config.Config, projectContext string, checkCommands func() []string, summary func() string) Executor {
	loader := skills.New("")
	return &executor{
		root:           root,
		cfg:            cfg,
		projectContext: projectContext,
		checkCommands:  checkCommands,
		summary:        summary,
		newRunner: func(ac config.AgentConfig) (runner.LLMRunner, error) {
			return runner.New(ac, loader, cfg.PromptLanguage)
		},
	}
}

func (x *executor) Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	if n.Kind == KindCheck {
		return x.runCheck(ctx, n, emit)
	}
	return x.runWork(ctx, g, n, emit)
}

func (x *executor) runWork(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	role, ok := FindRole(x.cfg, n.Role)
	if !ok {
		return Result{}, fmt.Errorf("role %q is not defined", n.Role)
	}
	r, err := x.newRunner(role.Agent)
	if err != nil {
		return Result{}, fmt.Errorf("runner for role %q: %w", role.Name, err)
	}
	rolePrompt := ""
	switch {
	case role.Plugin:
		rolePrompt = role.Prompt
	case !role.Builtin:
		rolePrompt = role.Description
	}
	skillBlock, skillWarnings := x.userSkills(role.UserSkills)
	if skillBlock != "" {
		if rolePrompt != "" {
			rolePrompt += "\n\n"
		}
		rolePrompt += skillBlock
	}
	before, snapOK := snapshotTree(x.root, maxSnapshotFiles)
	res, err := agent.RunWorker(ctx, r, x.root, x.cfg, agent.WorkerRequest{
		RolePrompt:  rolePrompt,
		Skills:      role.Agent.Skills,
		Model:       role.Agent.Model,
		Title:       n.Title,
		Description: n.Description,
		Context:     x.workContext(g, n),
		ReadOnly:    role.ReadOnly,
	}, func(s string) { emit(Event{NodeID: n.ID, Type: EventOutput, Text: s}) })
	if res.Usage.InputTokens > 0 || res.Usage.OutputTokens > 0 {
		emit(Event{NodeID: n.ID, Type: EventUsage, In: res.Usage.InputTokens, Out: res.Usage.OutputTokens, Estimated: res.Usage.Estimated})
	}
	if err != nil {
		return Result{}, err
	}
	out := Result{Summary: res.Summary, Files: res.Files, Warnings: skillWarnings}
	if snapOK {
		if after, ok := snapshotTree(x.root, maxSnapshotFiles); ok {
			out.Observed = diffSnapshots(before, after, res.Files)
		}
	}
	for _, sk := range res.Skipped {
		out.Warnings = append(out.Warnings, "not written: "+sk)
	}
	if role.ReadOnly && len(out.Observed) > 0 {
		out.Warnings = append(out.Warnings, "read-only role changed files: "+strings.Join(out.Observed, ", "))
	}
	if !role.ReadOnly && len(res.Files) == 0 && len(out.Observed) == 0 && len(res.Skipped) == 0 {
		out.Warnings = append(out.Warnings, "no files were written")
	}
	for _, w := range out.Warnings {
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + w + "\n"})
	}
	return out, nil
}

// userSkills loads the file-based skills of a role and returns them as a
// <skills> block plus one warning for each skill that could not be loaded.
func (x *executor) userSkills(names []string) (block string, warnings []string) {
	var sb strings.Builder
	for _, name := range names {
		var doc string
		var err error
		if config.ValidQualifiedName(name) {
			doc, err = plugins.LoadSkill(x.root, name)
		} else {
			doc, err = config.LoadUserSkill(x.root, name)
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			warnings = append(warnings, fmt.Sprintf("skill %q not found", name))
			continue
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("skill %q: %v", name, err))
			continue
		}
		if sb.Len() == 0 {
			sb.WriteString("<skills>\n")
		}
		fmt.Fprintf(&sb, "### %s\n%s\n", name, strings.TrimSpace(doc))
	}
	if sb.Len() > 0 {
		sb.WriteString("</skills>")
	}
	return sb.String(), warnings
}

// tailLines keeps the most recent (last) whole lines of s within max bytes; a
// single line over the cap is cut from its start.
func tailLines(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[len(s)-max:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 {
		return cut[i+1:]
	}
	for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
		cut = cut[1:]
	}
	return cut
}

// workContext is the project context plus the summaries of the node's
// dependencies.
func (x *executor) workContext(g *Graph, n *Node) string {
	var sb strings.Builder
	sb.WriteString(x.projectContext)
	if x.summary != nil {
		if sum := tailLines(strings.TrimSpace(x.summary()), maxSessionSummaryChars); sum != "" {
			sb.WriteString("\n\n## Earlier in this session\n" + sum + "\n")
		}
	}
	var deps []*Node
	for _, id := range n.DependsOn {
		if d := g.Node(id); d != nil {
			deps = append(deps, d)
		}
	}
	if len(deps) > 0 {
		sb.WriteString("\n\n## Completed dependencies\n")
		for _, d := range deps {
			fmt.Fprintf(&sb, "\n### %s: %s\n", d.ID, d.Title)
			if d.Summary != "" {
				sb.WriteString(d.Summary + "\n")
			}
			if all := d.AllFiles(); len(all) > 0 {
				sb.WriteString("Files: " + strings.Join(all, ", ") + "\n")
			}
		}
	}
	sb.WriteString(x.currentFiles(g, n))
	return sb.String()
}

// currentFiles renders the on-disk content of the files written by the
// node's transitive dependencies (for a fix node: by every done node), most
// recently written first, within the per-file and total budgets.
func (x *executor) currentFiles(g *Graph, n *Node) string {
	var src []*Node
	if isFixID(n.ID) {
		for _, o := range g.Nodes {
			if o != n && o.Status == StatusDone {
				src = append(src, o)
			}
		}
	} else {
		seen := map[string]bool{}
		var walk func(id string)
		walk = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			if d := g.Node(id); d != nil {
				src = append(src, d)
				for _, dd := range d.DependsOn {
					walk(dd)
				}
			}
		}
		for _, id := range n.DependsOn {
			walk(id)
		}
	}
	sort.SliceStable(src, func(i, j int) bool { return src[i].EndedAt.After(src[j].EndedAt) })

	var sb strings.Builder
	var left []string
	listed := map[string]bool{}
	total := 0
	for _, d := range src {
		for _, rel := range d.AllFiles() {
			if listed[rel] {
				continue
			}
			listed[rel] = true
			content, ok := x.readContextFile(rel)
			if !ok {
				continue
			}
			content = truncateMiddle(content, maxContextFileBytes)
			if total+len(content) > maxContextFilesBytes {
				left = append(left, rel)
				continue
			}
			total += len(content)
			if sb.Len() == 0 {
				sb.WriteString("\n\n## Current files\n\nThese are the latest versions on disk, including changes made by earlier agents.\n")
			}
			fence := "```"
			for strings.Contains(content, fence) {
				fence += "`"
			}
			fmt.Fprintf(&sb, "\n**%s**\n%s\n%s\n%s\n", rel, fence, strings.TrimRight(content, "\n"), fence)
		}
	}
	if len(left) > 0 {
		if sb.Len() == 0 {
			sb.WriteString("\n\n## Current files\n")
		}
		sb.WriteString("\nFiles not shown (size budget): " + strings.Join(left, ", ") + "\n")
	}
	return sb.String()
}

// readContextFile reads a project file for the context. Missing, non-regular,
// binary or escaping paths are skipped.
func (x *executor) readContextFile(rel string) (string, bool) {
	if !filepath.IsLocal(rel) {
		return "", false
	}
	p := filepath.Join(x.root, rel)
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(p) // #nosec G304 -- rel is checked with filepath.IsLocal and joined under the project root
	if err != nil || agent.IsBinaryContent(data) {
		return "", false
	}
	return string(data), true
}

// truncateMiddle keeps the start and end of s and replaces the middle with an
// "… (N bytes omitted) …" line when s is longer than max bytes.
func truncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := max / 2
	head, tail := s[:half], s[len(s)-half:]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return fmt.Sprintf("%s\n… (%d bytes omitted) …\n%s", head, len(s)-len(head)-len(tail), tail)
}

func (x *executor) runCheck(ctx context.Context, n *Node, emit func(Event)) (Result, error) {
	timeout := x.checkTimeout
	if timeout <= 0 {
		timeout = checkTimeout
	}
	run := executil.NewRunner(x.root).WithTimeout(timeout)
	commands := n.Commands
	if len(commands) == 0 && x.checkCommands != nil {
		commands = x.checkCommands()
	}
	if len(commands) == 0 {
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + noCheckCommandsMsg + "\n"})
		return Result{Summary: noCheckCommandsMsg, Warnings: []string{noCheckCommandsMsg}}, nil
	}
	for _, cmd := range commands {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "$ " + cmd + "\n"})
		res := run.RunContext(ctx, cmd)
		out := res.Stdout
		if res.Stderr != "" {
			if out != "" {
				out += "\n"
			}
			out += res.Stderr
		}
		if out != "" {
			emit(Event{NodeID: n.ID, Type: EventOutput, Text: out + "\n"})
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if res.ExitCode != 0 {
			if out == "" {
				out = fmt.Sprintf("exit code %d", res.ExitCode)
			}
			if len(out) > maxCheckErrorBytes {
				out = out[len(out)-maxCheckErrorBytes:]
			}
			return Result{}, fmt.Errorf("%s", out)
		}
	}
	return Result{Summary: "all checks passed"}, nil
}

// isFixID reports whether id looks like a fix node ID ("F" and digits).
func isFixID(id string) bool {
	return reservedKeyRe.MatchString(id) && id != "CHECK"
}

// maxSnapshotFiles caps the project tree snapshot; above it the detection of
// files changed by the runner itself is skipped for that run.
const maxSnapshotFiles = 20000

// snapSkipDirs are directories the snapshot does not enter.
var snapSkipDirs = map[string]bool{
	".git": true, ".decsr": true, "node_modules": true, "__pycache__": true, "build": true,
	"dist": true, "target": true, "bin": true, "obj": true, ".venv": true, "venv": true,
}

type fileStamp struct {
	size  int64
	mtime time.Time
}

// snapshotTree maps the slash-separated relative path of every regular file
// under root to its size and mtime. ok is false when more than limit files
// were found.
func snapshotTree(root string, limit int) (map[string]fileStamp, bool) {
	snap := map[string]fileStamp{}
	errCap := fmt.Errorf("snapshot cap")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && snapSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if len(snap) >= limit {
			return errCap
		}
		snap[filepath.ToSlash(rel)] = fileStamp{fi.Size(), fi.ModTime()}
		return nil
	})
	return snap, err == nil
}

// diffSnapshots returns the sorted paths that are new or changed in after and
// not in written.
func diffSnapshots(before, after map[string]fileStamp, written []string) []string {
	skip := make(map[string]bool, len(written))
	for _, w := range written {
		skip[filepath.ToSlash(filepath.Clean(w))] = true
	}
	var out []string
	for p, st := range after { // map keys are unique
		if skip[p] {
			continue
		}
		if old, ok := before[p]; ok && old.size == st.size && old.mtime.Equal(st.mtime) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
