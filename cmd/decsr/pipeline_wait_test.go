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
