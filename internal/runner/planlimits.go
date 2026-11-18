package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Plan limits are the usage windows of a subscription ("5h", "week") as the
// claude CLI prints them in its rate_limit_event lines. They come only from
// that output: decsr never calls an account or usage endpoint, never starts a
// run to refresh them, and nothing schedules, retries or waits based on these
// numbers. They are shown to the user and nothing else.

// PlanLimitsFile is the name of the global file the windows are kept in,
// under ~/.decsr: limits belong to the account, not to a project.
const PlanLimitsFile = "plan-limits.json"

// PlanWindow is one usage window of a plan, e.g. "five_hour" or "seven_day".
type PlanWindow struct {
	// Utilization is the used fraction (0.78 is 78%); nil when the CLI gave
	// none, which it does while the status is plain "allowed".
	Utilization *float64  `json:"utilization,omitempty"`
	ResetsAt    time.Time `json:"resets_at,omitempty"`
	Status      string    `json:"status,omitempty"`
	// Threshold is the warning level the window passed (0.75), 0 for none.
	Threshold float64 `json:"threshold,omitempty"`
	// UpdatedAt is when an event last carried this window.
	UpdatedAt time.Time `json:"updated_at"`
}

// PlanSnapshot is what one runner last reported.
type PlanSnapshot struct {
	UpdatedAt time.Time             `json:"updated_at"`
	Status    string                `json:"status,omitempty"` // overall: allowed, allowed_warning, rejected
	Windows   map[string]PlanWindow `json:"windows"`
}

// PlanLimits is the content of plan-limits.json: a snapshot per runner.
type PlanLimits struct {
	Runners map[string]PlanSnapshot `json:"runners"`
}

// Names lists the window keys of s: five_hour, seven_day, then the
// rest alphabetically.
func (s PlanSnapshot) Names() []string {
	var names []string
	for k := range s.Windows {
		names = append(names, k)
	}
	rank := func(k string) int {
		switch k {
		case "five_hour":
			return 0
		case "seven_day":
			return 1
		}
		return 2
	}
	sort.Slice(names, func(i, j int) bool {
		if ri, rj := rank(names[i]), rank(names[j]); ri != rj {
			return ri < rj
		}
		return names[i] < names[j]
	})
	return names
}

// claudeRateLimitInfo is the rate_limit_info of a rate_limit_event line.
type claudeRateLimitInfo struct {
	Status             string                       `json:"status"`
	ResetsAt           float64                      `json:"resetsAt"`
	RateLimitType      string                       `json:"rateLimitType"`
	Utilization        *float64                     `json:"utilization"`
	SurpassedThreshold float64                      `json:"surpassedThreshold"`
	UnifiedWindows     map[string]claudeLimitWindow `json:"unifiedWindows"`
}

type claudeLimitWindow struct {
	Utilization        *float64 `json:"utilization"`
	ResetsAt           float64  `json:"resetsAt"`
	Status             string   `json:"status"`
	SurpassedThreshold float64  `json:"surpassedThreshold"`
}

func unixTime(sec float64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0)
}

// windows turns the event into plan windows stamped with now. The windows of
// unifiedWindows are used; an event without them is one window named by its
// rateLimitType.
func (i claudeRateLimitInfo) windows(now time.Time) map[string]PlanWindow {
	out := map[string]PlanWindow{}
	for name, w := range i.UnifiedWindows {
		out[name] = PlanWindow{Utilization: w.Utilization, ResetsAt: unixTime(w.ResetsAt), Status: w.Status, Threshold: w.SurpassedThreshold, UpdatedAt: now}
	}
	if len(out) == 0 && i.RateLimitType != "" {
		out[i.RateLimitType] = PlanWindow{Utilization: i.Utilization, ResetsAt: unixTime(i.ResetsAt), Status: i.Status, Threshold: i.SurpassedThreshold, UpdatedAt: now}
	}
	return out
}

// merge folds the event's windows into s: a window the event carries replaces
// the stored one, a window it lacks keeps its last value and its own time.
func (s PlanSnapshot) merge(status string, ws map[string]PlanWindow, now time.Time) PlanSnapshot {
	merged := make(map[string]PlanWindow, len(s.Windows)+len(ws))
	for k, w := range s.Windows {
		merged[k] = w
	}
	for k, w := range ws {
		merged[k] = w
	}
	if status == "" {
		status = s.Status
	}
	return PlanSnapshot{UpdatedAt: now, Status: status, Windows: merged}
}

// planLimitsPath is ~/.decsr/plan-limits.json ("" when the home is unknown).
func planLimitsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".decsr", PlanLimitsFile)
}

var planMu sync.Mutex

// recordClaudeRateLimit is the one place that records plan limits: it merges
// the windows of an event into the saved snapshot of "claude" and writes the
// file atomically. Any failure is ignored; a run never breaks over it.
func recordClaudeRateLimit(info claudeRateLimitInfo, now time.Time) {
	ws := info.windows(now)
	if len(ws) == 0 {
		return
	}
	planMu.Lock()
	defer planMu.Unlock()
	path := planLimitsPath()
	if path == "" {
		return
	}
	all := readPlanLimits(path)
	if all.Runners == nil {
		all.Runners = map[string]PlanSnapshot{}
	}
	all.Runners["claude"] = all.Runners["claude"].merge(info.Status, ws, now)
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), PlanLimitsFile+".*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(data, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		os.Remove(tmp.Name())
	}
}

func readPlanLimits(path string) PlanLimits {
	var all PlanLimits
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &all)
	}
	return all
}

// LoadPlanLimits returns what runner last reported ("claude" is the only one
// that reports today); ok is false when nothing was recorded.
func LoadPlanLimits(runnerName string) (PlanSnapshot, bool) {
	path := planLimitsPath()
	if path == "" {
		return PlanSnapshot{}, false
	}
	planMu.Lock()
	defer planMu.Unlock()
	s, ok := readPlanLimits(path).Runners[runnerName]
	return s, ok && len(s.Windows) > 0
}
