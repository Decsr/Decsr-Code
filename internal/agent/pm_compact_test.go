package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/contextpack"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

func TestCompactRepository_AddsUserBlockAndStripsFence(t *testing.T) {
	mock := &runner.MockRunner{Responses: []string{
		"```markdown\n# Repository Context Pack\n\n## 1. Project Snapshot\n- Go service\n```",
	}}
	pm := NewPMAgent(bus.New(), mock, artifacts.Workspace{}, nil, "")

	out, err := pm.CompactRepository(context.Background(), "PROJECT_CONTEXT_MARKER", 6000)
	if err != nil {
		t.Fatalf("CompactRepository: %v", err)
	}

	if strings.HasPrefix(out, "```") {
		t.Errorf("outer code fence should be stripped:\n%s", out)
	}
	if _, ok := contextpack.ExtractUserBlock(out); !ok {
		t.Errorf("expected user block markers, got:\n%s", out)
	}
	if len(mock.Requests) != 1 {
		t.Fatalf("expected 1 completion, got %d", len(mock.Requests))
	}
	if !strings.Contains(mock.Requests[0].SystemPrompt, "PROJECT_CONTEXT_MARKER") {
		t.Errorf("project context missing from system prompt")
	}
	if !strings.Contains(mock.Requests[0].SystemPrompt, "6000") {
		t.Errorf("max token budget missing from system prompt")
	}
}

func TestCompactRepository_EmptyOutputErrors(t *testing.T) {
	mock := &runner.MockRunner{Responses: []string{"   \n"}}
	pm := NewPMAgent(bus.New(), mock, artifacts.Workspace{}, nil, "")

	if _, err := pm.CompactRepository(context.Background(), "ctx", 100); err == nil {
		t.Fatal("expected an error for empty output")
	}
}
