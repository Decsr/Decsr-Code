package workspace

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// maxCommandBytes caps one custom command file.
const maxCommandBytes = 64 * 1024

// maxDescriptionRunes is the length of a description taken from the body.
const maxDescriptionRunes = 60

// CustomCommand is a user-defined /name command read from a markdown file.
type CustomCommand struct {
	Name, Description, Body, Source string // Source: "project" or "user"
}

// LoadCustomCommands reads the *.md files of <root>/.decsr/commands and then
// ~/.decsr/commands. A project command wins over a user command of the same
// name. Files with an invalid name, over 64 KB, empty, not regular (links,
// directories), containing NUL bytes or unreadable are skipped. Descriptions
// have terminal escape sequences and control characters removed. The result is sorted by name.
func LoadCustomCommands(projectRoot string) []CustomCommand {
	seen := map[string]bool{}
	var out []CustomCommand
	load := func(dir, source string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if name == e.Name() || !config.ValidName(name) || seen[name] || !e.Type().IsRegular() {
				continue
			}
			body, ok := readCommandFile(filepath.Join(dir, e.Name()))
			if !ok {
				continue
			}
			desc, body := splitCommand(body)
			desc = Sanitize(desc)
			if body == "" {
				continue
			}
			seen[name] = true
			out = append(out, CustomCommand{Name: name, Description: desc, Body: body, Source: source})
		}
	}
	load(filepath.Join(projectRoot, config.GlobalDir, "commands"), "project")
	if home, err := os.UserHomeDir(); err == nil {
		load(filepath.Join(home, config.GlobalDir, "commands"), "user")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// readCommandFile reads at most 64 KB; a bigger file is reported as not ok.
func readCommandFile(path string) (string, bool) {
	f, err := os.Open(path) // #nosec G304 -- directory listing entry with a validated name
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxCommandBytes+1))
	if err != nil || len(data) > maxCommandBytes || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

// splitCommand returns the description and the body of a command file: a
// leading "# heading" line becomes the description and leaves the body;
// otherwise the description is the first 60 characters of the body.
func splitCommand(text string) (desc, body string) {
	text = strings.TrimSpace(text)
	if first, rest, _ := strings.Cut(text, "\n"); strings.HasPrefix(first, "# ") {
		return strings.TrimSpace(strings.TrimPrefix(first, "# ")), strings.TrimSpace(rest)
	}
	desc = strings.Join(strings.Fields(text), " ")
	if r := []rune(desc); len(r) > maxDescriptionRunes {
		desc = string(r[:maxDescriptionRunes])
	}
	return desc, text
}

// Expand returns the task text: every $ARGUMENTS in the body replaced by the
// trimmed args. A body without $ARGUMENTS gets the args appended after a blank
// line when there are any.
func (c CustomCommand) Expand(args string) string {
	args = strings.TrimSpace(args)
	if !strings.Contains(c.Body, "$ARGUMENTS") {
		if args == "" {
			return c.Body
		}
		return c.Body + "\n\n" + args
	}
	return strings.ReplaceAll(c.Body, "$ARGUMENTS", args)
}
