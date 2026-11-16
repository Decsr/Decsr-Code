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
	rs = append(rs, checkOptional(d)...)
	return rs
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
	case "openai", "deepseek":
		return checkAPIRunner(d, g, name, who)
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

func checkAPIRunner(d Deps, g runnerGroup, name, who string) []Result {
	// Distinct key variables, in order of first use; the key value is never read out.
	envs := map[string][]Agent{}
	var order []string
	var noURL []Agent
	for _, a := range g.agents {
		env := a.APIKeyEnv
		if env == "" && g.name == "deepseek" {
			env = deepSeekKeyEnv
		}
		if _, ok := envs[env]; !ok {
			order = append(order, env)
		}
		envs[env] = append(envs[env], a)
		if g.name == "openai" && a.BaseURL == "" {
			noURL = append(noURL, a)
		}
	}
	var rs []Result
	keysOK := true
	for _, env := range order {
		as := envs[env]
		switch {
		case env == "":
			keysOK = false
			rs = append(rs, Result{Name: name + " key", Status: Fail, Detail: "no api_key_env set (agents: " + agentNames(as) + ")", Hint: "set `api_key_env` for these agents to the name of the env var holding the key"})
		case d.Getenv(env) == "":
			keysOK = false
			rs = append(rs, Result{Name: name + " key", Status: Fail, Detail: env + " is not set (agents: " + agentNames(as) + ")", Hint: "export " + env + "=<your key>"})
		default:
			rs = append(rs, Result{Name: name + " key", Status: OK, Detail: env + " is set (agents: " + agentNames(as) + ")"})
		}
	}
	if len(noURL) > 0 {
		keysOK = false
		rs = append(rs, Result{Name: name + " base_url", Status: Fail, Detail: "base_url is not set (agents: " + agentNames(noURL) + ")", Hint: "set `base_url` for these agents, e.g. https://openrouter.ai/api/v1"})
	}
	if g.name == "deepseek" && keysOK {
		if err := d.Reachable("deepseek"); err != nil {
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
				return Result{Name: "tmux", Status: Warn, Detail: "not found on PATH (used by the multiplexer)", Hint: "install tmux, e.g. `brew install tmux` or `apt install tmux`"}
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
