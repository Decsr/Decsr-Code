package runner

import (
	"time"

	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

// liveUsageInterval is the shortest time between two live estimates.
const liveUsageInterval = time.Second

// liveUsage estimates the usage of a streaming HTTP call from the text seen so
// far and sends it as Token.Progress, at most once per interval.
type liveUsage struct {
	in       int
	outBytes int
	last     time.Time
	now      func() time.Time
}

func newLiveUsage(inputText string) *liveUsage {
	return &liveUsage{in: tokenutil.EstimateTokens(inputText), now: time.Now}
}

// add counts streamed output text (answer or reasoning) and sends an estimate
// when the interval has passed; the first call always sends one.
func (l *liveUsage) add(text string, ch chan<- Token) {
	l.outBytes += len(text)
	now := l.now()
	if !l.last.IsZero() && now.Sub(l.last) < liveUsageInterval {
		return
	}
	l.last = now
	ch <- Token{Progress: &TokenUsage{
		InputTokens:  l.in,
		OutputTokens: tokenutil.EstimateTokensForLen(l.outBytes),
		Estimated:    true,
	}}
}
