// Package automation holds the pieces of unattended, scheduled Workspace runs:
// the .decsr/automations.yaml config, a small cron parser, the run lock, the
// run log and the crontab lines. Nothing here starts a model.
package automation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// DirName is the directory under .decsr that holds locks and logs.
	DirName = "automations"
	// FileName is the config file under .decsr.
	FileName = "automations.yaml"
	// DefaultTimeout is how long a run may take when timeout is not set.
	DefaultTimeout = 2 * time.Hour
)

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Automation is one entry of automations.yaml.
type Automation struct {
	Name       string
	Schedule   string
	Task       string
	IssueQuery string
	Autonomy   int
	BudgetUSD  float64
	Timeout    time.Duration
	// AllowSubscription accepts an unattended run of the claude runner on a
	// subscription login.
	AllowSubscription bool
	Line              int // line of the entry in the file

	cron Cron
}

// Cron is the parsed schedule.
func (a Automation) Cron() Cron { return a.cron }

// Source describes what the automation works on, for listings.
func (a Automation) Source() string {
	if a.Task != "" {
		return "task: " + a.Task
	}
	return "issue: " + a.IssueQuery
}

// ConfigPath is the config file of a project.
func ConfigPath(root string) string { return filepath.Join(root, ".decsr", FileName) }

// Dir is the directory of locks and logs of a project.
func Dir(root string) string { return filepath.Join(root, ".decsr", DirName) }

// LogPath is the run log of one automation.
func LogPath(root, name string) string { return filepath.Join(Dir(root), name+".log") }

// LockPath is the lock file of one automation.
func LockPath(root, name string) string { return filepath.Join(Dir(root), name+".lock") }

// ErrNoConfig is returned by Load when the project has no automations.yaml.
var ErrNoConfig = errors.New("no .decsr/automations.yaml in this project")

// Load reads and validates the automations of a project.
func Load(root string) ([]Automation, error) {
	data, err := os.ReadFile(ConfigPath(root)) // #nosec G304 -- fixed path under the project root
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoConfig
	}
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Find returns the automation called name.
func Find(list []Automation, name string) (Automation, bool) {
	for _, a := range list {
		if a.Name == name {
			return a, true
		}
	}
	return Automation{}, false
}

var knownFields = map[string]bool{
	"name": true, "schedule": true, "task": true, "issue_query": true,
	"autonomy": true, "budget_usd": true, "timeout": true, "allow_subscription": true,
}

// Parse validates the contents of automations.yaml. Every problem is reported
// with its line; the error lists them all.
func Parse(data []byte) ([]Automation, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: line 1: expected a mapping with a top-level \"automations\" list", FileName)
	}
	top := doc.Content[0]
	var list *yaml.Node
	var errs []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], top.Content[i+1]
		if k.Value == "automations" {
			list = v
			continue
		}
		errs = append(errs, fmt.Sprintf("line %d: unknown top-level key %q (only \"automations\" is allowed)", k.Line, k.Value))
	}
	if list == nil {
		return nil, fmt.Errorf("%s: missing the top-level \"automations\" list", FileName)
	}
	if list.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s: line %d: \"automations\" must be a list", FileName, list.Line)
	}
	seen := map[string]int{}
	var out []Automation
	for _, item := range list.Content {
		a, itemErrs := parseItem(item, seen)
		errs = append(errs, itemErrs...)
		out = append(out, a)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s:\n  %s", FileName, strings.Join(errs, "\n  "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: the \"automations\" list is empty", FileName)
	}
	return out, nil
}

func parseItem(item *yaml.Node, seen map[string]int) (Automation, []string) {
	a := Automation{Line: item.Line, Timeout: DefaultTimeout}
	if item.Kind != yaml.MappingNode {
		return a, []string{fmt.Sprintf("line %d: each automation must be a mapping of fields", item.Line)}
	}
	var errs []string
	fields := map[string]*yaml.Node{}
	for i := 0; i+1 < len(item.Content); i += 2 {
		k, v := item.Content[i], item.Content[i+1]
		if !knownFields[k.Value] {
			errs = append(errs, fmt.Sprintf("line %d: unknown field %q", k.Line, k.Value))
			continue
		}
		if _, dup := fields[k.Value]; dup {
			errs = append(errs, fmt.Sprintf("line %d: field %q is set twice", k.Line, k.Value))
		}
		fields[k.Value] = v
	}
	str := func(key string) string {
		n := fields[key]
		if n == nil {
			return ""
		}
		var s string
		if err := n.Decode(&s); err != nil || n.Kind != yaml.ScalarNode {
			errs = append(errs, fmt.Sprintf("line %d: %s must be text", n.Line, key))
			return ""
		}
		return strings.TrimSpace(s)
	}
	a.Name = str("name")
	label := fmt.Sprintf("automation %q", a.Name)
	at := func(key string, def int) int {
		if n := fields[key]; n != nil {
			return n.Line
		}
		return def
	}
	switch {
	case a.Name == "":
		errs = append(errs, fmt.Sprintf("line %d: name is required", item.Line))
		label = "automation"
	case !nameRe.MatchString(a.Name):
		errs = append(errs, fmt.Sprintf("line %d: %s: name may only use a-z, 0-9 and single dashes", at("name", item.Line), label))
	default:
		if first, dup := seen[a.Name]; dup {
			errs = append(errs, fmt.Sprintf("line %d: %s: name is already used on line %d", at("name", item.Line), label, first))
		} else {
			seen[a.Name] = at("name", item.Line)
		}
	}

	a.Schedule = str("schedule")
	if a.Schedule == "" {
		errs = append(errs, fmt.Sprintf("line %d: %s: schedule is required (5-field cron, e.g. \"0 3 * * 1-5\")", item.Line, label))
	} else if c, err := ParseCron(a.Schedule); err != nil {
		errs = append(errs, fmt.Sprintf("line %d: %s: schedule %q: %v", at("schedule", item.Line), label, a.Schedule, err))
	} else {
		a.cron = c
	}

	a.Task, a.IssueQuery = str("task"), str("issue_query")
	switch {
	case a.Task == "" && a.IssueQuery == "":
		errs = append(errs, fmt.Sprintf("line %d: %s: set exactly one of task or issue_query", item.Line, label))
	case a.Task != "" && a.IssueQuery != "":
		errs = append(errs, fmt.Sprintf("line %d: %s: set only one of task or issue_query, not both", at("issue_query", item.Line), label))
	}

	if n := fields["autonomy"]; n == nil {
		errs = append(errs, fmt.Sprintf("line %d: %s: autonomy is required and must be 2 or 3", item.Line, label))
	} else if err := n.Decode(&a.Autonomy); err != nil || (a.Autonomy != 2 && a.Autonomy != 3) {
		errs = append(errs, fmt.Sprintf("line %d: %s: autonomy must be 2 or 3 (an unattended run needs L2 or L3)", n.Line, label))
	}

	if n := fields["budget_usd"]; n == nil {
		errs = append(errs, fmt.Sprintf("line %d: %s: budget_usd is required and must be greater than 0", item.Line, label))
	} else if err := n.Decode(&a.BudgetUSD); err != nil || a.BudgetUSD <= 0 {
		errs = append(errs, fmt.Sprintf("line %d: %s: budget_usd must be a number greater than 0", n.Line, label))
	}

	if n := fields["allow_subscription"]; n != nil {
		if err := n.Decode(&a.AllowSubscription); err != nil || n.Kind != yaml.ScalarNode {
			errs = append(errs, fmt.Sprintf("line %d: %s: allow_subscription must be true or false", n.Line, label))
		}
	}

	if n := fields["timeout"]; n != nil {
		d, err := time.ParseDuration(strings.TrimSpace(n.Value))
		if err != nil || d <= 0 || n.Kind != yaml.ScalarNode {
			errs = append(errs, fmt.Sprintf("line %d: %s: timeout %q is not a positive duration such as 90m or 2h", n.Line, label, n.Value))
		} else {
			a.Timeout = d
		}
	}
	return a, errs
}
