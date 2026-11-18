package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Setting is one setting that /config can show and change, named by a dotted
// key such as workspace.max_parallel.
type Setting struct {
	Key     string
	Section string   // the heading the setting is listed under
	Help    string   // one line
	Values  []string // the accepted words of an enumeration; nil for numbers, text and switches
	kind    settingKind
	get     func(Config) string
	set     func(*Config, string) error
}

type settingKind int

const (
	kindText settingKind = iota
	kindInt
	kindFloat
	kindBool
	kindEnum
)

// Hint is what the setting accepts, for usage lines: "on|off", "0-16", "text".
func (s Setting) Hint() string {
	switch s.kind {
	case kindBool:
		return "on|off"
	case kindEnum:
		return strings.Join(s.Values, "|")
	case kindInt:
		return "number"
	case kindFloat:
		return "amount"
	}
	return "text"
}

// Get is the current value of the setting in cfg as text; "" when it is not set.
func (s Setting) Get(cfg Config) string { return s.get(cfg) }

// Apply validates value and stores it in cfg. cfg is not changed on an error.
func (s Setting) Apply(cfg *Config, value string) error {
	value = strings.TrimSpace(value)
	if value == `""` {
		value = ""
	} else if value == "" {
		return fmt.Errorf("%s needs a value (%s)", s.Key, s.Hint())
	}
	return s.set(cfg, value)
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "on", "true", "yes", "1":
		return true, nil
	case "off", "false", "no", "0":
		return false, nil
	}
	return false, fmt.Errorf("%q is not on or off", v)
}

func intSetting(key, section, help string, lo, hi int, field func(*Config) *int) Setting {
	return Setting{Key: key, Section: section, Help: help, kind: kindInt,
		get: func(c Config) string {
			if v := *field(&c); v != 0 {
				return strconv.Itoa(v)
			}
			return ""
		},
		set: func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < lo || n > hi {
				return fmt.Errorf("%s is a whole number from %d to %d, not %q", key, lo, hi, v)
			}
			*field(c) = n
			return nil
		}}
}

func textSetting(key, section, help string, field func(*Config) *string, check func(string) error) Setting {
	return Setting{Key: key, Section: section, Help: help, kind: kindText,
		get: func(c Config) string { return *field(&c) },
		set: func(c *Config, v string) error {
			if strings.ContainsAny(v, "\r\n\x00") {
				return fmt.Errorf("%s must be one line of text", key)
			}
			if check != nil {
				if err := check(v); err != nil {
					return err
				}
			}
			*field(c) = v
			return nil
		}}
}

func enumSetting(key, section, help string, values []string, field func(*Config) *string) Setting {
	return Setting{Key: key, Section: section, Help: help, Values: values, kind: kindEnum,
		get: func(c Config) string {
			if v := *field(&c); v != "" {
				return v
			}
			return values[0] // the default is the first word
		},
		set: func(c *Config, v string) error {
			v = strings.ToLower(v)
			for _, ok := range values {
				if v == ok {
					if v == "auto" && key == "workspace.isolation" {
						v = ""
					}
					*field(c) = v
					return nil
				}
			}
			return fmt.Errorf("%s is %s, not %q", key, strings.Join(values, ", "), v)
		}}
}

func boolSetting(key, section, help string, field func(*Config) *bool) Setting {
	return Setting{Key: key, Section: section, Help: help, kind: kindBool,
		get: func(c Config) string { return onOff(*field(&c)) },
		set: func(c *Config, v string) error {
			b, err := parseBool(v)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*field(c) = b
			return nil
		}}
}

func ptrBoolSetting(key, section, help string, def bool, field func(*Config) **bool) Setting {
	return Setting{Key: key, Section: section, Help: help, kind: kindBool,
		get: func(c Config) string {
			if p := *field(&c); p != nil {
				return onOff(*p)
			}
			return onOff(def)
		},
		set: func(c *Config, v string) error {
			b, err := parseBool(v)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*field(c) = &b
			return nil
		}}
}

// settings is the list /config offers. It leaves out what has its own command
// with its own safeguards (/autonomy) and what holds secrets.
var settings = []Setting{
	intSetting("workspace.max_parallel", "Workspace", "nodes that run at once (0 = 3)", 0, 16, func(c *Config) *int { return &c.Workspace.MaxParallel }),
	enumSetting("workspace.isolation", "Workspace", "where nodes work", []string{"auto", "worktree", "shared"}, func(c *Config) *string { return &c.Workspace.Isolation }),
	{Key: "workspace.budget_usd", Section: "Workspace", Help: "stop starting nodes at this cost, 0 = none", kind: kindFloat,
		get: func(c Config) string {
			if c.Workspace.BudgetUSD > 0 {
				return strconv.FormatFloat(c.Workspace.BudgetUSD, 'f', -1, 64)
			}
			return ""
		},
		set: func(c *Config, v string) error {
			f, err := strconv.ParseFloat(strings.TrimPrefix(v, "$"), 64)
			if err != nil || f < 0 || f > 1e6 {
				return fmt.Errorf("workspace.budget_usd is an amount in dollars such as 5 or 0.5, not %q", v)
			}
			c.Workspace.BudgetUSD = f
			return nil
		}},
	intSetting("workspace.budget_warn_pct", "Workspace", "share of the budget that raises a warning (0 = 80)", 0, 100, func(c *Config) *int { return &c.Workspace.BudgetWarnPct }),
	boolSetting("workspace.coordinator", "Workspace", "the lead reviews each finished batch", func(c *Config) *bool { return &c.Workspace.Coordinator }),
	ptrBoolSetting("workspace.limit_auto_retry", "Workspace", "retry a node by itself after a usage limit", true, func(c *Config) **bool { return &c.Workspace.LimitAutoRetry }),
	textSetting("workspace.pr_base", "Workspace", "base branch of /pr (empty = the checked-out branch)", func(c *Config) *string { return &c.Workspace.PRBase }, nil),
	textSetting("workspace.pr_remote", "Workspace", "remote of /pr (empty = origin)", func(c *Config) *string { return &c.Workspace.PRRemote }, nil),
	intSetting("workspace.max_ci_fixes", "Workspace", "fix rounds of /pr watch (0 = 3)", 0, 20, func(c *Config) *int { return &c.Workspace.MaxCIFixes }),
	enumSetting("routing.mode", "Routing", "how a split picks agents", []string{RoutingManual, RoutingAutoRole, RoutingAutoConnected}, func(c *Config) *string { return &c.Routing.Mode }),
	ptrBoolSetting("routing.auto_approve", "Routing", "unattended runs approve the picks themselves", true, func(c *Config) **bool { return &c.Routing.AutoApprove }),
	enumSetting("ui.theme", "Interface", "colour theme", []string{"auto", "dark", "light"}, func(c *Config) *string { return &c.UI.Theme }),
	enumSetting("ui.icons", "Interface", "glyph set of the runners", []string{"unicode", "ascii", "nerd"}, func(c *Config) *string { return &c.UI.Icons }),
	{Key: "ui.markdown", Section: "Interface", Help: "render messages as Markdown", kind: kindBool,
		get: func(c Config) string { return onOff(c.UI.MarkdownOn()) },
		set: func(c *Config, v string) error {
			b, err := parseBool(v)
			if err != nil {
				return fmt.Errorf("ui.markdown: %w", err)
			}
			c.UI.Markdown = &b
			return nil
		}},
	boolSetting("ui.tmux", "Interface", "start inside a tmux session", func(c *Config) *bool { return &c.UI.Tmux }),
	{Key: "project.language", Section: "Project", Help: "programming language of the project", kind: kindEnum, Values: SupportedProgrammingLanguages,
		get: func(c Config) string { return c.Project.Language },
		set: func(c *Config, v string) error {
			v = strings.ToLower(v)
			for _, l := range SupportedProgrammingLanguages {
				if l == v {
					c.Project.Language = v
					return nil
				}
			}
			return fmt.Errorf("project.language is one of %s, not %q", strings.Join(SupportedProgrammingLanguages, ", "), v)
		}},
	textSetting("project.build_cmd", "Project", "build command of Build & test", func(c *Config) *string { return &c.Project.BuildCmd }, nil),
	textSetting("project.test_cmd", "Project", "test command of Build & test", func(c *Config) *string { return &c.Project.TestCmd }, nil),
	textSetting("project.lint_cmd", "Project", "lint command", func(c *Config) *string { return &c.Project.LintCmd }, nil),
	intSetting("project.max_fix_attempts", "Project", "fix attempts after a failed check", 0, 10, func(c *Config) *int { return &c.Project.MaxFixAttempts }),
	textSetting("prompt_language", "Project", "language agents answer in", func(c *Config) *string { return &c.PromptLanguage }, nil),
	boolSetting("notifications.disable_bell", "Notifications", "never ring the terminal bell", func(c *Config) *bool { return &c.Notifications.DisableBell }),
	boolSetting("notifications.disable_desktop", "Notifications", "never send desktop notifications", func(c *Config) *bool { return &c.Notifications.DisableDesktop }),
}

// Settings lists the settings /config offers, grouped by section in the
// order of their first appearance.
func Settings() []Setting { return append([]Setting(nil), settings...) }

// FindSetting returns the setting with the given dotted key.
func FindSetting(key string) (Setting, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, s := range settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// SaveProjectSetting sets one key in <root>/.decsr/project.yaml, keeping the
// rest of the file. It validates value first and fails without touching the
// file when project.yaml exists but cannot be parsed.
func SaveProjectSetting(root, key, value string) error {
	s, ok := FindSetting(key)
	if !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	cfg, err := loadFile(filepath.Join(root, GlobalDir), ProjectFilename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", ProjectFilename, err)
	}
	if err := s.Apply(&cfg, value); err != nil {
		return err
	}
	return Save(root, cfg)
}
