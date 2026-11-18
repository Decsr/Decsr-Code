package workspace

import (
	"path/filepath"
	"strings"
)

// SkippedText is why a Build & test that did not run was skipped.
const SkippedText = "nothing changed"

// IsDocFile reports whether a changed path is documentation, which does not
// need a build or a test: Markdown files, anything under doc/ or docs/ and
// LICENSE files.
func IsDocFile(p string) bool {
	p = filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
	if strings.EqualFold(filepath.Ext(p), ".md") {
		return true
	}
	if first, _, ok := strings.Cut(p, "/"); ok && (first == "doc" || first == "docs") {
		return true
	}
	return strings.HasPrefix(strings.ToUpper(filepath.Base(p)), "LICENSE")
}

// changedPaths are the files a node changed, as it reported or as seen on disk.
func changedPaths(n *Node) []string {
	out := append(append([]string(nil), n.Files...), n.Observed...)
	for _, fs := range n.FileStats {
		out = append(out, fs.Path)
	}
	return out
}

// ChangedSinceCheck reports whether a work node that ended after the check
// node last ran and passed changed a file that is not documentation.
func ChangedSinceCheck(g *Graph, check *Node) bool {
	for _, n := range g.Nodes {
		if n.Kind != KindWork || n.EndedAt.IsZero() || !n.EndedAt.After(check.LastRunAt) {
			continue
		}
		for _, p := range changedPaths(n) {
			if !IsDocFile(p) {
				return true
			}
		}
	}
	return false
}
