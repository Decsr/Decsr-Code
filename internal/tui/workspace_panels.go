package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── Inspector ────────────────────────────────────────────────────────────────

const (
	inspFilesLines  = 5  // Files rows, the last one "+N more" when there are more
	inspReportLines = 3  // rows of a report question or summary
	inspTextLines   = 6  // rows of Summary, Warnings, Error and the Output maximum
	inspOutputMin   = 4  // Output rows kept under height pressure
	inspCompactH    = 12 // below this height the inspector drops blank lines and hints
)

// renderInspector draws the detail panel of one node in at most h lines of at
// most w cells. The fixed sections (header, status, Role, Depends on) come
// first; Files, Warnings, Error, Summary, Tokens and Output get a bounded
// number of rows and Description takes all the height that remains (at least
// 4 rows, never fewer than 3). When the pane is too short, Tokens, Output (when
// the node is not running), Files and Summary are dropped, in that order.
func renderInspector(g *workspace.Graph, n *workspace.Node, cfg config.Config, plug plugins.Set, w, h int, outputTail []string) string {
	if w < 8 || h <= 0 {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(colMuted)
	bright := lipgloss.NewStyle().Foreground(colText).Bold(true)
	head := lipgloss.NewStyle().Foreground(colMuted)
	var lines []string
	add := func(s string) { lines = append(lines, ansi.Truncate(s, w, "…")) }
	section := func(title string) { add(head.Render(title)) }
	// On a short pane the blank separator lines and the hint lines go, so the
	// sections that matter (Description first) still fit.
	compact := h < inspCompactH
	gap := func() {
		if !compact {
			add("")
		}
	}
	gapN := 1
	if compact {
		gapN = 0
	}

	if n == nil {
		add(dim.Render("No node selected."))
		return strings.Join(clipLines(lines, h), "\n")
	}

	// ID + title.
	for i, l := range wrapText(n.ID+" · "+n.Title, w) {
		if i >= 3 {
			break
		}
		add(bright.Render(l))
	}

	// Status: glyph and word on the left, the elapsed time dim on the right.
	glyph, label := nodeStatusGlyph(g, n)
	status := glyph + " " + label
	if label == "blocked" {
		status += " by " + strings.Join(g.Blockers(n.ID), ", ")
	}
	statusLine := lipgloss.NewStyle().Foreground(statusLabelColor(g, n)).Render(status)
	if !n.StartedAt.IsZero() {
		end := n.EndedAt
		if end.IsZero() {
			end = time.Now()
		}
		elapsed := formatElapsed(end.Sub(n.StartedAt))
		if gap := w - ansi.StringWidth(status) - ansi.StringWidth(elapsed); gap >= 2 {
			statusLine += strings.Repeat(" ", gap) + dim.Render(elapsed)
		} else {
			statusLine += dim.Render(" · " + elapsed)
		}
	}
	add(statusLine)
	if n.AttemptNumber() > 1 {
		add(dim.Render(fmt.Sprintf("Attempt  %d of %d", n.AttemptNumber(), n.AttemptNumber())))
	}
	if n.Reply != "" {
		add(dim.Render("Reply  " + firstLine(workspace.SanitizeText(n.Reply))))
	}
	if n.PendingReply != "" {
		add(dim.Render("Queued  " + firstLine(workspace.SanitizeText(n.PendingReply))))
	}
	if l := n.Limit; l != nil && n.Status == workspace.StatusWaiting {
		gold := lipgloss.NewStyle().Foreground(colWarn)
		add(gold.Render("Limit  " + l.Provider + " · " + limitResets(time.Now(), l)))
		for i, s := range wrapText(workspace.SanitizeText(l.Message), w) {
			if i >= inspReportLines {
				break
			}
			add(gold.Render(s))
		}
		if l.Retries > 0 {
			add(dim.Render(fmt.Sprintf("Retries  %d of %d", l.Retries, workspace.MaxLimitRetries)))
		}
	}
	if r := n.Report; r != nil {
		gold := lipgloss.NewStyle().Foreground(colWarn)
		switch {
		case n.Status == workspace.StatusWaiting && r.Status == workspace.ReportQuestion:
			add(gold.Render("Report  question"))
			for i, l := range wrapText(workspace.SanitizeText(r.Ask()), w) {
				if i >= inspReportLines {
					break
				}
				add(gold.Render(l))
			}
		case n.Status == workspace.StatusWaiting:
			add(gold.Render("Report  blocked"))
			for i, l := range wrapText(workspace.SanitizeText(r.Summary), w) {
				if i >= inspReportLines {
					break
				}
				add(gold.Render(l))
			}
		case n.Status == workspace.StatusDone && strings.TrimSpace(r.Summary) != "" && strings.TrimSpace(r.Summary) != strings.TrimSpace(n.Summary):
			add(dim.Render("Report  done"))
			for i, l := range wrapText(workspace.SanitizeText(r.Summary), w) {
				if i >= inspReportLines {
					break
				}
				add(dim.Render(l))
			}
		}
	}
	gap()

	// Role.
	roleAt := len(lines)
	section("Role")
	switch {
	case n.Kind == workspace.KindCheck:
		add("none — runs build/test commands")
		for _, c := range n.Commands {
			add(dim.Render("$ " + c))
		}
		if len(n.Commands) == 0 && n.Status == workspace.StatusTodo && !compact {
			add(dim.Render("commands are resolved when it runs"))
		}
	case n.Role != "":
		var meta []string
		runner, mode := "", ""
		if r, ok := workspace.FindRole(cfg, n.Role); ok {
			runner, mode = r.Agent.Runner, roleModeLabel(r.Agent)
			if m := modelOnly(r.Agent.Model); m != "" {
				meta = append(meta, m)
			}
			if r.Agent.Effort != "" {
				meta = append(meta, r.Agent.Effort)
			}
			if r.ReadOnly {
				meta = append(meta, "read-only")
			}
		}
		line := runnerBadge(runner, cfg.UI.Icons) + " " + lipgloss.NewStyle().Foreground(colText).Bold(true).Render(n.Role)
		if len(meta) > 0 {
			line += dim.Render("  " + strings.Join(meta, " / "))
		}
		add(line)
		if mode != "" && !compact {
			// On the section header line, so it costs no row.
			tag := "Mode  " + mode
			if gap := w - ansi.StringWidth("Role") - ansi.StringWidth(tag); gap >= 2 {
				lines[roleAt] = head.Render("Role") + strings.Repeat(" ", gap) + dim.Render(tag)
			}
		}
	case n.SuggestedRole != "":
		s := "suggested: " + n.SuggestedRole
		if _, ok := workspace.FindRole(cfg, n.SuggestedRole); !ok {
			s += " (not defined yet)"
		}
		add(lipgloss.NewStyle().Foreground(colWarn).Render(s))
		if !compact {
			add(dim.Render("press a to assign"))
		}
	default:
		add(lipgloss.NewStyle().Foreground(colWarn).Render("unassigned — press a to assign"))
	}
	if compact && len(lines) > roleAt+1 { // "Role" and its first line share a row
		lines[roleAt] += " " + lines[roleAt+1]
		lines = append(lines[:roleAt+1], lines[roleAt+2:]...)
	}
	gap()

	// Skills.
	if n.Kind == workspace.KindWork {
		if rows := nodeSkillRows(n, cfg, plug); len(rows) > 0 {
			section("Skills")
			for _, r := range rows {
				add(r)
			}
			gap()
		}
	}

	// Dependencies.
	if len(n.DependsOn) > 0 {
		section("Depends on")
		for _, id := range n.DependsOn {
			d := g.Node(id)
			if d == nil {
				add("? " + id + dim.Render(" (missing)"))
				continue
			}
			dg, _ := nodeStatusGlyph(g, d)
			add(dg + " " + id + " " + dim.Render(d.Title))
		}
		gap()
	}

	// The variable sections are built as blocks (header + body, no trailing
	// blank line) and laid out in the height that the fixed sections leave.
	clampBody := func(body []string, limit int) []string {
		if len(body) <= limit {
			return body
		}
		body = append([]string(nil), body[:limit]...)
		body[limit-1] = ansi.Truncate(body[limit-1], max(w-1, 1), "") + dim.Render("…")
		return body
	}
	styled := func(text string, st lipgloss.Style, limit int) []string {
		var body []string
		for _, l := range wrapText(text, w) {
			body = append(body, st.Render(l))
		}
		return clampBody(body, limit)
	}
	mk := func(title lipgloss.Style, name string, body []string) []string {
		return append([]string{title.Render(name)}, body...)
	}

	type block struct {
		name  string
		lines []string
	}
	var desc []string // full wrapped description
	if n.Description != "" {
		for _, l := range wrapText(n.Description, w) {
			desc = append(desc, lipgloss.NewStyle().Foreground(colText).Render(l))
		}
	}
	descBlock := func(body int) []string {
		if body > len(desc) {
			body = len(desc)
		}
		return mk(head, "Description", clampBody(desc, body))
	}

	files := append([]string(nil), n.Files...)
	seenFile := map[string]bool{}
	for _, f := range n.Files {
		seenFile[f] = true
	}
	for _, f := range n.Observed {
		if !seenFile[f] {
			seenFile[f] = true
			files = append(files, f+dim.Render(" (by the agent's own tools)"))
		}
	}
	var filesBlock, warnBlock, errBlock, sumBlock, tokBlock []string
	if len(files) > 0 {
		body := files
		if len(files) > inspFilesLines {
			body = append(append([]string(nil), files[:inspFilesLines-1]...), dim.Render(fmt.Sprintf("… +%d more", len(files)-(inspFilesLines-1))))
		}
		filesBlock = mk(head, "Files", body)
	}
	if len(n.Warnings) > 0 {
		gold := lipgloss.NewStyle().Foreground(colWarn)
		var body []string
		for _, wn := range n.Warnings {
			for j, l := range wrapText(wn, w-2) {
				mark := "  "
				if j == 0 {
					mark = "⚠ "
				}
				body = append(body, gold.Render(mark+l))
			}
		}
		warnBlock = mk(gold.Bold(true), "Warnings", clampBody(body, inspTextLines))
	}
	if n.Error != "" {
		errBlock = mk(lipgloss.NewStyle().Foreground(colFail), "Error", styled(n.Error, lipgloss.NewStyle().Foreground(colFail), inspTextLines))
	}
	if n.Summary != "" {
		sumBlock = mk(head, "Summary", styled(n.Summary, lipgloss.NewStyle().Foreground(colText), inspTextLines))
	}
	var tokBody []string
	if n.InputTokens > 0 || n.OutputTokens > 0 {
		mark := ""
		if n.Estimated {
			mark = "~"
		}
		tokBody = append(tokBody, dim.Render(fmt.Sprintf("in %s%s · out %s%s", mark, formatTokensCompact(n.InputTokens), mark, formatTokensCompact(n.OutputTokens))))
	}
	if n.ContextTokens > 0 {
		ctx := "Context  " + ctxK(n.ContextTokens)
		if n.ContextLimit > 0 {
			ctx += " / " + ctxK(n.ContextLimit)
		}
		tokBody = append(tokBody, dim.Render(ctx+" tokens"))
	}
	if total, failed := activityCounts(n); total > 0 {
		act := fmt.Sprintf("Activity  %d %s", total, pluralize(total, "step", "steps"))
		if failed > 0 {
			act += fmt.Sprintf(" (%d failed)", failed)
		}
		tokBody = append(tokBody, dim.Render(act))
	}
	if len(tokBody) > 0 {
		tokBlock = mk(head, "Tokens", tokBody)
	}
	running := n.Status == workspace.StatusRunning
	showOutput := len(outputTail) > 0 || running
	outputBlock := func(limit int) []string {
		var body []string
		if len(outputTail) == 0 {
			body = []string{dim.Render("waiting for the first output…")}
		}
		t := outputTail
		if len(t) > limit {
			t = t[len(t)-limit:]
		}
		for _, l := range t {
			body = append(body, dim.Render(strings.TrimRight(l, "\r")))
		}
		return mk(head, "Output", body)
	}

	avail := h - len(lines) + gapN // the last block needs no trailing blank line
	cost := func(b []string) int {
		if len(b) == 0 {
			return 0
		}
		return len(b) + gapN
	}
	outMax := inspTextLines
	descMin := min(4, len(desc))
	if compact {
		descMin = min(2, len(desc))
	}
	// Drop order under pressure: TOKENS, OUTPUT (when not running), FILES, SUMMARY.
	dropped := map[string]bool{}
	layout := func() (rest int, blocks []block) {
		blocks = []block{{"FILES", filesBlock}, {"WARNINGS", warnBlock}, {"ERROR", errBlock}, {"SUMMARY", sumBlock}, {"TOKENS", tokBlock}}
		if showOutput {
			blocks = append(blocks, block{"OUTPUT", outputBlock(outMax)})
		}
		for i := range blocks {
			if dropped[blocks[i].name] {
				blocks[i].lines = nil
			}
			rest += cost(blocks[i].lines)
		}
		return rest, blocks
	}
	rest, blocks := layout()
	need := func() int {
		d := 0
		if len(desc) > 0 {
			d = len(descBlock(descMin)) + gapN
		}
		return rest + d
	}
	if need() > avail {
		outMax = inspOutputMin
		rest, blocks = layout()
	}
	for _, name := range []string{"TOKENS", "OUTPUT", "FILES", "SUMMARY"} {
		if need() <= avail {
			break
		}
		if name == "OUTPUT" && running {
			continue
		}
		dropped[name] = true
		rest, blocks = layout()
	}
	if len(desc) > 0 {
		if need() > avail {
			descMin = min(3, len(desc))
			if compact {
				descMin = min(1, len(desc))
			}
		}
		body := max(avail-rest-1-gapN, descMin) // header and trailing blank
		lines = append(lines, descBlock(body)...)
		lines = append(lines, "")
	}
	for _, b := range blocks {
		if len(b.lines) == 0 {
			continue
		}
		for _, l := range b.lines {
			add(l)
		}
		gap()
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(clipLines(lines, h), "\n")
}

// inspSkillRows is the most skill rows the inspector shows; the last one says
// how many more there are.
const inspSkillRows = 5

// nodeSkillRows lists the skills a node gets: the role's own first, marked
// "(role)", then the ones the lead chose, marked "(lead)". An id that no
// enabled plugin provides gets "(not installed)". A plain name (a skill file)
// is taken as present.
func nodeSkillRows(n *workspace.Node, cfg config.Config, plug plugins.Set) []string {
	type row struct {
		id, from string
	}
	var rows []row
	seen := map[string]bool{}
	if r, ok := workspace.FindRole(cfg, n.Role); ok {
		for _, id := range r.UserSkills {
			if !seen[id] {
				seen[id] = true
				rows = append(rows, row{id, "role"})
			}
		}
	}
	for _, id := range n.Skills {
		if !seen[id] {
			seen[id] = true
			rows = append(rows, row{id, "lead"})
		}
	}
	have := map[string]bool{}
	for _, p := range plug.Plugins {
		if p.Enabled {
			for _, s := range p.Skills {
				have[s.ID] = true
			}
		}
	}
	var out []string
	for i, r := range rows {
		if i >= inspSkillRows-1 && len(rows) > inspSkillRows {
			out = append(out, styleWsSkillNote.Render(fmt.Sprintf("… +%d more", len(rows)-i)))
			break
		}
		line := styleWsSkill.Render(workspace.Sanitize(r.id)) + styleWsSkillNote.Render(" ("+r.from+")")
		if strings.Contains(r.id, ":") && !have[r.id] {
			line += styleWsSkillWarn.Render(" (not installed)")
		}
		out = append(out, line)
	}
	return out
}

func clipLines(lines []string, h int) []string {
	if len(lines) > h {
		return lines[:h]
	}
	return lines
}

// modelOnly is the model name after its last "/".
func modelOnly(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

// shortModel is the model name after its last "/", prefixed with the runner
// ("claude:sonnet") unless the runner is the default opencode.
func shortModel(runner, model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	switch {
	case runner == "" || runner == "opencode":
		return model
	case model == "":
		return runner
	}
	return runner + ":" + model
}

// ── Overlay helpers ──────────────────────────────────────────────────────────

// wsBox draws a titled box of total width w around body lines (each already
// fitted to w-4). Overlays use a rounded border with the title in its top edge.
func wsBox(title string, w int, body []string) string {
	inner := w - 4
	for i, l := range body {
		body[i] = padRight(ansi.Truncate(l, inner, "…"), inner)
	}
	st := lipgloss.NewStyle().Padding(0, 1).Width(w - 2)
	out := st.Border(lipgloss.RoundedBorder(), false, true, true, true).BorderForeground(colAccent).Render(strings.Join(body, "\n"))
	bs := lipgloss.NewStyle().Foreground(colAccent)
	label := " " + ansi.Truncate(title, max(w-6, 1), "…") + " "
	fill := max(w-3-ansi.StringWidth(label), 0)
	top := bs.Render("╭─") + lipgloss.NewStyle().Foreground(colText).Bold(true).Render(label) + bs.Render(strings.Repeat("─", fill)+"╮")
	return top + "\n" + out
}

// overlayCenter draws box (multi-line) centered over base, leaving the first
// and last line of base alone when the box fits between them. The rows the
// box occupies are blanked on both sides, so no background text shows beside it.
func overlayCenter(base, box string, w, h int) string {
	lines := strings.Split(base, "\n")
	for len(lines) < h {
		lines = append(lines, "")
	}
	bl := strings.Split(box, "\n")
	bw := 0
	for _, l := range bl {
		if x := ansi.StringWidth(l); x > bw {
			bw = x
		}
	}
	x := max((w-bw)/2, 0)
	y := max((h-len(bl))/2, 1)
	if y+len(bl) > h-1 {
		y = max(h-1-len(bl), 0)
	}
	for i, l := range bl {
		if y+i >= len(lines) {
			break
		}
		lines[y+i] = strings.Repeat(" ", x) + padRight(l, bw) + "\x1b[0m" + strings.Repeat(" ", max(w-x-bw, 0))
	}
	return strings.Join(lines, "\n")
}

// ── Assignment picker ────────────────────────────────────────────────────────

type wsPickItem struct {
	kind      string // "role" | "create" | "new" | "unassign"
	name      string
	desc      string
	meta      string
	suggested bool
	readOnly  bool
	skills    string // file-based skills of a role, "a, b"
}

// buildPickItems lists what the assignment picker offers for node n.
func buildPickItems(g *workspace.Graph, n *workspace.Node, cfg config.Config) []wsPickItem {
	var items []wsPickItem
	for _, r := range workspace.Roles(cfg) {
		it := wsPickItem{kind: "role", name: r.Name, desc: r.Description, meta: roleMetaShort(r.Agent), readOnly: r.ReadOnly}
		if n != nil && r.Name == n.SuggestedRole {
			it.suggested = true
			items = append([]wsPickItem{it}, items...)
			continue
		}
		items = append(items, it)
	}
	for _, nr := range g.NewRoles {
		if _, ok := workspace.FindRole(cfg, nr.Name); ok {
			continue
		}
		items = append(items, wsPickItem{kind: "create", name: nr.Name, desc: nr.Description, suggested: n != nil && nr.Name == n.SuggestedRole})
	}
	items = append(items, wsPickItem{kind: "new"})
	if n != nil && n.Role != "" {
		items = append(items, wsPickItem{kind: "unassign"})
	}
	return items
}

func roleMetaShort(ac config.AgentConfig) string {
	return shortModel(ac.Runner, ac.Model) + " · " + roleModeLabel(ac)
}

// pickDefaultIndex puts the cursor on the suggested row.
func pickDefaultIndex(items []wsPickItem) int {
	for i, it := range items {
		if it.suggested {
			return i
		}
	}
	return 0
}

// renderPickRow draws one picker row of exactly inner cells.
func renderPickRow(it wsPickItem, selected bool, inner, nameW int) string {
	bg := func(fg lipgloss.Color, bold bool) lipgloss.Style {
		st := lipgloss.NewStyle().Foreground(fg).Bold(bold)
		if selected {
			st = st.Background(colSel)
		}
		return st
	}
	marker := "  "
	if selected {
		marker = "▸ "
	}
	var sb strings.Builder
	sb.WriteString(bg(colText, true).Render(marker))
	switch it.kind {
	case "role":
		sb.WriteString(bg(colText, true).Render(padRight(fitRoleName(it.name, nameW), nameW)))
		used := 2 + nameW
		if it.suggested {
			sb.WriteString(bg(colWarn, true).Render(" ★ suggested"))
			used += 12
		}
		desc := it.desc
		if it.skills != "" {
			desc = strings.TrimSuffix("skills: "+it.skills+" · "+desc, " · ")
		}
		if it.readOnly {
			desc = strings.TrimSuffix("read-only · "+desc, " · ")
		}
		// The description gets the width that is left; the model never
		// takes more than what leaves it at least 14 cells.
		if it.meta != "" {
			reserve := min(ansi.StringWidth(desc), 14)
			if mw := min(min(ansi.StringWidth(it.meta), 22), inner-used-2-reserve-2); mw >= 4 {
				sb.WriteString(bg(colText, false).Render("  " + ansi.Truncate(it.meta, mw, "…")))
			}
		}
		if desc != "" {
			sb.WriteString(bg(colMuted, false).Render("  " + desc))
		}
	case "create":
		sb.WriteString(bg(colOK, true).Render("＋ create \"" + it.name + "\""))
		if it.suggested {
			sb.WriteString(bg(colWarn, true).Render(" ★ suggested"))
		}
		if it.desc != "" {
			sb.WriteString(bg(colMuted, false).Render(" — " + it.desc))
		}
	case "new":
		sb.WriteString(bg(colOK, true).Render("＋ new role…"))
	case "unassign":
		sb.WriteString(bg(colFail, true).Render("– unassign"))
	}
	row := ansi.Truncate(sb.String(), inner, "…")
	if pad := inner - ansi.StringWidth(row); pad > 0 {
		row += bg(colText, false).Render(strings.Repeat(" ", pad))
	}
	return row
}

// maxPickNameW caps the role name column so the model and description still
// fit in the 70-cell picker.
const maxPickNameW = 28

// fitRoleName shortens a role name to w cells. A plugin role
// ("plugin:agent") loses the end of the plugin part first so the agent part
// stays readable: "pr-re…:silent-failure-hunter".
func fitRoleName(name string, w int) string {
	if ansi.StringWidth(name) <= w {
		return name
	}
	if i := strings.IndexByte(name, ':'); i > 0 {
		if keep := w - ansi.StringWidth(name[i:]) - 1; keep >= 3 {
			return ansi.Truncate(name[:i], keep, "") + "…" + name[i:]
		}
	}
	return ansi.Truncate(name, w, "…")
}

// renderPicker draws the role list box (assignment picker or /roles list).
func renderPicker(title string, items []wsPickItem, idx int, footer string, width, height int) string {
	boxW := min(70, width-2)
	inner := boxW - 4
	rows := max(height-2-2-2, 1) // box chrome 2, footer + blank 2, header/hint rows of the screen 2
	if rows > len(items) {
		rows = len(items)
	}
	start := 0
	if idx >= rows {
		start = idx - rows + 1
	}
	nameW := 14
	for _, it := range items {
		if it.kind == "role" {
			nameW = max(nameW, min(ansi.StringWidth(it.name), maxPickNameW))
		}
	}
	var body []string
	for i := start; i < start+rows && i < len(items); i++ {
		body = append(body, renderPickRow(items[i], i == idx, inner, nameW))
	}
	body = append(body, "", lipgloss.NewStyle().Foreground(colMuted).Render(footer))
	return wsBox(ansi.Truncate(title, boxW-6, "…"), boxW, body)
}

// ── Role form ────────────────────────────────────────────────────────────────

// renderRoleForm draws the new-role form. nameView/descView are the rendered
// text inputs; focus is 0 for name, 1 for description, 2 for the
// "Can change files" toggle.
func renderRoleForm(nameView, descView string, focus int, canChange bool, inherits, errText string, width int) string {
	boxW := min(70, width-2)
	inner := boxW - 4
	dim := lipgloss.NewStyle().Foreground(colMuted)
	label := func(s string, active bool) string {
		if active {
			return lipgloss.NewStyle().Foreground(colText).Bold(true).Render("▸ " + padRight(s, 18))
		}
		return dim.Render("  " + padRight(s, 18))
	}
	toggle := lipgloss.NewStyle().Foreground(colText).Render("yes") + dim.Render("  (←/→ or Space toggles)")
	if !canChange {
		toggle = lipgloss.NewStyle().Foreground(colWarn).Bold(true).Render("no") + dim.Render("   reports only, never writes files")
	}
	body := []string{
		label("Name", focus == 0) + nameView,
		label("Description", focus == 1) + descView,
		label("Can change files", focus == 2) + toggle,
		"",
	}
	for _, l := range wrapText(inherits, inner) {
		body = append(body, dim.Render(l))
	}
	if errText != "" {
		body = append(body, "")
		for _, l := range wrapText("! "+errText, inner) {
			body = append(body, lipgloss.NewStyle().Foreground(colFail).Render(l))
		}
	}
	body = append(body, "", dim.Render("Tab next · ←/→ toggle · Enter save · Esc cancel"))
	return wsBox("New role", boxW, body)
}

// ── Command list ─────────────────────────────────────────────────────────────

// renderCmdList draws the command rows (one line each, exactly width cells),
// at most maxRows of them, scrolled so the selected row stays visible.
func renderCmdList(cmds []wsCommand, idx, width, maxRows int) []string {
	start, end := 0, len(cmds)
	if maxRows > 0 && len(cmds) > maxRows {
		start, end = cmdWindow(len(cmds), idx, maxRows)
	}
	dim := lipgloss.NewStyle().Foreground(colMuted)
	var out []string
	if start > 0 && maxRows >= 3 {
		out = append(out, dim.Render(fmt.Sprintf("  ↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		c := cmds[i]
		sel := i == idx
		nameSt := lipgloss.NewStyle().Foreground(colText).Bold(true)
		descSt := lipgloss.NewStyle().Foreground(colMuted)
		marker := "  "
		if sel {
			marker = "▸ "
			nameSt = nameSt.Background(colSel)
			descSt = descSt.Background(colSel)
		}
		desc := c.desc
		if c.hint != "" {
			desc = c.hint + "  " + desc
		}
		if c.tag != "" {
			desc += " " + c.tag
		}
		row := nameSt.Render(marker+padRight(c.name, 10)) + descSt.Render(" "+desc)
		row = ansi.Truncate(row, width, "…")
		if pad := width - ansi.StringWidth(row); pad > 0 {
			st := lipgloss.NewStyle()
			if sel {
				st = st.Background(colSel)
			}
			row += st.Render(strings.Repeat(" ", pad))
		}
		out = append(out, row)
	}
	if end < len(cmds) && maxRows >= 3 {
		out = append(out, dim.Render(fmt.Sprintf("  ↓ %d more", len(cmds)-end)))
	}
	return out
}

// cmdWindow picks the slice [start, end) of n commands shown in rows rows,
// with the selected idx inside it. A row goes to each "more" marker that is
// needed, so the list never exceeds rows lines.
func cmdWindow(n, idx, rows int) (start, end int) {
	if rows < 3 {
		start = min(max(idx-rows+1, 0), n-rows)
		return start, start + rows
	}
	items := rows - 2
	start = max(idx-items+1, 0)
	if start == 0 {
		return 0, rows - 1
	}
	end = start + items
	if end >= n {
		return n - (rows - 1), n
	}
	return start, end
}

// ── Help ─────────────────────────────────────────────────────────────────────

func workspaceHelp(cmds []wsCommand) []helpSection {
	var cmdKeys []keyHint
	for _, c := range cmds {
		name := c.name
		if c.args != "" {
			name += " " + c.args
		}
		label := c.desc
		if c.tag != "" {
			label += " " + c.tag
		}
		cmdKeys = append(cmdKeys, keyHint{name, label})
	}
	return []helpSection{
		{"Focus", []keyHint{
			{"Tab", "switch between the prompt and the graph"},
			{"Esc", "prompt: go to the graph (closes the command list first)"},
			{"/ or c", "graph: go to the prompt"},
			{"Enter", "prompt: send the task or run the command"},
			{"→", "empty prompt: accept the suggestion shown; Esc hides it"},
			{"Alt+Enter", "prompt: new line"},
			{"/usr/lib is gone", "a first word with a second / is sent as a task"},
			{"//text", "sends /text as a task, not a command"},
			{"@T3 message", "reply to node T3 (@ lists nodes)"},
		}},
		{"Graph keys (graph focus)", []keyHint{
			{"← ↑ ↓ → / h j k l", "move between nodes"},
			{"Enter / o", "open the full output of the node"},
			{"i", "show or hide the inspector (under 100 columns it opens over the graph; i or Esc closes it)"},
			{"a / A", "pick the agent / accept all suggestions"},
			{"d / D", "diff of the node / diff of the whole session (c note a line, S send the notes as a new node)"},
			{"K", "choose the plugin skills of the node"},
			{"r / R", "run the node / run every assigned node"},
			{"s / S", "stop the node / stop all nodes"},
			{"f", "add a fix node after a failed Build & test"},
			{"m", "reply to the node (fills the prompt with @T3); a waiting node (◇) needs it"},
			{"y / n", "apply or reject the lead review that waits (coordinator mode, level 1)"},
			{"!", "open a shell in the node's worktree (a tmux split under tmux)"},
			{"q", "quit (asks first while nodes run)"},
		}},
		{"Anywhere", []keyHint{
			{"?", "this help (empty prompt or graph focus)"},
			{"Ctrl+C ×2", "quit"},
		}},
		{"Commands", cmdKeys},
	}
}

// roleModeLabel is the effective mode of a role for display, with a "!" when
// the runner cannot run it.
func roleModeLabel(ac config.AgentConfig) string {
	mode := ac.EffectiveMode()
	if ac.ModeError() != nil {
		mode += " !"
	}
	return mode
}
