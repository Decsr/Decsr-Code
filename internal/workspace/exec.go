package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/ranking"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/skills"
)

// EventType classifies an Event.
type EventType string

const (
	EventStarted EventType = "started"
	EventOutput  EventType = "output"
	// EventActivity carries a structured agent activity in Event.Activity
	// (thinking, a command, a file edit, ...). A later activity with the same
	// ID replaces the earlier one.
	EventActivity EventType = "activity"
	// EventUsage carries the usage of the node. With Live set it is a
	// progress snapshot sent while the node runs (at most about twice a
	// second); without it, it is the final usage of the run.
	EventUsage EventType = "usage"
	// EventModel reports the model (Event.Model) and reasoning effort
	// (Event.Effort) of the node. It is sent when the node starts, from the
	// role, and again when the runner reports the model it really uses.
	EventModel EventType = "model"
	// EventContext reports the size of the prompt a worker is about to get:
	// Event.ContextTokens (estimated) and Event.ContextLimit (0 = unknown).
	EventContext EventType = "context"
	// EventFallback reports that the node's assigned agent could not be used
	// and the role's own agent ran instead; Text is the reason.
	EventFallback EventType = "fallback"
	// EventSession reports the id of the node's own CLI conversation (Text) and
	// the runner it belongs to (Runner). Another attempt resumes it.
	EventSession EventType = "session"
	// EventResumeFailed reports that a node could not resume its conversation
	// and started fresh with a summary; Text is the line to show.
	EventResumeFailed EventType = "resume_failed"
	EventDone         EventType = "done"
	// EventWaiting ends a node that reported a question or was blocked, in
	// place of EventDone. Text is the question (or the blocked summary); the
	// node's Report holds the details.
	EventWaiting EventType = "waiting"
	EventFailed  EventType = "failed"
	// EventLimited ends a node that hit a provider usage limit; it waits to
	// be retried. Text is the limit message and ResetAt the reset time (zero
	// when unknown).
	EventLimited EventType = "limited"
	// EventBudgetWarn and EventBudgetReached are engine-level (NodeID "");
	// Text is the message to show.
	EventBudgetWarn    EventType = "budget_warn"
	EventBudgetReached EventType = "budget_reached"
)

// Event is a progress notification for one node. NodeID "" is used for
// engine-level messages (for example a failed save).
type Event struct {
	NodeID string
	Type   EventType
	Text   string // output chunk, or the error text for EventFailed
	// EventUsage: cumulative for the node. In is the whole prompt; Cached is
	// the part of it read from or written to a prompt cache.
	In, Cached, Out int
	Estimated       bool
	// Live marks an EventUsage that is a progress snapshot, not the final usage.
	Live bool
	// Answer is set for an EventDone of a read-only task: its full reply.
	Answer string
	// Effort is set for EventModel; "" = the provider default.
	Effort string
	// Runner and Model of the agent that used the tokens (EventUsage), to
	// price them. For EventModel, Model is the model of the node.
	Runner, Model string
	// Activity is set for EventActivity.
	Activity *runner.Activity
	// ContextTokens and ContextLimit are set for EventContext.
	ContextTokens, ContextLimit int
	// ResetAt is set for EventLimited.
	ResetAt time.Time
}

// Result is the outcome of a successful node run.
type Result struct {
	Summary  string
	Files    []string // written from the worker's explicit file blocks
	Observed []string // new or changed on disk during the run, by the agent's own tools
	// FileStats are the lines added and removed per file; set only when the
	// node ran in its own worktree.
	FileStats []FileStat
	// Commit is the node's own commit; set only when the node ran in its own
	// worktree and changed something.
	Commit   string
	Warnings []string
	// Answer is the full reply of a read-only task, without the REPORT block.
	Answer string
	// Skipped is set for a Build & test that did not run because no node had
	// changed a non-doc file.
	Skipped bool
	// Report is the worker's closing report; the zero value (no Status) for a
	// node that has none, such as Build & test.
	Report Report
	// Denied are the tool calls the runner refused during the run.
	Denied []runner.Denial
}

// Executor runs one node. It must return promptly when ctx is cancelled.
type Executor interface {
	Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error)
}

// maxCheckErrorBytes caps the failure text of a check node.
const maxCheckErrorBytes = 4000

const (
	// checkTimeout bounds each check command (builds and test suites can be slow).
	checkTimeout = 15 * time.Minute
	// maxContextFileBytes caps one file in the "Current files" section.
	maxContextFileBytes = 10 * 1024
	// maxContextFilesBytes caps the whole "Current files" section.
	maxContextFilesBytes = 24 * 1024

	noCheckCommandsMsg = "no build or test command is known for this project yet (set project.test_cmd or pick a language in Project Setup)"
)

type executor struct {
	root           string
	cfg            config.Config
	projectContext string
	contextFor     ContextFunc // overrides projectContext per node; may be nil
	newRunner      func(config.AgentConfig) (runner.LLMRunner, error)
	checkCommands  func() []string        // commands for a check node without Commands; may be nil
	checkTimeout   time.Duration          // 0 = checkTimeout
	summary        func() string          // session summary for the work context; may be nil
	iso            *Isolator              // per-node worktrees; nil = all nodes share root
	connected      func() map[string]bool // runners that can start now; nil = ranking.Connected
}

// maxSessionSummaryChars caps the session summary given to a worker.
const maxSessionSummaryChars = 2000

// NewExecutor returns the real Executor: work nodes go through the generic
// worker, check nodes run their shell commands.
func NewExecutor(root string, cfg config.Config, projectContext string) Executor {
	return NewExecutorWithChecks(root, cfg, projectContext, nil)
}

// NewExecutorWithChecks is NewExecutor with a lazy source of check commands:
// a check node without Commands calls checkCommands when it runs.
func NewExecutorWithChecks(root string, cfg config.Config, projectContext string, checkCommands func() []string) Executor {
	return NewExecutorWithSummary(root, cfg, projectContext, checkCommands, nil)
}

// NewExecutorWithSummary is NewExecutorWithChecks plus a source of the session
// summary, which work nodes get as an "Earlier in this session" section.
func NewExecutorWithSummary(root string, cfg config.Config, projectContext string, checkCommands func() []string, summary func() string) Executor {
	return NewExecutorIsolated(root, cfg, projectContext, checkCommands, summary, nil)
}

// NewExecutorIsolated is NewExecutorWithSummary with an Isolator: each work
// node runs in its own git worktree and Build & test runs in the integration
// worktree. A nil iso keeps every node in root.
func NewExecutorIsolated(root string, cfg config.Config, projectContext string, checkCommands func() []string, summary func() string, iso *Isolator) Executor {
	return NewExecutorCtx(root, cfg, func(string, bool) string { return projectContext }, checkCommands, summary, iso)
}

// ContextFunc gives the project context for a worker that runs on the named
// runner, in agent mode or not.
type ContextFunc func(runnerName string, agentMode bool) string

// NewExecutorCtx is NewExecutorIsolated with a project context that depends on
// the runner and mode of each node: text the runner loads itself, and source
// excerpts an agent can read on its own, are then left out.
func NewExecutorCtx(root string, cfg config.Config, ctxFor ContextFunc, checkCommands func() []string, summary func() string, iso *Isolator) Executor {
	loader := skills.New("")
	return &executor{
		root:          root,
		cfg:           cfg,
		contextFor:    ctxFor,
		checkCommands: checkCommands,
		summary:       summary,
		iso:           iso,
		newRunner: func(ac config.AgentConfig) (runner.LLMRunner, error) {
			return runner.New(ac, loader, cfg.PromptLanguage)
		},
	}
}

func (x *executor) Run(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	if n.Kind == KindCheck {
		return x.runCheck(ctx, g, n, emit)
	}
	return x.runWork(ctx, g, n, emit)
}

func (x *executor) runWork(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	role, ok := FindRole(x.cfg, n.Role)
	if !ok {
		return Result{}, fmt.Errorf("role %q is not defined", n.Role)
	}
	if err := role.Agent.ModeError(); err != nil {
		return Result{}, fmt.Errorf("role %q: %w", role.Name, err)
	}
	// A sub-task the lead marked as not writing runs read-only whatever its role.
	readOnly := role.ReadOnly || n.ReadOnlyTask
	// cur is the agent that runs the node: the assigned one, else the role's.
	cur, r, note := x.nodeAgent(role, n)
	if note != "" {
		emit(Event{NodeID: n.ID, Type: EventFallback, Text: note})
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + note + "\n"})
	}
	if r == nil {
		var err error
		if r, err = x.newRunner(cur); err != nil {
			return Result{}, fmt.Errorf("runner for role %q: %w", role.Name, err)
		}
	}
	rolePrompt := ""
	switch {
	case role.Plugin:
		rolePrompt = role.Prompt
	case !role.Builtin:
		rolePrompt = role.Description
	}
	skillBlock, skillWarnings := x.userSkills(unionSkills(role.UserSkills, n.Skills))
	if skillBlock != "" {
		if rolePrompt != "" {
			rolePrompt += "\n\n"
		}
		rolePrompt += skillBlock
	}
	// wx works in the node's own worktree when isolated; skills, plugins and
	// project context still come from the project root.
	wx := *x
	finished := false
	if x.iso != nil {
		wroot, warns, err := x.iso.Start(n.ID)
		if err != nil {
			return Result{}, fmt.Errorf("worktree: %w", err)
		}
		wx.root = wroot
		skillWarnings = append(skillWarnings, warns...)
		defer func() {
			if !finished {
				x.iso.Discard(n.ID)
			}
		}()
	}
	var buildCommands []string
	if x.checkCommands != nil && !readOnly && role.Agent.EffectiveMode() == config.ModeAgent {
		buildCommands = x.checkCommands()
	}
	before, snapOK := snapshotTree(wx.root, maxSnapshotFiles)
	emit(Event{NodeID: n.ID, Type: EventModel, Model: cur.Model, Effort: runner.NormalizeEffort(cur.Effort)})
	throttle := newUsageThrottle(time.Now)
	var res agent.WorkerResult
	var err error
	sess := &sessionState{id: n.SessionID, runner: n.SessionRunner, attempt: n.SessionAttempt}
	noResume, limitFallback := false, false
	for {
		resume := ""
		if !noResume && sess.id != "" && sess.runner == cur.Runner && runner.CanResume(cur.Runner) {
			resume = sess.id
		}
		res, err = x.runWorker(ctx, r, wx, cur, readOnly, role, n, g, rolePrompt, buildCommands, emit, throttle, sess, resume)
		// A resume that fails at start: the node starts over with a summary of
		// what it did, as an attempt without a conversation does.
		var re *runner.ResumeError
		if resume != "" && errors.As(err, &re) && ctx.Err() == nil {
			noResume = true
			emit(Event{NodeID: n.ID, Type: EventResumeFailed,
				Text: fmt.Sprintf("\u26a0 %s: could not resume its conversation, started fresh with a summary", n.ID)})
			emit(Event{NodeID: n.ID, Type: EventOutput, Text: fmt.Sprintf("\u26a0 could not resume its conversation (%s), started fresh with a summary\n", oneLineErr(re))})
			continue
		}
		// A rate limit on the assigned agent before it did any work: the
		// role's own agent takes over once.
		if !limitFallback && usedAssign(n, cur, role) && asRateLimit(err) != nil && res.Usage.OutputTokens == 0 && ctx.Err() == nil {
			limitFallback = true
			cur = role.Agent
			rr, rerr := x.newRunner(cur)
			if rerr != nil {
				break
			}
			r = rr
			note := fmt.Sprintf("%s %s is rate limited: %s ran instead", n.Assign.Runner, n.Assign.Model, roleLabel(role))
			emit(Event{NodeID: n.ID, Type: EventFallback, Text: note})
			emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + note + "\n"})
			emit(Event{NodeID: n.ID, Type: EventModel, Model: cur.Model, Effort: runner.NormalizeEffort(cur.Effort)})
			continue
		}
		break
	}
	if res.Usage.InputTokens > 0 || res.Usage.OutputTokens > 0 {
		emit(Event{NodeID: n.ID, Type: EventUsage, In: res.Usage.InputTokens, Cached: res.Usage.CachedTokens, Out: res.Usage.OutputTokens, Estimated: res.Usage.Estimated,
			Runner: cur.Runner, Model: cur.Model})
	}
	if err != nil {
		return Result{}, err
	}
	out := Result{Summary: res.Summary, Files: res.Files, Warnings: skillWarnings, Report: res.Report, Denied: res.Denials}
	if n.ReadOnlyTask {
		out.Answer = answerBody(res.Body)
	}
	if snapOK {
		if after, ok := snapshotTree(wx.root, maxSnapshotFiles); ok {
			out.Observed = diffSnapshots(before, after, res.Files)
		}
	}
	for _, sk := range res.Skipped {
		out.Warnings = append(out.Warnings, "not written: "+sk)
	}
	if readOnly && len(out.Observed) > 0 {
		out.Warnings = append(out.Warnings, "read-only role changed files: "+strings.Join(out.Observed, ", "))
	}
	if !readOnly && len(res.Files) == 0 && len(out.Observed) == 0 && len(res.Skipped) == 0 {
		out.Warnings = append(out.Warnings, "no files were written")
	}
	for _, w := range out.Warnings {
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + w + "\n"})
	}
	if x.iso != nil && !readOnly {
		finished = true
		stats, commit, err := x.iso.FinishCommit(n.ID, n.Title)
		if err != nil {
			return Result{}, err
		}
		out.FileStats = stats
		out.Commit = commit
	}
	if n.Spec != nil {
		if w := x.markSpecDone(n.Spec); w != "" {
			out.Warnings = append(out.Warnings, w)
			emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + w + "\n"})
		}
	}
	return out, nil
}

// usedAssign reports whether cur is the node's assigned agent rather than the
// role's own.
func usedAssign(n *Node, cur config.AgentConfig, role Role) bool {
	return n.Assign != nil && (cur.Runner != role.Agent.Runner || cur.Model != role.Agent.Model || cur.Effort != role.Agent.Effort)
}

func roleLabel(role Role) string {
	return fmt.Sprintf("role %s (%s %s)", role.Name, role.Agent.Runner, role.Agent.Model)
}

// nodeAgent returns the agent config that runs n and, when it is built, its
// runner. The assigned agent replaces the role's runner, model and effort; the
// prompt, skills, mode, MCP and read-only stay the role's. When the assigned
// agent cannot start (runner not connected, bad mode, no runner) the role's
// agent is used and note says why.
func (x *executor) nodeAgent(role Role, n *Node) (config.AgentConfig, runner.LLMRunner, string) {
	a := n.Assign
	if a == nil || a.Runner == "" {
		return role.Agent, nil, ""
	}
	ac := role.Agent
	if a.Runner != ac.Runner {
		// The role's endpoint and key belong to its own runner.
		ac.BaseURL, ac.APIKeyEnv, ac.Provider = "", "", ""
	}
	ac.Runner, ac.Model, ac.Effort = a.Runner, a.Model, a.Effort
	if same := ac.Runner == role.Agent.Runner && ac.Model == role.Agent.Model && ac.Effort == role.Agent.Effort; same {
		return role.Agent, nil, ""
	}
	fall := func(why string) (config.AgentConfig, runner.LLMRunner, string) {
		return role.Agent, nil, fmt.Sprintf("%s %s is not available (%s): %s ran instead", a.Runner, a.Model, why, roleLabel(role))
	}
	if ac.Runner != role.Agent.Runner {
		conn := ranking.Connected
		if x.connected != nil {
			conn = x.connected
		}
		if !usableRunner(conn(), ac.Runner) {
			return fall("not connected")
		}
	}
	if err := ac.ModeError(); err != nil {
		return fall(err.Error())
	}
	r, err := x.newRunner(ac)
	if err != nil {
		return fall(oneLineErr(err))
	}
	return ac, r, ""
}

// sessionState is the CLI conversation of a node while it runs: the latest id
// the runner reported, the runner it belongs to and the attempt that got it.
type sessionState struct {
	id, runner string
	attempt    int
}

// continueMessage is what a resumed attempt is sent when no new message from
// the user came with it (a retry of an attempt that stopped early).
const continueMessage = "Your previous run stopped before it finished. Continue where you left off."

// runWorker is one worker run with the agent cur. A non-empty resume is the id
// of the node's own CLI conversation to continue: the run sends only the user's
// new message (or continueMessage for a retry) and no excerpt of the earlier
// attempt.
func (x *executor) runWorker(ctx context.Context, r runner.LLMRunner, wx executor, cur config.AgentConfig, readOnly bool, role Role, n *Node, g *Graph,
	rolePrompt string, buildCommands []string, emit func(Event), throttle *usageThrottle, sess *sessionState, resume string) (agent.WorkerResult, error) {
	if wx.contextFor != nil {
		wx.projectContext = wx.contextFor(cur.Runner, role.Agent.EffectiveMode() == config.ModeAgent)
	}
	wn, message := n, ""
	if resume != "" {
		// The conversation has the earlier attempts: leave the excerpt out of
		// the context (a runner that cannot store its system prompt gets the
		// context again, without it).
		c := *n
		c.Reply = ""
		wn = &c
		message = continueMessage
		if n.Reply != "" && sess.attempt < n.AttemptNumber() {
			message = n.Reply
		}
	}
	// Only a claude run loads MCP servers, and only the ones the lead marked.
	var mcpServers []string
	if cur.Runner == "claude" {
		mcpServers = n.MCP
	}
	return agent.RunWorker(ctx, r, wx.root, x.cfg, agent.WorkerRequest{
		KeepSession:   runner.CanResume(cur.Runner),
		ResumeID:      resume,
		ResumeMessage: message,
		OnSession: func(id string) {
			*sess = sessionState{id: id, runner: cur.Runner, attempt: n.AttemptNumber()}
			emit(Event{NodeID: n.ID, Type: EventSession, Text: id, Runner: cur.Runner})
		},
		RolePrompt:      rolePrompt,
		Skills:          role.Agent.Skills,
		Model:           cur.Model,
		Title:           n.Title,
		Description:     n.Description,
		Context:         wx.workContext(g, wn),
		ReadOnly:        readOnly,
		Mode:            role.Agent.EffectiveMode(),
		MCP:             role.Agent.MCP,
		MCPServers:      mcpServers,
		BuildCommands:   buildCommands,
		AllowedCommands: append(append([]string(nil), x.cfg.Project.AllowedCommands...), n.ExtraAllowed...),
		OnActivity:      func(a runner.Activity) { emit(Event{NodeID: n.ID, Type: EventActivity, Activity: &a}) },
		OnUsage: func(u runner.TokenUsage) {
			if throttle.allow() {
				emit(Event{NodeID: n.ID, Type: EventUsage, Live: true, In: u.InputTokens, Cached: u.CachedTokens, Out: u.OutputTokens,
					Estimated: u.Estimated, Runner: cur.Runner, Model: cur.Model})
			}
		},
		OnModel: func(model string) {
			emit(Event{NodeID: n.ID, Type: EventModel, Model: model, Effort: runner.NormalizeEffort(cur.Effort)})
		},
		OnContext: func(tokens int) {
			emit(Event{NodeID: n.ID, Type: EventContext, ContextTokens: tokens, ContextLimit: max(role.Agent.MaxContextTokens, 0)})
		},
	}, func(s string) { emit(Event{NodeID: n.ID, Type: EventOutput, Text: s}) })
}

// liveUsageEvery is the shortest time between two live usage events of a node.
const liveUsageEvery = 500 * time.Millisecond

// usageThrottle lets one live usage event through per liveUsageEvery. The
// final usage event is not throttled, so a dropped snapshot is never lost.
type usageThrottle struct {
	now  func() time.Time
	last time.Time
}

func newUsageThrottle(now func() time.Time) *usageThrottle { return &usageThrottle{now: now} }

func (t *usageThrottle) allow() bool {
	now := t.now()
	if !t.last.IsZero() && now.Sub(t.last) < liveUsageEvery {
		return false
	}
	t.last = now
	return true
}

// unionSkills is the role's own skills followed by the skills the lead chose
// for the node, each once.
func unionSkills(role, node []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range append(append([]string(nil), role...), node...) {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// userSkills loads the file-based skills of a role and returns them as a
// <skills> block plus one warning for each skill that could not be loaded.
func (x *executor) userSkills(names []string) (block string, warnings []string) {
	var sb strings.Builder
	for _, name := range names {
		var doc string
		var err error
		if config.ValidQualifiedName(name) {
			doc, err = plugins.LoadSkill(x.root, name)
		} else {
			doc, err = config.LoadUserSkill(x.root, name)
		}
		switch {
		case errors.Is(err, os.ErrNotExist):
			warnings = append(warnings, fmt.Sprintf("skill %q not found", name))
			continue
		case err != nil:
			warnings = append(warnings, fmt.Sprintf("skill %q: %v", name, err))
			continue
		}
		if sb.Len() == 0 {
			sb.WriteString("<skills>\n")
		}
		fmt.Fprintf(&sb, "### %s\n%s\n", name, strings.TrimSpace(doc))
	}
	if sb.Len() > 0 {
		sb.WriteString("</skills>")
	}
	return sb.String(), warnings
}

// tailLines keeps the most recent (last) whole lines of s within max bytes; a
// single line over the cap is cut from its start.
func tailLines(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[len(s)-max:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 {
		return cut[i+1:]
	}
	for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
		cut = cut[1:]
	}
	return cut
}

// workContext is the project context plus the summaries of the node's
// dependencies.
func (x *executor) workContext(g *Graph, n *Node) string {
	var sb strings.Builder
	sb.WriteString(x.projectContext)
	if x.summary != nil {
		if sum := tailLines(strings.TrimSpace(x.summary()), maxSessionSummaryChars); sum != "" {
			sb.WriteString("\n\n## Earlier in this session\n" + sum + "\n")
		}
	}
	var deps []*Node
	for _, id := range n.DependsOn {
		if d := g.Node(id); d != nil {
			deps = append(deps, d)
		}
	}
	if len(deps) > 0 {
		sb.WriteString("\n\n## Completed dependencies\n")
		for _, d := range deps {
			fmt.Fprintf(&sb, "\n### %s: %s\n", d.ID, d.Title)
			if d.Summary != "" {
				sb.WriteString(d.Summary + "\n")
			}
			if all := d.AllFiles(); len(all) > 0 {
				sb.WriteString("Files: " + strings.Join(all, ", ") + "\n")
			}
		}
	}
	sb.WriteString(x.currentFiles(g, n))
	sb.WriteString(replySection(n))
	return sb.String()
}

// replySection tells a worker what the user replied to the node and, when an
// earlier attempt exists, what it did. "" for a node without a reply.
func replySection(n *Node) string {
	if n.Reply == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n")
	if k := len(n.Attempts); k > 0 {
		prev := n.Attempts[k-1]
		ex := n.replyContext
		if ex == "" {
			ex = ReplyExcerpt(n, prev, ReplyContextChars, nil)
		}
		if ex == "" {
			ex = "(nothing was recorded)"
		}
		state := string(prev.Status)
		after := ""
		if prev.Status == StatusWaiting && prev.Report != nil {
			switch prev.Report.Status {
			case agent.ReportQuestion:
				state += " — asked a question"
				if q := prev.Report.Ask(); q != "" {
					after = "Your question was:\n" + q + "\n\n"
				}
			case agent.ReportBlocked:
				state += " — blocked"
				if s := strings.TrimSpace(prev.Report.Summary); s != "" {
					after = "You reported:\n" + s + "\n\n"
				}
			}
		}
		fmt.Fprintf(&sb, "Previous attempt (attempt %d, %s):\n%s\n\n%s", prev.N, state, ex, after)
	}
	sb.WriteString("The user replied:\n" + n.Reply + "\n")
	return sb.String()
}

// currentFiles renders the on-disk content of the files written by the
// node's transitive dependencies (for a fix node: by every done node), most
// recently written first, within the per-file and total budgets.
func (x *executor) currentFiles(g *Graph, n *Node) string {
	var src []*Node
	if isFixID(n.ID) {
		for _, o := range g.Nodes {
			if o != n && o.Status == StatusDone {
				src = append(src, o)
			}
		}
	} else {
		seen := map[string]bool{}
		var walk func(id string)
		walk = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			if d := g.Node(id); d != nil {
				src = append(src, d)
				for _, dd := range d.DependsOn {
					walk(dd)
				}
			}
		}
		for _, id := range n.DependsOn {
			walk(id)
		}
	}
	sort.SliceStable(src, func(i, j int) bool { return src[i].EndedAt.After(src[j].EndedAt) })

	var sb strings.Builder
	var left []string
	listed := map[string]bool{}
	total := 0
	for _, d := range src {
		for _, rel := range d.AllFiles() {
			if listed[rel] {
				continue
			}
			listed[rel] = true
			content, ok := x.readContextFile(rel)
			if !ok {
				continue
			}
			content = truncateMiddle(content, maxContextFileBytes)
			if total+len(content) > maxContextFilesBytes {
				left = append(left, rel)
				continue
			}
			total += len(content)
			if sb.Len() == 0 {
				sb.WriteString("\n\n## Current files\n\nThese are the latest versions on disk, including changes made by earlier agents.\n")
			}
			fence := "```"
			for strings.Contains(content, fence) {
				fence += "`"
			}
			fmt.Fprintf(&sb, "\n**%s**\n%s\n%s\n%s\n", rel, fence, strings.TrimRight(content, "\n"), fence)
		}
	}
	if len(left) > 0 {
		if sb.Len() == 0 {
			sb.WriteString("\n\n## Current files\n")
		}
		sb.WriteString("\nFiles not shown (size budget, read them if you need them): " + strings.Join(left, ", ") + "\n")
	}
	return sb.String()
}

// readContextFile reads a project file for the context. Missing, non-regular,
// binary or escaping paths are skipped.
func (x *executor) readContextFile(rel string) (string, bool) {
	if !filepath.IsLocal(rel) {
		return "", false
	}
	p := filepath.Join(x.root, rel)
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	data, err := os.ReadFile(p) // #nosec G304 -- rel is checked with filepath.IsLocal and joined under the project root
	if err != nil || agent.IsBinaryContent(data) {
		return "", false
	}
	return string(data), true
}

// truncateMiddle keeps the start and end of s and replaces the middle with an
// "… (N bytes omitted) …" line when s is longer than max bytes.
func truncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := max / 2
	head, tail := s[:half], s[len(s)-half:]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return fmt.Sprintf("%s\n… (%d bytes omitted) …\n%s", head, len(s)-len(head)-len(tail), tail)
}

func (x *executor) runCheck(ctx context.Context, g *Graph, n *Node, emit func(Event)) (Result, error) {
	if !n.ForceRun && !ChangedSinceCheck(g, n) {
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "Build & test skipped: " + SkippedText + "\n"})
		return Result{Summary: "skipped · " + SkippedText, Skipped: true}, nil
	}
	timeout := x.checkTimeout
	if timeout <= 0 {
		timeout = checkTimeout
	}
	root := x.root
	if x.iso != nil {
		var err error
		if root, err = x.iso.IntegrationRoot(); err != nil {
			return Result{}, fmt.Errorf("worktree: %w", err)
		}
	}
	run := executil.NewRunner(root).WithTimeout(timeout)
	commands := n.Commands
	if len(commands) == 0 && x.checkCommands != nil {
		commands = x.checkCommands()
	}
	if len(commands) == 0 {
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "\u26a0 " + noCheckCommandsMsg + "\n"})
		return Result{Summary: noCheckCommandsMsg, Warnings: []string{noCheckCommandsMsg}}, nil
	}
	runID := time.Now().UnixNano()
	for i, cmd := range commands {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		emit(Event{NodeID: n.ID, Type: EventOutput, Text: "$ " + cmd + "\n"})
		act := runner.Activity{
			ID: fmt.Sprintf("check-%s-%d-%d", n.ID, runID, i), Kind: runner.ActCommand,
			State: runner.ActRunning, Title: runner.Clip(cmd, 200), At: time.Now(),
		}
		started := act
		emit(Event{NodeID: n.ID, Type: EventActivity, Activity: &started})
		res := run.RunContext(ctx, cmd)
		out := res.Stdout
		if res.Stderr != "" {
			if out != "" {
				out += "\n"
			}
			out += res.Stderr
		}
		code := res.ExitCode
		act.ExitCode = &code
		act.State = runner.ActDone
		if code != 0 {
			act.State = runner.ActFailed
		}
		act.Detail = runner.Clip(tailLines(out, 2000), 2000)
		act.Duration = time.Since(act.At)
		act.At = time.Now()
		emit(Event{NodeID: n.ID, Type: EventActivity, Activity: &act})
		if out != "" {
			emit(Event{NodeID: n.ID, Type: EventOutput, Text: out + "\n"})
		}
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if res.ExitCode != 0 {
			if out == "" {
				out = fmt.Sprintf("exit code %d", res.ExitCode)
			}
			if len(out) > maxCheckErrorBytes {
				out = out[len(out)-maxCheckErrorBytes:]
			}
			return Result{}, fmt.Errorf("%s", out)
		}
	}
	return Result{Summary: "all checks passed"}, nil
}

// isFixID reports whether id looks like a fix node ID ("F" and digits).
func isFixID(id string) bool {
	return reservedKeyRe.MatchString(id) && id != "CHECK"
}

// maxSnapshotFiles caps the project tree snapshot; above it the detection of
// files changed by the runner itself is skipped for that run.
const maxSnapshotFiles = 20000

// snapSkipDirs are directories the snapshot does not enter.
var snapSkipDirs = map[string]bool{
	".git": true, ".decsr": true, "node_modules": true, "__pycache__": true, "build": true,
	"dist": true, "target": true, "bin": true, "obj": true, ".venv": true, "venv": true,
}

type fileStamp struct {
	size  int64
	mtime time.Time
}

// snapshotTree maps the slash-separated relative path of every regular file
// under root to its size and mtime. ok is false when more than limit files
// were found.
func snapshotTree(root string, limit int) (map[string]fileStamp, bool) {
	snap := map[string]fileStamp{}
	errCap := fmt.Errorf("snapshot cap")
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && snapSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if len(snap) >= limit {
			return errCap
		}
		snap[filepath.ToSlash(rel)] = fileStamp{fi.Size(), fi.ModTime()}
		return nil
	})
	return snap, err == nil
}

// diffSnapshots returns the sorted paths that are new or changed in after and
// not in written.
func diffSnapshots(before, after map[string]fileStamp, written []string) []string {
	skip := make(map[string]bool, len(written))
	for _, w := range written {
		skip[filepath.ToSlash(filepath.Clean(w))] = true
	}
	var out []string
	for p, st := range after { // map keys are unique
		if skip[p] {
			continue
		}
		if old, ok := before[p]; ok && old.size == st.size && old.mtime.Equal(st.mtime) {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// sectionMarker matches a worker section line such as ===SUMMARY===.
var sectionMarker = regexp.MustCompile(`(?m)^[ \t]*===[A-Z][A-Z_]*===[ \t]*$`)

// answerBody is the part of a read-only reply that is shown in the chat as the
// answer: the text before the first section marker (===SUMMARY===). The
// output screen keeps the full text.
func answerBody(body string) string {
	if loc := sectionMarker.FindStringIndex(body); loc != nil {
		return strings.TrimSpace(body[:loc[0]])
	}
	return body
}
