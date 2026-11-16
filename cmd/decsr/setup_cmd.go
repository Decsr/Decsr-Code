package main

import (
	"fmt"
	"os"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/tui"
)

// firstRunWanted reports whether the first-run setup opens before the
// Workspace: only for an interactive start, never inside an automation, and
// only when no runner is usable (the required "backend" check of decsr
// doctor). usable is called last so the probes are skipped otherwise.
func firstRunWanted(opts tui.WorkspaceOptions, usable func() bool) bool {
	if opts.Automation.Name != "" {
		return false
	}
	return !usable()
}

// runnerUsable is doctor's "backend" check for the effective config of root.
func runnerUsable(root string) bool {
	return doctor.BackendUsable(buildDoctorDeps(root))
}

// firstRunDeps are the real probes of the setup screen; the doctor step runs
// the same checks as `decsr doctor`.
func firstRunDeps(root string) tui.FirstRunDeps {
	return tui.FirstRunDeps{
		Doctor: func() []doctor.Result { return doctor.Run(buildDoctorDeps(root)) },
	}
}

// setupCmd implements `decsr setup`: the first-run screen, any time. When it
// is finished with Enter it opens the Workspace.
func setupCmd(args []string) {
	if len(args) > 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: decsr setup")
		os.Exit(2)
	}
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	err = runSetup(func() (bool, error) {
		cfg, cfgErr := config.Load(root)
		if cfgErr != nil {
			return false, fmt.Errorf("load config: %w", cfgErr)
		}
		tui.ApplyTheme(cfg)
		res, runErr := tui.RunFirstRun(root, cfg, firstRunDeps(root))
		return res.Done, runErr
	}, func() { runCmd(nil) })
	if err != nil {
		fatal(err)
	}
}

// runSetup shows the setup screen and opens the Workspace when it was
// finished rather than skipped.
func runSetup(show func() (done bool, err error), openWorkspace func()) error {
	done, err := show()
	if err != nil {
		return err
	}
	if done {
		openWorkspace()
	}
	return nil
}
