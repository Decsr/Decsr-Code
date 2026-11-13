package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// FileName is the graph file inside .decsr.
const FileName = "workspace.json"

// Save writes g to path atomically (temp file, then rename) with mode 0o600.
func Save(path string, g *Graph) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data, ".workspace-*.tmp")
}

// writeAtomic writes data to path through a temp file (named by pattern in the
// same directory) and a rename, with mode 0o600.
func writeAtomic(path string, data []byte, pattern string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Load reads a graph from path. Nodes saved as running (an interrupted run)
// come back as todo. A missing file returns os.ErrNotExist unchanged.
func Load(path string) (*Graph, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- caller-controlled workspace path
	if err != nil {
		return nil, err
	}
	var g Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	if err := g.prepare(path); err != nil {
		return nil, err
	}
	return &g, nil
}

// prepare checks a decoded graph (node kinds and statuses, structure) and turns
// nodes saved as running into todo. src names the file in error messages.
func (g *Graph) prepare(src string) error {
	for i, n := range g.Nodes {
		if n == nil {
			return fmt.Errorf("load %s: node %d is null", src, i)
		}
		switch n.Status {
		case StatusTodo, StatusRunning, StatusDone, StatusFailed:
		default:
			return fmt.Errorf("load %s: node %q has unknown status %q", src, n.ID, n.Status)
		}
		switch n.Kind {
		case KindWork, KindCheck:
		default:
			return fmt.Errorf("load %s: node %q has unknown kind %q", src, n.ID, n.Kind)
		}
	}
	if err := g.Validate(); err != nil {
		return fmt.Errorf("load %s: %w", src, err)
	}
	for _, n := range g.Nodes {
		if n.Status == StatusRunning {
			n.Status = StatusTodo
		}
	}
	g.Sanitize()
	if top := g.highestFix(); top > g.FixSeq {
		g.FixSeq = top
	}
	return nil
}

// Sanitize removes terminal escape sequences and control characters from the
// graph title and the node titles and descriptions.
func (g *Graph) Sanitize() {
	g.Title = Sanitize(g.Title)
	for _, n := range g.Nodes {
		n.Title, n.Description = Sanitize(n.Title), SanitizeText(n.Description)
		n.Skills = validSkillIDs(n.Skills)
	}
	var reqs []PluginRequest
	for _, r := range g.PluginRequests {
		switch r.Status {
		case PluginPending, PluginInstalled, PluginDeclined:
		default:
			continue
		}
		if !config.ValidName(r.Name) {
			continue
		}
		r.Reason = Sanitize(r.Reason)
		reqs = append(reqs, r)
	}
	g.PluginRequests = reqs
}

// validSkillIDs keeps the well-formed "<plugin>:<skill>" ids of ids, once
// each, in order.
func validSkillIDs(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if seen[id] || !config.ValidQualifiedName(id) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
