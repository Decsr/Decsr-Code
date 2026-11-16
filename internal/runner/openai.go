package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

const apiErrBodyMaxSize = 500

// OpenAIRunner calls any OpenAI-compatible chat completions API in text mode:
// the prompt goes in as-is and plain text comes back (no tools, no agent loop).
// Provider names the runner in errors. The API key comes from the environment
// variable named APIKeyEnv, else from the saved credential CredName (default:
// Provider); it is never part of the config. When BaseURL is empty, a saved
// custom provider's base URL is used.
type OpenAIRunner struct {
	Provider  string
	Model     string
	BaseURL   string
	APIKeyEnv string
	CredName  string
}

func (r *OpenAIRunner) credName() string {
	if r.CredName != "" {
		return r.CredName
	}
	return r.Provider
}

// apiKey returns the key from the env var or the saved credential.
func (r *OpenAIRunner) apiKey() (string, error) {
	key, _, err := ResolveKey(r.credName(), r.APIKeyEnv)
	return key, err
}

// baseURL is the configured endpoint, else the saved provider's.
func (r *OpenAIRunner) baseURL() string {
	if strings.TrimSpace(r.BaseURL) != "" {
		return r.BaseURL
	}
	if c, ok, err := credentials.Get(r.credName()); err == nil && ok {
		return c.BaseURL
	}
	return ""
}

func (r *OpenAIRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	model := req.Model
	if model == "" {
		model = r.Model
	}
	if model == "" {
		return nil, fmt.Errorf("%s: model is not set — set `model` for this agent", r.Provider)
	}
	base := r.baseURL()
	if strings.TrimSpace(base) == "" {
		return nil, fmt.Errorf("%s: base_url is not set — set `base_url` for this agent (e.g. https://openrouter.ai/api/v1) or run \"decsr connect custom\"", r.Provider)
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

	endpoint := strings.TrimRight(base, "/") + "/chat/completions"
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
		if rl := rateLimitFromHTTP(r.Provider, resp.StatusCode, resp.Header, bodyStr); rl != nil {
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
	var think thinkingTracker

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
			emitActivity(ch, think.finish(time.Now()))
			ch <- Token{Error: fmt.Errorf("%s: decode stream chunk: %w", r.Provider, err)}
			ch <- Token{Done: true}
			return
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				emitActivity(ch, think.finish(time.Now()))
				ch <- Token{Text: c.Delta.Content}
				contentLen += len(c.Delta.Content)
			}
			if c.Delta.Reasoning != "" {
				ch <- Token{Reasoning: c.Delta.Reasoning}
				emitActivity(ch, think.add(c.Delta.Reasoning, time.Now()))
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

	emitActivity(ch, think.finish(time.Now()))
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
