package workspace

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/ranking"
)

// Difficulty grades the lead gives a node.
const (
	GradeSmall  = "small"
	GradeMedium = "medium"
	GradeLarge  = "large"
)

// Sources of an AgentAssign.
const (
	SourceAuto   = "auto"   // picked by PickAgent
	SourceManual = "manual" // set by the user
	SourceRole   = "role"   // the role's own agent
)

// smallFloor is the lowest intelligence index a small node may be given.
const smallFloor = 30.0

// mediumShare is the share of the role pair's intelligence a medium node needs.
const mediumShare = 0.85

// AgentAssign is the agent a node runs with instead of its role's own. Pending
// is true while the user has not approved it; a pending node does not start.
type AgentAssign struct {
	Runner       string  `json:"runner"`
	Model        string  `json:"model"`
	Effort       string  `json:"effort,omitempty"`
	Grade        string  `json:"grade,omitempty"`
	Reason       string  `json:"reason,omitempty"`
	Source       string  `json:"source"`
	EstCostUSD   float64 `json:"est_cost_usd,omitempty"` // cost per task from the rankings; 0 = unknown
	Pending      bool    `json:"pending,omitempty"`
	FallbackNote string  `json:"fallback_note,omitempty"` // set when the run used the role's agent instead
}

func (a *AgentAssign) clone() *AgentAssign {
	if a == nil {
		return nil
	}
	c := *a
	return &c
}

// AwaitingApproval reports whether the node's agent assignment is proposed
// and not approved yet.
func (n *Node) AwaitingApproval() bool { return n.Assign != nil && n.Assign.Pending }

// NormalizeGrade returns small, medium or large; anything else is medium.
func NormalizeGrade(s string) string {
	switch g := strings.ToLower(strings.TrimSpace(s)); g {
	case GradeSmall, GradeMedium, GradeLarge:
		return g
	}
	return GradeMedium
}

// PickAgent chooses the agent of one node from the lead's grade, never above
// the role's own agent: the cost per task is at most the role pair's, a small
// node needs an intelligence of at least 30, a medium node 85% of the role
// pair's, and a large node keeps the role pair. Among the allowed pairs the
// highest balance wins. Mode auto-role allows only the role's runner,
// auto-connected any connected rankable runner. An unranked role pair, an
// unknown mode (manual) or an empty table keep the role pair. why says in a
// few words how the choice came about. It reads no files and makes no call.
func PickAgent(table ranking.Table, roleAgent config.AgentConfig, grade, mode string, connected map[string]bool) (AgentAssign, string) {
	grade = NormalizeGrade(grade)
	keep := AgentAssign{Runner: roleAgent.Runner, Model: roleAgent.Model, Effort: roleAgent.Effort, Grade: grade, Source: SourceRole}
	mode = config.NormalizeRoutingMode(mode)
	if mode == config.RoutingManual {
		return keep, "manual mode: the role's agent"
	}
	ref, ok := table.Lookup(roleAgent.Runner, roleAgent.Model, roleAgent.Effort)
	if !ok {
		return keep, "the role's model is not ranked: kept"
	}
	keep.EstCostUSD = ref.CostPerTask
	if grade == GradeLarge {
		return keep, "large: the role's agent"
	}
	if ref.CostPerTask <= 0 {
		return keep, "the role's cost is unknown: kept"
	}
	var best ranking.Candidate
	found := false
	for _, c := range table.Candidates(ranking.RankableRunners) {
		if sameCandidate(c, ref) {
			continue // the role pair is the baseline; it wins unless beaten
		}
		if c.Runner != ref.Runner && (mode == config.RoutingAutoRole || !connected[c.Runner]) {
			continue
		}
		if c.CostPerTask <= 0 || c.CostPerTask > ref.CostPerTask {
			continue
		}
		if grade == GradeSmall && c.Intelligence < smallFloor {
			continue
		}
		if grade == GradeMedium && c.Intelligence < mediumShare*ref.Intelligence {
			continue
		}
		if !found || c.Balance > best.Balance || (c.Balance == best.Balance && c.CostPerTask < best.CostPerTask) {
			best, found = c, true
		}
	}
	if !found || best.Balance <= ref.Balance {
		keep.Source = SourceAuto
		return keep, grade + ": the role's agent is the best fit"
	}
	a := AgentAssign{Runner: best.Runner, Model: best.Model, Effort: best.Effort, Grade: grade, Source: SourceAuto, EstCostUSD: best.CostPerTask}
	return a, fmt.Sprintf("%s: %s %s %s (balance %.0f, role pair %.0f)", grade, best.Runner, best.Model, best.Effort, best.Balance, ref.Balance)
}

func sameCandidate(a, b ranking.Candidate) bool {
	return a.Runner == b.Runner && a.Model == b.Model && a.Effort == b.Effort
}

// Router proposes the agent of each node after a split. The zero value
// proposes nothing.
type Router struct {
	Mode      string // config.RoutingManual, RoutingAutoRole or RoutingAutoConnected
	Table     ranking.Table
	Connected map[string]bool
	// Role returns the agent of a role (nil = proposals use no role).
	Role func(name string) (config.AgentConfig, bool)
}

// propose returns the pending assignment of n, or nil when its role is unknown.
func (r *Router) propose(n *Node) *AgentAssign {
	if r == nil || r.Role == nil || n.Kind != KindWork {
		return nil
	}
	role := n.Role
	if role == "" {
		role = n.SuggestedRole
	}
	ac, ok := r.Role(role)
	if !ok {
		return nil
	}
	a, why := PickAgent(r.Table, ac, n.Difficulty, r.Mode, r.Connected)
	a.Reason = strings.TrimSpace(n.DifficultyReason)
	if a.Reason == "" {
		a.Reason = why
	}
	a.Pending = true
	return &a
}

// SetRouter sets the router used by Propose and by Assign (a node whose role
// changes is proposed again). nil clears it.
func (e *Engine) SetRouter(r *Router) {
	e.mu.Lock()
	e.router = r
	e.mu.Unlock()
}

// Propose gives every todo work node in ids an assignment marked pending
// (all of them when ids is empty). A node keeps its assignment when it has
// one. Nodes whose role is unknown get none. It returns how many nodes are now
// pending. Nothing starts until ApproveAssignments.
func (e *Engine) Propose(ids ...string) int {
	e.mu.Lock()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	for _, n := range e.g.Nodes {
		if (len(want) > 0 && !want[n.ID]) || n.Status != StatusTodo || n.Assign != nil {
			continue
		}
		n.Assign = e.router.propose(n)
	}
	count := len(e.g.PendingApproval())
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return count
}

// PendingApproval returns a snapshot of the nodes that wait for the approval
// of their agent assignment, in Nodes order.
func (e *Engine) PendingApproval() []*Node {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Node
	for _, n := range e.g.Clone().Nodes {
		if n.AwaitingApproval() {
			out = append(out, n)
		}
	}
	return out
}

// ApproveAssignments approves every pending assignment and, when auto mode is
// on, starts the nodes that became runnable. It returns how many were approved.
func (e *Engine) ApproveAssignments() int {
	e.mu.Lock()
	count := 0
	for _, n := range e.g.Nodes {
		if n.AwaitingApproval() {
			n.Assign.Pending = false
			count++
		}
	}
	var starts []started
	if count > 0 && e.auto {
		starts = e.pickLocked()
	}
	var err error
	if count > 0 {
		err = e.saveLocked()
	}
	e.mu.Unlock()
	e.reportSave(err)
	e.flush()
	for _, s := range starts {
		e.launch(s)
	}
	return count
}

// RejectSplit removes the nodes that wait for approval (and the dependencies
// on them) and returns their IDs. Build & test is removed too when no work
// node is left.
func (e *Engine) RejectSplit() []string {
	e.mu.Lock()
	drop := map[string]bool{}
	var ids []string
	for _, n := range e.g.Nodes {
		if n.AwaitingApproval() {
			drop[n.ID] = true
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		e.mu.Unlock()
		return nil
	}
	kept := e.g.Nodes[:0]
	for _, n := range e.g.Nodes {
		if drop[n.ID] {
			continue
		}
		deps := n.DependsOn[:0]
		for _, d := range n.DependsOn {
			if !drop[d] {
				deps = append(deps, d)
			}
		}
		n.DependsOn = deps
		kept = append(kept, n)
	}
	e.g.Nodes = kept
	work := 0
	for _, n := range e.g.Nodes {
		if n.Kind == KindWork {
			work++
		}
	}
	if work == 0 {
		var rest []*Node
		for _, n := range e.g.Nodes {
			if n.Kind != KindCheck {
				rest = append(rest, n)
			}
		}
		e.g.Nodes = rest
	}
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return ids
}

// SetAssignment sets the agent of a todo or failed work node by hand (source
// manual). The node keeps its pending state, so a manual edit on the approval
// card does not approve it.
func (e *Engine) SetAssignment(id string, a AgentAssign) error {
	if strings.TrimSpace(a.Runner) == "" {
		return errors.New("an agent needs a runner")
	}
	if !config.KnownRunner(a.Runner) {
		return fmt.Errorf("unknown runner %q", a.Runner)
	}
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case n.Kind != KindWork:
		e.mu.Unlock()
		return fmt.Errorf("node %s does not take an agent", id)
	case !editable(n):
		e.mu.Unlock()
		return fmt.Errorf("node %s is %s and its agent cannot be changed", id, n.Status)
	}
	pending := n.AwaitingApproval()
	grade, reason := a.Grade, a.Reason
	if n.Assign != nil {
		if grade == "" {
			grade = n.Assign.Grade
		}
		if reason == "" {
			reason = n.Assign.Reason
		}
	}
	a.Grade, a.Reason, a.Source, a.Pending, a.FallbackNote = grade, reason, SourceManual, pending, ""
	a.EstCostUSD = e.router.estimate(a)
	n.Assign = &a
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return nil
}

// estimate is the cost per task of a, from the router's table; 0 = unknown.
func (r *Router) estimate(a AgentAssign) float64 {
	if r == nil {
		return 0
	}
	if c, ok := r.Table.Lookup(a.Runner, a.Model, a.Effort); ok {
		return c.CostPerTask
	}
	return 0
}

// usableRunner reports whether the runner can start now. Runners the rankings
// do not cover cannot be checked and count as usable.
func usableRunner(connected map[string]bool, name string) bool {
	for _, r := range ranking.RankableRunners {
		if r == name {
			return connected[name]
		}
	}
	return true
}
