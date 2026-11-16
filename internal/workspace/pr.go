package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

// Limits of the pull request loop.
const (
	defaultPRPoll    = 60 * time.Second
	minPRPoll        = 15 * time.Second
	defaultMaxCIFix  = 3
	maxCITailLines   = 150
	maxCITailBytes   = 8 << 10
	maxCommentBytes  = 400
	maxPRTitleRunes  = 72
	maxPRTaskRunes   = 2000
	maxRunnerOutput  = 4 << 20
	cmdTimeout       = 60 * time.Second
	pushTimeout      = 3 * time.Minute
	defaultPRRemote  = "origin"
	prCommentSeenCap = 2000
)

// Errors of the pull request preconditions that are shown as they are.
var (
	ErrGHMissing     = errors.New("the GitHub CLI (gh) is not on PATH: install it from https://cli.github.com")
	ErrGHAuth        = errors.New("gh is not logged in: run `gh auth login`")
	ErrPRNothingDone = errors.New("no node of this session has been merged yet: run a node first")
)

// CmdRunner runs the program name (gh or git) with args in dir and returns its
// stdout. On a failure it returns the stdout read so far and an error that
// carries stderr. Tests replace it with a fake; nothing else talks to GitHub.
type CmdRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// ExecRunner is the real CmdRunner. It never prompts: a missing login or
// credential is an error, not a hang.
func ExecRunner(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := executil.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	var so, se bytes.Buffer
	cmd.Stdout = &limitWriter{w: &so, left: maxRunnerOutput}
	cmd.Stderr = &limitWriter{w: &se, left: maxRunnerOutput}
	err := cmd.Run()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		msg := strings.TrimSpace(se.String())
		if msg == "" {
			msg = strings.TrimSpace(so.String())
		}
		return so.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return so.String(), nil
}

// limitWriter keeps at most left bytes and drops the rest.
type limitWriter struct {
	w    *bytes.Buffer
	left int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > l.left {
		p = p[:l.left]
	}
	l.left -= len(p)
	l.w.Write(p)
	return n, nil
}

// PRState is what a session remembers about its pull request.
type PRState struct {
	Number int    `json:"number,omitempty"`
	URL    string `json:"url,omitempty"`
	// SeenComments are the review comments and reviews already turned into a
	// fix node ("c<id>" and "r<id>").
	SeenComments []string `json:"seen_comments,omitempty"`
	// CIFixes is the number of CI fix rounds started.
	CIFixes int `json:"ci_fixes,omitempty"`
	// FixedSHA is the head commit whose failing checks were already handled.
	FixedSHA string `json:"fixed_sha,omitempty"`
}

func (s *PRState) seen(id string) bool {
	for _, x := range s.SeenComments {
		if x == id {
			return true
		}
	}
	return false
}

// PRConfig is the part of the configuration the loop needs.
type PRConfig struct {
	Base, Remote string
	PollSeconds  int
	MaxCIFixes   int
}

// PollInterval is the polling period: 60 s by default, at least 15 s.
func (c PRConfig) PollInterval() time.Duration {
	d := time.Duration(c.PollSeconds) * time.Second
	if d <= 0 {
		return defaultPRPoll
	}
	if d < minPRPoll {
		return minPRPoll
	}
	return d
}

// MaxFixes is the number of CI fix rounds allowed.
func (c PRConfig) MaxFixes() int {
	if c.MaxCIFixes <= 0 {
		return defaultMaxCIFix
	}
	return c.MaxCIFixes
}

// PRClient pushes the session branch and talks to GitHub through gh.
type PRClient struct {
	Root    string // the user's checkout
	Session string
	Cfg     PRConfig
	Run     CmdRunner // nil = ExecRunner
}

func (c *PRClient) run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r := c.Run
	if r == nil {
		r = ExecRunner
	}
	out, err := r(ctx, c.Root, name, args...)
	return out, err
}

func (c *PRClient) branch() string { return SessionBranch(c.Session) }

// PRTarget is where the session branch goes.
type PRTarget struct {
	Base, Remote, Branch string
}

// Prepare checks what /pr needs and returns the target: a merged node, a
// logged-in gh, the remote and a base branch that differs from the session
// branch. "Isolation active" and "no node running" are the caller's checks.
func (c *PRClient) Prepare(ctx context.Context) (PRTarget, error) {
	t := PRTarget{Branch: c.branch()}
	if !hasMergedNode(c.Root, c.Session) {
		return t, ErrPRNothingDone
	}
	if _, err := c.run(ctx, cmdTimeout, "gh", "auth", "status"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return t, ErrGHMissing
		}
		return t, ErrGHAuth
	}
	t.Remote = c.Cfg.Remote
	if t.Remote == "" {
		t.Remote = defaultPRRemote
	}
	if _, err := c.run(ctx, cmdTimeout, "git", "remote", "get-url", t.Remote); err != nil {
		return t, fmt.Errorf("no git remote %q: add one with `git remote add %s <url>` or set workspace.pr_remote", t.Remote, t.Remote)
	}
	t.Base = c.Cfg.Base
	if t.Base == "" {
		cur, err := c.run(ctx, cmdTimeout, "git", "rev-parse", "--abbrev-ref", "HEAD")
		cur = strings.TrimSpace(cur)
		if err != nil || cur == "" || cur == "HEAD" {
			return t, errors.New("your checkout is not on a branch: check one out or set workspace.pr_base")
		}
		t.Base = cur
	}
	if t.Base == t.Branch {
		return t, fmt.Errorf("the base branch is the session branch %s: check out your own branch first", t.Branch)
	}
	return t, nil
}

// Push publishes the session branch. It never forces: the branch only gains
// merge commits, so a rejected push means someone else pushed to it, and their
// commits must not be overwritten.
func (c *PRClient) Push(ctx context.Context, t PRTarget) error {
	_, err := c.run(ctx, pushTimeout, "git", "push", "-u", t.Remote, t.Branch)
	if err != nil && rejectedPush(err) {
		return fmt.Errorf("the PR branch %s has commits that are not in your session (someone pushed to it) — pull them into the session branch or push manually; nothing was overwritten", t.Branch)
	}
	if err != nil {
		return fmt.Errorf("push %s to %s: %s", t.Branch, t.Remote, lastLine(err.Error()))
	}
	return nil
}

func rejectedPush(err error) bool {
	s := err.Error()
	return strings.Contains(s, "non-fast-forward") || strings.Contains(s, "fetch first") || strings.Contains(s, "[rejected]")
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// PRResult is the outcome of EnsurePR.
type PRResult struct {
	State   PRState
	Created bool
}

var prURLRe = regexp.MustCompile(`https?://\S+/pull/(\d+)`)

// EnsurePR pushes the session branch and returns its pull request: the open
// one when it exists, else a new one built from sess.
func (c *PRClient) EnsurePR(ctx context.Context, t PRTarget, sess Session) (PRResult, error) {
	if err := c.Push(ctx, t); err != nil {
		return PRResult{}, err
	}
	st := PRState{}
	if sess.PR != nil {
		st = *sess.PR
	}
	if out, err := c.run(ctx, cmdTimeout, "gh", "pr", "view", t.Branch, "--json", "number,url,state"); err == nil {
		var v struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			State  string `json:"state"`
		}
		if json.Unmarshal([]byte(out), &v) == nil && v.Number > 0 && strings.EqualFold(v.State, "OPEN") {
			if st.Number != v.Number {
				st = PRState{}
			}
			st.Number, st.URL = v.Number, v.URL
			return PRResult{State: st}, nil
		}
	}
	f, err := os.CreateTemp("", "decsr-pr-*.md")
	if err != nil {
		return PRResult{}, err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, werr := f.WriteString(PRBody(sess))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return PRResult{}, werr
	}
	out, err := c.run(ctx, cmdTimeout, "gh", "pr", "create", "--head", t.Branch, "--base", t.Base, "--title", PRTitle(sess), "--body-file", f.Name())
	if err != nil {
		return PRResult{}, fmt.Errorf("gh pr create: %s", lastLine(err.Error()))
	}
	m := prURLRe.FindStringSubmatch(out)
	if m == nil {
		return PRResult{}, fmt.Errorf("gh pr create printed no pull request URL: %s", lastLine(out))
	}
	n, _ := strconv.Atoi(m[1])
	return PRResult{State: PRState{Number: n, URL: m[0]}, Created: true}, nil
}

// sessionTask is the first task the user gave: the first "you" line, else the
// graph's task.
func sessionTask(s Session) string {
	for _, l := range s.Transcript {
		if l.Who == "you" && strings.TrimSpace(l.Text) != "" {
			return strings.TrimSpace(l.Text)
		}
	}
	if s.Graph != nil {
		return strings.TrimSpace(s.Graph.Task)
	}
	return ""
}

func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// PRTitle is the first line of the session's first task, at most 72 characters.
func PRTitle(s Session) string {
	t := sessionTask(s)
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[:i]
	}
	t = strings.TrimSpace(t)
	if t == "" {
		return "decsr session " + s.ID
	}
	return truncRunes(t, maxPRTitleRunes)
}

func cell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}

// PRBody is a plain Markdown summary of the session: task, nodes, what each
// finished node did and the Build & test result.
func PRBody(s Session) string {
	var b strings.Builder
	if t := sessionTask(s); t != "" {
		b.WriteString("## Task\n\n" + truncRunes(t, maxPRTaskRunes) + "\n\n")
	}
	if l := issueClosesLine(s.Issue); l != "" {
		b.WriteString(l + "\n\n")
	}
	if s.Graph == nil {
		return b.String()
	}
	var check *Node
	b.WriteString("## Nodes\n\n| ID | Title | Role | Status | Files |\n|---|---|---|---|---|\n")
	for _, n := range s.Graph.Nodes {
		if n.Kind == KindCheck {
			check = n
		}
		role := n.Role
		if role == "" {
			role = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d |\n", cell(n.ID), cell(n.Title), cell(role), n.Status, len(n.AllFiles()))
	}
	var sum []string
	for _, n := range s.Graph.Nodes {
		if n.Kind == KindWork && n.Status == StatusDone && strings.TrimSpace(n.Summary) != "" {
			sum = append(sum, fmt.Sprintf("- %s: %s", n.ID, cell(n.Summary)))
		}
	}
	if len(sum) > 0 {
		b.WriteString("\n## Changes\n\n" + strings.Join(sum, "\n") + "\n")
	}
	var open []string
	for _, n := range s.Graph.Nodes {
		if n.Status != StatusWaiting {
			continue
		}
		what := n.Summary
		if n.Report != nil && n.Report.Ask() != "" {
			what = n.Report.Ask()
		}
		open = append(open, fmt.Sprintf("- %s: %s", n.ID, cell(truncRunes(what, 300))))
	}
	if len(open) > 0 {
		b.WriteString("\n## Waiting for an answer\n\n" + strings.Join(open, "\n") + "\n")
	}
	b.WriteString("\n## Build & test\n\n")
	switch {
	case check == nil:
		b.WriteString("Not run.\n")
	case check.Status == StatusDone:
		b.WriteString("Passed.\n")
	case check.Status == StatusFailed:
		b.WriteString("Failed: " + cell(truncRunes(check.Error, 300)) + "\n")
	default:
		b.WriteString("Not run.\n")
	}
	return b.String()
}

// Check is one CI check of a pull request (gh pr checks --json).
type Check struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Bucket string `json:"bucket"` // pass | fail | pending | skipping | cancel
	Link   string `json:"link"`
}

// Checks lists the checks of a pull request. gh exits non-zero while checks
// fail or are pending but still prints the JSON, so stdout wins over the exit
// status; "no checks reported" is an empty list.
func (c *PRClient) Checks(ctx context.Context, number int) ([]Check, error) {
	out, err := c.run(ctx, cmdTimeout, "gh", "pr", "checks", strconv.Itoa(number), "--json", "name,state,bucket,link")
	var list []Check
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &list); jerr == nil {
		return list, nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no checks reported") {
			return nil, nil
		}
		return nil, fmt.Errorf("gh pr checks: %s", lastLine(err.Error()))
	}
	return nil, errors.New("gh pr checks: unreadable output")
}

// HeadSHA is the head commit of the pull request.
func (c *PRClient) HeadSHA(ctx context.Context, number int) (string, error) {
	out, err := c.run(ctx, cmdTimeout, "gh", "pr", "view", strconv.Itoa(number), "--json", "headRefOid")
	if err != nil {
		return "", fmt.Errorf("gh pr view: %s", lastLine(err.Error()))
	}
	var v struct {
		Oid string `json:"headRefOid"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Oid == "" {
		return "", errors.New("gh pr view: no headRefOid")
	}
	return v.Oid, nil
}

var runIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// tailLog keeps the last lines of a log, at most maxCITailBytes.
func tailLog(s string) string {
	s = strings.TrimSpace(SanitizeText(s))
	lines := strings.Split(s, "\n")
	if len(lines) > maxCITailLines {
		lines = lines[len(lines)-maxCITailLines:]
	}
	s = strings.Join(lines, "\n")
	if len(s) > maxCITailBytes {
		s = s[len(s)-maxCITailBytes:]
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:]
		}
	}
	return s
}

// failedLogs fetches the failed-step logs of the Actions runs behind the
// failing checks, once per run.
func (c *PRClient) failedLogs(ctx context.Context, failing []Check) string {
	var parts []string
	done := map[string]bool{}
	for _, ch := range failing {
		m := runIDRe.FindStringSubmatch(ch.Link)
		if m == nil {
			parts = append(parts, fmt.Sprintf("Check %s failed; no log available (see %s).", ch.Name, ch.Link))
			continue
		}
		if done[m[1]] {
			continue
		}
		done[m[1]] = true
		out, err := c.run(ctx, cmdTimeout, "gh", "run", "view", m[1], "--log-failed")
		if err != nil || strings.TrimSpace(out) == "" {
			parts = append(parts, fmt.Sprintf("Check %s failed; the log could not be read (see %s).", ch.Name, ch.Link))
			continue
		}
		parts = append(parts, tailLog(out))
	}
	return strings.Join(parts, "\n\n")
}

// ReviewItem is one piece of review feedback.
type ReviewItem struct {
	Key  string // "c<id>" for a line comment, "r<id>" for a review
	Text string // "path:line — body" or "review — body"
}

// NewFeedback returns the line comments and CHANGES_REQUESTED reviews of the
// pull request that are not in st.SeenComments yet, oldest first.
func (c *PRClient) NewFeedback(ctx context.Context, st PRState) ([]ReviewItem, error) {
	n := strconv.Itoa(st.Number)
	var items []ReviewItem
	out, err := c.run(ctx, cmdTimeout, "gh", "api", "repos/{owner}/{repo}/pulls/"+n+"/comments?per_page=100")
	if err != nil {
		return nil, fmt.Errorf("gh api comments: %s", lastLine(err.Error()))
	}
	var comments []struct {
		ID           int64  `json:"id"`
		Path         string `json:"path"`
		Line         int    `json:"line"`
		OriginalLine int    `json:"original_line"`
		Body         string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &comments); err != nil {
		return nil, fmt.Errorf("gh api comments: %w", err)
	}
	for _, cm := range comments {
		key := "c" + strconv.FormatInt(cm.ID, 10)
		if st.seen(key) {
			continue
		}
		line := cm.Line
		if line == 0 {
			line = cm.OriginalLine
		}
		where := cm.Path
		if line > 0 {
			where += ":" + strconv.Itoa(line)
		}
		items = append(items, ReviewItem{Key: key, Text: where + " — " + feedbackText(cm.Body)})
	}
	out, err = c.run(ctx, cmdTimeout, "gh", "api", "repos/{owner}/{repo}/pulls/"+n+"/reviews?per_page=100")
	if err != nil {
		return nil, fmt.Errorf("gh api reviews: %s", lastLine(err.Error()))
	}
	var reviews []struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal([]byte(out), &reviews); err != nil {
		return nil, fmt.Errorf("gh api reviews: %w", err)
	}
	for _, r := range reviews {
		key := "r" + strconv.FormatInt(r.ID, 10)
		if r.State != "CHANGES_REQUESTED" || strings.TrimSpace(r.Body) == "" || st.seen(key) {
			continue
		}
		items = append(items, ReviewItem{Key: key, Text: "review (changes requested) — " + feedbackText(r.Body)})
	}
	return items, nil
}

func feedbackText(s string) string {
	return truncRunes(strings.Join(strings.Fields(SanitizeText(s)), " "), maxCommentBytes)
}

// FixRequest is a fix node the loop wants.
type FixRequest struct {
	Title, Desc string
}

// PollResult is what one poll found.
type PollResult struct {
	HeadSHA   string
	Passed    bool        // every check passed (and there is at least one)
	Failed    []string    // names of the failing checks
	CIFix     *FixRequest // a new CI fix node is due
	ReviewFix *FixRequest // a new review fix node is due
	Exhausted bool        // checks fail on a new head but max_ci_fixes is used up
}

// Poll looks at the checks and the review feedback once. It returns the
// updated state; the caller saves it and adds the nodes.
func (c *PRClient) Poll(ctx context.Context, st PRState) (PRState, PollResult, error) {
	var res PollResult
	if st.Number == 0 {
		return st, res, errors.New("the session has no pull request")
	}
	sha, err := c.HeadSHA(ctx, st.Number)
	if err != nil {
		return st, res, err
	}
	res.HeadSHA = sha
	checks, err := c.Checks(ctx, st.Number)
	if err != nil {
		return st, res, err
	}
	var failing []Check
	good := len(checks) > 0
	for _, ch := range checks {
		switch ch.Bucket {
		case "fail":
			failing = append(failing, ch)
			res.Failed = append(res.Failed, ch.Name)
			good = false
		case "pass", "skipping":
		default:
			good = false
		}
	}
	res.Passed = good
	if len(failing) > 0 && st.FixedSHA != sha {
		st.FixedSHA = sha
		if st.CIFixes >= c.Cfg.MaxFixes() {
			res.Exhausted = true
		} else {
			st.CIFixes++
			names := strings.Join(res.Failed, ", ")
			res.CIFix = &FixRequest{
				Title: "Fix CI: " + truncRunes(names, 60),
				Desc: fmt.Sprintf("The CI checks of pull request #%d failed (%s). Change the code so they pass. Tail of the failing log:\n\n%s",
					st.Number, names, c.failedLogs(ctx, failing)),
			}
		}
	}
	items, err := c.NewFeedback(ctx, st)
	if err != nil {
		return st, res, err
	}
	if len(items) > 0 {
		var lines []string
		for _, it := range items {
			lines = append(lines, "- "+it.Text)
			st.SeenComments = append(st.SeenComments, it.Key)
		}
		if over := len(st.SeenComments) - prCommentSeenCap; over > 0 {
			st.SeenComments = append([]string(nil), st.SeenComments[over:]...)
		}
		res.ReviewFix = &FixRequest{
			Title: "Address review comments",
			Desc:  fmt.Sprintf("Reviewers left feedback on pull request #%d. Address each point:\n\n%s", st.Number, strings.Join(lines, "\n")),
		}
	}
	return st, res, nil
}

// IsFixID reports whether id is the ID of a fix node ("F" and digits).
func IsFixID(id string) bool { return isFixID(id) }
