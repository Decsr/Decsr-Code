package automation

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// Entry is one run in the log of an automation.
type Entry struct {
	Start, End time.Time
	CostUSD    float64
	Result     string
}

// Format is the log line of an entry:
//
//	start=<RFC3339> end=<RFC3339> cost=$0.42 result=<text>
func (e Entry) Format() string {
	res := strings.Join(strings.Fields(e.Result), " ")
	return fmt.Sprintf("start=%s end=%s cost=$%.2f result=%s",
		e.Start.Format(time.RFC3339), e.End.Format(time.RFC3339), e.CostUSD, res)
}

// ParseEntry reads a line written by Format.
func ParseEntry(line string) (Entry, bool) {
	var e Entry
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "start=")
	if !ok {
		return e, false
	}
	startS, rest, ok := strings.Cut(rest, " end=")
	if !ok {
		return e, false
	}
	endS, rest, ok := strings.Cut(rest, " cost=$")
	if !ok {
		return e, false
	}
	costS, res, ok := strings.Cut(rest, " result=")
	if !ok {
		return e, false
	}
	var err error
	if e.Start, err = time.Parse(time.RFC3339, startS); err != nil {
		return e, false
	}
	if e.End, err = time.Parse(time.RFC3339, endS); err != nil {
		return e, false
	}
	if _, err = fmt.Sscanf(costS, "%f", &e.CostUSD); err != nil {
		return e, false
	}
	e.Result = res
	return e, true
}

// AppendLog adds one entry to the log of name.
func AppendLog(root, name string, e Entry) error {
	if err := os.MkdirAll(Dir(root), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(LogPath(root, name), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600) // #nosec G304 -- path under the project root, validated name
	if err != nil {
		return err
	}
	_, werr := fmt.Fprintln(f, e.Format())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// LastEntry is the newest entry of the log of name. ok is false when the log
// is missing or holds no entry (other lines, such as cron output, are skipped).
func LastEntry(root, name string) (last Entry, ok bool) {
	f, err := os.Open(LogPath(root, name)) // #nosec G304 -- path under the project root, validated name
	if err != nil {
		return last, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		if e, good := ParseEntry(sc.Text()); good {
			last, ok = e, true
		}
	}
	return last, ok
}
