package runner

import (
	"os"
	"testing"
)

// TestMain points the home directory at an empty temp dir so no test reads or
// writes the real ~/.decsr (credentials, config).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "decsr-runner-home")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
