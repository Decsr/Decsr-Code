package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsAreUniqueAndComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Settings() {
		if seen[s.Key] || s.Section == "" || s.Help == "" || !strings.Contains(s.Key, ".") && s.Key != "prompt_language" {
			t.Errorf("bad setting %+v", s.Key)
		}
		seen[s.Key] = true
		if _, ok := FindSetting(strings.ToUpper(s.Key)); !ok {
			t.Errorf("%s is not found case-insensitively", s.Key)
		}
		_ = s.Get(DefaultConfig())
	}
	if _, ok := FindSetting("workspace.autonomy"); ok {
		t.Error("autonomy has its own command and safeguards")
	}
}

func TestSettingApplyValidates(t *testing.T) {
	good := map[string]string{
		"workspace.max_parallel": "5", "workspace.isolation": "shared", "workspace.budget_usd": "$2.5",
		"workspace.budget_warn_pct": "90", "workspace.coordinator": "on", "workspace.limit_auto_retry": "off",
		"routing.mode": "auto-role", "routing.auto_approve": "no", "ui.theme": "light", "ui.icons": "ascii",
		"ui.markdown": "off", "project.language": "Go", "project.test_cmd": "go test ./...", "workspace.pr_base": `""`,
	}
	for k, v := range good {
		s, _ := FindSetting(k)
		var c Config
		if err := s.Apply(&c, v); err != nil {
			t.Errorf("%s=%s: %v", k, v, err)
		}
	}
	bad := map[string]string{
		"workspace.max_parallel": "99", "workspace.isolation": "moon", "workspace.budget_usd": "-1",
		"workspace.budget_warn_pct": "101", "workspace.coordinator": "maybe", "routing.mode": "fast",
		"ui.theme": "pink", "project.language": "cobol2", "project.build_cmd": "a\nb", "workspace.max_ci_fixes": "x",
		"workspace.pr_remote": "",
	}
	for k, v := range bad {
		s, _ := FindSetting(k)
		var c Config
		before := c
		if err := s.Apply(&c, v); err == nil {
			t.Errorf("%s=%q must be refused", k, v)
		}
		if c.Workspace != before.Workspace || c.Routing.Mode != before.Routing.Mode || c.UI.Theme != before.UI.Theme {
			t.Errorf("%s: a refused value changed the config", k)
		}
	}
}

func TestSettingGet(t *testing.T) {
	c := DefaultConfig()
	for k, want := range map[string]string{
		"workspace.isolation": "auto", "routing.mode": "manual", "ui.theme": "auto", "ui.markdown": "on",
		"workspace.coordinator": "off", "workspace.limit_auto_retry": "on", "workspace.max_parallel": "",
		"project.max_fix_attempts": "3", "prompt_language": "English",
	} {
		s, _ := FindSetting(k)
		if got := s.Get(c); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	s, _ := FindSetting("workspace.isolation")
	if err := s.Apply(&c, "auto"); err != nil || c.Workspace.Isolation != "" {
		t.Errorf("auto isolation is stored as empty: %q %v", c.Workspace.Isolation, err)
	}
}

func TestSaveProjectSetting(t *testing.T) {
	root := t.TempDir()
	if err := SaveProjectSetting(root, "workspace.max_parallel", "4"); err != nil {
		t.Fatal(err)
	}
	if err := SaveProjectSetting(root, "routing.mode", "auto-role"); err != nil {
		t.Fatal(err)
	}
	c := LoadProject(root)
	if c.Workspace.MaxParallel != 4 || c.Routing.Mode != "auto-role" {
		t.Errorf("saved config: %+v", c)
	}
	if err := SaveProjectSetting(root, "workspace.max_parallel", "x"); err == nil {
		t.Error("an invalid value must not be saved")
	}
	if err := SaveProjectSetting(root, "nope.key", "1"); err == nil {
		t.Error("an unknown key must be refused")
	}
	if LoadProject(root).Workspace.MaxParallel != 4 {
		t.Error("a refused value changed the file")
	}
	bad := t.TempDir()
	_ = os.MkdirAll(filepath.Join(bad, GlobalDir), 0o750)
	_ = os.WriteFile(filepath.Join(bad, GlobalDir, ProjectFilename), []byte("workspace: [1"), 0o600)
	if err := SaveProjectSetting(bad, "ui.theme", "dark"); err == nil {
		t.Error("an unreadable project.yaml must stop the save")
	}
}
