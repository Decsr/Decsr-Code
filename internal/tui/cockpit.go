package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
)

// ── Cockpit view model ───────────────────────────────────────────────────────
//
// The cockpit is the workflow-first replacement for the old single-agent panel
// + system monitor layout. It keeps the phase spine, subtask tree, structured
// agent activity, run inspector, and token ledger on one screen.

type cockpitFocus int

const (
	cockpitFocusWorkflow cockpitFocus = iota
	cockpitFocusCenter
	cockpitFocusInspector
)

type cockpitPhase struct {
	id       string
	label    string
	detail   string
	status   string // done | active | pending
	started  time.Time
	duration time.Duration
}

type cockpitSubTask struct {
	key          string
	title        string
	description  string
	assignee     string
	status       string // pending | active | done | failed
	testStatus   string // pending | active | done | failed
	implStatus   string // pending | active | done | failed
	verifyStatus string // pending | active | done | failed
	depends      []string
	tokens       int
	beadID       string
	priority     int
	files        []string
}

type cockpitAgent struct {
	role     bus.AgentRole
	status   AgentState
	current  string
	last     string
	model    string
	effort   string
	tokens   int
	duration time.Duration
}

type cockpitActivity struct {
	at      time.Time
	role    bus.AgentRole
	kind    string
	text    string
	subtask int
}

type cockpitFile struct {
	path string
	kind string
}

type cockpitTests struct {
	status string // idle | running | pass | fail
	passed int
	failed int
	last   string
}

// CockpitModel is the main run screen model.
type CockpitModel struct {
	cfg    config.Config
	root   string
	wsPath string

	width  int
	height int

	taskTitle string
	pipeline  string
	state     string
	phase     string

	startedAt time.Time
	done      bool
	failed    bool
	errMsg    string

	phases   []cockpitPhase
	subtasks []cockpitSubTask
	agents   map[bus.AgentRole]*cockpitAgent
	activity []cockpitActivity
	files    map[string]cockpitFile
	tests    cockpitTests

	usage      map[bus.AgentRole]bus.AgentUsage
	totalUsage bus.AgentUsage

	tab      int
	focus    cockpitFocus
	selected int // phase index, or subtask index when focus is workflow and subtask row
	selSub   int // -1 when a phase row is selected
	selPhase int

	leftScroll   int
	centerScroll int
	inspector    bool

	gate         string
	currentSub   int
	testTarget   int
	fixRounds    int
	subStartAct  int
	tokenAtStart int
	planLoaded   bool

	lineBuf    map[bus.AgentRole]*strings.Builder
	raw        map[bus.AgentRole]*strings.Builder
	lastEvent  string
	activeRole bus.AgentRole
}

var cockpitTabs = []string{"Activity", "Plan", "Agents", "Tokens", "Files", "Tests", "Output"}

var (
	subTaskLineRe = regexp.MustCompile(`^── Sub-task (\d+)/(\d+)(?: \([^)]*\))?: (.*) ──$`)
	testLineRe    = regexp.MustCompile(`^── Tests (\d+)/(\d+): (.*) ──$`)
	filePathRe    = regexp.MustCompile(`([A-Za-z0-9_./-]+\.(?:go|rs|py|js|jsx|ts|tsx|java|rb|ex|exs|c|cc|cpp|h|hpp|cs|swift|kt|kts|scala|md|json|ya?ml|toml|css|html|sql|sh))`)
	registerRe    = regexp.MustCompile(`registered (T\d+) as ([A-Za-z0-9._-]+)`)
)

func NewCockpit(cfg config.Config, root, wsPath string) CockpitModel {
	c := CockpitModel{
		cfg:        cfg,
		root:       root,
		wsPath:     wsPath,
		agents:     make(map[bus.AgentRole]*cockpitAgent),
		files:      make(map[string]cockpitFile),
		usage:      make(map[bus.AgentRole]bus.AgentUsage),
		lineBuf:    make(map[bus.AgentRole]*strings.Builder),
		raw:        make(map[bus.AgentRole]*strings.Builder),
		currentSub: -1,
		testTarget: -1,
		selSub:     -1,
		tab:        0,
		focus:      cockpitFocusWorkflow,
		inspector:  true,
		state:      "idle",
	}
	for _, p := range []struct{ id, label string }{
		{"context", "Context"},
		{"spec", "Spec"},
		{"plan", "Plan"},
		{"build", "Build"},
		{"verify", "Verify"},
		{"review", "Review"},
		{"done", "Done"},
	} {
		c.phases = append(c.phases, cockpitPhase{id: p.id, label: p.label, status: "pending"})
	}
	for _, role := range agentOrder {
		ac := cfg.Agents[string(role)]
		c.agents[role] = &cockpitAgent{
			role:   role,
			status: AgentWaiting,
			model:  ac.Model,
			effort: ac.Effort,
		}
	}
	c.tests.status = "idle"
	return c
}

func (c *CockpitModel) SetSize(width, height int) {
	if width < 20 {
		width = 20
	}
	if height < 8 {
		height = 8
	}
	c.width, c.height = width, height
}

func (c *CockpitModel) SetTaskInput(input string) {
	input = strings.TrimSpace(input)
	if input == "" {
		return
	}
	if c.taskTitle == "" {
		c.taskTitle = cockpitFirstLine(input)
	}
}

func (c *CockpitModel) SetStarted(t time.Time) {
	if c.startedAt.IsZero() {
		c.startedAt = t
	}
}

func (c *CockpitModel) SetActiveRole(role bus.AgentRole) {
	if role != "" && role != bus.RoleSystem {
		c.activeRole = role
	}
}

func (c *CockpitModel) SetError(err error) {
	c.done = true
	if err != nil {
		c.failed = true
		c.errMsg = err.Error()
		c.tests.status = "fail"
	}
}

func (c *CockpitModel) SetDone(err error) {
	c.SetError(err)
	c.gate = ""
	if err == nil {
		c.done = true
		c.setPhase("done", "run complete")
		for i := range c.subtasks {
			c.subtasks[i].status = "done"
			c.subtasks[i].testStatus = "done"
			c.subtasks[i].implStatus = "done"
			c.subtasks[i].verifyStatus = "done"
		}
	}
}

func (c *CockpitModel) SetGate(filename string) {
	c.gate = filename
}

func (c *CockpitModel) ClearGate() {
	c.gate = ""
}

func (c *CockpitModel) ToggleInspector() {
	c.inspector = !c.inspector
}

func (c *CockpitModel) CycleFocus(dir int) {
	if c.width < 110 || !c.inspector {
		if c.focus == cockpitFocusWorkflow {
			c.focus = cockpitFocusCenter
		} else {
			c.focus = cockpitFocusWorkflow
		}
		return
	}
	n := 3
	c.focus = cockpitFocus((int(c.focus) + dir + n) % n)
}

func (c *CockpitModel) SetTab(i int) {
	if i >= 0 && i < len(cockpitTabs) {
		c.tab = i
		c.centerScroll = 0
	}
}

// LoadPlan reads the structured subtask plan from the workspace, falling back
// to task_plan.md. It preserves status and token data across reloads.
func (c *CockpitModel) LoadPlan() {
	if c.wsPath == "" {
		return
	}
	if c.taskTitle == "" {
		if data, err := os.ReadFile(filepath.Join(c.wsPath, "task_spec.json")); err == nil { // #nosec G304 -- workspace path
			var spec struct {
				Title string `json:"title"`
			}
			if json.Unmarshal(data, &spec) == nil {
				c.taskTitle = strings.TrimSpace(spec.Title)
			}
		}
	}
	old := make(map[string]cockpitSubTask, len(c.subtasks))
	for _, t := range c.subtasks {
		old[t.key] = t
	}

	var tasks []agent.SubTask
	var idByKey map[string]string

	jsonPath := filepath.Join(c.wsPath, "sub_tasks.json")
	markdownPath := filepath.Join(c.wsPath, "task_plan.md")
	// Prefer the Markdown plan while it is newer than the sidecar. This matters
	// at the plan gate: the PM writes task_plan.md before sub_tasks.json, so a
	// stale sidecar from a previous run must not win.
	useMarkdown := false
	if planInfo, err := os.Stat(markdownPath); err == nil {
		if jsonInfo, jerr := os.Stat(jsonPath); jerr != nil || !planInfo.ModTime().Before(jsonInfo.ModTime()) {
			useMarkdown = true
		}
	}
	if !useMarkdown {
		if data, err := os.ReadFile(jsonPath); err == nil { // #nosec G304 -- workspace path
			var plan struct {
				Tasks   []agent.SubTask   `json:"tasks"`
				IDByKey map[string]string `json:"id_by_key"`
			}
			if json.Unmarshal(data, &plan) == nil && len(plan.Tasks) > 0 {
				tasks = plan.Tasks
				idByKey = plan.IDByKey
			}
		}
	}
	if len(tasks) == 0 {
		tasks = parseTaskPlanFile(markdownPath)
	}
	if len(tasks) == 0 {
		return
	}

	seen := make(map[string]bool, len(tasks))
	next := make([]cockpitSubTask, 0, len(tasks)+len(old))
	for _, t := range tasks {
		seen[t.Key] = true
		st := cockpitSubTask{
			key:          t.Key,
			title:        t.Title,
			description:  t.Description,
			depends:      append([]string(nil), t.DependsOn...),
			priority:     t.Priority,
			assignee:     "coder",
			status:       "pending",
			testStatus:   "pending",
			implStatus:   "pending",
			verifyStatus: "pending",
		}
		if v, ok := old[t.Key]; ok {
			st = v
			st.title = t.Title
			st.description = t.Description
			st.depends = append([]string(nil), t.DependsOn...)
			st.priority = t.Priority
		}
		if idByKey != nil {
			st.beadID = idByKey[t.Key]
		}
		next = append(next, st)
	}
	// Preserve fix-round tasks already merged in earlier loads.
	for _, t := range old {
		if !seen[t.key] {
			next = append(next, t)
			seen[t.key] = true
		}
	}
	c.subtasks = next
	c.planLoaded = true
	if len(c.subtasks) > 0 && c.selSub < 0 {
		c.selSub = 0
		c.selected = 0
		c.selPhase = phaseIndex(c.phases, "build")
	}
}

func parseTaskPlanFile(path string) []agent.SubTask {
	data, err := os.ReadFile(path) // #nosec G304 -- workspace path
	if err != nil {
		return nil
	}
	var tasks []agent.SubTask
	var cur *agent.SubTask
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			key, name := title, title
			if i := strings.Index(title, " — "); i >= 0 {
				key, name = strings.TrimSpace(title[:i]), strings.TrimSpace(title[i+len(" — "):])
			}
			if !isSubTaskKey(key) {
				// e.g. "## Reviewer Notes" appended after the plan.
				cur = nil
				continue
			}
			tasks = append(tasks, agent.SubTask{Key: key, Title: name, Priority: 2})
			cur = &tasks[len(tasks)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "Priority:") {
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "Priority:")), "%d", &cur.Priority)
		}
		if strings.HasPrefix(line, "Depends on:") {
			for _, d := range strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "Depends on:")), ",") {
				if d = strings.TrimSpace(d); d != "" {
					cur.DependsOn = append(cur.DependsOn, d)
				}
			}
		}
	}
	for i := range tasks {
		if tasks[i].Priority == 0 {
			tasks[i].Priority = 2
		}
	}
	return tasks
}

// Observe consumes a bus message and updates the workflow view.
func (c *CockpitModel) Observe(msg bus.Message) {
	switch msg.Type {
	case bus.MsgTaskSpec:
		if s, ok := msg.Payload.(string); ok {
			var spec struct {
				Title string `json:"title"`
			}
			if json.Unmarshal([]byte(s), &spec) == nil && strings.TrimSpace(spec.Title) != "" {
				c.taskTitle = strings.TrimSpace(spec.Title)
			}
		}
	case bus.MsgHumanGate:
		if s, ok := msg.Payload.(string); ok {
			c.gate = s
			c.lastEvent = "waiting for approval: " + gateLabel(s)
			if s == "task_plan.md" {
				// The plan is written before the gate opens. Show it in the
				// Plan tab so the human can review the decomposition in place.
				c.LoadPlan()
				c.tab = 1
				c.focus = cockpitFocusCenter
			}
		}
	case bus.MsgUsage:
		if u, ok := msg.Payload.(bus.AgentUsage); ok {
			total := u.InputTokens + u.OutputTokens
			cur := c.usage[msg.From]
			cur.InputTokens += u.InputTokens
			cur.OutputTokens += u.OutputTokens
			if u.Estimated {
				cur.Estimated = true
			}
			c.usage[msg.From] = cur
			c.totalUsage.InputTokens += u.InputTokens
			c.totalUsage.OutputTokens += u.OutputTokens
			if u.Estimated {
				c.totalUsage.Estimated = true
			}
			if a := c.agents[msg.From]; a != nil {
				a.tokens += total
			}
			if c.currentSub >= 0 && c.phase == "build" && (msg.From == bus.RoleCoder || msg.From == bus.RoleQA) {
				c.subtasks[c.currentSub].tokens += total
			}
		}
	case bus.MsgRequest:
		if a := c.agents[msg.To]; a != nil {
			a.status = AgentRunning
		}
		c.setActiveAgent(msg.To)
	case bus.MsgResponse:
		if a := c.agents[msg.From]; a != nil {
			a.status = AgentDone
		}
		if report, ok := msg.Payload.(agent.TestReport); ok {
			if report.Success {
				c.tests.status = "pass"
				c.tests.passed = len(report.Commands)
				c.tests.failed = 0
			} else {
				c.tests.status = "fail"
				c.tests.failed = 1
			}
		}
	case bus.MsgEvent:
		switch p := msg.Payload.(type) {
		case bus.TokenPayload:
			if !p.Done {
				c.observeToken(msg.From, p.Text)
			}
		case string:
			c.observeEvent(msg.From, msg.To, p)
		}
	}
}

func (c *CockpitModel) setActiveAgent(role bus.AgentRole) {
	if role == "" || role == bus.RoleSystem {
		return
	}
	if a := c.agents[role]; a != nil && (a.status == AgentWaiting || a.status == AgentDone) {
		a.status = AgentRunning
	}
}

func (c *CockpitModel) observeToken(role bus.AgentRole, text string) {
	if role == "" || role == bus.RoleSystem {
		role = bus.RoleCoder
	}
	c.activeRole = role
	if c.raw[role] == nil {
		c.raw[role] = &strings.Builder{}
	}
	if c.raw[role].Len() < 24000 {
		c.raw[role].WriteString(text)
	}
	if c.lineBuf[role] == nil {
		c.lineBuf[role] = &strings.Builder{}
	}
	c.lineBuf[role].WriteString(text)
	for {
		s := c.lineBuf[role].String()
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			break
		}
		line := s[:i]
		c.lineBuf[role].Reset()
		c.lineBuf[role].WriteString(s[i+1:])
		c.addActivity(role, line)
	}
	c.setActiveAgent(role)
}

func (c *CockpitModel) addActivity(role bus.AgentRole, line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	kind := "out"
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "⚙ "):
		low := strings.ToLower(trimmed)
		switch {
		case strings.Contains(low, "edit"):
			kind = "edit"
		case strings.Contains(low, "write"):
			kind = "write"
		case strings.Contains(low, "read"):
			kind = "read"
		case strings.Contains(low, "bash"), strings.Contains(low, "shell"):
			kind = "cmd"
		default:
			kind = "tool"
		}
	case strings.HasPrefix(trimmed, "$ "):
		kind = "cmd"
	case strings.HasPrefix(trimmed, "wrote test:"):
		kind = "test"
	case strings.HasPrefix(trimmed, "wrote "):
		kind = "write"
	case strings.HasPrefix(trimmed, "✗") || strings.HasPrefix(trimmed, "error:"):
		kind = "error"
	case strings.Contains(strings.ToLower(trimmed), "passed") || strings.Contains(strings.ToLower(trimmed), "failed"):
		kind = "test"
	}
	item := cockpitActivity{at: time.Now(), role: role, kind: kind, text: trimmed, subtask: c.currentSub}
	if n := len(c.activity); n > 0 {
		last := c.activity[n-1]
		if last.role == item.role && last.text == item.text {
			return
		}
	}
	c.activity = append(c.activity, item)
	if len(c.activity) > 600 {
		c.activity = c.activity[len(c.activity)-600:]
		c.subStartAct -= 1
	}
	if a := c.agents[role]; a != nil {
		a.last = trimmed
		if a.status == AgentWaiting {
			a.status = AgentRunning
		}
	}
	c.observeActivityEvidence(role, trimmed)
}

func (c *CockpitModel) observeActivityEvidence(role bus.AgentRole, text string) {
	// File changes and test commands are the evidence the inspector needs.
	for _, m := range filePathRe.FindAllString(text, -1) {
		if strings.HasPrefix(m, "http") {
			continue
		}
		kind := "file"
		low := strings.ToLower(text)
		switch {
		case strings.Contains(low, "wrote test"):
			kind = "test"
		case strings.Contains(low, "edit"), strings.Contains(low, "write"):
			kind = "edit"
		case strings.Contains(low, "read"):
			kind = "read"
		}
		c.files[m] = cockpitFile{path: m, kind: kind}
		if c.currentSub >= 0 {
			found := false
			for _, f := range c.subtasks[c.currentSub].files {
				if f == m {
					found = true
					break
				}
			}
			if !found {
				c.subtasks[c.currentSub].files = append(c.subtasks[c.currentSub].files, m)
			}
		}
	}
	low := strings.ToLower(text)
	if strings.HasPrefix(strings.TrimSpace(text), "$ ") && (strings.Contains(low, "test") || strings.Contains(low, "build")) {
		c.tests.status = "running"
		c.tests.last = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "$"))
	}
	if strings.Contains(low, "all tests passed") {
		c.tests.status = "pass"
	}
	if strings.Contains(low, "tests failed") || strings.Contains(low, "build failed") {
		c.tests.status = "fail"
	}
	_ = role
}

func (c *CockpitModel) observeEvent(from, to bus.AgentRole, s string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	c.lastEvent = s
	if strings.HasPrefix(s, "pipeline: ") {
		c.pipeline = strings.TrimSpace(strings.TrimPrefix(s, "pipeline: "))
	}
	if strings.HasPrefix(s, "starting ") {
		role := bus.AgentRole(strings.TrimSpace(strings.TrimPrefix(s, "starting ")))
		c.setActiveAgent(role)
	}
	for _, st := range []string{"idle", "compacting", "negotiating", "research", "planning", "coder", "poc", "qa_tests", "qa_review", "ux_reviewer", "security", "coder_fixer", "done", "gate", "rate_limited"} {
		if s == st {
			c.state = st
			c.applyStatePhase(st)
		}
	}
	switch {
	case strings.Contains(s, "── Sub-task "):
		c.observeSubTask(s)
	case strings.Contains(s, "── Tests "):
		c.observeTests(s)
	case strings.Contains(s, "PM decomposing"):
		c.setPhase("plan", "decomposing")
	case strings.Contains(s, "decomposed into"):
		c.setPhase("plan", "plan ready")
		c.LoadPlan()
	case strings.Contains(s, "research ready"):
		c.setPhase("plan", "research ready")
	case strings.Contains(s, "PM analyzing"):
		c.setPhase("spec", "negotiating")
	case strings.Contains(s, "negotiation complete"):
		c.setPhase("spec", "spec ready")
	case strings.Contains(s, "QA writing tests"):
		c.tests.status = "running"
		c.setPhase("build", "tests first")
		if c.testTarget >= 0 {
			c.subtasks[c.testTarget].testStatus = "active"
		}
	case strings.HasPrefix(s, "QA wrote "):
		if c.testTarget >= 0 {
			c.subtasks[c.testTarget].testStatus = "done"
		}
	case strings.Contains(s, "running build and tests"):
		c.tests.status = "running"
		c.setPhase("verify", "build/test/fix")
		if c.currentSub >= 0 {
			c.subtasks[c.currentSub].verifyStatus = "active"
		}
	case strings.Contains(s, "all tests passed"):
		c.tests.status = "pass"
		if c.currentSub >= 0 {
			c.subtasks[c.currentSub].verifyStatus = "done"
		}
	case strings.Contains(s, "coder failed"):
		c.tests.status = "fail"
		if c.currentSub >= 0 {
			c.subtasks[c.currentSub].verifyStatus = "failed"
		}
	case strings.Contains(s, "build-fix loop stuck"):
		c.tests.status = "fail"
		c.failed = true
		c.errMsg = s
	case strings.Contains(s, "PM verdict"):
		c.setPhase("review", "PM arbitration")
		if a := c.agents[bus.RolePM]; a != nil {
			a.status = AgentDone
		}
		if strings.Contains(s, "issue(s) to fix") {
			c.fixRounds++
			c.LoadPlan()
		}
	case strings.Contains(s, "quality review passed"):
		c.setPhase("review", "all checks clean")
		for _, role := range []bus.AgentRole{bus.RolePM, bus.RoleQA, bus.RoleUXReviewer, bus.RoleSecurity} {
			if a := c.agents[role]; a != nil {
				a.status = AgentDone
			}
		}
	case strings.Contains(s, "QA reviewing code quality"):
		c.setPhase("review", "QA review")
		c.setActiveAgent(bus.RoleQA)
	case strings.Contains(s, "QA review passed"):
		if a := c.agents[bus.RoleQA]; a != nil {
			a.status = AgentDone
		}
	case strings.Contains(s, "UX reviewing"):
		c.setPhase("review", "UX review")
		c.setActiveAgent(bus.RoleUXReviewer)
	case strings.Contains(s, "UX review passed"):
		if a := c.agents[bus.RoleUXReviewer]; a != nil {
			a.status = AgentDone
		}
	case strings.Contains(s, "Security reviewing"):
		c.setPhase("review", "security audit")
		c.setActiveAgent(bus.RoleSecurity)
	case strings.Contains(s, "Security review passed"):
		if a := c.agents[bus.RoleSecurity]; a != nil {
			a.status = AgentDone
		}
	case strings.Contains(s, "PM arbitrating"):
		c.setPhase("review", "PM arbitration")
		c.setActiveAgent(bus.RolePM)
	case strings.Contains(s, "task complete"):
		c.SetDone(nil)
		for _, a := range c.agents {
			a.status = AgentDone
		}
	case strings.Contains(s, "rate limit") || strings.Contains(s, "⛔"):
		c.failed = true
		c.errMsg = s
	}
	if m := registerRe.FindStringSubmatch(s); len(m) == 3 {
		for i := range c.subtasks {
			if c.subtasks[i].key == m[1] {
				c.subtasks[i].beadID = m[2]
			}
		}
	}
	if strings.Contains(s, "closed beads issue") {
		id := strings.TrimSpace(strings.TrimPrefix(s, "closed beads issue"))
		for i := range c.subtasks {
			if c.subtasks[i].beadID == id {
				c.subtasks[i].status = "done"
				c.subtasks[i].implStatus = "done"
			}
		}
	}
	_ = from
	_ = to
}

func (c *CockpitModel) observeSubTask(s string) {
	m := subTaskLineRe.FindStringSubmatch(s)
	if len(m) != 4 {
		return
	}
	idx := -1
	title := strings.TrimSpace(m[3])
	for i := range c.subtasks {
		if c.subtasks[i].title == title {
			idx = i
			break
		}
	}
	if idx < 0 {
		// New fix-round task not yet loaded; reload the sidecar.
		c.LoadPlan()
		for i := range c.subtasks {
			if c.subtasks[i].title == title {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return
	}
	for i := range c.subtasks {
		if c.subtasks[i].status == "active" && i != idx {
			c.subtasks[i].status = "done"
			c.subtasks[i].implStatus = "done"
		}
	}
	c.subtasks[idx].status = "active"
	c.subtasks[idx].implStatus = "active"
	c.subtasks[idx].assignee = "coder"
	c.currentSub = idx
	c.selSub = idx
	c.selected = idx
	c.state = "coder"
	c.activeRole = bus.RoleCoder
	if c.tab == 1 {
		c.tab = 0 // leave the Plan gate view once implementation starts
	}
	c.subStartAct = len(c.activity)
	c.tokenAtStart = c.totalUsage.InputTokens + c.totalUsage.OutputTokens
	c.setPhase("build", title)
	if a := c.agents[bus.RoleCoder]; a != nil {
		a.status = AgentRunning
		a.current = title
	}
}

func (c *CockpitModel) observeTests(s string) {
	m := testLineRe.FindStringSubmatch(s)
	if len(m) != 4 {
		return
	}
	title := strings.TrimSpace(m[3])
	idx := -1
	for i := range c.subtasks {
		if c.subtasks[i].title == title {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	for i := range c.subtasks {
		if c.subtasks[i].testStatus == "active" && i != idx {
			c.subtasks[i].testStatus = "done"
		}
	}
	c.subtasks[idx].testStatus = "active"
	c.testTarget = idx
	c.tests.status = "running"
	c.tests.last = "TDD tests for " + title
	c.setPhase("build", "tests first")
}

func (c *CockpitModel) applyStatePhase(state string) {
	switch state {
	case "compacting":
		c.setPhase("context", "repository context")
	case "negotiating", "pm":
		c.setPhase("spec", "task spec")
	case "research", "planning":
		c.setPhase("plan", "planning")
	case "coder", "poc":
		c.setPhase("build", "implementation")
	case "qa_tests":
		if c.phase == "build" {
			c.setPhase("build", "tests first")
		} else {
			c.setPhase("verify", "tests")
		}
	case "coder_fixer":
		c.setPhase("verify", "build/test/fix")
	case "qa_review", "ux_reviewer", "security":
		c.setPhase("review", "quality review")
	case "done":
		c.setPhase("done", "run complete")
	case "rate_limited":
		c.failed = true
	}
}

func (c *CockpitModel) setPhase(id, detail string) {
	idx := -1
	for i := range c.phases {
		if c.phases[i].id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	if c.phase == id {
		if detail != "" {
			c.phases[idx].detail = detail
		}
		return
	}
	now := time.Now()
	for i := range c.phases {
		if c.phases[i].status == "active" {
			c.phases[i].duration = now.Sub(c.phases[i].started)
		}
		switch {
		case i < idx:
			c.phases[i].status = "done"
		case i == idx:
			c.phases[i].status = "active"
			c.phases[i].started = now
			c.phases[i].detail = detail
		default:
			c.phases[i].status = "pending"
		}
	}
	c.phase = id
}

func (c *CockpitModel) nextAction() string {
	switch {
	case c.gate != "":
		return "Review and approve " + gateLabel(c.gate) + "  Ctrl+A"
	case c.failed:
		return "Retry or resume: " + truncateOneLine(c.errMsg, 48)
	case c.done:
		return "Review the summary; Esc/M for menu"
	case c.phase == "context":
		return "Review the context pack"
	case c.phase == "spec":
		return "Approve the task spec"
	case c.phase == "plan":
		return "Review the subtask plan"
	case c.phase == "build":
		if c.currentSub >= 0 && c.currentSub < len(c.subtasks) {
			return "Finish " + c.subtasks[c.currentSub].key + " → global verification"
		}
		return "Implement the next subtask"
	case c.phase == "verify":
		if c.tests.status == "fail" {
			return "Fix the failing build/tests"
		}
		return "Run the global build/test/fix loop"
	case c.phase == "review":
		return "Await PM arbitration"
	}
	return "Waiting for the pipeline"
}

func (c *CockpitModel) elapsed() time.Duration {
	if c.startedAt.IsZero() {
		return 0
	}
	return time.Since(c.startedAt)
}

func (c *CockpitModel) View() string {
	if c.width <= 0 || c.height <= 0 {
		return ""
	}
	if c.width < 60 || c.height < 12 {
		return lipgloss.NewStyle().
			Foreground(crt.warn).
			Render(fmt.Sprintf("Terminal too small: %dx%d (minimum 60x12)", c.width, c.height))
	}
	header := c.renderTaskHeader()
	bodyH := c.height - 2
	if bodyH < 5 {
		bodyH = 5
	}
	var body string
	if c.width >= 110 {
		leftW := 30
		rightW := 34
		if c.width >= 150 {
			leftW = 34
			rightW = 40
		}
		centerW := c.width - leftW - rightW - 2
		left := c.renderLeft(leftW, bodyH)
		center := c.renderCenter(centerW, bodyH)
		right := c.renderInspector(rightW, bodyH)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, " ", center, " ", right)
	} else {
		leftW := 26
		if c.width < 90 {
			leftW = 22
		}
		centerW := c.width - leftW - 1
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			c.renderLeft(leftW, bodyH), " ", c.renderCenter(centerW, bodyH),
		)
	}
	footer := c.renderFooter()
	return strings.Join([]string{header, body, footer}, "\n")
}

func (c *CockpitModel) renderTaskHeader() string {
	title := c.taskTitle
	if title == "" {
		title = "New task"
	}
	left := lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("TASK  ") +
		lipgloss.NewStyle().Foreground(crt.bright).Render(truncateOneLine(title, max(10, c.width-56)))
	meta := ""
	if c.pipeline != "" {
		meta += " " + chip(strings.ToUpper(c.pipeline), galaxyCyan)
	}
	state := c.state
	if c.phase != "" {
		state = c.phase
	}
	if c.failed {
		state = "error"
	} else if c.gate != "" {
		state = "gate"
	} else if c.done {
		state = "done"
	}
	if state != "" {
		meta += " " + chip(strings.ToUpper(state), stateColor(state))
	}
	elapsed := ""
	if e := c.elapsed(); e > 0 {
		elapsed = "  " + formatElapsed(e.Round(time.Second))
	}
	line := left + meta + elapsed
	if ansi.StringWidth(line) > c.width {
		line = ansi.Truncate(line, c.width, "…")
	}
	return line
}

func stateColor(state string) lipgloss.Color {
	switch state {
	case "done":
		return galaxyMint
	case "error", "rate_limited":
		return galaxyRed
	case "gate":
		return galaxyGold
	case "build", "coder":
		return galaxyMint
	case "review":
		return galaxyPink
	default:
		return galaxyBlue
	}
}

func (c *CockpitModel) renderLeft(w, h int) string {
	rows := c.leftRows()
	if c.leftScroll >= len(rows) && len(rows) > 0 {
		c.leftScroll = len(rows) - 1
	}
	sel := c.leftSelectedRow(rows)
	if sel >= 0 {
		visible := h - 2
		if visible < 3 {
			visible = 3
		}
		if sel < c.leftScroll {
			c.leftScroll = sel
		}
		if sel >= c.leftScroll+visible {
			c.leftScroll = sel - visible + 1
		}
	}
	lines := make([]string, 0, h)
	for i := c.leftScroll; i < len(rows) && len(lines) < h-2; i++ {
		line := rows[i].text
		if i == sel {
			line = lipgloss.NewStyle().Background(surfaceRaised).Bold(true).Render(padRight(line, w-2))
		}
		lines = append(lines, line)
	}
	return cockpitPane("WORKFLOW", w, h, lines, c.focus == cockpitFocusWorkflow, galaxyViolet)
}

type cockpitLeftRow struct {
	text  string
	phase int
	sub   int
}

func (c *CockpitModel) leftRows() []cockpitLeftRow {
	var rows []cockpitLeftRow
	for pi := range c.phases {
		p := &c.phases[pi]
		glyph := "○"
		style := lipgloss.NewStyle().Foreground(crt.dim)
		switch p.status {
		case "done":
			glyph = "✓"
			style = lipgloss.NewStyle().Foreground(galaxyCyan)
		case "active":
			glyph = "▸"
			style = lipgloss.NewStyle().Foreground(galaxyViolet).Bold(true)
		}
		dur := ""
		phaseDur := p.duration
		if p.status == "active" && !p.started.IsZero() {
			phaseDur = time.Since(p.started)
		}
		if phaseDur > 0 {
			dur = "  " + formatElapsed(phaseDur.Round(time.Second))
		}
		rows = append(rows, cockpitLeftRow{
			text:  style.Render(glyph) + " " + style.Render(fmt.Sprintf("%-10s", p.label)) + lipgloss.NewStyle().Foreground(crt.dim).Render(dur),
			phase: pi,
			sub:   -1,
		})
		if p.status == "active" && p.detail != "" {
			rows = append(rows, cockpitLeftRow{
				text:  "  " + lipgloss.NewStyle().Foreground(crt.dim).Render(truncateOneLine(p.detail, 22)),
				phase: pi,
				sub:   -1,
			})
		}
		if p.id == "build" {
			titleW := 20
			if c.width >= 140 {
				titleW = 26
			} else if c.width < 100 {
				titleW = 13
			}
			for si := range c.subtasks {
				st := &c.subtasks[si]
				g, stStyle := statusGlyph(st.status)
				title := truncateOneLine(st.title, titleW)
				rows = append(rows, cockpitLeftRow{
					text:  "  " + stStyle.Render(g) + " " + lipgloss.NewStyle().Foreground(crt.bright).Render(st.key+" "+title),
					phase: pi,
					sub:   si,
				})
				detail := "     " + lipgloss.NewStyle().Foreground(crt.dim).Render(st.assignee)
				if st.tokens > 0 {
					detail += lipgloss.NewStyle().Foreground(crt.dim).Render(" · " + formatTokensCompact(st.tokens))
				}
				if len(st.depends) > 0 {
					detail += lipgloss.NewStyle().Foreground(crt.muted).Render(" · dep " + strings.Join(st.depends, ","))
				}
				rows = append(rows, cockpitLeftRow{text: detail, phase: pi, sub: -1})
			}
		}
	}
	return rows
}

func (c *CockpitModel) leftSelectedRow(rows []cockpitLeftRow) int {
	if c.focus != cockpitFocusWorkflow {
		return -1
	}
	if c.selSub >= 0 {
		for i, r := range rows {
			if r.sub == c.selSub {
				return i
			}
		}
	}
	for i, r := range rows {
		if r.phase == c.selPhase && r.sub < 0 {
			return i
		}
	}
	return -1
}

func statusGlyph(status string) (string, lipgloss.Style) {
	switch status {
	case "done":
		return "✓", lipgloss.NewStyle().Foreground(galaxyMint)
	case "active", "running":
		return "▸", lipgloss.NewStyle().Foreground(galaxyCyan).Bold(true)
	case "failed", "error":
		return "!", lipgloss.NewStyle().Foreground(galaxyRed).Bold(true)
	default:
		return "○", lipgloss.NewStyle().Foreground(crt.dim)
	}
}

func (c *CockpitModel) renderCenter(w, h int) string {
	header := c.centerHeader(w)
	tabs := c.renderTabs(w)
	content := c.centerLines(w, h-4)
	body := append([]string{header, tabs}, content...)
	return cockpitPane(cockpitTabs[c.tab], w, h, body, c.focus == cockpitFocusCenter, galaxyMint)
}

func (c *CockpitModel) centerHeader(w int) string {
	var left string
	if c.selSub >= 0 && c.selSub < len(c.subtasks) {
		st := c.subtasks[c.selSub]
		g, style := statusGlyph(st.status)
		left = style.Render(g) + " " + lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(st.key+" "+st.title)
	} else if c.phase != "" {
		left = lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(strings.ToUpper(c.phase))
	}
	if ansi.StringWidth(left) > w-4 {
		left = ansi.Truncate(left, w-4, "…")
	}
	return left
}

func (c *CockpitModel) renderTabs(w int) string {
	var parts []string
	short := []string{"Act", "Plan", "Agt", "Tok", "Files", "Test", "Out"}
	for i := range cockpitTabs {
		label := fmt.Sprintf("%d %s", i+1, short[i])
		if i == c.tab {
			parts = append(parts, lipgloss.NewStyle().Background(galaxyMint).Foreground(crt.bg).Bold(true).Render(label))
		} else {
			parts = append(parts, lipgloss.NewStyle().Foreground(crt.dim).Render(label))
		}
	}
	line := strings.Join(parts, " ")
	if ansi.StringWidth(line) > w-2 {
		line = ansi.Truncate(line, w-2, "…")
	}
	return line
}

func (c *CockpitModel) centerLines(w, h int) []string {
	switch c.tab {
	case 1:
		return c.planLines(w, h)
	case 2:
		return c.agentLines(w, h)
	case 3:
		return c.tokenLines(w, h)
	case 4:
		return c.fileLines(w, h)
	case 5:
		return c.testLines(w, h)
	case 6:
		return c.outputLines(w, h)
	default:
		return c.activityLines(w, h)
	}
}

func (c *CockpitModel) activityLines(w, h int) []string {
	start := 0
	if c.selSub >= 0 && c.selSub == c.currentSub {
		start = c.subStartAct
	}
	if start < 0 {
		start = 0
	}
	if start >= len(c.activity) && len(c.activity) > 0 {
		start = max(0, len(c.activity)-20)
	}
	var lines []string
	limit := w - 2
	for _, a := range c.activity[start:] {
		if len(lines) >= h {
			break
		}
		role := roleShort(a.role)
		roleStyle := lipgloss.NewStyle().Foreground(roleColor(string(a.role))).Bold(true)
		kindStyle := lipgloss.NewStyle().Foreground(activityColor(a.kind))
		textStyle := lipgloss.NewStyle().Foreground(crt.bright)
		line := lipgloss.NewStyle().Foreground(crt.dim).Render(a.at.Format("15:04")) + "  " +
			roleStyle.Render(fmt.Sprintf("%-5s", role)) + "  " +
			kindStyle.Render(fmt.Sprintf("%-5s", a.kind)) + "  " +
			textStyle.Render(ansi.Truncate(a.text, max(8, limit-22), "…"))
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render("  waiting for agent activity…"))
	}
	return windowLines(lines, h, c.centerScroll)
}

func activityColor(kind string) lipgloss.Color {
	switch kind {
	case "cmd":
		return galaxyCyan
	case "test":
		return galaxyMint
	case "error":
		return galaxyRed
	case "tool":
		return galaxyBlue
	case "write", "edit":
		return galaxyGold
	default:
		return crt.dim
	}
}

func (c *CockpitModel) planLines(w, h int) []string {
	lines := []string{
		lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render(fmt.Sprintf("%-4s %-30s %-7s %7s  %s", "ST", "SUBTASK", "OWNER", "TOKENS", "DEPENDS")),
	}
	for i := range c.subtasks {
		st := &c.subtasks[i]
		g, style := statusGlyph(st.status)
		title := ansi.Truncate(st.title, max(10, w-50), "…")
		dep := "—"
		if len(st.depends) > 0 {
			dep = strings.Join(st.depends, ",")
		}
		line := fmt.Sprintf("%s %-4s %-30s %-7s %7s  %s",
			style.Render(g), st.key, title, st.assignee, formatTokensCompact(st.tokens), dep)
		if i == c.selSub {
			line = lipgloss.NewStyle().Background(surfaceRaised).Bold(true).Render(padRight(line, w-2))
		}
		lines = append(lines, line)
	}
	if len(c.subtasks) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render("  the PM has not decomposed the task yet…"))
	}
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) agentLines(w, h int) []string {
	lines := []string{
		lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render(fmt.Sprintf("%-9s %-9s %-28s %7s  %s", "ROLE", "STATE", "CURRENT HANDOFF", "TOKENS", "LAST")),
	}
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil {
			continue
		}
		g, style := statusGlyph(string(a.status))
		current := a.current
		if current == "" {
			current = "—"
		}
		last := a.last
		if last == "" {
			last = "—"
		}
		line := fmt.Sprintf("%s %-8s %-8s %-28s %7s  %s",
			style.Render(g), roleLabel(role), agentStatusLabel(a.status),
			ansi.Truncate(current, 28, "…"), formatTokensCompact(a.tokens), ansi.Truncate(last, max(6, w-58), "…"))
		lines = append(lines, line)
		meta := a.model
		if a.effort != "" && a.effort != "default" {
			meta += " · " + a.effort
		}
		if meta != "" {
			lines = append(lines, "  "+lipgloss.NewStyle().Foreground(crt.dim).Render(truncateOneLine(meta, w-6)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render("  HANDOFFS  PM → Coder → QA → UX + Security → PM"))
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render("  Waiting is not failure; the inspector shows the next gate."))
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) tokenLines(w, h int) []string {
	total := c.totalUsage.InputTokens + c.totalUsage.OutputTokens
	lines := []string{
		lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(fmt.Sprintf("TOTAL %s   in %s   out %s   %s",
			formatTokensCompact(total), formatTokensCompact(c.totalUsage.InputTokens), formatTokensCompact(c.totalUsage.OutputTokens), estimatedMark(c.totalUsage.Estimated))),
		"",
	}
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil {
			continue
		}
		share := 0
		if total > 0 {
			share = a.tokens * 100 / total
		}
		lines = append(lines, fmt.Sprintf("%-9s %7s  %s %3d%%",
			roleLabel(role), formatTokensCompact(a.tokens), tokenBarColor(share, 20, roleColor(string(role))), share))
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render("SUBTASK USAGE"))
	for i := range c.subtasks {
		st := &c.subtasks[i]
		lines = append(lines, fmt.Sprintf("  %-3s %6s  %s", st.key, formatTokensCompact(st.tokens), st.title))
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render("Estimates are marked ~; unreported usage is shown as —."))
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) fileLines(w, h int) []string {
	if len(c.files) == 0 {
		return []string{lipgloss.NewStyle().Foreground(crt.dim).Render("  no files observed yet")}
	}
	paths := make([]string, 0, len(c.files))
	for p := range c.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	lines := []string{lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render(fmt.Sprintf("CHANGED FILES  %d", len(paths)))}
	for _, p := range paths {
		f := c.files[p]
		lines = append(lines, fmt.Sprintf("  %-5s %s", f.kind, p))
	}
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) testLines(w, h int) []string {
	status := c.tests.status
	label := map[string]string{"idle": "WAITING", "running": "RUNNING", "pass": "PASS", "fail": "FAIL"}[status]
	lines := []string{
		lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render("GLOBAL VERIFICATION  " + label),
		"",
		lipgloss.NewStyle().Foreground(crt.dim).Render("LAST  " + c.tests.last),
		"",
	}
	for i := range c.subtasks {
		st := &c.subtasks[i]
		g, style := statusGlyph(st.testStatus)
		ig, ist := statusGlyph(st.implStatus)
		vg, vst := statusGlyph(st.verifyStatus)
		lines = append(lines, fmt.Sprintf("  %s %-3s  tests %s  impl %s  verify %s  %s",
			style.Render(g), st.key, style.Render("tests"), ist.Render("impl"), vst.Render("verify"), ansi.Truncate(st.title, max(10, w-42), "…")))
		_ = ig
		_ = vg
	}
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) outputLines(w, h int) []string {
	role := c.activeRole
	if c.selSub >= 0 && c.selSub < len(c.subtasks) {
		role = bus.RoleCoder
	}
	var b *strings.Builder
	if role != "" {
		b = c.raw[role]
	}
	if b == nil || b.Len() == 0 {
		return []string{lipgloss.NewStyle().Foreground(crt.dim).Render("  no output captured for the active agent")}
	}
	text := b.String()
	lines := strings.Split(text, "\n")
	if len(lines) > h {
		lines = lines[len(lines)-h:]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], w-2, "…")
	}
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) renderInspector(w, h int) string {
	lines := []string{
		lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("GATE"),
	}
	if c.gate != "" {
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(galaxyGold).Bold(true).Render(gateLabel(c.gate)))
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(crt.dim).Render("Ctrl+O review  Ctrl+A approve"))
	} else {
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(crt.dim).Render("none"))
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("TOKENS"))
	total := c.totalUsage.InputTokens + c.totalUsage.OutputTokens
	lines = append(lines, fmt.Sprintf("  %s total  %s %s", formatTokensCompact(total), estimatedMark(c.totalUsage.Estimated), ""))
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil {
			continue
		}
		share := 0
		if total > 0 {
			share = a.tokens * 100 / total
		}
		lines = append(lines, fmt.Sprintf("  %-5s %6s %s", roleLabel(role), formatTokensCompact(a.tokens), tokenBarColor(share, 8, roleColor(string(role)))))
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("TESTS"))
	tstatus := map[string]string{"idle": "waiting", "running": "running", "pass": "pass", "fail": "fail"}[c.tests.status]
	tstyle := lipgloss.NewStyle().Foreground(crt.dim)
	if c.tests.status == "pass" {
		tstyle = lipgloss.NewStyle().Foreground(galaxyMint)
	} else if c.tests.status == "fail" {
		tstyle = lipgloss.NewStyle().Foreground(galaxyRed)
	}
	lines = append(lines, "  "+tstyle.Render(tstatus))
	if c.tests.last != "" {
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(crt.dim).Render(truncateOneLine(c.tests.last, w-6)))
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("AGENTS"))
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil {
			continue
		}
		g, style := statusGlyph(string(a.status))
		lines = append(lines, fmt.Sprintf("  %s %-8s %s", style.Render(g), roleLabel(role), truncateOneLine(a.current, 18)))
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("CHANGES"))
	noun := "files"
	if len(c.files) == 1 {
		noun = "file"
	}
	lines = append(lines, fmt.Sprintf("  %d %s  %d fix rounds", len(c.files), noun, c.fixRounds))
	if c.failed && c.errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(galaxyRed).Bold(true).Render("ERROR"))
		for _, l := range wrapText(c.errMsg, w-4) {
			lines = append(lines, "  "+lipgloss.NewStyle().Foreground(galaxyRed).Render(l))
		}
	}
	lines = append(lines, "")
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("NEXT"))
	for _, l := range wrapText(c.nextAction(), w-4) {
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(crt.bright).Render(l))
	}
	return cockpitPane("RUN", w, h, lines, c.focus == cockpitFocusInspector, galaxyBlue)
}

func (c *CockpitModel) renderFooter() string {
	hints := "tab focus  j/k move  1-7 view  i inspector  enter inspect  Ctrl+A approve  q quit"
	if c.width < 100 {
		hints = "tab focus  j/k  1-7 view  i inspector  q quit"
	}
	left := lipgloss.NewStyle().Foreground(crt.dim).Render(hints)
	right := ""
	if c.width < 110 {
		total := c.totalUsage.InputTokens + c.totalUsage.OutputTokens
		right = lipgloss.NewStyle().Foreground(crt.muted).Render(fmt.Sprintf("tok %s · tests %s", formatTokensCompact(total), c.tests.status))
	} else if c.lastEvent != "" {
		right = lipgloss.NewStyle().Foreground(crt.muted).Render(truncateOneLine(c.lastEvent, max(8, c.width/3)))
	}
	line := left
	if right != "" {
		gap := c.width - ansi.StringWidth(left) - ansi.StringWidth(right)
		if gap > 1 {
			line = left + strings.Repeat(" ", gap) + right
		}
	}
	if ansi.StringWidth(line) > c.width {
		line = ansi.Truncate(line, c.width, "…")
	}
	return line
}

func cockpitPane(title string, w, h int, lines []string, focused bool, accent lipgloss.Color) string {
	if w < 4 || h < 2 {
		return ""
	}
	borderColor := crt.border
	if focused {
		borderColor = focusBorder
	}
	bs := lipgloss.NewStyle().Foreground(borderColor)
	innerW := w - 2
	bodyH := h - 2
	var sb strings.Builder
	sb.WriteString(panelTop(w, " "+title, accent, borderColor) + "\n")
	for i := 0; i < bodyH; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		line = ansi.Truncate(line, innerW, "…")
		pad := innerW - ansi.StringWidth(line)
		if pad < 0 {
			pad = 0
		}
		sb.WriteString(bs.Render("│") + line + strings.Repeat(" ", pad) + bs.Render("│"))
		if i < bodyH-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n" + bs.Render("└"+strings.Repeat("─", innerW)+"┘"))
	return sb.String()
}

func windowLines(lines []string, h, scroll int) []string {
	if h <= 0 {
		return nil
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll > len(lines) {
		scroll = len(lines)
	}
	end := scroll + h
	if end > len(lines) {
		end = len(lines)
	}
	return lines[scroll:end]
}

func tokenBar(percent, width int) string {
	return tokenBarColor(percent, width, galaxyMint)
}

func tokenBarColor(percent, width int, color lipgloss.Color) string {
	if width < 4 {
		width = 4
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := percent * width / 100
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(crt.border).Render(strings.Repeat("░", width-filled))
}

func estimatedMark(estimated bool) string {
	if estimated {
		return "~"
	}
	return " "
}

func truncateOneLine(s string, w int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func padRight(s string, w int) string {
	pad := w - ansi.StringWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

func wrapText(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	words := strings.Fields(s)
	var lines []string
	var cur string
	for _, word := range words {
		if cur == "" {
			cur = word
			continue
		}
		if ansi.StringWidth(cur)+1+ansi.StringWidth(word) > w {
			lines = append(lines, cur)
			cur = word
		} else {
			cur += " " + word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// Update handles cockpit-local keys. Global keys are handled by Model.
func (c CockpitModel) Update(msg tea.KeyMsg) (CockpitModel, tea.Cmd) {
	switch msg.String() {
	case "tab":
		c.CycleFocus(1)
	case "shift+tab":
		c.CycleFocus(-1)
	case "1", "2", "3", "4", "5", "6", "7":
		c.SetTab(int(msg.String()[0] - '1'))
	case "p":
		c.SetTab(1)
	case "a":
		c.SetTab(2)
	case "x":
		c.SetTab(3)
	case "f":
		c.SetTab(4)
	case "t":
		c.SetTab(5)
	case "o":
		c.SetTab(6)
	case "i":
		c.ToggleInspector()
	case "up", "k":
		c.move(-1)
	case "down", "j":
		c.move(1)
	case "pgup":
		c.centerScroll -= 8
		if c.centerScroll < 0 {
			c.centerScroll = 0
		}
	case "pgdown":
		c.centerScroll += 8
	case "home":
		c.centerScroll = 0
	case "end":
		c.centerScroll = 1 << 20
	case "enter":
		if c.focus == cockpitFocusWorkflow {
			c.focus = cockpitFocusCenter
		}
	}
	return c, nil
}

func (c *CockpitModel) move(dir int) {
	switch c.focus {
	case cockpitFocusWorkflow:
		build := phaseIndex(c.phases, "build")
		if c.selSub >= 0 {
			switch {
			case dir > 0 && c.selSub < len(c.subtasks)-1:
				c.selSub++
				c.selected = c.selSub
			case dir > 0:
				c.selSub = -1
				c.selPhase = min(build+1, len(c.phases)-1)
			case c.selSub > 0:
				c.selSub--
				c.selected = c.selSub
			default:
				c.selSub = -1
				c.selPhase = build
			}
			if c.selSub >= 0 {
				c.selPhase = build
			}
			return
		}
		if dir > 0 && c.selPhase == build && len(c.subtasks) > 0 {
			c.selSub = 0
			c.selected = 0
			return
		}
		c.selPhase += dir
		if c.selPhase < 0 {
			c.selPhase = 0
		}
		if c.selPhase >= len(c.phases) {
			c.selPhase = len(c.phases) - 1
		}
	case cockpitFocusCenter:
		c.centerScroll += dir
		if c.centerScroll < 0 {
			c.centerScroll = 0
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func phaseIndex(phases []cockpitPhase, id string) int {
	for i := range phases {
		if phases[i].id == id {
			return i
		}
	}
	return 0
}

func roleShort(role bus.AgentRole) string {
	switch role {
	case bus.RolePM:
		return "PM"
	case bus.RoleCoder:
		return "Coder"
	case bus.RoleQA:
		return "QA"
	case bus.RoleUXReviewer:
		return "UX"
	case bus.RoleSecurity:
		return "Sec"
	}
	return string(role)
}

func roleLabel(role bus.AgentRole) string {
	switch role {
	case bus.RolePM:
		return "PM"
	case bus.RoleCoder:
		return "Coder"
	case bus.RoleQA:
		return "QA"
	case bus.RoleUXReviewer:
		return "UX"
	case bus.RoleSecurity:
		return "Security"
	}
	return string(role)
}

func agentStatusLabel(s AgentState) string {
	switch s {
	case AgentWaiting:
		return "waiting"
	case AgentRunning:
		return "running"
	case AgentFixing:
		return "fixing"
	case AgentDone:
		return "done"
	case AgentError:
		return "error"
	case AgentGate:
		return "gate"
	}
	return string(s)
}

func isSubTaskKey(key string) bool {
	if len(key) < 2 || key[0] != 'T' {
		return false
	}
	for _, r := range key[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func cockpitFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.TrimLeft(s, "# "))
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
