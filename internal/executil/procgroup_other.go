//go:build !unix && !windows

package executil

import "os/exec"

// killProcessGroup is a no-op on platforms without POSIX process groups.
func killProcessGroup(*exec.Cmd) {}
