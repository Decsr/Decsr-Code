package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

const (
	deepSeekBaseURL   = "https://api.deepseek.com"
	deepSeekKeyEnv    = "DEEPSEEK_API_KEY"
	deepSeekModel     = "deepseek-v4-flash"
	apiErrBodyMaxSize = 500
)

// deepSeekFallbackModels is shown in setup when the models endpoint is
// unreachable.
var deepSeekFallbackModels = []string{"deepseek-v4-flash", "deepseek-v4-pro"}

// OpenAIRunner calls any OpenAI-compatible chat completions API in text mode:
// the prompt goes in as-is and plain text comes back (no tools, no agent loop).
// Provider ("openai" or "deepseek") names the runner in errors. The API key is
// read from the environment variable named APIKeyEnv and is never stored.
type OpenAIRunner struct {
	Provider  string
	Model     string
	BaseURL   string
	APIKeyEnv string
}

// apiKey returns the key from the configured env var, or an error naming it.
func (r *OpenAIRunner) apiKey() (string, error) {
	return apiKeyFromEnv(r.Provider, r.APIKeyEnv)
}

func apiKeyFromEnv(provider, envName string) (string, error) {
	if envName == "" {
		return "", fmt.Errorf("%s: api_key_env is not set — set it to the name of the environment variable holding the API key", provider)
	}
	key := os.Getenv(envName)
	if key == "" {
		return "", fmt.Errorf("%s: API key not set — export %s", provider, envName)
	}
	return key, nil
}

func (r *OpenAIRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = r.Model
	}
	if model == "" {
		return nil, fmt.Errorf("%s: model is not set — set `model` for this agent", r.Provider)
	}
	if strings.TrimSpace(r.BaseURL) == "" {
		return nil, fmt.Errorf("%s: base_url is not set — set `base_url` for this agent (e.g. https://openrouter.ai/api/v1)", r.Provider)
	}
	key, err := r.apiKey()
	if err != nil {
		return nil, err
	}

	body := openAIChatRequest{
		Model:         model,
		Messages:      r.buildMessages(req),
		Stream:        true,
		StreamOptions: &openAIStreamOptions{IncludeUsage: true},
	}
	if effort := NormalizeEffort(req.Effort); effort != "" {
		body.ReasoningEffort = mapOpenAIReasoningEffort(effort)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal request: %w", r.Provider, err)
	}

	endpoint := strings.TrimRight(r.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", r.Provider, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+key)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: request to %s failed: %w", r.Provider, endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		bodyStr := strings.TrimSpace(string(errBody))
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, &RateLimitError{Provider: r.Provider, Detail: bodyStr}
		}
		if rl := ClassifyRateLimit(r.Provider, bodyStr); rl != nil {
			return nil, rl
		}
		return nil, fmt.Errorf("%s: model %q returned HTTP %d: %s", r.Provider, model, resp.StatusCode, truncate(bodyStr, apiErrBodyMaxSize))
	}

	ch := make(chan Token, 16)
	go func() {
		defer func() { _ = resp.Body.Close() }()
		r.streamFromReader(resp.Body, joinPromptText(req), ch)
	}()
	return ch, nil
}

func (r *OpenAIRunner) buildMessages(req CompletionRequest) []openAIChatMessage {
	var msgs []openAIChatMessage
	if req.SystemPrompt != "" {
		msgs = append(msgs, openAIChatMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, openAIChatMessage(m))
	}
	return msgs
}

// streamFromReader parses an OpenAI-style SSE stream, emitting content deltas
// as Tokens and reasoning deltas as display-only Token.Reasoning.
func (r *OpenAIRunner) streamFromReader(reader io.Reader, inputText string, ch chan<- Token) {
	defer close(ch)

	var usage *TokenUsage
	contentLen := 0
	reasoningLen := 0

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk openAIChatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- Token{Error: fmt.Errorf("%s: decode stream chunk: %w", r.Provider, err)}
			ch <- Token{Done: true}
			return
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				ch <- Token{Text: c.Delta.Content}
				contentLen += len(c.Delta.Content)
			}
			if c.Delta.Reasoning != "" {
				ch <- Token{Reasoning: c.Delta.Reasoning}
				reasoningLen += len(c.Delta.Reasoning)
			}
		}
		if chunk.Usage != nil {
			usage = &TokenUsage{
				InputTokens:  chunk.Usage.PromptTokens,
				OutputTokens: chunk.Usage.CompletionTokens,
			}
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- Token{Error: fmt.Errorf("%s: read stream: %w", r.Provider, err)}
		ch <- Token{Done: true}
		return
	}

	if contentLen == 0 {
		promptTokens := tokenutil.EstimateTokens(inputText)
		if reasoningLen > 0 {
			ch <- Token{Error: fmt.Errorf("%s: model spent its entire response on reasoning and produced no answer for a ~%d-token prompt — try a lower effort, a shorter prompt or a non-reasoning model", r.Provider, promptTokens)}
		} else {
			ch <- Token{Error: fmt.Errorf("%s: model returned an empty answer for a ~%d-token prompt", r.Provider, promptTokens)}
		}
		ch <- Token{Done: true}
		return
	}

	if usage == nil {
		usage = estimatedUsage(inputText, strings.Repeat("x", contentLen))
	}
	ch <- Token{Done: true, Usage: usage}
}

type openAIChatRequest struct {
	Model           string               `json:"model"`
	Messages        []openAIChatMessage  `json:"messages"`
	Stream          bool                 `json:"stream"`
	StreamOptions   *openAIStreamOptions `json:"stream_options,omitempty"`
	ReasoningEffort string               `json:"reasoning_effort,omitempty"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// DeepSeekFallbackModels returns the static DeepSeek model list.
func DeepSeekFallbackModels() []string {
	return append([]string(nil), deepSeekFallbackModels...)
}

// DeepSeekListModels returns the model IDs offered by the DeepSeek API. It
// falls back to a static list when the key is missing or the request fails.
func DeepSeekListModels() ([]string, error) {
	fallback := DeepSeekFallbackModels()
	key, err := apiKeyFromEnv("deepseek", deepSeekKeyEnv)
	if err != nil {
		return fallback, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, deepSeekBaseURL+"/models", nil)
	if err != nil {
		return fallback, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fallback, fmt.Errorf("deepseek not reachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fallback, fmt.Errorf("deepseek /models returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fallback, err
	}
	var names []string
	for _, m := range result.Data {
		names = append(names, m.ID)
	}
	if len(names) == 0 {
		return fallback, nil
	}
	return names, nil
}
