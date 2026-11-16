// Package pricing turns token counts into USD from the prices the user
// configured. There are no built-in prices: they go stale.
package pricing

import (
	"path"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// IsLocal reports whether the runner runs on the user's machine; its cost is
// always zero.
func IsLocal(runner string) bool {
	switch runner {
	case "ollama", "lmstudio", "mlx":
		return true
	}
	return false
}

// Lookup returns the configured price of model: the exact key first, then the
// matching glob with the most characters (ties go to the smaller key).
func Lookup(cfg config.Config, model string) (config.ModelPrice, bool) {
	if p, ok := cfg.Pricing[model]; ok {
		return p, true
	}
	best, found := "", false
	for key := range cfg.Pricing {
		if ok, err := path.Match(key, model); err != nil || !ok {
			continue
		}
		if !found || len(key) > len(best) || (len(key) == len(best) && key < best) {
			best, found = key, true
		}
	}
	if !found {
		return config.ModelPrice{}, false
	}
	return cfg.Pricing[best], true
}

// Cost is the USD cost of in input and out output tokens of model on runner.
// known is false when no price is configured for the model. Local runners cost
// $0 and are always known.
func Cost(cfg config.Config, runner, model string, in, out int) (usd float64, known bool) {
	if IsLocal(runner) {
		return 0, true
	}
	p, ok := Lookup(cfg, model)
	if !ok {
		return 0, false
	}
	return (float64(in)*p.InputPerMTok + float64(out)*p.OutputPerMTok) / 1e6, true
}
