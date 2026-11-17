package ranking

import (
	"math"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// Candidate sources.
const (
	SourceMeasured = "measured"
	SourceEstimate = "estimate"
	SourceFallback = "fallback"
)

// Candidate is one (runner, model, effort) pair with its numbers. CostPerTask
// is in USD, TimePerTask in seconds; 0 means unknown.
type Candidate struct {
	Name         string  `json:"name"`
	Runner       string  `json:"runner"`
	Model        string  `json:"model"`
	Effort       string  `json:"effort"`
	Intelligence float64 `json:"intelligence"`
	CostPerTask  float64 `json:"cost_per_task"`
	TimePerTask  float64 `json:"time_per_task"`
	Context      int     `json:"context"`
	Estimated    bool    `json:"estimated"`
	Balance      float64 `json:"balance"`
	Source       string  `json:"source"`
}

// Ratios against max effort: tokens and cost (time follows tokens), and
// intelligence. Efforts not listed (off, minimal) use the low ratios.
var (
	costRatio  = map[string]float64{"max": 1, "xhigh": 0.5, "high": 0.3, "medium": 0.2, "low": 0.1}
	intelRatio = map[string]float64{"max": 1, "xhigh": 0.95, "high": 0.85, "medium": 0.75, "low": 0.6}
)

func ratios(effort string) (cost, intel float64) {
	if effort == "" {
		effort = "high"
	}
	if c, ok := costRatio[effort]; ok {
		return c, intelRatio[effort]
	}
	return costRatio["low"], intelRatio["low"]
}

// builtinFallback holds relative tiers only (no AA numbers): claude opus >
// sonnet > haiku; deepseek pro and flash are close in intelligence but flash
// is cheaper; gpt large > mini.
var builtinFallback = map[string]Tier{
	"claude/claude-opus-5-5":   {Intelligence: 90, Cost: 10, Time: 10},
	"claude/claude-sonnet-5-5": {Intelligence: 78, Cost: 4, Time: 6},
	"claude/claude-haiku-5-5":  {Intelligence: 60, Cost: 1, Time: 3},
	"dsh/deepseek-v4-pro":      {Intelligence: 72, Cost: 2, Time: 8},
	"dsh/deepseek-flash":       {Intelligence: 70, Cost: 0.5, Time: 4},
	"deepseek/deepseek-v4-pro": {Intelligence: 72, Cost: 2, Time: 8},
	"deepseek/deepseek-flash":  {Intelligence: 70, Cost: 0.5, Time: 4},
	"codex/gpt-5.5":            {Intelligence: 88, Cost: 8, Time: 9},
	"codex/gpt-5.4-mini":       {Intelligence: 65, Cost: 1, Time: 3},
	"openai/gpt-5.5":           {Intelligence: 88, Cost: 8, Time: 9},
	"openai/gpt-5.4-mini":      {Intelligence: 65, Cost: 1, Time: 3},
}

// FallbackTiers is the built-in table merged with the user's overrides.
func (t Table) fallbackTiers() map[string]Tier {
	out := make(map[string]Tier, len(builtinFallback)+len(t.set.Fallback))
	for k, v := range builtinFallback {
		out[k] = v
	}
	for k, v := range t.set.Fallback {
		out[k] = v
	}
	return out
}

type pair struct{ runner, model string }

// group is the rows of one AA model.
type group struct {
	name string
	rows []Row
}

func (t Table) groups() map[string]*group {
	out := map[string]*group{}
	for _, r := range t.Rows {
		k := Normalize(r.Base)
		g := out[k]
		if g == nil {
			g = &group{name: r.Base}
			out[k] = g
		}
		g.rows = append(g.rows, r)
	}
	return out
}

// Efforts are the efforts a runner supports for a model, mapped the way the
// provider runs them and deduplicated: dsh runs medium as high and, for V4
// Pro, low as high. A runner with no effort control gets [""].
func Efforts(runnerName, model string) []string {
	opts := runner.EffortOptions(runnerName, model)
	if runnerName == "dsh" {
		opts = append([]string{"off"}, opts...)
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range opts {
		e = MapEffort(runnerName, model, e)
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// MapEffort maps an effort to the one the provider really runs.
func MapEffort(runnerName, model, effort string) string {
	switch runnerName {
	case "dsh":
		e := runner.MapDshEffort(effort)
		if e == "low" && strings.Contains(strings.ToLower(model), "pro") {
			return "high"
		}
		return e
	case "claude":
		return runner.EffectiveEffort("claude", runner.NormalizeEffort(effort))
	}
	return runner.NormalizeEffort(effort)
}

func wanted(runners []string, name string) bool {
	for _, r := range runners {
		if r == name {
			return true
		}
	}
	return false
}

// Candidates lists every (runner, model, effort) pair of the given runners
// with its numbers and balance score, sorted by balance (best first). The data
// comes from the AA row of that exact effort, else an estimate from the
// model's other rows (Estimated), else the fallback table.
func (t Table) Candidates(runners []string) []Candidate {
	cs := t.build(runners)
	cs = dropFallbackWhenRanked(cs)
	Score(cs, t.set.Weights)
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Balance != cs[j].Balance {
			return cs[i].Balance > cs[j].Balance
		}
		return cs[i].Name+cs[i].Runner+cs[i].Effort < cs[j].Name+cs[j].Runner+cs[j].Effort
	})
	return cs
}

// dropFallbackWhenRanked removes the fallback pairs once any pair has AA
// numbers: relative tiers and measured dollars are not comparable, so a pair
// AA does not list stays unranked rather than being mixed in.
func dropFallbackWhenRanked(cs []Candidate) []Candidate {
	ranked := false
	for _, c := range cs {
		ranked = ranked || c.Source != SourceFallback
	}
	if !ranked {
		return cs
	}
	out := cs[:0:0]
	for _, c := range cs {
		if c.Source != SourceFallback {
			out = append(out, c)
		}
	}
	return out
}

func (t Table) build(runners []string) []Candidate {
	groups := t.groups()
	tiers := t.fallbackTiers()
	byPair := map[pair]*group{}
	var order []pair
	add := func(p pair, g *group) {
		if !wanted(runners, p.runner) {
			return
		}
		if _, ok := byPair[p]; !ok {
			order = append(order, p)
			byPair[p] = g
		} else if byPair[p] == nil {
			byPair[p] = g
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, tg := range t.targets(k) {
			add(pair{tg.Runner, tg.Model}, groups[k])
		}
	}
	tierKeys := make([]string, 0, len(tiers))
	for k := range tiers {
		tierKeys = append(tierKeys, k)
	}
	sort.Strings(tierKeys)
	for _, k := range tierKeys {
		if r, m, ok := strings.Cut(k, "/"); ok {
			add(pair{r, m}, nil)
		}
	}

	var out []Candidate
	for _, p := range order {
		for _, e := range Efforts(p.runner, p.model) {
			if c, ok := t.candidate(p, e, byPair[p], tiers); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func (t Table) candidate(p pair, effort string, g *group, tiers map[string]Tier) (Candidate, bool) {
	c := Candidate{Runner: p.runner, Model: p.model, Effort: effort, Name: p.model}
	if g != nil {
		c.Name = g.name
		if r, src, ok := estimateFrom(g.rows, effort); ok {
			c.Intelligence, c.CostPerTask, c.TimePerTask, c.Context = r.Intelligence, r.CostPerTask, r.TimePerTask, r.Context
			c.Source = src
			c.Estimated = src != SourceMeasured
			return c, true
		}
	}
	tier, ok := tiers[p.runner+"/"+p.model]
	if !ok {
		tier, ok = tiers[p.model]
	}
	if !ok {
		return c, false
	}
	cr, ir := ratios(effort)
	c.Intelligence, c.CostPerTask, c.TimePerTask, c.Context = tier.Intelligence*ir, tier.Cost*cr, tier.Time*cr, tier.Context
	c.Source, c.Estimated = SourceFallback, true
	return c, true
}

// estimateFrom returns the AA numbers for an effort: the exact row, else the
// ratio-scaled numbers of the known row closest to max.
func estimateFrom(rows []Row, effort string) (Row, string, bool) {
	want := effort
	for _, r := range rows {
		if r.Effort == want {
			return r, SourceMeasured, true
		}
	}
	if want == "" {
		want = "high"
		for _, r := range rows {
			if r.Effort == want {
				return r, SourceMeasured, true
			}
		}
	}
	var ref *Row
	refCost := 0.0
	for i := range rows {
		e := rows[i].Effort
		if e == "" {
			e = "high"
		}
		if c, ok := costRatio[e]; ok && c > refCost {
			ref, refCost = &rows[i], c
		}
	}
	if ref == nil {
		return Row{}, "", false
	}
	refE := ref.Effort
	if refE == "" {
		refE = "high"
	}
	cr, ir := ratios(want)
	rc, ri := ratios(refE)
	out := *ref
	out.Effort = effort
	out.CostPerTask = ref.CostPerTask * cr / rc
	out.TimePerTask = ref.TimePerTask * cr / rc
	out.Intelligence = ref.Intelligence * ir / ri
	return out, SourceEstimate, true
}

// Lookup finds the numbers of one pair, with the balance scored across every
// rankable runner. The model and effort are read the way the runner reads
// them (claude defaults, dsh mapping). It is false for an unranked pair.
func (t Table) Lookup(runnerName, model, effort string) (Candidate, bool) {
	model = runner.EffectiveModel(runnerName, model)
	effort = MapEffort(runnerName, model, effort)
	for _, c := range t.Candidates(RankableRunners) {
		if c.Runner == runnerName && c.Model == model && c.Effort == effort {
			return c, true
		}
	}
	return Candidate{}, false
}

// Score sets the balance (0-100) of every candidate, in place: intelligence
// scaled 0-100 across the list, cost and time log-scaled (cheaper and faster
// is better), context against 1M tokens. A value that is unknown scores 50.
func Score(cs []Candidate, w Weights) {
	if w == (Weights{}) {
		w = DefaultWeights
	}
	sum := w.Intelligence + w.Cost + w.Time + w.Context
	if sum <= 0 {
		return
	}
	intel := scale(cs, func(c Candidate) float64 { return c.Intelligence }, false, false)
	cost := scale(cs, func(c Candidate) float64 { return c.CostPerTask }, true, true)
	tm := scale(cs, func(c Candidate) float64 { return c.TimePerTask }, true, true)
	for i := range cs {
		ctx := 50.0
		if cs[i].Context > 0 {
			ctx = math.Min(float64(cs[i].Context)/1e6, 1) * 100
		}
		cs[i].Balance = (w.Intelligence*intel[i] + w.Cost*cost[i] + w.Time*tm[i] + w.Context*ctx) / sum
	}
}

// scale maps a value to 0-100 across the list (min-max, in log space when
// logScale). lowerBetter flips it. Unknown (<= 0) values get 50; a list with
// no spread gets 100.
func scale(cs []Candidate, get func(Candidate) float64, logScale, lowerBetter bool) []float64 {
	f := func(v float64) float64 {
		if logScale {
			return math.Log(v)
		}
		return v
	}
	lo, hi, n := math.Inf(1), math.Inf(-1), 0
	for _, c := range cs {
		if v := get(c); v > 0 {
			x := f(v)
			lo, hi, n = math.Min(lo, x), math.Max(hi, x), n+1
		}
	}
	out := make([]float64, len(cs))
	for i, c := range cs {
		v := get(c)
		switch {
		case v <= 0:
			out[i] = 50
		case hi == lo || n == 0:
			out[i] = 100
		default:
			s := (f(v) - lo) / (hi - lo) * 100
			if lowerBetter {
				s = 100 - s
			}
			out[i] = s
		}
	}
	return out
}
