package plugins

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"
)

// maxDescriptionRunes is the length of a description taken from the body.
const maxDescriptionRunes = 60

// Frontmatter holds the YAML header of a markdown file as a map.
type Frontmatter map[string]any

// ParseFrontmatter splits a markdown file that may start with
// "---\n<yaml>\n---\n". Without a header, or when the header is not valid
// YAML mapping and holds no "key: value" line, the whole text is returned as
// the body and the map is nil.
func ParseFrontmatter(text string) (Frontmatter, string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return nil, text
	}
	// The header ends at the first line that is only "---".
	var head, body string
	closed := false
	for off := 0; off <= len(rest); {
		end := strings.IndexByte(rest[off:], '\n')
		line, next := rest[off:], len(rest)+1
		if end >= 0 {
			line, next = rest[off:off+end], off+end+1
		}
		if strings.TrimRight(line, " \t") == "---" {
			head, closed = rest[:off], true
			if next <= len(rest) {
				body = rest[next:]
			}
			break
		}
		off = next
	}
	if !closed {
		return nil, text
	}
	fm := Frontmatter{}
	if err := yaml.Unmarshal([]byte(head), &fm); err != nil {
		fm = lineFrontmatter(head)
		if len(fm) == 0 {
			return nil, text
		}
	}
	return fm, body
}

var fmLineRe = regexp.MustCompile(`^([A-Za-z0-9_-]+):[ \t]*(.*)$`)

// lineFrontmatter reads a header that is not valid YAML: each top-level
// "key: value" line becomes a string entry (one pair of matching quotes is
// stripped); other lines are ignored.
func lineFrontmatter(head string) Frontmatter {
	fm := Frontmatter{}
	for _, line := range strings.Split(head, "\n") {
		m := fmLineRe.FindStringSubmatch(strings.TrimRight(line, " \t\r"))
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[2])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		fm[m[1]] = v
	}
	return fm
}

// String returns the value of key as one sanitized line ("" when absent). A
// list value is joined as "[a, b]".
func (f Frontmatter) String(key string) string {
	switch v := f[key].(type) {
	case string:
		return sanitize(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				parts = append(parts, sanitize(s))
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return ""
}

// List returns the value of key as a list: a YAML list, or a string split on
// commas and spaces. ok is false when the key is absent or null.
func (f Frontmatter) List(key string) (items []string, ok bool) {
	switch v := f[key].(type) {
	case string:
		return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }), true
	case []any:
		for _, e := range v {
			if s, isStr := e.(string); isStr {
				items = append(items, strings.TrimSpace(s))
			}
		}
		return items, true
	}
	return nil, false
}

// SplitCommand returns the description, argument hint and body of a command
// file. A frontmatter "description" wins. Otherwise a leading "# heading"
// line becomes the description and leaves the body, or the description is the
// first 60 characters of the body.
func SplitCommand(text string) (desc, hint, body string) {
	fm, text := ParseFrontmatter(text)
	text = strings.TrimSpace(text)
	if d := fm.String("description"); d != "" {
		return d, fm.String("argument-hint"), text
	}
	hint = fm.String("argument-hint")
	if first, rest, _ := strings.Cut(text, "\n"); strings.HasPrefix(first, "# ") {
		return strings.TrimSpace(strings.TrimPrefix(first, "# ")), hint, strings.TrimSpace(rest)
	}
	desc = strings.Join(strings.Fields(text), " ")
	if r := []rune(desc); len(r) > maxDescriptionRunes {
		desc = string(r[:maxDescriptionRunes])
	}
	return desc, hint, text
}

// sanitizeText removes terminal escape sequences and control characters. It
// mirrors workspace.SanitizeText, which this package cannot import.
func sanitizeText(s string) string {
	if strings.ContainsRune(s, '\x1b') {
		s = ansi.Strip(s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		}
		return r
	}, s)
}

// sanitize is sanitizeText for one line.
func sanitize(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(sanitizeText(s), "\n", " "))
}
