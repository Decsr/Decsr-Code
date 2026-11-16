package automation

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestEntryFormatParse(t *testing.T) {
	start := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	e := Entry{Start: start, End: start.Add(12 * time.Minute), CostUSD: 0.4, Result: "pr #12 checks passed"}
	line := e.Format()
	want := "start=2026-11-16T03:00:00Z end=2026-11-16T03:12:00Z cost=$0.40 result=pr #12 checks passed"
	if line != want {
		t.Fatalf("Format = %q\nwant     %q", line, want)
	}
	got, ok := ParseEntry(line)
	if !ok || !got.Start.Equal(e.Start) || !got.End.Equal(e.End) || got.CostUSD != 0.4 || got.Result != e.Result {
		t.Errorf("ParseEntry = %+v, %v", got, ok)
	}
	if strings.Contains(Entry{Start: start, End: start, Result: "a\nb  c"}.Format(), "\n") {
		t.Error("line break in a log line")
	}
	for _, bad := range []string{"", "hello", "start=x end=y cost=$1 result=z", "start=2026-11-16T03:00:00Z end=2026-11-16T03:00:00Z"} {
		if _, ok := ParseEntry(bad); ok {
			t.Errorf("parsed %q", bad)
		}
	}
}

func TestAppendAndLastEntry(t *testing.T) {
	root := t.TempDir()
	if _, ok := LastEntry(root, "a"); ok {
		t.Fatal("entry in a missing log")
	}
	t0 := time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)
	for i, res := range []string{"nothing to do", "needs you"} {
		s := t0.Add(time.Duration(i) * time.Hour)
		if err := AppendLog(root, "a", Entry{Start: s, End: s.Add(time.Minute), Result: res}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.OpenFile(LogPath(root, "a"), os.O_APPEND|os.O_WRONLY, 0o600)
	_, _ = f.WriteString("cron said something\n")
	_ = f.Close()
	last, ok := LastEntry(root, "a")
	if !ok || last.Result != "needs you" {
		t.Errorf("last = %+v, %v", last, ok)
	}
}
