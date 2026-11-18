package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// wrapViolations reports every line that the next line's first word would have
// fit on, and every line wider than w.
func wrapViolations(lines []string, w int) []string {
	var bad []string
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			bad = append(bad, "too wide: "+l)
		}
		if i+1 < len(lines) {
			next := strings.Fields(lines[i+1])
			if len(next) > 0 && ansi.StringWidth(l)+1+ansi.StringWidth(next[0]) <= w {
				bad = append(bad, "early break: "+l+" / "+lines[i+1])
			}
		}
	}
	return bad
}

func TestFeedMsgWrapsOnceAtTheInnerWidth(t *testing.T) {
	para := strings.Repeat("This directory looks like a scratch or work area for Decsr-Code, a Go terminal-UI tool, with a stray dbg_test.go and all.txt files in it. ", 5)
	list := "- first item " + strings.Repeat("with a long text that goes on and on ", 4) + "\n- second item"
	for _, w := range []int{40, 60, 77, 94, 100, 120} {
		for name, text := range map[string]string{"paragraph": para, "list": list} {
			var lines []string
			for _, l := range renderFeedMsg("lead", text, time.Now(), w)[1:] {
				lines = append(lines, strings.TrimPrefix(ansi.Strip(l), "  "))
			}
			if name == "list" {
				continue // hanging indent: only the width is checked below
			}
			if bad := wrapViolations(lines, w-2); len(bad) > 0 {
				t.Errorf("width %d %s:\n%s", w, name, strings.Join(bad, "\n"))
			}
		}
		for _, l := range renderFeedMsg("lead", list, time.Now(), w) {
			if ansi.StringWidth(l) > w {
				t.Errorf("width %d: list line too wide: %q", w, ansi.Strip(l))
			}
		}
	}
}

func TestFeedMsgNeverBreaksAWordThatFits(t *testing.T) {
	text := strings.Repeat("then head -15 all.txt ", 30)
	for _, w := range []int{30, 47, 61, 100} {
		for _, l := range renderFeedMsg("lead", text, time.Now(), w)[1:] {
			for _, word := range strings.Fields(ansi.Strip(l)) {
				if word != "then" && word != "head" && word != "-15" && word != "all.txt" {
					t.Errorf("width %d: broken word %q in %q", w, word, ansi.Strip(l))
				}
			}
		}
	}
}
