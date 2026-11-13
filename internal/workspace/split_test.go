package workspace

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const happy = `===TITLE===
Add health endpoint
===TASKS===
[{"key":"T1","title":"Add handler","description":"edit main.go","role":"coder","depends_on":[]},
 {"key":"t2","title":"Write docs","description":"edit README.md","role":"Docs Writer","depends_on":[]},
 {"key":"T3","title":"Review","description":"review","role":"qa","depends_on":["T1","T2"]}]
===NEW_ROLES===
[{"name":"docs_writer","description":"Writes docs."}]
===END===`

func testRoles() []Role { return Roles(testCfg()) }

func TestParseSplitHappyPath(t *testing.T) {
	g, err := ParseSplit("the task", happy, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if g.Task != "the task" || g.Title != "Add health endpoint" {
		t.Errorf("task/title = %q / %q", g.Task, g.Title)
	}
	if got := ids(g.Nodes); !reflect.DeepEqual(got, []string{"T1", "T2", "T3"}) {
		t.Fatalf("ids = %v", got)
	}
	if !reflect.DeepEqual(g.Nodes[2].DependsOn, []string{"T1", "T2"}) {
		t.Errorf("deps = %v", g.Nodes[2].DependsOn)
	}
	if g.Nodes[0].SuggestedRole != "coder" || g.Nodes[2].SuggestedRole != "qa" {
		t.Errorf("suggested = %q %q", g.Nodes[0].SuggestedRole, g.Nodes[2].SuggestedRole)
	}
	if g.Nodes[0].Kind != KindWork || g.Nodes[0].Status != StatusTodo {
		t.Errorf("node = %+v", g.Nodes[0])
	}
	if len(g.NewRoles) != 1 || g.NewRoles[0].Name != "docs_writer" || g.NewRoles[0].Description != "Writes docs." {
		t.Errorf("new roles = %+v", g.NewRoles)
	}
	if g.Nodes[1].SuggestedRole != "docs_writer" {
		t.Errorf("normalized role = %q", g.Nodes[1].SuggestedRole)
	}
}

func TestParseSplitUnknownRoleBecomesNewRole(t *testing.T) {
	out := `===TASKS===
[{"key":"T1","title":"a","description":"d","role":"Data-Scientist","depends_on":[]}]
===END===`
	g, err := ParseSplit("x", out, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].SuggestedRole != "data_scientist" {
		t.Errorf("suggested = %q", g.Nodes[0].SuggestedRole)
	}
	if len(g.NewRoles) != 1 || g.NewRoles[0].Name != "data_scientist" || g.NewRoles[0].Description != "" {
		t.Errorf("new roles = %+v", g.NewRoles)
	}
}

func TestParseSplitNeverAssigns(t *testing.T) {
	g, err := ParseSplit("x", happy, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	for _, nd := range g.Nodes {
		if nd.Role != "" {
			t.Errorf("%s assigned %q", nd.ID, nd.Role)
		}
	}
}

func TestParseSplitTolerant(t *testing.T) {
	out := "Sure, here you go:\n===TASKS===\n```json\n" +
		`[{"key":"T1","title":"a","description":"d","role":"coder","depends_on":[]}]` +
		"\n```\n===END===\ntrailing chatter"
	g, err := ParseSplit(strings.Repeat("long task ", 20), out, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || len(g.NewRoles) != 0 {
		t.Errorf("graph = %+v", g)
	}
	if want := []rune(strings.Repeat("long task ", 20))[:60]; g.Title != string(want) {
		t.Errorf("title = %q", g.Title)
	}
	pre := "intro\n" + happy + "\nbye"
	if _, err := ParseSplit("x", pre, testRoles()); err != nil {
		t.Errorf("preamble: %v", err)
	}
}

func TestParseSplitErrors(t *testing.T) {
	wrap := func(js string) string { return "===TASKS===\n" + js + "\n===END===" }
	tests := map[string]string{
		"no marker":    "just some text",
		"invalid json": wrap(`[{"key":`),
		"empty array":  wrap(`[]`),
		"cycle":        wrap(`[{"key":"T1","title":"a","role":"coder","depends_on":["T2"]},{"key":"T2","title":"b","role":"coder","depends_on":["T1"]}]`),
		"duplicate":    wrap(`[{"key":"T1","title":"a","role":"coder"},{"key":"T1","title":"b","role":"coder"}]`),
		"missing dep":  wrap(`[{"key":"T1","title":"a","role":"coder","depends_on":["T7"]}]`),
		"empty key":    wrap(`[{"key":"","title":"a","role":"coder"}]`),
	}
	for name, out := range tests {
		t.Run(name, func(t *testing.T) {
			g, err := ParseSplit("x", out, testRoles())
			if err == nil || !strings.HasPrefix(err.Error(), "split:") || g != nil {
				t.Fatalf("g=%v err=%v", g, err)
			}
		})
	}
}

func TestBuildSplitPromptListsRoles(t *testing.T) {
	roles := testRoles()
	p := BuildSplitPrompt("build the thing", roles, "CTX-MARKER")
	for _, r := range roles {
		if !strings.Contains(p, "- "+r.Name+": "+r.Description) {
			t.Errorf("missing role line for %s", r.Name)
		}
	}
	if !strings.Contains(p, "build the thing") || !strings.Contains(p, "CTX-MARKER") {
		t.Error("task or context missing")
	}
	if strings.Contains(p, "{{") {
		t.Error("unreplaced placeholder")
	}
}

type chunkRunner struct {
	chunks []string
	usage  *runner.TokenUsage
	req    runner.CompletionRequest
}

func (c *chunkRunner) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	c.req = req
	ch := make(chan runner.Token, len(c.chunks)+1)
	for _, s := range c.chunks {
		ch <- runner.Token{Text: s}
	}
	ch <- runner.Token{Done: true, Usage: c.usage}
	close(ch)
	return ch, nil
}

func TestSplitStreams(t *testing.T) {
	a, b := len(happy)/3, 2*len(happy)/3
	r := &chunkRunner{
		chunks: []string{happy[:a], happy[a:b], happy[b:]},
		usage:  &runner.TokenUsage{InputTokens: 10, OutputTokens: 20},
	}
	var seen strings.Builder
	g, u, err := Split(context.Background(), r, "m", "task", testRoles(), "ctx", func(s string) { seen.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 3 || seen.String() != happy {
		t.Errorf("nodes=%d seen ok=%v", len(g.Nodes), seen.String() == happy)
	}
	if u.InputTokens != 10 || u.OutputTokens != 20 {
		t.Errorf("usage = %+v", u)
	}
	if r.req.Model != "m" || len(r.req.Messages) != 1 || r.req.Messages[0].Content != "task" || r.req.SystemPrompt == "" {
		t.Errorf("request = %+v", r.req)
	}
	// nil onToken must not panic.
	r2 := &chunkRunner{chunks: []string{happy}}
	if _, _, err := Split(context.Background(), r2, "m", "task", testRoles(), "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestSplitRunnerError(t *testing.T) {
	r := &runner.MockRunner{}
	if _, _, err := Split(context.Background(), r, "m", "t", testRoles(), "", nil); err == nil {
		t.Fatal("expected error")
	}
}

func wrapTasks(js string) string { return "===TASKS===\n" + js + "\n===END===" }

func TestParseSplitLastMarkerWins(t *testing.T) {
	out := "===TASKS===\n[{\"key\":\"T1\",\"title\":\"draft\"}]\n===END===\nrevised:\n===TITLE===\nFinal\n===TASKS===\n[{\"key\":\"T1\",\"title\":\"final\",\"role\":\"coder\"}]\n===END==="
	g, err := ParseSplit("x", out, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].Title != "final" || g.Title != "Final" {
		t.Errorf("nodes=%+v title=%q", g.Nodes[0], g.Title)
	}
}

func TestParseSplitProseAndTrailingCommas(t *testing.T) {
	js := "Here is the plan [see below]:\n[{\"key\":\"T1\",\"title\":\"a [x]\",\"description\":\"d, ]\",\"role\":\"coder\",\"depends_on\":[],},]\nHope that helps."
	g, err := ParseSplit("x", wrapTasks(js), testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 || g.Nodes[0].Title != "a [x]" || g.Nodes[0].Description != "d, ]" {
		t.Errorf("nodes = %+v", g.Nodes[0])
	}
}

func TestParseSplitKeyCase(t *testing.T) {
	g, err := ParseSplit("x", wrapTasks(`[{"key":"t1","title":"a","role":"coder"},{"key":" t2 ","title":"b","role":"coder","depends_on":["t1"]}]`), testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[1].ID != "T2" || !reflect.DeepEqual(g.Nodes[1].DependsOn, []string{"T1"}) {
		t.Errorf("node = %+v", g.Nodes[1])
	}
}

func TestParseSplitReservedKeysRenamed(t *testing.T) {
	js := `[{"key":"CHECK","title":"a","role":"coder"},{"key":"f1","title":"b","role":"coder","depends_on":["check"]},{"key":"T_CHECK","title":"c","role":"coder","depends_on":["F1"]}]`
	g, err := ParseSplit("x", wrapTasks(js), testRoles())
	if err != nil {
		t.Fatal(err)
	}
	got := ids(g.Nodes)
	for _, id := range got {
		if id == "CHECK" || id == "F1" {
			t.Fatalf("reserved id kept: %v", got)
		}
	}
	if got[0] == got[1] || got[0] == got[2] || got[1] == got[2] {
		t.Fatalf("ids not unique: %v", got)
	}
	if !reflect.DeepEqual(g.Nodes[1].DependsOn, []string{got[0]}) || !reflect.DeepEqual(g.Nodes[2].DependsOn, []string{got[1]}) {
		t.Errorf("deps not updated: %v %v (ids %v)", g.Nodes[1].DependsOn, g.Nodes[2].DependsOn, got)
	}
	if g.Nodes[2].ID != "T_CHECK" {
		t.Errorf("existing T_CHECK must stay: %v", got)
	}
}

func TestParseSplitTooManyTasks(t *testing.T) {
	var parts []string
	for i := 1; i <= 13; i++ {
		parts = append(parts, fmt.Sprintf(`{"key":"T%d","title":"t","role":"coder"}`, i))
	}
	_, err := ParseSplit("x", wrapTasks("["+strings.Join(parts, ",")+"]"), testRoles())
	if err == nil || err.Error() != "split: too many sub-tasks (13, max 12)" {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseSplit("x", wrapTasks("["+strings.Join(parts[:12], ",")+"]"), testRoles()); err != nil {
		t.Fatalf("12 tasks: %v", err)
	}
}

func TestParseSplitRoleNamesAlwaysValid(t *testing.T) {
	long := strings.Repeat("a", 40)
	roles := []string{"9lives", "x", "Data Scientist!", long, "_hidden", "日本語"}
	var parts []string
	for i, r := range roles {
		parts = append(parts, fmt.Sprintf(`{"key":"T%d","title":"t","role":%q}`, i+1, r))
	}
	g, err := ParseSplit("x", wrapTasks("["+strings.Join(parts, ",")+"]"), testRoles())
	if err != nil {
		t.Fatal(err)
	}
	for _, nd := range g.Nodes {
		if !config.ValidRoleName(nd.SuggestedRole) {
			t.Errorf("%s: invalid suggested role %q", nd.ID, nd.SuggestedRole)
		}
	}
	for _, nr := range g.NewRoles {
		if !config.ValidRoleName(nr.Name) {
			t.Errorf("invalid new role %q", nr.Name)
		}
	}
}

func TestParseSplitFallbackTitleDescription(t *testing.T) {
	g, err := ParseSplit("x", wrapTasks(`[{"key":"t1","title":"","description":"","role":"coder"},{"key":"T2","title":"Real","description":"","role":"coder"}]`), testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if g.Nodes[0].Title != "T1" || g.Nodes[0].Description != "T1" {
		t.Errorf("node0 = %+v", g.Nodes[0])
	}
	if g.Nodes[1].Title != "Real" || g.Nodes[1].Description != "Real" {
		t.Errorf("node1 = %+v", g.Nodes[1])
	}
}

func TestParseSplitFriendlyJSONError(t *testing.T) {
	for _, js := range []string{`[{"key":`, `[{"key" "T1"}]`, `no array here`} {
		_, err := ParseSplit("x", wrapTasks(js), testRoles())
		if err == nil || !strings.HasPrefix(err.Error(), "split: the lead's answer was not valid JSON (") || !strings.HasSuffix(err.Error(), ")") {
			t.Errorf("%q: err = %v", js, err)
		}
	}
}

func TestBuildSplitPromptMarksReadOnly(t *testing.T) {
	p := BuildSplitPrompt("t", testRoles(), "")
	if !strings.Contains(p, "- security: Audits code for vulnerabilities and unsafe patterns. (read-only: reviews and reports, cannot change files)") {
		t.Errorf("security not marked read-only:\n%s", p)
	}
	if strings.Contains(p, "- coder: Writes and changes production code. (read-only") {
		t.Error("coder marked read-only")
	}
	if !strings.Contains(p, "read-only") {
		t.Error("rules missing")
	}
}

// seqRunner answers each Complete call with the next scripted reply.
type seqRunner struct {
	replies []string
	reqs    []runner.CompletionRequest
}

func (s *seqRunner) Complete(_ context.Context, req runner.CompletionRequest) (<-chan runner.Token, error) {
	s.reqs = append(s.reqs, req)
	text := s.replies[min(len(s.reqs)-1, len(s.replies)-1)]
	ch := make(chan runner.Token, 2)
	ch <- runner.Token{Text: text}
	ch <- runner.Token{Done: true, Usage: &runner.TokenUsage{InputTokens: 10, OutputTokens: 5}}
	close(ch)
	return ch, nil
}

const noRoles = `===TASKS===
[{"key":"T1","title":"a","description":"d"},{"key":"T2","title":"b","description":"d"}]
===END===`

func TestSplitRetriesWhenNoRoles(t *testing.T) {
	r := &seqRunner{replies: []string{noRoles, happy}}
	var seen strings.Builder
	g, u, err := Split(context.Background(), r, "m", "task", testRoles(), "", func(s string) { seen.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(r.reqs) != 2 {
		t.Fatalf("calls = %d", len(r.reqs))
	}
	msgs := r.reqs[1].Messages
	if len(msgs) != 3 || msgs[1].Role != "assistant" || msgs[1].Content != noRoles || msgs[2].Role != "user" ||
		msgs[2].Content != `Your answer is missing the "role" (and possibly "depends_on") fields. Output the complete answer again in the exact format, with a role for every sub-task and depends_on wherever one sub-task needs another's files.` {
		t.Errorf("retry messages = %+v", msgs)
	}
	if len(g.Nodes) != 3 || g.Nodes[0].SuggestedRole != "coder" {
		t.Errorf("graph = %+v", g.Nodes)
	}
	if u.InputTokens != 20 || u.OutputTokens != 10 {
		t.Errorf("usage = %+v", u)
	}
	if !strings.Contains(seen.String(), noRoles) {
		t.Error("first answer not streamed")
	}
}

func TestSplitRetryKeepsFirstWhenRetryIsWorse(t *testing.T) {
	for _, second := range []string{"garbage", noRoles} {
		r := &seqRunner{replies: []string{noRoles, second}}
		g, _, err := Split(context.Background(), r, "m", "task", testRoles(), "", nil)
		if err != nil || len(g.Nodes) != 2 || len(r.reqs) != 2 {
			t.Fatalf("second=%q g=%v err=%v calls=%d", second, g, err, len(r.reqs))
		}
	}
}

func TestSplitNoRetryWhenRolesPresentOrSingleNode(t *testing.T) {
	r := &seqRunner{replies: []string{happy}}
	if _, _, err := Split(context.Background(), r, "m", "task", testRoles(), "", nil); err != nil || len(r.reqs) != 1 {
		t.Fatalf("err=%v calls=%d", err, len(r.reqs))
	}
	one := wrapTasks(`[{"key":"T1","title":"a"}]`)
	r = &seqRunner{replies: []string{one}}
	if _, _, err := Split(context.Background(), r, "m", "task", testRoles(), "", nil); err != nil || len(r.reqs) != 1 {
		t.Fatalf("single: err=%v calls=%d", err, len(r.reqs))
	}
}

func TestParseSplitKeyAliases(t *testing.T) {
	js := `[{"id":"a","name":"First","description":"d","agent":"coder","deps":[]},
	{"key":"B","title":"Second","name":"ignored","role":"qa","assignee":"ignored","dependencies":["A"]},
	{"key":"C","title":"Third","owner":"coder","dependsOn":["A","B"]},
	{"key":"D","title":"Fourth","depends":["C"],"depends_on":["A"]}]`
	g, err := ParseSplit("x", wrapTasks(js), testRoles())
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes
	if n[0].ID != "A" || n[0].Title != "First" || n[0].SuggestedRole != "coder" {
		t.Errorf("n0 = %+v", n[0])
	}
	if n[1].Title != "Second" || n[1].SuggestedRole != "qa" || !reflect.DeepEqual(n[1].DependsOn, []string{"A"}) {
		t.Errorf("n1 = %+v", n[1])
	}
	if n[2].SuggestedRole != "coder" || !reflect.DeepEqual(n[2].DependsOn, []string{"A", "B"}) {
		t.Errorf("n2 = %+v", n[2])
	}
	if !reflect.DeepEqual(n[3].DependsOn, []string{"A"}) {
		t.Errorf("n3 = %+v", n[3])
	}
}
