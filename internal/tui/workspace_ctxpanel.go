package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/config"
	dctx "github.com/Decsr/Decsr-Code/internal/context"
	"github.com/Decsr/Decsr-Code/internal/memory"
	"github.com/Decsr/Decsr-Code/internal/prompts"
	"github.com/Decsr/Decsr-Code/internal/ui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── /context ─────────────────────────────────────────────────────────────────

// maxWorkerSummary is the most characters of the session summary a worker gets.
const maxWorkerSummary = 4000

// ctxRow is one part of what an agent receives.
type ctxRow struct {
	name, to string // to: "lead", "workers", "lead, workers" or "-"
	chars    int
	note     string
}

// ctxData is the list of parts with the size each agent sees.
type ctxData struct {
	rows         []ctxRow
	role         string // the sample role of the worker prompt
	lead, worker int    // total characters
}

// approxTokens is the estimate used for sizes: one token per four characters.
func approxTokens(chars int) int { return (chars + 3) / 4 }

// buildContext adds up what the lead and a worker of the sample role receive.
// pack, overview and memory are the texts of the project parts.
func buildContext(c config.Config, summary string, overview, pack, mem string) ctxData {
	roles := workspace.Roles(c)
	sample := workspace.Role{Name: "coder", Description: "Writes the code."}
	for _, r := range roles {
		if r.Name == "coder" {
			sample = r
		}
	}
	rolePrompt := sample.Description
	if sample.Prompt != "" {
		rolePrompt = sample.Prompt
	}
	leadTpl := len(workspace.BuildSplitPrompt(roles))
	workerTpl := len(strings.NewReplacer("{{MODE}}", "", "{{ROLE}}", rolePrompt, "{{CONTEXT}}", "").Replace(prompts.MustLoad("ws-worker")))
	sum := len(strings.TrimSpace(summary))
	d := ctxData{role: sample.Name}
	d.rows = []ctxRow{
		{name: "Project overview", to: "lead, workers", chars: len(overview), note: "scan of the files"},
		{name: "Context pack", to: "lead, workers", chars: len(pack), note: ".decsr/context_pack.md"},
		{name: "Session summary", to: "lead, workers", chars: sum, note: "workers: last 4,000"},
		{name: "Lead prompt", to: "lead", chars: leadTpl, note: "rules and roles"},
		{name: "Worker prompt", to: "workers", chars: workerTpl, note: "with the " + sample.Name + " role"},
		{name: "Memory", to: "-", chars: len(mem), note: "not sent here"},
	}
	d.lead = len(overview) + len(pack) + sum + leadTpl
	d.worker = len(overview) + len(pack) + min(sum, maxWorkerSummary) + workerTpl
	return d
}

// wsCtxMsg carries the sizes read off the UI thread.
type wsCtxMsg struct{ data ctxData }

func (m workspaceModel) cmdContext(string) (tea.Model, tea.Cmd) {
	m = m.openPanel(panelContext, 0)
	root, c, summary := m.root, m.liveCfg(), m.sess.snapshot().Summary
	return m, func() tea.Msg {
		cc := c
		cc.Project.Context.SemanticIndex.Enabled = false // a size estimate must not call an embedder
		overview := ""
		if pc, err := dctx.Collect(root, cc); err == nil {
			overview = pc.SystemPromptFragment()
		}
		pack := readSmall(filepath.Join(root, artifacts.DirName, artifacts.ContextPackFile))
		mem := ""
		if lay, err := memoryLayout(root); err == nil {
			mem = lay.ReadMemory()
		}
		return wsCtxMsg{data: buildContext(c, summary, overview, pack, mem)}
	}
}

func (m workspaceModel) onCtx(msg wsCtxMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelContext {
		d := msg.data
		m.panel.ctx = &d
	}
	return m, nil
}

// readSmall reads a file and gives "" when it is missing.
func readSmall(path string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- fixed path under the project root
	if err != nil {
		return ""
	}
	return string(b)
}

func (m workspaceModel) contextOpts(o *ui.PanelOpts) {
	o.Title = "Context"
	o.Keys = hintsFor(false, ui.Hint{Key: "↑↓", Label: "scroll"})
	d := m.panel.ctx
	if d == nil {
		o.Body = []string{dimText("measuring what the agents receive…")}
		return
	}
	o.Body = contextLines(*d, ui.PanelInner(m.width))
}

func contextLines(d ctxData, width int) []string {
	gc := ui.GroupColor(ui.GroupNameUsage)
	var rows [][]string
	for _, r := range d.rows {
		rows = append(rows, []string{r.name, r.to, ui.Num(r.chars), "≈ " + ui.Num(approxTokens(r.chars)), dimText(r.note)})
	}
	out := ui.Table(ui.TableOpts{
		Cols: []ui.Col{{Title: "PART"}, {Title: "GOES TO"}, {Title: "CHARS", Align: ui.Right}, {Title: "TOKENS", Align: ui.Right}, {Title: "NOTE"}},
		Rows: rows, Header: gc, Width: width})
	out = append(out, "")
	for _, l := range wrapText(fmt.Sprintf("The lead gets ≈ %s tokens and a %s worker ≈ %s tokens, before the task itself.",
		ui.Num(approxTokens(d.lead)), d.role, ui.Num(approxTokens(d.worker))), width) {
		out = append(out, plainText(l))
	}
	for _, l := range wrapText("Sizes are estimates: one token per four characters. The Workspace does not send memory.", width) {
		out = append(out, dimText(l))
	}
	return out
}

// htmlComment is the hidden marker MEMORY.md keeps after a fact.
var htmlComment = regexp.MustCompile(`<!--.*?-->`)

// ── /memory ──────────────────────────────────────────────────────────────────

const memoryUsage = "/memory [search <query> | add <text>]"

// memoryLayout is the memory folder of the project; it is created when missing.
func memoryLayout(root string) (memory.Layout, error) {
	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		return memory.Layout{}, err
	}
	return memory.NewLayout(ws.MemoryDir())
}

// wsMemoryMsg is a memory view read off the UI thread.
type wsMemoryMsg struct {
	title string
	lines []string
}

func (m workspaceModel) cmdMemory(args string) (tea.Model, tea.Cmd) {
	verb, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	rest = strings.TrimSpace(rest)
	switch {
	case verb == "":
		m = m.openPanel(panelMemory, 0)
		m.panel.memTitle, m.panel.memBusy = "Memory", true
		root := m.root
		return m, func() tea.Msg { return memoryShow(root) }
	case verb == "search" && rest != "":
		m = m.openPanel(panelMemory, 0)
		m.panel.memTitle, m.panel.memBusy = "Memory · "+truncateOneLine(rest, 40), true
		root, c := m.root, m.liveCfg()
		return m, func() tea.Msg { return memorySearch(root, c, rest) }
	case verb == "add" && rest != "":
		lay, err := memoryLayout(m.root)
		if err != nil {
			m.say("error", "memory: "+oneLine(err.Error()))
			return m, nil
		}
		n, err := lay.PromoteFacts(m.now(), "manual", []memory.Fact{{Source: "manual", Text: rest}})
		switch {
		case err != nil:
			m.say("error", "could not add the fact: "+oneLine(err.Error()))
			return m, nil
		case n == 0:
			m.say("system", "already in MEMORY.md")
		default:
			m.say("ok", "added to MEMORY.md")
		}
		return m.cmdMemory("")
	}
	m.sayUsage(memoryUsage)
	return m, nil
}

// memoryShow is MEMORY.md, the pinned facts.
func memoryShow(root string) wsMemoryMsg {
	lay, err := memoryLayout(root)
	if err != nil {
		return wsMemoryMsg{title: "Memory", lines: []string{"! " + oneLine(err.Error())}}
	}
	text := strings.TrimSpace(lay.ReadMemory())
	if text == "" {
		return wsMemoryMsg{title: "Memory", lines: []string{"MEMORY.md is empty: no facts yet.", "", "/memory add <text> saves one."}}
	}
	return wsMemoryMsg{title: "Memory", lines: strings.Split(text, "\n")}
}

// memorySearch looks the query up in the memory index of the project.
func memorySearch(root string, c config.Config, query string) wsMemoryMsg {
	title := "Memory · " + truncateOneLine(query, 40)
	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		return wsMemoryMsg{title: title, lines: []string{"! " + oneLine(err.Error())}}
	}
	mc := c.Project.Context.Memory
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mem, err := memory.OpenMemory(ctx, ws.MemoryDir(), ws.MemoryDBPath(), memory.Options{ChunkTokens: mc.ChunkTokens, OverlapTokens: mc.OverlapTokens})
	if mem == nil {
		return wsMemoryMsg{title: title, lines: []string{"! " + oneLine(fmt.Sprint(err))}}
	}
	defer func() { _ = mem.Close() }()
	hits, err := mem.Store.Search(ctx, query, memory.SearchOptions{K: 8, HybridAlpha: 1.0})
	if err != nil {
		return wsMemoryMsg{title: title, lines: []string{"! " + oneLine(err.Error())}}
	}
	if len(hits) == 0 {
		return wsMemoryMsg{title: title, lines: []string{"no matches"}}
	}
	var lines []string
	for i, h := range hits {
		lines = append(lines, fmt.Sprintf("#%d %s  lines %d-%d  score %.2f", i+1, h.File, h.StartLine, h.EndLine, h.Score))
		body := strings.TrimSpace(h.Body)
		if len(body) > 500 {
			body = body[:500] + "…"
		}
		for _, l := range strings.Split(body, "\n") {
			lines = append(lines, "    "+l)
		}
		lines = append(lines, "")
	}
	return wsMemoryMsg{title: title, lines: lines}
}

func (m workspaceModel) onMemory(msg wsMemoryMsg) (tea.Model, tea.Cmd) {
	if m.overlay == wsOverlayPanel && m.panel.kind == panelMemory {
		m.panel.memTitle, m.panel.memLines, m.panel.memBusy = msg.title, msg.lines, false
	}
	return m, nil
}

func (m workspaceModel) memoryOpts(o *ui.PanelOpts) {
	o.Title = orDefault(m.panel.memTitle, "Memory")
	o.Keys = hintsFor(false, ui.Hint{Key: "↑↓", Label: "scroll"})
	if m.panel.memBusy {
		o.Body = []string{dimText("reading the project memory…")}
		return
	}
	inner := ui.PanelInner(m.width)
	head := lipgloss.NewStyle().Foreground(o.Color).Bold(true)
	for _, l := range m.panel.memLines {
		l = strings.TrimRight(htmlComment.ReplaceAllString(workspace.SanitizeText(l), ""), " ")
		switch {
		case strings.HasPrefix(l, "#"):
			o.Body = append(o.Body, head.Render(truncateOneLine(l, inner)))
		case strings.TrimSpace(l) == "":
			o.Body = append(o.Body, "")
		default:
			o.Body = append(o.Body, wrapText(l, inner)...)
		}
	}
}

// ── /diff ────────────────────────────────────────────────────────────────────

// cmdDiff opens the full diff of the session: the session branch against its
// base, or the working tree when the checkout is shared.
func (m workspaceModel) cmdDiff(string) (tea.Model, tea.Cmd) {
	return m.openDiff(nil), nil
}
