package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/x/term"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const connectUsage = "usage: decsr connect <deepseek|openai|openrouter|groq|artificialanalysis|custom> [--no-test] [--name N --base-url URL]\n       decsr connect --list"

var customNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// maxModelChoices caps the model list printed when the user must pick one.
const maxModelChoices = 30

// connectIO is everything connect touches outside the credentials file and the
// global config, so tests can drive it.
type connectIO struct {
	In         io.Reader
	Out        io.Writer
	Err        io.Writer
	IsTTY      bool
	ReadSecret func() (string, error) // hidden input from the terminal
	Client     *http.Client           // nil = default
	Getenv     func(string) string
}

func stdConnectIO() connectIO {
	fd := os.Stdin.Fd()
	tty := term.IsTerminal(fd)
	return connectIO{
		In: os.Stdin, Out: os.Stdout, Err: os.Stderr, IsTTY: tty, Getenv: os.Getenv,
		Client: &http.Client{Timeout: runner.ProbeTimeout},
		ReadSecret: func() (string, error) {
			b, err := term.ReadPassword(fd)
			_, _ = fmt.Fprintln(os.Stdout)
			return string(b), err
		},
	}
}

func connectCmd(args []string) {
	if err := runConnect(args, stdConnectIO()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func disconnectCmd(args []string) {
	if err := runDisconnect(args, stdConnectIO()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseArgs lets flags come before or after the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

type lineReader struct{ r *bufio.Reader }

// line reads one line without its newline; eof is true when nothing was read.
func (l lineReader) line() (s string, eof bool) {
	s, err := l.r.ReadString('\n')
	if err != nil && s == "" {
		return "", true
	}
	return strings.TrimSpace(s), false
}

func runConnect(args []string, c connectIO) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	list := fs.Bool("list", false, "")
	noTest := fs.Bool("no-test", false, "")
	nameFlag := fs.String("name", "", "")
	baseFlag := fs.String("base-url", "", "")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return fmt.Errorf("%v\n%s", err, connectUsage)
	}
	if *list {
		return connectList(c)
	}
	if len(pos) != 1 {
		return errors.New(connectUsage)
	}
	in := lineReader{bufio.NewReader(c.In)}
	choice := pos[0]

	if choice == credentials.ArtificialAnalysis {
		return connectRankings(c, in)
	}

	var name, baseURL, runnerName, provider string
	var preset runner.Preset
	if p, ok := runner.PresetByName(choice); ok {
		preset, name, baseURL, runnerName = p, p.Name, p.BaseURL, p.Name
	} else if choice == "custom" {
		var err error
		if name, baseURL, err = askCustom(c, in, *nameFlag, *baseFlag); err != nil {
			return err
		}
		runnerName, provider = "openai", name
	} else {
		return fmt.Errorf("unknown provider %q\n%s", choice, connectUsage)
	}

	key, err := readKey(c, in, name)
	if err != nil {
		return err
	}

	var models []string
	if !*noTest {
		_, _ = fmt.Fprintf(c.Out, "Testing %s/models ...\n", strings.TrimRight(baseURL, "/"))
		models, err = runner.ProbeModels(context.Background(), c.Client, baseURL, key)
		var rejected *runner.KeyRejectedError
		switch {
		case errors.As(err, &rejected):
			return fmt.Errorf("%s: key rejected (HTTP %d) — not saved", name, rejected.Status)
		case err != nil:
			_, _ = fmt.Fprintf(c.Out, "Could not test the key: %v\nSave it anyway? [y/N]: ", err)
			ans, _ := in.line()
			if a := strings.ToLower(ans); a != "y" && a != "yes" {
				return errors.New("not saved")
			}
		default:
			_, _ = fmt.Fprintf(c.Out, "Key accepted (%d models).\n", len(models))
		}
	}

	cred := credentials.Cred{APIKey: key}
	if provider != "" {
		cred.BaseURL = baseURL
	}
	if err := credentials.Set(name, cred); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.Out, "Saved %s key (%s) to %s\n", name, credentials.Mask(key), credentials.DisplayPath)

	return askRoles(c, in, name, runnerName, provider, preset.DefaultModel, models)
}

// connectRankings saves the Artificial Analysis key. It is a data source, not
// an LLM provider, so no role is changed and the key is not tested: a test
// would use one of the 100 daily requests.
func connectRankings(c connectIO, in lineReader) error {
	key, err := readKey(c, in, credentials.ArtificialAnalysis)
	if err != nil {
		return err
	}
	if err := credentials.Set(credentials.ArtificialAnalysis, credentials.Cred{APIKey: key}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.Out, "Saved %s key (%s) to %s\nRun: decsr models rank\n", credentials.ArtificialAnalysis, credentials.Mask(key), credentials.DisplayPath)
	return nil
}

// askCustom collects the name and base URL of a custom provider.
func askCustom(c connectIO, in lineReader, name, baseURL string) (string, string, error) {
	if name == "" {
		_, _ = fmt.Fprint(c.Out, "Provider name (a-z, 0-9, -): ")
		name, _ = in.line()
	}
	if err := validateCustomName(name); err != nil {
		return "", "", err
	}
	if baseURL == "" {
		_, _ = fmt.Fprint(c.Out, "Base URL (e.g. https://api.example.com/v1): ")
		baseURL, _ = in.line()
	}
	if err := validateBaseURL(baseURL); err != nil {
		return "", "", err
	}
	return name, strings.TrimRight(baseURL, "/"), nil
}

func validateCustomName(name string) error {
	if !customNameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: use lowercase letters, digits and -", name)
	}
	if runner.IsPreset(name) || name == "custom" {
		return fmt.Errorf("%q is a built-in provider name — pick another name", name)
	}
	return nil
}

// validateBaseURL requires https, except http for localhost and 127.0.0.1.
func validateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid base URL %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" {
			return nil
		}
	}
	return fmt.Errorf("base URL must use https (http is only allowed for localhost and 127.0.0.1): %q", raw)
}

// readKey reads the key from the terminal without echo, or from the first line
// of stdin when it is not a terminal. It is never taken from the arguments.
func readKey(c connectIO, in lineReader, name string) (string, error) {
	var key string
	if c.IsTTY && c.ReadSecret != nil {
		_, _ = fmt.Fprintf(c.Out, "%s API key (input hidden): ", name)
		k, err := c.ReadSecret()
		if err != nil {
			return "", errors.New("could not read the key")
		}
		key = k
	} else {
		key, _ = in.line()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("no API key given")
	}
	return key, nil
}

// askRoles asks which roles use the new provider and writes the global config.
func askRoles(c connectIO, in lineReader, name, runnerName, provider, defModel string, models []string) error {
	_, _ = fmt.Fprintf(c.Out, "Use %s for: [1] the lead only [2] all roles [3] nothing now [3]: ", name)
	ans, _ := in.line()
	if ans != "1" && ans != "2" {
		_, _ = fmt.Fprintf(c.Out, "No roles changed. Pick it later with /config.\n")
		return nil
	}
	model := defModel
	if model == "" {
		model = pickModel(c, in, models)
		if model == "" {
			_, _ = fmt.Fprintln(c.Out, "No model chosen; roles not changed.")
			return nil
		}
	}
	if err := config.UseProviderGlobal(runnerName, provider, model, ans == "2"); err != nil {
		return fmt.Errorf("saved the key but could not update ~/.decsr/config.yaml: %w", err)
	}
	scope := "the lead"
	if ans == "2" {
		scope = "all roles"
	}
	_, _ = fmt.Fprintf(c.Out, "%s (%s) now runs %s in ~/.decsr/config.yaml.\n", name, model, scope)
	return nil
}

// pickModel shows the model list and returns the chosen id ("" = none).
func pickModel(c connectIO, in lineReader, models []string) string {
	if len(models) == 0 {
		_, _ = fmt.Fprint(c.Out, "Model id: ")
		m, _ := in.line()
		return m
	}
	shown := models
	if len(shown) > maxModelChoices {
		shown = shown[:maxModelChoices]
	}
	for i, m := range shown {
		_, _ = fmt.Fprintf(c.Out, "  [%d] %s\n", i+1, m)
	}
	if len(models) > len(shown) {
		_, _ = fmt.Fprintf(c.Out, "  ... %d more (type a model id)\n", len(models)-len(shown))
	}
	_, _ = fmt.Fprint(c.Out, "Model [1]: ")
	ans, _ := in.line()
	if ans == "" {
		return shown[0]
	}
	if n, err := strconv.Atoi(ans); err == nil {
		if n >= 1 && n <= len(shown) {
			return shown[n-1]
		}
		return ""
	}
	return ans
}

func connectList(c connectIO) error {
	entries, err := credentials.List()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(c.Out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tBASE URL\tKEY\tSAVED")
	rows := 0
	saved := map[string]bool{}
	for _, e := range entries {
		saved[e.Name] = true
		base := e.BaseURL
		if p, ok := runner.PresetByName(e.Name); ok && base == "" {
			base = p.BaseURL
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Name, base, e.Masked, e.SavedAt.Local().Format("2006-01-02 15:04"))
		rows++
	}
	for _, p := range runner.Presets() {
		if c.Getenv != nil && c.Getenv(p.KeyEnv) != "" {
			_, _ = fmt.Fprintf(tw, "%s\t%s\tenv (%s)\t-\n", p.Name, p.BaseURL, p.KeyEnv)
			rows++
		}
	}
	if rows == 0 {
		_, _ = fmt.Fprintln(c.Out, "No providers connected. Run: decsr connect deepseek")
		return nil
	}
	return tw.Flush()
}

func runDisconnect(args []string, c connectIO) error {
	fs := flag.NewFlagSet("disconnect", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		return errors.New("usage: decsr disconnect <name> [--yes]")
	}
	name := pos[0]
	_, ok, err := credentials.Get(name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no saved key for %q (see: decsr connect --list)", name)
	}
	if !*yes {
		_, _ = fmt.Fprintf(c.Out, "Delete the saved key for %s? [y/N]: ", name)
		ans, _ := lineReader{bufio.NewReader(c.In)}.line()
		if a := strings.ToLower(ans); a != "y" && a != "yes" {
			_, _ = fmt.Fprintln(c.Out, "Kept.")
			return nil
		}
	}
	if err := credentials.Delete(name); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(c.Out, "Deleted the saved key for %s.\n", name)
	return nil
}
