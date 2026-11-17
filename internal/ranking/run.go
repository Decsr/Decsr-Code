package ranking

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// Connected reports the rankable runners that can run now: the CLI is on PATH
// or the API key is available. It makes no network call.
func Connected() map[string]bool {
	out := map[string]bool{}
	for _, r := range RankableRunners {
		switch r {
		case "claude", "codex":
			_, err := exec.LookPath(r)
			out[r] = err == nil
		case "dsh":
			_, err := exec.LookPath("dsh")
			out[r] = err == nil && runner.KeySource("deepseek", "") != ""
		default:
			out[r] = runner.KeySource(r, "") != ""
		}
	}
	return out
}

// RunOptions configure Run. The zero value uses the real API, the saved key
// and ~/.decsr/rankings.json.
type RunOptions struct {
	Settings  Settings
	BaseURL   string
	Client    *http.Client
	Key       string          // "" = the environment variable, then the saved key
	Path      string          // "" = ~/.decsr/rankings.json
	Connected map[string]bool // nil = Connected()
}

// Report is what Run produced, ready to print or encode.
type Report struct {
	Candidates  []Candidate     `json:"candidates"`
	Unmapped    []string        `json:"unmapped"`
	Connected   map[string]bool `json:"connected"`
	FetchedAt   time.Time       `json:"fetched_at"`
	Requests    int             `json:"requests"`
	Fetched     bool            `json:"fetched"`
	NoKey       bool            `json:"no_key"`
	Saved       bool            `json:"saved"`
	Warnings    []string        `json:"warnings,omitempty"`
	Err         error           `json:"-"`
	Error       string          `json:"error,omitempty"`
	Attribution string          `json:"attribution"`
}

// Run fetches the rankings (when a key is available), saves them and builds
// the sorted table. With no key, or when the fetch fails, it shows the saved
// table, or the fallback table when there is none.
func Run(ctx context.Context, o RunOptions) Report {
	rep := Report{Attribution: Attribution, Connected: o.Connected}
	if rep.Connected == nil {
		rep.Connected = Connected()
	}
	path := o.Path
	if path == "" {
		path = Path()
	}
	key := o.Key
	if key == "" {
		key, _, _ = runner.ResolveKey(CredName, KeyEnv)
	}
	tbl, loadErr := LoadFrom(path)
	if loadErr != nil {
		rep.Warnings = append(rep.Warnings, loadErr.Error())
	}
	if key == "" {
		rep.NoKey = true
	} else {
		fetched, err := Fetch(ctx, FetchOptions{
			BaseURL: o.BaseURL, Key: key, Client: o.Client, Settings: o.Settings,
			Connected: func(r string) bool { return rep.Connected[r] },
		})
		rep.Requests = fetched.Requests
		if err != nil {
			rep.Err, rep.Error = err, err.Error()
		} else {
			tbl, rep.Fetched = fetched, true
			if err := tbl.SaveTo(path); err != nil {
				rep.Warnings = append(rep.Warnings, "could not save the rankings: "+err.Error())
			} else {
				rep.Saved = true
			}
		}
	}
	tbl = tbl.With(o.Settings)
	rep.FetchedAt = tbl.FetchedAt
	rep.Warnings = append(rep.Warnings, tbl.Warnings...)
	rep.Candidates = tbl.Candidates(RankableRunners)
	rep.Unmapped = tbl.Unmapped()
	return rep
}

// Lines renders the report as text lines, the same for the command line and
// the Workspace view.
func (r Report) Lines() []string {
	var out []string
	if r.NoKey {
		out = append(out, "No Artificial Analysis key. Connect one with:", "  decsr connect artificialanalysis", "")
	}
	if r.Err != nil {
		out = append(out, "! "+r.Err.Error(), "")
	}
	switch {
	case r.Fetched:
		out = append(out, "Fetched "+r.FetchedAt.Local().Format("2006-01-02 15:04")+" and saved to ~/.decsr/rankings.json")
	case !r.FetchedAt.IsZero():
		out = append(out, "Saved rankings from "+r.FetchedAt.Local().Format("2006-01-02 15:04")+" (not refreshed)")
	default:
		out = append(out, "No saved rankings: showing the built-in fallback tiers (no measured numbers)")
	}
	shown := r.showsAAData()
	if shown {
		out = append(out, Attribution)
	}
	out = append(out, "")
	out = append(out, tableLines(r.Candidates, r.Connected)...)
	out = append(out, "", "INTEL intelligence index, $/TASK cost per task, TIME per task, BAL balance score 0-100,", "CONN runner connected. ~ marks estimates; tier = relative fallback tier, no numbers.")
	if r.Requests > 0 {
		out = append(out, fmt.Sprintf("Requests used: %d (free tier: %d a day)", r.Requests, DailyLimit))
	}
	if len(r.Unmapped) > 0 {
		out = append(out, "", fmt.Sprintf("Not mapped to a runner model (%d): add routing.aliases to use them", len(r.Unmapped)))
		out = append(out, wrapJoin(r.Unmapped, ", ", 70, "  ")...)
	}
	for _, w := range r.Warnings {
		out = append(out, "", "! "+w)
	}
	if shown {
		out = append(out, "", Attribution)
	}
	return out
}

// showsAAData reports whether any row of the report has Artificial Analysis
// numbers (measured or estimated from them), as opposed to the fallback tiers.
func (r Report) showsAAData() bool {
	for _, c := range r.Candidates {
		if c.Source == SourceMeasured || c.Source == SourceEstimate {
			return true
		}
	}
	return false
}

func tableLines(cs []Candidate, connected map[string]bool) []string {
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 1, 1, ' ', 0)
	_, _ = fmt.Fprintln(tw, "MODEL\tEFFORT\tRUNNER\tINTEL\t$/TASK\tTIME\tBAL\tDATA\tCONN")
	for _, c := range cs {
		mark, src := "", "measured"
		if c.Estimated {
			mark, src = "~", "~est"
			if c.Source == SourceFallback {
				src = "~tier"
			}
		}
		effort := c.Effort
		if effort == "" {
			effort = "-"
		}
		conn := "no"
		if connected[c.Runner] {
			conn = "yes"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%.0f\t%s\t%s\n", c.Name, effort, c.Runner,
			fnum(mark, c.Intelligence, "%.1f", c.Source == SourceFallback), fnum(mark, c.CostPerTask, "$%.2f", c.Source == SourceFallback),
			fnum(mark, c.TimePerTask, "%.0fs", c.Source == SourceFallback), c.Balance, src, conn)
	}
	_ = tw.Flush()
	return strings.Split(strings.TrimRight(sb.String(), "\n"), "\n")
}

// fnum formats a number; fallback tiers are relative, so they print as "tier".
func fnum(mark string, v float64, format string, tier bool) string {
	switch {
	case tier:
		return "tier"
	case v <= 0:
		return "-"
	}
	return mark + fmt.Sprintf(format, v)
}

func wrapJoin(items []string, sep string, width int, indent string) []string {
	var lines []string
	cur := indent
	for i, it := range items {
		piece := it
		if i < len(items)-1 {
			piece += strings.TrimRight(sep, " ")
		}
		if len(cur)+len(piece) > width && strings.TrimSpace(cur) != "" {
			lines = append(lines, strings.TrimRight(cur, " "))
			cur = indent
		}
		cur += piece + " "
	}
	return append(lines, strings.TrimRight(cur, " "))
}
