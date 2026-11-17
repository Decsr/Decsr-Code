package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func diffFile(path string, lines ...workspace.DiffLine) *workspace.DiffFile {
	f := &workspace.DiffFile{Path: path, Hunks: []workspace.Hunk{{Header: "@@ -1,3 +1,3 @@", Lines: lines}}}
	for _, l := range lines {
		switch l.Kind {
		case '+':
			f.Added++
		case '-':
			f.Removed++
		}
	}
	return f
}

func dl(kind byte, old, nw int, text string) workspace.DiffLine {
	return workspace.DiffLine{Kind: kind, OldNo: old, NewNo: nw, Text: text}
}

var fgRe = regexp.MustCompile(`38;2;(\d+;\d+;\d+)`)

func trueColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestDiffHighlightColoursGoCode(t *testing.T) {
	trueColor(t)
	f := diffFile("a.go",
		dl(' ', 1, 1, "package main"),
		dl('-', 2, 0, `var x = "old"`),
		dl('+', 0, 2, `var x = "new" // note`))
	v := &wsDiffView{diff: workspace.Diff{Files: []workspace.DiffFile{*f}}}
	f = &v.diff.Files[0]
	row := v.renderRow(f, diffRow{0, 2}, false, 60, 2)
	if w := ansi.StringWidth(row); w != 60 {
		t.Fatalf("row is %d cells", w)
	}
	seen := map[string]bool{}
	for _, m := range fgRe.FindAllStringSubmatch(row, -1) {
		seen[m[1]] = true
	}
	if len(seen) < 3 {
		t.Errorf("want several colours in the code, got %v in %q", seen, row)
	}
	if !strings.Contains(ansi.Strip(row), `+ var x = "new" // note`) {
		t.Errorf("text changed: %q", ansi.Strip(row))
	}
}

func TestDiffHighlightRowsAreExactWidthAtAnyScroll(t *testing.T) {
	trueColor(t)
	f := diffFile("a.go", dl('+', 0, 1, "func veryLongFunctionName(argumentOne, argumentTwo string) error { return nil }"), dl(' ', 1, 2, "x"))
	v := &wsDiffView{diff: workspace.Diff{Files: []workspace.DiffFile{*f}}}
	f = &v.diff.Files[0]
	for _, hx := range []int{0, 5, 30, 200} {
		v.hx = hx
		for _, w := range []int{14, 30, 60} {
			for line := 0; line < 2; line++ {
				for _, cur := range []bool{false, true} {
					if got := ansi.StringWidth(v.renderRow(f, diffRow{0, line}, cur, w, 2)); got != w {
						t.Errorf("hx %d w %d line %d cursor %v: %d cells", hx, w, line, cur, got)
					}
				}
			}
		}
	}
}

func TestDiffHighlightMultiLineComment(t *testing.T) {
	f := diffFile("a.go",
		dl(' ', 1, 1, "/* start"),
		dl('+', 0, 2, "   middle of the comment"),
		dl(' ', 2, 3, "   end */"))
	segs := highlightFileDiff(f, chromaStyle())
	if segs == nil {
		t.Fatal("no segments")
	}
	colour := func(line int) string {
		s := segs[0][line]
		if len(s) == 0 {
			t.Fatalf("line %d has no segments", line)
		}
		return s[0].fg
	}
	if colour(1) == "" || colour(1) != colour(0) || colour(2) != colour(0) {
		t.Errorf("comment colours differ: %q %q %q", colour(0), colour(1), colour(2))
	}
}

func TestDiffHighlightSkips(t *testing.T) {
	var lines []workspace.DiffLine
	for i := 0; i < diffHLMaxLines+1; i++ {
		lines = append(lines, dl('+', 0, i+1, fmt.Sprintf("var a%d = 1", i)))
	}
	if highlightFileDiff(diffFile("big.go", lines...), chromaStyle()) != nil {
		t.Error("a file over the limit was highlighted")
	}
	if highlightFileDiff(diffFile("notes.zzzunknown", dl('+', 0, 1, "hello")), chromaStyle()) != nil {
		t.Error("a file without a lexer was highlighted")
	}
}

func TestDiffUnknownExtensionRendersAsBefore(t *testing.T) {
	trueColor(t)
	f := diffFile("notes.zzzunknown", dl('+', 0, 1, "hello world"))
	v := &wsDiffView{diff: workspace.Diff{Files: []workspace.DiffFile{*f}}}
	f = &v.diff.Files[0]
	row := v.renderRow(f, diffRow{0, 0}, false, 40, 2)
	if ansi.StringWidth(row) != 40 {
		t.Fatalf("width %d", ansi.StringWidth(row))
	}
	seen := map[string]bool{}
	for _, m := range fgRe.FindAllStringSubmatch(row, -1) {
		seen[m[1]] = true
	}
	// the gutter numbers (muted) and the one-colour body
	if len(seen) > 2 {
		t.Errorf("plain row has %d colours: %q", len(seen), row)
	}
}
