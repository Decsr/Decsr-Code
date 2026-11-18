package workspace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	markerChanges = "===CHANGES==="

	// Change actions the lead may ask for on an existing node.
	ChangeUpdate = "update"
	ChangeCancel = "cancel"
	ChangeNote   = "note"

	// maxChanges is the most changes one answer may carry.
	maxChanges = 12
	// maxLiveFiles is the most files named per node in the live graph.
	maxLiveFiles = 8
	// maxLiveDescRunes cuts the one-line description of a node in the live graph.
	maxLiveDescRunes = 160
)

// Change is one edit the lead asks for on a node that already exists.
type Change struct {
	Node   string `json:"node"`
	Action string `json:"action"`
	// Description is the new description of an update; "" keeps the old one.
	Description string `json:"description,omitempty"`
	// DependsOn is the new dependency list of an update; it only applies when
	// SetDeps is true (an empty list then clears the dependencies).
	DependsOn []string `json:"depends_on,omitempty"`
	SetDeps   bool     `json:"set_deps,omitempty"`
	// Message is the text a note sends to the node.
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// LiveNode is what the lead is told about one node of the live graph.
type LiveNode struct {
	ID, Title, Status, Role, Description string
	Files                                []string
	// Started is true once the node ran at least once.
	Started bool
}

// NotStarted reports whether n never ran: a todo node, or a waiting one that
// has no run behind it. Only such a node may be updated or cancelled.
func (n *Node) NotStarted() bool {
	if n.Kind != KindWork || !n.StartedAt.IsZero() || len(n.Attempts) > 0 {
		return false
	}
	return n.Status == StatusTodo || n.Status == StatusWaiting
}

// LiveNodes describes the work nodes of g for the lead, in graph order.
func LiveNodes(g *Graph) []LiveNode {
	var out []LiveNode
	for _, n := range g.Nodes {
		if n.Kind != KindWork {
			continue
		}
		ln := LiveNode{
			ID: n.ID, Title: Sanitize(n.Title), Status: string(n.Status), Role: orRole(n),
			Description: cutRunes(Sanitize(n.Description), maxLiveDescRunes),
			Started:     !n.NotStarted(),
		}
		seen := map[string]bool{}
		add := func(p string) {
			if p != "" && !seen[p] {
				seen[p] = true
				ln.Files = append(ln.Files, p)
			}
		}
		for _, f := range n.AllFiles() {
			add(f)
		}
		for _, fs := range n.FileStats {
			add(fs.Path)
		}
		sort.Strings(ln.Files)
		out = append(out, ln)
	}
	return out
}

func orRole(n *Node) string {
	if n.Role != "" {
		return n.Role
	}
	return n.SuggestedRole
}

// liveSection is the "nodes in the graph" part of the follow-up prompt.
func liveSection(live []LiveNode) string {
	var sb strings.Builder
	for _, n := range live {
		fmt.Fprintf(&sb, "- %s %q [%s]", n.ID, n.Title, n.Status)
		if n.Role != "" {
			sb.WriteString(" role " + n.Role)
		}
		if n.Description != "" && n.Description != n.Title {
			sb.WriteString(": " + n.Description)
		}
		if len(n.Files) > 0 {
			files := n.Files
			tail := ""
			if len(files) > maxLiveFiles {
				files, tail = files[:maxLiveFiles], fmt.Sprintf(", … %d more", len(n.Files)-maxLiveFiles)
			}
			sb.WriteString(" | files: " + strings.Join(files, ", ") + tail)
		} else if n.Status == string(StatusRunning) {
			sb.WriteString(" | files: not known yet")
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

type changeRaw struct {
	Node        string  `json:"node"`
	ID          string  `json:"id"`
	Action      string  `json:"action"`
	Description string  `json:"description"`
	DependsOn   *idList `json:"depends_on"`
	Message     string  `json:"message"`
	Reason      string  `json:"reason"`
}

// parseChanges reads the CHANGES section against the live graph. Entries that
// are invalid are dropped and reported in warnings. newKeys are the keys of
// the answer's own new tasks, which an update may depend on.
func parseChanges(output string, live []LiveNode, newKeys map[string]bool) (changes []Change, warnings []string) {
	raw, ok := section(output, markerChanges)
	if !ok {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := unmarshalArray(raw, &entries); err != nil {
		return nil, []string{fmt.Sprintf("changes: not valid JSON (%s), ignored", shortReason(err))}
	}
	byID := make(map[string]LiveNode, len(live))
	for _, n := range live {
		byID[n.ID] = n
	}
	seen := map[string]bool{}
	for i, e := range entries {
		var r changeRaw
		if err := json.Unmarshal(e, &r); err != nil {
			warnings = append(warnings, fmt.Sprintf("change %d: not an object, dropped", i+1))
			continue
		}
		if len(changes) >= maxChanges {
			warnings = append(warnings, fmt.Sprintf("changes: more than %d, the rest dropped", maxChanges))
			break
		}
		id := upperKey(r.Node)
		if id == "" {
			id = upperKey(r.ID)
		}
		action := strings.ToLower(strings.TrimSpace(r.Action))
		n, known := byID[id]
		c := Change{Node: id, Action: action, Reason: cutRunes(Sanitize(r.Reason), 200)}
		switch {
		case action != ChangeUpdate && action != ChangeCancel && action != ChangeNote:
			warnings = append(warnings, fmt.Sprintf("change of %s: unknown action %q, dropped", orDash(id), r.Action))
			continue
		case !known:
			warnings = append(warnings, fmt.Sprintf("change of %s: no such node, dropped", orDash(id)))
			continue
		case seen[id]:
			warnings = append(warnings, fmt.Sprintf("%s: more than one change, the later one dropped", id))
			continue
		}
		switch action {
		case ChangeUpdate, ChangeCancel:
			if n.Started {
				warnings = append(warnings, fmt.Sprintf("%s is %s: it cannot be %sd, only noted, dropped", id, n.Status, action))
				continue
			}
		case ChangeNote:
			if !n.Started {
				warnings = append(warnings, fmt.Sprintf("%s has not started: update it instead of noting it, dropped", id))
				continue
			}
			c.Message = strings.TrimSpace(SanitizeText(r.Message))
			if c.Message == "" {
				warnings = append(warnings, fmt.Sprintf("note for %s is empty, dropped", id))
				continue
			}
		}
		if action == ChangeUpdate {
			c.Description = strings.TrimSpace(SanitizeText(r.Description))
			if r.DependsOn != nil {
				c.SetDeps = true
				for _, d := range *r.DependsOn {
					d = upperKey(d)
					_, isLive := byID[d]
					switch {
					case d == id:
						warnings = append(warnings, fmt.Sprintf("%s: depends on itself, dropped that dependency", id))
					case !isLive && !newKeys[d]:
						warnings = append(warnings, fmt.Sprintf("%s: depends on unknown %s, dropped that dependency", id, d))
					default:
						c.DependsOn = append(c.DependsOn, d)
					}
				}
			}
			if c.Description == "" && !c.SetDeps {
				warnings = append(warnings, fmt.Sprintf("update of %s changes nothing, dropped", id))
				continue
			}
		}
		seen[id] = true
		changes = append(changes, c)
	}
	return changes, warnings
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// UpdateNode changes the description and, when deps is not nil, the
// dependencies of a node that never ran. It works while other nodes run.
func (e *Engine) UpdateNode(id, desc string, deps *[]string) error {
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case !n.NotStarted():
		e.mu.Unlock()
		return fmt.Errorf("%s is %s: only a node that has not started can be updated", id, n.Status)
	}
	if deps != nil {
		trial := e.g.Clone()
		trial.Node(id).DependsOn = append([]string(nil), *deps...)
		if err := trial.Validate(); err != nil {
			e.mu.Unlock()
			return fmt.Errorf("%s: %v", id, err)
		}
		n.DependsOn = append([]string(nil), *deps...)
	}
	if d := strings.TrimSpace(desc); d != "" {
		n.Description = d
	}
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.flush()
	return nil
}

// CancelNode removes a node that never ran and the dependencies on it.
func (e *Engine) CancelNode(id string) error {
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case !n.NotStarted():
		e.mu.Unlock()
		return fmt.Errorf("%s is %s: only a node that has not started can be cancelled", id, n.Status)
	}
	kept := e.g.Nodes[:0]
	for _, o := range e.g.Nodes {
		if o.ID == id {
			continue
		}
		deps := o.DependsOn[:0]
		for _, d := range o.DependsOn {
			if d != id {
				deps = append(deps, d)
			}
		}
		o.DependsOn = deps
		kept = append(kept, o)
	}
	e.g.Nodes = kept
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.flush()
	return nil
}

// AppendNodes merges the work nodes of add into the live graph (see
// Graph.Append). Unlike Replace it works while nodes run; it refuses only
// when Build & test is running and a new node writes, because Build & test
// would be reset under its own run.
func (e *Engine) AppendNodes(add *Graph, nextID int) (int, map[string]string, error) {
	e.mu.Lock()
	if c := e.g.Node("CHECK"); c != nil && c.Status == StatusRunning {
		for _, n := range add.Nodes {
			if n.Kind == KindWork && !n.ReadOnlyTask {
				e.mu.Unlock()
				return 0, nil, fmt.Errorf("Build & test is running: wait for it, then send the task")
			}
		}
	}
	next, idMap := e.g.Append(add, nextID)
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.flush()
	return next, idMap, nil
}
