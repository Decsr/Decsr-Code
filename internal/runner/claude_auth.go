package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude auth modes. A subscription login is the one the claude CLI keeps
// after an interactive sign-in; an API key (or a cloud provider) is metered
// API use.
const (
	AuthAPIKey       = "api-key"
	AuthSubscription = "subscription"
)

// claudeAPIEnv are the variables whose presence means API use.
var claudeAPIEnv = []string{
	"ANTHROPIC_API_KEY",
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY",
}

// ClaudeAuthMode reports how the claude CLI will authenticate: AuthAPIKey when
// an API key, a cloud provider or an apiKeyHelper is configured, else
// AuthSubscription. Only the presence of a setting is read, never a secret.
func ClaudeAuthMode() string {
	return ClaudeAuthModeFrom(os.Getenv, claudeSettingsPaths())
}

// claudeSettingsPaths are the claude settings files that can set apiKeyHelper.
func claudeSettingsPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		paths = append(paths,
			filepath.Join(cwd, ".claude", "settings.json"),
			filepath.Join(cwd, ".claude", "settings.local.json"))
	}
	return paths
}

// ClaudeAuthModeFrom is ClaudeAuthMode with the environment and the settings
// files given, for tests.
func ClaudeAuthModeFrom(getenv func(string) string, settingsPaths []string) string {
	for _, k := range claudeAPIEnv {
		if strings.TrimSpace(getenv(k)) != "" {
			return AuthAPIKey
		}
	}
	for _, p := range settingsPaths {
		if claudeSettingsHaveKeyHelper(p) {
			return AuthAPIKey
		}
	}
	return AuthSubscription
}

// claudeSettingsHaveKeyHelper is true when the settings file names an
// apiKeyHelper. Nothing else of the file is kept.
func claudeSettingsHaveKeyHelper(path string) bool {
	data, err := os.ReadFile(path) // #nosec G304 -- fixed claude settings paths
	if err != nil {
		return false
	}
	var s struct {
		APIKeyHelper string `json:"apiKeyHelper"`
	}
	if json.Unmarshal(data, &s) != nil {
		return false
	}
	return strings.TrimSpace(s.APIKeyHelper) != ""
}
