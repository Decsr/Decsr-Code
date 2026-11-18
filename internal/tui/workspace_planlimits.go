package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// Plan limits are read from the file the claude runner keeps from what the
// CLI printed (runner.LoadPlanLimits). decsr asks no account endpoint and does
// not act on these numbers: they are shown, nothing more.

const planRunner = "claude"

// planWindowLabel is the name of a window as the claude /usage screen words it.
func planWindowLabel(key string) string {
	switch key {
	case "five_hour":
		return "Current session (5h)"
	case "seven_day":
		return "Current week (all models)"
	case "seven_day_opus":
		return "Current week (Opus)"
	case "seven_day_sonnet":
		return "Current week (Sonnet)"
	}
	s := strings.ReplaceAll(key, "_", " ")
	if s == "" {
		return key
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// planTime is t in local time: the time of day when it is today, else
// "Mon 2 Jan 15:04".
func planTime(t, now time.Time) string {
	t, now = t.In(now.Location()), now.In(now.Location())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}

// planAsOf is "as of 14:02" today, "as of Mon 15:04" for an older snapshot.
func planAsOf(t, now time.Time) string {
	t, now = t.In(now.Location()), now.In(now.Location())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "as of " + t.Format("15:04")
	}
	return "as of " + t.Format("Mon 15:04")
}

// planStale says whether a window of s has reset since the snapshot was taken.
func planStale(s runner.PlanSnapshot, now time.Time) bool {
	for _, w := range s.Windows {
		if !w.ResetsAt.IsZero() && w.ResetsAt.Before(now) {
			return true
		}
	}
	return false
}

// planBar is a meter of width cells for a used fraction, with half cells.
func planBar(frac float64, width int) string {
	f := frac
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	halves := int(f*float64(width)*2 + 0.5)
	full, half := halves/2, halves%2
	bar := lipgloss.NewStyle().Foreground(ui.MeterColor(f)).Render(strings.Repeat("█", full) + strings.Repeat("▌", half))
	return bar + lipgloss.NewStyle().Foreground(ui.Border).Render(strings.Repeat("░", max(width-full-half, 0)))
}

// planPct is the used share as "17% used".
func planPct(u float64) string { return fmt.Sprintf("%d%% used", int(u*100+0.5)) }

// planLimitLines is the "Plan limits" block of the Session and Limits tabs,
// inner cells wide.
func planLimitLines(inner int, now time.Time) []string {
	dim := func(s string) string { return dimText(s) }
	s, ok := runner.LoadPlanLimits(planRunner)
	head := plainText("Plan limits · " + planRunner)
	if !ok {
		return []string{head, "", dim("claude has not reported plan limits yet. They update whenever the lead or a claude node runs; decsr never asks the account for them.")}
	}
	asOf := planAsOf(s.UpdatedAt, now)
	if planStale(s, now) {
		asOf += " (stale)"
	}
	right := asOf + " · from the last claude run"
	if inner < ansi.StringWidth("Plan limits · "+planRunner)+ansi.StringWidth(right)+2 {
		right = asOf
	}
	lines := []string{head + strings.Repeat(" ", max(inner-ansi.StringWidth("Plan limits · "+planRunner)-ansi.StringWidth(right), 1)) + dim(right)}

	// The right column is as wide as the longest "NN% used" with its warning.
	names := s.Names()
	rightW := 0
	note := func(w runner.PlanWindow) string {
		if w.Utilization != nil && w.Threshold > 0 && *w.Utilization >= w.Threshold {
			return fmt.Sprintf("  ⚠ over %d%%", int(w.Threshold*100+0.5))
		}
		return ""
	}
	for _, k := range names {
		w := s.Windows[k]
		if w.Utilization != nil && !(!w.ResetsAt.IsZero() && w.ResetsAt.Before(now)) {
			rightW = max(rightW, ansi.StringWidth(planPct(*w.Utilization)+note(w)))
		}
	}
	barW := max(inner-rightW-1, 4)
	for _, k := range names {
		w := s.Windows[k]
		lines = append(lines, plainText(planWindowLabel(k)))
		switch {
		case !w.ResetsAt.IsZero() && w.ResetsAt.Before(now):
			lines = append(lines, dim("reset since, updates on the next claude run"))
		case w.Utilization == nil:
			txt := w.Status
			if txt == "" || txt == "allowed" {
				txt = "under the warning level"
			}
			lines = append(lines, dim(txt))
			if !w.ResetsAt.IsZero() {
				lines = append(lines, dim("Resets "+planTime(w.ResetsAt, now)))
			}
		default:
			u := *w.Utilization
			pct := lipgloss.NewStyle().Foreground(ui.MeterColor(u)).Render(planPct(u))
			if n := note(w); n != "" {
				pct += lipgloss.NewStyle().Foreground(ui.Warn).Render(n)
			}
			gap := max(inner-barW-ansi.StringWidth(planPct(u)+note(w)), 1)
			lines = append(lines, planBar(u, barW)+strings.Repeat(" ", gap)+pct)
			if !w.ResetsAt.IsZero() {
				lines = append(lines, dim("Resets "+planTime(w.ResetsAt, now)))
			}
		}
	}
	return lines
}

// planStatusLine is the /status line "5h 17% · week 78% (as of 14:02)", or ""
// when no window with a known share was reported.
func planStatusLine(now time.Time) string {
	s, ok := runner.LoadPlanLimits(planRunner)
	if !ok {
		return ""
	}
	var parts []string
	for _, k := range s.Names() {
		w := s.Windows[k]
		if w.Utilization == nil || (!w.ResetsAt.IsZero() && w.ResetsAt.Before(now)) {
			continue
		}
		name := map[string]string{"five_hour": "5h", "seven_day": "week", "seven_day_opus": "Opus week", "seven_day_sonnet": "Sonnet week"}[k]
		if name == "" {
			name = strings.ReplaceAll(k, "_", " ")
		}
		parts = append(parts, fmt.Sprintf("%s %d%%", name, int(*w.Utilization*100+0.5)))
	}
	if len(parts) == 0 {
		return ""
	}
	return fmt.Sprintf("%s (%s)", strings.Join(parts, " · "), planAsOf(s.UpdatedAt, now))
}
