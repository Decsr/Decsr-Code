//go:build !windows

package executil

// shellArgs returns the shell executable and arguments that run command.
func shellArgs(command string) (string, []string) {
	return "sh", []string{"-c", command}
}
