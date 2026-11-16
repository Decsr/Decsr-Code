package workspace

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// ErrSpecTaskMissing is returned by the tasks.md edit when no open line of the
// task exists any more.
var ErrSpecTaskMissing = errors.New("task not found")

// TickSpecTask marks a task done in the text of a tasks.md: the first line
// matching "- [ ] <id>" (any indentation, "-" or "*" bullet) gets "[x]" and
// nothing else changes, line endings included. changed is false when the task
// is already done; ErrSpecTaskMissing means no such line.
func TickSpecTask(data []byte, id string) (out []byte, changed bool, err error) {
	q := regexp.QuoteMeta(id)
	open := regexp.MustCompile(`^(\s*[-*]\s+\[) (\]\s+` + q + `\b)`)
	done := regexp.MustCompile(`^\s*[-*]\s+\[[xX]\]\s+` + q + `\b`)
	text := string(data)
	alreadyDone := false
	for pos := 0; pos < len(text); {
		end := strings.IndexByte(text[pos:], '\n')
		next := len(text)
		if end >= 0 {
			next = pos + end + 1
		}
		line := strings.TrimRight(text[pos:next], "\r\n")
		if m := open.FindStringSubmatchIndex(line); m != nil {
			// m[3] is the end of the first group, where the space sits.
			at := pos + m[3]
			return []byte(text[:at] + "x" + text[at+1:]), true, nil
		}
		if done.MatchString(line) {
			alreadyDone = true
		}
		pos = next
	}
	if alreadyDone {
		return data, false, nil
	}
	return data, false, ErrSpecTaskMissing
}

// writeFileAtomic replaces path with data through a temp file and a rename,
// keeping the file's mode.
func writeFileAtomic(p string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(p); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".decsr-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, mode)
	}
	if werr == nil {
		werr = os.Rename(name, p)
	}
	if werr != nil {
		_ = os.Remove(name)
	}
	return werr
}

// Tracked reports whether rel (relative to the project root) is tracked by git.
func (i *Isolator) Tracked(rel string) bool {
	_, err := git(i.root, "ls-files", "--error-unmatch", "--", rel)
	return err == nil
}

// CommitFileEdit edits rel (relative to the project root) in the integration
// worktree and commits it with msg. edit returns the new content and whether
// it changed anything; without a change nothing is written or committed.
func (i *Isolator) CommitFileEdit(rel string, edit func([]byte) ([]byte, bool, error), msg string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ensureReady(); err != nil {
		return err
	}
	p := filepath.Join(i.integ, i.prefix, filepath.FromSlash(rel))
	data, err := os.ReadFile(p) // #nosec G304 -- inside the integration worktree
	if err != nil {
		return err
	}
	out, changed, err := edit(data)
	if err != nil || !changed {
		return err
	}
	if err := writeFileAtomic(p, out); err != nil {
		return err
	}
	if _, err := git(i.integ, "add", "--", filepath.ToSlash(filepath.Join(i.prefix, rel))); err != nil {
		return err
	}
	_, err = git(i.integ, append(commitArgs(i.integ), "commit", "--no-verify", "-m", msg)...)
	return err
}

// sharedSpecMu serializes tick edits of the files in the user's checkout.
var sharedSpecMu sync.Mutex

// markSpecDone ticks the task of ref in tasks.md after its node finished. It
// returns a warning text, "" on success.
func (x *executor) markSpecDone(ref *SpecRef) string {
	rel := path.Clean(ref.File)
	if ref.TaskID == "" || path.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	edit := func(data []byte) ([]byte, bool, error) { return TickSpecTask(data, ref.TaskID) }
	var err error
	if x.iso != nil && x.iso.Tracked(rel) {
		err = x.iso.CommitFileEdit(rel, edit, fmt.Sprintf("decsr: mark %s done in %s", ref.TaskID, rel))
	} else {
		err = editFileInPlace(filepath.Join(x.root, filepath.FromSlash(rel)), edit)
	}
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrSpecTaskMissing):
		return fmt.Sprintf("tasks.md: %s not found, not marked done", ref.TaskID)
	default:
		return fmt.Sprintf("tasks.md: could not mark %s done: %v", ref.TaskID, err)
	}
}

func editFileInPlace(p string, edit func([]byte) ([]byte, bool, error)) error {
	sharedSpecMu.Lock()
	defer sharedSpecMu.Unlock()
	data, err := os.ReadFile(p) // #nosec G304 -- path under the project root
	if err != nil {
		return err
	}
	out, changed, err := edit(data)
	if err != nil || !changed {
		return err
	}
	return writeFileAtomic(p, out)
}

// SpecFeature is a feature directory with open tasks, found by DetectSpec.
type SpecFeature struct {
	Dir  string // "specs/003-login"
	Open int    // open tasks
}

// maxSpecDirs caps the feature directories DetectSpec looks at.
const maxSpecDirs = 200

// DetectSpec finds the most recently modified specs/*/tasks.md under root that
// has an open task. ok is false when there is none.
func DetectSpec(root string) (SpecFeature, bool) {
	entries, err := os.ReadDir(filepath.Join(root, "specs"))
	if err != nil {
		return SpecFeature{}, false
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Name() < entries[b].Name() })
	var best SpecFeature
	var bestTime int64 = -1
	seen := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if seen++; seen > maxSpecDirs {
			break
		}
		p := filepath.Join(root, "specs", e.Name(), "tasks.md")
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || st.ModTime().UnixNano() <= bestTime {
			continue
		}
		data, err := os.ReadFile(p) // #nosec G304 -- under the project root
		if err != nil {
			continue
		}
		open := 0
		for _, t := range ParseSpecKit(string(data)).Tasks {
			if !t.Done && t.ID != "" {
				open++
			}
		}
		if open > 0 {
			best, bestTime = SpecFeature{Dir: "specs/" + e.Name(), Open: open}, st.ModTime().UnixNano()
		}
	}
	return best, bestTime >= 0
}

// SpecUntracked lists the spec.md, plan.md and tasks.md of the feature
// directory dir (relative to the project root) that exist but are not tracked
// by git. It is nil when root is not in a git repository.
func SpecUntracked(root, dir string) []string {
	if _, err := git(root, "rev-parse", "--show-toplevel"); err != nil {
		return nil
	}
	var out []string
	for _, f := range []string{"tasks.md", "spec.md", "plan.md"} {
		rel := dir + "/" + f
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			continue
		}
		if _, err := git(root, "ls-files", "--error-unmatch", "--", rel); err != nil {
			out = append(out, f)
		}
	}
	return out
}
