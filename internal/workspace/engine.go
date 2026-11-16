package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const eventBuffer = 256

// stopWait is how long StopAll waits for running nodes to return.
const stopWait = 5 * time.Second

// Engine schedules graph nodes onto an Executor, up to maxParallel at once.
// All graph mutation happens under its mutex; callers read snapshots.
type Engine struct {
	mu     sync.Mutex
	g      *Graph
	ex     Executor
	max    int
	save   func(*Graph) error // nil disables persistence
	events chan Event

	auto    bool
	autoCtx context.Context
	cancels map[string]context.CancelFunc // running nodes
	stopped map[string]bool               // nodes whose stop was requested
	// costBase is what a node's earlier attempts cost when its current run began.
	costBase map[string]costBase
	wg       sync.WaitGroup

	cost    CostFunc
	budget  func() Budget
	pending []Event // engine events queued under the lock, sent by flush
	warned  bool    // the warn threshold was announced
	reached bool    // the budget-reached message was sent

	stopping  atomic.Bool // set once StopAll begins; event sends never block after that
	stopCh    chan struct{}
	stopOnce  sync.Once
	done      chan struct{}
	closeOnce sync.Once
}

// started is a node that was marked running under the lock and still needs
// its goroutine launched.
type started struct {
	ctx    context.Context
	cancel context.CancelFunc
	g      *Graph
	n      *Node
}

// NewEngine creates an engine over g. savePath "" disables persistence.
func NewEngine(g *Graph, ex Executor, maxParallel int, savePath string) *Engine {
	var save func(*Graph) error
	if savePath != "" {
		save = func(g *Graph) error { return Save(savePath, g) }
	}
	return NewEngineWithSave(g, ex, maxParallel, save)
}

// NewEngineWithSave creates an engine over g that persists the graph through
// save after every change. A nil save disables persistence.
func NewEngineWithSave(g *Graph, ex Executor, maxParallel int, save func(*Graph) error) *Engine {
	if maxParallel < 1 {
		maxParallel = 1
	}
	return &Engine{
		g:        g,
		ex:       ex,
		max:      maxParallel,
		save:     save,
		events:   make(chan Event, eventBuffer),
		cancels:  make(map[string]context.CancelFunc),
		costBase: make(map[string]costBase),
		stopped:  make(map[string]bool),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Events returns the event stream. It is never closed.
func (e *Engine) Events() <-chan Event { return e.events }

// Snapshot returns a deep copy of the graph.
func (e *Engine) Snapshot() *Graph {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.g.Clone()
}

// Auto reports whether auto mode is on.
func (e *Engine) Auto() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.auto
}

// Running returns the number of nodes currently running.
func (e *Engine) Running() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cancels)
}

// Done is closed by Close; an event pump can stop reading when it fires.
func (e *Engine) Done() <-chan struct{} { return e.done }

// Close stops every node (see StopAll) and then closes Done. It is safe to
// call more than once.
func (e *Engine) Close() {
	e.StopAll()
	e.closeOnce.Do(func() { close(e.done) })
}

// WillAutoRun reports whether auto mode is on and the node is todo and
// assigned, so it will start by itself as soon as its dependencies are done.
func (e *Engine) WillAutoRun(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.g.Node(id)
	return e.auto && n != nil && n.Status == StatusTodo && e.g.Assigned(n)
}

// emit sends a state event, blocking until there is room. Once StopAll has
// begun the event is dropped when the buffer is full.
func (e *Engine) emit(ev Event) {
	if e.stopping.Load() {
		e.emitOutput(ev)
		return
	}
	select {
	case e.events <- ev:
	case <-e.stopCh:
		e.emitOutput(ev)
	}
}

// emitOutput sends an output event and drops it when the buffer is full.
func (e *Engine) emitOutput(ev Event) {
	select {
	case e.events <- ev:
	default:
	}
}

// saveLocked persists the graph. The caller reports a non-nil error with
// reportSave after unlocking.
func (e *Engine) saveLocked() error {
	if e.save == nil {
		return nil
	}
	return e.save(e.g)
}

func (e *Engine) reportSave(err error) {
	if err != nil {
		e.emitOutput(Event{Type: EventOutput, Text: "workspace: save failed: " + err.Error() + "\n"})
	}
}

// Replace runs mut on the live graph under the lock and saves it. It refuses
// while any node is running.
func (e *Engine) Replace(mut func(g *Graph)) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cancels) > 0 {
		return errors.New("stop the running nodes first")
	}
	for _, n := range e.g.Nodes {
		if n.Status == StatusRunning {
			return errors.New("stop the running nodes first")
		}
	}
	mut(e.g)
	return e.saveLocked()
}

func editable(n *Node) bool { return n.Status == StatusTodo || n.Status == StatusFailed }

// Assign sets the role of a todo or failed work node. "" unassigns.
func (e *Engine) Assign(id, role string) error {
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case n.Kind != KindWork:
		e.mu.Unlock()
		return fmt.Errorf("node %s does not take a role", id)
	case !editable(n):
		e.mu.Unlock()
		return fmt.Errorf("node %s is %s and cannot be reassigned", id, n.Status)
	}
	n.Role = role
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return nil
}

// SetSkills sets the plugin skills of a todo or failed work node (the role's
// own skills are not part of the list). Ids that are not well formed are
// dropped.
func (e *Engine) SetSkills(id string, skills []string) error {
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case n.Kind != KindWork:
		e.mu.Unlock()
		return fmt.Errorf("node %s does not take skills", id)
	case !editable(n):
		e.mu.Unlock()
		return fmt.Errorf("node %s is %s and its skills cannot be changed", id, n.Status)
	}
	n.Skills = validSkillIDs(skills)
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return nil
}

// AcceptSuggestions assigns the suggested role of every unassigned work node
// whose suggestion is defined. It returns how many nodes were assigned.
func (e *Engine) AcceptSuggestions(defined func(string) bool) int {
	e.mu.Lock()
	count := 0
	for _, n := range e.g.Nodes {
		if n.Kind == KindWork && n.Role == "" && n.SuggestedRole != "" && editable(n) && defined(n.SuggestedRole) {
			n.Role = n.SuggestedRole
			count++
		}
	}
	var err error
	if count > 0 {
		err = e.saveLocked()
	}
	e.mu.Unlock()
	e.reportSave(err)
	return count
}

// AddFix adds a fix node for a failed check node and returns its ID.
func (e *Engine) AddFix(checkID string) (string, error) {
	e.mu.Lock()
	c := e.g.Node(checkID)
	if c == nil {
		e.mu.Unlock()
		return "", fmt.Errorf("unknown node %s", checkID)
	}
	if c.Status != StatusFailed {
		e.mu.Unlock()
		return "", fmt.Errorf("node %s has not failed", checkID)
	}
	f, err := e.g.AddFix(checkID, c.Error)
	if err != nil {
		e.mu.Unlock()
		return "", err
	}
	serr := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(serr)
	return f.ID, nil
}

// AddCIFix adds a fix node for a failing pull request check or for review
// comments, assigns role when it is not "", and returns its ID. It works while
// other nodes run.
func (e *Engine) AddCIFix(title, desc, role string) string {
	e.mu.Lock()
	f := e.g.AddCIFix(title, desc)
	f.Role = role
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return f.ID
}

// AddWork adds a work node T<n> (n at least nextID) with the given title and
// description, assigns role when it is not "", and returns its ID and the next
// free number. It works while other nodes run.
func (e *Engine) AddWork(title, desc, role string, nextID int) (string, int) {
	e.mu.Lock()
	f, next := e.g.AddWork(title, desc, role, nextID)
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return f.ID, next
}

// Run starts one node now.
func (e *Engine) Run(ctx context.Context, id string) error {
	e.mu.Lock()
	n := e.g.Node(id)
	switch {
	case n == nil:
		e.mu.Unlock()
		return fmt.Errorf("unknown node %s", id)
	case n.Status == StatusRunning:
		e.mu.Unlock()
		return fmt.Errorf("node %s is already running", id)
	case n.Status == StatusDone:
		e.mu.Unlock()
		return fmt.Errorf("node %s is already done", id)
	case !e.g.Assigned(n):
		e.mu.Unlock()
		return fmt.Errorf("node %s has no role assigned", id)
	}
	if b := e.g.Blockers(id); len(b) > 0 {
		e.mu.Unlock()
		return fmt.Errorf("node %s is blocked by %s", id, strings.Join(b, ", "))
	}
	if len(e.cancels) >= e.max {
		running := len(e.cancels)
		e.mu.Unlock()
		return fmt.Errorf("parallel limit reached (%d running)", running)
	}
	if msg, blocked := e.budgetBlockedLocked(); blocked {
		e.mu.Unlock()
		return errors.New(msg)
	}
	s := e.startLocked(ctx, n)
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.launch(s)
	return nil
}

// RunAll turns auto mode on and starts every runnable node, then keeps
// starting nodes as others finish.
func (e *Engine) RunAll(ctx context.Context) {
	e.mu.Lock()
	e.auto = true
	e.autoCtx = ctx
	starts := e.pickLocked()
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	e.flush()
	for _, s := range starts {
		e.launch(s)
	}
}

// Stop cancels one running node; it ends failed with Error "stopped".
func (e *Engine) Stop(id string) {
	e.mu.Lock()
	if cancel, ok := e.cancels[id]; ok {
		e.stopped[id] = true
		cancel()
	}
	e.mu.Unlock()
}

// StopAll turns auto mode off, cancels every running node and waits for the
// node goroutines to return, for at most 5 seconds: a node that ignores its
// cancelled context is abandoned and StopAll returns anyway. From the moment
// it begins, event sends never block (events are dropped when the buffer is
// full), so it does not depend on anyone reading Events().
func (e *Engine) StopAll() {
	e.stopping.Store(true)
	e.stopOnce.Do(func() { close(e.stopCh) })
	e.mu.Lock()
	e.auto = false
	for id, cancel := range e.cancels {
		e.stopped[id] = true
		cancel()
	}
	e.mu.Unlock()
	finished := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(stopWait):
	}
}

// startLocked marks n running and reserves its goroutine slot.
func (e *Engine) startLocked(parent context.Context, n *Node) started {
	ctx, cancel := context.WithCancel(parent)
	n.Status = StatusRunning
	n.StartedAt = time.Now()
	n.EndedAt = time.Time{}
	n.Error = ""
	n.Summary = ""
	n.Files = nil
	n.Observed = nil
	n.FileStats = nil
	n.Commit = ""
	n.Warnings = nil
	// The cost adds up over the attempts: this run is priced on top of what the
	// earlier ones cost, and an unpriced earlier attempt keeps CostKnown false.
	e.costBase[n.ID] = costBase{usd: n.CostUSD, unpriced: (n.InputTokens > 0 || n.OutputTokens > 0) && !n.CostKnown}
	n.InputTokens, n.OutputTokens, n.Estimated = 0, 0, false
	e.cancels[n.ID] = cancel
	e.wg.Add(1)
	gc := e.g.Clone()
	return started{ctx: ctx, cancel: cancel, g: gc, n: gc.Node(n.ID)}
}

// pickLocked starts runnable nodes while auto mode is on and capacity is
// left. It turns auto mode off when nothing runs and nothing is runnable.
func (e *Engine) pickLocked() []started {
	if !e.auto {
		return nil
	}
	if e.autoCtx.Err() != nil {
		e.auto = false
		return nil
	}
	var out []started
	runnable := e.g.Runnable()
	if msg, blocked := e.budgetBlockedLocked(); blocked {
		if len(runnable) > 0 && !e.reached {
			e.reached = true
			e.pending = append(e.pending, Event{Type: EventBudgetReached, Text: msg})
		}
		runnable = nil
	}
	for _, n := range runnable {
		if len(e.cancels) >= e.max {
			break
		}
		out = append(out, e.startLocked(e.autoCtx, n))
	}
	if len(e.cancels) == 0 {
		e.auto = false
	}
	return out
}

func (e *Engine) launch(s started) { go e.runNode(s) }

func (e *Engine) runNode(s started) {
	defer e.wg.Done()
	defer s.cancel()
	id := s.n.ID
	e.emit(Event{NodeID: id, Type: EventStarted})

	forward := func(ev Event) {
		ev.NodeID = id
		switch ev.Type {
		case EventOutput:
			e.emitOutput(ev)
		case EventUsage:
			e.mu.Lock()
			if n := e.g.Node(id); n != nil {
				n.InputTokens, n.OutputTokens, n.Estimated = ev.In, ev.Out, ev.Estimated
				if e.cost != nil {
					usd, known := e.cost(ev.Runner, ev.Model, ev.In, ev.Out)
					base := e.costBase[id]
					n.CostUSD, n.CostKnown = base.usd, !base.unpriced && known
					if known {
						n.CostUSD += usd
					}
				}
			}
			e.checkBudgetLocked()
			e.mu.Unlock()
			e.emit(ev)
			e.flush()
		}
	}
	res, err := e.ex.Run(s.ctx, s.g, s.n, forward)

	e.mu.Lock()
	n := e.g.Node(id)
	delete(e.cancels, id)
	wasStopped := e.stopped[id]
	delete(e.stopped, id)
	n.EndedAt = time.Now()
	var fin Event
	var overlaps []Event
	if err != nil {
		n.Status = StatusFailed
		n.Error = err.Error()
		if wasStopped || s.ctx.Err() != nil {
			n.Error = "stopped"
		}
		fin = Event{NodeID: id, Type: EventFailed, Text: n.Error}
	} else {
		n.Status = StatusDone
		n.Summary = res.Summary
		n.Files = append([]string(nil), res.Files...)
		n.Observed = append([]string(nil), res.Observed...)
		n.FileStats = append([]FileStat(nil), res.FileStats...)
		n.Commit = res.Commit
		n.Warnings = append([]string(nil), res.Warnings...)
		fin = Event{NodeID: id, Type: EventDone, Text: n.Summary}
		overlaps = e.markOverlapsLocked(n)
	}
	serr := e.saveLocked()
	e.checkBudgetLocked()
	next := e.pickLocked()
	e.mu.Unlock()

	e.reportSave(serr)
	e.flush()
	for _, ev := range overlaps {
		e.emitOutput(ev)
	}
	e.emit(fin)
	for _, ns := range next {
		e.launch(ns)
	}
}

// markOverlapsLocked compares the files of the node that just finished with
// the files of every node whose run overlapped it in time and adds a warning
// to both for each shared path. It returns the output events to send.
func (e *Engine) markOverlapsLocked(n *Node) []Event {
	if len(n.Files) == 0 {
		return nil
	}
	mine := make(map[string]bool, len(n.Files))
	for _, f := range n.Files {
		mine[f] = true
	}
	var evs []Event
	warn := func(target *Node, other, path string) {
		w := fmt.Sprintf("also written by %s in parallel: %s", other, path)
		for _, x := range target.Warnings {
			if x == w {
				return
			}
		}
		target.Warnings = append(target.Warnings, w)
		evs = append(evs, Event{NodeID: target.ID, Type: EventOutput, Text: "\u26a0 " + w + "\n"})
	}
	for _, o := range e.g.Nodes {
		if o == n || o.StartedAt.IsZero() {
			continue
		}
		overlapped := o.Status == StatusRunning || (!o.EndedAt.IsZero() && o.EndedAt.After(n.StartedAt) && o.StartedAt.Before(n.EndedAt))
		if !overlapped {
			continue
		}
		for _, f := range o.Files {
			if mine[f] {
				warn(n, o.ID, f)
				warn(o, n.ID, f)
			}
		}
	}
	return evs
}
