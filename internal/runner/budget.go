package runner

import (
	"context"

	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// BudgetRunner wraps an LLMRunner and truncates message content
// to stay within a token budget. Used for cloud runners where tokens have cost.
type BudgetRunner struct {
	inner     LLMRunner
	maxTokens int
}

func (r *BudgetRunner) Complete(ctx context.Context, req CompletionRequest) (<-chan Token, error) {
	// Reserve ~20% of budget for system prompt + skills, rest for messages.
	systemBudget := r.maxTokens / 5
	messageBudget := r.maxTokens - systemBudget

	req.SystemPrompt = tokenutil.Truncate(req.SystemPrompt, systemBudget)

	// Allocate the message budget from the newest message backwards so the
	// current request is always kept; older history is truncated or dropped.
	if n := len(req.Messages); n > 0 {
		kept := make([]ConvMessage, n)
		copy(kept, req.Messages)
		start := n
		for i := n - 1; i >= 0; i-- {
			if i < n-1 && messageBudget <= 0 {
				break
			}
			tokens := tokenutil.EstimateTokens(kept[i].Content)
			if tokens > messageBudget {
				kept[i].Content = tokenutil.Truncate(kept[i].Content, messageBudget)
				messageBudget = 0
				start = i
				if i < n-1 {
					break
				}
				continue
			}
			messageBudget -= tokens
			start = i
		}
		req.Messages = kept[start:]
	}

	return r.inner.Complete(ctx, req)
}
