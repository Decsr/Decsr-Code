package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// maxDiffLines caps the line diff: above it, a rewritten file counts all its
// new lines as added and all its old lines as removed.
const maxDiffLines = 5000

var writeActivitySeq atomic.Int64

// countTextLines is the number of lines of s (a trailing newline does not
// start a new line).
func countTextLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// lineDiffCounts returns how many lines were added and removed going from old
// to next, from the length of their longest common line subsequence. Equal
// heads and tails are skipped first; if more than maxDiffLines lines remain
// on either side the result falls back to all-new and all-old line counts.
func lineDiffCounts(old, next string) (added, removed int) {
	if old == next {
		return 0, 0
	}
	a, b := splitLines(old), splitLines(next)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		return countTextLines(next), countTextLines(old)
	}
	if len(a) == 0 || len(b) == 0 {
		return len(b), len(a)
	}
	// Longest common subsequence length with two rolling rows.
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			switch {
			case a[i-1] == b[j-1]:
				cur[j] = prev[j-1] + 1
			case prev[j] >= cur[j-1]:
				cur[j] = prev[j]
			default:
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	lcs := prev[len(b)]
	return len(b) - lcs, len(a) - lcs
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// writeTracker reports the file blocks decsr itself writes as Activities: a
// running item when a block starts and a done (or failed) item with the line
// counts when it is written. A nil emit makes it a no-op.
type writeTracker struct {
	root    string
	emit    func(runner.Activity)
	pending map[string][]runner.Activity // rel path -> started blocks, oldest first
}

func newWriteTracker(root string, emit func(runner.Activity)) *writeTracker {
	return &writeTracker{root: root, emit: emit, pending: map[string][]runner.Activity{}}
}

func (t *writeTracker) rel(path string) string {
	if r := sanitizeFilePath(path, t.root); r != "" {
		return filepath.ToSlash(filepath.Clean(r))
	}
	return strings.TrimSpace(path)
}

func (t *writeTracker) newActivity(rel string) runner.Activity {
	kind := runner.ActWrite
	if _, err := os.Stat(filepath.Join(t.root, rel)); err == nil { // #nosec G304 -- rel is sanitized under the project root
		kind = runner.ActEdit
	}
	return runner.Activity{
		ID:    fmt.Sprintf("write-%d", writeActivitySeq.Add(1)),
		Kind:  kind,
		State: runner.ActRunning,
		Title: runner.Clip(rel, 200),
		At:    time.Now(),
	}
}

// begin reports that a file block for path started.
func (t *writeTracker) begin(path string) {
	if t == nil || t.emit == nil {
		return
	}
	rel := t.rel(path)
	a := t.newActivity(rel)
	t.pending[rel] = append(t.pending[rel], a)
	t.emit(a)
}

// write runs do (the actual file write of content to path) and reports the
// result with the lines added and removed against the previous file.
func (t *writeTracker) write(path, content string, do func() error) error {
	if t == nil || t.emit == nil {
		return do()
	}
	rel := t.rel(path)
	var a runner.Activity
	if q := t.pending[rel]; len(q) > 0 {
		a, t.pending[rel] = q[0], q[1:]
	} else {
		a = t.newActivity(rel)
	}
	target := filepath.Join(t.root, rel)
	oldData, readErr := os.ReadFile(target) // #nosec G304 -- rel is sanitized under the project root
	existed := readErr == nil
	if existed {
		a.Kind = runner.ActEdit
	}

	err := do()
	noop := errors.Is(err, errNoOpRewrite)
	switch {
	case err != nil && !noop:
		a.State = runner.ActFailed
		a.Detail = runner.Clip(skipReason(err), 2000)
	case noop:
		a.State = runner.ActDone
		a.Added, a.Removed = 0, 0
	default:
		a.State = runner.ActDone
		written := content
		if data, rerr := os.ReadFile(target); rerr == nil { // #nosec G304 -- rel is sanitized under the project root
			written = string(data) // the writer may have fixed the content
		}
		if existed {
			a.Added, a.Removed = lineDiffCounts(string(oldData), written)
		} else {
			a.Added, a.Removed = countTextLines(written), 0
		}
	}
	a.Duration = time.Since(a.At)
	a.At = time.Now()
	t.emit(a)
	return err
}

// finish marks blocks that started but were never written as failed.
func (t *writeTracker) finish() {
	if t == nil || t.emit == nil {
		return
	}
	for rel, q := range t.pending {
		for _, a := range q {
			a.State = runner.ActFailed
			a.Detail = "not written"
			a.Duration = time.Since(a.At)
			a.At = time.Now()
			t.emit(a)
		}
		delete(t.pending, rel)
	}
}

// blockWatcher finds the start of file blocks in a streamed answer, with the
// same path rules as the Coder's stream writer, and reports their paths.
type blockWatcher struct {
	onStart func(path string)
	line    strings.Builder
	recent  []string
	inFence bool
	fence   fenceState
}

func (w *blockWatcher) feed(text string) {
	w.line.WriteString(text)
	for {
		s := w.line.String()
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return
		}
		w.line.Reset()
		w.line.WriteString(s[i+1:])
		w.handle(s[:i])
	}
}

func (w *blockWatcher) handle(line string) {
	trimmed := strings.TrimSpace(line)
	_, _, info, isFence := parseFence(trimmed)
	switch {
	case w.inFence && w.fence.closes(trimmed):
		w.inFence = false
	case !w.inFence && isFence:
		w.fence.open(trimmed)
		w.inFence = true
		path := findPathInRecent(w.recent)
		if path == "" {
			path = pathFromFenceTag(info)
		}
		if path != "" && w.onStart != nil {
			w.onStart(path)
		}
	}
	if !isFence {
		w.recent = append(w.recent, line)
		if len(w.recent) > 8 {
			w.recent = w.recent[1:]
		}
	}
}
