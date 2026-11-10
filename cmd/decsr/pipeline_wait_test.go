package main

import (
	"testing"
	"time"
)

func TestWaitForPipeline(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	}()
	if !waitForPipeline(done, time.Second) {
		t.Fatal("expected pipeline exit to be observed")
	}
	if waitForPipeline(make(chan struct{}), 20*time.Millisecond) {
		t.Fatal("expected timeout when pipeline never exits")
	}
}

func TestStopPipeline_CancelsAndWaits(t *testing.T) {
	cancelled := false
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	}()
	ok := stopPipeline(func() { cancelled = true }, done, time.Second)
	if !cancelled {
		t.Fatal("expected cancel to be called")
	}
	if !ok {
		t.Fatal("expected pipeline exit to be observed")
	}
	select {
	case <-done:
	default:
		t.Fatal("stopPipeline returned before the pipeline exited")
	}
}
