package ranking

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

const maxBody = 16 << 20

// RateLimitError is returned on HTTP 429. RetryAfter is the server's
// Retry-After (0 when it sent none); Requests counts the calls already made.
type RateLimitError struct {
	RetryAfter time.Duration
	Requests   int
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("Artificial Analysis rate limit reached (HTTP 429); retry after %s", e.RetryAfter.Round(time.Second))
	}
	return "Artificial Analysis rate limit reached (HTTP 429)"
}

// FetchOptions configure Fetch. BaseURL and Client can be replaced in tests.
type FetchOptions struct {
	BaseURL  string
	Key      string
	Client   *http.Client
	Settings Settings
	// Connected says whether a runner is usable; the per-model detail call is
	// made only for models that map to a connected runner. nil = none.
	Connected func(runner string) bool
	Now       func() time.Time
}

type fetcher struct {
	o        FetchOptions
	requests int
}

// Fetch calls GET {base}/language/models once and, when a connected model's
// effort variants are not separate entries, GET {base}/language/models/{slug}
// for it. It never writes the table; the caller saves it.
func Fetch(ctx context.Context, o FetchOptions) (Table, error) {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 30 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	f := &fetcher{o: o}
	t := Table{Version: tableVersion, FetchedAt: o.Now().UTC()}.With(o.Settings)

	body, err := f.get(ctx, "/language/models")
	if err != nil {
		t.Requests = f.requests
		return t, err
	}
	rows, err := parseList(body)
	if err != nil {
		t.Requests = f.requests
		return t, err
	}
	t.Rows = rows

	// Models with a single, effort-less entry: ask for their variants.
	groups := t.groups()
	seenSlug := map[string]bool{}
	var extra []Row
	drop := map[string]bool{}
	for _, r := range rows {
		g := groups[Normalize(r.Base)]
		if r.Effort != "" || r.Slug == "" || seenSlug[r.Slug] || len(g.rows) != 1 || !t.connected(Normalize(r.Base), o.Connected) {
			continue
		}
		seenSlug[r.Slug] = true
		body, err := f.get(ctx, "/language/models/"+url.PathEscape(r.Slug))
		if err != nil {
			t.Warnings = append(t.Warnings, fmt.Sprintf("%s: effort variants not fetched: %v", r.Base, err))
			if _, limited := err.(*RateLimitError); limited {
				break
			}
			continue
		}
		variants := parseDetail(body, r)
		labelled := false
		for _, v := range variants {
			labelled = labelled || v.Effort != ""
		}
		if labelled {
			drop[r.Slug] = true
			extra = append(extra, variants...)
		}
	}
	if len(drop) > 0 {
		kept := t.Rows[:0:0]
		for _, r := range t.Rows {
			if r.Effort == "" && drop[r.Slug] {
				continue
			}
			kept = append(kept, r)
		}
		t.Rows = append(kept, extra...)
	}
	t.Requests = f.requests
	return t, nil
}

func (t Table) connected(norm string, connected func(string) bool) bool {
	if connected == nil {
		return false
	}
	for _, tg := range t.targets(norm) {
		if connected(tg.Runner) {
			return true
		}
	}
	return false
}

func (f *fetcher) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(f.o.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", f.o.Key)
	req.Header.Set("Accept", "application/json")
	f.requests++
	resp, err := f.o.Client.Do(req)
	if err != nil {
		// The URL carries no secret; the key is only in a header.
		return nil, fmt.Errorf("Artificial Analysis request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, &RateLimitError{RetryAfter: retryAfter(resp.Header.Get("Retry-After"), f.o.Now()), Requests: f.requests}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &runner.KeyRejectedError{Status: resp.StatusCode}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Artificial Analysis returned HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if d, err := http.ParseTime(v); err == nil && d.After(now) {
		return d.Sub(now)
	}
	return 0
}

// parseList reads {"data": [...]} or a bare array. Entries that are not
// objects or have no name are skipped.
func parseList(body []byte) ([]Row, error) {
	var top any
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, fmt.Errorf("Artificial Analysis answered with invalid JSON")
	}
	items := asList(top)
	if m, ok := top.(map[string]any); ok {
		items = asList(m["data"])
	}
	var rows []Row
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if r, ok := parseRow(m, ""); ok {
				rows = append(rows, r)
			}
		}
	}
	return rows, nil
}

// variantKeys are the keys a detail answer may hold its effort variants under.
var variantKeys = []string{"variants", "effort_variants", "reasoning_efforts"}

// parseDetail reads a /models/{slug} answer: the object (under "data" or at
// the top), plus any nested variant entries. fallback names the model when the
// answer has no name.
func parseDetail(body []byte, fallback Row) []Row {
	var top any
	if json.Unmarshal(body, &top) != nil {
		return nil
	}
	if m, ok := top.(map[string]any); ok {
		if d, ok := m["data"]; ok {
			top = d
		}
	}
	var objs []map[string]any
	for _, it := range asList(top) {
		if m, ok := it.(map[string]any); ok {
			objs = append(objs, m)
		}
	}
	if m, ok := top.(map[string]any); ok {
		objs = append(objs, m)
	}
	var rows []Row
	for _, m := range objs {
		nested := false
		for _, k := range variantKeys {
			for _, v := range asList(m[k]) {
				if vm, ok := v.(map[string]any); ok {
					if r, ok := parseRow(vm, fallback.Name); ok {
						rows = append(rows, r)
						nested = true
					}
				}
			}
		}
		if !nested {
			if r, ok := parseRow(m, fallback.Name); ok {
				rows = append(rows, r)
			}
		}
	}
	return rows
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func parseRow(m map[string]any, parentName string) (Row, bool) {
	name := str(m["name"])
	if name == "" {
		name = parentName
	}
	if name == "" {
		return Row{}, false
	}
	slug := str(m["slug"])
	base, effort := ParseEffort(name, slug)
	if effort == "" {
		for _, k := range []string{"reasoning_effort", "effort"} {
			if e := strings.ToLower(strings.TrimSpace(str(m[k]))); e != "" && e != "default" {
				effort = canonEffort(e)
				break
			}
		}
	}
	r := Row{Name: name, Base: base, Slug: slug, Effort: effort}
	if c, ok := m["model_creator"].(map[string]any); ok {
		r.Creator = str(c["name"])
	}
	r.Intelligence = num(m, "evaluations", "artificial_analysis_intelligence_index")
	r.CostPerTask = num(m, "artificial_analysis_intelligence_index_cost", "cost_per_task", "total_cost")
	tokens := num(m, "artificial_analysis_intelligence_index_token_counts", "output_tokens")
	if r.CostPerTask <= 0 && tokens > 0 {
		if p := num(m, "pricing", "price_1m_output_tokens"); p > 0 {
			r.CostPerTask = tokens * p / 1e6
		}
	}
	r.TimePerTask = num(m, "performance_v3", "time_per_task_seconds")
	if r.TimePerTask <= 0 && tokens > 0 {
		if tps := num(m, "median_output_tokens_per_second"); tps > 0 {
			r.TimePerTask = tokens / tps
		}
	}
	for _, k := range []string{"context_window_tokens", "context_window", "context_length", "max_context_tokens"} {
		if v := num(m, k); v > 0 {
			r.Context = int(v)
			break
		}
	}
	return r, true
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// num follows a path of object keys and returns the number there, or 0 when
// any step is missing, null or not a number (numeric strings are accepted).
func num(m map[string]any, path ...string) float64 {
	var cur any = m
	for _, k := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return 0
		}
		cur = obj[k]
	}
	switch v := cur.(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f
	}
	return 0
}
