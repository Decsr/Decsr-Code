package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ClaudeModels lists the values the Claude CLI --model flag is offered with:
// the aliases for the latest model of each family first, then full model
// names. Update this list when new models ship.
var ClaudeModels = []string{
	"fable",
	"opus",
	"sonnet",
	"haiku",
	"claude-fable-5-2",
	"claude-opus-5-5",
	"claude-sonnet-5-5",
	"claude-haiku-5-5",
}

// ClaudeListModels returns the known Claude model aliases and names.
func ClaudeListModels() ([]string, error) {
	return ClaudeModels, nil
}

// ClaudeModelHint describes a model alias, such as "latest Opus". It is empty
// for a full model name.
func ClaudeModelHint(model string) string {
	switch model {
	case "fable", "opus", "sonnet", "haiku":
		return "latest " + strings.ToUpper(model[:1]) + model[1:]
	}
	return ""
}

// ClaudeSettingsModel returns the "model" value of the user's Claude settings
// file ($HOME/.claude/settings.json) and whether one is set.
func ClaudeSettingsModel() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return ClaudeSettingsModelIn(home)
}

// ClaudeSettingsModelIn is ClaudeSettingsModel for an explicit home directory.
func ClaudeSettingsModelIn(home string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return "", false
	}
	var cfg struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return "", false
	}
	model := strings.TrimSpace(cfg.Model)
	return model, model != ""
}
