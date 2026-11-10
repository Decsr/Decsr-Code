package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

type fakeTokenRunner struct{ chunks []string }

func (f fakeTokenRunner) Complete(_ context.Context, _ runner.CompletionRequest) (<-chan runner.Token, error) {
	ch := make(chan runner.Token, len(f.chunks)+1)
	for _, c := range f.chunks {
		ch <- runner.Token{Text: c}
	}
	ch <- runner.Token{Done: true}
	close(ch)
	return ch, nil
}

func TestCoderResearch_HeadingNotGluedToPreamble(t *testing.T) {
	root := t.TempDir()
	r := fakeTokenRunner{chunks: []string{"I'll start by checking.", "### Current state\n\nbody"}}
	a := NewCoderAgent(bus.New(), r, artifacts.Workspace{Dir: root}, root, config.DefaultConfig(), nil, "")

	got, err := a.Research(context.Background(), CoderResearchPayload{Requirements: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "### Current state") {
		t.Errorf("report should start at the heading, got %q", got)
	}
}

func TestTidyResearchReport(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"glued preamble dropped", "I'll start by checking.### Current state\n\nbody", "### Current state\n\nbody"},
		{"preamble on own line dropped", "Let me look.\n\n## Findings\n\nx", "## Findings\n\nx"},
		{"heading first unchanged", "## A\n\ntext", "## A\n\ntext"},
		{"no headings unchanged", "just prose. no headings", "just prose. no headings"},
		{"glued heading mid-document split", "## A\n\ntext.### B\n\nmore", "## A\n\ntext.\n\n### B\n\nmore"},
		{"long preamble kept", strings.Repeat("word ", 100) + "\n\n## A", strings.Repeat("word ", 100) + "\n\n## A"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tidyResearchReport(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
