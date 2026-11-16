//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// execProcess replaces the current process so the terminal belongs to tmux.
func execProcess(path string, args []string) error {
	err := syscall.Exec(path, args, os.Environ()) // #nosec G204 -- path is tmux from LookPath, args are built here
	return fmt.Errorf("exec %s: %w", path, err)
}
