package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGH answers every gh call from a script; git runs for real.
type fakeGH struct {
	mu     sync.Mutex
	calls  []string // "gh <args>"
	handle func(args []string) (string, error)
}

func (f *fakeGH) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if name == "git" {
		return ExecRunner(ctx, dir, name, args...)
	}
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	f.mu.Unlock()
	return f.handle(args)
}

func (f *fakeGH) called(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// prFixture is a repository with one merged node and a bare origin.
type prFixture struct {
	repo, origin string
	gh           *fakeGH
	c            *PRClient
}

func newPRFixture(t *testing.T, merged bool) *prFixture {
	t.Helper()
	repo := newRepo(t)
	origin := t.TempDir()
	gitT(t, origin, "init", "-q", "--bare")
	gitT(t, repo, "remote", "add", "origin", origin)
	if merged {
		iso := newIso(t, repo)
		if err := finishWith(t, iso, "T1", "c.go", "package c\n"); err != nil {
			t.Fatal(err)
		}
	}
	gh := &fakeGH{handle: func([]string) (string, error) { return "", errors.New("unexpected gh call") }}
	return &prFixture{repo: repo, origin: origin, gh: gh, c: &PRClient{Root: repo, Session: testSession, Run: gh.run}}
}

func (f *prFixture) base(t *testing.T) string {
	return gitT(t, f.repo, "rev-parse", "--abbrev-ref", "HEAD")
}

func testSessionData() Session {
	return Session{
		ID:         testSession,
		Transcript: []Line{{Who: "system", Text: "hi"}, {Who: "you", Text: "  Add search to the CLI\nwith fuzzy matching  "}},
		Graph: &Graph{Task: "Add search", Nodes: []*Node{
			{ID: "T1", Title: "Index | build", Kind: KindWork, Role: "coder", Status: StatusDone, Summary: "built the index", Files: []string{"a.go", "b.go"}, Observed: []string{"b.go", "c.go"}},
			{ID: "T2", Title: "Tests", Kind: KindWork, Status: StatusTodo},
			{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Status: StatusDone},
		}},
	}
}

func TestPRPreconditions(t *testing.T) {
	ctx := context.Background()
	f := newPRFixture(t, false)
	if _, err := f.c.Prepare(ctx); !errors.Is(err, ErrPRNothingDone) {
		t.Fatalf("nothing merged: got %v", err)
	}

	f = newPRFixture(t, true)
	f.gh.handle = func([]string) (string, error) { return "", fmt.Errorf("gh: %w", exec.ErrNotFound) }
	if _, err := f.c.Prepare(ctx); !errors.Is(err, ErrGHMissing) {
		t.Fatalf("gh missing: got %v", err)
	}
	f.gh.handle = func([]string) (string, error) { return "", errors.New("not logged in") }
	if _, err := f.c.Prepare(ctx); !errors.Is(err, ErrGHAuth) {
		t.Fatalf("gh auth: got %v", err)
	}

	f.gh.handle = func([]string) (string, error) { return "ok", nil }
	f.c.Cfg.Remote = "nowhere"
	if _, err := f.c.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "no git remote") {
		t.Fatalf("remote: got %v", err)
	}
	f.c.Cfg.Remote = ""
	f.c.Cfg.Base = SessionBranch(testSession)
	if _, err := f.c.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "session branch") {
		t.Fatalf("base == session branch: got %v", err)
	}
	f.c.Cfg.Base = ""
	tg, err := f.c.Prepare(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tg.Remote != "origin" || tg.Base != f.base(t) || tg.Branch != SessionBranch(testSession) {
		t.Errorf("target = %+v", tg)
	}
	f.c.Cfg = PRConfig{Base: "develop", Remote: "origin"}
	if tg, _ = f.c.Prepare(ctx); tg.Base != "develop" {
		t.Errorf("config base ignored: %+v", tg)
	}
}

func TestPRTitleAndBody(t *testing.T) {
	s := testSessionData()
	if got := PRTitle(s); got != "Add search to the CLI" {
		t.Errorf("title = %q", got)
	}
	s.Transcript[1].Text = strings.Repeat("x", 100)
	if got := PRTitle(s); len([]rune(got)) != 72 {
		t.Errorf("long title has %d runes", len([]rune(got)))
	}
	if got := PRTitle(Session{ID: "sid"}); got != "decsr session sid" {
		t.Errorf("fallback title = %q", got)
	}
	body := PRBody(testSessionData())
	for _, want := range []string{
		"## Task", "Add search to the CLI",
		"| T1 | Index \\| build | coder | done | 3 |",
		"| T2 | Tests | - | todo | 0 |",
		"- T1: built the index",
		"## Build & test\n\nPassed.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	for _, bad := range []string{"Claude", "Anthropic", "Co-authored", "Generated"} {
		if strings.Contains(body, bad) {
			t.Errorf("body contains %q", bad)
		}
	}
}

func TestEnsurePRCreatesAndPushes(t *testing.T) {
	f := newPRFixture(t, true)
	var body string
	f.gh.handle = func(args []string) (string, error) {
		switch strings.Join(args[:2], " ") {
		case "pr view":
			return "", errors.New("no pull requests found")
		case "pr create":
			for i, a := range args {
				if a == "--body-file" {
					b, err := os.ReadFile(args[i+1])
					if err != nil {
						return "", err
					}
					body = string(b)
				}
			}
			return "https://github.com/o/r/pull/42\n", nil
		}
		return "", errors.New("unexpected " + strings.Join(args, " "))
	}
	tg := PRTarget{Base: f.base(t), Remote: "origin", Branch: SessionBranch(testSession)}
	res, err := f.c.EnsurePR(context.Background(), tg, testSessionData())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.State.Number != 42 || res.State.URL != "https://github.com/o/r/pull/42" {
		t.Errorf("result = %+v", res)
	}
	if !strings.Contains(body, "## Task") {
		t.Errorf("body file = %q", body)
	}
	if got := gitT(t, f.origin, "rev-parse", "--verify", "refs/heads/"+tg.Branch); got == "" {
		t.Error("branch was not pushed")
	}
	create := ""
	for _, c := range f.gh.calls {
		if strings.HasPrefix(c, "pr create") {
			create = c
		}
	}
	for _, want := range []string{"--head " + tg.Branch, "--base " + tg.Base, "--title Add search to the CLI"} {
		if !strings.Contains(create, want) {
			t.Errorf("create call %q lacks %q", create, want)
		}
	}
}

func TestEnsurePRExistingOnlyPushes(t *testing.T) {
	f := newPRFixture(t, true)
	f.gh.handle = func(args []string) (string, error) {
		if args[0] == "pr" && args[1] == "view" {
			return `{"number":7,"url":"https://github.com/o/r/pull/7","state":"OPEN"}`, nil
		}
		return "", errors.New("unexpected " + strings.Join(args, " "))
	}
	tg := PRTarget{Base: f.base(t), Remote: "origin", Branch: SessionBranch(testSession)}
	s := testSessionData()
	s.PR = &PRState{Number: 7, URL: "old", SeenComments: []string{"c1"}, CIFixes: 2}
	res, err := f.c.EnsurePR(context.Background(), tg, s)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.State.Number != 7 || res.State.URL != "https://github.com/o/r/pull/7" {
		t.Errorf("result = %+v", res)
	}
	if res.State.CIFixes != 2 || len(res.State.SeenComments) != 1 {
		t.Errorf("progress of the same PR must be kept: %+v", res.State)
	}
	if f.gh.called("pr create") != 0 {
		t.Error("an existing PR must not be created again")
	}
}

func TestPushNeverForces(t *testing.T) {
	f := newPRFixture(t, true)
	var mu sync.Mutex
	var pushes [][]string
	f.c.Run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "git" && len(args) > 0 && args[0] == "push" {
			mu.Lock()
			pushes = append(pushes, args)
			mu.Unlock()
		}
		return f.gh.run(ctx, dir, name, args...)
	}
	tg := PRTarget{Base: f.base(t), Remote: "origin", Branch: SessionBranch(testSession)}
	if err := f.c.Push(context.Background(), tg); err != nil {
		t.Fatal(err)
	}
	// Re-create the history of the branch: the push is rejected.
	gitT(t, f.repo, "branch", "-f", tg.Branch, "HEAD")
	origin := gitT(t, f.origin, "rev-parse", tg.Branch)
	pushes = nil
	err := f.c.Push(context.Background(), tg)
	if err == nil {
		t.Fatal("a rejected push must fail")
	}
	if !strings.Contains(err.Error(), "someone pushed to it") || !strings.Contains(err.Error(), "nothing was overwritten") || !strings.Contains(err.Error(), tg.Branch) {
		t.Errorf("error = %v", err)
	}
	if len(pushes) != 1 {
		t.Errorf("want exactly one push attempt, got %d: %v", len(pushes), pushes)
	}
	for _, args := range pushes {
		for _, a := range args {
			if strings.HasPrefix(a, "--force") || a == "-f" {
				t.Errorf("force argument used: %v", args)
			}
		}
	}
	if gitT(t, f.origin, "rev-parse", tg.Branch) != origin {
		t.Error("origin branch was overwritten")
	}
}

func checksJSON(cs ...Check) string {
	b, _ := json.Marshal(cs)
	return string(b)
}

func TestChecksParsing(t *testing.T) {
	f := newPRFixture(t, false)
	f.gh.handle = func([]string) (string, error) {
		// gh prints the JSON and exits 1 while a check fails.
		return checksJSON(Check{Name: "build", State: "FAILURE", Bucket: "fail", Link: "x"}), errors.New("exit status 1")
	}
	cs, err := f.c.Checks(context.Background(), 1)
	if err != nil || len(cs) != 1 || cs[0].Bucket != "fail" || cs[0].Name != "build" {
		t.Fatalf("checks = %+v, %v", cs, err)
	}
	f.gh.handle = func([]string) (string, error) {
		return "", errors.New("gh pr checks: exit status 1: no checks reported on the 'x' branch")
	}
	if cs, err = f.c.Checks(context.Background(), 1); err != nil || len(cs) != 0 {
		t.Fatalf("no checks: %+v, %v", cs, err)
	}
	f.gh.handle = func([]string) (string, error) { return "", errors.New("HTTP 502") }
	if _, err = f.c.Checks(context.Background(), 1); err == nil {
		t.Fatal("a real error must surface")
	}
}

// pollGH scripts the gh calls of Poll.
type pollGH struct {
	sha      string
	checks   []Check
	comments string
	reviews  string
	log      string
}

func (p *pollGH) handle(args []string) (string, error) {
	switch {
	case args[0] == "pr" && args[1] == "view":
		return fmt.Sprintf(`{"headRefOid":%q}`, p.sha), nil
	case args[0] == "pr" && args[1] == "checks":
		return checksJSON(p.checks...), nil
	case args[0] == "run" && args[1] == "view":
		return p.log, nil
	case args[0] == "api" && strings.Contains(args[1], "/comments"):
		if p.comments == "" {
			return "[]", nil
		}
		return p.comments, nil
	case args[0] == "api" && strings.Contains(args[1], "/reviews"):
		if p.reviews == "" {
			return "[]", nil
		}
		return p.reviews, nil
	}
	return "", errors.New("unexpected " + strings.Join(args, " "))
}

func failing(name string) Check {
	return Check{Name: name, State: "FAILURE", Bucket: "fail", Link: "https://github.com/o/r/actions/runs/123/job/9"}
}

func TestPollChecksPassed(t *testing.T) {
	f := newPRFixture(t, false)
	p := &pollGH{sha: "aaa", checks: []Check{{Name: "build", Bucket: "pass"}, {Name: "lint", Bucket: "skipping"}}}
	f.gh.handle = p.handle
	_, res, err := f.c.Poll(context.Background(), PRState{Number: 5})
	if err != nil || !res.Passed || res.CIFix != nil {
		t.Fatalf("res = %+v, %v", res, err)
	}
	p.checks = append(p.checks, Check{Name: "slow", Bucket: "pending"})
	if _, res, _ = f.c.Poll(context.Background(), PRState{Number: 5}); res.Passed {
		t.Error("a pending check is not a pass")
	}
	p.checks = nil
	if _, res, _ = f.c.Poll(context.Background(), PRState{Number: 5}); res.Passed {
		t.Error("no checks is not a pass")
	}
}

func TestPollFailingCheckOneFixPerSHAAndMax(t *testing.T) {
	f := newPRFixture(t, false)
	var long []string
	for i := 0; i < 500; i++ {
		long = append(long, fmt.Sprintf("line %03d %s", i, strings.Repeat("x", 40)))
	}
	p := &pollGH{sha: "aaa", checks: []Check{failing("build"), failing("test")}, log: strings.Join(long, "\n")}
	f.gh.handle = p.handle
	f.c.Cfg.MaxCIFixes = 2
	ctx := context.Background()

	st, res, err := f.c.Poll(ctx, PRState{Number: 5})
	if err != nil || res.CIFix == nil {
		t.Fatalf("first failure: %+v, %v", res, err)
	}
	if res.CIFix.Title != "Fix CI: build, test" {
		t.Errorf("title = %q", res.CIFix.Title)
	}
	d := res.CIFix.Desc
	if !strings.Contains(d, "line 499") || strings.Contains(d, "line 300 ") {
		t.Errorf("log tail wrong:\n%s", d)
	}
	if len(d) > maxCITailBytes+400 {
		t.Errorf("description has %d bytes", len(d))
	}
	if n := strings.Count(d, "\nline "); n > maxCITailLines {
		t.Errorf("%d log lines", n)
	}
	if f.gh.called("run view 123 --log-failed") != 1 {
		t.Errorf("both checks share one run, calls: %v", f.gh.calls)
	}
	if st.CIFixes != 1 || st.FixedSHA != "aaa" {
		t.Errorf("state = %+v", st)
	}

	if st, res, _ = f.c.Poll(ctx, st); res.CIFix != nil || res.Exhausted {
		t.Errorf("same head must not add a node: %+v", res)
	}
	p.sha = "bbb"
	if st, res, _ = f.c.Poll(ctx, st); res.CIFix == nil || st.CIFixes != 2 {
		t.Fatalf("second head: %+v %+v", res, st)
	}
	p.sha = "ccc"
	st, res, _ = f.c.Poll(ctx, st)
	if res.CIFix != nil || !res.Exhausted || st.CIFixes != 2 {
		t.Fatalf("max reached: %+v %+v", res, st)
	}
	if _, res, _ = f.c.Poll(ctx, st); res.Exhausted {
		t.Error("the limit is announced once per head")
	}
}

func TestPollReviewCommentsOneNodeAndSeen(t *testing.T) {
	f := newPRFixture(t, false)
	p := &pollGH{sha: "aaa", checks: []Check{{Name: "build", Bucket: "pass"}},
		comments: `[{"id":11,"path":"a.go","line":3,"body":"rename   this\nplease"},{"id":12,"path":"b.go","line":0,"original_line":9,"body":"why?"}]`,
		reviews:  `[{"id":21,"state":"CHANGES_REQUESTED","body":"needs tests"},{"id":22,"state":"APPROVED","body":"lgtm"},{"id":23,"state":"CHANGES_REQUESTED","body":""}]`}
	f.gh.handle = p.handle
	ctx := context.Background()
	st, res, err := f.c.Poll(ctx, PRState{Number: 5, SeenComments: []string{"c12"}})
	if err != nil || res.ReviewFix == nil {
		t.Fatalf("res = %+v, %v", res, err)
	}
	d := res.ReviewFix.Desc
	for _, want := range []string{"- a.go:3 — rename this please", "- review (changes requested) — needs tests"} {
		if !strings.Contains(d, want) {
			t.Errorf("desc lacks %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "why?") || strings.Contains(d, "lgtm") {
		t.Errorf("seen or non-blocking feedback included:\n%s", d)
	}
	if res.ReviewFix.Title != "Address review comments" {
		t.Errorf("title = %q", res.ReviewFix.Title)
	}
	for _, id := range []string{"c11", "c12", "r21"} {
		if !st.seen(id) {
			t.Errorf("%s not recorded as seen: %v", id, st.SeenComments)
		}
	}
	if _, res, _ = f.c.Poll(ctx, st); res.ReviewFix != nil {
		t.Error("seen comments must not create another node")
	}
}

func TestSessionPRBackwardCompatible(t *testing.T) {
	root := t.TempDir()
	old := fmt.Sprintf(`{"id":%q,"title":"t","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`, testSession)
	if err := os.MkdirAll(SessionsDir(root), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SessionsDir(root)+"/"+testSession+".json", []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSession(root, testSession)
	if err != nil || s.PR != nil {
		t.Fatalf("old session: %+v, %v", s, err)
	}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(SessionsDir(root) + "/" + testSession + ".json")
	if strings.Contains(string(data), `"pr"`) {
		t.Errorf("an unset PR must not be written:\n%s", data)
	}
	s.PR = &PRState{Number: 3, URL: "u", SeenComments: []string{"c1"}, CIFixes: 1, FixedSHA: "abc"}
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	back, err := LoadSession(root, testSession)
	if err != nil || back.PR == nil || back.PR.Number != 3 || back.PR.CIFixes != 1 || back.PR.SeenComments[0] != "c1" || back.PR.FixedSHA != "abc" {
		t.Fatalf("round trip: %+v, %v", back.PR, err)
	}
}

func TestPRConfigDefaults(t *testing.T) {
	for _, c := range []struct {
		in   int
		want time.Duration
	}{{0, 60 * time.Second}, {5, 15 * time.Second}, {15, 15 * time.Second}, {90, 90 * time.Second}} {
		if got := (PRConfig{PollSeconds: c.in}).PollInterval(); got != c.want {
			t.Errorf("poll %d = %v, want %v", c.in, got, c.want)
		}
	}
	if (PRConfig{}).MaxFixes() != 3 || (PRConfig{MaxCIFixes: 5}).MaxFixes() != 5 {
		t.Error("max_ci_fixes default")
	}
}

func TestAddCIFix(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "a", Kind: KindWork, Status: StatusDone},
		{ID: "CHECK", Title: "Build & test", Kind: KindCheck, Status: StatusDone, DependsOn: []string{"T1"}},
	}}
	f := g.AddCIFix("Fix CI:   build", "log")
	if f.ID != "F1" || f.Title != "Fix CI: build" || f.Kind != KindWork || f.Status != StatusTodo || f.SuggestedRole != "coder_fixer" || len(f.DependsOn) != 0 {
		t.Errorf("fix = %+v", f)
	}
	c := g.Node("CHECK")
	if c.Status != StatusTodo || c.DependsOn[len(c.DependsOn)-1] != "F1" {
		t.Errorf("check = %+v", c)
	}
	if f2 := g.AddCIFix("x", "y"); f2.ID != "F2" {
		t.Errorf("second id = %s", f2.ID)
	}
	e := NewEngineWithSave(&Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Status: StatusDone}}}, nil, 1, nil)
	id := e.AddCIFix("Address review comments", "d", "coder_fixer")
	n := e.Snapshot().Node(id)
	if n == nil || n.Role != "coder_fixer" || n.Description != "d" {
		t.Errorf("engine fix = %+v", n)
	}
}
