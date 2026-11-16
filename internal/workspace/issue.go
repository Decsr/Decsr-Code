package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Issue sources.
const (
	SourceGitHub = "github"
	SourceBeads  = "beads"
)

// maxIssueBody caps the issue body that is handed to the lead.
const maxIssueBody = 8 << 10

// Issue is a task fetched from an issue tracker.
type Issue struct {
	Source string // "github" | "beads"
	// ID is how the issue is named: "#42" for an issue of the current
	// repository, "owner/repo#42" for another one, the bead id for beads.
	ID     string
	Title  string
	Body   string
	URL    string
	Labels []string
	Closed bool
}

// IssueRef is what a session remembers about the issue it started from.
type IssueRef struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	URL    string `json:"url,omitempty"`
}

// Ref is the part of the issue a session keeps.
func (i Issue) Ref() *IssueRef {
	return &IssueRef{Source: i.Source, ID: i.ID, Title: i.Title, URL: i.URL}
}

// Summary is the one-line form: "Issue #42: Fix flaky login test".
func (i Issue) Summary() string { return "Issue " + i.ID + ": " + i.Title }

// Task is the text sent to the lead.
func (i Issue) Task() string {
	var b strings.Builder
	b.WriteString(i.Summary())
	if body := strings.TrimSpace(i.Body); body != "" {
		b.WriteString("\n\n" + body)
	}
	if len(i.Labels) > 0 {
		b.WriteString("\n\nLabels: " + strings.Join(i.Labels, ", "))
	}
	return b.String()
}

var (
	issueNumRe = regexp.MustCompile(`^#?(\d+)$`)
	issueURLRe = regexp.MustCompile(`^https?://[^/\s]+/([^/\s]+)/([^/\s]+)/issues/(\d+)(?:[/?#]\S*)?$`)
	beadIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// ParseIssueRef tells which tracker ref names: digits, "#N" and a GitHub issue
// URL are GitHub issues, anything else is a bead id.
func ParseIssueRef(ref string) (source, arg string, err error) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return "", "", errors.New("usage: /issue <number|URL|bead-id> [--force]")
	case issueNumRe.MatchString(ref):
		return SourceGitHub, issueNumRe.FindStringSubmatch(ref)[1], nil
	case issueURLRe.MatchString(ref):
		return SourceGitHub, ref, nil
	case strings.Contains(ref, "://"):
		return "", "", fmt.Errorf("%q is not a GitHub issue URL (expected https://github.com/<owner>/<repo>/issues/<number>)", ref)
	case beadIDRe.MatchString(ref):
		return SourceBeads, ref, nil
	}
	return "", "", fmt.Errorf("%q is not an issue number, issue URL or bead id", ref)
}

// FetchIssue loads the issue ref names. root is the project; run is the
// command runner (nil = ExecRunner).
func FetchIssue(ctx context.Context, root, ref string, run CmdRunner) (Issue, error) {
	source, arg, err := ParseIssueRef(ref)
	if err != nil {
		return Issue{}, err
	}
	if run == nil {
		run = ExecRunner
	}
	if source == SourceBeads {
		return fetchBead(ctx, root, arg, run)
	}
	return fetchGitHub(ctx, root, arg, run)
}

func fetchGitHub(ctx context.Context, root, arg string, run CmdRunner) (Issue, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	out, err := run(ctx, root, "gh", "issue", "view", arg, "--json", "number,title,body,labels,url,state")
	if err != nil {
		return Issue{}, ghIssueError(arg, err)
	}
	var v struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
		State  string `json:"state"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Number <= 0 {
		return Issue{}, fmt.Errorf("gh issue view %s: unexpected output", arg)
	}
	is := Issue{
		Source: SourceGitHub, ID: fmt.Sprintf("#%d", v.Number), Title: oneLineText(v.Title),
		Body: trimBody(v.Body), URL: v.URL, Closed: strings.EqualFold(v.State, "CLOSED"),
	}
	for _, l := range v.Labels {
		if l.Name != "" {
			is.Labels = append(is.Labels, l.Name)
		}
	}
	if m := issueURLRe.FindStringSubmatch(arg); m != nil {
		// A full URL may point to another repository. Name it as such unless
		// it is the current one.
		slug := m[1] + "/" + m[2]
		cur, cerr := run(ctx, root, "gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		if cerr != nil || !strings.EqualFold(strings.TrimSpace(cur), slug) {
			is.ID = fmt.Sprintf("%s#%d", slug, v.Number)
		}
	}
	return is, nil
}

func ghIssueError(arg string, err error) error {
	s := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return ErrGHMissing
	case strings.Contains(s, "gh auth login") || strings.Contains(s, "not logged") || strings.Contains(s, "authentication") || strings.Contains(s, "http 401"):
		return ErrGHAuth
	case strings.Contains(s, "could not resolve") || strings.Contains(s, "not found") || strings.Contains(s, "http 404"):
		return fmt.Errorf("issue %s not found (check the number and that gh points at the right repository)", arg)
	case strings.Contains(s, "not a git repository") || strings.Contains(s, "none of the git remotes"):
		return errors.New("this project has no GitHub remote: use the full issue URL")
	}
	return fmt.Errorf("gh issue view %s: %s", arg, lastLine(err.Error()))
}

func fetchBead(ctx context.Context, root, id string, run CmdRunner) (Issue, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	out, err := run(ctx, root, "bd", "show", id, "--json")
	if err != nil {
		s := strings.ToLower(err.Error())
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return Issue{}, errors.New("bd is not on PATH: install beads to start from a bead id")
		case strings.Contains(s, "no beads database"):
			return Issue{}, errors.New("this project has no beads database (run `bd init`)")
		case strings.Contains(s, "not found") || strings.Contains(s, "no issue"):
			return Issue{}, fmt.Errorf("bead %s not found", id)
		}
		return Issue{}, fmt.Errorf("bd show %s: %s", id, lastLine(err.Error()))
	}
	type bead struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Status      string   `json:"status"`
		Labels      []string `json:"labels"`
	}
	var list []bead
	data := []byte(strings.TrimSpace(out))
	if i := strings.IndexAny(string(data), "[{"); i > 0 {
		data = data[i:] // bd prints warnings before the JSON
	}
	if json.Unmarshal(data, &list) != nil {
		var one bead
		if err := json.Unmarshal(data, &one); err != nil {
			return Issue{}, fmt.Errorf("bd show %s: unexpected output", id)
		}
		list = []bead{one}
	}
	if len(list) == 0 || list[0].ID == "" {
		return Issue{}, fmt.Errorf("bead %s not found", id)
	}
	b := list[0]
	return Issue{
		Source: SourceBeads, ID: b.ID, Title: oneLineText(b.Title), Body: trimBody(b.Description),
		Labels: b.Labels, Closed: strings.EqualFold(b.Status, "closed"),
	}, nil
}

func oneLineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// trimBody normalises line ends and caps the body at about 8 KB.
func trimBody(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if len(s) <= maxIssueBody {
		return s
	}
	s = s[:maxIssueBody]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return strings.TrimSpace(s) + "\n… (issue body trimmed)"
}

// issueClosesLine is the line a pull request body carries to link the issue.
func issueClosesLine(r *IssueRef) string {
	if r == nil || r.ID == "" {
		return ""
	}
	switch {
	case r.Source == SourceBeads:
		return "Refs " + r.ID
	case strings.Contains(r.ID, "#"):
		return "Closes " + r.ID
	}
	return ""
}
