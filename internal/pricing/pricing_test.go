package pricing

import (
	"math"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func cfgWith(p map[string]config.ModelPrice) config.Config { return config.Config{Pricing: p} }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostExactBeatsGlob(t *testing.T) {
	c := cfgWith(map[string]config.ModelPrice{
		"deepseek-*":        {InputPerMTok: 1, OutputPerMTok: 2},
		"deepseek-v4-flash": {InputPerMTok: 0.1, OutputPerMTok: 0.2},
	})
	usd, ok := Cost(c, "deepseek", "deepseek-v4-flash", 1_000_000, 1_000_000)
	if !ok || !near(usd, 0.3) {
		t.Fatalf("got %v %v", usd, ok)
	}
}

func TestCostLongestGlob(t *testing.T) {
	c := cfgWith(map[string]config.ModelPrice{
		"*":             {InputPerMTok: 9},
		"openai/*":      {InputPerMTok: 5},
		"openai/gpt-5*": {InputPerMTok: 3},
		"*/gpt-5*":      {InputPerMTok: 4},
	})
	usd, ok := Cost(c, "opencode", "openai/gpt-5.1", 1_000_000, 0)
	if !ok || !near(usd, 3) {
		t.Fatalf("got %v %v", usd, ok)
	}
}

func TestCostLocalIsFree(t *testing.T) {
	c := cfgWith(map[string]config.ModelPrice{"*": {InputPerMTok: 9, OutputPerMTok: 9}})
	for _, r := range []string{"ollama", "lmstudio", "mlx"} {
		usd, ok := Cost(c, r, "qwen", 5000, 5000)
		if !ok || usd != 0 {
			t.Fatalf("%s: got %v %v", r, usd, ok)
		}
	}
}

func TestCostUnknown(t *testing.T) {
	c := cfgWith(map[string]config.ModelPrice{"deepseek-*": {InputPerMTok: 1}})
	for _, r := range []string{"claude", "codex", "copilot", "opencode", ""} {
		if usd, ok := Cost(c, r, "sonnet", 100, 100); ok || usd != 0 {
			t.Fatalf("%q: got %v %v", r, usd, ok)
		}
	}
	if _, ok := Cost(config.Config{}, "deepseek", "deepseek-x", 1, 1); ok {
		t.Fatal("no pricing block must be unknown")
	}
}

func TestCostSubscriptionRunnerPricedWhenConfigured(t *testing.T) {
	c := cfgWith(map[string]config.ModelPrice{"sonnet": {InputPerMTok: 3, OutputPerMTok: 15}})
	usd, ok := Cost(c, "claude", "sonnet", 1_000_000, 1_000_000)
	if !ok || !near(usd, 18) {
		t.Fatalf("got %v %v", usd, ok)
	}
}

func TestCostBadGlobIgnored(t *testing.T) {
	c := cfgWith(map[string]config.ModelPrice{"[": {InputPerMTok: 1}})
	if _, ok := Cost(c, "claude", "x", 1, 1); ok {
		t.Fatal("malformed glob must not match")
	}
}
