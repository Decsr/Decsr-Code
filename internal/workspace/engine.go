package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/runner"
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

	// router proposes the agent of a node (see Propose); nil = none.
	router *Router

	auto    bool
	autoCtx context.Context
	// resumeAuto is set when a node hit a usage limit during a run-all: its
	// retry turns auto mode on again so its dependents follow.
	resumeAuto bool
	cancels    map[string]context.CancelFunc // running nodes
	stopped    map[string]bool               // nodes whose stop was requested
	// costBase is what a node's earlier attempts cost when its current run began.
	costBase map[string]costBase
	wg       sync.WaitGroup

	cost    CostFunc
	budget  func() Budget
	pending []Event // engine events queued under the lock, sent by flush
	// parent is the context of the latest node start; a reply starts under it.
	parent context.Context
	// startNotes are output lines to send right after a node's started event.
	startNotes map[string][]Event
	warned     bool // the warn threshold was announced
	reached    bool // the budget-reached message was sent

	// details holds the full activity detail (up to 2000 chars) per node and
	// activity ID, in memory only. It follows the 200-per-node cap of Node.Activity.
	details     map[string]*detailLog
	lastActSave time.Time
	actSaveGap  time.Duration    // minimum time between saves caused by activity
	now         func() time.Time // clock; time.Now unless a test sets it

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
		g:          g,
		ex:         ex,
		max:        maxParallel,
		save:       save,
		events:     make(chan Event, eventBuffer),
		cancels:    make(map[string]context.CancelFunc),
		costBase:   make(map[string]costBase),
		stopped:    make(map[string]bool),
		startNotes: make(map[string][]Event),
		details:    make(map[string]*detailLog),
		actSaveGap: activitySaveEvery,
		now:        time.Now,
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
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

// Waiting returns a snapshot of the nodes that wait for a reply, in Nodes
// order. Each node's Report holds the question or the blocked summary.
func (e *Engine) Waiting() []*Node {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []*Node
	for _, n := range e.g.Clone().Nodes {
		if n.Status == StatusWaiting {
			out = append(out, n)
		}
	}
	return out
}

// waitingText is what a waiting node asks: its question, else its summary.
func waitingText(n *Node) string {
	if n.Report != nil {
		if s := n.Report.Ask(); s != "" {
			return s
		}
	}
	return n.Summary
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
	if n.Role != role && n.Assign != nil && n.Assign.Source != SourceManual {
		// The proposal was made for another role: propose again, with the
		// same approval state.
		pending := n.Assign.Pending
		n.Role = role
		n.Assign = e.router.propose(n)
		if n.Assign != nil {
			n.Assign.Pending = pending
		}
	}
	if n.Role != role {
		// The conversation belongs to the old role.
		n.SessionID, n.SessionRunner, n.SessionAttempt = "", "", 0
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

// AllowCommands adds command programs to the allow list of a work or fix node
// for its later attempts. Each program is kept once.
func (e *Engine) AllowCommands(id string, programs []string) error {
	e.mu.Lock()
	n := e.g.Node(id)
	if n == nil || n.Kind != KindWork {
		e.mu.Unlock()
		return fmt.Errorf("unknown work node %s", id)
	}
	for _, p := range programs {
		p = strings.TrimSpace(p)
		have := p == ""
		for _, x := range n.ExtraAllowed {
			have = have || x == p
		}
		if !have {
			n.ExtraAllowed = append(n.ExtraAllowed, p)
		}
	}
	serr := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(serr)
	return nil
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
	failed := []*Node{c}
	for _, d := range c.DependsOn {
		if dn := e.g.Node(d); dn != nil && dn.Status == StatusFailed {
			failed = append(failed, dn)
		}
	}
	excerpt := FixExcerptWith(failed, FixExcerptChars, e.detailLocked)
	f, err := e.g.AddFixExcerpt(checkID, c.Error, excerpt)
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
	// The excerpt of failed nodes goes after the CI text, only if budget remains.
	if room := FixExcerptChars - utf8.RuneCountInString(desc); room > 200 {
		var failed []*Node
		for _, n := range e.g.Nodes {
			if n.Status == StatusFailed {
				failed = append(failed, n)
			}
		}
		if ex := FixExcerptWith(failed, room-len("\n\nWhat failed:\n"), e.detailLocked); ex != "" {
			desc += whatFailed(ex)
		}
	}
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

// QueueCheck makes Build & test ready to run now whatever changed: it adds the
// CHECK node (with commands, or deferred when there are none) when the graph has
// none, else resets it to todo. It fails while Build & test is running.
func (e *Engine) QueueCheck(commands []string) error {
	e.mu.Lock()
	c := e.g.Node("CHECK")
	switch {
	case c == nil:
		if len(commands) > 0 {
			c = e.g.AddCheck(commands)
		} else {
			c = e.g.AddCheckDeferred()
		}
	case c.Status == StatusRunning:
		e.mu.Unlock()
		return errors.New("Build & test is already running")
	default:
		c.Status = StatusTodo
		c.Error, c.Summary, c.Warnings, c.Skipped = "", "", nil, false
	}
	c.ForceRun = true
	err := e.saveLocked()
	e.mu.Unlock()
	e.reportSave(err)
	return nil
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
	case n.Limited():
		e.mu.Unlock()
		return fmt.Errorf("node %s waits for the %s limit to lift: retry it with r", id, n.Limit.Provider)
	case n.Status == StatusWaiting:
		e.mu.Unlock()
		return fmt.Errorf("node %s is waiting for your reply: answer it with m", id)
	case !e.g.Assigned(n):
		e.mu.Unlock()
		return fmt.Errorf("node %s has no role assigned", id)
	case n.AwaitingApproval():
		e.mu.Unlock()
		return fmt.Errorf("node %s waits for the approval of its agent", id)
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
	e.resumeAuto = false
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
	e.parent = parent
	// A reply attempt is given an account of the previous attempt. It is built
	// here because the full details of that attempt are dropped just below.
	n.replyContext = ""
	if n.Reply != "" && len(n.Attempts) > 0 {
		n.replyContext = ReplyExcerpt(n, n.Attempts[len(n.Attempts)-1], ReplyContextChars, e.detailLocked)
	}
	n.Status = StatusRunning
	n.StartedAt = time.Now()
	n.EndedAt = time.Time{}
	n.Error = ""
	n.Summary = ""
	n.Report = nil
	n.Skipped = false
	n.Answered = false
	n.Denied = nil
	n.Files = nil
	n.Observed = nil
	n.FileStats = nil
	n.Commit = ""
	n.Warnings = nil
	// The activity journal belongs to one attempt: a re-run starts it empty
	// (the history of earlier attempts is not kept).
	n.Activity, n.ActivityDropped = nil, 0
	delete(e.details, n.ID)
	n.LastActivityAt = time.Now()
	n.ContextTokens, n.ContextLimit = 0, 0
	// The cost adds up over the attempts: this run is priced on top of what the
	// earlier ones cost, and an unpriced earlier attempt keeps CostKnown false.
	e.costBase[n.ID] = costBase{usd: n.CostUSD, unpriced: (n.InputTokens > 0 || n.OutputTokens > 0) && !n.CostKnown}
	n.InputTokens, n.CachedTokens, n.OutputTokens, n.Estimated = 0, 0, 0, false
	n.Model, n.Effort = "", ""
	if n.Assign != nil {
		n.Assign.FallbackNote = ""
	}
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
	e.mu.Lock()
	notes := e.startNotes[id]
	delete(e.startNotes, id)
	e.mu.Unlock()
	for _, ev := range notes {
		e.emitOutput(ev)
	}

	forward := func(ev Event) {
		ev.NodeID = id
		switch ev.Type {
		case EventOutput:
			e.mu.Lock()
			e.touchLocked(id)
			e.mu.Unlock()
			e.emitOutput(ev)
		case EventActivity:
			serr := e.recordActivity(id, ev.Activity)
			e.reportSave(serr)
			e.emitOutput(ev)
		case EventFallback:
			e.mu.Lock()
			if n := e.g.Node(id); n != nil && n.Assign != nil {
				n.Assign.FallbackNote = ev.Text
			}
			e.mu.Unlock()
			e.emit(ev)
		case EventSession:
			e.mu.Lock()
			if n := e.g.Node(id); n != nil && ev.Text != "" {
				n.SessionID, n.SessionRunner, n.SessionAttempt = ev.Text, ev.Runner, n.AttemptNumber()
			}
			serr := e.saveLocked()
			e.mu.Unlock()
			e.reportSave(serr)
		case EventResumeFailed:
			e.emit(ev)
		case EventContext:
			e.mu.Lock()
			if n := e.g.Node(id); n != nil {
				n.ContextTokens, n.ContextLimit = ev.ContextTokens, ev.ContextLimit
			}
			e.mu.Unlock()
			e.emit(ev)
		case EventModel:
			e.mu.Lock()
			if n := e.g.Node(id); n != nil {
				if ev.Model != "" {
					n.Model = ev.Model
				}
				n.Effort = ev.Effort
			}
			e.mu.Unlock()
			e.emit(ev)
		case EventUsage:
			e.mu.Lock()
			if n := e.g.Node(id); n != nil {
				n.InputTokens, n.CachedTokens, n.OutputTokens, n.Estimated = ev.In, ev.Cached, ev.Out, ev.Estimated
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
			if !ev.Live {
				e.flush()
			}
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
	if rl := asRateLimit(err); rl != nil && !wasStopped && s.ctx.Err() == nil {
		retries := 0
		if n.Limit != nil {
			retries = n.Limit.Retries
		}
		n.Status = StatusWaiting
		n.Limit = &LimitInfo{Provider: rl.Provider, ResetAt: rl.ResetAt, Message: limitMessage(rl), Retries: retries}
		if e.auto {
			e.resumeAuto = true
		}
		fin = Event{NodeID: id, Type: EventLimited, Text: n.Limit.Message, ResetAt: rl.ResetAt}
	} else if err != nil {
		n.Status = StatusFailed
		n.Limit = nil
		n.Error = err.Error()
		if wasStopped || s.ctx.Err() != nil {
			n.Error = "stopped"
		}
		fin = Event{NodeID: id, Type: EventFailed, Text: n.Error}
	} else {
		n.Limit = nil
		n.Status = StatusDone
		n.Summary = res.Summary
		if n.Kind == KindCheck {
			n.Skipped = res.Skipped
			if !res.Skipped {
				n.LastRunAt = n.EndedAt
			}
		}
		if res.Report.Status != "" {
			rep := res.Report
			n.Report = &rep
			if n.Kind == KindWork && rep.Waits() {
				n.Status = StatusWaiting
			}
		}
		n.Denied = append([]runner.Denial(nil), res.Denied...)
		n.Files = append([]string(nil), res.Files...)
		n.Observed = append([]string(nil), res.Observed...)
		n.FileStats = append([]FileStat(nil), res.FileStats...)
		n.Commit = res.Commit
		n.Warnings = append([]string(nil), res.Warnings...)
		fin = Event{NodeID: id, Type: EventDone, Text: n.Summary}
		if n.Status == StatusDone && n.Kind == KindWork && n.ReadOnlyTask && strings.TrimSpace(res.Answer) != "" {
			n.Answered = true
			fin.Answer = res.Answer
		}
		if n.Status == StatusWaiting {
			fin = Event{NodeID: id, Type: EventWaiting, Text: waitingText(n)}
		}
		overlaps = e.markOverlapsLocked(n)
	}
	n.ForceRun = false
	e.checkBudgetLocked()
	// A reply that came in while the node ran starts the next attempt now,
	// whatever way this one ended.
	var replyStart *started
	if n.PendingReply != "" {
		var notes []Event
		_, replyStart, notes = e.applyReplyLocked(n, n.PendingReply)
		overlaps = append(overlaps, notes...)
	}
	serr := e.saveLocked()
	next := e.pickLocked()
	e.mu.Unlock()

	e.reportSave(serr)
	e.flush()
	for _, ev := range overlaps {
		e.emitOutput(ev)
	}
	e.emit(fin)
	if replyStart != nil {
		e.launch(*replyStart)
	}
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

// Activity journal limits.
const (
	// maxNodeActivity caps Node.Activity (and the in-memory details).
	maxNodeActivity = 200
	// maxStoredDetail is the longest Detail kept in a node (and so in the
	// session file); ActivityDetail returns the full text from memory.
	maxStoredDetail = 500
	// activitySaveEvery is the shortest time between saves caused only by
	// activity events.
	activitySaveEvery = 2 * time.Second
)

// detailLog is the full detail of one node's activity items, oldest first.
type detailLog struct {
	ids []string
	m   map[string]string
}

func (d *detailLog) set(id, detail string) {
	if _, ok := d.m[id]; !ok {
		d.ids = append(d.ids, id)
		if len(d.ids) > maxNodeActivity {
			delete(d.m, d.ids[0])
			d.ids = d.ids[1:]
		}
	}
	d.m[id] = detail
}

// touchLocked records that a running node just did something.
func (e *Engine) touchLocked(id string) {
	if n := e.g.Node(id); n != nil && n.Status == StatusRunning {
		n.LastActivityAt = e.now()
	}
}

// recordActivity adds a to the journal of node id: an item with a known ID is
// replaced in place, a new ID is appended (the oldest item is dropped beyond
// 200). The graph is saved at most every 2 seconds this way.
func (e *Engine) recordActivity(id string, a *runner.Activity) error {
	if a == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.g.Node(id)
	if n == nil || n.Status != StatusRunning {
		return nil
	}
	n.LastActivityAt = e.now()
	d := e.details[id]
	if d == nil {
		d = &detailLog{m: make(map[string]string)}
		e.details[id] = d
	}
	d.set(a.ID, a.Detail)
	stored := *a
	if a.ExitCode != nil {
		c := *a.ExitCode
		stored.ExitCode = &c
	}
	stored.Detail = runner.Clip(a.Detail, maxStoredDetail)
	replaced := false
	for i := len(n.Activity) - 1; i >= 0; i-- {
		if n.Activity[i].ID == a.ID {
			n.Activity[i] = stored
			replaced = true
			break
		}
	}
	if !replaced {
		n.Activity = append(n.Activity, stored)
		if over := len(n.Activity) - maxNodeActivity; over > 0 {
			n.Activity = append([]runner.Activity(nil), n.Activity[over:]...)
			n.ActivityDropped += over
		}
	}
	if now := e.now(); now.Sub(e.lastActSave) >= e.actSaveGap {
		e.lastActSave = now
		return e.saveLocked()
	}
	return nil
}

// ActivityDetail returns the full detail (up to 2000 characters) of one
// activity item of a node. It is kept in memory only: false after a restart of
// the program or of the node, or when the item was dropped from the journal.
func (e *Engine) ActivityDetail(nodeID, activityID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.detailLocked(nodeID, activityID)
}

func (e *Engine) detailLocked(nodeID, activityID string) (string, bool) {
	d := e.details[nodeID]
	if d == nil {
		return "", false
	}
	s, ok := d.m[activityID]
	return s, ok
}
