package tui

import (
	"strings"

	dctx "github.com/Decsr/Decsr-Code/internal/context"
)

const (
	// leadContextMax caps the project context the lead gets: it needs an
	// overview of the project, not every source file.
	leadContextMax = 10000
	// contextPackMax caps the saved context pack in a prompt.
	contextPackMax = 4000
)

// ctxSource is the project context before it is rendered, so each prompt gets
// only what its reader needs.
type ctxSource struct {
	pc   dctx.ProjectContext
	repo bool   // pc was collected
	pack string // saved context pack, "" for none
}

// runnerLoads is the instruction file each runner reads by itself, so decsr
// does not send it again.
var runnerLoads = map[string]string{"claude": "CLAUDE.md", "codex": "AGENTS.md", "opencode": "AGENTS.md"}

func withoutFile(m map[string]string, name string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if name == "" || !strings.EqualFold(k, name) {
			out[k] = v
		}
	}
	return out
}

// cutLines keeps whole lines of s within max bytes.
func cutLines(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n… (shortened)\n"
}

// render builds the context text for a reader that runs on runnerName. An
// agent (or the lead) can read files itself, so the source excerpts are left
// out; a worker without tools keeps them. The lead's context is also capped.
func (s ctxSource) render(runnerName string, agentMode, lead bool) string {
	var out string
	if s.repo {
		pc := s.pc
		loaded := runnerLoads[strings.ToLower(runnerName)]
		pc.AlwaysInclude = withoutFile(pc.AlwaysInclude, loaded)
		pc.SourceFiles = withoutFile(pc.SourceFiles, loaded)
		if agentMode || lead {
			pc.SourceFiles = nil
		}
		out = pc.SystemPromptFragment()
	}
	if s.pack != "" {
		out += "\n\n## Context pack\n" + cutLines(s.pack, contextPackMax)
	}
	if lead {
		out = cutLines(out, leadContextMax)
	}
	return out
}
