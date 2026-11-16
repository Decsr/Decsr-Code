package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

type actLog struct {
	mu   sync.Mutex
	acts []runner.Activity
}

func (l *actLog) add(a runner.Activity) {
	l.mu.Lock()
	l.acts = append(l.acts, a)
	l.mu.Unlock()
}

func (l *actLog) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, a := range l.acts {
		out = append(out, fmt.Sprintf("%s %s %q +%d -%d", a.Kind, a.State, a.Title, a.Added, a.Removed))
	}
	return out
}

func wantLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("activities differ\n got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestLineDiffCounts(t *testing.T) {
	cases := []struct {
		name, old, next string
		added, removed  int
	}{
		{"same", "a\nb\n", "a\nb\n", 0, 0},
		{"new file", "", "a\nb\nc\n", 3, 0},
		{"append", "a\nb\n", "a\nb\nc\nd\n", 2, 0},
		{"delete", "a\nb\nc\n", "a\nc\n", 0, 1},
		{"replace middle", "a\nb\nc\n", "a\nX\nY\nc\n", 2, 1},
		{"moved line", "a\nb\nc\n", "b\nc\na\n", 1, 1},
		{"no trailing newline", "a\nb", "a\nc", 1, 1},
		{"emptied", "a\nb\n", "", 0, 2},
	}
	for _, c := range cases {
		a, r := lineDiffCounts(c.old, c.next)
		if a != c.added || r != c.removed {
			t.Errorf("%s: got +%d -%d, want +%d -%d", c.name, a, r, c.added, c.removed)
		}
	}
}

func TestLineDiffCountsHugeFileFallsBack(t *testing.T) {
	var old, next strings.Builder
	for i := 0; i < maxDiffLines+10; i++ {
		fmt.Fprintf(&old, "old %d\n", i)
		fmt.Fprintf(&next, "new %d\n", i)
	}
	a, r := lineDiffCounts(old.String(), next.String())
	if a != maxDiffLines+10 || r != maxDiffLines+10 {
		t.Errorf("fallback = +%d -%d", a, r)
	}
}

func TestStreamAndWriteFilesActivities(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("pkg/edit.txt", "one\ntwo\nthree\n")
	mustWrite("pkg/same.txt", "same\n")

	var log actLog
	a := &CoderAgent{root: root, BaseAgent: NewBase("coder", bus.New())}
	a.OnActivity = log.add

	ch := make(chan runner.Token, 64)
	ch <- runner.Token{Activity: &runner.Activity{ID: "r1", Kind: runner.ActThinking, State: runner.ActDone, Title: "t"}}
	for _, l := range []string{
		"**pkg/new.txt**\n```\nA\nB\n```\n",
		"**pkg/edit.txt**\n```\none\nTWO\nthree\nfour\n```\n",
		"**pkg/same.txt**\n```\nsame\n```\n",
	} {
		ch <- runner.Token{Text: l}
	}
	ch <- runner.Token{Done: true}
	close(ch)

	written, _, _, err := a.streamAndWriteFiles(ch)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 3 {
		t.Fatalf("written = %v", written)
	}
	wantLines(t, log.lines(), []string{
		`thinking done "t" +0 -0`,
		`write running "pkg/new.txt" +0 -0`,
		`write done "pkg/new.txt" +2 -0`,
		`edit running "pkg/edit.txt" +0 -0`,
		`edit done "pkg/edit.txt" +2 -1`,
		`edit running "pkg/same.txt" +0 -0`,
		`edit done "pkg/same.txt" +0 -0`,
	})
	// A running item and its done update share one ID.
	if log.acts[1].ID != log.acts[2].ID || log.acts[1].ID == log.acts[3].ID {
		t.Errorf("IDs: %q %q %q", log.acts[1].ID, log.acts[2].ID, log.acts[3].ID)
	}
}

func TestStreamAndWriteFilesActivitiesNilCallback(t *testing.T) {
	root := t.TempDir()
	a := &CoderAgent{root: root, BaseAgent: NewBase("coder", bus.New())}
	ch := make(chan runner.Token, 4)
	ch <- runner.Token{Text: "**x.txt**\n```\nhi\n```\n"}
	ch <- runner.Token{Done: true}
	close(ch)
	if _, _, _, err := a.streamAndWriteFiles(ch); err != nil {
		t.Fatal(err)
	}
}

func TestRunWorkerActivities(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := "Plan.\n\n**new.txt**\n```\nx\ny\nz\n```\n\n**old.txt**\n```\na\nB\nc\n```\n===SUMMARY===\ndone\n"
	f := &workerFake{chunks: []string{out[:20], out[20:]}}
	var log actLog
	_, err := RunWorker(context.Background(), f, root, config.DefaultConfig(),
		WorkerRequest{Title: "T", OnActivity: log.add}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantLines(t, log.lines(), []string{
		`write running "new.txt" +0 -0`,
		`edit running "old.txt" +0 -0`,
		`write done "new.txt" +3 -0`,
		`edit done "old.txt" +2 -1`,
	})
}

func TestRunWorkerForwardsRunnerActivities(t *testing.T) {
	root := t.TempDir()
	r := &activityRunner{}
	var log actLog
	if _, err := RunWorker(context.Background(), r, root, config.DefaultConfig(),
		WorkerRequest{Title: "T", ReadOnly: true, OnActivity: log.add}, nil); err != nil {
		t.Fatal(err)
	}
	wantLines(t, log.lines(), []string{`command running "ls" +0 -0`, `command done "ls" +0 -0`})
}

func TestRunWorkerReadOnlyEmitsNoWriteActivities(t *testing.T) {
	root := t.TempDir()
	f := &workerFake{chunks: []string{"**a.txt**\n```\nx\n```\nreport\n"}}
	var log actLog
	if _, err := RunWorker(context.Background(), f, root, config.DefaultConfig(),
		WorkerRequest{Title: "T", ReadOnly: true, OnActivity: log.add}, nil); err != nil {
		t.Fatal(err)
	}
	if got := log.lines(); len(got) != 0 {
		t.Errorf("activities = %v", got)
	}
}

func TestRunWorkerFailedWriteIsReported(t *testing.T) {
	root := t.TempDir()
	f := &workerFake{chunks: []string{"**.git/config**\n```\nx\n```\n**ok.txt**\n```\ny\n```\n"}}
	var log actLog
	_, err := RunWorker(context.Background(), f, root, config.DefaultConfig(), WorkerRequest{Title: "T", OnActivity: log.add}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := log.lines()
	var failed bool
	for i, l := range lines {
		if strings.Contains(l, `.git/config"`) && strings.HasPrefix(l, "write failed") {
			failed = true
			if log.acts[i].Detail == "" {
				t.Error("failed write needs a reason")
			}
		}
	}
	if !failed {
		t.Errorf("want a failed write for .git/config, got %v", lines)
	}
}

// activityRunner emits runner activities and a plain text answer.
type activityRunner struct{}

func (activityRunner) Complete(_ context.Context, _ runner.CompletionRequest) (<-chan runner.Token, error) {
	ch := make(chan runner.Token, 5)
	ch <- runner.Token{Activity: &runner.Activity{ID: "c1", Kind: runner.ActCommand, State: runner.ActRunning, Title: "ls"}}
	ch <- runner.Token{Activity: &runner.Activity{ID: "c1", Kind: runner.ActCommand, State: runner.ActDone, Title: "ls"}}
	ch <- runner.Token{Text: "report"}
	ch <- runner.Token{Done: true}
	close(ch)
	return ch, nil
}
