// Package workspace holds the node-workspace graph model, its storage, roles,
// the task-split prompt/parser and the scheduler.
package workspace

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is the lifecycle state of a node.
type Status string

const (
	StatusTodo    Status = "todo"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
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
	ID            string    `json:"id"` // "T1", "T2", …, "CHECK", "F1"
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Kind          Kind      `json:"kind"`
	Role          string    `json:"role,omitempty"` // assigned role; "" = unassigned
	SuggestedRole string    `json:"suggested_role,omitempty"`
	DependsOn     []string  `json:"depends_on,omitempty"`
	Status        Status    `json:"status"`
	Commands      []string  `json:"commands,omitempty"` // check nodes
	Summary       string    `json:"summary,omitempty"`
	Files         []string  `json:"files,omitempty"`
	Observed      []string  `json:"observed,omitempty"` // changed on disk by the agent's own tools
	Warnings      []string  `json:"warnings,omitempty"`
	Error         string    `json:"error,omitempty"`
	InputTokens   int       `json:"input_tokens,omitempty"`
	OutputTokens  int       `json:"output_tokens,omitempty"`
	Estimated     bool      `json:"estimated,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	EndedAt       time.Time `json:"ended_at,omitempty"`
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

// Graph is the whole workspace: the task and its nodes.
type Graph struct {
	Task     string    `json:"task"`
	Title    string    `json:"title"`
	Nodes    []*Node   `json:"nodes"`
	NewRoles []NewRole `json:"new_roles,omitempty"`
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
		nn.Warnings = append([]string(nil), n.Warnings...)
		c.Nodes[i] = &nn
	}
	c.NewRoles = append([]NewRole(nil), g.NewRoles...)
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
	c := g.Node(checkID)
	if c == nil || c.Kind != KindCheck {
		return nil, fmt.Errorf("node %q is not a check node", checkID)
	}
	if len(failure) > maxFailureBytes {
		failure = failure[len(failure)-maxFailureBytes:]
		for len(failure) > 0 && !utf8.RuneStart(failure[0]) {
			failure = failure[1:]
		}
	}
	id := ""
	for i := 1; ; i++ {
		if id = fmt.Sprintf("F%d", i); g.Node(id) == nil {
			break
		}
	}
	f := &Node{
		ID:            id,
		Title:         "Fix: " + c.Title,
		Description:   "The check failed. Change the code so it passes. Failure output:\n" + strings.TrimSpace(failure),
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
