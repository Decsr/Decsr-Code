package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

const rateLimitLine = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1795154400,"rateLimitType":"seven_day","utilization":0.78,"overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false,"surpassedThreshold":0.75,"unifiedWindows":{"seven_day":{"utilization":0.78,"resetsAt":1795154400,"status":"allowed_warning","surpassedThreshold":0.75}},"overageEnabled":false},"uuid":"u","session_id":"s"}`

func parseInfo(t *testing.T, line string) claudeRateLimitInfo {
	t.Helper()
	evt, err := parseClaudeStreamEvent([]byte(line))
	if err != nil || evt.Type != "rate_limit_event" || evt.RateLimitInfo == nil {
		t.Fatalf("parse: %v %+v", err, evt)
	}
	return *evt.RateLimitInfo
}

func TestRateLimitEventParses(t *testing.T) {
	now := time.Unix(1795000000, 0)
	ws := parseInfo(t, rateLimitLine).windows(now)
	w, ok := ws["seven_day"]
	if !ok || len(ws) != 1 {
		t.Fatalf("windows = %+v", ws)
	}
	if w.Utilization == nil || *w.Utilization != 0.78 || w.Threshold != 0.75 || w.Status != "allowed_warning" {
		t.Errorf("window = %+v", w)
	}
	if !w.ResetsAt.Equal(time.Unix(1795154400, 0)) || !w.UpdatedAt.Equal(now) {
		t.Errorf("times = %v %v", w.ResetsAt, w.UpdatedAt)
	}
}

func TestRateLimitEventNullUtilizationAndUnknownWindow(t *testing.T) {
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1795154400,"rateLimitType":"five_hour","utilization":null,"unifiedWindows":{"five_hour":{"utilization":null,"resetsAt":1795154400,"status":"allowed"},"seven_day_haiku":{"utilization":0.1,"resetsAt":1795200000,"status":"allowed"}}}}`
	ws := parseInfo(t, line).windows(time.Now())
	if len(ws) != 2 || ws["five_hour"].Utilization != nil || ws["seven_day_haiku"].Utilization == nil {
		t.Errorf("windows = %+v", ws)
	}
	// Without unifiedWindows the event is one window named by its type.
	one := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"five_hour"}}`
	ws = parseInfo(t, one).windows(time.Now())
	if _, ok := ws["five_hour"]; !ok || len(ws) != 1 {
		t.Errorf("windows = %+v", ws)
	}
}

func TestPlanSnapshotMergeKeepsMissingWindows(t *testing.T) {
	t0, t1 := time.Unix(1000, 0), time.Unix(2000, 0)
	u := 0.5
	first := PlanSnapshot{}.merge("allowed", map[string]PlanWindow{
		"five_hour": {Utilization: &u, UpdatedAt: t0},
		"seven_day": {Utilization: &u, UpdatedAt: t0},
	}, t0)
	v := 0.9
	second := first.merge("allowed_warning", map[string]PlanWindow{"seven_day": {Utilization: &v, UpdatedAt: t1}}, t1)
	if second.Windows["five_hour"].UpdatedAt != t0 || *second.Windows["five_hour"].Utilization != 0.5 {
		t.Errorf("five_hour changed: %+v", second.Windows["five_hour"])
	}
	if *second.Windows["seven_day"].Utilization != 0.9 || second.Windows["seven_day"].UpdatedAt != t1 || second.UpdatedAt != t1 {
		t.Errorf("seven_day = %+v", second)
	}
	if got := (PlanSnapshot{Windows: map[string]PlanWindow{"seven_day_sonnet": {}, "seven_day": {}, "five_hour": {}, "a": {}}}).Names(); !reflect.DeepEqual(got, []string{"five_hour", "seven_day", "a", "seven_day_sonnet"}) {
		t.Errorf("order = %v", got)
	}
}

func TestRecordClaudeRateLimitSavesAndLoads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, ok := LoadPlanLimits("claude"); ok {
		t.Fatal("nothing recorded yet")
	}
	t0 := time.Unix(1795000000, 0)
	recordClaudeRateLimit(parseInfo(t, rateLimitLine), t0)
	five := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.17,"resetsAt":1795100000,"status":"allowed"}}}}`
	recordClaudeRateLimit(parseInfo(t, five), t0.Add(time.Hour))

	s, ok := LoadPlanLimits("claude")
	if !ok || len(s.Windows) != 2 {
		t.Fatalf("snapshot = %+v %v", s, ok)
	}
	if !s.Windows["seven_day"].UpdatedAt.Equal(t0) || !s.Windows["five_hour"].UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("window times = %+v", s.Windows)
	}
	data, err := os.ReadFile(filepath.Join(home, ".decsr", "plan-limits.json"))
	if err != nil || !json.Valid(data) {
		t.Fatalf("file: %v %s", err, data)
	}
	if left, _ := filepath.Glob(filepath.Join(home, ".decsr", "plan-limits.json.*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	if _, ok := LoadPlanLimits("codex"); ok {
		t.Error("another runner has nothing")
	}
}
