package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/executil"
)

// Level says how much a note needs the user. It picks the ntfy priority and
// tags and the desktop urgency.
type Level int

const (
	Info     Level = iota
	NeedsYou       // something is blocked until the user answers
	Failed         // something went wrong
	Done           // a piece of work finished
)

func (l Level) urgent() bool { return l == NeedsYou || l == Failed }

// Note is one notification.
type Note struct {
	Title string
	Body  string
	Level Level
}

// Webhook formats.
const (
	FormatNtfy    = "ntfy"
	FormatSlack   = "slack"
	FormatDiscord = "discord"
)

const (
	sendTimeout = 5 * time.Second
	dedupeFor   = 10 * time.Second
)

// Doer is the part of http.Client the webhook needs.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Deps are the side effects of a Sender. Zero fields get real implementations.
type Deps struct {
	CMux     func(title, subtitle, body string)
	Exec     func(ctx context.Context, name string, args ...string) error
	LookPath func(name string) (string, error)
	HTTP     Doer
	OpenTTY  func() (io.WriteCloser, error)
	GOOS     string
	Getenv   func(key string) string
	Now      func() time.Time
	Go       func(func()) // runs one channel; tests pass a synchronous runner
}

// Sender delivers notes to the enabled channels.
type Sender struct {
	d    Deps
	wg   sync.WaitGroup
	mu   sync.Mutex
	last map[string]time.Time
}

// NewSender fills the missing deps with the real ones.
func NewSender(d Deps) *Sender {
	if d.CMux == nil {
		d.CMux = SendCMux
	}
	if d.Exec == nil {
		d.Exec = func(ctx context.Context, name string, args ...string) error {
			return executil.CommandContext(ctx, name, args...).Run()
		}
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: sendTimeout}
	}
	if d.OpenTTY == nil {
		d.OpenTTY = func() (io.WriteCloser, error) { return os.OpenFile("/dev/tty", os.O_WRONLY, 0) }
	}
	if d.GOOS == "" {
		d.GOOS = runtime.GOOS
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Sender{d: d, last: map[string]time.Time{}}
	if d.Go == nil {
		s.d.Go = func(f func()) {
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				f()
			}()
		}
	}
	return s
}

var defaultSender = NewSender(Deps{})

// Send delivers n through the default sender. It never blocks on the network
// and never fails: every channel is best-effort. It does nothing under
// "go test", so tests never ring the bell of the developer's terminal.
func Send(cfg config.NotifyConfig, n Note) {
	if testing.Testing() {
		return
	}
	defaultSender.Send(cfg, n)
}

// Wait blocks until the default sender has finished its pending deliveries or
// d has passed. Call it before the process exits so the last note is not lost.
func Wait(d time.Duration) { defaultSender.Wait(d) }

// Wait blocks until pending deliveries are done or d has passed.
func (s *Sender) Wait(d time.Duration) {
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}

// Send delivers n to every enabled channel.
func (s *Sender) Send(cfg config.NotifyConfig, n Note) {
	if s.duplicate(n) {
		return
	}
	if !cfg.DisableCMux {
		s.d.Go(func() { s.d.CMux(n.Title, "", n.Body) })
	}
	if !cfg.DisableBell {
		s.d.Go(s.bell)
	}
	if !cfg.DisableDesktop {
		s.d.Go(func() { s.desktop(n) })
	}
	if cfg.Webhook != "" {
		s.d.Go(func() { s.webhook(cfg, n) })
	}
}

// duplicate reports whether an identical note went out in the last 10 s, and
// records n otherwise.
func (s *Sender) duplicate(n Note) bool {
	now := s.d.Now()
	key := n.Title + "\x00" + n.Body
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.last {
		if now.Sub(t) >= dedupeFor {
			delete(s.last, k)
		}
	}
	if _, ok := s.last[key]; ok {
		return true
	}
	s.last[key] = now
	return false
}

func (s *Sender) bell() {
	if s.d.GOOS == "windows" {
		return
	}
	w, err := s.d.OpenTTY()
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("\a"))
	_ = w.Close()
}

func (s *Sender) desktop(n Note) {
	var name string
	var args []string
	switch s.d.GOOS {
	case "linux":
		if s.d.Getenv("DISPLAY") == "" && s.d.Getenv("WAYLAND_DISPLAY") == "" {
			return
		}
		name = "notify-send"
		args = []string{"-a", "decsr"}
		if n.Level.urgent() {
			args = append(args, "-u", "critical")
		}
		args = append(args, n.Title, n.Body)
	case "darwin":
		name = "osascript"
		// Title and body travel as argv, never inside the script text.
		args = []string{
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run",
			n.Title, n.Body,
		}
	default:
		return
	}
	if _, err := s.d.LookPath(name); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if err := s.d.Exec(ctx, name, args...); err != nil {
		slog.Debug("desktop notification failed", slog.String("tool", name), slog.Any("error", err))
	}
}

func (s *Sender) webhook(cfg config.NotifyConfig, n Note) {
	req, err := buildWebhook(cfg, n)
	if err != nil {
		slog.Debug("webhook not sent", slog.Any("error", err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	resp, err := s.d.HTTP.Do(req.WithContext(ctx))
	if err != nil {
		slog.Debug("webhook failed", slog.String("host", req.URL.Host), slog.Any("error", redact(err)))
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 300 {
		slog.Debug("webhook rejected", slog.String("host", req.URL.Host), slog.Int("status", resp.StatusCode))
	}
}

// Host returns only the host of a webhook URL, for messages: the rest of the
// URL may hold a secret token. "" when the URL is empty, "invalid URL" when it
// cannot be used.
func Host(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "invalid URL"
	}
	return u.Host
}

// redact drops the URL a net/http error carries, which may include a token.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

func buildWebhook(cfg config.NotifyConfig, n Note) (*http.Request, error) {
	if Host(cfg.Webhook) == "invalid URL" {
		return nil, errors.New("webhook URL is not a valid http(s) URL")
	}
	var body []byte
	headers := map[string]string{}
	switch strings.ToLower(cfg.WebhookFormat) {
	case FormatSlack:
		body, _ = json.Marshal(map[string]string{"text": "*" + n.Title + "*\n" + n.Body})
		headers["Content-Type"] = "application/json"
	case FormatDiscord:
		body, _ = json.Marshal(map[string]string{"content": "**" + n.Title + "**\n" + n.Body})
		headers["Content-Type"] = "application/json"
	case "", FormatNtfy:
		body = []byte(n.Body)
		headers["Title"] = mime.QEncoding.Encode("UTF-8", n.Title)
		headers["Priority"] = "default"
		if n.Level.urgent() {
			headers["Priority"] = "high"
		}
		headers["Tags"] = tagFor(n.Level)
	default:
		return nil, fmt.Errorf("unknown webhook_format %q", cfg.WebhookFormat)
	}
	req, err := http.NewRequest(http.MethodPost, cfg.Webhook, bytes.NewReader(body)) // #nosec G107 -- user-configured URL
	if err != nil {
		return nil, errors.New("webhook URL is not valid")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

func tagFor(l Level) string {
	switch l {
	case NeedsYou:
		return "bell"
	case Failed:
		return "rotating_light"
	case Done:
		return "white_check_mark"
	}
	return "information_source"
}
