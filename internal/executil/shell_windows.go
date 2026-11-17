//go:build windows

package executil

import (
	"os"
	"os/exec"
)

// shellArgs returns the shell executable and arguments that run command. It
// prefers sh (Git for Windows, MSYS2) so POSIX commands keep working, and
// falls back to cmd.exe.
func shellArgs(command string) (string, []string) {
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh, []string{"-c", command}
	}
	comspec := os.Getenv("COMSPEC")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	return comspec, []string{"/C", command}
}
