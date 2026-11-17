package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

const mdSample = "# Plan\n\nThis is **bold** text and `code`.\n\n- first item\n- second item\n\n> quoted words\n\n```go\nfunc main() {}\n```\n"

func TestRenderMarkdownStructure(t *testing.T) {
	lines, ok := renderMarkdown(mdSample, 80)
	if !ok {
		t.Fatal("expected markdown to render")
	}
	all := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(all, "#") {
		t.Errorf("heading kept its #: %q", all)
	}
	for _, want := range []string{"Plan", "bold", "• first item", "• second item", "│ quoted", "func main"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %q", want, all)
		}
	}
	if strings.Contains(all, "**") || strings.Contains(all, "`") {
		t.Errorf("markup left in %q", all)
	}
	if strings.TrimSpace(lines[len(lines)-1]) == "" || strings.TrimSpace(lines[0]) == "" {
		t.Error("blank edge lines were kept")
	}
}

func TestRenderMarkdownBoldIsStyled(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	lines, ok := renderMarkdown("some **bold** word", 80)
	if !ok || !strings.Contains(strings.Join(lines, ""), "\x1b[") {
		t.Fatalf("no styling in %q", lines)
	}
}

func TestRenderMarkdownFallbacks(t *testing.T) {
	if _, ok := renderMarkdown(mdSample, 59); ok {
		t.Error("rendered below 60 columns")
	}
	if _, ok := renderMarkdown(mdSample, 60); !ok {
		t.Error("did not render at 60 columns")
	}
	markdownOff.Store(true)
	defer markdownOff.Store(false)
	if _, ok := renderMarkdown(mdSample, 80); ok {
		t.Error("rendered with ui.markdown off")
	}
}

func TestRenderMarkdownNeverExceedsWidth(t *testing.T) {
	long := strings.Repeat("word ", 60) + "\n\n- " + strings.Repeat("item ", 40) + "\n\n> " + strings.Repeat("quote ", 40) +
		"\n\n[link](https://example.com/" + strings.Repeat("a", 90) + ")\n\n```\n" + strings.Repeat("x", 120) + "\n```\n"
	for _, w := range []int{60, 72, 100} {
		lines, ok := renderMarkdown(long, w)
		if !ok {
			t.Fatalf("width %d: not rendered", w)
		}
		for _, l := range lines {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("width %d: line of %d cells: %q", w, got, ansi.Strip(l))
			}
		}
	}
}

func TestFeedMessageUsesMarkdownWhenWide(t *testing.T) {
	wide := ansi.Strip(strings.Join(renderFeedMsg("lead", "# Title\n\n- one", feedT0, 80), "\n"))
	if strings.Contains(wide, "#") || !strings.Contains(wide, "• one") {
		t.Errorf("wide message not rendered as markdown: %q", wide)
	}
	narrow := ansi.Strip(strings.Join(renderFeedMsg("lead", "# Title\n\n- one", feedT0, 40), "\n"))
	if !strings.Contains(narrow, "# Title") {
		t.Errorf("narrow message should stay plain: %q", narrow)
	}
}

func TestRenderMarkdownLinesAreSelfContained(t *testing.T) {
	trueColor(t)
	lines, ok := renderMarkdown(mdSample, 80)
	if !ok {
		t.Fatal("expected markdown to render")
	}
	sgr := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	for i, l := range lines {
		if strings.TrimSpace(ansi.Strip(l)) == "" {
			if l != "" {
				t.Errorf("line %d: blank line is %q", i, l)
			}
			continue
		}
		all := sgr.FindAllString(l, -1)
		if len(all) > 0 && all[len(all)-1] != "\x1b[0m" && all[len(all)-1] != "\x1b[m" {
			t.Errorf("line %d ends with an open SGR: %q", i, l)
		}
	}
}
