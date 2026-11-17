package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/automation"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/logging"
	"github.com/Decsr/Decsr-Code/internal/multiplexer"
	"github.com/Decsr/Decsr-Code/internal/notify"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/tui"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const automationUsage = `usage:
  decsr automation list
  decsr automation run <name> [--tmux]
  decsr automation install [name] [--yes]
  decsr automation uninstall [name]`

// autoSessionPrefix starts the tmux session name of an automation run.
const autoSessionPrefix = "decsr-auto-"

// autoDeps are the side effects of the automation commands, replaced in tests.
type autoDeps struct {
	root    string
	out     io.Writer
	now     func() time.Time
	goos    string
	crontab automation.Crontab
	exe     func() (string, error)
	path    string
	tmux    detachDeps
	ghRun   workspace.CmdRunner
	pid     int
	alive   func(pid int) bool
	isTTY   func() bool
	in      io.Reader
	// authMode is the claude login mode (runner.AuthAPIKey or AuthSubscription).
	authMode func() string
	// claudeRoles lists the roles of the project that run on the claude runner.
	claudeRoles func(root string) []string
	// runWorkspace opens the Workspace for the run and returns how it ended.
	runWorkspace func(root string, opts tui.WorkspaceOptions) (tui.AutomationResult, error)
	// notify sends a note through the configured channels.
	notify func(root string, level notify.Level, body string)
}

func defaultAutoDeps(root string) autoDeps {
	return autoDeps{
		root:         root,
		out:          os.Stdout,
		now:          time.Now,
		goos:         runtime.GOOS,
		crontab:      automation.ExecCrontab{},
		exe:          os.Executable,
		path:         os.Getenv("PATH"),
		tmux:         defaultDetachDeps(),
		ghRun:        workspace.ExecRunner,
		pid:          os.Getpid(),
		alive:        automation.ProcessAlive,
		isTTY:        stdinIsTerminal,
		in:           os.Stdin,
		authMode:     runner.ClaudeAuthMode,
		claudeRoles:  projectClaudeRoles,
		runWorkspace: runAutomationWorkspace,
		notify:       sendAutomationNote,
	}
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (d autoDeps) locker(timeout time.Duration) automation.Locker {
	l := automation.NewLocker(d.root, timeout)
	l.PID, l.Now, l.Alive = d.pid, d.now, d.alive
	return l
}

// automationCmd implements `decsr automation <list|run|install|uninstall>`.
func automationCmd(args []string) {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if err := automationMain(defaultAutoDeps(root), args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func automationMain(d autoDeps, args []string) error {
	if len(args) == 0 {
		return errors.New(automationUsage)
	}
	switch args[0] {
	case "list":
		return automationList(d)
	case "run":
		name, useTmux, err := parseAutomationRun(args[1:])
		if err != nil {
			return err
		}
		return automationRun(d, name, useTmux)
	case "install", "uninstall":
		rest, yes := args[1:], false
		if args[0] == "install" {
			rest, yes = stripYesFlag(rest)
		}
		if len(rest) > 1 {
			return errors.New(automationUsage)
		}
		name := ""
		if len(rest) == 1 {
			name = rest[0]
		}
		if args[0] == "install" {
			return automationInstall(d, name, yes)
		}
		return automationUninstall(d, name)
	}
	return errors.New(automationUsage)
}

// stripYesFlag removes --yes / -y and reports whether it was there.
func stripYesFlag(args []string) ([]string, bool) {
	var rest []string
	yes := false
	for _, a := range args {
		if a == "--yes" || a == "-y" {
			yes = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, yes
}

func parseAutomationRun(args []string) (name string, useTmux bool, err error) {
	rest, useTmux := stripTmuxFlag(args)
	if len(rest) != 1 || strings.HasPrefix(rest[0], "-") {
		return "", false, errors.New("usage: decsr automation run <name> [--tmux]")
	}
	return rest[0], useTmux, nil
}

func (d autoDeps) abs() (string, error) { return filepath.Abs(d.root) }

// ── list ─────────────────────────────────────────────────────────────────────

func automationList(d autoDeps) error {
	list, err := automation.Load(d.root)
	if errors.Is(err, automation.ErrNoConfig) {
		_, _ = fmt.Fprintln(d.out, "no automations: create .decsr/automations.yaml (see doc/automations.md)")
		return nil
	}
	if err != nil {
		return err
	}
	project, err := d.abs()
	if err != nil {
		return err
	}
	installed, ierr := automation.Installed(d.crontab, project)
	tw := tabwriter.NewWriter(d.out, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tSCHEDULE\tNEXT RUN\tWORK\tAUTONOMY\tBUDGET\tINSTALLED\tLAST RESULT")
	for _, a := range list {
		next := "never"
		if t := a.Cron().Next(d.now()); !t.IsZero() {
			next = t.Format("2006-01-02 15:04")
		}
		inst := "no"
		switch {
		case ierr != nil:
			inst = "?"
		case installed[a.Name]:
			inst = "yes"
		}
		last := "-"
		if e, ok := automation.LastEntry(d.root, a.Name); ok {
			last = fmt.Sprintf("%s: %s", e.End.Format("2006-01-02 15:04"), e.Result)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\tL%d\t$%.2f\t%s\t%s\n",
			a.Name, a.Schedule, next, clip(a.Source(), 48), a.Autonomy, a.BudgetUSD, inst, last)
	}
	return tw.Flush()
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ── install / uninstall ──────────────────────────────────────────────────────

// subscriptionTermsWarning is shown before an automation that runs the claude
// runner is installed on a subscription login.
const subscriptionTermsWarning = `warning: these automations run the claude runner unattended on a Claude subscription login:
  %s
Anthropic's Consumer Terms (section 3) do not allow access to the Services through
automated or non-human means such as a bot or a script, except with an Anthropic
API key: https://www.anthropic.com/legal/consumer-terms
Advertised Pro, Max and Ultra usage limits assume ordinary, individual usage:
https://code.claude.com/docs/en/legal-and-compliance
Set ANTHROPIC_API_KEY for the cron environment to use the API instead.`

// subscriptionRisk reports whether a runs claude on a subscription login
// without having accepted it with allow_subscription.
func (d autoDeps) subscriptionRisk(a automation.Automation) bool {
	if a.AllowSubscription || d.authMode() != runner.AuthSubscription {
		return false
	}
	return len(d.claudeRoles(d.root)) > 0
}

// confirmSubscription warns about the automations in list that run claude on a
// subscription login and asks for a yes. yes skips the question; without a
// terminal and without yes it refuses.
func confirmSubscription(d autoDeps, list []automation.Automation, yes bool) error {
	var names []string
	for _, a := range list {
		if d.subscriptionRisk(a) {
			names = append(names, a.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	_, _ = fmt.Fprintf(d.out, subscriptionTermsWarning+"\n", strings.Join(names, ", "))
	if yes {
		return nil
	}
	if !d.isTTY() {
		return errors.New("refusing to install without a terminal to confirm; use --yes to accept this, or set ANTHROPIC_API_KEY")
	}
	_, _ = fmt.Fprint(d.out, "Install anyway? [y/N] ")
	line, _ := bufio.NewReader(d.in).ReadString('\n')
	if ans := strings.ToLower(strings.TrimSpace(line)); ans != "y" && ans != "yes" {
		return errors.New("not installed")
	}
	return nil
}

// projectClaudeRoles are the roles of the project that run on the claude runner.
func projectClaudeRoles(root string) []string {
	cfg, err := config.Load(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range workspace.Roles(cfg) {
		if r.Agent.Runner == "claude" {
			out = append(out, r.Name)
		}
	}
	return out
}

func automationInstall(d autoDeps, name string, yes bool) error {
	if d.goos == "windows" {
		return errors.New("automation install needs cron and is not supported on Windows")
	}
	list, err := automation.Load(d.root)
	if err != nil {
		return err
	}
	if name != "" {
		a, ok := automation.Find(list, name)
		if !ok {
			return fmt.Errorf("no automation named %q in .decsr/automations.yaml", name)
		}
		list = []automation.Automation{a}
	}
	if err := confirmSubscription(d, list, yes); err != nil {
		return err
	}
	project, err := d.abs()
	if err != nil {
		return err
	}
	exe, err := d.exe()
	if err != nil {
		return err
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return err
	}
	lines := map[string]string{}
	var order []string
	for _, a := range list {
		l, err := automation.Line(a, project, exe, d.path)
		if err != nil {
			return fmt.Errorf("%s: %w", a.Name, err)
		}
		lines[a.Name] = l
		order = append(order, a.Name)
	}
	written, err := automation.Install(d.crontab, project, lines, order)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(d.out, "crontab has %d decsr line(s):\n", len(written))
	for _, l := range written {
		_, _ = fmt.Fprintln(d.out, l)
	}
	return nil
}

func automationUninstall(d autoDeps, name string) error {
	if d.goos == "windows" {
		return errors.New("automation uninstall needs cron and is not supported on Windows")
	}
	project, err := d.abs()
	if err != nil {
		return err
	}
	var names []string
	if name != "" {
		names = []string{name}
	}
	removed, err := automation.Uninstall(d.crontab, project, names)
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		_, _ = fmt.Fprintln(d.out, "no decsr crontab lines to remove")
		return nil
	}
	_, _ = fmt.Fprintf(d.out, "removed %d crontab line(s):\n", len(removed))
	for _, l := range removed {
		_, _ = fmt.Fprintln(d.out, l)
	}
	return nil
}

// ── run ──────────────────────────────────────────────────────────────────────

// autoSessionName is the detached tmux session of an automation run.
func autoSessionName(project, name string) string {
	return autoSessionPrefix + strings.TrimPrefix(multiplexer.SessionName(project), multiplexer.SessionPrefix) + "-" + name
}

func (d autoDeps) logRun(name string, start time.Time, result string, cost float64) {
	e := automation.Entry{Start: start, End: d.now(), CostUSD: cost, Result: result}
	if err := automation.AppendLog(d.root, name, e); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "could not write the run log: %v\n", err)
	}
	_, _ = fmt.Fprintf(d.out, "automation %s: %s\n", name, result)
}

func automationRun(d autoDeps, name string, useTmux bool) error {
	list, err := automation.Load(d.root)
	if err != nil {
		return err
	}
	a, ok := automation.Find(list, name)
	if !ok {
		return fmt.Errorf("no automation named %q in .decsr/automations.yaml", name)
	}
	// Load validates, so L2/L3 and a budget are known to be set here.
	start := d.now()
	if d.subscriptionRisk(a) {
		msg := "claude runs on a subscription login, which does not allow unattended use: set ANTHROPIC_API_KEY for the cron environment, or set allow_subscription: true in .decsr/automations.yaml to accept the risk"
		d.logRun(name, start, "refused: "+msg, 0)
		d.notify(d.root, notify.Failed, fmt.Sprintf("automation %s refused: %s", name, msg))
		return nil
	}
	locker := d.locker(a.Timeout)
	if locker.Active(name) {
		d.logRun(name, start, "skipped: previous run still active", 0)
		return nil
	}
	if useTmux {
		return launchAutomationTmux(d, a, start)
	}
	if !d.isTTY() {
		return errors.New("automation run needs a terminal; use --tmux to run it in a detached tmux session")
	}
	release, got, err := locker.Acquire(name)
	if err != nil {
		return err
	}
	if !got {
		d.logRun(name, start, "skipped: previous run still active", 0)
		return nil
	}
	defer release()

	opts := tui.WorkspaceOptions{Automation: tui.AutomationOptions{
		Name: a.Name, Timeout: a.Timeout, Autonomy: a.Autonomy, BudgetUSD: a.BudgetUSD, AllowSubscription: a.AllowSubscription,
	}}
	if a.IssueQuery != "" {
		num, err := oldestIssue(d, a.IssueQuery)
		if err != nil {
			d.logRun(name, start, "failed: "+oneLineErr(err), 0)
			d.notify(d.root, notify.Failed, fmt.Sprintf("automation %s failed: %s", name, oneLineErr(err)))
			return nil
		}
		if num == 0 {
			d.logRun(name, start, "nothing to do", 0)
			d.notify(d.root, notify.Info, fmt.Sprintf("automation %s: nothing to do", name))
			return nil
		}
		opts.Issue = fmt.Sprint(num)
	} else {
		opts.Automation.Task = a.Task
	}
	res, err := d.runWorkspace(d.root, opts)
	if err != nil {
		d.logRun(name, start, "failed: "+oneLineErr(err), res.CostUSD)
		return nil
	}
	if !res.Finished {
		res.Result = "interrupted"
	}
	d.logRun(name, start, res.Result, res.CostUSD)
	return nil
}

func oneLineErr(err error) string { return strings.Join(strings.Fields(err.Error()), " ") }

// launchAutomationTmux starts the run in a detached session. It never
// attaches: cron has no terminal.
func launchAutomationTmux(d autoDeps, a automation.Automation, start time.Time) error {
	if _, err := d.tmux.requireTmux(); err != nil {
		return err
	}
	project, err := d.abs()
	if err != nil {
		return err
	}
	session := autoSessionName(project, a.Name)
	sessions, err := multiplexer.ListSessions(d.tmux.runner)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.Name == session {
			d.logRun(a.Name, start, "skipped: previous run still active", 0)
			return nil
		}
	}
	exe, err := d.exe()
	if err != nil {
		return err
	}
	// env keeps the PATH of the caller (cron sets a short one itself); the
	// session itself gets DECSR_TMUX=1 from CreateDetached.
	argv := []string{"env", "PATH=" + d.path, exe, "automation", "run", a.Name}
	if err := multiplexer.CreateDetached(d.tmux.runner, session, project, argv); err != nil {
		return fmt.Errorf("create tmux session: %w", err)
	}
	_, _ = fmt.Fprintf(d.out, "automation %s started in tmux session %s (decsr attach %s)\n", a.Name, session, session)
	return nil
}

// oldestIssue is the number of the oldest open issue that matches query, 0
// when none does.
func oldestIssue(d autoDeps, query string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := d.ghRun(ctx, d.root, "gh", "issue", "list", "--state", "open",
		"--search", query+" sort:created-asc", "--json", "number", "--limit", "1")
	if err != nil {
		return 0, fmt.Errorf("gh issue list: %w", err)
	}
	var found []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal([]byte(out), &found); err != nil {
		return 0, fmt.Errorf("gh issue list: unreadable answer: %w", err)
	}
	if len(found) == 0 {
		return 0, nil
	}
	return found[0].Number, nil
}

func runAutomationWorkspace(root string, opts tui.WorkspaceOptions) (tui.AutomationResult, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return tui.AutomationResult{}, fmt.Errorf("load config: %w", err)
	}
	tui.ApplyTheme(cfg)
	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		return tui.AutomationResult{}, err
	}
	if err := logging.SetupFile(ws.Dir, logging.LogFileName); err == nil {
		defer logging.Close()
	}
	res, err := tui.RunWorkspace(root, cfg, opts)
	return res.Automation, err
}

func sendAutomationNote(root string, level notify.Level, body string) {
	cfg, err := config.Load(root)
	if err != nil {
		return
	}
	notify.Send(cfg.Notifications, notify.Note{Title: "decsr · " + filepath.Base(root), Body: body, Level: level})
	notify.Wait(2 * time.Second)
}

// ── doctor ───────────────────────────────────────────────────────────────────

// automationDoctor is the "automations" line of `decsr doctor`; nil when the
// project has no automations.yaml.
func automationDoctor(root string, ct automation.Crontab) *doctor.Result {
	const name = "automations"
	list, err := automation.Load(root)
	if errors.Is(err, automation.ErrNoConfig) {
		return nil
	}
	if err != nil {
		return &doctor.Result{Name: name, Status: doctor.Warn, Detail: oneLineErr(err), Hint: "fix .decsr/automations.yaml"}
	}
	project, perr := filepath.Abs(root)
	if perr != nil {
		return nil
	}
	lines, err := automation.InstalledLines(ct, project)
	if err != nil {
		return &doctor.Result{Name: name, Status: doctor.OK, Detail: fmt.Sprintf("%d defined, crontab not readable (%s)", len(list), clip(err.Error(), 60))}
	}
	var missing []string
	for n, l := range lines {
		if bin, ok := automation.LineBinary(l); ok {
			if _, err := os.Stat(bin); err != nil {
				missing = append(missing, n)
			}
		}
	}
	detail := fmt.Sprintf("%d defined, %d installed in crontab", len(list), len(lines))
	if len(missing) > 0 {
		return &doctor.Result{Name: name, Status: doctor.Warn, Detail: detail + "; binary missing for " + strings.Join(missing, ", "),
			Hint: "run `decsr automation install` again with the current decsr binary"}
	}
	return &doctor.Result{Name: name, Status: doctor.OK, Detail: detail}
}

// claudeAuthDoctor is the "claude auth" line of `decsr doctor`; nil when no
// role runs on the claude runner. It warns when an installed automation runs
// claude unattended on a subscription login.
func claudeAuthDoctor(root string, ct automation.Crontab, mode func() string, roles []string) *doctor.Result {
	const name = "claude auth"
	if len(roles) == 0 {
		return nil
	}
	m := mode()
	ok := &doctor.Result{Name: name, Status: doctor.OK, Detail: m}
	if m != runner.AuthSubscription {
		return ok
	}
	list, err := automation.Load(root)
	if err != nil {
		return ok
	}
	project, err := filepath.Abs(root)
	if err != nil {
		return ok
	}
	lines, err := automation.InstalledLines(ct, project)
	if err != nil {
		return ok
	}
	var risky []string
	for _, a := range list {
		if _, in := lines[a.Name]; in && !a.AllowSubscription {
			risky = append(risky, a.Name)
		}
	}
	if len(risky) == 0 {
		return ok
	}
	sort.Strings(risky)
	return &doctor.Result{Name: name, Status: doctor.Warn,
		Detail: m + "; installed automations use claude unattended: " + strings.Join(risky, ", "),
		Hint:   "set ANTHROPIC_API_KEY for the cron environment (subscription logins are for ordinary, individual use: https://code.claude.com/docs/en/legal-and-compliance)"}
}
