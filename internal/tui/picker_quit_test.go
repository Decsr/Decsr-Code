package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestPickerQ_GoesBackInsteadOfQuitting(t *testing.T) {
	root := t.TempDir()
	p := NewPicker(root, root)
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("expected a command from q")
	}
	if _, ok := cmd().(PickerCancelledMsg); !ok {
		t.Fatalf("q must emit PickerCancelledMsg, got %T", cmd())
	}
}

func TestOverlayPicker_CancelClosesOverlay(t *testing.T) {
	root := t.TempDir()
	m := New(nil, root, root, nil, config.Config{})
	m.overlayPicker = NewPicker(root, root)
	m.overlay = overlayPicker

	updated, _ := m.updateOverlay(PickerCancelledMsg{})
	got := updated.(Model)
	if got.overlay != overlayNone {
		t.Fatalf("expected overlay closed, got %v", got.overlay)
	}
}
