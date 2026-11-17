// Package ranking holds the model rankings used to pick an agent per node:
// the Artificial Analysis client, the (runner, model, effort) candidates built
// from it, the effort estimates, the built-in fallback table and the balance
// score. Fetching happens only when Run or Fetch is called.
//
// Data: Artificial Analysis (artificialanalysis.ai)
package ranking

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/safefile"
)

const (
	// Attribution must be shown wherever the data is shown.
	Attribution = "Data: Artificial Analysis (artificialanalysis.ai)"
	// CredName is the credentials entry of the AA key.
	CredName = credentials.ArtificialAnalysis
	// KeyEnv is the environment variable checked before the saved key.
	KeyEnv = "ARTIFICIAL_ANALYSIS_API_KEY"
	// DefaultBaseURL is the AA API root.
	DefaultBaseURL = "https://artificialanalysis.ai/api/v2"
	// FileName is the saved table inside ~/.decsr.
	FileName = "rankings.json"
	// DailyLimit is the free-tier request allowance per day.
	DailyLimit = 100

	tableVersion = 1
)

// RankableRunners are the runners the table can rank.
var RankableRunners = []string{"claude", "codex", "dsh", "deepseek", "openai"}

// Target is a runner and the model id it takes.
type Target struct{ Runner, Model string }

// Tier is a relative-tier entry of the fallback table. It holds no AA
// numbers: only a relative intelligence (0-100) and relative cost and time
// per task, all for the highest effort.
type Tier struct {
	Intelligence float64 `json:"intelligence"`
	Cost         float64 `json:"cost"`
	Time         float64 `json:"time"`
	Context      int     `json:"context,omitempty"`
}

// Weights are the shares of the balance score. All zero means the defaults.
type Weights struct {
	Intelligence float64 `json:"intelligence"`
	Cost         float64 `json:"cost"`
	Time         float64 `json:"time"`
	Context      float64 `json:"context"`
}

// DefaultWeights is 50% intelligence, 30% cost, 10% time, 10% context.
var DefaultWeights = Weights{Intelligence: 50, Cost: 30, Time: 10, Context: 10}

// Settings are the user's overrides, usually built with FromConfig.
type Settings struct {
	Aliases  map[string][]Target // keyed by the normalized AA name
	Weights  Weights
	Fallback map[string]Tier // keyed "runner/model" or "model"
}

// FromConfig reads the routing section of the config.
func FromConfig(c config.Config) Settings {
	s := Settings{
		Weights: Weights{
			Intelligence: c.Routing.Weights.Intelligence, Cost: c.Routing.Weights.Cost,
			Time: c.Routing.Weights.Time, Context: c.Routing.Weights.Context,
		},
	}
	if len(c.Routing.Aliases) > 0 {
		s.Aliases = map[string][]Target{}
		for name, v := range c.Routing.Aliases {
			if ts := parseTargets(v); len(ts) > 0 {
				s.Aliases[Normalize(name)] = ts
			}
		}
	}
	if len(c.Routing.Fallback) > 0 {
		s.Fallback = map[string]Tier{}
		for k, v := range c.Routing.Fallback {
			s.Fallback[k] = Tier{Intelligence: v.Intelligence, Cost: v.Cost, Time: v.Time, Context: v.Context}
		}
	}
	return s
}

// parseTargets reads "runner/model[,runner/model]".
func parseTargets(v string) []Target {
	var out []Target
	for _, part := range strings.Split(v, ",") {
		r, m, ok := strings.Cut(strings.TrimSpace(part), "/")
		if ok && r != "" && m != "" {
			out = append(out, Target{Runner: r, Model: m})
		}
	}
	return out
}

// Row is one AA entry: a model at one effort. Effort is "" when the entry
// names none. A value of 0 means unknown.
type Row struct {
	Name         string  `json:"name"`
	Base         string  `json:"base"`
	Slug         string  `json:"slug,omitempty"`
	Creator      string  `json:"creator,omitempty"`
	Effort       string  `json:"effort,omitempty"`
	Intelligence float64 `json:"intelligence,omitempty"`
	CostPerTask  float64 `json:"cost_per_task,omitempty"`
	TimePerTask  float64 `json:"time_per_task,omitempty"`
	Context      int     `json:"context,omitempty"`
}

// Table is the saved AA data plus the settings used to read it.
type Table struct {
	Version   int       `json:"version"`
	FetchedAt time.Time `json:"fetched_at"`
	Requests  int       `json:"requests"`
	Warnings  []string  `json:"warnings,omitempty"`
	Rows      []Row     `json:"rows"`

	set Settings
}

// With returns the table reading its data with the given settings.
func (t Table) With(s Settings) Table { t.set = s; return t }

// Path is ~/.decsr/rankings.json ("" when the home is unknown).
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, credentials.Dirname, FileName)
}

// Load reads the saved table. A missing file is an empty table, so the
// fallback table applies.
func Load() (Table, error) { return LoadFrom(Path()) }

// LoadFrom reads a table from path.
func LoadFrom(path string) (Table, error) {
	t := Table{Version: tableVersion}
	if path == "" {
		return t, errors.New("ranking: home directory unknown")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- fixed file under the user's directory
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, fmt.Errorf("ranking: read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return Table{Version: tableVersion}, fmt.Errorf("ranking: %s is not valid JSON", path)
	}
	return t, nil
}

// Save writes the table to ~/.decsr/rankings.json.
func (t Table) Save() error { return t.SaveTo(Path()) }

// SaveTo writes the table to path.
func (t Table) SaveTo(path string) error {
	if path == "" {
		return errors.New("ranking: home directory unknown")
	}
	t.Version = tableVersion
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return safefile.WriteFile(dir, filepath.Base(path), append(data, '\n'), 0o600)
}

// Unmapped lists the AA model names that map to no runner model.
func (t Table) Unmapped() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range t.Rows {
		k := Normalize(r.Base)
		if seen[k] {
			continue
		}
		seen[k] = true
		if len(t.targets(k)) == 0 {
			out = append(out, r.Base)
		}
	}
	sort.Strings(out)
	return out
}

// targets are the runner models an AA name (normalized) maps to; the user's
// alias wins over the built-in one.
func (t Table) targets(norm string) []Target {
	if ts, ok := t.set.Aliases[norm]; ok {
		return ts
	}
	return builtinAliases[norm]
}
