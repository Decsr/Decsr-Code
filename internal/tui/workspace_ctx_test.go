package tui

import (
	"strings"
	"testing"

	dctx "github.com/Decsr/Decsr-Code/internal/context"
)

func ctxFixture() ctxSource {
	return ctxSource{
		repo: true,
		pc: dctx.ProjectContext{
			IsBrownfield:  true,
			TreeStructure: "src/",
			Files:         []string{"a.go"},
			SourceFiles:   map[string]string{"a.go": "package a // EXCERPT", "CLAUDE.md": "claude rules"},
			AlwaysInclude: map[string]string{"AGENTS.md": "agents rules", "README.md": "readme text"},
		},
		pack: strings.Repeat("p", 6000),
	}
}

func TestContextRenderSkipsWhatTheRunnerLoads(t *testing.T) {
	s := ctxFixture()
	claude := s.render("claude", false, false)
	if strings.Contains(claude, "claude rules") || !strings.Contains(claude, "agents rules") || !strings.Contains(claude, "EXCERPT") {
		t.Errorf("claude context wrong:\n%s", claude)
	}
	codex := s.render("codex", false, false)
	if strings.Contains(codex, "agents rules") || !strings.Contains(codex, "claude rules") {
		t.Errorf("codex context wrong")
	}
	if other := s.render("dsh", false, false); !strings.Contains(other, "agents rules") || !strings.Contains(other, "claude rules") {
		t.Errorf("a runner that loads nothing keeps both files")
	}
}

func TestContextRenderAgentAndLead(t *testing.T) {
	s := ctxFixture()
	if got := s.render("claude", true, false); strings.Contains(got, "EXCERPT") || !strings.Contains(got, "a.go") {
		t.Errorf("an agent reads files itself, but keeps the file list:\n%s", got)
	}
	if got := s.render("claude", false, false); strings.Contains(got, strings.Repeat("p", 4500)) {
		t.Errorf("the context pack is not capped")
	}
	s.pack = strings.Repeat("line of the pack\n", 2000)
	lead := s.render("claude", true, true)
	if len(lead) > leadContextMax+20 || !strings.Contains(lead, "(shortened)") || strings.Contains(lead, "EXCERPT") {
		t.Errorf("lead context = %d bytes", len(lead))
	}
}
