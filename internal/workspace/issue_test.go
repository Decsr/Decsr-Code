package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

type issueRun struct {
	calls []string
	reply func(name, line string) (string, error)
}

func (r *issueRun) run(_ context.Context, _ string, name string, args ...string) (string, error) {
	line := strings.Join(args, " ")
	r.calls = append(r.calls, name+" "+line)
	return r.reply(name, line)
}

const ghJSON = `{"number":42,"title":"Fix flaky\n login test","body":"It fails.\r\nOften.","url":"https://github.com/o/r/issues/42","state":"OPEN","labels":[{"name":"bug"},{"name":"ci"}]}`

func TestParseIssueRef(t *testing.T) {
	for in, want := range map[string][2]string{
		"42": {SourceGitHub, "42"}, "#42": {SourceGitHub, "42"},
		"https://github.com/o/r/issues/7": {SourceGitHub, "https://github.com/o/r/issues/7"},
		"decsr-ab1":                       {SourceBeads, "decsr-ab1"},
	} {
		s, a, err := ParseIssueRef(in)
		if err != nil || s != want[0] || a != want[1] {
			t.Errorf("%q: got %s %s %v, want %v", in, s, a, err, want)
		}
	}
	for _, bad := range []string{"", "-x", "https://example.com/foo", "a b"} {
		if _, _, err := ParseIssueRef(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestFetchIssueGitHubNumber(t *testing.T) {
	r := &issueRun{reply: func(string, string) (string, error) { return ghJSON, nil }}
	is, err := FetchIssue(context.Background(), t.TempDir(), "#42", r.run)
	if err != nil {
		t.Fatal(err)
	}
	if is.ID != "#42" || is.Title != "Fix flaky login test" || is.Source != SourceGitHub || is.Closed {
		t.Fatalf("issue = %+v", is)
	}
	if want := "Issue #42: Fix flaky login test\n\nIt fails.\nOften.\n\nLabels: bug, ci"; is.Task() != want {
		t.Fatalf("task = %q", is.Task())
	}
	if len(r.calls) != 1 || !strings.HasPrefix(r.calls[0], "gh issue view 42 --json") {
		t.Fatalf("calls = %v", r.calls)
	}
}

func TestFetchIssueURLSameAndOtherRepo(t *testing.T) {
	for cur, want := range map[string]string{"o/r\n": "#42", "x/y\n": "o/r#42"} {
		r := &issueRun{reply: func(_, line string) (string, error) {
			if strings.HasPrefix(line, "repo view") {
				return cur, nil
			}
			return ghJSON, nil
		}}
		is, err := FetchIssue(context.Background(), t.TempDir(), "https://github.com/o/r/issues/42", r.run)
		if err != nil || is.ID != want {
			t.Fatalf("current %q: id = %q err %v, want %q", cur, is.ID, err, want)
		}
	}
}

func TestFetchIssueClosedAndTrim(t *testing.T) {
	body := strings.Repeat("é", maxIssueBody)
	r := &issueRun{reply: func(string, string) (string, error) {
		return `{"number":1,"title":"t","body":"` + body + `","state":"CLOSED","labels":[]}`, nil
	}}
	is, err := FetchIssue(context.Background(), t.TempDir(), "1", r.run)
	if err != nil || !is.Closed {
		t.Fatalf("issue = %+v err %v", is, err)
	}
	if len(is.Body) > maxIssueBody+64 || !strings.HasSuffix(is.Body, "(issue body trimmed)") {
		t.Fatalf("body of %d bytes not trimmed", len(is.Body))
	}
}

func TestFetchIssueGitHubErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("gh issue view 9: exit status 1: GraphQL: Could not resolve to an issue or pull request with the number of 9."), "not found"},
		{errors.New("gh: exit status 4: To get started with GitHub CLI, please run:  gh auth login"), "gh auth login"},
		{exec.ErrNotFound, "not on PATH"},
		{errors.New("boom"), "boom"},
	} {
		r := &issueRun{reply: func(string, string) (string, error) { return "", tc.err }}
		_, err := FetchIssue(context.Background(), t.TempDir(), "9", r.run)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.err, err, tc.want)
		}
	}
}

func TestFetchBead(t *testing.T) {
	r := &issueRun{reply: func(name, line string) (string, error) {
		if name != "bd" || line != "show decsr-ab1 --json" {
			t.Errorf("unexpected %s %s", name, line)
		}
		return "Warning: perms\n" + `[{"id":"decsr-ab1","title":"Add retry","description":"Retry 3x","status":"open","labels":["net"]}]`, nil
	}}
	is, err := FetchIssue(context.Background(), t.TempDir(), "decsr-ab1", r.run)
	if err != nil {
		t.Fatal(err)
	}
	if is.Source != SourceBeads || is.ID != "decsr-ab1" || is.Closed || is.Task() != "Issue decsr-ab1: Add retry\n\nRetry 3x\n\nLabels: net" {
		t.Fatalf("issue = %+v task %q", is, is.Task())
	}
	r.reply = func(string, string) (string, error) {
		return `{"id":"decsr-ab1","title":"x","status":"closed"}`, nil
	}
	if is, err = FetchIssue(context.Background(), t.TempDir(), "decsr-ab1", r.run); err != nil || !is.Closed {
		t.Fatalf("closed bead: %+v %v", is, err)
	}
	for _, tc := range []struct{ err, want string }{
		{"bd show x: Error: no beads database found", "no beads database"},
		{"bd show x: issue not found", "not found"},
	} {
		r.reply = func(string, string) (string, error) { return "", errors.New(tc.err) }
		if _, err := FetchIssue(context.Background(), t.TempDir(), "decsr-ab1", r.run); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v", tc.err, err)
		}
	}
	r.reply = func(string, string) (string, error) { return "", exec.ErrNotFound }
	if _, err := FetchIssue(context.Background(), t.TempDir(), "decsr-ab1", r.run); err == nil || !strings.Contains(err.Error(), "bd is not on PATH") {
		t.Errorf("missing bd: %v", err)
	}
}

func TestPRBodyIssueLine(t *testing.T) {
	for _, tc := range []struct {
		ref  *IssueRef
		want string
	}{
		{&IssueRef{Source: SourceGitHub, ID: "#42"}, "Closes #42\n"},
		{&IssueRef{Source: SourceGitHub, ID: "o/r#42"}, "Closes o/r#42\n"},
		{&IssueRef{Source: SourceBeads, ID: "decsr-ab1"}, "Refs decsr-ab1\n"},
		{nil, ""},
	} {
		body := PRBody(Session{Issue: tc.ref, Transcript: []Line{{Who: "you", Text: "do it"}}})
		if tc.want == "" {
			if strings.Contains(body, "Closes") || strings.Contains(body, "Refs") {
				t.Errorf("unexpected link in %q", body)
			}
			continue
		}
		if !strings.Contains("\n"+body, "\n"+tc.want) {
			t.Errorf("body lacks %q:\n%s", tc.want, body)
		}
	}
}

func TestSessionIssueJSONCompat(t *testing.T) {
	root := t.TempDir()
	s := NewSession()
	if b, _ := json.Marshal(s); strings.Contains(string(b), `"issue"`) {
		t.Fatalf("empty issue must be omitted: %s", b)
	}
	var old Session
	if err := json.Unmarshal([]byte(`{"id":"20260101-000000-abcd","title":"old"}`), &old); err != nil || old.Issue != nil {
		t.Fatalf("old session: %+v %v", old, err)
	}
	s.Issue = &IssueRef{Source: SourceGitHub, ID: "#1", Title: "t", URL: "u"}
	s.AddLine("you", "x")
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(root, s.ID)
	if err != nil || got.Issue == nil || got.Issue.ID != "#1" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}
