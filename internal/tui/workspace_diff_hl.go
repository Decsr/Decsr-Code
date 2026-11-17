package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// diffHLMaxLines is the most changed lines (added plus removed) of a file that
// still get syntax colours; a bigger file is drawn in one colour per row.
const diffHLMaxLines = 2000

// codeSeg is a run of code text in one foreground colour.
type codeSeg struct {
	text         string
	fg           string // hex from the chroma style, "" for the default
	bold, italic bool
}

// fileHL holds the coloured segments of every line of one file, [hunk][line],
// for one theme. A nil entry (the '\' lines) is drawn plain.
type fileHL struct {
	dark bool
	segs [][][]codeSeg
}

// diffSegments returns the segments of file f for the active theme, building
// them once. It returns nil when f gets no highlighting: too many changes or
// no lexer for the path.
func (v *wsDiffView) diffSegments(f *workspace.DiffFile) [][][]codeSeg {
	dark := ui.IsDark()
	if c, ok := v.hl[f]; ok && c.dark == dark {
		return c.segs
	}
	if v.hl == nil {
		v.hl = map[*workspace.DiffFile]*fileHL{}
	}
	c := &fileHL{dark: dark, segs: highlightFileDiff(f, chromaStyle())}
	v.hl[f] = c
	return c.segs
}

// highlightFileDiff lexes each hunk of f as two texts, the old side (context
// and '-' lines) and the new side (context and '+' lines), so a comment or
// string that spans lines is coloured right, and maps the tokens back to
// lines. Context lines take the new side.
func highlightFileDiff(f *workspace.DiffFile, style *chroma.Style) [][][]codeSeg {
	if f.Binary || f.Added+f.Removed > diffHLMaxLines {
		return nil
	}
	lexer := lexers.Match(f.Path)
	if lexer == nil {
		return nil
	}
	lexer = chroma.Coalesce(lexer)
	out := make([][][]codeSeg, len(f.Hunks))
	for h, hk := range f.Hunks {
		var oldLines, newLines []string
		oldIdx := make([]int, len(hk.Lines))
		newIdx := make([]int, len(hk.Lines))
		for i, l := range hk.Lines {
			oldIdx[i], newIdx[i] = -1, -1
			if l.Kind == '\\' {
				continue
			}
			t := sanitizeCode(l.Text)
			if l.Kind != '+' {
				oldIdx[i] = len(oldLines)
				oldLines = append(oldLines, t)
			}
			if l.Kind != '-' {
				newIdx[i] = len(newLines)
				newLines = append(newLines, t)
			}
		}
		oldSegs := lexLines(lexer, style, oldLines)
		newSegs := lexLines(lexer, style, newLines)
		out[h] = make([][]codeSeg, len(hk.Lines))
		for i, l := range hk.Lines {
			switch {
			case l.Kind == '\\':
			case l.Kind == '-' && oldIdx[i] < len(oldSegs):
				out[h][i] = oldSegs[oldIdx[i]]
			case l.Kind != '-' && newIdx[i] >= 0 && newIdx[i] < len(newSegs):
				out[h][i] = newSegs[newIdx[i]]
			}
		}
	}
	return out
}

// lexLines tokenises lines as one text and splits the tokens back into one
// segment list per line. It returns nil when lexing fails.
func lexLines(lexer chroma.Lexer, style *chroma.Style, lines []string) [][]codeSeg {
	if len(lines) == 0 {
		return nil
	}
	it, err := lexer.Tokenise(nil, strings.Join(lines, "\n"))
	if err != nil {
		return nil
	}
	out := make([][]codeSeg, 1, len(lines))
	for _, tok := range it.Tokens() {
		e := style.Get(tok.Type)
		seg := codeSeg{bold: e.Bold == chroma.Yes, italic: e.Italic == chroma.Yes}
		if e.Colour.IsSet() {
			seg.fg = e.Colour.String()
		}
		parts := strings.Split(tok.Value, "\n")
		for i, p := range parts {
			if i > 0 {
				out = append(out, nil)
			}
			if p != "" {
				s := seg
				s.text = p
				out[len(out)-1] = append(out[len(out)-1], s)
			}
		}
	}
	return out
}

// renderCodeSegs draws segs, scrolled left by hx cells, after the prefix
// segment, in exactly avail cells. Every piece carries the background of bg so
// the row has no stripes; text past avail ends in "…".
func renderCodeSegs(prefix string, segs []codeSeg, hx, avail int, bg lipgloss.Style, prefixFg lipgloss.TerminalColor) string {
	skip := hx
	var kept []codeSeg
	for _, s := range segs {
		w := ansi.StringWidth(s.text)
		if skip >= w {
			skip -= w
			continue
		}
		if skip > 0 {
			s.text = ansi.TruncateLeft(s.text, skip, "")
			skip = 0
		}
		kept = append(kept, s)
	}
	total := ansi.StringWidth(prefix)
	for _, s := range kept {
		total += ansi.StringWidth(s.text)
	}
	limit, cut := avail, total > avail
	if cut {
		limit = max(avail-1, 0)
	}
	var b strings.Builder
	used := 0
	put := func(st lipgloss.Style, text string) {
		room := limit - used
		if room <= 0 || text == "" {
			return
		}
		text = ansi.Truncate(text, room, "")
		used += ansi.StringWidth(text)
		b.WriteString(st.Render(text))
	}
	put(bg.Foreground(prefixFg), prefix)
	for _, s := range kept {
		st := bg
		if s.fg != "" {
			st = st.Foreground(lipgloss.AdaptiveColor{Dark: s.fg, Light: s.fg})
		} else {
			st = st.Foreground(ui.Text)
		}
		put(st.Bold(s.bold).Italic(s.italic), s.text)
	}
	if cut && avail > 0 {
		b.WriteString(bg.Foreground(ui.Text).Render("…"))
		used++
	}
	if used < avail {
		b.WriteString(bg.Render(strings.Repeat(" ", avail-used)))
	}
	return b.String()
}
