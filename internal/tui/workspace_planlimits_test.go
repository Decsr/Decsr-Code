package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// planNow is 14:05 local on a fixed day; the sample resets are relative to it.
var planNow = time.Date(2026, 11, 18, 14, 5, 0, 0, time.Local)

func writePlanLimits(t *testing.T, home string) {
	t.Helper()
	at := planNow.Add(-3 * time.Minute)
	reset5 := planNow.Add(55 * time.Minute)
	reset7 := time.Date(2026, 11, 22, 14, 0, 0, 0, time.Local)
	js := `{"runners":{"claude":{"updated_at":"` + at.Format(time.RFC3339) + `","status":"allowed_warning","windows":{
"five_hour":{"utilization":0.17,"resets_at":"` + reset5.Format(time.RFC3339) + `","status":"allowed","updated_at":"` + at.Format(time.RFC3339) + `"},
"seven_day":{"utilization":0.78,"resets_at":"` + reset7.Format(time.RFC3339) + `","status":"allowed_warning","threshold":0.75,"updated_at":"` + at.Format(time.RFC3339) + `"},
"seven_day_opus":{"resets_at":"` + reset7.Format(time.RFC3339) + `","status":"allowed","updated_at":"` + at.Format(time.RFC3339) + `"},
"seven_day_sonnet":{"utilization":0.4,"resets_at":"` + planNow.Add(-time.Hour).Format(time.RFC3339) + `","status":"allowed","updated_at":"` + at.Format(time.RFC3339) + `"}}}}}`
	dir := filepath.Join(home, ".decsr")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan-limits.json"), []byte(js), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPlanLimitLinesWithData(t *testing.T) {
	for _, mode := range []termenv.Profile{termenv.TrueColor, termenv.Ascii} {
		withColorProfile(t, mode)
		for _, inner := range []int{76, 116} {
			home := t.TempDir()
			t.Setenv("HOME", home)
			writePlanLimits(t, home)
			lines := planLimitLines(inner, planNow)
			text := ansi.Strip(strings.Join(lines, "\n"))
			for _, l := range lines {
				if ansi.StringWidth(l) > inner {
					t.Errorf("inner=%d: %d cells: %q", inner, ansi.StringWidth(l), ansi.Strip(l))
				}
			}
			for _, want := range []string{
				"Plan limits · claude", "as of 14:02", "Current session (5h)", "17% used", "Resets 15:00",
				"Current week (all models)", "78% used  ⚠ over 75%", "Resets Sun 22 Nov 14:00",
				"Current week (Opus)", "under the warning level",
				"Current week (Sonnet)", "reset since, updates on the next claude run",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("inner=%d: missing %q:\n%s", inner, want, text)
				}
			}
			if strings.Index(text, "Current session (5h)") > strings.Index(text, "Current week (all models)") ||
				strings.Index(text, "Current week (all models)") > strings.Index(text, "Current week (Opus)") {
				t.Errorf("order:\n%s", text)
			}
			if inner == 116 {
				t.Log("\n" + text)
			}
		}
	}
}

func TestPlanLimitLinesNothingRecorded(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	t.Setenv("HOME", t.TempDir())
	text := ansi.Strip(strings.Join(planLimitLines(80, planNow), "\n"))
	if !strings.Contains(text, "claude has not reported plan limits yet") || !strings.Contains(text, "decsr never asks the account") {
		t.Errorf("text:\n%s", text)
	}
	if planStatusLine(planNow) != "" {
		t.Error("no status line without data")
	}
}

func TestPlanStatusLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writePlanLimits(t, home)
	if got := planStatusLine(planNow); got != "5h 17% · week 78% (as of 14:02)" {
		t.Errorf("line = %q", got)
	}
}

func TestPlanStaleWhenAWindowReset(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	home := t.TempDir()
	t.Setenv("HOME", home)
	writePlanLimits(t, home)
	if text := ansi.Strip(strings.Join(planLimitLines(100, planNow.Add(2*time.Hour)), "\n")); !strings.Contains(text, "(stale)") {
		t.Errorf("not stale:\n%s", text)
	}
}

func TestUsageSessionAndLimitsTabsShowPlanLimits(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	h := newWSHarness(t)
	home := os.Getenv("HOME")
	for _, inner := range []int{76, 116} {
		if body := ansi.Strip(strings.Join(h.m.usageSessionBody(inner), "\n")); !strings.Contains(body, "claude has not reported plan limits yet") ||
			!strings.Contains(body, "This session") || !strings.Contains(body, "in 0 · out 0 · cache 0 · ") {
			t.Errorf("empty:\n%s", body)
		}
	}
	writePlanLimits(t, home)
	for _, inner := range []int{76, 116} {
		for _, body := range [][]string{h.m.usageSessionBody(inner), h.m.usageLimitsBody(inner)} {
			text := ansi.Strip(strings.Join(body, "\n"))
			if !strings.Contains(text, "Current week (all models)") {
				t.Errorf("inner=%d:\n%s", inner, text)
			}
			for _, l := range body {
				if ansi.StringWidth(l) > inner {
					t.Errorf("inner=%d: line of %d cells: %q", inner, ansi.StringWidth(l), ansi.Strip(l))
				}
			}
		}
	}
}
