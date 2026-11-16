package runner

// Preset is a hosted OpenAI-compatible provider. Its Name is also a valid
// `runner:` value; base_url and api_key_env in the config still override it.
type Preset struct {
	Name         string
	Label        string
	BaseURL      string
	KeyEnv       string
	DefaultModel string // "" = no default; the user picks one
}

var presets = []Preset{
	{Name: "deepseek", Label: "DeepSeek", BaseURL: "https://api.deepseek.com", KeyEnv: "DEEPSEEK_API_KEY", DefaultModel: "deepseek-v4-flash"},
	{Name: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1", KeyEnv: "OPENAI_API_KEY"},
	{Name: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1", KeyEnv: "OPENROUTER_API_KEY"},
	{Name: "groq", Label: "Groq", BaseURL: "https://api.groq.com/openai/v1", KeyEnv: "GROQ_API_KEY"},
}

// deepSeekFallbackModels is shown in setup when the models endpoint is
// unreachable.
var deepSeekFallbackModels = []string{"deepseek-v4-flash", "deepseek-v4-pro"}

// Presets returns the provider presets in display order.
func Presets() []Preset { return append([]Preset(nil), presets...) }

// PresetByName finds a preset by its name.
func PresetByName(name string) (Preset, bool) {
	for _, p := range presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

// IsPreset reports whether name is a provider preset.
func IsPreset(name string) bool {
	_, ok := PresetByName(name)
	return ok
}

// DeepSeekFallbackModels returns the static DeepSeek model list.
func DeepSeekFallbackModels() []string {
	return append([]string(nil), deepSeekFallbackModels...)
}
