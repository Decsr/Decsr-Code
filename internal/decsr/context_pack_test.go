package decsr

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
	appctx "github.com/Decsr/Decsr-Code/internal/context"
	"github.com/Decsr/Decsr-Code/internal/contextpack"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

func testContextPackRunner(t *testing.T, responses ...string) (*TaskRunner, *runner.MockRunner, artifacts.Workspace) {
	t.Helper()
	root := t.TempDir()
	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	b := bus.New()
	mock := &runner.MockRunner{Responses: responses}
	pm := agent.NewPMAgent(b, mock, ws, nil, "")
	cfg := config.Config{Project: config.ProjectConfig{
		Language: "go",
		TestCmd:  "go test ./...",
		LintCmd:  "go vet ./...",
		Context: config.ContextConfig{
			Pack: config.ContextPackConfig{MaxOutputTokens: 6000},
		},
	}}
	tr := NewTaskRunner(b, map[bus.AgentRole]agent.Agent{bus.RolePM: pm}, cfg, ws, root)
	tr.SetInteractive(false)
	return tr, mock, ws
}

func TestBuildFallbackContextPack_HasUserBlockAndFacts(t *testing.T) {
	pc := appctx.ProjectContext{
		Files:               []string{"main.go", "internal/app/app.go"},
		ProjectType:         "go",
		ProgrammingLanguage: "go",
		IsBrownfield:        true,
		TreeStructure:       "internal/\n  app/\n",
	}
	pack := buildFallbackContextPack(pc, config.Config{Project: config.ProjectConfig{TestCmd: "go test ./..."}}, "/tmp/demo")

	if _, ok := contextpack.ExtractUserBlock(pack); !ok {
		t.Fatalf("fallback pack missing user block:\n%s", pack)
	}
	for _, want := range []string{"# Repository Context Pack", "go test ./...", "main.go", "internal/app/app.go"} {
		if !strings.Contains(pack, want) {
			t.Errorf("fallback pack missing %q:\n%s", want, pack)
		}
	}
}

func TestCapContextPackGenerated_PreservesUserBlock(t *testing.T) {
	userBlock := contextpack.BeginMarker + "\nUSER_MARKER\n" + contextpack.EndMarker
	doc := "# Pack\n\n" + strings.Repeat("generated content ", 200) + "\n\n" + userBlock + "\n\n## 8. Open Questions\n\nAFTER_MARKER\n"

	got := capContextPackGenerated(doc, 50)

	if !strings.Contains(got, "USER_MARKER") {
		t.Fatalf("user block was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "AFTER_MARKER") {
		t.Fatalf("content after the user block was not preserved:\n%s", got)
	}
	if !strings.Contains(got, "truncated to fit token budget") {
		t.Fatalf("expected generated body to be truncated:\n%s", got)
	}
}

func TestBuildContextFragment_UsesPackAsPrimary(t *testing.T) {
	tr, _, _ := testContextPackRunner(t)
	tr.contextPack = "PACK_MARKER"
	tr.cfg.Project.TestCmd = "go test ./..."
	tr.cfg.Project.LintCmd = "go vet ./..."

	pc := appctx.ProjectContext{
		ProjectType:         "go",
		ProgrammingLanguage: "go",
		IsBrownfield:        true,
		TreeStructure:       "internal/\n",
	}
	frag := tr.buildContextFragment(pc)

	for _, want := range []string{"PACK_MARKER", "Approved Repository Context Pack", "go test ./...", "go vet ./...", "Existing codebase: yes", "internal/"} {
		if !strings.Contains(frag, want) {
			t.Errorf("context fragment missing %q:\n%s", want, frag)
		}
	}
}

func TestBuildCompactionInput_IncludesSourceAndCommands(t *testing.T) {
	pc := appctx.ProjectContext{
		Files:         []string{"main.go", "internal/app/app.go"},
		SourceFiles:   map[string]string{"main.go": "package main\nfunc main() {}"},
		AlwaysInclude: map[string]string{"README.md": "hello"},
		TreeStructure: "internal/\n",
	}
	input := buildCompactionInput(pc, config.Config{Project: config.ProjectConfig{
		TestCmd: "go test ./...",
		LintCmd: "go vet ./...",
	}})

	for _, want := range []string{"go test ./...", "go vet ./...", "main.go", "package main", "README.md", "hello"} {
		if !strings.Contains(input, want) {
			t.Errorf("compaction input missing %q:\n%s", want, input)
		}
	}
}

func TestLoadContextPack_RequiresApprovalMarker(t *testing.T) {
	tr, _, ws := testContextPackRunner(t)
	if _, ok := tr.loadContextPack(); ok {
		t.Fatal("empty workspace should not load a pack")
	}

	if err := ws.WriteFile(artifacts.ContextPackFile, []byte("# Pack\n")); err != nil {
		t.Fatal(err)
	}
	if _, ok := tr.loadContextPack(); ok {
		t.Fatal("pack without approval marker should not load")
	}

	if err := ws.WriteFile(artifacts.ContextPackApprovedFile, []byte("approved\n")); err != nil {
		t.Fatal(err)
	}
	pack, ok := tr.loadContextPack()
	if !ok || !strings.Contains(pack, "# Pack") {
		t.Fatalf("approved pack should load, got ok=%v pack=%q", ok, pack)
	}
}

func TestRunContextPackGate_PlainModeAutoApproves(t *testing.T) {
	tr, _, ws := testContextPackRunner(t, "# Repository Context Pack\n\n## 1. Project Snapshot\n- demo\n")
	pc := appctx.ProjectContext{Files: []string{"main.go"}, ProjectType: "go"}

	pack, err := tr.runContextPackGate(context.Background(), pc)
	if err != nil {
		t.Fatalf("runContextPackGate: %v", err)
	}
	if !strings.Contains(pack, "# Repository Context Pack") {
		t.Fatalf("unexpected pack: %q", pack)
	}
	if _, ok := contextpack.ExtractUserBlock(pack); !ok {
		t.Fatalf("pack missing user block: %q", pack)
	}
	if _, err := ws.ReadFile(artifacts.ContextPackFile); err != nil {
		t.Fatalf("context pack file missing: %v", err)
	}
	if _, err := ws.ReadFile(artifacts.ContextPackApprovedFile); err != nil {
		t.Fatalf("approval marker missing: %v", err)
	}
}

func TestRunContextPackGate_PreservesEditedUserBlock(t *testing.T) {
	tr, _, ws := testContextPackRunner(t,
		"# Pack v1\n\nGENERATED_ONE\n",
		"# Pack v2\n\nGENERATED_TWO\n",
	)
	pc := appctx.ProjectContext{Files: []string{"main.go"}}

	if _, err := tr.runContextPackGate(context.Background(), pc); err != nil {
		t.Fatalf("first gate: %v", err)
	}

	// Simulate the user editing the user block between runs.
	edited := "# Pack v1\n\nGENERATED_ONE\n\n" + contextpack.UserHeading + "\n\n" +
		contextpack.BeginMarker + "\nMY IMPORTANT CONTEXT\n" + contextpack.EndMarker + "\n"
	if err := ws.WriteFile(artifacts.ContextPackFile, []byte(edited)); err != nil {
		t.Fatal(err)
	}

	pack, err := tr.runContextPackGate(context.Background(), pc)
	if err != nil {
		t.Fatalf("second gate: %v", err)
	}
	if !strings.Contains(pack, "GENERATED_TWO") {
		t.Errorf("regenerated content missing:\n%s", pack)
	}
	if !strings.Contains(pack, "MY IMPORTANT CONTEXT") {
		t.Errorf("user context was not preserved:\n%s", pack)
	}
}

func TestAskIntent_PlainModeUsesInitialText(t *testing.T) {
	tr, _, _ := testContextPackRunner(t)
	got, err := tr.askIntent(context.Background(), "add a health endpoint")
	if err != nil {
		t.Fatalf("askIntent: %v", err)
	}
	if got != "add a health endpoint" {
		t.Fatalf("got %q", got)
	}
}

func TestAskIntent_InteractiveWaitsForHuman(t *testing.T) {
	tr, _, _ := testContextPackRunner(t)
	tr.SetInteractive(true)
	tr.SendHumanReply("fix the parser")

	got, err := tr.askIntent(context.Background(), "")
	if err != nil {
		t.Fatalf("askIntent: %v", err)
	}
	if got != "fix the parser" {
		t.Fatalf("got %q", got)
	}
}

func TestContextPackEnabled_DisableFlag(t *testing.T) {
	tr, _, _ := testContextPackRunner(t)
	if !tr.contextPackEnabled() {
		t.Fatal("context pack should be enabled by default")
	}
	tr.cfg.Project.Context.Pack.Disable = true
	if tr.contextPackEnabled() {
		t.Fatal("context pack should be disabled")
	}
}

func TestRunContextPackGate_ApprovalUsesEditedFile(t *testing.T) {
	tr, _, ws := testContextPackRunner(t, "# Pack\n\nGENERATED\n")
	tr.SetInteractive(true)
	pc := appctx.ProjectContext{Files: []string{"main.go"}}

	type result struct {
		pack string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		pack, err := tr.runContextPackGate(context.Background(), pc)
		done <- result{pack: pack, err: err}
	}()

	// Wait until the generated pack is on disk, then simulate the user's edit.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := ws.ReadFile(artifacts.ContextPackFile); err == nil && len(data) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for context pack")
		}
		time.Sleep(5 * time.Millisecond)
	}

	edited := "# Pack\n\nGENERATED\n\n" + contextpack.UserHeading + "\n\n" +
		contextpack.BeginMarker + "\nUSER EDIT\n" + contextpack.EndMarker + "\n"
	if err := ws.WriteFile(artifacts.ContextPackFile, []byte(edited)); err != nil {
		t.Fatal(err)
	}
	tr.Approve()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("runContextPackGate: %v", r.err)
		}
		if !strings.Contains(r.pack, "USER EDIT") {
			t.Fatalf("approved pack did not use the edited file:\n%s", r.pack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for approval")
	}
}
