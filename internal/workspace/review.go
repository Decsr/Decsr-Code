package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/prompts"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const (
	// MaxReviewRounds is the number of lead reviews per task that start by
	// themselves.
	MaxReviewRounds = 5
	// MaxProposals is the most proposals one review may apply.
	MaxProposals = 8
	// MaxReviews is how many reviews a session keeps.
	MaxReviews = 20
	// ReviewExcerptChars is the budget of the failure excerpt in the review prompt.
	ReviewExcerptChars = 6000
)

// Proposal actions.
const (
	ActionAdd    = "add"
	ActionChange = "change"
	ActionCancel = "cancel"
)

// Review states.
const (
	ReviewPending  = "pending"
	ReviewApplied  = "applied"
	ReviewRejected = "rejected"
	ReviewEmpty    = "empty"
)

// Proposal is one change the lead suggests to the graph.
type Proposal struct {
	Action      string   `json:"action"`
	ID          string   `json:"id,omitempty"` // change, cancel
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Role        string   `json:"role,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	// SetDeps is true when the lead gave depends_on, which for a change
	// replaces the dependencies (also with an empty list).
	SetDeps bool   `json:"set_deps,omitempty"`
	Reason  string `json:"reason,omitempty"` // cancel
}

// leadProposal reads a proposal as the lead wrote it: a few alternative key
// names are accepted and the text is cleaned. Stored proposals use Proposal's
// own tags.
type leadProposal Proposal

// UnmarshalJSON implements json.Unmarshaler.
func (p *leadProposal) UnmarshalJSON(data []byte) error {
	var raw struct {
		Action      string    `json:"action"`
		Type        string    `json:"type"`
		ID          string    `json:"id"`
		Node        string    `json:"node"`
		NodeID      string    `json:"node_id"`
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Role        string    `json:"role"`
		DependsOn   *[]string `json:"depends_on"`
		DependsOnC  *[]string `json:"dependsOn"`
		Reason      string    `json:"reason"`
		SetDeps     bool      `json:"set_deps"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	first := func(vs ...string) string {
		for _, v := range vs {
			if strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	p.Action = strings.ToLower(strings.TrimSpace(first(raw.Action, raw.Type)))
	p.ID = upperKey(first(raw.ID, raw.Node, raw.NodeID))
	p.Title = Sanitize(raw.Title)
	p.Description = strings.TrimSpace(SanitizeText(raw.Description))
	p.Role = strings.TrimSpace(Sanitize(raw.Role))
	p.Reason = Sanitize(raw.Reason)
	deps := raw.DependsOn
	if deps == nil {
		deps = raw.DependsOnC
	}
	p.SetDeps = raw.SetDeps
	if deps != nil {
		p.SetDeps = true
		for _, d := range *deps {
			if d = upperKey(d); d != "" {
				p.DependsOn = append(p.DependsOn, d)
			}
		}
	}
	return nil
}

// AppliedProposal is a proposal that was (or would be) applied. NodeID is the
// node it touched or created; Text is one line telling what happened.
type AppliedProposal struct {
	Proposal
	NodeID string `json:"node_id,omitempty"`
	Text   string `json:"text"`
}

// SkippedProposal is a proposal that is not valid, with the reason.
type SkippedProposal struct {
	Proposal
	Reason string `json:"skip_reason"`
}

// Review is one lead review kept in the session.
type Review struct {
	Round     int               `json:"round"`
	At        time.Time         `json:"at"`
	Note      string            `json:"note,omitempty"`
	Proposals []Proposal        `json:"proposals,omitempty"`
	Applied   []AppliedProposal `json:"applied,omitempty"`
	Skipped   []SkippedProposal `json:"skipped,omitempty"`
	State     string            `json:"state"` // pending | applied | rejected | empty
}

// ReviewResult is what the lead answered.
type ReviewResult struct {
	Note      string
	Proposals []Proposal
}

// ParseReview reads the lead's answer: a JSON object, bare or in a code fence,
// with "note" and "proposals". An answer that cannot be read gives no
// proposals and a note saying so.
func ParseReview(raw string) ReviewResult {
	for start := 0; start < len(raw); start++ {
		if raw[start] != '{' {
			continue
		}
		end := matchBrace(raw, start)
		if end < 0 {
			continue
		}
		var v struct {
			Note      string          `json:"note"`
			Proposals *[]leadProposal `json:"proposals"`
		}
		if err := json.Unmarshal([]byte(dropTrailingCommas(raw[start:end+1])), &v); err != nil {
			continue
		}
		if v.Proposals == nil && strings.TrimSpace(v.Note) == "" {
			continue
		}
		res := ReviewResult{Note: Sanitize(v.Note)}
		if v.Proposals != nil {
			for _, p := range *v.Proposals {
				res.Proposals = append(res.Proposals, Proposal(p))
			}
		}
		return res
	}
	return ReviewResult{Note: "the lead's answer was not valid JSON: no changes proposed"}
}

// matchBrace returns the index of the "}" matching the "{" at start, skipping
// strings, or -1.
func matchBrace(s string, start int) int {
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ── Prompt ───────────────────────────────────────────────────────────────────

// ReviewInput is what the lead sees when it reviews a batch.
type ReviewInput struct {
	Task    string
	Summary string // the session summary; "" for none
	Graph   *Graph
	Roles   []Role
	// Detail returns the full detail of an activity (Engine.ActivityDetail); nil
	// uses the stored details.
	Detail func(nodeID, actID string) (string, bool)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n-1]) + "…"
	}
	return s
}

// reviewTable is the compact node table of the review prompt.
func reviewTable(g *Graph) string {
	var sb strings.Builder
	for _, n := range g.Nodes {
		role := n.Role
		if role == "" {
			role = "unassigned"
			if n.SuggestedRole != "" {
				role += " (suggested " + n.SuggestedRole + ")"
			}
		}
		kind := "work"
		if n.Kind == KindCheck {
			kind = "check"
		}
		fmt.Fprintf(&sb, "- %s | %s | %s | %s | %s", n.ID, clip(n.Title, 80), kind, role, n.Status)
		if len(n.DependsOn) > 0 {
			sb.WriteString(" | after " + strings.Join(n.DependsOn, ","))
		}
		if r := n.Report; r != nil {
			sb.WriteString(" | report " + r.Status)
			if s := clip(r.Summary, 240); s != "" {
				sb.WriteString(": " + s)
			}
			if q := clip(r.Question, 240); q != "" {
				sb.WriteString(" | question: " + q)
			}
		} else if s := clip(n.Summary, 240); s != "" {
			sb.WriteString(" | summary: " + s)
		}
		if e := strings.TrimSpace(n.Error); e != "" {
			line, _, _ := strings.Cut(e, "\n")
			sb.WriteString(" | error: " + clip(line, 160))
		}
		fmt.Fprintf(&sb, " | files %d\n", len(n.AllFiles()))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// BuildReviewPrompt assembles the lead's system prompt for a review.
func BuildReviewPrompt(in ReviewInput) string {
	var roles strings.Builder
	for _, r := range in.Roles {
		if r.ReadOnly {
			fmt.Fprintf(&roles, "- %s: %s (read-only)\n", r.Name, r.Description)
			continue
		}
		fmt.Fprintf(&roles, "- %s: %s\n", r.Name, r.Description)
	}
	table, failures := "", "(nothing failed)"
	if in.Graph != nil {
		table = reviewTable(in.Graph)
		var failed []*Node
		for _, n := range in.Graph.Nodes {
			if n.Status == StatusFailed && n.Error != "stopped" {
				failed = append(failed, n)
			}
		}
		if ex := FixExcerptWith(failed, ReviewExcerptChars, in.Detail); ex != "" {
			failures = ex
		}
	}
	summary := strings.TrimSpace(in.Summary)
	if summary == "" {
		summary = "(none)"
	}
	return strings.NewReplacer(
		"{{ROLES}}", strings.TrimRight(roles.String(), "\n"),
		"{{TASK}}", strings.TrimSpace(in.Task),
		"{{SUMMARY}}", summary,
		"{{NODES}}", table,
		"{{FAILURES}}", failures,
	).Replace(prompts.MustLoad("ws-review"))
}

// RunReview asks the lead to review the graph and parses the answer. The
// returned usage is what the call cost.
func RunReview(ctx context.Context, r runner.LLMRunner, model string, in ReviewInput, onToken func(string)) (ReviewResult, runner.TokenUsage, error) {
	req := runner.CompletionRequest{
		SystemPrompt: BuildReviewPrompt(in),
		Model:        model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: "Review the graph now and answer with the JSON object."}},
	}
	raw, usage, err := splitComplete(ctx, r, req, onToken)
	if err != nil {
		return ReviewResult{}, usage, fmt.Errorf("review: %s", strings.TrimPrefix(err.Error(), "split: "))
	}
	return ParseReview(raw), usage, nil
}
