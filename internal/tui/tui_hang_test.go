package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestChat_TokenMsgErrorFinalisesTurn(t *testing.T) {
	m := NewChat(nil, "sys")
	m.waiting = true
	got, _ := m.Update(ChatTokenMsg{Text: "error: boom", Done: true})
	if got.waiting {
		t.Fatal("expected waiting to be cleared")
	}
	if !strings.Contains(got.vp.View(), "boom") {
		t.Errorf("expected error in viewport:\n%s", got.vp.View())
	}
	if n := len(got.history); n == 0 || !strings.Contains(got.history[n-1].Content, "boom") {
		t.Errorf("expected error committed to history, got %+v", got.history)
	}
}

func TestModel_PipelineDoneHandledWithOverlayOpen(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.decsr", nil, config.Config{})
	m.overlay = overlayNegotiate
	updated, _ := m.Update(PipelineDoneMsg{Err: errors.New("x")})
	got := updated.(Model)
	if !got.pipelineDone || !got.pipelineFailed {
		t.Fatalf("pipelineDone=%v pipelineFailed=%v", got.pipelineDone, got.pipelineFailed)
	}
	if got.overlay != overlayNone {
		t.Errorf("expected negotiate overlay closed so error is visible, got %v", got.overlay)
	}
}

func TestModel_StatusBarTickHandledWithOverlayOpen(t *testing.T) {
	m := New(nil, "/tmp/project", "/tmp/project/.decsr", nil, config.Config{})
	m.overlay = overlayNegotiate
	m.statusbar.stageInfo = "stage one of many"
	_, cmd := m.Update(statusBarTickMsg{})
	if cmd == nil {
		t.Fatal("expected tick to re-arm while overlay open")
	}
}
