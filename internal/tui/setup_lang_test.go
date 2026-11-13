package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func langSetupRoot(t *testing.T, language string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".decsr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if language != "" {
		cfg := config.LoadProject(root)
		cfg.Project.Language = language
		if err := config.Save(root, cfg); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestProjectSetup_EmptyLanguagePreselectsAutoAndStaysEmpty(t *testing.T) {
	root := langSetupRoot(t, "")
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "", "proj", nil)
	m.root = root
	if m.progLanguages[m.progLangIdx] != "auto" {
		t.Fatalf("preselected %q, want auto", m.progLanguages[m.progLangIdx])
	}
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadProject(root).Project.Language; got != "" {
		t.Fatalf("language = %q, want empty", got)
	}
}

func TestProjectSetup_ConfiguredLanguageKept(t *testing.T) {
	root := langSetupRoot(t, "java")
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "java", "proj", nil)
	m.root = root
	if m.progLanguages[m.progLangIdx] != "java" {
		t.Fatalf("preselected %q, want java", m.progLanguages[m.progLangIdx])
	}
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadProject(root).Project.Language; got != "java" {
		t.Fatalf("language = %q, want java", got)
	}
}

func TestProjectSetup_SwitchToAutoClears(t *testing.T) {
	root := langSetupRoot(t, "java")
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "java", "proj", nil)
	m.root = root
	m.progLangIdx = 0
	if err := m.savePayload(m.currentPayload()); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadProject(root).Project.Language; got != "" {
		t.Fatalf("language = %q, want cleared", got)
	}
}

func TestProjectSetup_AutoRowShowsDetected(t *testing.T) {
	root := langSetupRoot(t, "")
	m := NewSetupModelWithOverrides("claude", "sonnet", "default", "", "proj", nil)
	m.root = root
	if got := m.progLangLabels()[0]; got != "auto (not detected)" {
		t.Fatalf("label = %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "CMakeLists.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := m.progLangLabels()[0]; !strings.HasPrefix(got, "auto (cpp)") {
		t.Fatalf("label = %q", got)
	}
}
