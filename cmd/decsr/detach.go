package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/multiplexer"
)

// Detached runs: the whole decsr TUI runs inside a tmux session named
// "decsr-<slug>-<hash>" so it survives a closed terminal or a dropped SSH
// connection. The project path is stored in the tmux user option
// @decsr_project, which `decsr ls` reads back.

var errTmuxWindows = errors.New("tmux mode is not supported on Windows")

// detachEnv is the environment variable set in the child so it never recurses.
const detachEnv = "DECSR_TMUX"

// detachDeps are the side effects of detached runs, replaced in tests.
type detachDeps struct {
	runner   multiplexer.Runner
	lookPath func(string) (string, error)
	getenv   func(string) string
	exe      func() (string, error)
	goos     string
	// exec replaces the current process (tmux takes over the terminal).
	exec func(path string, args []string) error
	out  io.Writer
	now  func() time.Time
}

func defaultDetachDeps() detachDeps {
	return detachDeps{
		runner:   multiplexer.ExecRunner{},
		lookPath: exec.LookPath,
		getenv:   os.Getenv,
		exe:      os.Executable,
		goos:     runtime.GOOS,
		exec:     execProcess,
		out:      os.Stdout,
		now:      time.Now,
	}
}

// stripTmuxFlag removes --tmux (also -tmux, --tmux=true/false) from args and
// reports whether it asked for tmux mode.
func stripTmuxFlag(args []string) ([]string, bool) {
	rest := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		switch a {
		case "--tmux", "-tmux", "--tmux=true", "-tmux=true":
			found = true
		case "--tmux=false", "-tmux=false":
		default:
			rest = append(rest, a)
		}
	}
	return rest, found
}

// hasIssueArg reports whether args start a run from an issue.
func hasIssueArg(args []string) bool {
	for _, a := range args {
		if a == "issue" || a == "--issue" || a == "-issue" || strings.HasPrefix(a, "--issue=") {
			return true
		}
	}
	return false
}

// insideTmux reports whether decsr should just run: already in tmux, or it is
// the child of a detached run.
func (d detachDeps) insideTmux() bool {
	return d.getenv("TMUX") != "" || d.getenv(detachEnv) == "1"
}

// wantsTmux decides whether this invocation runs in a detached session: an
// explicit --tmux, or plain `decsr` with ui.tmux set.
func wantsTmux(flagged bool, args []string, uiTmux func() bool) bool {
	if flagged {
		return true
	}
	return len(args) == 0 && uiTmux()
}

// configUITmux reads ui.tmux for the current project.
func configUITmux() bool {
	root, err := os.Getwd()
	if err != nil {
		return false
	}
	cfg, err := config.Load(root)
	return err == nil && cfg.UI.Tmux
}

// requireTmux checks the platform and that tmux is installed.
func (d detachDeps) requireTmux() (string, error) {
	if d.goos == "windows" {
		return "", errTmuxWindows
	}
	path, err := d.lookPath("tmux")
	if err != nil {
		return "", errors.New("tmux is not installed (needed for --tmux); run `decsr doctor` for install hints")
	}
	return path, nil
}

// launchDetached attaches to the project's decsr session, creating it first
// when it does not exist. args are the decsr arguments without --tmux.
func launchDetached(d detachDeps, projectDir string, args []string) error {
	tmuxPath, err := d.requireTmux()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return err
	}
	name := multiplexer.SessionName(abs)
	sessions, err := multiplexer.ListSessions(d.runner)
	if err != nil {
		return err
	}
	exists := false
	for _, s := range sessions {
		if s.Name == name {
			exists = true
		}
	}
	if exists {
		if hasIssueArg(args) {
			return fmt.Errorf("a decsr run is already in session %s; run `decsr attach` and use /issue there", name)
		}
		return d.attach(tmuxPath, name)
	}
	exe, err := d.exe()
	if err != nil {
		return err
	}
	if err := multiplexer.CreateDetached(d.runner, name, abs, append([]string{exe}, args...)); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}
	return d.attach(tmuxPath, name)
}

// attach hands the terminal to tmux. Inside tmux it switches client instead,
// since tmux refuses nested attaches.
func (d detachDeps) attach(tmuxPath, name string) error {
	if d.getenv("TMUX") != "" {
		return d.exec(tmuxPath, []string{"tmux", "switch-client", "-t", name})
	}
	return d.exec(tmuxPath, []string{"tmux", "attach-session", "-t", name})
}

// errAmbiguous carries the sessions to list when attach cannot choose.
type errAmbiguous struct{ sessions []multiplexer.Session }

func (e errAmbiguous) Error() string {
	var b strings.Builder
	b.WriteString("several decsr sessions are running; pass a name or project path:\n")
	for _, s := range e.sessions {
		fmt.Fprintf(&b, "  %s  %s\n", s.Name, s.Project)
	}
	return strings.TrimRight(b.String(), "\n")
}

// selectSession picks the session `decsr attach [arg]` means. cwd is the
// current directory.
func selectSession(sessions []multiplexer.Session, arg, cwd string) (multiplexer.Session, error) {
	find := func(name string) (multiplexer.Session, bool) {
		for _, s := range sessions {
			if s.Name == name {
				return s, true
			}
		}
		return multiplexer.Session{}, false
	}
	if arg == "" {
		if s, ok := find(multiplexer.SessionName(cwd)); ok {
			return s, nil
		}
		switch len(sessions) {
		case 0:
			return multiplexer.Session{}, errors.New("no decsr sessions are running; start one with `decsr --tmux`")
		case 1:
			return sessions[0], nil
		}
		return multiplexer.Session{}, errAmbiguous{sessions}
	}
	if s, ok := find(arg); ok {
		return s, nil
	}
	if s, ok := find(multiplexer.SessionPrefix + arg); ok {
		return s, nil
	}
	if abs, err := filepath.Abs(arg); err == nil {
		if s, ok := find(multiplexer.SessionName(abs)); ok {
			return s, nil
		}
		for _, s := range sessions {
			if s.Project != "" && filepath.Clean(s.Project) == abs {
				return s, nil
			}
		}
	}
	return multiplexer.Session{}, fmt.Errorf("no decsr session matches %q (see `decsr ls`)", arg)
}

// attachCommand implements `decsr attach [name|project-path]`.
func attachCommand(d detachDeps, args []string, cwd string) error {
	if len(args) > 1 {
		return errors.New("usage: decsr attach [name|project-path]")
	}
	tmuxPath, err := d.requireTmux()
	if err != nil {
		return err
	}
	sessions, err := multiplexer.ListSessions(d.runner)
	if err != nil {
		return err
	}
	arg := ""
	if len(args) == 1 {
		arg = args[0]
	}
	s, err := selectSession(sessions, arg, cwd)
	if err != nil {
		return err
	}
	return d.attach(tmuxPath, s.Name)
}

// lsCommand implements `decsr ls`.
func lsCommand(d detachDeps) error {
	if _, err := d.requireTmux(); err != nil {
		return err
	}
	sessions, err := multiplexer.ListSessions(d.runner)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(d.out, "no decsr sessions running")
		return nil
	}
	tw := tabwriter.NewWriter(d.out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tPROJECT\tSTATE\tCREATED")
	for _, s := range sessions {
		state, created, project := "detached", "-", s.Project
		if s.Attached {
			state = "attached"
		}
		if !s.Created.IsZero() {
			created = s.Created.Format("2006-01-02 15:04")
		}
		if project == "" {
			project = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, project, state, created)
	}
	return tw.Flush()
}

// tmuxEntry runs before normal dispatch. It returns the arguments to dispatch
// (--tmux removed) and whether it already handled the invocation.
func tmuxEntry(d detachDeps, args []string, uiTmux func() bool) ([]string, bool, error) {
	rest, flagged := stripTmuxFlag(args)
	if !wantsTmux(flagged, rest, uiTmux) || d.insideTmux() {
		return rest, false, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return rest, true, err
	}
	return rest, true, launchDetached(d, cwd, rest)
}

// detachMain handles `attach`, `ls` and --tmux. It reports whether it handled
// the invocation.
func detachMain() bool {
	d := defaultDetachDeps()
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "automation":
			return false // its own --tmux starts a detached session and never attaches
		case "attach", "ls":
			cwd, _ := os.Getwd()
			var err error
			if args[0] == "attach" {
				err = attachCommand(d, args[1:], cwd)
			} else {
				err = lsCommand(d)
			}
			exitOnDetachErr(err)
			return true
		}
	}
	rest, handled, err := tmuxEntry(d, args, configUITmux)
	exitOnDetachErr(err)
	if handled {
		return true
	}
	os.Args = append(os.Args[:1], rest...)
	return false
}

func exitOnDetachErr(err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
