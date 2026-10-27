package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/decsr"
)

func TestGateLabel_ContextPack(t *testing.T) {
	if got := gateLabel(artifacts.ContextPackFile); got != "context" {
		t.Fatalf("gateLabel(%q) = %q, want context", artifacts.ContextPackFile, got)
	}
}

func TestActiveContextGates(t *testing.T) {
	tr := decsr.NewTaskRunner(nil, nil, config.Config{}, artifacts.Workspace{}, "")
	m := New(nil, "", "", nil, config.Config{})
	m.taskRunner = tr

	if gates := m.activeContextGates(); len(gates) != 1 || gates[0] != artifacts.ContextPackFile {
		t.Fatalf("expected context gate, got %v", gates)
	}

	m.contextPackEnabled = false
	if gates := m.activeContextGates(); len(gates) != 0 {
		t.Fatalf("disabled context pack should have no gate, got %v", gates)
	}
}

func TestRenderPhaseBar_ShowsContextGate(t *testing.T) {
	tr := decsr.NewTaskRunner(nil, nil, config.Config{}, artifacts.Workspace{}, "")
	m := New(nil, "", "", nil, config.Config{})
	m.taskRunner = tr
	m.contextPackEnabled = true
	m.phase = "compacting"

	bar := m.renderPhaseBar()
	if !strings.Contains(bar, "CONTEXT") {
		t.Fatalf("expected CONTEXT in phase bar, got %q", bar)
	}

	m.approvedGates[artifacts.ContextPackFile] = true
	bar = m.renderPhaseBar()
	if !strings.Contains(bar, "✓ CONTEXT") {
		t.Fatalf("expected approved CONTEXT in phase bar, got %q", bar)
	}
}

func TestDiscoverArtifacts_ContextPackFirst(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{artifacts.ContextPackFile, "requirements.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, idx := discoverArtifacts(dir, artifacts.ContextPackFile)
	if len(got) < 2 || got[0] != artifacts.ContextPackFile {
		t.Fatalf("context pack should be first, got %v", got)
	}
	if idx != 0 {
		t.Fatalf("expected context pack index 0, got %d", idx)
	}
}

func TestNegotiatePrefill(t *testing.T) {
	m := NewNegotiate(nil, "")
	m.Prefill("add a health endpoint")
	if m.input != "add a health endpoint" {
		t.Fatalf("input = %q", m.input)
	}
}

func TestNegotiateRequireInput_BlocksEmptyCtrlA(t *testing.T) {
	var sent string
	m := NewNegotiate(func(msg string) { sent = msg }, "")
	m.SetRequireInput(true)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if sent != "" || m.waiting {
		t.Fatalf("empty Ctrl+A should be ignored, sent=%q waiting=%v", sent, m.waiting)
	}
}

func TestNegotiateRequireInput_SendsPrefilledText(t *testing.T) {
	var sent string
	m := NewNegotiate(func(msg string) { sent = msg }, "")
	m.SetRequireInput(true)
	m.Prefill("do the task")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if sent != "do the task" {
		t.Fatalf("expected prefilled text to be sent, got %q", sent)
	}
}

func TestCtrlO_ReopensPendingGate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, artifacts.ContextPackFile), []byte("# Pack\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New(nil, dir, dir, nil, config.Config{})
	m.width = 120
	m.height = 30
	m.gateArtifact = artifacts.ContextPackFile

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	mm, ok := updated.(Model)
	if !ok {
		t.Fatal("expected Model")
	}
	if mm.overlay != overlayArtifact {
		t.Fatalf("expected artifact overlay, got %v", mm.overlay)
	}
}
