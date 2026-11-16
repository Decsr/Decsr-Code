package agent

import (
	"regexp"
	"strings"
)

// Report statuses a worker can end with.
const (
	ReportDone     = "done"
	ReportBlocked  = "blocked"
	ReportQuestion = "question"
)

// Report is the structured end of a worker's reply: how the sub-task ended, a
// short summary and, for a question, what to ask the user.
type Report struct {
	Status   string `json:"status"` // ReportDone, ReportBlocked or ReportQuestion
	Summary  string `json:"summary,omitempty"`
	Question string `json:"question,omitempty"`
}

// Waits reports whether the node must wait for the user: a question or a
// blocked report.
func (r Report) Waits() bool {
	return r.Status == ReportQuestion || r.Status == ReportBlocked
}

// Ask is the text to show the user: the question, else the summary.
func (r Report) Ask() string {
	if q := strings.TrimSpace(r.Question); q != "" {
		return q
	}
	return strings.TrimSpace(r.Summary)
}

// reportInstruction is appended to every worker prompt.
const reportInstruction = "\n\nAfter that, END your reply with exactly this block (the last lines of the reply, nothing after it):\n\nREPORT\nstatus: done | blocked | question\nsummary: <one or two sentences>\nquestion: <only when status is question: the exact question for the user>\n\nUse \"done\" when the sub-task is complete, \"blocked\" when you cannot continue without outside\nhelp (say what in the summary), and \"question\" when you need an answer from the user first."

var (
	reportMark = regexp.MustCompile("(?i)^[\\s>*_`#-]*report[\\s*_`:#]*$")
	reportKey  = regexp.MustCompile("(?i)^[\\s>*_`-]*(status|summary|question)[\\s*_`]*:[\\s*_`]*(.*)$")
	fenceLine  = regexp.MustCompile("^\\s*(```|~~~)")
)

// ParseReport finds the last REPORT block of a worker's output. body is the
// output without that block (and without a code fence opened just before it).
// found is false, and body is output unchanged, when there is no usable
// report: a REPORT line followed by a status key. An unknown status counts as
// done.
func ParseReport(output string) (rep Report, body string, found bool) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !reportMark.MatchString(lines[i]) {
			continue
		}
		r, ok := parseReportBlock(lines[i+1:])
		if !ok {
			continue
		}
		cut := i
		if cut > 0 && fenceLine.MatchString(lines[cut-1]) {
			cut--
		}
		return r, strings.TrimRight(strings.Join(lines[:cut], "\n"), " \t\n"), true
	}
	return Report{}, output, false
}

func parseReportBlock(lines []string) (Report, bool) {
	vals := map[string][]string{}
	cur := ""
	for _, l := range lines {
		if fenceLine.MatchString(l) {
			continue
		}
		if m := reportKey.FindStringSubmatch(l); m != nil {
			cur = strings.ToLower(m[1])
			if _, dup := vals[cur]; !dup {
				vals[cur] = []string{m[2]}
				continue
			}
			cur = "" // a repeated key is ignored
			continue
		}
		if cur != "" {
			vals[cur] = append(vals[cur], l)
		}
	}
	st, ok := vals["status"]
	if !ok {
		return Report{}, false
	}
	join := func(key string) string {
		s := strings.TrimSpace(strings.Join(vals[key], "\n"))
		return strings.Trim(s, "*_` \n")
	}
	status := strings.ToLower(strings.Trim(strings.TrimSpace(st[0]), "*_` .\t"))
	switch status {
	case ReportBlocked, ReportQuestion:
	default:
		status = ReportDone
	}
	return Report{Status: status, Summary: join("summary"), Question: join("question")}, true
}
