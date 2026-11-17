package ranking

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestParseEffort(t *testing.T) {
	cases := []struct{ name, slug, base, effort string }{
		{"Claude Opus 5.5 (Max)", "", "Claude Opus 5.5", "max"},
		{"Claude Opus 5.5 (Reasoning, High Effort)", "", "Claude Opus 5.5", "high"},
		{"GPT-5.5 (xhigh)", "", "GPT-5.5", "xhigh"},
		{"GPT-5.5 (x-high)", "", "GPT-5.5", "xhigh"},
		{"DeepSeek V4 Pro (Non-reasoning)", "", "DeepSeek V4 Pro", "off"},
		{"Claude Opus 5.5", "claude-opus-5-5-medium", "Claude Opus 5.5", "medium"},
		{"Claude Opus 5.5", "claude-opus-5-5", "Claude Opus 5.5", ""},
		{"Model (preview)", "", "Model (preview)", ""},
	}
	for _, c := range cases {
		base, effort := ParseEffort(c.name, c.slug)
		if base != c.base || effort != c.effort {
			t.Errorf("ParseEffort(%q,%q) = %q,%q want %q,%q", c.name, c.slug, base, effort, c.base, c.effort)
		}
	}
}

func TestAliasMapping(t *testing.T) {
	tbl := Table{}
	if ts := tbl.targets(Normalize("DeepSeek V4.1 Flash")); len(ts) != 2 || ts[0] != (Target{"dsh", "deepseek-flash"}) {
		t.Fatalf("builtin: %v", ts)
	}
	if ts := tbl.targets(Normalize("Claude Opus 5.5")); len(ts) != 1 || ts[0].Model != "claude-opus-5-5" {
		t.Fatalf("opus: %v", ts)
	}
	cfg := config.Config{Routing: config.RoutingConfig{Aliases: map[string]string{
		"Claude Opus 5.5": "claude/opus", "My Model": "ollama/mine, openai/mine",
	}}}
	tbl = tbl.With(FromConfig(cfg))
	if ts := tbl.targets("claude opus 5.5"); len(ts) != 1 || ts[0].Model != "opus" {
		t.Fatalf("user alias must win: %v", ts)
	}
	if ts := tbl.targets("my model"); len(ts) != 2 {
		t.Fatalf("multi alias: %v", ts)
	}
}

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func opusRows() []Row {
	return []Row{
		{Name: "Claude Opus 5.5 (Max)", Base: "Claude Opus 5.5", Effort: "max", Intelligence: 80, CostPerTask: 10, TimePerTask: 1000, Context: 1_000_000},
		{Name: "Claude Opus 5.5 (Low)", Base: "Claude Opus 5.5", Effort: "low", Intelligence: 50, CostPerTask: 1.5, TimePerTask: 100, Context: 1_000_000},
	}
}

func find(cs []Candidate, runnerName, model, effort string) (Candidate, bool) {
	for _, c := range cs {
		if c.Runner == runnerName && c.Model == model && c.Effort == effort {
			return c, true
		}
	}
	return Candidate{}, false
}

func TestEstimateRatios(t *testing.T) {
	tbl := Table{Rows: opusRows()}
	cs := tbl.Candidates([]string{"claude"})
	max, _ := find(cs, "claude", "claude-opus-5-5", "max")
	if max.Estimated || max.Source != SourceMeasured {
		t.Fatalf("max is measured: %+v", max)
	}
	low, _ := find(cs, "claude", "claude-opus-5-5", "low")
	if low.Estimated || low.CostPerTask != 1.5 {
		t.Fatalf("low must use its own row: %+v", low)
	}
	hi, ok := find(cs, "claude", "claude-opus-5-5", "high")
	if !ok || !hi.Estimated || hi.Source != SourceEstimate {
		t.Fatalf("high must be estimated: %+v", hi)
	}
	approx(t, "high cost", hi.CostPerTask, 10*0.3)
	approx(t, "high time", hi.TimePerTask, 1000*0.3)
	approx(t, "high intel", hi.Intelligence, 80*0.85)
	xh, _ := find(cs, "claude", "claude-opus-5-5", "xhigh")
	approx(t, "xhigh cost", xh.CostPerTask, 5)
	approx(t, "xhigh intel", xh.Intelligence, 80*0.95)
	med, _ := find(cs, "claude", "claude-opus-5-5", "medium")
	approx(t, "medium cost", med.CostPerTask, 2)
	approx(t, "medium intel", med.Intelligence, 60)
}

func TestEstimateFromNonMaxRow(t *testing.T) {
	rows := []Row{{Name: "Claude Opus 5.5 (High)", Base: "Claude Opus 5.5", Effort: "high", Intelligence: 85, CostPerTask: 3, TimePerTask: 30}}
	cs := Table{Rows: rows}.Candidates([]string{"claude"})
	mx, _ := find(cs, "claude", "claude-opus-5-5", "max")
	approx(t, "max cost from high", mx.CostPerTask, 10)
	approx(t, "max intel from high", mx.Intelligence, 100)
}

func TestFallbackTiers(t *testing.T) {
	cs := Table{}.Candidates([]string{"claude", "dsh"})
	opus, ok := find(cs, "claude", "claude-opus-5-5", "max")
	sonnet, _ := find(cs, "claude", "claude-sonnet-5-5", "max")
	haiku, _ := find(cs, "claude", "claude-haiku-5-5", "max")
	if !ok || opus.Source != SourceFallback || !opus.Estimated {
		t.Fatalf("fallback expected: %+v", opus)
	}
	if !(opus.Intelligence > sonnet.Intelligence && sonnet.Intelligence > haiku.Intelligence) {
		t.Fatal("opus > sonnet > haiku")
	}
	pro, _ := find(cs, "dsh", "deepseek-v4-pro", "max")
	flash, _ := find(cs, "dsh", "deepseek-flash", "max")
	if flash.CostPerTask >= pro.CostPerTask || math.Abs(flash.Intelligence-pro.Intelligence) > 5 {
		t.Fatalf("flash is cheaper and close in intelligence: %+v %+v", flash, pro)
	}
	// override from config
	cfg := config.Config{Routing: config.RoutingConfig{Fallback: map[string]config.FallbackTier{
		"claude/claude-haiku-5-5": {Intelligence: 99, Cost: 1, Time: 1},
	}}}
	cs = Table{}.With(FromConfig(cfg)).Candidates([]string{"claude"})
	h, _ := find(cs, "claude", "claude-haiku-5-5", "max")
	approx(t, "override intel", h.Intelligence, 99)
}

func TestEffortsMapping(t *testing.T) {
	if got := Efforts("claude", "claude-opus-5-5"); strings.Join(got, ",") != "low,medium,high,xhigh,max" {
		t.Fatalf("claude: %v", got)
	}
	// dsh: off, low, high, max; medium runs as high, so it is not listed twice.
	if got := Efforts("dsh", "deepseek-flash"); strings.Join(got, ",") != "off,low,high,max" {
		t.Fatalf("dsh flash: %v", got)
	}
	// V4 Pro: low runs as high.
	if got := Efforts("dsh", "deepseek-v4-pro"); strings.Join(got, ",") != "off,high,max" {
		t.Fatalf("dsh pro: %v", got)
	}
	if got := Efforts("deepseek", "deepseek-flash"); len(got) != 1 || got[0] != "" {
		t.Fatalf("no effort control: %v", got)
	}
}

func TestScoreMath(t *testing.T) {
	cs := []Candidate{
		{Intelligence: 100, CostPerTask: 100, TimePerTask: 100, Context: 1_000_000},
		{Intelligence: 0.0001, CostPerTask: 1, TimePerTask: 1, Context: 500_000},
		{Intelligence: 50, CostPerTask: 10, TimePerTask: 10, Context: 0},
	}
	Score(cs, Weights{Intelligence: 1})
	approx(t, "intel only a", cs[0].Balance, 100)
	approx(t, "intel only b", cs[1].Balance, 0)
	Score(cs, Weights{Cost: 1})
	approx(t, "cost best", cs[1].Balance, 100)
	approx(t, "cost worst", cs[0].Balance, 0)
	approx(t, "cost log middle", cs[2].Balance, 50) // 10 is the log midpoint of 1..100
	Score(cs, Weights{Context: 1})
	approx(t, "ctx full", cs[0].Balance, 100)
	approx(t, "ctx half", cs[1].Balance, 50)
	approx(t, "ctx unknown", cs[2].Balance, 50)
	Score(cs, Weights{}) // defaults 50/30/10/10
	want := (50*100 + 30*0 + 10*0 + 10*100) / 100.0
	approx(t, "default weights", cs[0].Balance, want)
	Score(cs, Weights{Intelligence: 2, Cost: 2})
	approx(t, "normalized weights", cs[0].Balance, 50)
}

func TestLookupAndCap(t *testing.T) {
	tbl := Table{Rows: opusRows()}
	c, ok := tbl.Lookup("claude", "", "") // claude defaults: opus, high
	if !ok || c.Model != "claude-opus-5-5" || c.Effort != "high" {
		t.Fatalf("claude defaults: %+v %v", c, ok)
	}
	approx(t, "role cost", c.CostPerTask, 3)
	low, _ := tbl.Lookup("claude", "claude-opus-5-5", "low")
	if low.CostPerTask > c.CostPerTask {
		t.Fatal("low must fit under the high cap")
	}
	if _, ok := tbl.Lookup("ollama", "llama3", ""); ok {
		t.Fatal("unranked pair must not be found")
	}
	if d, ok := (Table{}).Lookup("dsh", "deepseek-v4-pro", "low"); !ok || d.Effort != "high" {
		t.Fatalf("v4 pro low counts as high: %+v", d)
	}
}

func TestFallbackNotMixedWithMeasured(t *testing.T) {
	cs := Table{Rows: opusRows()}.Candidates(RankableRunners)
	for _, c := range cs {
		if c.Source == SourceFallback {
			t.Fatalf("relative tiers must not mix with measured numbers: %+v", c)
		}
	}
}

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "rankings.json")
	tbl, err := LoadFrom(path)
	if err != nil || len(tbl.Rows) != 0 {
		t.Fatalf("missing file is empty: %v %v", tbl, err)
	}
	tbl.Rows = opusRows()
	tbl.Requests = 2
	tbl.FetchedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := tbl.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFrom(path)
	if err != nil || len(got.Rows) != 2 || got.Requests != 2 || !got.FetchedAt.Equal(tbl.FetchedAt) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

// aaServer serves the list and the detail routes and counts the calls.
func aaServer(t *testing.T, list, detail string, calls *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.Header.Get("x-api-key") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/language/models":
			_, _ = w.Write([]byte(list))
		case strings.HasPrefix(r.URL.Path, "/language/models/"):
			_, _ = w.Write([]byte(detail))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func row(name, slug string, intel, cost, secs float64) string {
	return `{"name":"` + name + `","slug":"` + slug + `","model_creator":{"name":"X"},"evaluations":{"artificial_analysis_intelligence_index":` + f(intel) + `},` +
		`"artificial_analysis_intelligence_index_cost":{"cost_per_task":{"total_cost":` + f(cost) + `}},` +
		`"performance_v3":{"time_per_task_seconds":` + f(secs) + `},"context_window_tokens":1000000}`
}

func f(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func connectedClaude(r string) bool { return r == "claude" }

func TestFetchSeparateEffortEntries(t *testing.T) {
	var calls int32
	list := `{"status":200,"data":[` + row("Claude Opus 5.5 (Max)", "claude-opus-5-5-max", 80, 10, 1000) + `,` +
		row("Claude Opus 5.5 (Low)", "claude-opus-5-5-low", 50, 1.5, 100) + `,` +
		row("Mystery Model", "mystery", 40, 1, 10) + `,{"name":"","slug":"x"},"junk",{"name":"No Numbers","evaluations":null}]}`
	srv := aaServer(t, list, `{}`, &calls)
	tbl, err := Fetch(context.Background(), FetchOptions{BaseURL: srv.URL, Key: "secret", Connected: connectedClaude})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || tbl.Requests != 1 {
		t.Fatalf("variants are separate entries: one call, got %d / %d", calls, tbl.Requests)
	}
	if len(tbl.Rows) != 4 {
		t.Fatalf("rows: %+v", tbl.Rows)
	}
	if got := tbl.Unmapped(); strings.Join(got, "|") != "Mystery Model|No Numbers" {
		t.Fatalf("unmapped: %v", got)
	}
	c, ok := tbl.Lookup("claude", "claude-opus-5-5", "max")
	if !ok || c.Source != SourceMeasured || c.CostPerTask != 10 || c.Context != 1_000_000 {
		t.Fatalf("%+v", c)
	}
}

func TestFetchSingleEntryUsesDetail(t *testing.T) {
	var calls int32
	list := `[` + row("Claude Opus 5.5", "claude-opus-5-5", 78, 6, 600) + `,` + row("DeepSeek V4 Pro", "deepseek-v4-pro", 70, 2, 200) + `]`
	detail := `{"data":{"name":"Claude Opus 5.5","variants":[` +
		row("Claude Opus 5.5 (Max)", "claude-opus-5-5-max", 80, 10, 1000) + `,` + row("Claude Opus 5.5 (Low)", "claude-opus-5-5-low", 50, 1.5, 100) + `]}}`
	srv := aaServer(t, list, detail, &calls)
	tbl, err := Fetch(context.Background(), FetchOptions{BaseURL: srv.URL, Key: "secret", Connected: connectedClaude})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || tbl.Requests != 2 {
		t.Fatalf("one list call and one detail call (connected model only): %d", calls)
	}
	var efforts []string
	for _, r := range tbl.Rows {
		if r.Base == "Claude Opus 5.5" {
			efforts = append(efforts, r.Effort)
		}
	}
	if strings.Join(efforts, ",") != "max,low" {
		t.Fatalf("the effort-less row must be replaced by the variants: %v", efforts)
	}
}

func TestFetchErrors(t *testing.T) {
	var calls int32
	srv := aaServer(t, `{}`, `{}`, &calls)
	_, err := Fetch(context.Background(), FetchOptions{BaseURL: srv.URL, Key: "wrong"})
	if err == nil || strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("rejected key: %v", err)
	}
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer limited.Close()
	_, err = Fetch(context.Background(), FetchOptions{BaseURL: limited.URL, Key: "k"})
	rl, ok := err.(*RateLimitError)
	if !ok || rl.RetryAfter != 2*time.Minute || !strings.Contains(err.Error(), "2m0s") {
		t.Fatalf("429: %#v", err)
	}
}

func TestParseRowDefensive(t *testing.T) {
	rows, err := parseList([]byte(`{"data":[{"name":"A","evaluations":{"artificial_analysis_intelligence_index":null},` +
		`"pricing":{"price_1m_output_tokens":"10"},"artificial_analysis_intelligence_index_token_counts":{"output_tokens":2000000},` +
		`"median_output_tokens_per_second":100}]}`))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	r := rows[0]
	if r.Intelligence != 0 || r.CostPerTask != 20 || r.TimePerTask != 20000 {
		t.Fatalf("derived from tokens: %+v", r)
	}
	if _, err := parseList([]byte(`not json`)); err == nil {
		t.Fatal("invalid JSON must fail")
	}
}

func TestRunNoKeyShowsFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(KeyEnv, "")
	rep := Run(context.Background(), RunOptions{Path: filepath.Join(t.TempDir(), "r.json"), Connected: map[string]bool{"claude": true}})
	if !rep.NoKey || rep.Fetched {
		t.Fatalf("%+v", rep)
	}
	text := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{"decsr connect artificialanalysis", "fallback tiers", "claude-opus-5-5", "tier"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, Attribution) {
		t.Errorf("the fallback tiers carry no Artificial Analysis data, so no credit:\n%s", text)
	}
}

func TestRunFetchSavesAndReports(t *testing.T) {
	var calls int32
	list := `{"data":[` + row("Claude Opus 5.5 (Max)", "a", 80, 10, 1000) + `,` + row("Mystery Model", "m", 40, 1, 10) + `]}`
	srv := aaServer(t, list, `{}`, &calls)
	path := filepath.Join(t.TempDir(), "r.json")
	rep := Run(context.Background(), RunOptions{BaseURL: srv.URL, Key: "secret", Path: path, Connected: map[string]bool{"claude": true}})
	if rep.Err != nil || !rep.Fetched || !rep.Saved || rep.Requests != 1 {
		t.Fatalf("%+v", rep)
	}
	text := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{"Requests used: 1", "free tier: 100", "Mystery Model", "Not mapped", "measured", Attribution} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if tbl, _ := LoadFrom(path); len(tbl.Rows) != 2 {
		t.Fatal("rankings.json must be saved")
	}
	// A later failed fetch keeps showing the saved table.
	rep = Run(context.Background(), RunOptions{BaseURL: srv.URL, Key: "bad", Path: path})
	if rep.Err == nil || rep.Fetched || !strings.Contains(strings.Join(rep.Lines(), "\n"), "not refreshed") {
		t.Fatalf("%+v\n%s", rep, strings.Join(rep.Lines(), "\n"))
	}
}
