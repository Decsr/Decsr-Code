package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/tui"
)

func TestFirstRunWanted(t *testing.T) {
	none := func() bool { return false }
	one := func() bool { return true }
	auto := tui.WorkspaceOptions{Automation: tui.AutomationOptions{Name: "nightly"}}
	probed := false
	spy := func() bool { probed = true; return false }

	if !firstRunWanted(tui.WorkspaceOptions{}, none) {
		t.Error("no usable runner: the setup should open")
	}
	if firstRunWanted(tui.WorkspaceOptions{}, one) {
		t.Error("a usable runner: the setup must not open")
	}
	if firstRunWanted(auto, none) {
		t.Error("an automation must never open the setup")
	}
	if firstRunWanted(auto, spy); probed {
		t.Error("an automation must not even probe the runners")
	}
}

// TestRunnerUsableFollowsDoctorBackend runs the real rule against a temp HOME
// and a PATH that holds only what the test puts there.
func TestRunnerUsableFollowsDoctorBackend(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	root := t.TempDir()
	if runnerUsable(root) {
		t.Fatal("empty PATH: no runner should be usable")
	}
	script := "#!/bin/sh\necho 1.0\n"
	if err := os.WriteFile(filepath.Join(bin, "opencode"), []byte(script), 0o700); err != nil { // #nosec G306 -- test stub
		t.Fatal(err)
	}
	if !runnerUsable(root) {
		t.Fatal("opencode on PATH: a runner should be usable")
	}
}

func TestRunSetup(t *testing.T) {
	opened := 0
	open := func() { opened++ }

	if err := runSetup(func() (bool, error) { return true, nil }, open); err != nil || opened != 1 {
		t.Fatalf("finished: err = %v, opened = %d, want the Workspace once", err, opened)
	}
	if err := runSetup(func() (bool, error) { return false, nil }, open); err != nil || opened != 1 {
		t.Fatalf("skipped: err = %v, opened = %d, want no Workspace", err, opened)
	}
	boom := errors.New("boom")
	if err := runSetup(func() (bool, error) { return false, boom }, open); !errors.Is(err, boom) || opened != 1 {
		t.Fatalf("error: err = %v, opened = %d", err, opened)
	}
}
