package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Provider effort vocabularies. These are the levels accepted by the
// corresponding CLI/server; model-specific discovery overrides them where the
// backend exposes model metadata.
var (
	claudeEfforts      = []string{"low", "medium", "high", "xhigh", "max"}
	codexEfforts       = []string{"minimal", "low", "medium", "high"}
	copilotEfforts     = []string{"low", "medium", "high", "xhigh", "max"}
	openCodeFallbacks  = []string{"low", "medium", "high"}
	lmStudioEfforts    = []string{"low", "medium", "high", "xhigh"}
	mlxEfforts         = []string{"low", "medium", "high"}
	effortHTTPTimeout  = 3 * time.Second
	openCodeCacheName  = "models.json"
	openCodeCacheDir   = "opencode"
	lmStudioModelsURL  = lmStudioBaseURL + "/api/v0/models"
	ollamaShowEndpoint = ollamaBaseURL + "/api/show"
)

// NormalizeEffort trims and lowercases an effort value. The config sentinel
// "default" means "provider default" and normalizes to the empty string.
func NormalizeEffort(effort string) string {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "default" {
		return ""
	}
	return effort
}

// EffortOptions returns the reasoning-effort levels the given provider/model
// supports. An empty result means the model exposes no effort control.
func EffortOptions(provider, model string) []string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}

	switch provider {
	case "claude":
		return append([]string(nil), claudeEfforts...)
	case "codex":
		return append([]string(nil), codexEfforts...)
	case "copilot":
		if opts, found := openCodeCacheEfforts("github-copilot", model); found {
			return filterEfforts(opts, copilotEfforts)
		}
		return append([]string(nil), copilotEfforts...)
	case "opencode", "":
		return openCodeEfforts(model)
	case "ollama":
		return ollamaEfforts(model)
	case "lmstudio":
		return lmStudioEffortsFor(model)
	case "mlx":
		return append([]string(nil), mlxEfforts...)
	default:
		return nil
	}
}

func openCodeEfforts(model string) []string {
	if strings.Contains(model, "#") || !strings.Contains(model, "/") || strings.HasPrefix(model, "ollama/") {
		return nil
	}
	if opts, found := openCodeCacheEfforts("", model); found {
		return opts
	}
	return append([]string(nil), openCodeFallbacks...)
}

// openCodeCacheEfforts reads ~/.cache/opencode/models.json. found reports
// whether the model exists in the cache, even when it advertises no effort.
func openCodeCacheEfforts(provider, model string) (options []string, found bool) {
	modelID := model
	if provider == "" {
		slash := strings.Index(model, "/")
		if slash < 0 {
			return nil, false
		}
		provider = model[:slash]
		modelID = model[slash+1:]
	}
	if strings.Contains(modelID, "#") {
		return nil, false
	}

	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(cacheDir, openCodeCacheDir, openCodeCacheName)) // #nosec G304 -- fixed cache path under the user cache dir
	if err != nil {
		return nil, false
	}

	var cache map[string]struct {
		Models map[string]struct {
			ReasoningOptions []struct {
				Type   string   `json:"type"`
				Values []string `json:"values"`
			} `json:"reasoning_options"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, false
	}
	p, ok := cache[provider]
	if !ok {
		return nil, false
	}
	m, ok := p.Models[modelID]
	if !ok {
		return nil, false
	}
	for _, opt := range m.ReasoningOptions {
		if opt.Type != "effort" {
			continue
		}
		if values := cleanEffortValues(opt.Values); len(values) > 0 {
			return values, true
		}
	}
	return nil, true
}

// ollamaEfforts asks /api/show which thinking values the model advertises.
func ollamaEfforts(model string) []string {
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return nil
	}
	req, err := http.NewRequest(http.MethodPost, ollamaShowEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: effortHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload struct {
		Thinking *struct {
			Values []any `json:"values"`
		} `json:"thinking"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.Thinking == nil {
		return nil
	}
	var out []string
	for _, raw := range payload.Thinking.Values {
		switch v := raw.(type) {
		case string:
			if v != "" && v != "default" {
				out = append(out, v)
			}
		case bool:
			if v {
				out = append(out, "on")
			} else {
				out = append(out, "off")
			}
		}
	}
	out = dedupeEfforts(out)
	if len(out) == 1 && out[0] == "off" {
		return nil
	}
	return out
}

// lmStudioEffortsFor inspects the model's capabilities when the server exposes
// them; older LM Studio builds omit capability metadata and get the fallback.
func lmStudioEffortsFor(model string) []string {
	client := &http.Client{Timeout: effortHTTPTimeout}
	resp, err := client.Get(lmStudioModelsURL) // #nosec G107 -- fixed localhost LM Studio endpoint
	if err != nil {
		return append([]string(nil), lmStudioEfforts...)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return append([]string(nil), lmStudioEfforts...)
	}
	var payload struct {
		Data []struct {
			ID           string   `json:"id"`
			Capabilities []string `json:"capabilities"`
			Reasoning    any      `json:"reasoning"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return append([]string(nil), lmStudioEfforts...)
	}
	for _, m := range payload.Data {
		if m.ID != model {
			continue
		}
		if len(m.Capabilities) == 0 {
			return append([]string(nil), lmStudioEfforts...)
		}
		for _, c := range m.Capabilities {
			if c == "reasoning" || c == "thinking" {
				return append([]string(nil), lmStudioEfforts...)
			}
		}
		if m.Reasoning != nil {
			return append([]string(nil), lmStudioEfforts...)
		}
		return nil
	}
	return append([]string(nil), lmStudioEfforts...)
}

func cleanEffortValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || v == "default" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func dedupeEfforts(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func filterEfforts(values, allowed []string) []string {
	ok := make(map[string]bool, len(allowed))
	for _, v := range allowed {
		ok[v] = true
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if ok[v] {
			out = append(out, v)
		}
	}
	return out
}

func mapClaudeEffort(effort string) string {
	switch effort {
	case "minimal", "none":
		return "low"
	default:
		return effort
	}
}

func mapCodexEffort(effort string) string {
	switch effort {
	case "none":
		return "minimal"
	case "xhigh", "max":
		return "high"
	default:
		return effort
	}
}

func mapCopilotEffort(effort string) string {
	switch effort {
	case "minimal", "none":
		return "low"
	default:
		return effort
	}
}

func ollamaThinkValue(effort string) any {
	switch effort {
	case "off":
		return false
	case "on":
		return true
	default:
		return effort
	}
}

func mapOpenAIReasoningEffort(effort string) string {
	switch effort {
	case "max":
		return "xhigh"
	default:
		return effort
	}
}
