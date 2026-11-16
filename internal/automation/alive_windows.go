//go:build windows

package automation

import "os"

// ProcessAlive reports whether a process with this pid exists.
func ProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
