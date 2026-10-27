package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

func TestBudgetRunner_PassesThroughUsage(t *testing.T) {
	inner := &MockRunner{
		Responses: []string{"response text"},
		MockUsage: &TokenUsage{InputTokens: 100, OutputTokens: 50},
	}
	budget := &BudgetRunner{inner: inner, maxTokens: 10000}

	ch, err := budget.Complete(context.Background(), CompletionRequest{
		SystemPrompt: "system",
		Messages:     []ConvMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var done Token
	for tok := range ch {
		if tok.Done {
			done = tok
		}
	}

	if done.Usage == nil {
		t.Fatal("BudgetRunner should pass through Usage from inner runner")
	}
	if done.Usage.InputTokens != 100 || done.Usage.OutputTokens != 50 {
		t.Errorf("unexpected usage: %+v", done.Usage)
	}
}

func TestBudgetRunner_NeverExceedsBudgetAcrossMessages(t *testing.T) {
	inner := &MockRunner{Responses: []string{"ok"}}
	budget := &BudgetRunner{inner: inner, maxTokens: 1000}
	big := strings.Repeat("word ", 4000)
	req := CompletionRequest{Messages: []ConvMessage{
		{Role: "user", Content: big},
		{Role: "assistant", Content: big},
		{Role: "user", Content: big},
	}}
	var got CompletionRequest
	capture := &captureRunner{onComplete: func(r CompletionRequest) { got = r }}
	budget.inner = capture
	if _, err := budget.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, m := range got.Messages {
		total += tokenutil.EstimateTokens(m.Content)
	}
	// 800 token message budget plus a small allowance for truncation markers.
	if total > 800+50 {
		t.Errorf("messages total %d tokens, exceeds budget", total)
	}
}

type captureRunner struct{ onComplete func(CompletionRequest) }

func (c *captureRunner) Complete(_ context.Context, req CompletionRequest) (<-chan Token, error) {
	c.onComplete(req)
	ch := make(chan Token)
	close(ch)
	return ch, nil
}

func TestBudgetRunner_KeepsLastMessageAndOrder(t *testing.T) {
	var got CompletionRequest
	budget := &BudgetRunner{inner: &captureRunner{onComplete: func(r CompletionRequest) { got = r }}, maxTokens: 1000}
	_, err := budget.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{
		{Role: "user", Content: strings.Repeat("old ", 5000)},
		{Role: "assistant", Content: "middle answer"},
		{Role: "user", Content: "current question"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	n := len(got.Messages)
	if n < 2 || got.Messages[n-1].Content != "current question" || got.Messages[n-2].Content != "middle answer" {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
	if n == 3 && got.Messages[0].Role != "user" {
		t.Errorf("order not preserved: %+v", got.Messages)
	}
}

func TestBudgetRunner_HugeLastMessageTruncatedNotDropped(t *testing.T) {
	var got CompletionRequest
	budget := &BudgetRunner{inner: &captureRunner{onComplete: func(r CompletionRequest) { got = r }}, maxTokens: 1000}
	_, _ = budget.Complete(context.Background(), CompletionRequest{Messages: []ConvMessage{
		{Role: "user", Content: "first"},
		{Role: "user", Content: strings.Repeat("big ", 5000)},
	}})
	if len(got.Messages) != 1 || got.Messages[0].Content == "" || tokenutil.EstimateTokens(got.Messages[0].Content) > 850 {
		t.Fatalf("unexpected: %d msgs", len(got.Messages))
	}
}
