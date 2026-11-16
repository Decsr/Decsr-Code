package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/credentials"
)

// Key sources reported by KeySource.
const (
	KeySourceSaved = "saved (" + credentials.DisplayPath + ")"
	envSourcePfx   = "env "
)

// effectiveEnv is the env var checked for a provider: the configured one, else
// the preset's.
func effectiveEnv(credName, envName string) string {
	if envName != "" {
		return envName
	}
	if p, ok := PresetByName(credName); ok {
		return p.KeyEnv
	}
	return ""
}

// ResolveKey finds the API key for a provider: the env var first, then the
// saved credential named credName. source says where it came from. The key is
// never part of an error.
func ResolveKey(credName, envName string) (key, source string, err error) {
	env := effectiveEnv(credName, envName)
	if env != "" {
		if v := os.Getenv(env); v != "" {
			return v, envSourcePfx + env, nil
		}
	}
	c, ok, cerr := credentials.Get(credName)
	if cerr != nil {
		return "", "", fmt.Errorf("%s: %w", credName, cerr)
	}
	if ok {
		return c.APIKey, KeySourceSaved, nil
	}
	connect := credName
	if !IsPreset(credName) {
		connect = "custom"
	}
	if env == "" {
		return "", "", fmt.Errorf("%s: no API key — run \"decsr connect %s\" or set api_key_env to the name of the variable holding it", credName, connect)
	}
	return "", "", fmt.Errorf("%s: no API key — run \"decsr connect %s\" or export %s", credName, connect, env)
}

// KeySource says where the key for a provider comes from: "env NAME",
// "saved (~/.decsr/credentials.json)", or "" when there is none.
func KeySource(credName, envName string) string {
	_, src, err := ResolveKey(credName, envName)
	if err != nil {
		return ""
	}
	return src
}

// KeyRejectedError is returned when the provider answers 401 or 403.
type KeyRejectedError struct{ Status int }

func (e *KeyRejectedError) Error() string { return fmt.Sprintf("key rejected (HTTP %d)", e.Status) }

// ProbeTimeout bounds one /models request made by ProbeModels.
const ProbeTimeout = 10 * time.Second

// ProbeModels calls GET <baseURL>/models with the key and returns the model
// ids. client may be nil. A 401/403 gives a *KeyRejectedError. The key never
// appears in an error.
func ProbeModels(ctx context.Context, client *http.Client, baseURL, key string) ([]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, errors.New("invalid base URL")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		var ue interface{ Unwrap() error }
		if errors.As(err, &ue) && ue.Unwrap() != nil {
			err = ue.Unwrap()
		}
		return nil, fmt.Errorf("%s not reachable: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &KeyRejectedError{Status: resp.StatusCode}
	default:
		return nil, fmt.Errorf("%s/models returned HTTP %d", baseURL, resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("%s/models: unexpected answer", baseURL)
	}
	var names []string
	for _, m := range result.Data {
		names = append(names, m.ID)
	}
	return names, nil
}

// ListModels returns the model ids offered at baseURL for key.
func ListModels(baseURL, key string) ([]string, error) {
	return ProbeModels(context.Background(), nil, baseURL, key)
}

// ListProviderModels lists the models of a preset (or of a saved custom
// provider when baseURL is given), resolving the key like a runner does. Only
// deepseek falls back to a static list when the key is missing or the request
// fails; other providers return nil and the error.
func ListProviderModels(name, baseURL string) ([]string, error) {
	var fallback []string
	if name == "deepseek" {
		fallback = DeepSeekFallbackModels()
	}
	if p, ok := PresetByName(name); ok && baseURL == "" {
		baseURL = p.BaseURL
	}
	if baseURL == "" {
		return fallback, fmt.Errorf("%s: base URL unknown", name)
	}
	key, _, err := ResolveKey(name, "")
	if err != nil {
		return fallback, err
	}
	names, err := ListModels(baseURL, key)
	if err != nil || len(names) == 0 {
		return fallback, err
	}
	return names, nil
}
