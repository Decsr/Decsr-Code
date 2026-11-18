package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

type leadStep struct {
	text, session string
	err           error
	acts          []runner.Activity
	usage         runner.TokenUsage
}

// leadFake answers each call with the next scripted step.
type leadFake struct {
	steps []leadStep
	reqs  []runner.CompletionRequest
}

func (f *leadFake) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	f.reqs = append(f.reqs, req)
	st := f.steps[min(len(f.reqs)-1, len(f.steps)-1)]
	ch := make(chan runner.Token, 16)
	if st.session != "" {
		ch <- runner.Token{SessionID: st.session}
	}
	for i := range st.acts {
		ch <- runner.Token{Activity: &st.acts[i]}
	}
	if st.err != nil {
		ch <- runner.Token{Error: st.err}
		close(ch)
		return ch, nil
	}
	ch <- runner.Token{Text: st.text}
	u := st.usage
	ch <- runner.Token{Done: true, Usage: &u}
	close(ch)
	return ch, nil
}

func claudeTurn(task string, conv LeadConv) LeadTurn {
	return LeadTurn{Task: task, Roles: testRoles(), Runner: "claude", Model: "opus", Dir: "/proj", Tools: true, Conv: conv,
		Summary: "SUMMARY-TEXT", Context: "MAP-TEXT"}
}

func TestLeadPlainAnswerCreatesNothingAndNeverRetries(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "It is a Go CLI.", session: "s1"}}}
	res, err := RunLead(t.Context(), f, claudeTurn("what is this?", LeadConv{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Graph.Answer != "It is a Go CLI." || len(res.Graph.Nodes) != 0 || len(f.reqs) != 1 {
		t.Errorf("graph %+v, calls %d", res.Graph, len(f.reqs))
	}
}

func TestLeadAnswerWithNodes(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "On it.\n\n" + happy, session: "s1"}}}
	res, err := RunLead(t.Context(), f, claudeTurn("add health", LeadConv{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Graph.Chat != "On it." || len(res.Graph.Nodes) != 3 || res.Graph.Answer != "" {
		t.Errorf("graph = %+v", res.Graph)
	}
}

func TestLeadLegacyAnswerMarker(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "===ANSWER===\nOld style.\n===END==="}}}
	res, err := RunLead(t.Context(), f, claudeTurn("q", LeadConv{}), nil)
	if err != nil || res.Graph.Answer != "Old style." {
		t.Fatalf("%v %+v", err, res.Graph)
	}
}

func TestLeadChangesStillParse(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "Dropping it.\n===TASKS===\n[]\n===CHANGES===\n[{\"node\":\"T3\",\"action\":\"cancel\",\"reason\":\"not needed\"}]\n===END==="}}}
	turn := claudeTurn("drop flags", LeadConv{})
	turn.Existing = []string{"T1", "T2", "T3", "T4"}
	turn.Opts = SplitOptions{Live: liveFixture()}
	res, err := RunLead(t.Context(), f, turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Graph.Changes) != 1 || res.Graph.Changes[0].Node != "T3" || res.Graph.Chat != "Dropping it." {
		t.Errorf("graph = %+v", res.Graph)
	}
}

func TestLeadFirstTurnKeepsSessionSecondResumes(t *testing.T) {
	f := &leadFake{steps: []leadStep{
		{text: "one", session: "sess-1", usage: runner.TokenUsage{InputTokens: 100, OutputTokens: 5, ContextTokens: 60}},
		{text: "two", session: "sess-2"},
	}}
	first, err := RunLead(t.Context(), f, claudeTurn("hello", LeadConv{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	r1 := f.reqs[0]
	if !r1.KeepSession || r1.ResumeID != "" || !r1.Agent || !r1.ReadOnly || r1.Dir != "/proj" {
		t.Errorf("first request = %+v", r1)
	}
	if first.Conv != (LeadConv{SessionID: "sess-1", Runner: "claude", Model: "opus"}) || first.Context != 60 {
		t.Errorf("conv %+v context %d", first.Conv, first.Context)
	}
	m1 := r1.Messages[0].Content
	for _, want := range []string{"hello", "## Project map\nMAP-TEXT", "## Summary of the earlier conversation\nSUMMARY-TEXT"} {
		if !strings.Contains(m1, want) {
			t.Errorf("first message lacks %q:\n%s", want, m1)
		}
	}

	turn := claudeTurn("and now?", first.Conv)
	turn.Existing, turn.Next = []string{"T1"}, 2
	turn.Opts.Live = []LiveNode{{ID: "T1", Title: "A", Status: "running", Role: "coder"}}
	second, err := RunLead(t.Context(), f, turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2 := f.reqs[1]
	if r2.ResumeID != "sess-1" || !r2.KeepSession || len(r2.Messages) != 1 {
		t.Errorf("second request = %+v", r2)
	}
	m2 := r2.Messages[0].Content
	if !strings.HasPrefix(m2, "and now?") || !strings.Contains(m2, `T1 "A" [running]`) || !strings.Contains(m2, "Next free id: T2") {
		t.Errorf("resumed message:\n%s", m2)
	}
	if strings.Contains(m2, "MAP-TEXT") || strings.Contains(m2, "SUMMARY-TEXT") {
		t.Errorf("a resumed message must not repeat the map or the summary:\n%s", m2)
	}
	if r1.SystemPrompt != r2.SystemPrompt {
		t.Error("the system prompt must be the same on every turn")
	}
	if second.Conv.SessionID != "sess-2" || second.ResumeFailed {
		t.Errorf("second = %+v", second)
	}
}

func TestLeadResumeErrorStartsFreshWithSummary(t *testing.T) {
	f := &leadFake{steps: []leadStep{
		{err: &runner.ResumeError{Err: errors.New("no conversation found")}},
		{text: "fresh answer", session: "new-1"},
	}}
	res, err := RunLead(t.Context(), f, claudeTurn("go on", LeadConv{SessionID: "old", Runner: "claude", Model: "opus"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ResumeFailed || res.Conv.SessionID != "new-1" || res.Graph.Answer != "fresh answer" {
		t.Errorf("res = %+v", res)
	}
	if f.reqs[0].ResumeID != "old" || f.reqs[1].ResumeID != "" || !f.reqs[1].KeepSession {
		t.Errorf("requests = %+v / %+v", f.reqs[0], f.reqs[1])
	}
	if m := f.reqs[1].Messages[0].Content; !strings.Contains(m, "SUMMARY-TEXT") || !strings.Contains(m, "MAP-TEXT") {
		t.Errorf("the fresh conversation lacks the summary or the map:\n%s", m)
	}
}

func TestLeadOtherErrorIsNotAResumeFailure(t *testing.T) {
	f := &leadFake{steps: []leadStep{{err: errors.New("boom")}}}
	res, err := RunLead(t.Context(), f, claudeTurn("go on", LeadConv{SessionID: "old", Runner: "claude", Model: "opus"}), nil)
	if err == nil || res.ResumeFailed || len(f.reqs) != 1 {
		t.Errorf("err=%v res=%+v calls=%d", err, res, len(f.reqs))
	}
}

func TestLeadAgentChangeStartsNewConversation(t *testing.T) {
	for name, conv := range map[string]LeadConv{
		"runner": {SessionID: "old", Runner: "codex", Model: "opus"},
		"model":  {SessionID: "old", Runner: "claude", Model: "sonnet"},
	} {
		f := &leadFake{steps: []leadStep{{text: "ok", session: "new"}}}
		res, err := RunLead(t.Context(), f, claudeTurn("hi", conv), nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.AgentChanged || f.reqs[0].ResumeID != "" || res.Conv.SessionID != "new" || res.Conv.Model != "opus" {
			t.Errorf("%s: res=%+v req=%+v", name, res, f.reqs[0])
		}
		if !strings.Contains(f.reqs[0].Messages[0].Content, "SUMMARY-TEXT") {
			t.Errorf("%s: the new conversation lacks the summary", name)
		}
	}
}

func TestLeadReadOnlyTools(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "ok"}}}
	if _, err := RunLead(t.Context(), f, claudeTurn("hi", LeadConv{}), nil); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.reqs[0].AllowedTools, ",")
	want := "Read,Grep,Glob,Bash(git log:*),Bash(git diff:*),Bash(git status:*),Bash(git show:*)," +
		"Bash(claude plugin list:*),Bash(claude plugin marketplace list:*),Bash(claude mcp list:*)"
	if got != want {
		t.Errorf("tools = %s", got)
	}
	for _, bad := range []string{"Write", "Edit", "mcp__", "Web", "go test", "install", "add"} {
		if strings.Contains(got, bad) {
			t.Errorf("tool list has %s", bad)
		}
	}
	if f.reqs[0].MCP {
		t.Error("the lead runs without MCP servers")
	}
}

func TestLeadSandboxOfOtherRunners(t *testing.T) {
	for runnerName, agent := range map[string]bool{"codex": false, "dsh": true, "opencode": true} {
		f := &leadFake{steps: []leadStep{{text: "ok", session: "s"}}}
		turn := claudeTurn("hi", LeadConv{})
		turn.Runner = runnerName
		if _, err := RunLead(t.Context(), f, turn, nil); err != nil {
			t.Fatal(err)
		}
		r := f.reqs[0]
		if r.Agent != agent || !r.ReadOnly || !r.KeepSession {
			t.Errorf("%s: %+v", runnerName, r)
		}
	}
}

func TestLeadWithoutToolsIsOneShot(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "a", session: "ignored"}, {text: "b"}}}
	turn := claudeTurn("hi", LeadConv{})
	turn.Runner, turn.Tools = "deepseek", false
	res, err := RunLead(t.Context(), f, turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := f.reqs[0]
	if r.Agent || r.ReadOnly || r.KeepSession || r.ResumeID != "" || len(r.AllowedTools) != 0 || res.Conv.SessionID != "" {
		t.Errorf("request = %+v conv %+v", r, res.Conv)
	}
	if !strings.Contains(r.Messages[0].Content, "## Project context\nMAP-TEXT") {
		t.Errorf("no project context:\n%s", r.Messages[0].Content)
	}
	// The next turn carries the context and the summary again: nothing is resumed.
	turn.Conv = res.Conv
	if _, err := RunLead(t.Context(), f, turn, nil); err != nil {
		t.Fatal(err)
	}
	if m := f.reqs[1].Messages[0].Content; f.reqs[1].ResumeID != "" || !strings.Contains(m, "SUMMARY-TEXT") {
		t.Errorf("second one-shot turn: %+v", f.reqs[1])
	}
}

func TestLeadRetryOnlyWhenNodesHaveNoRoles(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: noRoles, session: "s1"}, {text: happy, session: "s2"}}}
	res, err := RunLead(t.Context(), f, claudeTurn("do it", LeadConv{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 2 || f.reqs[1].ResumeID != "s1" || f.reqs[1].Messages[0].Content != splitRetryMsg {
		t.Fatalf("calls = %d, retry = %+v", len(f.reqs), f.reqs[len(f.reqs)-1])
	}
	if len(res.Graph.Nodes) != 3 || res.Conv.SessionID != "s2" {
		t.Errorf("res = %+v", res)
	}
}

func TestLeadUsageAndActivity(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "done", session: "s", usage: runner.TokenUsage{InputTokens: 900, OutputTokens: 30},
		acts: []runner.Activity{
			{ID: "a", Kind: runner.ActRead, State: runner.ActRunning, Title: "/proj/internal/x.go"},
			{ID: "a", Kind: runner.ActRead, State: runner.ActDone, Title: "/proj/internal/x.go"},
			{ID: "b", Kind: runner.ActSearch, State: runner.ActRunning, Title: "RateLimit"},
		}}}}
	act := &LeadActivity{Root: "/proj"}
	turn := claudeTurn("hi", LeadConv{})
	turn.Activity = act
	res, err := RunLead(t.Context(), f, turn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens != 900 || res.Context != 900 {
		t.Errorf("usage %+v context %d", res.Usage, res.Context)
	}
	if got := act.Line(); got != `lead · searching "RateLimit"` {
		t.Errorf("line = %q", got)
	}
	if got := act.Summary(); got != "read 1 file · 1 search" {
		t.Errorf("summary = %q", got)
	}
}

func TestLeadActivityLines(t *testing.T) {
	a := &LeadActivity{Root: "/proj/"}
	if a.Line() != "" || a.Summary() != "" {
		t.Error("an idle lead has no line")
	}
	a.Note(runner.Activity{ID: "1", Kind: runner.ActRead, Title: "/proj/internal/runner/claude.go"})
	if got := a.Line(); got != "lead · reading internal/runner/claude.go" {
		t.Errorf("line = %q", got)
	}
	a.Note(runner.Activity{ID: "2", Kind: runner.ActCommand, Title: "git log"})
	if got := a.Line(); got != "lead · git log" {
		t.Errorf("line = %q", got)
	}
	a.Note(runner.Activity{ID: "3", Kind: runner.ActMessage, Title: "text"})
	if got := a.Line(); got != "lead · git log" {
		t.Errorf("a message changed the line: %q", got)
	}
	for _, id := range []string{"4", "5", "6"} {
		a.Note(runner.Activity{ID: id, Kind: runner.ActRead, Title: "f" + id})
	}
	a.Note(runner.Activity{ID: "7", Kind: runner.ActSearch, Title: "x"})
	a.Note(runner.Activity{ID: "8", Kind: runner.ActSearch, Title: "y"})
	if got := a.Summary(); got != "read 4 files · 2 searches · 1 command" {
		t.Errorf("summary = %q", got)
	}
}

func TestProjectMap(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("AGENTS.md", "SECRET-BODY")
	write("CLAUDE.md", "x")
	write("README.rst", "x")
	write("CONTRIBUTING.md", "x")
	write("LICENSE", "x")
	for i := 0; i < 40; i++ {
		write(filepath.Join("doc", "d"+string(rune('a'+i/26))+string(rune('a'+i%26))+".md"), "x")
	}
	write("docs/guide.md", "x")
	for i := 0; i < 25; i++ {
		name := filepath.Join(".decsr", "exports", "s"+string(rune('a'+i))+".md")
		write(name, "x")
		at := time.Now().Add(time.Duration(i) * time.Minute)
		_ = os.Chtimes(filepath.Join(root, name), at, at)
	}
	write(".decsr/exports/notes.txt", "x")
	write(".decsr/memory/MEMORY.md", strings.Repeat("m", 5000))

	m := ProjectMap(root, "go")
	for _, want := range []string{"Language: go", "AGENTS.md, CLAUDE.md, CONTRIBUTING.md, README.rst", "- doc/daa.md", "read the ones you need with your tools"} {
		if !strings.Contains(m, want) {
			t.Errorf("map lacks %q:\n%s", want, m)
		}
	}
	if strings.Contains(m, "LICENSE") || strings.Contains(m, "SECRET-BODY") {
		t.Error("the map lists only the instruction files, never their text")
	}
	if n := strings.Count(m, "\n- doc/") + strings.Count(m, "\n- docs/"); n != 30 {
		t.Errorf("%d doc paths, want 30", n)
	}
	if strings.Contains(m, "docs/guide.md") {
		t.Error("the cap of 30 was passed")
	}
	if n := strings.Count(m, "\n- .decsr/exports/"); n != 20 {
		t.Errorf("%d exports, want 20", n)
	}
	if !strings.Contains(m, "exports/sy.md") || strings.Contains(m, "exports/sa.md") || strings.Contains(m, "notes.txt") {
		t.Errorf("exports are not the 20 newest .md files:\n%s", m)
	}
	mem := m[strings.Index(m, "Project memory notes:\n")+len("Project memory notes:\n"):]
	mem = mem[:strings.Index(mem, "\n")]
	if r := []rune(mem); len(r) != mapMemoryMax {
		t.Errorf("memory is %d characters, want %d", len(r), mapMemoryMax)
	}
	if ProjectMap(t.TempDir(), "") != "" {
		t.Error("an empty project has no map")
	}
}

func TestCompactInput(t *testing.T) {
	t0 := time.Now()
	tr := []Line{
		{Who: "you", Text: "old question", At: t0.Add(-time.Hour)},
		{Who: "you", Text: "add search", At: t0},
		{Who: "lead", Text: "split into 2 nodes: T1, T2", At: t0.Add(time.Second)},
		{Who: "system", Text: "cancelled the split: removed T1", At: t0.Add(2 * time.Second)},
		{Who: "error", Text: "bad", At: t0.Add(3 * time.Second)},
	}
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "Index", Kind: KindWork, Status: StatusDone, Role: "coder", Summary: "built the index", Files: []string{"a.go"}},
		{ID: "T2", Title: "Tests", Kind: KindWork, Status: StatusFailed, Error: "build broke"},
	}}
	in := CompactInput("EARLIER", tr, t0, g, "the search work")
	for _, want := range []string{"## Earlier summary\nEARLIER", "User: add search", "Lead: split into 2 nodes", "decsr: cancelled the split: removed T1", "decsr error: bad", "- T1 \"Index\" [done] role coder", "built the index", "T2 failed: build broke", "## Focus\nthe search work"} {
		if !strings.Contains(in, want) {
			t.Errorf("input lacks %q:\n%s", want, in)
		}
	}
	for _, bad := range []string{"old question"} {
		if strings.Contains(in, bad) {
			t.Errorf("input has %q", bad)
		}
	}
	if CompactInput("", nil, time.Time{}, &Graph{}, "") != "" {
		t.Error("nothing to compact must give an empty input")
	}
}

func TestRunCompact(t *testing.T) {
	f := &leadFake{steps: []leadStep{{text: "  - the summary\n", usage: runner.TokenUsage{InputTokens: 50, OutputTokens: 5}}}}
	sum, u, err := RunCompact(t.Context(), f, "m", "INPUT", nil)
	if err != nil || sum != "- the summary" || u.InputTokens != 50 {
		t.Fatalf("%q %+v %v", sum, u, err)
	}
	r := f.reqs[0]
	if r.Agent || r.KeepSession || len(r.AllowedTools) != 0 || r.Messages[0].Content != "INPUT" || r.SystemPrompt == "" {
		t.Errorf("request = %+v", r)
	}
	f = &leadFake{steps: []leadStep{{text: "  "}}}
	if _, _, err := RunCompact(t.Context(), f, "m", "INPUT", nil); err == nil {
		t.Error("an empty summary must be an error")
	}
}
