package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the graph file inside .decsr.
const FileName = "workspace.json"

// Save writes g to path atomically (temp file, then rename) with mode 0o600.
func Save(path string, g *Graph) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".workspace-*.tmp")
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
	for i, n := range g.Nodes {
		if n == nil {
			return nil, fmt.Errorf("load %s: node %d is null", path, i)
		}
		switch n.Status {
		case StatusTodo, StatusRunning, StatusDone, StatusFailed:
		default:
			return nil, fmt.Errorf("load %s: node %q has unknown status %q", path, n.ID, n.Status)
		}
		switch n.Kind {
		case KindWork, KindCheck:
		default:
			return nil, fmt.Errorf("load %s: node %q has unknown kind %q", path, n.ID, n.Kind)
		}
	}
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	for _, n := range g.Nodes {
		if n.Status == StatusRunning {
			n.Status = StatusTodo
		}
	}
	return &g, nil
}
