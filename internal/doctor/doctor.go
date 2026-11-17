// Package doctor checks that the machine and the effective configuration are
// ready to run decsr. All environment access goes through Deps so the checks
// can be tested without real binaries or network.
package doctor

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/Decsr/Decsr-Code/internal/notify"
)

// Status is the outcome of one check.
type Status int

const (
	OK Status = iota
	Warn
	Fail
)

// Result is one reported line.
type Result struct {
	Name     string
	Status   Status
	Detail   string
	Hint     string // fix hint, shown for Warn and Fail
	Required bool   // a Fail of a required check makes the run fail
}

// Agent is the part of an agent's config that the checks need.
type Agent struct {
	Name      string
	Runner    string // "" means the default runner
	BaseURL   string
	APIKeyEnv string
	Provider  string // saved custom provider (runner openai)
	ModeErr   string // why the agent's mode cannot run, "" when it can
}

// Layer is one config source and whether it was found.
type Layer struct {
	Name  string
	Path  string
	Found bool
}

// Deps are the injected lookups.
type Deps struct {
	LookPath  func(name string) (string, error)
	Getenv    func(key string) string
	Run       func(name string, args ...string) (string, error) // combined output, short timeout
	Reachable func(provider string) error
	Writable  func(dir string) error
	Root      string // project root (current directory)
	Home      string // user home directory, "" if unknown
	Agents    []Agent
	Layers    []Layer
	ConfigErr error // error from loading the effective config
	Notify    NotifyInfo
	GOOS      string // "" means the running OS

	// KeySource says where an API key comes from ("env NAME", "saved (...)" or
	// ""); nil checks only the environment. CredsErr reports a credentials file
	// with loose permissions. PresetKeyEnv gives a preset's key variable. None
	// of them ever returns the key itself.
	KeySource    func(credName, envName string) string
	CredsErr     func() error
	PresetKeyEnv func(name string) string

	// Automations is the optional "automations" line; nil when the project has
	// no .decsr/automations.yaml.
	Automations *Result

	// Theme is the ui.theme setting; "" is auto.
	Theme string
}

// NotifyInfo is the notification config the "notify" check reports on.
type NotifyInfo struct {
	DisableCMux, DisableBell, DisableDesktop bool
	Webhook, WebhookFormat                   string
}

// DefaultRunner is what an agent with no runner uses.
const DefaultRunner = "opencode"

// deepSeekKeyEnv is the preset's key variable.
const deepSeekKeyEnv = "DEEPSEEK_API_KEY"

// Run executes every check and returns the results in a stable order.
func Run(d Deps) []Result {
	var rs []Result
	rs = append(rs, checkGit(d)...)
	rs = append(rs, checkWritable(d))
	rs = append(rs, checkConfig(d))
	rs = append(rs, checkRunners(d)...)
	rs = append(rs, checkModes(d)...)
	rs = append(rs, checkOptional(d)...)
	if d.Automations != nil {
		rs = append(rs, *d.Automations)
	}
	if r, ok := checkTheme(d); ok {
		rs = append(rs, r)
	}
	return rs
}

// BackendUsable reports whether at least one runner is usable: the required
// "backend" check of Run, without the other checks.
func BackendUsable(d Deps) bool {
	for _, r := range checkRunners(d) {
		if r.Name == "backend" {
			return r.Status == OK
		}
	}
	return false
}

// Failed reports whether any required check failed.
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Required && r.Status == Fail {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// parallel runs fns concurrently and returns their results in order.
func parallel[T any](fns ...func() T) []T {
	out := make([]T, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = fn()
		}()
	}
	wg.Wait()
	return out
}

func checkGit(d Deps) []Result {
	if _, err := d.LookPath("git"); err != nil {
		return []Result{
			{Name: "git", Status: Fail, Required: true, Detail: "not found on PATH", Hint: "install git (https://git-scm.com/downloads)"},
			{Name: "git repo", Status: Warn, Detail: "cannot check without git", Hint: "install git first"},
		}
	}
	rs := parallel(
		func() Result {
			out, err := d.Run("git", "--version")
			if err != nil {
				return Result{Name: "git", Status: Fail, Required: true, Detail: "found but failed to run", Hint: "reinstall git"}
			}
			return Result{Name: "git", Status: OK, Required: true, Detail: firstLine(out)}
		},
		func() Result {
			out, err := d.Run("git", "-C", d.Root, "rev-parse", "--is-inside-work-tree")
			if err != nil || strings.TrimSpace(out) != "true" {
				return Result{Name: "git repo", Status: Warn, Detail: "not inside a git repository (workspace falls back to shared mode)", Hint: "run `git init` in the project directory"}
			}
			return Result{Name: "git repo", Status: OK, Detail: d.Root}
		},
	)
	return rs
}

func checkWritable(d Deps) Result {
	const name = ".decsr writable"
	projectDir := d.Root + "/.decsr"
	perr := d.Writable(projectDir)
	if perr == nil {
		return Result{Name: name, Status: OK, Required: true, Detail: projectDir}
	}
	if d.Home != "" {
		homeDir := d.Home + "/.decsr"
		if herr := d.Writable(homeDir); herr == nil {
			return Result{Name: name, Status: Warn, Required: true, Detail: fmt.Sprintf("%s not writable (%v); using %s", projectDir, perr, homeDir), Hint: "make the project directory writable"}
		}
	}
	return Result{Name: name, Status: Fail, Required: true, Detail: fmt.Sprintf("%s: %v", projectDir, perr), Hint: "fix permissions on the project directory or ~/.decsr"}
}

func checkConfig(d Deps) Result {
	var found []string
	for _, l := range d.Layers {
		if l.Found {
			found = append(found, fmt.Sprintf("%s (%s)", l.Name, l.Path))
		}
	}
	where := "built-in defaults only"
	if len(found) > 0 {
		where = "defaults + " + strings.Join(found, " + ")
	}
	if d.ConfigErr != nil {
		return Result{Name: "config", Status: Fail, Required: true, Detail: d.ConfigErr.Error(), Hint: "fix the YAML syntax in the config file named above"}
	}
	return Result{Name: "config", Status: OK, Required: true, Detail: where}
}

// runnerGroup is one distinct runner with the agents using it.
type runnerGroup struct {
	name   string
	agents []Agent
}

func groupRunners(agents []Agent) []runnerGroup {
	by := map[string]*runnerGroup{DefaultRunner: {name: DefaultRunner}}
	for _, a := range agents {
		n := a.Runner
		if n == "" {
			n = DefaultRunner
		}
		g, ok := by[n]
		if !ok {
			g = &runnerGroup{name: n}
			by[n] = g
		}
		g.agents = append(g.agents, a)
	}
	groups := make([]runnerGroup, 0, len(by))
	for _, g := range by {
		sort.Slice(g.agents, func(i, j int) bool { return g.agents[i].Name < g.agents[j].Name })
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	return groups
}

func agentNames(as []Agent) string {
	if len(as) == 0 {
		return "default runner"
	}
	n := make([]string, len(as))
	for i, a := range as {
		n[i] = a.Name
	}
	return strings.Join(n, ", ")
}

func checkRunners(d Deps) []Result {
	groups := groupRunners(d.Agents)
	fns := make([]func() []Result, len(groups))
	for i, g := range groups {
		fns[i] = func() []Result { return checkRunner(d, g) }
	}
	var rs []Result
	anyOK := false
	for _, part := range parallel(fns...) {
		for _, r := range part {
			if r.Status == OK && strings.HasPrefix(r.Name, "runner ") {
				anyOK = true
			}
		}
		rs = append(rs, part...)
	}
	if d.CredsErr != nil {
		for _, g := range groups {
			if !isAPIRunner(g.name) {
				continue
			}
			if err := d.CredsErr(); err != nil {
				rs = append(rs, Result{Name: "credentials file", Status: Warn, Detail: err.Error(), Hint: "chmod 600 ~/.decsr/credentials.json"})
			}
			break
		}
	}
	if anyOK {
		rs = append(rs, Result{Name: "backend", Status: OK, Required: true, Detail: "at least one runner is usable"})
	} else {
		rs = append(rs, Result{Name: "backend", Status: Fail, Required: true, Detail: "no usable runner", Hint: "install a runner (opencode, claude, codex, copilot, ollama, ...) or configure an API runner"})
	}
	return rs
}

func checkRunner(d Deps, g runnerGroup) []Result {
	name := "runner " + g.name
	who := agentNames(g.agents)
	switch g.name {
	case "openai", "deepseek", "openrouter", "groq":
		return checkAPIRunner(d, g, name, who)
	}
	if g.name == "dsh" {
		// dsh runs on the DeepSeek key decsr hands over. A key kept by dsh's
		// own credentials service is not visible here.
		if d.keySource("deepseek", deepSeekKeyEnv) == "" {
			return []Result{{Name: name + " key", Status: Fail, Detail: deepSeekKeyEnv + " is not set and no saved key (agents: " + who + ")", Hint: "run `decsr connect deepseek` or export " + deepSeekKeyEnv + "=<your key>"}}
		}
	}
	if err := d.Reachable(g.name); err != nil {
		return []Result{{Name: name, Status: Warn, Detail: fmt.Sprintf("%v (agents: %s)", err, who), Hint: runnerHint(g.name)}}
	}
	return []Result{{Name: name, Status: OK, Detail: "reachable (agents: " + who + ")"}}
}

func runnerHint(runner string) string {
	switch runner {
	case "ollama":
		return "start it with `ollama serve`"
	case "mlx":
		return "start the oMLX server"
	case "lmstudio":
		return "start the LM Studio local server"
	default:
		return "install `" + runner + "` and make sure it is on PATH, or pick another runner with /config"
	}
}

// isAPIRunner reports whether name is a hosted API runner.
func isAPIRunner(name string) bool {
	switch name {
	case "openai", "deepseek", "openrouter", "groq":
		return true
	}
	return false
}

func (d Deps) presetEnv(name string) string {
	if d.PresetKeyEnv != nil {
		return d.PresetKeyEnv(name)
	}
	if name == "deepseek" {
		return deepSeekKeyEnv
	}
	return ""
}

func (d Deps) keySource(credName, env string) string {
	if d.KeySource != nil {
		return d.KeySource(credName, env)
	}
	if env != "" && d.Getenv(env) != "" {
		return "env " + env
	}
	return ""
}

func checkAPIRunner(d Deps, g runnerGroup, name, who string) []Result {
	// Distinct key sources, in order of first use; the key value is never read out.
	type keyID struct{ cred, env string }
	groups := map[keyID][]Agent{}
	var order []keyID
	for _, a := range g.agents {
		cred := a.Provider
		if cred == "" {
			cred = g.name
		}
		env := a.APIKeyEnv
		if env == "" {
			env = d.presetEnv(cred)
		}
		id := keyID{cred, env}
		if _, ok := groups[id]; !ok {
			order = append(order, id)
		}
		groups[id] = append(groups[id], a)
	}
	var rs []Result
	keysOK := true
	for _, id := range order {
		as := groups[id]
		if src := d.keySource(id.cred, id.env); src != "" {
			rs = append(rs, Result{Name: name + " key", Status: OK, Detail: "key: " + src + " (agents: " + agentNames(as) + ")"})
			continue
		}
		keysOK = false
		connect := id.cred
		if !isAPIRunner(id.cred) {
			connect = "custom"
		}
		hint := "run `decsr connect " + connect + "`"
		detail := "no API key (agents: " + agentNames(as) + ")"
		if id.env != "" {
			hint += " or export " + id.env + "=<your key>"
			detail = id.env + " is not set and no saved key (agents: " + agentNames(as) + ")"
		} else {
			hint += " or set `api_key_env` for these agents"
		}
		rs = append(rs, Result{Name: name + " key", Status: Fail, Detail: detail, Hint: hint})
	}
	if g.name != "openai" && keysOK {
		if err := d.Reachable(g.name); err != nil {
			rs = append(rs, Result{Name: name, Status: Warn, Detail: err.Error(), Hint: "check your network and that the key is valid"})
		} else {
			rs = append(rs, Result{Name: name, Status: OK, Detail: "reachable (agents: " + who + ")"})
		}
	} else if g.name == "openai" && keysOK {
		rs = append(rs, Result{Name: name, Status: OK, Detail: "configured (agents: " + who + ")"})
	}
	return rs
}

func checkOptional(d Deps) []Result {
	return parallel(
		func() Result {
			if _, err := d.LookPath("tmux"); err != nil {
				return Result{Name: "tmux", Status: Warn, Detail: "not found on PATH (used by the multiplexer and `decsr --tmux`)", Hint: "install tmux, e.g. `brew install tmux` or `apt install tmux`; then `decsr --tmux` keeps runs alive when the terminal closes"}
			}
			out, _ := d.Run("tmux", "-V")
			return Result{Name: "tmux", Status: OK, Detail: firstLine(out)}
		},
		func() Result {
			if _, err := d.LookPath("gh"); err != nil {
				return Result{Name: "gh", Status: Warn, Detail: "not found on PATH (needed for upcoming PR features)", Hint: "install the GitHub CLI from https://cli.github.com"}
			}
			return Result{Name: "gh", Status: OK, Detail: "installed"}
		},
		func() Result {
			if _, err := d.LookPath("gh"); err != nil {
				return Result{Name: "gh auth", Status: Warn, Detail: "skipped, gh is not installed", Hint: "install gh, then run `gh auth login`"}
			}
			if _, err := d.Run("gh", "auth", "status"); err != nil {
				return Result{Name: "gh auth", Status: Warn, Detail: "not logged in", Hint: "run `gh auth login`"}
			}
			return Result{Name: "gh auth", Status: OK, Detail: "logged in"}
		},
		func() Result { return checkTerm(d) },
		func() Result { return checkNotify(d) },
	)
}

// checkNotify lists the active notification channels. The webhook is shown by
// host only, its path may hold a secret token.
func checkNotify(d Deps) Result {
	const name = "notify"
	goos := d.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	n := d.Notify
	var on []string
	var problems, hints []string
	if !n.DisableCMux {
		on = append(on, "cmux")
	}
	if !n.DisableBell && goos != "windows" {
		on = append(on, "bell")
	}
	if !n.DisableDesktop {
		switch goos {
		case "linux":
			_, err := d.LookPath("notify-send")
			display := d.Getenv("DISPLAY") != "" || d.Getenv("WAYLAND_DISPLAY") != ""
			switch {
			case err != nil:
				problems = append(problems, "desktop enabled but notify-send is not on PATH")
				hints = append(hints, "install libnotify (`apt install libnotify-bin`) or set `disable_desktop: true`")
			case !display:
				problems = append(problems, "desktop enabled but DISPLAY and WAYLAND_DISPLAY are unset")
				hints = append(hints, "run decsr in a graphical session or set `disable_desktop: true`")
			default:
				on = append(on, "desktop")
			}
		case "darwin":
			on = append(on, "desktop")
		}
	}
	if n.Webhook != "" {
		if host := notify.Host(n.Webhook); host == "invalid URL" {
			problems = append(problems, "webhook is not a valid http(s) URL")
			hints = append(hints, "set `notifications.webhook` to e.g. https://ntfy.sh/<your-topic>")
		} else {
			f := n.WebhookFormat
			if f == "" {
				f = notify.FormatNtfy
			}
			on = append(on, "webhook "+host+" ("+f+")")
		}
	}
	detail := "no channel active"
	if len(on) > 0 {
		detail = strings.Join(on, ", ")
	}
	if len(problems) > 0 {
		return Result{Name: name, Status: Warn, Detail: detail + "; " + strings.Join(problems, "; "), Hint: strings.Join(hints, "; ")}
	}
	return Result{Name: name, Status: OK, Detail: detail}
}

// checkTheme warns about an invalid ui.theme; a valid or empty one is silent.
func checkTheme(d Deps) (Result, bool) {
	switch strings.ToLower(strings.TrimSpace(d.Theme)) {
	case "", "auto", "dark", "light":
		return Result{}, false
	}
	return Result{Name: "theme", Status: Warn, Detail: fmt.Sprintf("ui.theme %q is not valid; using auto", d.Theme), Hint: "set ui.theme to auto, dark or light"}, true
}

func checkTerm(d Deps) Result {
	term := d.Getenv("TERM")
	ct := strings.ToLower(d.Getenv("COLORTERM"))
	switch {
	case term == "" || term == "dumb":
		return Result{Name: "terminal", Status: Warn, Detail: fmt.Sprintf("TERM=%q", term), Hint: "run decsr in a real terminal (the TUI needs one)"}
	case ct == "truecolor" || ct == "24bit":
		return Result{Name: "terminal", Status: OK, Detail: "TERM=" + term + ", truecolor"}
	default:
		return Result{Name: "terminal", Status: Warn, Detail: "TERM=" + term + ", truecolor not advertised", Hint: "use a truecolor terminal or set COLORTERM=truecolor for the best theme"}
	}
}

// Print writes one line per result (glyph plus word, never color alone), a
// fix hint under each warning or failure, and a summary.
func Print(w io.Writer, rs []Result) {
	var ok, warn, fail int
	for _, r := range rs {
		var tag string
		switch r.Status {
		case OK:
			tag, ok = "✓ OK  ", ok+1
		case Warn:
			tag, warn = "! WARN", warn+1
		default:
			tag, fail = "✗ FAIL", fail+1
		}
		req := ""
		if r.Required {
			req = " (required)"
		}
		_, _ = fmt.Fprintf(w, "%s  %-22s %s%s\n", tag, r.Name, r.Detail, req)
		if r.Status != OK && r.Hint != "" {
			_, _ = fmt.Fprintf(w, "        fix: %s\n", r.Hint)
		}
	}
	verdict := "ready"
	if Failed(rs) {
		verdict = "not ready (a required check failed)"
	}
	_, _ = fmt.Fprintf(w, "\n%d ok, %d warnings, %d failed: %s\n", ok, warn, fail, verdict)
}

// checkModes reports one line for each role whose mode its runner cannot run.
func checkModes(d Deps) []Result {
	var rs []Result
	for _, a := range d.Agents {
		if a.ModeErr != "" {
			rs = append(rs, Result{Name: "mode " + a.Name, Status: Warn, Detail: a.ModeErr, Hint: "set mode: text or agent for this role in .decsr/project.yaml, or change its runner"})
		}
	}
	return rs
}
