package tui

import (
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// markdownMinWidth is the narrowest pane that renders Markdown; below it the
// text stays plain.
const markdownMinWidth = 60

// markdownOff is set by ApplyTheme when ui.markdown is false.
var markdownOff atomic.Bool

type mdKey struct {
	width int
	dark  bool
}

var (
	mdMu        sync.Mutex
	mdRenderers = map[mdKey]*glamour.TermRenderer{}
	mdTrailing  = regexp.MustCompile(`(?: +(?:\x1b\[[0-9;]*m)*)+$`)
	mdTrailSGR  = regexp.MustCompile(`(?:\x1b\[[0-9;]*m)+$`)
)

// markdownActive reports whether Markdown is on for a pane width.
func markdownActive(width int) bool { return !markdownOff.Load() && width >= markdownMinWidth }

// renderMarkdown renders src, which the caller has already sanitised, to lines
// of at most width cells. ok is false when the text should stay plain: Markdown
// is off, the width is under markdownMinWidth or glamour failed. Callers pass
// only finished text, never one still streaming.
func renderMarkdown(src string, width int) (lines []string, ok bool) {
	if !markdownActive(width) || strings.TrimSpace(src) == "" {
		return nil, false
	}
	r := mdRenderer(width)
	if r == nil {
		return nil, false
	}
	mdMu.Lock()
	out, err := r.Render(src)
	mdMu.Unlock()
	if err != nil {
		return nil, false
	}
	lines = strings.Split(out, "\n")
	for i, l := range lines {
		l = mdTrailSGR.ReplaceAllString(mdTrailing.ReplaceAllString(l, ""), "")
		switch {
		case strings.TrimSpace(ansi.Strip(l)) == "":
			l = ""
		case strings.Contains(l, "\x1b["):
			l += "\x1b[0m" // every line is self-contained
		}
		lines[i] = l
		if ansi.StringWidth(lines[i]) > width {
			lines[i] = ansi.Truncate(lines[i], width, "")
		}
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	return lines, len(lines) > 0
}

// mdRenderer returns the renderer of a width and theme, building it once.
func mdRenderer(width int) *glamour.TermRenderer {
	k := mdKey{width, ui.IsDark()}
	mdMu.Lock()
	defer mdMu.Unlock()
	if r, ok := mdRenderers[k]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithColorProfile(lipgloss.DefaultRenderer().ColorProfile()),
		glamour.WithStyles(mdStyle(k.dark)),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		r = nil
	}
	mdRenderers[k] = r
	return r
}

// mdStyle is the glamour style of the decsr theme, built from the ui tokens.
func mdStyle(dark bool) gansi.StyleConfig {
	pick := func(c lipgloss.AdaptiveColor) *string {
		s := c.Light
		if dark {
			s = c.Dark
		}
		return &s
	}
	yes, zero := true, uint(0)
	str := func(s string) *string { return &s }
	theme := "github"
	if dark {
		theme = "github-dark"
	}
	block := gansi.StyleBlock{Margin: &zero}
	head := gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Color: pick(ui.Accent), Bold: &yes}, Margin: &zero}
	return gansi.StyleConfig{
		Document:  gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Color: pick(ui.Text)}, Margin: &zero},
		Paragraph: block,
		BlockQuote: gansi.StyleBlock{
			StylePrimitive: gansi.StylePrimitive{Color: pick(ui.Muted)},
			Indent:         func() *uint { i := uint(1); return &i }(),
			IndentToken:    str("│ "),
		},
		List:           gansi.StyleList{StyleBlock: block, LevelIndent: 2},
		Heading:        head,
		H1:             head,
		H2:             head,
		H3:             head,
		H4:             head,
		H5:             head,
		H6:             head,
		Strong:         gansi.StylePrimitive{Bold: &yes},
		Emph:           gansi.StylePrimitive{Italic: &yes},
		Strikethrough:  gansi.StylePrimitive{CrossedOut: &yes},
		HorizontalRule: gansi.StylePrimitive{Color: pick(ui.Border), Format: "────────"},
		Item:           gansi.StylePrimitive{BlockPrefix: "• "},
		Enumeration:    gansi.StylePrimitive{BlockPrefix: ". "},
		Link:           gansi.StylePrimitive{Color: pick(ui.Muted)},
		LinkText:       gansi.StylePrimitive{Color: pick(ui.Accent)},
		Code:           gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Color: pick(ui.Warn)}},
		CodeBlock: gansi.StyleCodeBlock{
			StyleBlock: gansi.StyleBlock{Margin: &zero},
			Theme:      theme,
		},
	}
}
