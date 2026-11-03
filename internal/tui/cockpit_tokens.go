package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var phaseLetters = map[string]string{
	"context": "C", "spec": "S", "plan": "P", "build": "B", "verify": "V", "review": "R", "done": "D",
}

func (c *CockpitModel) tokenLines(w, h int) []string {
	dim := lipgloss.NewStyle().Foreground(crt.dim)
	total := c.totalUsage.InputTokens + c.totalUsage.OutputTokens
	calls := 0
	for _, a := range c.agents {
		if a != nil {
			calls += a.calls
		}
	}
	totalLine := fmt.Sprintf("TOTAL %s%s   in %s   out %s   %d calls",
		formatTokensCompact(total), strings.TrimSpace(estimatedMark(c.totalUsage.Estimated)),
		formatTokensCompact(c.totalUsage.InputTokens), formatTokensCompact(c.totalUsage.OutputTokens), calls)
	lines := []string{
		ansi.Truncate(lipgloss.NewStyle().Foreground(crt.bright).Bold(true).Render(totalLine), w, "…"),
		"",
	}

	if len(c.sessions) == 0 {
		lines = append(lines, dim.Render("no usage reported yet"))
	} else {
		lines = append(lines, c.tokenShare(w)...)
		lines = append(lines, "")
		lines = append(lines, c.tokenPerAgent(w)...)
		lines = append(lines, "")
		lines = append(lines, c.tokenChart(w, h)...)
		lines = append(lines, "")
	}

	lines = append(lines, "", dim.Render("SUBTASK USAGE"))
	for i := range c.subtasks {
		st := &c.subtasks[i]
		lines = append(lines, ansi.Truncate(fmt.Sprintf("  %-3s %6s  %s", st.key, formatTokensCompact(st.tokens), st.title), w, "…"))
	}
	lines = append(lines, "", ansi.Truncate(dim.Render("Estimates are marked ~; unreported usage is shown as —."), w, "…"))
	if len(c.sessions) > 0 {
		lines = append(lines, "")
		lines = append(lines, c.tokenLog(w)...)
	}
	return windowLines(lines, h, c.centerScroll)
}

func (c *CockpitModel) tokenShare(w int) []string {
	total := 0
	for _, a := range c.agents {
		if a != nil {
			total += a.tokens
		}
	}
	var segs []chartSegment
	var legend []string
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil || a.tokens <= 0 {
			continue
		}
		col := roleColor(string(role))
		segs = append(segs, chartSegment{value: a.tokens, color: col})
		legend = append(legend, lipgloss.NewStyle().Foreground(col).Render("■")+
			fmt.Sprintf(" %s %d%%", roleLabel(role), max(1, a.tokens*100/total)))
	}
	if total == 0 {
		return nil
	}
	return []string{stackedBar(segs, w), ansi.Truncate(strings.Join(legend, "  "), w, "…")}
}

func (c *CockpitModel) tokenPerAgent(w int) []string {
	lines := []string{lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("PER AGENT")}
	maxTok := 1
	for _, a := range c.agents {
		if a != nil && a.tokens > maxTok {
			maxTok = a.tokens
		}
	}
	// label(9) + sp + tokens(7) + sp + calls(5) + sp
	barW := w - 24
	for _, role := range agentOrder {
		a := c.agents[role]
		if a == nil {
			continue
		}
		u := c.usage[role]
		in, out := u.InputTokens, u.OutputTokens
		prefix := fmt.Sprintf("%-9s %7s %5s ", roleLabel(role), formatTokensCompact(a.tokens), fmt.Sprintf("×%d", a.calls))
		bar := ""
		if barW > 0 {
			bar = agentBar(in, out, maxTok, barW, roleColor(string(role)))
		}
		lines = append(lines, ansi.Truncate(prefix+bar, w, ""))
	}
	return lines
}

func agentBar(in, out, maxTok, width int, color lipgloss.Color) string {
	total := in + out
	if total <= 0 || maxTok <= 0 || width <= 0 {
		return ""
	}
	cells := (total*width + maxTok/2) / maxTok
	cells = min(max(cells, 1), width)
	co := (out*cells + total/2) / total
	if out > 0 && co < 1 {
		co = 1
	}
	ci := cells - co
	if in > 0 && ci < 1 {
		ci, co = 1, cells-1
	}
	if in <= 0 {
		ci, co = 0, cells
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", ci) + strings.Repeat("▓", co))
}

func (c *CockpitModel) tokenChart(w, h int) []string {
	lines := []string{lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("TOKENS PER CALL")}
	cols := make([]chartColumn, len(c.sessions))
	for i, s := range c.sessions {
		cols[i] = chartColumn{value: s.in + s.out, color: roleColor(string(s.role))}
	}
	ch, hidden := columnChart(cols, w, max(1, min(8, h/4)))
	lines = append(lines, ch...)

	visible := c.sessions[hidden:]
	marks := make([]byte, w)
	for i := range marks {
		marks[i] = ' '
	}
	prev := ""
	for i, s := range visible {
		if l := phaseLetters[s.phase]; l != "" && (i == 0 || s.phase != prev) && 2*i < w {
			marks[2*i] = l[0]
		}
		prev = s.phase
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render(string(marks)))
	totalCalls := 0
	for _, a := range c.agents {
		if a != nil {
			totalCalls += a.calls
		}
	}
	if hidden += totalCalls - len(c.sessions); hidden > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(crt.dim).Render(fmt.Sprintf("+%d earlier calls", hidden)))
	}
	return lines
}

func (c *CockpitModel) tokenLog(w int) []string {
	lines := []string{lipgloss.NewStyle().Foreground(crt.dim).Bold(true).Render("CALL LOG")}
	for i := len(c.sessions) - 1; i >= 0; i-- {
		s := c.sessions[i]
		sub := s.subtask
		if sub == "" {
			sub = "—"
		}
		mark := ""
		if s.estimated {
			mark = "~"
		}
		role := lipgloss.NewStyle().Foreground(roleColor(string(s.role))).Render(fmt.Sprintf("%-8s", roleLabel(s.role)))
		body := fmt.Sprintf("#%-3d %s  ", s.seq, s.at.Format("15:04:05")) + role +
			fmt.Sprintf(" %-7s %-4s %6s %6s %6s",
				s.phase, sub, formatTokensCompact(s.in), formatTokensCompact(s.out), formatTokensCompact(s.in+s.out))
		lines = append(lines, ansi.Truncate(body, w-len(mark), "…")+mark)
	}
	return lines
}
