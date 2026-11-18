package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

// DiffLine is one line of a hunk. Kind is '+', '-', ' ' or '\\' (the "No
// newline at end of file" marker). OldNo is 0 for an added line and NewNo is 0
// for a removed one.
type DiffLine struct {
	Kind  byte
	OldNo int
	NewNo int
	Text  string
}

// Hunk is one "@@" block.
type Hunk struct {
	Header string
	Lines  []DiffLine
}

// DiffFile is the change to one file. OldPath is set only for a rename.
type DiffFile struct {
	Path    string
	OldPath string
	Added   int
	Removed int
	Binary  bool
	New     bool
	Deleted bool
	Hunks   []Hunk
}

// Diff is a parsed change set. Note is a dim line to show above it, for
// example that the diff may include other changes.
type Diff struct {
	Files []DiffFile
	Note  string
}

// Totals is the lines added and removed over all files.
func (d Diff) Totals() (added, removed int) {
	for _, f := range d.Files {
		added += f.Added
		removed += f.Removed
	}
	return added, removed
}

// maxUntracked caps the untracked files shown in a diff of a working tree.
const maxUntracked = 100

// gitOut runs git in dir and returns stdout. Exit code 1 is accepted when
// okOne is set (git diff --no-index exits 1 when the files differ).
func gitOut(dir string, okOne bool, args ...string) (string, error) {
	cmd := executil.Command("git", append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if okOne && errors.As(err, &ee) && ee.ExitCode() == 1 {
			return stdout.String(), nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

// NodeDiff returns the change a node made. With isolation it is the node's own
// commit; while the node runs (or kept its worktree after a failed merge) it
// is the worktree against the point it started from, untracked files
// included. Without isolation it is the working tree change to the node's
// files in root, which may include changes of other nodes or of the user.
func NodeDiff(root string, n *Node, iso *Isolator) (Diff, error) {
	if n == nil {
		return Diff{}, errors.New("no node")
	}
	if n.Kind == KindCheck {
		return Diff{}, nil
	}
	if iso == nil {
		return sharedDiff(root, n)
	}
	if wt := filepath.Join(iso.nodePath(n.ID), iso.prefix); dirExists(wt) {
		return worktreeDiff(iso, wt)
	}
	if n.Commit != "" {
		out, err := gitOut(iso.root, false, "show", "--format=", "--no-color", "--relative", "-U3", n.Commit)
		if err != nil {
			return Diff{}, fmt.Errorf("commit %s of %s is not available any more: %s", shortSHA(n.Commit), n.ID, oneLineErr(err))
		}
		return Diff{Files: ParseDiff(out)}, nil
	}
	switch n.Status {
	case StatusDone:
		if len(n.FileStats) == 0 && len(n.AllFiles()) == 0 {
			return Diff{}, nil
		}
		return Diff{}, fmt.Errorf("no commit is recorded for %s (it finished before diffs were kept)", n.ID)
	default:
		return Diff{}, nil
	}
}

// SessionDiff is everything the session branch holds that the current branch
// of root does not: git diff HEAD...decsr/<session>.
func SessionDiff(root, session string) (Diff, error) {
	if !sessionIDRe.MatchString(session) {
		return Diff{}, fmt.Errorf("invalid session id %q", session)
	}
	branch := SessionBranch(session)
	if _, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err != nil {
		return Diff{}, errors.New("nothing has been merged into the session branch yet")
	}
	out, err := gitOut(root, false, "diff", "--no-color", "--relative", "-U3", "HEAD..."+branch)
	if err != nil {
		return Diff{}, fmt.Errorf("session diff: %s", oneLineErr(err))
	}
	return Diff{Files: ParseDiff(out)}, nil
}

// WorkingTreeDiff is every change of the working tree of root against HEAD,
// untracked files included: the session diff of a shared checkout.
func WorkingTreeDiff(root string) (Diff, error) {
	if _, err := git(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return Diff{}, errors.New("the project is not a git repository, so there is no diff")
	}
	args := []string{"diff", "--no-color", "--relative", "-U3"}
	if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		args = append(args, "HEAD")
	}
	out, err := gitOut(root, false, args...)
	if err != nil {
		return Diff{}, fmt.Errorf("diff: %s", oneLineErr(err))
	}
	d := Diff{Note: "Shared checkout: the working tree compared with HEAD.", Files: ParseDiff(out)}
	more, err := untrackedDiff(root, nil)
	if err != nil {
		return Diff{}, err
	}
	d.Files = append(d.Files, more...)
	return d, nil
}

// worktreeDiff compares a node's worktree with the point it started from (the
// merge base with the session branch, or HEAD when that is unknown).
// Untracked files are added with "git diff --no-index /dev/null <file>", which
// reads the file and leaves the index alone, so the worktree is not touched.
func worktreeDiff(iso *Isolator, wt string) (Diff, error) {
	base := "HEAD"
	if mb, err := git(wt, "merge-base", "HEAD", iso.Branch()); err == nil && mb != "" {
		base = mb
	}
	out, err := gitOut(wt, false, "diff", "--no-color", "--relative", "-U3", base)
	if err != nil {
		return Diff{}, fmt.Errorf("worktree diff: %s", oneLineErr(err))
	}
	files := ParseDiff(out)
	more, err := untrackedDiff(wt, nil)
	if err != nil {
		return Diff{}, err
	}
	return Diff{Files: append(files, more...)}, nil
}

// sharedDiff is the working tree change to the node's files.
func sharedDiff(root string, n *Node) (Diff, error) {
	var paths []string
	for _, p := range n.AllFiles() {
		p = filepath.ToSlash(filepath.Clean(p))
		if p == "." || p == ".." || strings.HasPrefix(p, "../") || filepath.IsAbs(p) {
			continue
		}
		paths = append(paths, p)
	}
	d := Diff{Note: "Shared checkout: this diff may include changes from other nodes or from you."}
	if len(paths) == 0 {
		return d, nil
	}
	if _, err := git(root, "rev-parse", "--is-inside-work-tree"); err != nil {
		return Diff{}, errors.New("the project is not a git repository, so there is no diff")
	}
	args := []string{"diff", "--no-color", "--relative", "-U3"}
	if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		args = append(args, "HEAD")
	}
	out, err := gitOut(root, false, append(args, append([]string{"--"}, paths...)...)...)
	if err != nil {
		return Diff{}, fmt.Errorf("diff: %s", oneLineErr(err))
	}
	d.Files = ParseDiff(out)
	more, err := untrackedDiff(root, paths)
	if err != nil {
		return Diff{}, err
	}
	d.Files = append(d.Files, more...)
	return d, nil
}

// untrackedDiff diffs the untracked, not ignored files under dir (limited to
// paths when given) against nothing.
func untrackedDiff(dir string, paths []string) ([]DiffFile, error) {
	args := []string{"ls-files", "-z", "--others", "--exclude-standard"}
	if len(paths) > 0 {
		args = append(args, append([]string{"--"}, paths...)...)
	}
	list, err := gitOut(dir, false, args...)
	if err != nil {
		return nil, fmt.Errorf("untracked files: %s", oneLineErr(err))
	}
	var out []DiffFile
	for i, p := range strings.Split(list, "\x00") {
		if p == "" {
			continue
		}
		if i >= maxUntracked {
			break
		}
		txt, err := gitOut(dir, true, "diff", "--no-color", "-U3", "--no-index", "--", os.DevNull, p)
		if err != nil {
			continue
		}
		out = append(out, ParseDiff(txt)...)
	}
	return out, nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func oneLineErr(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ParseDiff parses unified diff text (git diff or git show output).
func ParseDiff(text string) []DiffFile {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	var files []DiffFile
	var cur *DiffFile
	var hunk *Hunk
	oldNo, newNo := 0, 0
	flush := func() {
		if hunk != nil && cur != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	finish := func() {
		flush()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			finish()
			cur = &DiffFile{Path: headerPath(rest)}
			continue
		}
		if cur == nil {
			continue
		}
		if hunk == nil {
			switch {
			case strings.HasPrefix(line, "rename from "):
				cur.OldPath = unquoteGit(strings.TrimPrefix(line, "rename from "))
			case strings.HasPrefix(line, "rename to "):
				cur.Path = unquoteGit(strings.TrimPrefix(line, "rename to "))
			case strings.HasPrefix(line, "new file mode"):
				cur.New = true
			case strings.HasPrefix(line, "deleted file mode"):
				cur.Deleted = true
			case strings.HasPrefix(line, "Binary files "), strings.HasPrefix(line, "GIT binary patch"):
				cur.Binary = true
			case strings.HasPrefix(line, "+++ "):
				if p := diffSidePath(line[4:], "b/"); p != "" {
					cur.Path = p
				}
			}
		}
		if strings.HasPrefix(line, "@@") {
			flush()
			hunk = &Hunk{Header: line}
			oldNo, newNo = parseHunkStart(line)
			continue
		}
		if hunk == nil {
			continue
		}
		switch {
		case line == "":
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: ' ', OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
		case line[0] == '+':
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: '+', NewNo: newNo, Text: line[1:]})
			newNo++
			cur.Added++
		case line[0] == '-':
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: '-', OldNo: oldNo, Text: line[1:]})
			oldNo++
			cur.Removed++
		case line[0] == '\\':
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: '\\', Text: line})
		default:
			hunk.Lines = append(hunk.Lines, DiffLine{Kind: ' ', OldNo: oldNo, NewNo: newNo, Text: line[1:]})
			oldNo++
			newNo++
		}
	}
	finish()
	for i := range files {
		f := &files[i]
		if f.OldPath == f.Path {
			f.OldPath = ""
		}
	}
	return files
}

// headerPath is the new path of "a/P b/P" (the rest of a "diff --git" line).
// With a rename the rename lines replace it.
func headerPath(rest string) string {
	if strings.HasPrefix(rest, `"`) {
		// Quoted paths: take the last quoted token.
		if i := strings.LastIndex(rest, ` "`); i >= 0 {
			return diffSidePath(rest[i+1:], "b/")
		}
		return ""
	}
	if n := len(rest); n >= 5 && n%2 == 1 && strings.HasPrefix(rest, "a/") {
		half := (n - 5) / 2
		if rest[2+half:2+half+3] == " b/" && rest[2:2+half] == rest[5+half:] {
			return rest[2 : 2+half]
		}
	}
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return rest[i+3:]
	}
	return rest
}

// diffSidePath is the path of a "--- " or "+++ " line without the a/ or b/
// prefix and a trailing tab; "" for /dev/null.
func diffSidePath(s, prefix string) string {
	s = strings.TrimSuffix(s, "\t")
	if i := strings.Index(s, "\t"); i >= 0 {
		s = s[:i]
	}
	s = unquoteGit(s)
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

func unquoteGit(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// parseHunkStart reads the first old and new line numbers of "@@ -a,b +c,d @@".
func parseHunkStart(h string) (oldNo, newNo int) {
	f := strings.Fields(h)
	if len(f) < 3 {
		return 1, 1
	}
	num := func(s string) int {
		s = strings.TrimLeft(s, "-+")
		if i := strings.IndexByte(s, ','); i >= 0 {
			s = s[:i]
		}
		v, _ := strconv.Atoi(s)
		return v
	}
	return num(f[1]), num(f[2])
}
