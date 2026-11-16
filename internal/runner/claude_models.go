package runner

import "strings"

// ClaudeDefaultModel and ClaudeDefaultEffort are what the claude runner uses
// when a role sets no model or effort.
const (
	ClaudeDefaultModel  = "claude-opus-5-5"
	ClaudeDefaultEffort = "high"
)

// EffectiveModel is the model the runner really uses: for claude an empty model
// becomes ClaudeDefaultModel; other runners keep their value unchanged.
func EffectiveModel(runnerName, model string) string {
	if runnerName == "claude" && strings.TrimSpace(model) == "" {
		return ClaudeDefaultModel
	}
	return model
}

// EffectiveEffort is the effort the runner really uses: for claude an empty or
// "default" effort becomes ClaudeDefaultEffort; other runners keep their value.
func EffectiveEffort(runnerName, effort string) string {
	if runnerName == "claude" {
		if e := NormalizeEffort(effort); e != "" {
			return e
		}
		return ClaudeDefaultEffort
	}
	return effort
}

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
