package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

// Isolation modes of workspace.isolation. "" means auto.
const (
	IsolationWorktree = "worktree"
	IsolationShared   = "shared"
)

// Errors of MergeSession that the caller shows to the user as they are.
var (
	ErrNothingMerged = errors.New("no node of this session has been merged yet")
	ErrDirtyTree     = errors.New("your checkout has uncommitted changes to tracked files: commit or stash them first (the session branch starts from a snapshot of your working tree, so merging it brings those changes back as commits)")
)

var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SessionBranch is the integration branch of a session.
func SessionBranch(session string) string { return "decsr/" + session }

// nodeBranch is not "decsr/<session>/<id>": git cannot hold both that ref and
// the integration branch "decsr/<session>".
func nodeBranch(session, id string) string { return "decsr/" + session + "-" + id }

func worktreesDir(root, session string) string {
	return filepath.Join(root, ".decsr", "worktrees", session)
}

// fallbackIdent is used for the commits decsr makes when git has no identity.
var fallbackIdent = []string{"-c", "user.name=decsr", "-c", "user.email=decsr@localhost"}

// git runs git in dir and returns its trimmed combined output.
func git(dir string, args ...string) (string, error) {
	cmd := executil.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return s, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, s)
	}
	return s, nil
}

// commitArgs are the leading git arguments for a commit or merge: the user's
// identity when set, else a fallback, and no signing.
func commitArgs(dir string) []string {
	args := []string{"-c", "commit.gpgsign=false"}
	name, _ := git(dir, "config", "user.name")
	email, _ := git(dir, "config", "user.email")
	if name == "" || email == "" {
		args = append(args, fallbackIdent...)
	}
	return args
}

// Isolator gives every node of one session its own git worktree and branch
// and merges finished nodes into the session's integration branch.
type Isolator struct {
	root, session string
	prefix        string // path of root inside the repository, "" or "sub/dir/"

	mu       sync.Mutex // guards ready, warnings and every git change below
	ready    bool
	integ    string // integration worktree
	warnings []string
}

// NewIsolator returns the isolator for a session, or nil when nodes must share
// the checkout: mode is "shared", git is missing, root is not in a git
// repository, or the repository has no commit yet.
func NewIsolator(root, session, mode string) *Isolator {
	if mode == IsolationShared || !sessionIDRe.MatchString(session) {
		return nil
	}
	prefix, err := git(root, "rev-parse", "--show-prefix")
	if err != nil {
		return nil
	}
	if _, err := git(root, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		return nil
	}
	return &Isolator{root: root, session: session, prefix: prefix}
}

// Branch is the integration branch of the session.
func (i *Isolator) Branch() string { return SessionBranch(i.session) }

func (i *Isolator) nodePath(id string) string {
	return filepath.Join(worktreesDir(i.root, i.session), id)
}

// NodeDir is the project root inside a node's worktree, and whether that
// worktree exists (a running node, or a failed or conflicted one that kept it).
func (i *Isolator) NodeDir(id string) (string, bool) {
	if !nodeIDRe.MatchString(id) {
		return "", false
	}
	return existingDir(filepath.Join(i.nodePath(id), i.prefix))
}

// IntegrationDir is the project root inside the integration worktree (the
// merged result), and whether that worktree exists. Unlike IntegrationRoot it
// never creates it.
func (i *Isolator) IntegrationDir() (string, bool) {
	return existingDir(filepath.Join(worktreesDir(i.root, i.session), "_integration", i.prefix))
}

func existingDir(p string) (string, bool) {
	st, err := os.Stat(p)
	return p, err == nil && st.IsDir()
}

// ensureReady creates the integration branch and worktree on first use. The
// caller holds i.mu.
func (i *Isolator) ensureReady() error {
	if i.ready {
		return nil
	}
	if err := i.ensureExclude(); err != nil {
		i.warnings = append(i.warnings, "could not hide .decsr/worktrees from git: "+err.Error())
	}
	dir := worktreesDir(i.root, i.session)
	i.integ = filepath.Join(dir, "_integration")
	branch := i.Branch()
	if _, err := git(i.root, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err == nil {
		// A resumed session: the branch is there, its worktree may be gone.
		if _, err := os.Stat(i.integ); err != nil {
			_, _ = git(i.root, "worktree", "prune")
			if _, err := git(i.root, "worktree", "add", i.integ, branch); err != nil {
				return err
			}
		}
		i.ready = true
		return nil
	}
	_ = os.RemoveAll(dir) // leftovers of a session whose branch is gone
	_, _ = git(i.root, "worktree", "prune")
	// "git stash create" captures the uncommitted tracked changes as a commit
	// without touching the working tree or the stash list. That commit is a
	// stash-shaped merge ("WIP on …" with an "index on …" parent), which must
	// not reach the user's history, so its tree becomes one plain commit on
	// top of HEAD.
	base := "HEAD"
	if stash, _ := git(i.root, "stash", "create"); stash != "" {
		args := append(commitArgs(i.root), "commit-tree", stash+"^{tree}", "-p", "HEAD", "-m", "decsr: uncommitted changes at session start")
		c, err := git(i.root, args...)
		if err != nil {
			return err
		}
		base = c
	}
	if _, err := git(i.root, "worktree", "add", "-b", branch, i.integ, base); err != nil {
		return err
	}
	if n := i.untrackedCount(); n > 0 {
		i.warnings = append(i.warnings, fmt.Sprintf("%d untracked %s not copied into the worktrees; git add them if the nodes need them", n, pluralWord(n, "file is", "files are")))
	}
	i.ready = true
	return nil
}

func pluralWord(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// untrackedCount counts untracked paths under root, not counting .decsr.
func (i *Isolator) untrackedCount() int {
	out, err := git(i.root, "status", "--porcelain", "--untracked-files=normal", "--", ".")
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(l, "?? "); ok && !strings.HasPrefix(p, i.prefix+".decsr/") {
			n++
		}
	}
	return n
}

// ensureExclude adds .decsr/worktrees/ to the repository's info/exclude once.
func (i *Isolator) ensureExclude() error {
	p, err := git(i.root, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(i.root, p)
	}
	entry := i.prefix + ".decsr/worktrees/"
	data, _ := os.ReadFile(p) // #nosec G304 -- path reported by git for this repository
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) == entry {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- path reported by git for this repository
	if err != nil {
		return err
	}
	text := entry + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		text = "\n" + text
	}
	_, werr := f.WriteString(text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// IntegrationRoot is the project root inside the integration worktree, where
// the Build & test check runs. It is created when it does not exist yet.
func (i *Isolator) IntegrationRoot() (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ensureReady(); err != nil {
		return "", err
	}
	return filepath.Join(i.integ, i.prefix), nil
}

// Start creates the worktree of a node from the current integration tip and
// returns the project root inside it, plus warnings to show (once). A stale
// worktree or branch of the node from an earlier run is replaced.
func (i *Isolator) Start(id string) (string, []string, error) {
	if !nodeIDRe.MatchString(id) {
		return "", nil, fmt.Errorf("invalid node id %q", id)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.ensureReady(); err != nil {
		return "", nil, err
	}
	i.removeNode(id)
	path := i.nodePath(id)
	if _, err := git(i.root, "worktree", "add", "-b", nodeBranch(i.session, id), path, i.Branch()); err != nil {
		return "", nil, err
	}
	warns := i.warnings
	i.warnings = nil
	return filepath.Join(path, i.prefix), warns, nil
}

// removeNode deletes the worktree and branch of a node. The caller holds i.mu.
func (i *Isolator) removeNode(id string) {
	path := i.nodePath(id)
	_, _ = git(i.root, "worktree", "remove", "--force", path)
	_ = os.RemoveAll(path)
	_, _ = git(i.root, "worktree", "prune")
	_, _ = git(i.root, "branch", "-D", nodeBranch(i.session, id))
}

// Discard drops the worktree and branch of a node without merging.
func (i *Isolator) Discard(id string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.removeNode(id)
}

// Finish commits what the node changed and merges it into the integration
// branch. On a clean merge the node's worktree and branch are removed. On a
// conflict the merge is aborted, the branch and worktree stay, and the error
// names the conflicting files.
func (i *Isolator) Finish(id, title string) error {
	_, err := i.FinishStats(id, title)
	return err
}

// FinishStats is Finish that also returns the lines the node added and
// removed per file; it is empty when the node changed nothing.
func (i *Isolator) FinishStats(id, title string) ([]FileStat, error) {
	stats, _, err := i.FinishCommit(id, title)
	return stats, err
}

// FinishCommit is FinishStats that also returns the SHA of the node's commit,
// which stays reachable through the merge into the integration branch after
// the node branch is deleted. It is "" when the node changed nothing.
func (i *Isolator) FinishCommit(id, title string) ([]FileStat, string, error) {
	path := i.nodePath(id)
	branch := nodeBranch(i.session, id)
	if _, err := git(path, "add", "-A"); err != nil {
		return nil, "", err
	}
	staged, err := git(path, "diff", "--cached", "--name-only")
	if err != nil {
		return nil, "", err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if staged == "" {
		i.removeNode(id)
		return nil, "", nil
	}
	msg := id + ": " + strings.Join(strings.Fields(title), " ")
	if _, err := git(path, append(commitArgs(path), "commit", "--no-verify", "-m", msg)...); err != nil {
		return nil, "", err
	}
	stats := commitStats(path)
	commit, _ := git(path, "rev-parse", "HEAD")
	if _, err := git(i.integ, append(commitArgs(i.integ), "merge", "--no-ff", "--no-edit", branch)...); err != nil {
		files, _ := git(i.integ, "diff", "--name-only", "--diff-filter=U")
		_, _ = git(i.integ, "merge", "--abort")
		if files == "" {
			return nil, "", err
		}
		return nil, "", fmt.Errorf("merge conflict with earlier nodes in: %s — the branch %s is kept; run the node again to retry on top of the latest work",
			strings.Join(strings.Split(files, "\n"), ", "), branch)
	}
	i.removeNode(id)
	return stats, commit, nil
}

// commitStats is the numstat of the last commit in dir; binary files show
// "-" for both counts. A failure gives no stats.
func commitStats(dir string) []FileStat {
	out, err := git(dir, "diff", "--numstat", "--no-renames", "HEAD^", "HEAD")
	if err != nil || out == "" {
		return nil
	}
	created := map[string]bool{}
	if added, err := git(dir, "diff", "--name-only", "--diff-filter=A", "--no-renames", "HEAD^", "HEAD"); err == nil && added != "" {
		for _, p := range strings.Split(added, "\n") {
			created[p] = true
		}
	}
	var stats []FileStat
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, "\t", 3)
		if len(f) != 3 {
			continue
		}
		st := FileStat{Path: f[2], New: created[f[2]]}
		if f[0] == "-" && f[1] == "-" {
			st.Binary = true
		} else {
			st.Added, _ = strconv.Atoi(f[0])
			st.Removed, _ = strconv.Atoi(f[1])
		}
		stats = append(stats, st)
	}
	return stats
}

// RemoveSessionWorktrees removes every worktree of a session. Branches are
// kept.
func RemoveSessionWorktrees(root, session string) error {
	if !sessionIDRe.MatchString(session) {
		return fmt.Errorf("invalid session id %q", session)
	}
	dir := worktreesDir(root, session)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // nothing was ever created
	}
	for _, e := range entries {
		_, _ = git(root, "worktree", "remove", "--force", filepath.Join(dir, e.Name()))
	}
	err = os.RemoveAll(dir)
	_, _ = git(root, "worktree", "prune")
	return err
}

// hasMergedNode reports whether at least one node of the session was merged
// into its integration branch.
func hasMergedNode(root, session string) bool {
	branch := SessionBranch(session)
	if _, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err != nil {
		return false
	}
	n, err := git(root, "rev-list", "--merges", "--count", "--fixed-strings", "--grep=decsr/"+session+"-", branch)
	if err == nil && n != "0" {
		return true
	}
	// A node that changed nothing still leaves its tasks.md tick commit.
	n, err = git(root, "rev-list", "--count", "--grep=^decsr: mark .* done in ", branch)
	return err == nil && n != "0"
}

// MergeSession merges the integration branch of a session into the branch
// checked out in root and removes the session's worktrees. It returns a
// one-line summary.
func MergeSession(root, session string) (string, error) {
	if !sessionIDRe.MatchString(session) {
		return "", fmt.Errorf("invalid session id %q", session)
	}
	branch := SessionBranch(session)
	if _, err := git(root, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err != nil {
		return "", ErrNothingMerged
	}
	if !hasMergedNode(root, session) {
		return "", ErrNothingMerged
	}
	if dirty, err := git(root, "status", "--porcelain", "--untracked-files=no"); err != nil {
		return "", err
	} else if dirty != "" {
		return "", ErrDirtyTree
	}
	if _, err := git(root, append(commitArgs(root), "merge", "--no-ff", "--no-edit", branch)...); err != nil {
		files, _ := git(root, "diff", "--name-only", "--diff-filter=U")
		_, _ = git(root, "merge", "--abort")
		if files == "" {
			return "", err
		}
		return "", fmt.Errorf("merging %s conflicts with your branch in: %s (nothing was changed)", branch, strings.Join(strings.Split(files, "\n"), ", "))
	}
	_ = RemoveSessionWorktrees(root, session)
	cur, _ := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	return fmt.Sprintf("merged %s into %s", branch, cur), nil
}
