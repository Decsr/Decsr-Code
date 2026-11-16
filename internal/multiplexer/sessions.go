package multiplexer

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- not security relevant: short collision-avoiding suffix only
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

const (
	// SessionPrefix starts the name of every tmux session decsr creates.
	SessionPrefix = "decsr-"
	// ProjectOption is the tmux user option (set on the session when decsr
	// creates it) that holds the absolute project path.
	ProjectOption = "@decsr_project"

	listFormat = "#{session_name}\t#{session_attached}\t#{session_created}\t#{" + ProjectOption + "}"
)

// Runner runs a command and returns its standard output. On failure the
// error carries the command's standard error. Tests inject a fake.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ExecRunner runs real commands.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(name string, args ...string) (string, error) {
	cmd := executil.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.String(), fmt.Errorf("%s: %s", name, msg)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return stdout.String(), fmt.Errorf("%s: %w", name, err)
		}
		return stdout.String(), err
	}
	return stdout.String(), nil
}

// Session is one running decsr tmux session.
type Session struct {
	Name     string
	Project  string // "" when the session carries no @decsr_project
	Attached bool
	Created  time.Time
}

// SessionName returns the tmux session name for a project directory:
// "decsr-<slug>-<4 hex of sha1(abs path)>". The slug is the lowercased base
// name with every character outside [a-z0-9-] replaced by "-" (tmux session
// names must not contain "." or ":"); the hash keeps projects with the same
// base name apart.
func SessionName(projectDir string) string {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		abs = projectDir
	}
	slug := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, strings.ToLower(filepath.Base(abs)))
	if len(slug) > 40 {
		slug = slug[:40]
	}
	if slug = strings.Trim(slug, "-"); slug == "" {
		slug = "project"
	}
	sum := sha1.Sum([]byte(abs)) // #nosec G401 -- not security relevant
	return SessionPrefix + slug + "-" + hex.EncodeToString(sum[:])[:4]
}

// ParseSessions parses `tmux list-sessions -F` output produced with
// listFormat. Malformed lines and sessions that are not decsr's are skipped.
func ParseSessions(out string) []Session {
	var res []Session
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		f := strings.SplitN(line, "\t", 4)
		if !strings.HasPrefix(f[0], SessionPrefix) {
			continue
		}
		s := Session{Name: f[0]}
		if len(f) > 1 {
			if n, err := strconv.Atoi(strings.TrimSpace(f[1])); err == nil {
				s.Attached = n > 0
			}
		}
		if len(f) > 2 {
			if sec, err := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64); err == nil && sec > 0 {
				s.Created = time.Unix(sec, 0)
			}
		}
		if len(f) > 3 {
			s.Project = strings.TrimSpace(f[3])
		}
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Name < res[j].Name })
	return res
}

// ListSessions returns the running decsr-* tmux sessions. A missing tmux
// server means there are none.
func ListSessions(r Runner) ([]Session, error) {
	out, err := r.Run("tmux", "list-sessions", "-F", listFormat)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to") || strings.Contains(msg, "No such file or directory") {
			return nil, nil
		}
		return nil, err
	}
	return ParseSessions(out), nil
}

// ShellQuote single-quotes s for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CreateDetached starts a detached session running argv in dir with
// DECSR_TMUX=1 in its environment, and records dir in @decsr_project.
func CreateDetached(r Runner, name, dir string, argv []string) error {
	parts := []string{"DECSR_TMUX=1"}
	for _, a := range argv {
		parts = append(parts, ShellQuote(a))
	}
	if _, err := r.Run("tmux", "new-session", "-d", "-s", name, "-c", dir, strings.Join(parts, " ")); err != nil {
		return err
	}
	// Best effort: the command may already have exited.
	_, _ = r.Run("tmux", "set-option", "-t", name, ProjectOption, dir)
	return nil
}
