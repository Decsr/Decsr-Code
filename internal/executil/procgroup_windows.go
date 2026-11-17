//go:build windows

package executil

import (
	"os/exec"
	"strconv"
)

// killProcessGroup kills the whole process tree on context cancellation, so
// grandchildren do not outlive the command.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		pid := strconv.Itoa(cmd.Process.Pid)
		if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
