package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// reviewKeyPrefix starts the selection ID of a review card.
const reviewKeyPrefix = "review:"

// reviewKey is the selection and expansion key of a review card.
func reviewKey(r workspace.Review) string {
	return reviewKeyPrefix + strconv.FormatInt(r.At.UnixNano(), 10)
}

// reviewHeadline is "Lead review 2 · 3 changes applied".
func reviewHeadline(r workspace.Review) string {
	var what string
	switch r.State {
	case workspace.ReviewPending:
		what = fmt.Sprintf("%d %s waiting for you", len(r.Applied), pluralize(len(r.Applied), "change", "changes"))
	case workspace.ReviewApplied:
		what = fmt.Sprintf("%d %s applied", len(r.Applied), pluralize(len(r.Applied), "change", "changes"))
	case workspace.ReviewRejected:
		what = "rejected"
	default:
		what = "no changes"
	}
	return fmt.Sprintf("Lead review %d · %s", r.Round, what)
}

// proposalRow is one "+ add T7 title" row of a review.
func proposalRow(a workspace.AppliedProposal) (text string, colour lipgloss.TerminalColor) {
	switch a.Action {
	case workspace.ActionAdd:
		t := "+ add " + a.NodeID + " " + a.Title
		if a.Role != "" {
			t += " [" + a.Role + "]"
		}
		if len(a.DependsOn) > 0 {
			t += " after " + strings.Join(a.DependsOn, ", ")
		}
		return t, colOK
	case workspace.ActionChange:
		t := "~ change " + a.NodeID
		if a.Title != "" {
			t += " " + a.Title
		}
		if i := strings.Index(a.Text, "("); i >= 0 {
			t += " " + a.Text[i:]
		}
		return t, colAccent
	}
	t := "× cancel " + a.NodeID
	if a.Reason != "" {
		t += " — " + a.Reason
	}
	return t, colFail
}

// skippedRow is the dim row of a proposal that was not valid.
func skippedRow(s workspace.SkippedProposal) string {
	what := s.Action
	switch {
	case s.ID != "":
		what += " " + s.ID
	case s.Title != "":
		what += " " + s.Title
	}
	return "skipped " + what + " — " + s.Reason
}

// clipRows wraps text to w cells and keeps at most max rows, the last ending
// in "…" when text is longer.
func clipRows(text string, w, max int) []string {
	lines := hardWrap(oneLine(workspace.SanitizeText(text)), w)
	if len(lines) > max {
		lines = lines[:max]
		lines[max-1] = truncateOneLine(lines[max-1]+" …", w)
	}
	return lines
}

// renderReviewCard draws a lead review in at most width cells. A review that
// is resolved and not expanded is one dim line; a pending or expanded one is a
// rounded card with the note, the proposals and the skipped ones.
func renderReviewCard(r workspace.Review, width int, selected, expanded bool) []string {
	dim := lipgloss.NewStyle().Foreground(colMuted)
	if r.State != workspace.ReviewPending && !expanded {
		st := dim
		if selected {
			st = lipgloss.NewStyle().Foreground(colAccent)
		}
		return []string{st.Render(truncateOneLine(reviewHeadline(r)+" ›", width))}
	}
	iw := max(width-4, 4)
	text := lipgloss.NewStyle().Foreground(colText)
	head := lipgloss.NewStyle().Foreground(colAccent).Bold(true).Render("Lead review") + dim.Render(fmt.Sprintf(" %d · ", r.Round)) +
		dim.Render(strings.TrimPrefix(reviewHeadline(r), fmt.Sprintf("Lead review %d · ", r.Round)))
	clock := dim.Render(feedClock(r.At))
	gap := max(iw-ansi.StringWidth(head)-ansi.StringWidth(clock), 1)
	rows := []string{head + strings.Repeat(" ", gap) + clock}
	if r.Note != "" {
		for _, l := range clipRows(r.Note, iw, 3) {
			rows = append(rows, text.Render(l))
		}
	}
	for _, a := range r.Applied {
		t, col := proposalRow(a)
		st := lipgloss.NewStyle().Foreground(col)
		if r.State == workspace.ReviewRejected {
			st = dim
		}
		for i, l := range clipRows(t, iw-2, 2) {
			p := "  "
			if i == 0 {
				p = ""
			}
			rows = append(rows, st.Render(p+l))
		}
	}
	for _, s := range r.Skipped {
		for i, l := range clipRows(skippedRow(s), iw-2, 2) {
			p := "  "
			if i == 0 {
				p = ""
			}
			rows = append(rows, dim.Render(p+l))
		}
	}
	if len(r.Applied) == 0 && len(r.Skipped) == 0 && r.State == workspace.ReviewEmpty && r.Note == "" {
		rows = append(rows, dim.Render("no changes proposed"))
	}
	if r.State == workspace.ReviewPending {
		rows = append(rows, text.Render("y apply · n reject")+dim.Render("  (/review y, /review n)"))
	}
	bc := colBorder
	switch {
	case selected:
		bc = colAccent
	case r.State == workspace.ReviewPending:
		bc = colWarn
	}
	b := lipgloss.NewStyle().Foreground(bc)
	out := make([]string, 0, len(rows)+2)
	out = append(out, b.Render("╭"+strings.Repeat("─", iw+2)+"╮"))
	for _, row := range rows {
		out = append(out, b.Render("│")+" "+padRight(ansi.Truncate(row, iw, "…"), iw)+" "+b.Render("│"))
	}
	return append(out, b.Render("╰"+strings.Repeat("─", iw+2)+"╯"))
}
