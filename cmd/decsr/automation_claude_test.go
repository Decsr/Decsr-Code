package main

import (
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

func onSubscription(h *autoHarness) {
	h.d.authMode = func() string { return runner.AuthSubscription }
	h.d.claudeRoles = func(string) []string { return []string{"pm"} }
}

func TestInstallWarnsAndAsksOnSubscription(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	onSubscription(h)
	h.d.in = strings.NewReader("n\n")
	if err := automationMain(h.d, []string{"install"}); err == nil || h.ct.writes != 0 {
		t.Fatalf("declined install: err %v, writes %d", err, h.ct.writes)
	}
	out := h.out.String()
	for _, want := range []string{"consumer-terms", "legal-and-compliance", "deps-bump, triage", "[y/N]"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning lacks %q:\n%s", want, out)
		}
	}
	h = newAutoHarness(t, autoYAML)
	onSubscription(h)
	h.d.in = strings.NewReader("y\n")
	if err := automationMain(h.d, []string{"install"}); err != nil || h.ct.writes != 1 {
		t.Fatalf("confirmed install: err %v, writes %d", err, h.ct.writes)
	}
}

func TestInstallYesSkipsQuestion(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	onSubscription(h)
	if err := automationMain(h.d, []string{"install", "--yes"}); err != nil || h.ct.writes != 1 {
		t.Fatalf("err %v, writes %d", err, h.ct.writes)
	}
	if !strings.Contains(h.out.String(), "consumer-terms") || strings.Contains(h.out.String(), "[y/N]") {
		t.Errorf("output:\n%s", h.out.String())
	}
	h = newAutoHarness(t, autoYAML)
	onSubscription(h)
	if err := automationMain(h.d, []string{"install", "deps-bump", "-y"}); err != nil || h.ct.writes != 1 {
		t.Fatalf("named with -y: err %v, writes %d", err, h.ct.writes)
	}
}

func TestInstallRefusesWithoutTerminal(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	onSubscription(h)
	h.d.isTTY = func() bool { return false }
	err := automationMain(h.d, []string{"install"})
	if err == nil || !strings.Contains(err.Error(), "--yes") || h.ct.writes != 0 {
		t.Fatalf("err %v, writes %d", err, h.ct.writes)
	}
}

func TestInstallNoWarningWithAPIKeyOrNoClaude(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	h.d.claudeRoles = func(string) []string { return []string{"pm"} }
	h.d.isTTY = func() bool { return false }
	if err := automationMain(h.d, []string{"install"}); err != nil || strings.Contains(h.out.String(), "warning") {
		t.Fatalf("api key: err %v\n%s", err, h.out.String())
	}
	h = newAutoHarness(t, autoYAML)
	onSubscription(h)
	h.d.claudeRoles = func(string) []string { return nil }
	h.d.isTTY = func() bool { return false }
	if err := automationMain(h.d, []string{"install"}); err != nil || strings.Contains(h.out.String(), "warning") {
		t.Fatalf("no claude role: err %v\n%s", err, h.out.String())
	}
}

const optInYAML = `automations:
  - name: nightly
    schedule: "0 3 * * *"
    task: "x"
    autonomy: 2
    budget_usd: 1
    allow_subscription: true
`

func TestRunRefusedOnSubscriptionWithoutOptIn(t *testing.T) {
	h := newAutoHarness(t, autoYAML)
	onSubscription(h)
	if err := automationMain(h.d, []string{"run", "deps-bump"}); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 0 {
		t.Fatal("the run started")
	}
	log := strings.Join(h.logLines("deps-bump"), "\n")
	if !strings.Contains(log, "refused") || !strings.Contains(log, "ANTHROPIC_API_KEY") || !strings.Contains(log, "allow_subscription") {
		t.Errorf("log: %s", log)
	}
	if len(h.notes) != 1 {
		t.Errorf("notes: %v", h.notes)
	}
}

func TestRunWithOptInOrAPIKeyRuns(t *testing.T) {
	h := newAutoHarness(t, optInYAML)
	onSubscription(h)
	if err := automationMain(h.d, []string{"run", "nightly"}); err != nil || len(h.ran) != 1 {
		t.Fatalf("opt-in: err %v, ran %d", err, len(h.ran))
	}
	h = newAutoHarness(t, autoYAML)
	h.d.claudeRoles = func(string) []string { return []string{"pm"} }
	if err := automationMain(h.d, []string{"run", "deps-bump"}); err != nil || len(h.ran) != 1 {
		t.Fatalf("api key: err %v, ran %d", err, len(h.ran))
	}
}

func TestClaudeAuthDoctor(t *testing.T) {
	sub := func() string { return runner.AuthSubscription }
	key := func() string { return runner.AuthAPIKey }
	h := newAutoHarness(t, autoYAML)
	roles := []string{"pm"}

	if r := claudeAuthDoctor(h.d.root, h.ct, sub, nil); r != nil {
		t.Errorf("no claude role: %+v", r)
	}
	if r := claudeAuthDoctor(h.d.root, h.ct, key, roles); r == nil || r.Status != doctor.OK || r.Detail != runner.AuthAPIKey {
		t.Errorf("api key: %+v", r)
	}
	if r := claudeAuthDoctor(h.d.root, h.ct, sub, roles); r == nil || r.Status != doctor.OK || r.Detail != runner.AuthSubscription {
		t.Errorf("subscription, nothing installed: %+v", r)
	}
	if err := automationMain(h.d, []string{"install"}); err != nil {
		t.Fatal(err)
	}
	r := claudeAuthDoctor(h.d.root, h.ct, sub, roles)
	if r == nil || r.Status != doctor.Warn || !strings.Contains(r.Hint, "ANTHROPIC_API_KEY") || !strings.Contains(r.Detail, "deps-bump") {
		t.Errorf("subscription with installed automations: %+v", r)
	}
	if r := claudeAuthDoctor(h.d.root, h.ct, key, roles); r.Status != doctor.OK {
		t.Errorf("api key with installed automations: %+v", r)
	}
	// opted in: no warning
	h2 := newAutoHarness(t, optInYAML)
	if err := automationMain(h2.d, []string{"install"}); err != nil {
		t.Fatal(err)
	}
	if r := claudeAuthDoctor(h2.d.root, h2.ct, sub, roles); r.Status != doctor.OK {
		t.Errorf("opt-in: %+v", r)
	}
}
