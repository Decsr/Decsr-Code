package decsr

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestWaitArtifact_NonInteractiveAutoApproves(t *testing.T) {
	tr := &TaskRunner{
		b:            bus.New(),
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg:          config.Config{},
		gateCh:       make(chan struct{}),
		regenerateCh: make(chan struct{}),
	}
	tr.cfg.Notifications.DisableCMux = true
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ok, err := tr.waitArtifact(ctx, "task-spec.md")
	if err != nil || !ok {
		t.Fatalf("waitArtifact non-interactive = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestNextBead_EmptyTaskBeadIDNeverRunsUnscopedReady(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	script := "#!/bin/sh\necho \"$@\" >> " + marker + "\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tr := &TaskRunner{b: bus.New(), root: t.TempDir()}
	_, _, ok, err := tr.nextBead(context.Background(), map[string]bool{})
	if err != nil || ok {
		t.Fatalf("nextBead = ok %v err %v, want ok=false err=nil", ok, err)
	}
	if b, _ := os.ReadFile(marker); len(b) > 0 {
		t.Fatalf("bd was invoked with empty taskBeadID: %q", b)
	}
}
