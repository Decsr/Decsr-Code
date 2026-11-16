package workspace

import (
	"fmt"
	"strings"
)

// ApplyOptions tells ApplyProposals which roles exist and where the numbers of
// new nodes start. NextID is updated to the next free number.
type ApplyOptions struct {
	// KnownRole reports whether a role is defined. A proposal naming another
	// role leaves the node unassigned with that role as its suggestion. Nil
	// means no role is defined.
	KnownRole func(string) bool
	NextID    int
}

func (o *ApplyOptions) role(s string) (role string, defined bool) {
	name := strings.TrimSpace(s)
	if name == "" {
		return "", false
	}
	known := func(r string) bool { return o != nil && o.KnownRole != nil && o.KnownRole(r) }
	if known(name) {
		return name, true
	}
	n := normalizeRole(name)
	return n, known(n)
}

// ApplyProposals applies the proposals to g in order and returns what was
// applied and what was skipped, with the reason. Rules: only the first
// MaxProposals count; a change needs a todo or failed work node; a cancel a
// todo, failed or waiting work node; an add may depend only on existing nodes;
// a done, running or CHECK node is never touched. A new node makes CHECK
// depend on it (and resets CHECK to todo); a cancelled node is removed and its
// edges are dropped. A changed failed node goes back to todo.
func (g *Graph) ApplyProposals(p []Proposal, opts *ApplyOptions) (applied []AppliedProposal, skipped []SkippedProposal) {
	if opts == nil {
		opts = &ApplyOptions{}
	}
	skip := func(pr Proposal, format string, a ...any) {
		skipped = append(skipped, SkippedProposal{Proposal: pr, Reason: fmt.Sprintf(format, a...)})
	}
	for i, pr := range p {
		if i >= MaxProposals {
			skip(pr, "more than %d proposals in one review", MaxProposals)
			continue
		}
		var ap AppliedProposal
		var reason string
		switch pr.Action {
		case ActionAdd:
			ap, reason = g.proposeAdd(pr, opts)
		case ActionChange:
			ap, reason = g.proposeChange(pr, opts)
		case ActionCancel:
			ap, reason = g.proposeCancel(pr)
		default:
			reason = fmt.Sprintf("unknown action %q", pr.Action)
		}
		if reason != "" {
			skip(pr, "%s", reason)
			continue
		}
		applied = append(applied, ap)
	}
	return applied, skipped
}

// checkDeps validates and de-duplicates dependencies for the node id ("" for a
// new node).
func (g *Graph) checkDeps(id string, deps []string) ([]string, string) {
	var out []string
	seen := map[string]bool{}
	for _, d := range deps {
		switch {
		case seen[d]:
			continue
		case d == id:
			return nil, id + " cannot depend on itself"
		case d == "CHECK":
			return nil, "nothing can depend on CHECK"
		case g.Node(d) == nil:
			return nil, "depends on " + d + ", which does not exist"
		case id != "" && g.reaches(d, id):
			return nil, "depends on " + d + ", which would make a cycle"
		}
		seen[d] = true
		out = append(out, d)
	}
	return out, ""
}

// reaches reports whether target is among the dependencies of from, direct or
// indirect.
func (g *Graph) reaches(from, target string) bool {
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == target {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		n := g.Node(id)
		if n == nil {
			return false
		}
		for _, d := range n.DependsOn {
			if walk(d) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

func (g *Graph) proposeAdd(pr Proposal, opts *ApplyOptions) (AppliedProposal, string) {
	title := strings.Join(strings.Fields(pr.Title), " ")
	if title == "" {
		return AppliedProposal{}, "an add needs a title"
	}
	deps, reason := g.checkDeps("", pr.DependsOn)
	if reason != "" {
		return AppliedProposal{}, reason
	}
	desc := strings.TrimSpace(pr.Description)
	if desc == "" {
		desc = title
	}
	next := max(opts.NextID, g.nextWorkNumber())
	n := &Node{ID: fmt.Sprintf("T%d", next), Title: title, Description: desc, Kind: KindWork,
		Status: StatusTodo, DependsOn: deps}
	if role, defined := opts.role(pr.Role); defined {
		n.Role = role
	} else {
		n.SuggestedRole = role
	}
	opts.NextID = next + 1
	g.Nodes = append(g.Nodes, n)
	if c := g.Node("CHECK"); c != nil && c.Kind == KindCheck && c.Status != StatusRunning {
		c.Status = StatusTodo
		c.Error = ""
		c.DependsOn = append(c.DependsOn, n.ID)
	}
	return AppliedProposal{Proposal: pr, NodeID: n.ID, Text: fmt.Sprintf("added %s %s", n.ID, title)}, ""
}

func (g *Graph) proposeChange(pr Proposal, opts *ApplyOptions) (AppliedProposal, string) {
	n := g.Node(pr.ID)
	switch {
	case pr.ID == "":
		return AppliedProposal{}, "a change needs an id"
	case n == nil:
		return AppliedProposal{}, pr.ID + " does not exist"
	case n.Kind != KindWork:
		return AppliedProposal{}, pr.ID + " is the Build & test node"
	case n.Status != StatusTodo && n.Status != StatusFailed:
		return AppliedProposal{}, fmt.Sprintf("%s is %s and cannot be changed", n.ID, n.Status)
	}
	title := strings.Join(strings.Fields(pr.Title), " ")
	desc := strings.TrimSpace(pr.Description)
	if title == "" && desc == "" && pr.Role == "" && !pr.SetDeps {
		return AppliedProposal{}, "nothing to change"
	}
	var deps []string
	if pr.SetDeps {
		var reason string
		if deps, reason = g.checkDeps(n.ID, pr.DependsOn); reason != "" {
			return AppliedProposal{}, reason
		}
	}
	var fields []string
	if title != "" && title != n.Title {
		n.Title = title
		fields = append(fields, "title")
	}
	if desc != "" && desc != n.Description {
		n.Description = desc
		fields = append(fields, "description")
	}
	if pr.Role != "" {
		role, defined := opts.role(pr.Role)
		if defined {
			n.Role, n.SuggestedRole = role, ""
		} else {
			n.Role, n.SuggestedRole = "", role
		}
		fields = append(fields, "role")
	}
	if pr.SetDeps {
		n.DependsOn = deps
		fields = append(fields, "dependencies")
	}
	if len(fields) == 0 {
		return AppliedProposal{}, "nothing to change"
	}
	if n.Status == StatusFailed {
		n.Status, n.Error = StatusTodo, ""
	}
	return AppliedProposal{Proposal: pr, NodeID: n.ID,
		Text: fmt.Sprintf("changed %s (%s)", n.ID, strings.Join(fields, ", "))}, ""
}

func (g *Graph) proposeCancel(pr Proposal) (AppliedProposal, string) {
	n := g.Node(pr.ID)
	switch {
	case pr.ID == "":
		return AppliedProposal{}, "a cancel needs an id"
	case n == nil:
		return AppliedProposal{}, pr.ID + " does not exist"
	case n.Kind != KindWork:
		return AppliedProposal{}, pr.ID + " is the Build & test node"
	case n.Status != StatusTodo && n.Status != StatusFailed && n.Status != StatusWaiting:
		return AppliedProposal{}, fmt.Sprintf("%s is %s and cannot be cancelled", n.ID, n.Status)
	}
	g.RemovedCostUSD += n.CostUSD
	kept := g.Nodes[:0:0]
	for _, x := range g.Nodes {
		if x == n {
			continue
		}
		var deps []string
		for _, d := range x.DependsOn {
			if d != n.ID {
				deps = append(deps, d)
			}
		}
		x.DependsOn = deps
		kept = append(kept, x)
	}
	g.Nodes = kept
	text := "cancelled " + n.ID
	if pr.Reason != "" {
		text += ": " + pr.Reason
	}
	return AppliedProposal{Proposal: pr, NodeID: n.ID, Text: text}, ""
}

// CancelSummary adds a line "- T5 cancelled by the lead: <reason>" to prev for
// every cancel among applied.
func CancelSummary(prev string, applied []AppliedProposal) string {
	var lines []string
	if p := strings.TrimSpace(prev); p != "" {
		lines = strings.Split(p, "\n")
	}
	added := false
	for _, a := range applied {
		if a.Action != ActionCancel {
			continue
		}
		line := "- " + a.NodeID + " cancelled by the lead"
		if a.Reason != "" {
			line += ": " + a.Reason
		}
		lines = append(lines, line)
		added = true
	}
	if !added {
		return prev
	}
	return trimSummary(lines)
}

// ── Engine ───────────────────────────────────────────────────────────────────

// ApplyProposals applies proposals to the live graph and saves it. It works
// while other nodes run: only nodes that are not running can be touched.
func (e *Engine) ApplyProposals(p []Proposal, opts *ApplyOptions) ([]AppliedProposal, []SkippedProposal) {
	e.mu.Lock()
	applied, skipped := e.g.ApplyProposals(p, opts)
	var err error
	if len(applied) > 0 {
		err = e.saveLocked()
	}
	e.mu.Unlock()
	e.reportSave(err)
	return applied, skipped
}

// PreviewProposals is ApplyProposals on a copy: it changes nothing.
func (e *Engine) PreviewProposals(p []Proposal, opts *ApplyOptions) ([]AppliedProposal, []SkippedProposal) {
	e.mu.Lock()
	c := e.g.Clone()
	e.mu.Unlock()
	return c.ApplyProposals(p, opts)
}

// StartReview counts a lead review of the current task and returns the number
// of reviews so far.
func (e *Engine) StartReview() int {
	e.mu.Lock()
	e.g.ReviewRounds++
	n := e.g.ReviewRounds
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return n
}

// ReviewRounds is the number of lead reviews of the current task.
func (e *Engine) ReviewRounds() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.g.ReviewRounds
}
