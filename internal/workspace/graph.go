// Package workspace holds the node-workspace graph model, its storage, roles,
// the task-split prompt/parser and the scheduler.
package workspace

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

// Report is the structured end of a worker's reply (see agent.Report).
type Report = agent.Report

// Report statuses.
const (
	ReportDone     = agent.ReportDone
	ReportBlocked  = agent.ReportBlocked
	ReportQuestion = agent.ReportQuestion
)

// Status is the lifecycle state of a node.
type Status string

const (
	StatusTodo    Status = "todo"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	// StatusWaiting is a node that ended with a question or a blocked report:
	// its changes are merged, but it is not done. A reply starts its next attempt.
	// With Node.Limit set it instead waits for a provider usage limit to lift.
	StatusWaiting Status = "waiting"
)

// Kind distinguishes LLM work nodes from the build/test check node.
type Kind string

const (
	KindWork  Kind = "work"
	KindCheck Kind = "check"
)

// maxFailureBytes caps the failure text copied into a fix node.
const maxFailureBytes = 4000

// Node is one sub-task in the graph.
type Node struct {
	ID            string     `json:"id"` // "T1", "T2", …, "CHECK", "F1"
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Kind          Kind       `json:"kind"`
	Role          string     `json:"role,omitempty"` // assigned role; "" = unassigned
	SuggestedRole string     `json:"suggested_role,omitempty"`
	DependsOn     []string   `json:"depends_on,omitempty"`
	Status        Status     `json:"status"`
	Commands      []string   `json:"commands,omitempty"` // check nodes
	Summary       string     `json:"summary,omitempty"`
	Report        *Report    `json:"report,omitempty"` // the worker's closing report, set when the node ends
	Files         []string   `json:"files,omitempty"`
	Observed      []string   `json:"observed,omitempty"`   // changed on disk by the agent's own tools
	FileStats     []FileStat `json:"file_stats,omitempty"` // lines added and removed per file, set when the node ran in a worktree
	Commit        string     `json:"commit,omitempty"`     // the node's own commit when it ran in a worktree; reachable through the session branch
	Warnings      []string   `json:"warnings,omitempty"`
	Skills        []string   `json:"skills,omitempty"` // plugin skill ids the lead chose for this node
	Spec          *SpecRef   `json:"spec,omitempty"`   // the Spec Kit task this node was imported from
	Error         string     `json:"error,omitempty"`
	InputTokens   int        `json:"input_tokens,omitempty"`
	OutputTokens  int        `json:"output_tokens,omitempty"`
	Estimated     bool       `json:"estimated,omitempty"`
	CostUSD       float64    `json:"cost_usd,omitempty"`   // priced from the configured prices, added up over every attempt; 0 when unknown
	CostKnown     bool       `json:"cost_known,omitempty"` // a price was found for the node's model
	StartedAt     time.Time  `json:"started_at,omitempty"`
	EndedAt       time.Time  `json:"ended_at,omitempty"`

	// Activity is the journal of the current attempt: at most maxNodeActivity
	// items, each Detail clipped to maxStoredDetail runes. ActivityDropped counts
	// the older items that were cut off.
	Activity        []runner.Activity `json:"activity,omitempty"`
	ActivityDropped int               `json:"activity_dropped,omitempty"`
	// LastActivityAt is the last activity or output of a running node (stall detection).
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	// ContextTokens is the estimated size of the worker prompt; ContextLimit the
	// role's context limit, 0 when unknown.
	ContextTokens int `json:"context_tokens,omitempty"`
	ContextLimit  int `json:"context_limit,omitempty"`

	// Attempt is the number of the current attempt; 0 and 1 both mean the
	// first. A reply (Engine.Reply) starts the next one.
	Attempt int `json:"attempt,omitempty"`
	// Reply is the user's message that started the current attempt; "" for a
	// first attempt that was not started by a reply.
	Reply string `json:"reply,omitempty"`
	// PendingReply is a reply to a running node, kept until the run ends.
	PendingReply string `json:"pending_reply,omitempty"`
	// Attempts are the finished earlier attempts, oldest first.
	Attempts []Attempt `json:"attempts,omitempty"`
	// Limit is set while the node waits for a provider usage limit to lift
	// (Status waiting). It tells such a node from one that waits for a reply.
	Limit *LimitInfo `json:"limit,omitempty"`

	// replyContext is the excerpt of the previous attempt the engine built when
	// this attempt started (it can read details that are kept in memory only).
	// Not saved.
	replyContext string
}

// Attempt is one finished attempt of a node, kept when a reply starts the next.
// Activity is kept for the most recent maxAttemptActivity attempts only.
type Attempt struct {
	N               int               `json:"n"`
	Reply           string            `json:"reply,omitempty"` // what started it; "" for the first
	Status          Status            `json:"status"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	EndedAt         time.Time         `json:"ended_at,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	Report          *Report           `json:"report,omitempty"`
	Error           string            `json:"error,omitempty"`
	FileStats       []FileStat        `json:"file_stats,omitempty"`
	Commit          string            `json:"commit,omitempty"`
	Activity        []runner.Activity `json:"activity,omitempty"`
	ActivityDropped int               `json:"activity_dropped,omitempty"`
}

// maxAttemptActivity is how many archived attempts keep their activity.
const maxAttemptActivity = 3

// AttemptNumber is the number of the current attempt (1 for the first).
func (n *Node) AttemptNumber() int {
	if n.Attempt < 1 {
		return 1
	}
	return n.Attempt
}

func (a Attempt) clone() Attempt {
	a.Report = cloneReport(a.Report)
	a.FileStats = append([]FileStat(nil), a.FileStats...)
	a.Activity = append([]runner.Activity(nil), a.Activity...)
	return a
}

func cloneReport(r *Report) *Report {
	if r == nil {
		return nil
	}
	c := *r
	return &c
}

func cloneAttempts(in []Attempt) []Attempt {
	if len(in) == 0 {
		return nil
	}
	out := make([]Attempt, len(in))
	for i, a := range in {
		out[i] = a.clone()
	}
	return out
}

// SpecRef names the Spec Kit task a node was imported from.
type SpecRef struct {
	File   string `json:"file"`    // tasks.md relative to the project root, slash separated
	TaskID string `json:"task_id"` // "T003"
}

// FileStat is the change one node made to one file, from git's numstat.
type FileStat struct {
	Path    string `json:"path"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Binary  bool   `json:"binary,omitempty"`
	New     bool   `json:"new,omitempty"` // the node created the file
}

// AllFiles is Files followed by the Observed paths not already in Files.
func (n *Node) AllFiles() []string {
	out := append([]string(nil), n.Files...)
	seen := make(map[string]bool, len(out))
	for _, f := range out {
		seen[f] = true
	}
	for _, f := range n.Observed {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// NewRole is a role the lead proposed that is not defined yet.
type NewRole struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Plugin request states.
const (
	PluginPending   = "pending"
	PluginInstalled = "installed"
	PluginDeclined  = "declined"
)

// PluginRequest is a catalog plugin the lead asked for. Status is pending
// until the user answered, then installed (also for an enabled one) or
// declined.
type PluginRequest struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
	Status string `json:"status"`
}

// Graph is the whole workspace: the task and its nodes.
type Graph struct {
	Task     string    `json:"task"`
	Title    string    `json:"title"`
	Nodes    []*Node   `json:"nodes"`
	NewRoles []NewRole `json:"new_roles,omitempty"`
	// FixSeq is the highest fix-node number handed out; F<n> is never reused,
	// even after Compact removed the earlier fix nodes.
	FixSeq int `json:"fix_seq,omitempty"`
	// PluginRequests are the plugins the lead asked for, with the answers.
	PluginRequests []PluginRequest `json:"plugin_requests,omitempty"`
	// SplitWarnings are the notes of the split that produced the graph (an
	// unusable skill or plugin dropped). Not saved.
	SplitWarnings []string `json:"-"`
	// ReviewRounds is the number of lead reviews of the current task; a new
	// task, issue or import resets it (Append).
	ReviewRounds int `json:"review_rounds,omitempty"`
	// RemovedCostUSD is the priced cost of nodes that Compact folded or a lead
	// proposal cancelled, so removing a node never lowers the money spent.
	RemovedCostUSD float64 `json:"removed_cost_usd,omitempty"`
}

// PendingPlugins returns the plugin requests nobody answered yet.
func (g *Graph) PendingPlugins() []PluginRequest {
	var out []PluginRequest
	for _, r := range g.PluginRequests {
		if r.Status == PluginPending {
			out = append(out, r)
		}
	}
	return out
}

// SetPluginStatus sets the status of the request for name; false when there
// is none.
func (g *Graph) SetPluginStatus(name, status string) bool {
	for i := range g.PluginRequests {
		if g.PluginRequests[i].Name == name {
			g.PluginRequests[i].Status = status
			return true
		}
	}
	return false
}

// highestFix is the highest F<number> among the node IDs (0 for none).
func (g *Graph) highestFix() int {
	top := 0
	for _, n := range g.Nodes {
		if isFixID(n.ID) {
			if v, err := strconv.Atoi(n.ID[1:]); err == nil && v > top {
				top = v
			}
		}
	}
	return top
}

// Validate checks unique non-empty IDs, existing dependencies, no self
// dependency and no cycle.
func (g *Graph) Validate() error {
	seen := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID == "" {
			return fmt.Errorf("node with empty id (%q)", n.Title)
		}
		if seen[n.ID] {
			return fmt.Errorf("duplicate node id %s", n.ID)
		}
		seen[n.ID] = true
	}
	for _, n := range g.Nodes {
		for _, d := range n.DependsOn {
			if d == n.ID {
				return fmt.Errorf("node %s depends on itself", n.ID)
			}
			if !seen[d] {
				return fmt.Errorf("node %s depends on missing node %s", n.ID, d)
			}
		}
	}
	// Depth-first cycle search: 0 = unvisited, 1 = on stack, 2 = done.
	state := make(map[string]int, len(g.Nodes))
	var visit func(n *Node) error
	visit = func(n *Node) error {
		state[n.ID] = 1
		for _, d := range n.DependsOn {
			switch state[d] {
			case 1:
				return fmt.Errorf("dependency cycle through %s", d)
			case 0:
				if err := visit(g.Node(d)); err != nil {
					return err
				}
			}
		}
		state[n.ID] = 2
		return nil
	}
	for _, n := range g.Nodes {
		if state[n.ID] == 0 {
			if err := visit(n); err != nil {
				return err
			}
		}
	}
	return nil
}

// Node returns the node with the given ID, or nil.
func (g *Graph) Node(id string) *Node {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// Blockers returns the dependency IDs of id that are not done, in DependsOn
// order. A dependency that does not exist counts as a blocker.
func (g *Graph) Blockers(id string) []string {
	n := g.Node(id)
	if n == nil {
		return nil
	}
	var out []string
	for _, d := range n.DependsOn {
		if dep := g.Node(d); dep == nil || dep.Status != StatusDone {
			out = append(out, d)
		}
	}
	return out
}

// Assigned reports whether a node may run: check nodes always, work nodes
// once a role is set.
func (g *Graph) Assigned(n *Node) bool {
	return n.Kind == KindCheck || n.Role != ""
}

// Runnable returns the todo, assigned, unblocked nodes in Nodes order.
func (g *Graph) Runnable() []*Node {
	var out []*Node
	for _, n := range g.Nodes {
		if n.Status == StatusTodo && g.Assigned(n) && len(g.Blockers(n.ID)) == 0 {
			out = append(out, n)
		}
	}
	return out
}

// Layers groups nodes by dependency depth (1 + the deepest dependency),
// keeping Nodes order inside a layer. The graph must be acyclic.
func (g *Graph) Layers() [][]*Node {
	depth := make(map[string]int, len(g.Nodes))
	var calc func(n *Node) int
	calc = func(n *Node) int {
		if d, ok := depth[n.ID]; ok {
			return d
		}
		depth[n.ID] = 0 // guards against a cycle in an unvalidated graph
		d := 0
		for _, dep := range n.DependsOn {
			if dn := g.Node(dep); dn != nil {
				if l := calc(dn) + 1; l > d {
					d = l
				}
			}
		}
		depth[n.ID] = d
		return d
	}
	var layers [][]*Node
	for _, n := range g.Nodes {
		d := calc(n)
		for len(layers) <= d {
			layers = append(layers, nil)
		}
		layers[d] = append(layers[d], n)
	}
	return layers
}

// Counts returns how many nodes are in each status.
func (g *Graph) Counts() (todo, running, done, failed int) {
	for _, n := range g.Nodes {
		switch n.Status {
		case StatusTodo:
			todo++
		case StatusRunning:
			running++
		case StatusDone:
			done++
		case StatusFailed:
			failed++
		}
	}
	return
}

// CountWaiting returns how many nodes are waiting for a reply.
func (g *Graph) CountWaiting() int {
	k := 0
	for _, n := range g.Nodes {
		if n.Status == StatusWaiting {
			k++
		}
	}
	return k
}

// Clone returns a deep copy.
func (g *Graph) Clone() *Graph {
	c := *g
	c.Nodes = make([]*Node, len(g.Nodes))
	for i, n := range g.Nodes {
		nn := *n
		nn.DependsOn = append([]string(nil), n.DependsOn...)
		nn.Commands = append([]string(nil), n.Commands...)
		nn.Files = append([]string(nil), n.Files...)
		nn.Observed = append([]string(nil), n.Observed...)
		nn.FileStats = append([]FileStat(nil), n.FileStats...)
		nn.Warnings = append([]string(nil), n.Warnings...)
		nn.Skills = append([]string(nil), n.Skills...)
		nn.Activity = append([]runner.Activity(nil), n.Activity...)
		nn.Attempts = cloneAttempts(n.Attempts)
		nn.Report = cloneReport(n.Report)
		if n.Spec != nil {
			sr := *n.Spec
			nn.Spec = &sr
		}
		if n.Limit != nil {
			l := *n.Limit
			nn.Limit = &l
		}
		c.Nodes[i] = &nn
	}
	c.NewRoles = append([]NewRole(nil), g.NewRoles...)
	c.PluginRequests = append([]PluginRequest(nil), g.PluginRequests...)
	c.SplitWarnings = append([]string(nil), g.SplitWarnings...)
	return &c
}

// AddCheck appends the CHECK node depending on every other node. It returns
// nil without change when commands is empty or CHECK already exists.
func (g *Graph) AddCheck(commands []string) *Node {
	if len(commands) == 0 {
		return nil
	}
	return g.addCheck(commands)
}

// AddCheckDeferred appends the CHECK node without commands; the executor asks
// for the commands when the node runs. It returns nil when CHECK exists.
func (g *Graph) AddCheckDeferred() *Node { return g.addCheck(nil) }

func (g *Graph) addCheck(commands []string) *Node {
	if g.Node("CHECK") != nil {
		return nil
	}
	c := &Node{
		ID:       "CHECK",
		Title:    "Build & test",
		Kind:     KindCheck,
		Status:   StatusTodo,
		Commands: append([]string(nil), commands...),
	}
	for _, n := range g.Nodes {
		c.DependsOn = append(c.DependsOn, n.ID)
	}
	g.Nodes = append(g.Nodes, c)
	return c
}

// AddFix appends a work node "F<n>" that fixes a failed check, resets the
// check to todo and makes it depend on the new node.
func (g *Graph) AddFix(checkID, failure string) (*Node, error) {
	return g.AddFixExcerpt(checkID, failure, "")
}

// AddFixExcerpt is AddFix with an excerpt of what failed (see FixExcerpt) added
// to the description under "What failed:". An empty excerpt is built from the
// check node itself.
func (g *Graph) AddFixExcerpt(checkID, failure, excerpt string) (*Node, error) {
	c := g.Node(checkID)
	if c == nil || c.Kind != KindCheck {
		return nil, fmt.Errorf("node %q is not a check node", checkID)
	}
	if excerpt == "" {
		excerpt = FixExcerpt([]*Node{c}, FixExcerptChars)
	}
	if len(failure) > maxFailureBytes {
		failure = failure[len(failure)-maxFailureBytes:]
		for len(failure) > 0 && !utf8.RuneStart(failure[0]) {
			failure = failure[1:]
		}
	}
	seq := g.FixSeq
	if top := g.highestFix(); top > seq {
		seq = top
	}
	id := ""
	for seq++; ; seq++ {
		if id = fmt.Sprintf("F%d", seq); g.Node(id) == nil {
			break
		}
	}
	g.FixSeq = seq
	desc := "The check failed. Change the code so it passes."
	if excerpt == "" {
		desc += " Failure output:\n" + strings.TrimSpace(failure)
	} else {
		desc += whatFailed(excerpt) // the excerpt already holds the command tails
	}
	f := &Node{
		ID:            id,
		Title:         "Fix: " + c.Title,
		Description:   desc,
		Kind:          KindWork,
		SuggestedRole: "coder_fixer",
		DependsOn:     append([]string(nil), c.DependsOn...),
		Status:        StatusTodo,
	}
	g.Nodes = append(g.Nodes, f)
	c.Status = StatusTodo
	c.Error = ""
	c.DependsOn = append(c.DependsOn, id)
	return f, nil
}

// AddCIFix appends a work node "F<n>" with the given title and description
// (CI log or review comments) and returns it. It has no dependencies: it works
// on what is already merged. A Build & test node is reset to todo and made to
// depend on it, as AddFix does.
func (g *Graph) AddCIFix(title, desc string) *Node {
	seq := g.FixSeq
	if top := g.highestFix(); top > seq {
		seq = top
	}
	id := ""
	for seq++; ; seq++ {
		if id = fmt.Sprintf("F%d", seq); g.Node(id) == nil {
			break
		}
	}
	g.FixSeq = seq
	f := &Node{
		ID:            id,
		Title:         strings.Join(strings.Fields(title), " "),
		Description:   strings.TrimSpace(desc),
		Kind:          KindWork,
		SuggestedRole: "coder_fixer",
		Status:        StatusTodo,
	}
	g.Nodes = append(g.Nodes, f)
	if c := g.Node("CHECK"); c != nil && c.Kind == KindCheck && c.Status != StatusRunning {
		c.Status = StatusTodo
		c.Error = ""
		c.DependsOn = append(c.DependsOn, id)
	}
	return f
}

// AddWork appends a work node "T<n>" (n at least nextID and above every T
// number in g) with no dependencies, assigned to role when it is not "", and
// returns it with the next free number. A Build & test node that is not running
// is reset to todo and made to depend on it, as AddCIFix does.
func (g *Graph) AddWork(title, desc, role string, nextID int) (*Node, int) {
	if m := g.nextWorkNumber(); nextID < m {
		nextID = m
	}
	f := &Node{
		ID:          fmt.Sprintf("T%d", nextID),
		Title:       strings.Join(strings.Fields(title), " "),
		Description: strings.TrimSpace(desc),
		Kind:        KindWork,
		Role:        role,
		Status:      StatusTodo,
	}
	g.Nodes = append(g.Nodes, f)
	if c := g.Node("CHECK"); c != nil && c.Kind == KindCheck && c.Status != StatusRunning {
		c.Status = StatusTodo
		c.Error = ""
		c.DependsOn = append(c.DependsOn, f.ID)
	}
	return f, nextID + 1
}

// Append merges the work nodes of add into g under fresh IDs T<n>, T<n+1>, …
// (n is at least nextID and above every T number already in g), rewrites their
// dependencies, merges add's new roles and drops add's CHECK. g's CHECK (created
// when missing) is moved last, depends on every work node, and is reset to
// todo. It returns the next free number and the old-to-new ID map. add is not
// modified. A dependency naming one of add's own keys follows the renumbering
// (also when the key collides with an existing ID); one naming another work
// node already in g is kept; any other dependency is dropped.
func (g *Graph) Append(add *Graph, nextID int) (newNext int, idMap map[string]string) {
	if m := g.nextWorkNumber(); nextID < m {
		nextID = m
	}
	idMap = make(map[string]string)
	for _, n := range add.Nodes {
		if n.Kind == KindCheck {
			continue
		}
		idMap[n.ID] = fmt.Sprintf("T%d", nextID)
		nextID++
	}
	for _, n := range add.Nodes {
		if n.Kind == KindCheck {
			continue
		}
		c := *n
		c.ID = idMap[n.ID]
		c.DependsOn = nil
		for _, d := range n.DependsOn {
			if nd, ok := idMap[d]; ok {
				c.DependsOn = append(c.DependsOn, nd)
			} else if dep := g.Node(d); dep != nil && dep.Kind == KindWork {
				// An existing node of g that add did not define itself.
				c.DependsOn = append(c.DependsOn, d)
			}
		}
		c.Commands = append([]string(nil), n.Commands...)
		c.Files = append([]string(nil), n.Files...)
		c.Observed = append([]string(nil), n.Observed...)
		c.FileStats = append([]FileStat(nil), n.FileStats...)
		c.Warnings = append([]string(nil), n.Warnings...)
		c.Skills = append([]string(nil), n.Skills...)
		c.Activity = append([]runner.Activity(nil), n.Activity...)
		c.Attempts = cloneAttempts(n.Attempts)
		c.Report = cloneReport(n.Report)
		g.Nodes = append(g.Nodes, &c)
	}
	g.ReviewRounds = 0
	if g.Task == "" {
		g.Task = add.Task
	}
	if g.Title == "" {
		g.Title = add.Title
	}
	have := make(map[string]bool, len(g.NewRoles))
	for _, r := range g.NewRoles {
		have[r.Name] = true
	}
	for _, r := range add.NewRoles {
		if !have[r.Name] {
			have[r.Name] = true
			g.NewRoles = append(g.NewRoles, r)
		}
	}

	asked := make(map[string]bool, len(g.PluginRequests))
	for _, r := range g.PluginRequests {
		asked[r.Name] = true
	}
	for _, r := range add.PluginRequests {
		if !asked[r.Name] { // an answered request stays answered
			asked[r.Name] = true
			g.PluginRequests = append(g.PluginRequests, r)
		}
	}

	check := g.Node("CHECK")
	if check == nil {
		check = g.AddCheckDeferred()
	} else {
		for i, n := range g.Nodes {
			if n == check {
				g.Nodes = append(g.Nodes[:i], g.Nodes[i+1:]...)
				break
			}
		}
		g.Nodes = append(g.Nodes, check)
	}
	check.DependsOn = nil
	for _, n := range g.Nodes {
		if n.Kind == KindWork {
			check.DependsOn = append(check.DependsOn, n.ID)
		}
	}
	check.Status = StatusTodo
	check.Error, check.Summary, check.Warnings = "", "", nil
	return nextID, idMap
}

// Compact removes done work and fix nodes (CHECK stays) and drops their IDs
// from the dependencies of the remaining nodes. A done node is folded only
// when every node that depends on it (the CHECK node aside) is folded too, so
// unfinished nodes keep the work they need. It returns the removed nodes in
// Nodes order.
func (g *Graph) Compact() (folded []*Node) {
	gone := make(map[string]bool)
	for _, n := range g.Nodes {
		if n.Kind == KindWork && n.Status == StatusDone {
			gone[n.ID] = true
		}
	}
	// Greatest fixed point: drop candidates that still have a dependent staying.
	for changed := true; changed; {
		changed = false
		for _, n := range g.Nodes {
			if gone[n.ID] || n.Kind == KindCheck {
				continue
			}
			for _, d := range n.DependsOn {
				if gone[d] {
					gone[d] = false
					changed = true
				}
			}
		}
	}
	for id, ok := range gone {
		if !ok {
			delete(gone, id)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	kept := g.Nodes[:0:0]
	for _, n := range g.Nodes {
		if gone[n.ID] {
			folded = append(folded, n)
			g.RemovedCostUSD += n.CostUSD
			continue
		}
		var deps []string
		for _, d := range n.DependsOn {
			if !gone[d] {
				deps = append(deps, d)
			}
		}
		n.DependsOn = deps
		kept = append(kept, n)
	}
	g.Nodes = kept
	return folded
}

// maxSummaryChars caps the compacted session summary.
const maxSummaryChars = 12000

// CompactSummary appends one line per folded node to prev:
// "- T1 <title> [<role>]: <summary, first 400 chars> (files: a, b)". When the
// result exceeds 12,000 characters the oldest lines are dropped.
func CompactSummary(prev string, folded []*Node) string {
	var lines []string
	if p := strings.TrimSpace(prev); p != "" {
		lines = strings.Split(p, "\n")
	}
	for _, n := range folded {
		var sb strings.Builder
		sb.WriteString("- " + n.ID + " " + n.Title)
		if n.Role != "" {
			sb.WriteString(" [" + n.Role + "]")
		}
		if s := strings.Join(strings.Fields(n.Summary), " "); s != "" {
			if r := []rune(s); len(r) > 400 {
				s = string(r[:400])
			}
			sb.WriteString(": " + s)
		}
		if files := n.AllFiles(); len(files) > 0 {
			sb.WriteString(" (files: " + strings.Join(files, ", ") + ")")
		}
		lines = append(lines, sb.String())
	}
	return trimSummary(lines)
}

// trimSummary joins lines, dropping the oldest while the result exceeds
// maxSummaryChars.
func trimSummary(lines []string) string {
	total := func() int {
		t := 0
		for _, l := range lines {
			t += len(l) + 1
		}
		return t - 1
	}
	for len(lines) > 1 && total() > maxSummaryChars {
		lines = lines[1:]
	}
	out := strings.Join(lines, "\n")
	for len(out) > maxSummaryChars { // a single oversized line
		out = out[:maxSummaryChars]
		for len(out) > 0 && !utf8.ValidString(out) {
			out = out[:len(out)-1]
		}
	}
	return out
}

// whatFailed formats an excerpt as the "What failed:" block of a description.
func whatFailed(excerpt string) string {
	if excerpt == "" {
		return ""
	}
	return "\n\nWhat failed:\n" + excerpt
}
