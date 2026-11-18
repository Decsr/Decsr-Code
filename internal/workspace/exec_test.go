package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

type execFake struct {
	mu   sync.Mutex
	text string
	reqs []runner.CompletionRequest
}

func (f *execFake) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	ch := make(chan runner.Token, 3)
	ch <- runner.Token{Text: f.text}
	ch <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 11, OutputTokens: 5}}
	close(ch)
	return ch, nil
}

func newTestExec(t *testing.T, cfg config.Config, f *execFake, seen *[]config.AgentConfig) *executor {
	t.Helper()
	// Most tests feed file blocks, which is text mode; an empty runner is
	// opencode, which defaults to agent mode.
	agents := map[string]config.AgentConfig{}
	for k, v := range cfg.Agents {
		if v.Runner == "" {
			v.Runner = "claude"
		}
		agents[k] = v
	}
	cfg.Agents = agents
	return &executor{
		root:           t.TempDir(),
		cfg:            cfg,
		projectContext: "PROJCTX",
		newRunner: func(ac config.AgentConfig) (runner.LLMRunner, error) {
			if seen != nil {
				*seen = append(*seen, ac)
			}
			return f, nil
		},
	}
}

func collect(events *[]Event) func(Event) {
	return func(e Event) { *events = append(*events, e) }
}

func TestExecWorkUsesRoleRunner(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents["docs_writer"] = config.AgentConfig{Runner: "claude", Model: "m1", Description: "Writes friendly docs."}
	f := &execFake{text: "done\n===SUMMARY===\nok"}
	var seen []config.AgentConfig
	ex := newTestExec(t, cfg, f, &seen)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Docs", Description: "write", Kind: KindWork, Role: "docs_writer"}}}
	var evs []Event
	res, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs))
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].Runner != "claude" || seen[0].Model != "m1" {
		t.Fatalf("agent config = %+v", seen)
	}
	if !strings.Contains(f.reqs[0].SystemPrompt, "Writes friendly docs.") || !strings.Contains(f.reqs[0].SystemPrompt, "PROJCTX") {
		t.Errorf("prompt = %q", f.reqs[0].SystemPrompt)
	}
	if f.reqs[0].Model != "m1" {
		t.Errorf("model = %q", f.reqs[0].Model)
	}
	if res.Summary != "ok" {
		t.Errorf("summary = %q", res.Summary)
	}
	var out, usage bool
	for _, e := range evs {
		if e.Type == EventOutput {
			out = true
		}
		if e.Type == EventUsage && e.In == 11 && e.Out == 5 {
			usage = true
		}
	}
	if !out || !usage {
		t.Errorf("events = %+v", evs)
	}
}

func TestExecWorkBuiltinRoleUsesSkills(t *testing.T) {
	cfg := config.DefaultConfig()
	ac := cfg.Agents["coder"]
	ac.Skills = []string{"skill-a"}
	cfg.Agents["coder"] = ac
	f := &execFake{text: "x"}
	ex := newTestExec(t, cfg, f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	if _, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs[0].Skills) != 1 || f.reqs[0].Skills[0] != "skill-a" {
		t.Errorf("skills = %v", f.reqs[0].Skills)
	}
}

func TestExecWorkUnknownRole(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), &execFake{text: "x"}, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "ghost"}}}
	_, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err == nil || err.Error() != `role "ghost" is not defined` {
		t.Fatalf("err = %v", err)
	}
}

func TestExecWorkIncludesDependencySummaries(t *testing.T) {
	f := &execFake{text: "x"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "First", Kind: KindWork, Status: StatusDone, Summary: "made the thing", Files: []string{"a.go"}},
		{ID: "T2", Title: "Second", Kind: KindWork, Role: "coder", DependsOn: []string{"T1"}},
	}}
	if _, err := ex.Run(context.Background(), g, g.Nodes[1], func(Event) {}); err != nil {
		t.Fatal(err)
	}
	p := f.reqs[0].SystemPrompt
	for _, want := range []string{"Completed dependencies", "T1", "First", "made the thing", "a.go"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func checkNode(cmds ...string) (*Graph, *Node) {
	n := &Node{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Commands: cmds, ForceRun: true}
	return &Graph{Nodes: []*Node{n}}, n
}

func TestExecCheckPass(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	g, n := checkNode("true")
	var evs []Event
	res, err := ex.Run(context.Background(), g, n, collect(&evs))
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "all checks passed" {
		t.Errorf("summary = %q", res.Summary)
	}
	if len(evs) == 0 || evs[0].Text != "$ true\n" {
		t.Errorf("events = %+v", evs)
	}
}

func TestExecCheckFail(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	g, n := checkNode("sh -c 'echo boom; exit 3'")
	_, err := ex.Run(context.Background(), g, n, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecCheckStopsAtFirstFailure(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	g, n := checkNode("false", "echo second")
	var evs []Event
	_, err := ex.Run(context.Background(), g, n, collect(&evs))
	if err == nil {
		t.Fatal("want error")
	}
	for _, e := range evs {
		if strings.Contains(e.Text, "second") {
			t.Fatalf("second command ran: %+v", evs)
		}
	}
}

func TestExecCheckCancelled(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	g, n := checkNode("sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ex.Run(ctx, g, n, func(Event) {}); err == nil {
		t.Fatal("want error")
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func doneNode(id string, ended time.Time, files ...string) *Node {
	return &Node{ID: id, Title: id, Kind: KindWork, Status: StatusDone, Files: files, EndedAt: ended}
}

func TestExecCurrentFilesFromTransitiveDeps(t *testing.T) {
	f := &execFake{text: "x"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	now := time.Now()
	writeFile(t, ex.root, "a.go", "package a // LIVE-A\n")
	writeFile(t, ex.root, "b.go", "package b // LIVE-B\n")
	writeFile(t, ex.root, "unrelated.go", "package u\n")
	writeFile(t, ex.root, "bin.dat", "ab\x00cd")
	g := &Graph{Nodes: []*Node{
		doneNode("T1", now.Add(-2*time.Minute), "a.go", "bin.dat", "gone.go"),
		doneNode("T2", now.Add(-1*time.Minute), "b.go"),
		doneNode("T9", now, "unrelated.go"),
		{ID: "T3", Title: "Three", Kind: KindWork, Role: "coder", DependsOn: []string{"T2"}},
	}}
	g.Node("T2").DependsOn = []string{"T1"}
	if _, err := ex.Run(context.Background(), g, g.Node("T3"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	p := f.reqs[0].SystemPrompt
	if !strings.Contains(p, "## Current files") || !strings.Contains(p, "**a.go**\n```\npackage a // LIVE-A\n```") || !strings.Contains(p, "LIVE-B") {
		t.Errorf("current files missing:\n%s", p)
	}
	if strings.Contains(p, "unrelated.go") || strings.Contains(p, "bin.dat\n```") || strings.Contains(p, "**gone.go**") {
		t.Errorf("unexpected files in prompt")
	}
	if strings.Index(p, "**b.go**") > strings.Index(p, "**a.go**") {
		t.Errorf("most recently written dependency must come first")
	}
}

func TestExecCurrentFilesReadsLiveContent(t *testing.T) {
	f := &execFake{text: "x"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	writeFile(t, ex.root, "a.go", "v1\n")
	g := &Graph{Nodes: []*Node{doneNode("T1", time.Now(), "a.go"), {ID: "T2", Kind: KindWork, Role: "coder", DependsOn: []string{"T1"}}}}
	writeFile(t, ex.root, "a.go", "v2-after-planning\n")
	if _, err := ex.Run(context.Background(), g, g.Node("T2"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].SystemPrompt, "v2-after-planning") {
		t.Error("content not read at run time")
	}
}

func TestExecCurrentFilesFixNodeSeesAllDone(t *testing.T) {
	f := &execFake{text: "x"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	writeFile(t, ex.root, "x.go", "package x // FIXME\n")
	g := &Graph{Nodes: []*Node{
		doneNode("T1", time.Now(), "x.go"),
		{ID: "F1", Title: "Fix", Kind: KindWork, Role: "coder_fixer"},
	}}
	if _, err := ex.Run(context.Background(), g, g.Node("F1"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].SystemPrompt, "package x // FIXME") {
		t.Errorf("fix node did not get files of done nodes")
	}
}

func TestExecCurrentFilesBudgets(t *testing.T) {
	f := &execFake{text: "x"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	big := strings.Repeat("0123456789", 5000) // 50000 bytes
	writeFile(t, ex.root, "big.txt", "HEAD"+big+"TAIL")
	now := time.Now()
	var files []string
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		writeFile(t, ex.root, name, strings.Repeat("x", 20000))
		files = append(files, name)
	}
	g := &Graph{Nodes: []*Node{
		doneNode("T1", now.Add(-time.Minute), files...),
		doneNode("T2", now, "big.txt"),
		{ID: "T3", Kind: KindWork, Role: "coder", DependsOn: []string{"T1", "T2"}},
	}}
	if _, err := ex.Run(context.Background(), g, g.Node("T3"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	p := f.reqs[0].SystemPrompt
	if !strings.Contains(p, "HEAD") || !strings.Contains(p, "TAIL") || !strings.Contains(p, "bytes omitted) …") {
		t.Errorf("big file not truncated in the middle")
	}
	i := strings.Index(p, "## Current files")
	section := p[i:]
	if len(section) > 125*1024 {
		t.Errorf("section is %d bytes, over the budget", len(section))
	}
	if !strings.Contains(section, "not shown") || !strings.Contains(section, "f7.txt") {
		t.Errorf("left-out files not listed:\n%s", section[len(section)-300:])
	}
}

func TestExecWarnsOnSkippedWrites(t *testing.T) {
	f := &execFake{text: "**ok.txt**\n```\nhi\n```\n\n**.git/config**\n```\nx\n```\n===SUMMARY===\ndone"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	var evs []Event
	res, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "not written: .git/config: ") || !strings.Contains(res.Warnings[0], "protected path") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
	found := false
	for _, e := range evs {
		if e.Type == EventOutput && e.Text == "⚠ "+res.Warnings[0]+"\n" {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning output line: %+v", evs)
	}
}

func TestExecWarnsWhenNoFilesWritten(t *testing.T) {
	f := &execFake{text: "just prose\n===SUMMARY===\nnothing"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "no files were written" {
		t.Errorf("warnings = %v", res.Warnings)
	}
	// A read-only role is expected to write nothing.
	g.Nodes[0].Role = "security"
	res, err = ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil || len(res.Warnings) != 0 {
		t.Errorf("read-only: res=%+v err=%v", res, err)
	}
}

func TestExecReadOnlyRoleWritesNothing(t *testing.T) {
	f := &execFake{text: "**a.txt**\n```\nhi\n```\n===SUMMARY===\nreport"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "ux_reviewer"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(ex.root, "a.txt")); serr == nil {
		t.Error("read-only role wrote a file")
	}
	if len(res.Files) != 0 || res.Summary != "report" {
		t.Errorf("res = %+v", res)
	}
}

func TestExecReadOnlyTaskWritesNothingWhateverTheRole(t *testing.T) {
	f := &execFake{text: "**a.txt**\n```\nhi\n```\n===SUMMARY===\nreport"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder", ReadOnlyTask: true}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(ex.root, "a.txt")); serr == nil {
		t.Error("a read-only task wrote a file")
	}
	if len(res.Files) != 0 || len(res.Warnings) != 0 {
		t.Errorf("res = %+v", res)
	}
}

func TestExecCheckTimeout(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	ex.checkTimeout = 200 * time.Millisecond
	g, n := checkNode("sleep 5")
	start := time.Now()
	_, err := ex.Run(context.Background(), g, n, func(Event) {})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("timeout not applied")
	}
}

func TestExecCheckLazyCommands(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	calls := 0
	ex.checkCommands = func() []string { calls++; return []string{"echo lazy"} }
	g, n := checkNode()
	var evs []Event
	res, err := ex.Run(context.Background(), g, n, collect(&evs))
	if err != nil || res.Summary != "all checks passed" || calls != 1 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, calls)
	}
	if evs[0].Text != "$ echo lazy\n" {
		t.Errorf("events = %+v", evs)
	}
	// Static commands win and the func is not called.
	g, n = checkNode("true")
	if _, err := ex.Run(context.Background(), g, n, func(Event) {}); err != nil || calls != 1 {
		t.Errorf("err=%v calls=%d", err, calls)
	}
}

func TestExecCheckNoCommandsKnown(t *testing.T) {
	const msg = "no build or test command is known for this project yet (set project.test_cmd or pick a language in Project Setup)"
	for _, fn := range []func() []string{nil, func() []string { return nil }} {
		ex := newTestExec(t, config.DefaultConfig(), nil, nil)
		ex.checkCommands = fn
		g, n := checkNode()
		var evs []Event
		res, err := ex.Run(context.Background(), g, n, collect(&evs))
		if err != nil || res.Summary != msg || len(res.Warnings) != 1 || res.Warnings[0] != msg {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	}
}

func TestNewExecutorWithChecks(t *testing.T) {
	ex := NewExecutorWithChecks(t.TempDir(), config.DefaultConfig(), "ctx", func() []string { return []string{"true"} })
	g, n := checkNode()
	if _, err := ex.Run(context.Background(), g, n, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if NewExecutor(t.TempDir(), config.DefaultConfig(), "ctx") == nil {
		t.Fatal("NewExecutor broke")
	}
}

// diskWriter is a runner that edits files in root by itself, like an agentic
// CLI, and answers with text.
type diskWriter struct {
	root  string
	files map[string]string
	text  string
}

func (d *diskWriter) Complete(_ context.Context, _ runner.CompletionRequest) (<-chan runner.Token, error) {
	for p, c := range d.files {
		full := filepath.Join(d.root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			return nil, err
		}
	}
	ch := make(chan runner.Token, 2)
	ch <- runner.Token{Text: d.text}
	ch <- runner.Token{Done: true}
	close(ch)
	return ch, nil
}

func newDiskExec(t *testing.T, files map[string]string, text string) *executor {
	t.Helper()
	ex := newTestExec(t, config.DefaultConfig(), &execFake{}, nil)
	ex.newRunner = func(config.AgentConfig) (runner.LLMRunner, error) {
		return &diskWriter{root: ex.root, files: files, text: text}, nil
	}
	return ex
}

func TestExecObservesFilesChangedByTheRunner(t *testing.T) {
	ex := newDiskExec(t, map[string]string{"todo.py": "print(1)\n", "pkg/x.py": "x\n", "node_modules/m/i.js": "m", ".decsr/s": "s", "__pycache__/c.pyc": "c"},
		"**explicit.txt**\n```\nhi\n```\n===SUMMARY===\nok")
	writeFile(t, ex.root, "old.txt", "unchanged")
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Files, ",") != "explicit.txt" {
		t.Errorf("files = %v", res.Files)
	}
	if strings.Join(res.Observed, ",") != "pkg/x.py,todo.py" {
		t.Errorf("observed = %v", res.Observed)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestExecNoFilesWarningOnlyWhenBothEmpty(t *testing.T) {
	ex := newDiskExec(t, map[string]string{"todo.py": "x\n"}, "done\n===SUMMARY===\nok")
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 || len(res.Observed) != 1 {
		t.Errorf("res = %+v", res)
	}
}

func TestExecReadOnlyRoleChangingFilesWarns(t *testing.T) {
	ex := newDiskExec(t, map[string]string{"b.txt": "1", "a.txt": "2"}, "report\n===SUMMARY===\nr")
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "ux_reviewer"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "read-only role changed files: a.txt, b.txt" {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestExecObservedChangedExistingFile(t *testing.T) {
	ex := newDiskExec(t, map[string]string{"a.txt": "longer content now"}, "x\n===SUMMARY===\nok")
	writeFile(t, ex.root, "a.txt", "v1")
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil || strings.Join(res.Observed, ",") != "a.txt" {
		t.Errorf("res = %+v err=%v", res, err)
	}
}

func TestExecCurrentFilesIncludesObserved(t *testing.T) {
	f := &execFake{text: "x"}
	ex := newTestExec(t, config.DefaultConfig(), f, nil)
	writeFile(t, ex.root, "seen.py", "print('OBSERVED')\n")
	writeFile(t, ex.root, "other.py", "print('FIXALL')\n")
	d := doneNode("T1", time.Now())
	d.Observed = []string{"seen.py"}
	d2 := doneNode("T2", time.Now())
	d2.Observed = []string{"other.py"}
	g := &Graph{Nodes: []*Node{d, d2, {ID: "T3", Title: "x", Kind: KindWork, Role: "coder", DependsOn: []string{"T1"}},
		{ID: "F1", Title: "fix", Kind: KindWork, Role: "coder"}}}
	if _, err := ex.Run(context.Background(), g, g.Node("T3"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[0].SystemPrompt, "OBSERVED") || !strings.Contains(f.reqs[0].SystemPrompt, "Files: seen.py") {
		t.Errorf("observed file missing:\n%s", f.reqs[0].SystemPrompt)
	}
	if _, err := ex.Run(context.Background(), g, g.Node("F1"), func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.reqs[1].SystemPrompt, "FIXALL") {
		t.Errorf("fix node lacks observed file")
	}
}

func TestSnapshotCapSkipsDetection(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		writeFile(t, root, fmt.Sprintf("f%d.txt", i), "x")
	}
	if _, ok := snapshotTree(root, 3); ok {
		t.Error("cap not reported")
	}
	if s, ok := snapshotTree(root, 10); !ok || len(s) != 5 {
		t.Errorf("snapshot = %v %v", s, ok)
	}
}

func writeSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	d := filepath.Join(dir, ".decsr", "skills")
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecWorkIncludesUserSkills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents["docs_writer"] = config.AgentConfig{Description: "Writes friendly docs.", UserSkills: []string{"style", "tone"}}
	cfg.Agents["coder"] = config.AgentConfig{Skills: []string{"coder"}, UserSkills: []string{"style"}}
	f := &execFake{text: "done\n===SUMMARY===\nok"}
	ex := newTestExec(t, cfg, f, nil)
	writeSkill(t, ex.root, "style", "STYLE-DOC")
	writeSkill(t, ex.root, "tone", "TONE-DOC")
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "Docs", Kind: KindWork, Role: "docs_writer"},
		{ID: "T2", Title: "Code", Kind: KindWork, Role: "coder"},
	}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	p := f.reqs[0].SystemPrompt
	for _, want := range []string{"Writes friendly docs.", "<skills>", "STYLE-DOC", "TONE-DOC", "</skills>"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "skill") {
			t.Errorf("unexpected warning %q", w)
		}
	}
	// A built-in role gets its user skills too, next to the embedded skills.
	if _, err := ex.Run(context.Background(), g, g.Nodes[1], func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if p := f.reqs[1].SystemPrompt; !strings.Contains(p, "STYLE-DOC") {
		t.Errorf("builtin prompt missing user skill")
	}
	if got := f.reqs[1].Skills; len(got) != 1 || got[0] != "coder" {
		t.Errorf("embedded skills = %v", got)
	}
}

func TestExecWorkMissingUserSkillWarns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents["docs_writer"] = config.AgentConfig{Description: "Writes docs.", UserSkills: []string{"ghost", "style"}}
	f := &execFake{text: "done\n===SUMMARY===\nok"}
	ex := newTestExec(t, cfg, f, nil)
	writeSkill(t, ex.root, "style", "STYLE-DOC")
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Docs", Kind: KindWork, Role: "docs_writer"}}}
	var evs []Event
	res, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range res.Warnings {
		if w == `skill "ghost" not found` {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v", res.Warnings)
	}
	warned := false
	for _, e := range evs {
		if e.Type == EventOutput && strings.Contains(e.Text, `skill "ghost" not found`) {
			warned = true
		}
	}
	if !warned {
		t.Error("warning not emitted as output")
	}
	if !strings.Contains(f.reqs[0].SystemPrompt, "STYLE-DOC") {
		t.Error("remaining skill missing from prompt")
	}
}

func TestRolesCarryUserSkills(t *testing.T) {
	cfg := config.DefaultConfig()
	ac := cfg.Agents["qa"]
	ac.UserSkills = []string{"review"}
	cfg.Agents["qa"] = ac
	cfg.Agents["custom"] = config.AgentConfig{UserSkills: []string{"a"}}
	for _, name := range []string{"qa", "custom"} {
		r, ok := FindRole(cfg, name)
		if !ok || len(r.UserSkills) != 1 {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func activitiesOf(evs []Event) []runner.Activity {
	var out []runner.Activity
	for _, e := range evs {
		if e.Type == EventActivity && e.Activity != nil {
			out = append(out, *e.Activity)
		}
	}
	return out
}

func TestExecCheckCommandActivities(t *testing.T) {
	ex := newTestExec(t, config.DefaultConfig(), nil, nil)
	g, n := checkNode("true", "sh -c 'echo boom; exit 3'", "echo never")
	var evs []Event
	if _, err := ex.Run(context.Background(), g, n, collect(&evs)); err == nil {
		t.Fatal("want error")
	}
	acts := activitiesOf(evs)
	if len(acts) != 4 {
		t.Fatalf("want 4 activities (running+done, running+failed), got %+v", acts)
	}
	for _, a := range acts {
		if a.Kind != runner.ActCommand {
			t.Errorf("kind = %q", a.Kind)
		}
	}
	if acts[0].ID != acts[1].ID || acts[2].ID != acts[3].ID || acts[0].ID == acts[2].ID {
		t.Errorf("IDs must pair running with its result: %q %q %q %q", acts[0].ID, acts[1].ID, acts[2].ID, acts[3].ID)
	}
	if acts[0].State != runner.ActRunning || acts[0].Title != "true" || acts[0].ExitCode != nil {
		t.Errorf("first = %+v", acts[0])
	}
	if acts[1].State != runner.ActDone || acts[1].ExitCode == nil || *acts[1].ExitCode != 0 {
		t.Errorf("second = %+v", acts[1])
	}
	if acts[3].State != runner.ActFailed || acts[3].ExitCode == nil || *acts[3].ExitCode != 3 || !strings.Contains(acts[3].Detail, "boom") {
		t.Errorf("fourth = %+v", acts[3])
	}
	if acts[1].Duration <= 0 || acts[3].Duration <= 0 {
		t.Errorf("durations = %v %v", acts[1].Duration, acts[3].Duration)
	}
	// The existing output events are unchanged and still come first.
	if evs[0].Type != EventOutput || evs[0].Text != "$ true\n" {
		t.Errorf("first event = %+v", evs[0])
	}
	for _, e := range evs {
		if e.NodeID != "CHECK" {
			t.Errorf("event without node ID: %+v", e)
		}
	}
}

type activityFake struct{}

func (activityFake) Complete(_ context.Context, _ runner.CompletionRequest) (<-chan runner.Token, error) {
	ch := make(chan runner.Token, 4)
	ch <- runner.Token{Activity: &runner.Activity{ID: "t1", Kind: runner.ActThinking, State: runner.ActDone, Title: "Thinking"}}
	ch <- runner.Token{Text: "**a.txt**\n```\nhello\n```\n===SUMMARY===\nok\n"}
	ch <- runner.Token{Done: true}
	close(ch)
	return ch, nil
}

func TestExecWorkEmitsActivityEvents(t *testing.T) {
	cfg := config.DefaultConfig()
	ex := newTestExec(t, cfg, nil, nil)
	ex.newRunner = func(config.AgentConfig) (runner.LLMRunner, error) { return activityFake{}, nil }
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Do", Kind: KindWork, Role: "coder"}}}
	var evs []Event
	if _, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs)); err != nil {
		t.Fatal(err)
	}
	acts := activitiesOf(evs)
	var kinds []string
	for _, a := range acts {
		kinds = append(kinds, fmt.Sprintf("%s/%s", a.Kind, a.State))
	}
	want := "thinking/done write/running write/done"
	if strings.Join(kinds, " ") != want {
		t.Errorf("activities = %v, want %s", kinds, want)
	}
	if acts[2].Added != 1 || acts[2].Title != "a.txt" {
		t.Errorf("write = %+v", acts[2])
	}
	for _, e := range evs {
		if e.Type == EventActivity && e.NodeID != "T1" {
			t.Errorf("activity event without node ID: %+v", e)
		}
	}
}

func TestExecWorkEmitsContext(t *testing.T) {
	cfg := config.DefaultConfig()
	ac := cfg.Agents["coder"]
	ac.MaxContextTokens = 32000
	cfg.Agents["coder"] = ac
	f := &execFake{text: "done\n===SUMMARY===\nok"}
	ex := newTestExec(t, cfg, f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Do", Description: "thing", Kind: KindWork, Role: "coder"}}}
	var evs []Event
	if _, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs)); err != nil {
		t.Fatal(err)
	}
	var ctxEvs []Event
	for _, e := range evs {
		if e.Type == EventContext {
			ctxEvs = append(ctxEvs, e)
		}
	}
	if len(ctxEvs) != 1 {
		t.Fatalf("context events = %d", len(ctxEvs))
	}
	req := f.reqs[0]
	want := tokenutil.EstimateTokens(req.SystemPrompt)
	for _, m := range req.Messages {
		want += tokenutil.EstimateTokens(m.Content)
	}
	if ctxEvs[0].ContextTokens != want || want == 0 || ctxEvs[0].ContextLimit != 32000 || ctxEvs[0].NodeID != "T1" {
		t.Errorf("event = %+v, want tokens %d limit 32000", ctxEvs[0], want)
	}
	for _, e := range evs {
		if e.Type == EventOutput {
			t.Fatal("run output came before the context event")
		}
		if e.Type == EventContext {
			break
		}
	}
}

func TestExecCheckEmitsNoContext(t *testing.T) {
	ex := &executor{root: t.TempDir(), cfg: config.DefaultConfig()}
	g := &Graph{Nodes: []*Node{{ID: "CHECK", Kind: KindCheck, Commands: []string{"true"}}}}
	var evs []Event
	if _, err := ex.Run(context.Background(), g, g.Nodes[0], collect(&evs)); err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == EventContext {
			t.Errorf("unexpected context event %+v", e)
		}
	}
}

func TestExecAgentModeObservesFilesAndIgnoresBlocks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents["coder"] = config.AgentConfig{Runner: "claude", Mode: config.ModeAgent}
	ex := newDiskExec(t, map[string]string{"made.go": "package x\n"}, "**ignored.txt**\n```\nhi\n```\n===SUMMARY===\nok")
	ex.cfg = cfg
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 0 || strings.Join(res.Observed, ",") != "made.go" {
		t.Errorf("files = %v, observed = %v", res.Files, res.Observed)
	}
	if _, err := os.Stat(filepath.Join(ex.root, "ignored.txt")); err == nil {
		t.Error("file block was written in agent mode")
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "no files were written") {
			t.Errorf("unexpected warning %q", w)
		}
	}
}

func TestExecAgentModeNoChangesWarns(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents["coder"] = config.AgentConfig{Runner: "claude", Mode: config.ModeAgent}
	ex := newTestExec(t, cfg, &execFake{text: "nothing to do\n===SUMMARY===\nok"}, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
	res, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "no files were written" {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestExecRejectsUnsupportedMode(t *testing.T) {
	for _, tc := range []struct {
		ac   config.AgentConfig
		want string
	}{
		{config.AgentConfig{Runner: "ollama", Mode: config.ModeAgent}, "agent mode needs claude, opencode, codex or dsh"},
		{config.AgentConfig{Runner: "opencode", Mode: config.ModeText}, "text mode is not supported for opencode"},
	} {
		cfg := config.DefaultConfig()
		cfg.Agents["coder"] = tc.ac
		f := &execFake{text: "x"}
		ex := newTestExec(t, cfg, f, nil)
		g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder"}}}
		_, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.ac, err, tc.want)
		}
		if len(f.reqs) != 0 {
			t.Errorf("%+v: the runner must not start", tc.ac)
		}
	}
}

func TestExecAgentModeAddsTheNodesExtraAllowedCommands(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents["coder"] = config.AgentConfig{Runner: "claude", Mode: config.ModeAgent}
	cfg.Project.AllowedCommands = []string{"make"}
	f := &execFake{text: "nothing\n===SUMMARY===\nok"}
	ex := newTestExec(t, cfg, f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "t", Kind: KindWork, Role: "coder", ExtraAllowed: []string{"cmake"}}}}
	if _, err := ex.Run(context.Background(), g, g.Nodes[0], func(Event) {}); err != nil {
		t.Fatal(err)
	}
	tools := strings.Join(f.reqs[0].AllowedTools, " ")
	if !strings.Contains(tools, "Bash(cmake:*)") || !strings.Contains(tools, "Bash(make:*)") {
		t.Errorf("allowed tools = %s", tools)
	}
	if len(ex.cfg.Project.AllowedCommands) != 1 {
		t.Errorf("the project list was changed: %v", ex.cfg.Project.AllowedCommands)
	}
}
