package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/executil"
)

const markerInstall = "===INSTALL==="

// Kinds of install action the lead may propose.
const (
	InstallDecsrPlugin  = "decsr-plugin"
	InstallClaudePlugin = "claude-plugin"
	InstallClaudeMCP    = "claude-mcp"
)

// Scopes of a Claude Code install.
const (
	ScopeLocal = "local"
	ScopeUser  = "user"
)

// SecretAsk is the value the lead writes where a secret belongs. decsr asks the
// user for it, hidden, when the install is confirmed.
const SecretAsk = "<ask>"

// SecretMask stands for a secret in everything that is shown.
const SecretMask = "••••"

const (
	// maxInstallActions is the most install actions one reply may carry.
	maxInstallActions = 5
	maxInstallArgs    = 24
	maxInstallValue   = 512

	// OfficialMarketplace is the Claude Code marketplace of Anthropic.
	OfficialMarketplace = "claude-plugins-official"
)

// InstallAction is one install the lead proposes. decsr runs it only after the
// user's key press on its card.
type InstallAction struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Source is the catalog name or marketplace repo (decsr-plugin),
	// <plugin>[@<marketplace>] (claude-plugin), or the URL or program of an
	// MCP server.
	Source    string            `json:"source"`
	Args      []string          `json:"args,omitempty"`
	Transport string            `json:"transport,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Scope     string            `json:"scope,omitempty"`
	Why       string            `json:"why,omitempty"`
}

// InstallRecord is an install of the session, kept without any secret value.
type InstallRecord struct {
	Name  string    `json:"name"`
	Kind  string    `json:"kind"`
	Scope string    `json:"scope,omitempty"`
	OK    bool      `json:"ok"`
	Note  string    `json:"note,omitempty"` // "needs you" for an install an automation run skipped
	At    time.Time `json:"at"`
}

var (
	installPluginRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}(@[A-Za-z0-9][A-Za-z0-9._-]{0,63})?$`)
	installNameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	envKeyRe        = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	headerKeyRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)
	secretNameRe    = regexp.MustCompile(`(?i)(key|token|secret|passw|credential|auth)`)
)

type installRaw struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Source    string            `json:"source"`
	Args      []string          `json:"args"`
	Transport string            `json:"transport"`
	Env       map[string]string `json:"env"`
	Headers   map[string]string `json:"headers"`
	Scope     string            `json:"scope"`
	Why       string            `json:"why"`
}

// parseInstalls reads the INSTALL section. An action that is invalid is
// dropped and reported in warnings.
func parseInstalls(output string) (acts []InstallAction, warnings []string) {
	raw, ok := section(output, markerInstall)
	if !ok {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := unmarshalArray(raw, &entries); err != nil {
		return nil, []string{fmt.Sprintf("install: not valid JSON (%s), ignored", shortReason(err))}
	}
	seen := map[string]bool{}
	for i, e := range entries {
		var r installRaw
		if err := json.Unmarshal(e, &r); err != nil {
			warnings = append(warnings, fmt.Sprintf("install %d: not an install action, dropped", i+1))
			continue
		}
		if len(acts) >= maxInstallActions {
			warnings = append(warnings, fmt.Sprintf("install: more than %d actions, the rest dropped", maxInstallActions))
			break
		}
		a, warns, err := cleanInstall(r)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("install %s: %v, dropped", orDash(cutRunes(Sanitize(r.Name), 40)), err))
			continue
		}
		warnings = append(warnings, warns...)
		key := a.Kind + "/" + a.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		acts = append(acts, a)
	}
	return acts, warnings
}

func cleanInstall(r installRaw) (a InstallAction, warns []string, err error) {
	a = InstallAction{
		Kind:      strings.ToLower(strings.TrimSpace(r.Kind)),
		Name:      strings.TrimSpace(r.Name),
		Source:    strings.TrimSpace(r.Source),
		Transport: strings.ToLower(strings.TrimSpace(r.Transport)),
		Scope:     strings.ToLower(strings.TrimSpace(r.Scope)),
		Why:       cutRunes(Sanitize(r.Why), 200),
	}
	switch a.Kind {
	case InstallDecsrPlugin, InstallClaudePlugin, InstallClaudeMCP:
	default:
		return a, nil, fmt.Errorf("unknown kind %q", cutRunes(Sanitize(r.Kind), 30))
	}
	if a.Name == "" || a.Source == "" {
		return a, nil, fmt.Errorf("name and source are required")
	}
	if a.Scope == "" {
		a.Scope = ScopeLocal
	}
	if a.Scope != ScopeLocal && a.Scope != ScopeUser {
		return a, nil, fmt.Errorf("scope %q is not local or user", cutRunes(Sanitize(r.Scope), 20))
	}
	for _, s := range []string{a.Name, a.Source} {
		if len(s) > maxInstallValue || strings.ContainsAny(s, "\x00\r\n") {
			return a, nil, fmt.Errorf("name or source is not usable")
		}
	}
	switch a.Kind {
	case InstallDecsrPlugin:
		if !config.ValidName(a.Name) {
			return a, nil, fmt.Errorf("%q is not a plugin name", cutRunes(Sanitize(a.Name), 40))
		}
		a.Scope = ""
	case InstallClaudePlugin:
		if !installPluginRe.MatchString(a.Source) {
			return a, nil, fmt.Errorf("source must be <plugin> or <plugin>@<marketplace>")
		}
	case InstallClaudeMCP:
		if !installNameRe.MatchString(a.Name) {
			return a, nil, fmt.Errorf("%q is not a server name", cutRunes(Sanitize(a.Name), 40))
		}
		warns, err = cleanMCP(&a, r)
	}
	return a, warns, err
}

func cleanMCP(a *InstallAction, r installRaw) (warns []string, err error) {
	if a.Transport == "" {
		a.Transport = "stdio"
		if strings.HasPrefix(a.Source, "http://") || strings.HasPrefix(a.Source, "https://") {
			a.Transport = "http"
		}
	}
	switch a.Transport {
	case "stdio", "http", "sse":
	default:
		return nil, fmt.Errorf("transport %q is not stdio, http or sse", cutRunes(Sanitize(a.Transport), 20))
	}
	a.Args = r.Args
	if a.Transport == "stdio" {
		if strings.HasPrefix(a.Source, "-") {
			return nil, fmt.Errorf("the program must not start with -")
		}
		if f := strings.Fields(a.Source); len(f) > 1 && len(a.Args) == 0 {
			a.Source, a.Args = f[0], f[1:]
		}
		if strings.ContainsAny(a.Source, " \t") {
			return nil, fmt.Errorf("source is the program only; put its arguments in args")
		}
	} else {
		u, perr := url.Parse(a.Source)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("source must be an http(s) URL")
		}
		if len(a.Args) > 0 {
			warns = append(warns, "install "+a.Name+": args ignored for a "+a.Transport+" server")
			a.Args = nil
		}
	}
	if len(a.Args) > maxInstallArgs {
		return warns, fmt.Errorf("more than %d args", maxInstallArgs)
	}
	for _, s := range a.Args {
		if len(s) > maxInstallValue || strings.ContainsAny(s, "\x00\r\n") {
			return warns, fmt.Errorf("an argument is not usable")
		}
	}
	if len(r.Env) > 0 {
		a.Env = map[string]string{}
	}
	for k, v := range r.Env {
		if !envKeyRe.MatchString(k) || len(v) > maxInstallValue || strings.ContainsAny(v, "\x00\r\n") {
			return warns, fmt.Errorf("env %q is not usable", cutRunes(Sanitize(k), 30))
		}
		a.Env[k], warns = askIfSecret(k, v, warns)
	}
	if len(r.Headers) > 0 && a.Transport != "stdio" {
		a.Headers = map[string]string{}
	}
	for k, v := range r.Headers {
		if a.Transport == "stdio" {
			warns = append(warns, "install "+a.Name+": headers ignored for a stdio server")
			break
		}
		if !headerKeyRe.MatchString(k) || len(v) > maxInstallValue || strings.ContainsAny(v, "\x00\r\n") {
			return warns, fmt.Errorf("header %q is not usable", cutRunes(Sanitize(k), 30))
		}
		a.Headers[k], warns = askIfSecret(k, v, warns)
	}
	return warns, nil
}

// askIfSecret replaces a literal value of a secret-looking name with the
// placeholder, so a secret the lead wrote is never shown, run or kept.
func askIfSecret(key, val string, warns []string) (string, []string) {
	if val != "" && !strings.Contains(val, SecretAsk) && secretNameRe.MatchString(key) {
		if strings.EqualFold(key, "authorization") && strings.HasPrefix(val, "Bearer ") {
			return "Bearer " + SecretAsk, append(warns, "install: the value of "+key+" is asked for, not taken from the reply")
		}
		return SecretAsk, append(warns, "install: the value of "+key+" is asked for, not taken from the reply")
	}
	return val, warns
}

// SecretSlots are the values the user is asked for, in a fixed order: env,
// then headers, then arguments.
func (a InstallAction) SecretSlots() []string {
	var out []string
	for _, k := range sortedKeys(a.Env) {
		if strings.Contains(a.Env[k], SecretAsk) {
			out = append(out, "env "+k)
		}
	}
	for _, k := range sortedKeys(a.Headers) {
		if strings.Contains(a.Headers[k], SecretAsk) {
			out = append(out, "header "+k)
		}
	}
	for i, s := range a.Args {
		if strings.Contains(s, SecretAsk) {
			out = append(out, "arg "+strconv.Itoa(i+1))
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Argv is the argument list after "claude" for a claude-plugin or claude-mcp
// action: no shell is involved. secrets maps a slot to its value; a slot
// without a value (and every slot when secrets is nil) shows as SecretMask.
// accept is the sha256 of a marketplace-declared command the user confirmed.
func (a InstallAction) Argv(secrets map[string]string, accept string) []string {
	fill := func(slot, v string) string {
		val := SecretMask
		if s, ok := secrets[slot]; ok {
			val = s
		}
		return strings.ReplaceAll(v, SecretAsk, val)
	}
	scope := a.Scope
	if scope == "" {
		scope = ScopeLocal
	}
	switch a.Kind {
	case InstallClaudePlugin:
		argv := []string{"plugin", "install", a.Source, "--scope", scope, "--json"}
		if accept != "" {
			argv = append(argv, "--accept-command", accept)
		}
		return argv
	case InstallClaudeMCP:
		argv := []string{"mcp", "add", "--scope", scope, "--transport", a.Transport, a.Name}
		if a.Transport != "stdio" {
			argv = append(argv, a.Source)
		}
		for _, k := range sortedKeys(a.Env) {
			argv = append(argv, "-e", k+"="+fill("env "+k, a.Env[k]))
		}
		for _, k := range sortedKeys(a.Headers) {
			argv = append(argv, "-H", k+": "+fill("header "+k, a.Headers[k]))
		}
		if a.Transport == "stdio" {
			argv = append(argv, "--", a.Source)
			for i, s := range a.Args {
				argv = append(argv, fill("arg "+strconv.Itoa(i+1), s))
			}
		}
		return argv
	}
	return nil
}

// Command is the command line the card shows: the program and Argv with every
// secret masked. --json is left out, it only changes how the result is printed.
func (a InstallAction) Command() string {
	argv := a.Argv(nil, "")
	if a.Kind == InstallClaudePlugin {
		argv = argv[:len(argv)-1]
	}
	parts := []string{"claude"}
	for _, s := range argv {
		parts = append(parts, shellWord(s))
	}
	return strings.Join(parts, " ")
}

// shellWord quotes s for display when it holds a space or a quote.
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$`") {
		return s
	}
	return strconv.Quote(s)
}

// ── running the claude CLI ───────────────────────────────────────────────────

// ClaudeCLI runs the installed claude program for the plugin and MCP installs.
// Binary is its path; "" looks "claude" up on PATH.
type ClaudeCLI struct {
	Binary string
	Dir    string
}

// installTimeout bounds one install command.
const installTimeout = 3 * time.Minute

// Run runs claude with argv, without a shell and without input, and returns
// what it printed.
func (c ClaudeCLI) Run(ctx context.Context, argv []string) (stdout, stderr string, err error) {
	bin := c.Binary
	if bin == "" {
		bin = "claude"
	}
	ctx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()
	cmd := executil.CommandContext(ctx, bin, argv...)
	cmd.Dir = c.Dir
	cmd.Env = os.Environ()
	var out, errb limitedBuffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.String(), errb.String(), err
}

// limitedBuffer keeps the first 256 KiB written to it.
type limitedBuffer struct{ b []byte }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := 256<<10 - len(l.b); room > 0 {
		l.b = append(l.b, p[:min(len(p), room)]...)
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string { return string(l.b) }

// ClaudeInstallResult is what a claude plugin install printed.
type ClaudeInstallResult struct {
	OK bool
	// Confirm is set when the marketplace declares a command that the user must
	// confirm: Command is its text and SHA its sha256.
	Confirm bool
	Command string
	SHA     string
	// Err is the first error line when the install failed.
	Err string
}

// ParseClaudeInstall reads the result of "claude plugin install --json"
// leniently. accepted is the sha256 the run was given with --accept-command.
func ParseClaudeInstall(stdout, stderr string, runErr error, accepted string) ClaudeInstallResult {
	obj := lastJSONObject(stdout)
	var res ClaudeInstallResult
	if sc, ok := obj["shownCommand"].(map[string]any); ok {
		if sha, _ := sc["sha256"].(string); sha != "" && sha != accepted {
			res.Confirm, res.SHA, res.Command = true, sha, shownCommandText(sc)
			return res
		}
	}
	failed := runErr != nil
	if v, ok := obj["ok"].(bool); ok && !v {
		failed = true
	}
	if v, ok := obj["success"].(bool); ok && !v {
		failed = true
	}
	for _, k := range []string{"status", "outcome"} {
		s, _ := obj[k].(string)
		switch strings.ToLower(s) {
		case "error", "failed", "refused", "rejected", "cancelled", "canceled":
			failed = true
		case "needs_confirmation", "confirm", "pending":
			if sc, ok := obj["shownCommand"].(map[string]any); ok {
				if sha, _ := sc["sha256"].(string); sha != "" && sha != accepted {
					continue
				}
			}
			if accepted == "" {
				if sc, ok := obj["shownCommand"].(map[string]any); ok {
					res.Confirm, res.Command = true, shownCommandText(sc)
					res.SHA, _ = sc["sha256"].(string)
					return res
				}
			}
		}
	}
	if !failed {
		res.OK = true
		return res
	}
	res.Err = firstErrorLine(obj, stdout, stderr, runErr)
	return res
}

// lastJSONObject is the last line of s that is a JSON object (the whole of s
// when it is one), or nil.
func lastJSONObject(s string) map[string]any {
	var obj map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(s)), &obj) == nil {
		return obj
	}
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var o map[string]any
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &o) == nil {
			return o
		}
	}
	return nil
}

// shownCommandText is the command of a shownCommand object, whatever the field
// is called; the object without its sha256 when none is found.
func shownCommandText(sc map[string]any) string {
	for _, k := range []string{"command", "text", "cmd", "value", "shown"} {
		switch v := sc[k].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return SanitizeText(v)
			}
		case []any:
			var parts []string
			for _, p := range v {
				if s, ok := p.(string); ok {
					parts = append(parts, shellWord(s))
				}
			}
			if len(parts) > 0 {
				return SanitizeText(strings.Join(parts, " "))
			}
		}
	}
	rest := map[string]any{}
	for k, v := range sc {
		if k != "sha256" {
			rest[k] = v
		}
	}
	b, _ := json.Marshal(rest)
	return SanitizeText(string(b))
}

// firstErrorLine picks the line to show for a failed install.
func firstErrorLine(obj map[string]any, stdout, stderr string, runErr error) string {
	pick := func(vs ...string) string {
		for _, v := range vs {
			for _, l := range strings.Split(SanitizeText(v), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					return cutRunes(l, 240)
				}
			}
		}
		return ""
	}
	var fromJSON string
	for _, k := range []string{"error", "message", "reason"} {
		if s, ok := obj[k].(string); ok && s != "" {
			fromJSON = s
			break
		}
	}
	if fromJSON == "" {
		if errs, ok := obj["errors"].([]any); ok && len(errs) > 0 {
			fromJSON, _ = errs[0].(string)
		}
	}
	if fromJSON != "" {
		if code, _ := obj["failureCode"].(string); code != "" {
			fromJSON += " (" + code + ")"
		}
	}
	line := pick(fromJSON, stderr)
	if line == "" && obj == nil {
		line = pick(stdout)
	}
	if line == "" && runErr != nil {
		line = pick(runErr.Error())
	}
	if line == "" {
		line = "the claude CLI reported a failure"
	}
	return line
}
