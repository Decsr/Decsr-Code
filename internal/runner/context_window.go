package runner

import "strings"

const (
	// defaultContextWindow is the size assumed for a model whose window is not
	// known.
	defaultContextWindow = 128000
	// claudeContextWindow is the window of a Claude model; the 1M variants
	// (a "1m" in the model name) are larger.
	claudeContextWindow   = 200000
	claudeContextWindow1M = 1000000
)

// ContextWindow is the number of tokens the model can hold in one
// conversation: 200K for claude (1M for a "1m" model), 128K for anything else.
func ContextWindow(runnerName, model string) int {
	if strings.EqualFold(runnerName, "claude") {
		if strings.Contains(strings.ToLower(model), "1m") {
			return claudeContextWindow1M
		}
		return claudeContextWindow
	}
	return defaultContextWindow
}
