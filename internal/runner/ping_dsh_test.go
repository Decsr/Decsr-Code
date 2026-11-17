package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pingFakeDsh puts an executable dsh on PATH.
func pingFakeDsh(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dsh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestPingDsh_NoKey(t *testing.T) {
	pingFakeDsh(t)
	t.Setenv("DEEPSEEK_API_KEY", "")
	for name, fn := range map[string]func() error{
		"ProviderReachable": func() error { return ProviderReachable("dsh") },
		"Ping":              func() error { return Ping("dsh", "") },
	} {
		err := fn()
		if !errors.Is(err, ErrDshNoKey) {
			t.Fatalf("%s: err = %v, want ErrDshNoKey", name, err)
		}
		if !strings.Contains(err.Error(), "decsr connect deepseek") || !strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
			t.Errorf("%s: hint missing: %v", name, err)
		}
	}
}

func TestPingDsh_WithKey(t *testing.T) {
	pingFakeDsh(t)
	t.Setenv("DEEPSEEK_API_KEY", "test-value")
	if err := ProviderReachable("dsh"); err != nil {
		t.Fatalf("ProviderReachable: %v", err)
	}
	if err := Ping("dsh", ""); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPingDsh_NoBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DEEPSEEK_API_KEY", "test-value")
	err := ProviderReachable("dsh")
	if err == nil || errors.Is(err, ErrDshNoKey) {
		t.Fatalf("err = %v, want a not-found error", err)
	}
}
