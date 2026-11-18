package workspace

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/prompts"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const (
	// maxCompactChatChars caps the chat text of a compaction input; the oldest
	// lines are dropped first.
	maxCompactChatChars = 60000
	// maxCompactLineChars cuts one chat line.
	maxCompactLineChars = 2000
	// maxCompactNodeChars cuts the result of one node.
	maxCompactNodeChars = 600
)

// CompactInput is what the compaction call is given: the earlier summary, the
// chat of the current lead conversation (the lines at or after since), the
// nodes and an optional focus.
func CompactInput(summary string, transcript []Line, since time.Time, g *Graph, focus string) string {
	var sb strings.Builder
	if s := strings.TrimSpace(summary); s != "" {
		sb.WriteString("## Earlier summary\n" + s + "\n\n")
	}
	var lines []string
	for _, l := range transcript {
		if !since.IsZero() && l.At.Before(since) {
			continue
		}
		text := strings.TrimSpace(SanitizeText(l.Text))
		if text == "" {
			continue
		}
		who := "decsr"
		switch l.Who {
		case "you":
			who = "User"
		case "lead":
			who = "Lead"
		case "error":
			who = "decsr error"
		}
		lines = append(lines, who+": "+cutRunes(text, maxCompactLineChars))
	}
	total := 0
	start := len(lines)
	for start > 0 && total+len(lines[start-1]) <= maxCompactChatChars {
		start--
		total += len(lines[start]) + 1
	}
	if start < len(lines) {
		sb.WriteString("## Chat\n" + strings.Join(lines[start:], "\n") + "\n\n")
	}
	if g != nil {
		if nodes := compactNodes(g); nodes != "" {
			sb.WriteString("## Nodes\n" + nodes + "\n\n")
		}
		if open := compactOpen(g); open != "" {
			sb.WriteString("## Open questions and problems\n" + open + "\n\n")
		}
	}
	if f := strings.TrimSpace(focus); f != "" {
		sb.WriteString("## Focus\n" + f + "\n")
	}
	return strings.TrimSpace(sb.String())
}

func compactNodes(g *Graph) string {
	var sb strings.Builder
	for _, n := range g.Nodes {
		if n.Kind != KindWork {
			continue
		}
		fmt.Fprintf(&sb, "- %s %q [%s]", n.ID, Sanitize(n.Title), n.Status)
		if r := orRole(n); r != "" {
			sb.WriteString(" role " + r)
		}
		if len(n.MCP) > 0 {
			sb.WriteString(" mcp: " + strings.Join(n.MCP, ", "))
		}
		if files := n.AllFiles(); len(files) > 0 {
			sb.WriteString(" files: " + strings.Join(files, ", "))
		}
		if s := strings.Join(strings.Fields(n.Summary), " "); s != "" {
			sb.WriteString(": " + cutRunes(s, maxCompactNodeChars))
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// compactOpen lists the nodes that wait for the user or failed.
func compactOpen(g *Graph) string {
	var sb strings.Builder
	for _, n := range g.Nodes {
		if n.Kind != KindWork {
			continue
		}
		switch {
		case n.Status == StatusWaiting && n.Report != nil && n.Report.Ask() != "":
			fmt.Fprintf(&sb, "- %s asks: %s\n", n.ID, cutRunes(Sanitize(n.Report.Ask()), maxCompactNodeChars))
		case n.Status == StatusFailed:
			fmt.Fprintf(&sb, "- %s failed: %s\n", n.ID, cutRunes(Sanitize(n.Error), maxCompactNodeChars))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// RunCompact asks the lead for the summary of a conversation. It is a call
// without tools.
func RunCompact(ctx context.Context, r runner.LLMRunner, model, input string, onToken func(string)) (string, runner.TokenUsage, error) {
	req := runner.CompletionRequest{
		SystemPrompt: prompts.MustLoad("ws-compact"),
		Model:        model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: input}},
	}
	raw, usage, err := splitComplete(ctx, r, req, onToken)
	if err != nil {
		return "", usage, fmt.Errorf("compact: %s", strings.TrimPrefix(err.Error(), "split: "))
	}
	out := strings.TrimSpace(SanitizeText(raw))
	if out == "" {
		return "", usage, fmt.Errorf("compact: the lead's summary is empty")
	}
	return cutRunes(out, maxSummaryChars), usage, nil
}
