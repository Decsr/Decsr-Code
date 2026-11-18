package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
)

const (
	// mapMaxDocs is the most files named under doc/ and docs/.
	mapMaxDocs = 30
	// mapMaxExports is the most /export files named.
	mapMaxExports = 20
	// mapMemoryMax caps the project memory notes, in characters.
	mapMemoryMax = 2000
)

// ProjectMap is what a lead with tools is told about the project instead of
// file contents: the language, which instruction files exist, the documents and
// exports it can read, and the project memory notes. The lead reads the files
// itself when it needs them.
func ProjectMap(root, language string) string {
	var sb strings.Builder
	if language != "" {
		sb.WriteString("Language: " + language + "\n")
	}
	if names := instructionFiles(root); len(names) > 0 {
		sb.WriteString("Instruction files in the project root: " + strings.Join(names, ", ") + "\n")
	}
	if docs := docFiles(root); len(docs) > 0 {
		sb.WriteString("\nFiles under doc/ and docs/:\n")
		for _, d := range docs {
			sb.WriteString("- " + d + "\n")
		}
	}
	if ex := exportFiles(root); len(ex) > 0 {
		sb.WriteString("\nExports of earlier sessions (/export, newest first):\n")
		for _, e := range ex {
			sb.WriteString("- " + e + "\n")
		}
	}
	pack := filepath.Join(artifacts.DirName, artifacts.ContextPackFile)
	if _, err := os.Stat(filepath.Join(root, pack)); err == nil {
		sb.WriteString("\nContext pack: " + pack + "\n")
	}
	if b, err := os.ReadFile(filepath.Join(root, artifacts.DirName, artifacts.MemoryDirName, "MEMORY.md")); err == nil { // #nosec G304 -- fixed path under the project root
		if mem := strings.TrimSpace(string(b)); mem != "" {
			sb.WriteString("\nProject memory notes:\n" + cutRunes(mem, mapMemoryMax) + "\n")
		}
	}
	if sb.Len() == 0 {
		return ""
	}
	sb.WriteString("\nThese files are not included here: read the ones you need with your tools.")
	return strings.TrimSpace(sb.String())
}

// instructionFiles lists AGENTS.md, CLAUDE.md, README* and CONTRIBUTING* of
// the project root.
func instructionFiles(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		up := strings.ToUpper(e.Name())
		if up == "AGENTS.MD" || up == "CLAUDE.MD" || strings.HasPrefix(up, "README") || strings.HasPrefix(up, "CONTRIBUTING") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// docFiles is up to mapMaxDocs file paths under doc/ and docs/, in path order.
func docFiles(root string) []string {
	var out []string
	for _, dir := range []string{"doc", "docs"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if len(out) >= mapMaxDocs {
				return fs.SkipAll
			}
			if d.IsDir() {
				if p != filepath.Join(root, dir) && strings.HasPrefix(d.Name(), ".") {
					return fs.SkipDir
				}
				return nil
			}
			if rel, err := filepath.Rel(root, p); err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
			return nil
		})
	}
	return out
}

// exportFiles is the newest .decsr/exports/*.md files, newest first.
func exportFiles(root string) []string {
	dir := filepath.Join(root, artifacts.DirName, "exports")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type f struct {
		name string
		mod  int64
	}
	var files []f
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, f{e.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod != files[j].mod {
			return files[i].mod > files[j].mod
		}
		return files[i].name > files[j].name
	})
	var out []string
	for _, x := range files {
		if len(out) >= mapMaxExports {
			break
		}
		out = append(out, filepath.ToSlash(filepath.Join(artifacts.DirName, "exports", x.name)))
	}
	return out
}
