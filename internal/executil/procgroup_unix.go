//go:build unix

package executil

import (
	"os/exec"
	"syscall"
)

// killProcessGroup makes cmd start in its own process group and kill the
// whole group on context cancellation, so grandchildren do not outlive it.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
