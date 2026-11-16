package workspace

import (
	"fmt"
)

// CostFunc prices tokens of one model; known is false when no price is
// configured for it.
type CostFunc func(runner, model string, in, out int) (usd float64, known bool)

// Budget is the spending limit the engine enforces.
type Budget struct {
	LimitUSD float64 // 0 = no budget
	WarnPct  int     // 0 = DefaultBudgetWarnPct
	ExtraUSD float64 // known cost outside the nodes, such as the lead's
}

// DefaultBudgetWarnPct is the warn threshold when none is configured.
const DefaultBudgetWarnPct = 80

// WarnShare is the warn threshold as a percentage, defaulted.
func (b Budget) WarnShare() int {
	if b.WarnPct <= 0 || b.WarnPct > 100 {
		return DefaultBudgetWarnPct
	}
	return b.WarnPct
}

// SetCost sets how node usage is priced. Without it no node cost is known.
func (e *Engine) SetCost(f CostFunc) {
	e.mu.Lock()
	e.cost = f
	e.mu.Unlock()
}

// SetBudget sets the budget the engine reads before it starts a node and
// when usage changes.
func (e *Engine) SetBudget(f func() Budget) {
	e.mu.Lock()
	e.budget = f
	e.mu.Unlock()
}

// costBase is the cost of a node's earlier attempts when a new run starts.
type costBase struct {
	usd      float64
	unpriced bool // an earlier attempt used tokens without a known price
}

// SpentUSD is the priced cost of the nodes of g, all attempts added up.
func (g *Graph) SpentUSD() float64 {
	var sum float64
	for _, n := range g.Nodes {
		sum += n.CostUSD
	}
	return sum
}

// BudgetReachedMsg is the message shown when the budget stops new nodes.
func BudgetReachedMsg(spent, limit float64) string {
	return fmt.Sprintf("Budget reached: $%.2f of $%.2f — raise workspace.budget_usd or /budget <amount> to continue", spent, limit)
}

// budgetStateLocked is the budget in force and the known cost so far.
func (e *Engine) budgetStateLocked() (b Budget, spent float64) {
	if e.budget == nil {
		return Budget{}, 0
	}
	b = e.budget()
	return b, e.g.SpentUSD() + b.ExtraUSD
}

// budgetBlockedLocked reports whether no new node may start, with the message.
func (e *Engine) budgetBlockedLocked() (string, bool) {
	b, spent := e.budgetStateLocked()
	if b.LimitUSD > 0 && spent >= b.LimitUSD {
		return BudgetReachedMsg(spent, b.LimitUSD), true
	}
	return "", false
}

// checkBudgetLocked queues the warn and reached events, each once until the
// spending falls below its threshold again (the budget was raised).
func (e *Engine) checkBudgetLocked() {
	b, spent := e.budgetStateLocked()
	if b.LimitUSD <= 0 {
		e.warned, e.reached = false, false
		return
	}
	if spent < b.LimitUSD {
		e.reached = false
	}
	warnAt := b.LimitUSD * float64(b.WarnShare()) / 100
	if spent < warnAt {
		e.warned = false
		return
	}
	switch {
	case spent >= b.LimitUSD:
		if !e.reached {
			e.reached, e.warned = true, true
			e.pending = append(e.pending, Event{Type: EventBudgetReached, Text: BudgetReachedMsg(spent, b.LimitUSD)})
		}
	case !e.warned:
		e.warned = true
		e.pending = append(e.pending, Event{Type: EventBudgetWarn, Text: fmt.Sprintf("Budget warning: $%.2f of $%.2f spent (%d%%)", spent, b.LimitUSD, int(spent/b.LimitUSD*100))})
	}
}

// CheckBudget re-evaluates the budget, for spending the engine does not see
// (the lead) or a changed limit.
func (e *Engine) CheckBudget() {
	e.mu.Lock()
	e.checkBudgetLocked()
	e.mu.Unlock()
	e.flush()
}

// flush sends the queued budget events.
func (e *Engine) flush() {
	e.mu.Lock()
	evs := e.pending
	e.pending = nil
	e.mu.Unlock()
	for _, ev := range evs {
		e.emit(ev)
	}
}

// UnpricedNodes lists the IDs of nodes that used tokens without a known price.
func (g *Graph) UnpricedNodes() []string {
	var ids []string
	for _, n := range g.Nodes {
		if (n.InputTokens > 0 || n.OutputTokens > 0) && !n.CostKnown {
			ids = append(ids, n.ID)
		}
	}
	return ids
}
