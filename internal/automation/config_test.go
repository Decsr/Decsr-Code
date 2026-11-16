package automation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goodYAML = `automations:
  - name: deps-bump
    schedule: "0 3 * * 1-5"
    task: "Update Go dependencies"
    autonomy: 3
    budget_usd: 2.5
    timeout: 90m
  - name: triage
    schedule: "*/15 * * * *"
    issue_query: "label:decsr is:open"
    autonomy: 2
    budget_usd: 1
`

func TestParseGood(t *testing.T) {
	list, err := Parse([]byte(goodYAML))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d automations", len(list))
	}
	a := list[0]
	if a.Name != "deps-bump" || a.Autonomy != 3 || a.BudgetUSD != 2.5 || a.Timeout != 90*time.Minute || a.Line != 2 {
		t.Errorf("unexpected %+v", a)
	}
	if list[1].Timeout != DefaultTimeout {
		t.Errorf("default timeout = %s", list[1].Timeout)
	}
	if list[1].IssueQuery == "" || list[1].Task != "" {
		t.Errorf("issue_query not read: %+v", list[1])
	}
	if got := a.Source(); !strings.HasPrefix(got, "task: ") {
		t.Errorf("source %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	item := func(body string) string { return "automations:\n  - " + body }
	ok := `name: x
    schedule: "0 3 * * *"
    task: t
    autonomy: 2
    budget_usd: 1`
	cases := []struct{ name, yaml, want string }{
		{"not yaml", "automations: [", "automations.yaml"},
		{"no key", "other: 1\n", `missing the top-level "automations" list`},
		{"unknown top key", "automations: []\nfoo: 1\n", `unknown top-level key "foo"`},
		{"not a list", "automations: 3\n", "must be a list"},
		{"empty", "automations: []\n", "is empty"},
		{"item not mapping", "automations:\n  - nope\n", "line 2: each automation must be a mapping"},
		{"unknown field", item(ok + "\n    colour: red\n"), `line 7: unknown field "colour"`},
		{"missing name", item(strings.Replace(ok, "name: x\n    ", "", 1)), "name is required"},
		{"bad name", item(strings.Replace(ok, "name: x", "name: Bad_Name", 1)), `automation "Bad_Name": name may only use`},
		{"dup name", "automations:\n  - " + ok + "\n  - " + ok + "\n", `automation "x": name is already used on line 2`},
		{"missing schedule", item(strings.Replace(ok, `schedule: "0 3 * * *"`, "", 1)), "schedule is required"},
		{"bad schedule", item(strings.Replace(ok, "0 3 * * *", "0 3 * *", 1)), `automation "x": schedule "0 3 * *": want 5 fields`},
		{"no work", item(strings.Replace(ok, "task: t", "", 1)), "set exactly one of task or issue_query"},
		{"both work", item(ok + "\n    issue_query: foo\n"), "set only one of task or issue_query"},
		{"missing autonomy", item(strings.Replace(ok, "autonomy: 2", "", 1)), "autonomy is required and must be 2 or 3"},
		{"autonomy 1", item(strings.Replace(ok, "autonomy: 2", "autonomy: 1", 1)), "autonomy must be 2 or 3"},
		{"autonomy text", item(strings.Replace(ok, "autonomy: 2", "autonomy: high", 1)), "autonomy must be 2 or 3"},
		{"missing budget", item(strings.Replace(ok, "budget_usd: 1", "", 1)), "budget_usd is required"},
		{"zero budget", item(strings.Replace(ok, "budget_usd: 1", "budget_usd: 0", 1)), "budget_usd must be a number greater than 0"},
		{"bad timeout", item(ok + "\n    timeout: soon\n"), `timeout "soon" is not a positive duration`},
		{"negative timeout", item(ok + "\n    timeout: -1h\n"), "is not a positive duration"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseReportsEveryError(t *testing.T) {
	_, err := Parse([]byte("automations:\n  - name: a\n    schedule: bad\n    autonomy: 1\n"))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"schedule", "set exactly one", "autonomy must be 2 or 3", "budget_usd is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); err != ErrNoConfig {
		t.Fatalf("err = %v, want ErrNoConfig", err)
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath(root)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(root), []byte(goodYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := Load(root)
	if err != nil || len(list) != 2 {
		t.Fatalf("Load = %v, %v", list, err)
	}
	if _, ok := Find(list, "triage"); !ok {
		t.Error("Find missed triage")
	}
	if _, ok := Find(list, "nope"); ok {
		t.Error("Find found nope")
	}
}
