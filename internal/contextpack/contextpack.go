// Package contextpack defines the user-editable markers and helpers for the
// repository context pack. The generated pack is Markdown; the block between
// the BEGIN/END markers is owned by the user and is preserved across
// regenerations.
package contextpack

import "strings"

const (
	// BeginMarker and EndMarker delimit the user-editable section.
	BeginMarker = "<!-- USER-CONTEXT:BEGIN -->"
	EndMarker   = "<!-- USER-CONTEXT:END -->"

	// UserHeading is the heading displayed above the user-editable section.
	UserHeading = "## 7. Additional Context — user-editable"

	// DefaultUserBlock is inserted when a pack has no user section yet.
	DefaultUserBlock = BeginMarker + "\n" +
		"<!-- Add free-form text, design notes, constraints, URLs, examples, etc.\n" +
		"     This block is preserved when the context pack is regenerated. -->\n" +
		EndMarker
)

// EnsureUserBlock appends the user-editable section when doc does not contain
// the markers. Existing blocks are left untouched apart from adding the
// section heading when the model omitted it.
func EnsureUserBlock(doc string) string {
	if hasUserBlock(doc) {
		if !strings.Contains(doc, UserHeading) {
			if start := strings.Index(doc, BeginMarker); start >= 0 {
				return doc[:start] + UserHeading + "\n\n" + doc[start:]
			}
		}
		return doc
	}
	doc = strings.TrimRight(doc, "\n")
	return doc + "\n\n" + UserHeading + "\n\n" + DefaultUserBlock + "\n"
}

// ExtractUserBlock returns the full user block (including markers) and true
// when both markers are present. The returned text is verbatim.
func ExtractUserBlock(doc string) (string, bool) {
	start := strings.Index(doc, BeginMarker)
	if start < 0 {
		return "", false
	}
	rel := strings.Index(doc[start:], EndMarker)
	if rel < 0 {
		return "", false
	}
	end := start + rel + len(EndMarker)
	return doc[start:end], true
}

// ReplaceUserBlock replaces the existing user block with block. When the
// document has no user block, the block is appended under UserHeading.
// The bool reports whether an existing block was replaced.
func ReplaceUserBlock(doc, block string) (string, bool) {
	if !strings.Contains(block, BeginMarker) || !strings.Contains(block, EndMarker) {
		return doc, false
	}
	start := strings.Index(doc, BeginMarker)
	if start < 0 {
		doc = strings.TrimRight(doc, "\n")
		return doc + "\n\n" + UserHeading + "\n\n" + block + "\n", false
	}
	rel := strings.Index(doc[start:], EndMarker)
	if rel < 0 {
		return doc, false
	}
	end := start + rel + len(EndMarker)
	return doc[:start] + block + doc[end:], true
}

// StripOuterCodeFence removes a Markdown fence that wraps the entire document.
// Models sometimes return the whole pack inside ```markdown ... ``` even when
// asked not to. Inner fences are left intact.
func StripOuterCodeFence(doc string) string {
	trimmed := strings.TrimSpace(doc)
	if !strings.HasPrefix(trimmed, "```") {
		return doc
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return doc
	}
	first := strings.TrimSpace(lines[0])
	if first != "```" && first != "```markdown" && first != "```md" {
		return doc
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if last != "```" {
		return doc
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}

func hasUserBlock(doc string) bool {
	start := strings.Index(doc, BeginMarker)
	if start < 0 {
		return false
	}
	return strings.Contains(doc[start+len(BeginMarker):], EndMarker)
}
