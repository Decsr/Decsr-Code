package workspace

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// FixExcerptChars is the budget of the "What failed" excerpt in a fix node.
const FixExcerptChars = 8000

// markerReserve is the room kept for the two omission markers.
const markerReserve = 100

// FixExcerpt builds a budgeted excerpt of what failed in the given nodes, for
// a fix node. See FixExcerptWith.
func FixExcerpt(failed []*Node, maxChars int) string {
	return FixExcerptWith(failed, maxChars, nil)
}

// FixExcerptWith builds an excerpt of at most maxChars characters from failed
// nodes, in this priority order until the budget is spent: failing commands
// with the tail of their output, then other errors (error activities and the
// node error), then the files touched as one line. Thinking and message items
// are never included. Where text was cut it says "[… N characters omitted]".
// detail, when not nil, returns the full detail of an activity (kept in memory
// only); the stored, shorter detail is the fallback.
func FixExcerptWith(failed []*Node, maxChars int, detail func(nodeID, actID string) (string, bool)) string {
	if maxChars <= 0 {
		return ""
	}
	var cmds, errs []string
	files := map[string]bool{}
	var fileList []string
	for _, n := range failed {
		if n == nil {
			continue
		}
		hasCmd := false
		for _, a := range n.Activity {
			if a.State != runner.ActFailed {
				continue
			}
			d := a.Detail
			if detail != nil {
				if full, ok := detail(n.ID, a.ID); ok {
					d = full
				}
			}
			switch a.Kind {
			case runner.ActCommand:
				hasCmd = true
				head := fmt.Sprintf("[%s] $ %s", n.ID, a.Title)
				if a.ExitCode != nil {
					head += fmt.Sprintf(" (exit %d)", *a.ExitCode)
				}
				cmds = append(cmds, joinNonEmpty(head, strings.TrimSpace(d)))
			case runner.ActError:
				errs = append(errs, joinNonEmpty(fmt.Sprintf("[%s] error: %s", n.ID, a.Title), strings.TrimSpace(d)))
			}
		}
		// The node error repeats the output of a failing command, so it is
		// listed only when no command failed.
		if e := strings.TrimSpace(n.Error); e != "" && !hasCmd {
			errs = append(errs, fmt.Sprintf("[%s] %s", n.ID, e))
		}
		for _, p := range n.AllFiles() {
			add(files, &fileList, p)
		}
		for _, fs := range n.FileStats {
			add(files, &fileList, fs.Path)
		}
	}
	parts := append(cmds, errs...)
	if len(fileList) > 0 {
		parts = append(parts, "Files touched: "+strings.Join(fileList, ", "))
	}

	budget := maxChars
	if budget > 2*markerReserve {
		budget -= markerReserve
	}
	var out []string
	used := 0
	for i, p := range parts {
		sep := 0
		if len(out) > 0 {
			sep = 2
		}
		size := utf8.RuneCountInString(p)
		if used+sep+size <= budget {
			out = append(out, p)
			used += sep + size
			continue
		}
		// Cut this part (keeping its end, where the failure is) and drop the rest.
		room := budget - used - sep - 40
		omitted := size
		if room > 40 {
			r := []rune(p)
			out = append(out, fmt.Sprintf("[… %d characters omitted]\n", size-room)+string(r[size-room:]))
			omitted = 0
		}
		rest := 0
		for _, q := range parts[i+1:] {
			rest += utf8.RuneCountInString(q)
		}
		if tail := omitted + rest; tail > 0 {
			out = append(out, fmt.Sprintf("[… %d characters omitted]", tail))
		}
		break
	}
	res := strings.Join(out, "\n\n")
	if utf8.RuneCountInString(res) > maxChars {
		res = string([]rune(res)[:maxChars])
	}
	return res
}

func add(seen map[string]bool, list *[]string, p string) {
	if p != "" && !seen[p] {
		seen[p] = true
		*list = append(*list, p)
	}
}

func joinNonEmpty(head, body string) string {
	if body == "" {
		return head
	}
	return head + "\n" + body
}
