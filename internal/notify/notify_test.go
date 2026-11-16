package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
)

type recDoer struct {
	mu   sync.Mutex
	reqs []*http.Request
	body []string
	err  error
}

func (r *recDoer) Do(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, _ := io.ReadAll(req.Body)
	r.reqs = append(r.reqs, req)
	r.body = append(r.body, string(b))
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
}

type rig struct {
	s     *Sender
	http  *recDoer
	cmux  []string
	execs [][]string
	tty   *strings.Builder
	ttyOK bool
	env   map[string]string
	have  map[string]bool
	now   time.Time
}

type ttyW struct{ b *strings.Builder }

func (t ttyW) Write(p []byte) (int, error) { return t.b.Write(p) }
func (ttyW) Close() error                  { return nil }

func newRig(goos string) *rig {
	r := &rig{http: &recDoer{}, tty: &strings.Builder{}, ttyOK: true, env: map[string]string{}, have: map[string]bool{"notify-send": true, "osascript": true}, now: time.Unix(1000, 0)}
	r.s = NewSender(Deps{
		CMux: func(title, sub, body string) { r.cmux = append(r.cmux, title+"|"+sub+"|"+body) },
		Exec: func(_ context.Context, name string, args ...string) error {
			r.execs = append(r.execs, append([]string{name}, args...))
			return nil
		},
		LookPath: func(n string) (string, error) {
			if r.have[n] {
				return "/bin/" + n, nil
			}
			return "", errors.New("missing")
		},
		HTTP: r.http,
		OpenTTY: func() (io.WriteCloser, error) {
			if !r.ttyOK {
				return nil, errors.New("no tty")
			}
			return ttyW{r.tty}, nil
		},
		GOOS:   goos,
		Getenv: func(k string) string { return r.env[k] },
		Now:    func() time.Time { return r.now },
		Go:     func(f func()) { f() },
	})
	return r
}

func TestSend_AllChannelsLinux(t *testing.T) {
	r := newRig("linux")
	r.env["DISPLAY"] = ":0"
	r.s.Send(config.NotifyConfig{Webhook: "https://ntfy.sh/topic"}, Note{Title: "decsr", Body: "Assign roles", Level: NeedsYou})
	if len(r.cmux) != 1 || r.cmux[0] != "decsr||Assign roles" {
		t.Errorf("cmux = %v", r.cmux)
	}
	if r.tty.String() != "\a" {
		t.Errorf("bell = %q", r.tty.String())
	}
	want := []string{"notify-send", "-a", "decsr", "-u", "critical", "decsr", "Assign roles"}
	if len(r.execs) != 1 || strings.Join(r.execs[0], "|") != strings.Join(want, "|") {
		t.Errorf("exec = %v", r.execs)
	}
	if len(r.http.reqs) != 1 {
		t.Fatalf("webhook calls = %d", len(r.http.reqs))
	}
}

func TestSend_DisableFlags(t *testing.T) {
	r := newRig("linux")
	r.env["DISPLAY"] = ":0"
	r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true, DisableDesktop: true}, Note{Title: "t", Body: "b"})
	if len(r.cmux)+len(r.execs)+len(r.http.reqs) != 0 || r.tty.Len() != 0 {
		t.Errorf("channels fired despite being disabled: %v %v %q", r.cmux, r.execs, r.tty.String())
	}
}

func TestDesktop_Gating(t *testing.T) {
	tests := []struct {
		name  string
		goos  string
		env   map[string]string
		have  bool
		fires bool
	}{
		{"linux no display", "linux", nil, true, false},
		{"linux display", "linux", map[string]string{"DISPLAY": ":0"}, true, true},
		{"linux wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "w"}, true, true},
		{"linux no tool", "linux", map[string]string{"DISPLAY": ":0"}, false, false},
		{"darwin", "darwin", nil, true, true},
		{"windows", "windows", map[string]string{"DISPLAY": ":0"}, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(tc.goos)
			r.env = tc.env
			if r.env == nil {
				r.env = map[string]string{}
			}
			r.have["notify-send"], r.have["osascript"] = tc.have, tc.have
			r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true}, Note{Title: "t", Body: "b", Level: Done})
			if got := len(r.execs) == 1; got != tc.fires {
				t.Errorf("fired = %v, want %v (%v)", got, tc.fires, r.execs)
			}
		})
	}
}

func TestDesktop_UrgencyOnlyForUrgentLevels(t *testing.T) {
	for lvl, critical := range map[Level]bool{Info: false, Done: false, NeedsYou: true, Failed: true} {
		r := newRig("linux")
		r.env["DISPLAY"] = ":0"
		r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true}, Note{Title: "t", Body: "b", Level: lvl})
		if got := strings.Contains(strings.Join(r.execs[0], " "), "-u critical"); got != critical {
			t.Errorf("level %d critical = %v, want %v", lvl, got, critical)
		}
	}
}

func TestDesktop_DarwinPassesTextAsArgv(t *testing.T) {
	r := newRig("darwin")
	evil := `x" & do shell script "touch /tmp/pwned" & "`
	r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true}, Note{Title: "T", Body: evil})
	a := r.execs[0]
	if a[0] != "osascript" || a[len(a)-2] != "T" || a[len(a)-1] != evil {
		t.Fatalf("argv = %q", a)
	}
	for _, s := range a[:len(a)-2] {
		if strings.Contains(s, "pwned") {
			t.Errorf("user text leaked into the script: %q", s)
		}
	}
}

func TestBell_Gating(t *testing.T) {
	r := newRig("windows")
	r.s.Send(config.NotifyConfig{DisableCMux: true, DisableDesktop: true}, Note{Title: "t", Body: "b"})
	if r.tty.Len() != 0 {
		t.Error("bell rang on windows")
	}
	r = newRig("linux")
	r.ttyOK = false
	r.s.Send(config.NotifyConfig{DisableCMux: true, DisableDesktop: true}, Note{Title: "t", Body: "b"}) // must not panic
	if r.tty.Len() != 0 {
		t.Error("bell written without a tty")
	}
}

func TestWebhook_Ntfy(t *testing.T) {
	r := newRig("linux")
	r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true, DisableDesktop: true, Webhook: "https://ntfy.sh/topic"}, Note{Title: "decsr · proj", Body: "x failed", Level: Failed})
	req := r.http.reqs[0]
	if req.Method != "POST" || r.http.body[0] != "x failed" {
		t.Errorf("req = %s %q", req.Method, r.http.body[0])
	}
	if req.Header.Get("Priority") != "high" || req.Header.Get("Tags") != "rotating_light" {
		t.Errorf("headers = %v", req.Header)
	}
	if got := req.Header.Get("Title"); !strings.HasPrefix(got, "=?UTF-8?") {
		t.Errorf("non-ASCII title should be RFC 2047 encoded, got %q", got)
	}
	r2 := newRig("linux")
	r2.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true, DisableDesktop: true, Webhook: "https://ntfy.sh/t"}, Note{Title: "decsr", Body: "ok", Level: Done})
	h := r2.http.reqs[0].Header
	if h.Get("Title") != "decsr" || h.Get("Priority") != "default" || h.Get("Tags") != "white_check_mark" {
		t.Errorf("headers = %v", h)
	}
}

func TestWebhook_SlackAndDiscord(t *testing.T) {
	for format, want := range map[string]string{
		FormatSlack:   `{"text":"*decsr*\nbody"}`,
		FormatDiscord: `{"content":"**decsr**\nbody"}`,
	} {
		r := newRig("linux")
		r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true, DisableDesktop: true, Webhook: "https://hooks.example.com/x", WebhookFormat: format}, Note{Title: "decsr", Body: "body"})
		if r.http.body[0] != want {
			t.Errorf("%s body = %s, want %s", format, r.http.body[0], want)
		}
		if r.http.reqs[0].Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s content type = %q", format, r.http.reqs[0].Header.Get("Content-Type"))
		}
	}
}

func TestWebhook_BadURLAndFormatSendNothing(t *testing.T) {
	for _, cfg := range []config.NotifyConfig{
		{Webhook: "ftp://x/y"},
		{Webhook: "not a url"},
		{Webhook: "https://ntfy.sh/t", WebhookFormat: "carrier-pigeon"},
	} {
		cfg.DisableCMux, cfg.DisableBell, cfg.DisableDesktop = true, true, true
		r := newRig("linux")
		r.s.Send(cfg, Note{Title: "t", Body: "b"})
		if len(r.http.reqs) != 0 {
			t.Errorf("%+v: sent a request", cfg)
		}
	}
}

func TestRedactAndHost(t *testing.T) {
	const secret = "s3cret-token"
	raw := "https://ntfy.example.com/" + secret
	_, err := (&http.Client{Transport: failTransport{}}).Get(raw)
	if !strings.Contains(err.Error(), secret) {
		t.Fatal("precondition: raw http error should contain the URL")
	}
	if got := redact(err).Error(); strings.Contains(got, secret) {
		t.Errorf("redacted error still has the secret: %q", got)
	}
	if got := Host(raw); got != "ntfy.example.com" {
		t.Errorf("Host = %q", got)
	}
	if got := Host("::bad"); got != "invalid URL" {
		t.Errorf("Host(bad) = %q", got)
	}
	r := newRig("linux")
	r.http.err = err
	r.s.Send(config.NotifyConfig{DisableCMux: true, DisableBell: true, DisableDesktop: true, Webhook: raw}, Note{Title: "t", Body: "b"}) // must not panic
}

type failTransport struct{}

func (failTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("boom") }

func TestDedupe(t *testing.T) {
	r := newRig("linux")
	cfg := config.NotifyConfig{DisableBell: true, DisableDesktop: true}
	n := Note{Title: "t", Body: "b"}
	r.s.Send(cfg, n)
	r.now = r.now.Add(5 * time.Second)
	r.s.Send(cfg, n)
	if len(r.cmux) != 1 {
		t.Fatalf("duplicate inside 10s was sent: %d", len(r.cmux))
	}
	r.s.Send(cfg, Note{Title: "t", Body: "other"})
	if len(r.cmux) != 2 {
		t.Error("different body should send")
	}
	r.now = r.now.Add(11 * time.Second)
	r.s.Send(cfg, n)
	if len(r.cmux) != 3 {
		t.Error("same note after 10s should send again")
	}
}

func TestSend_AsyncWait(t *testing.T) {
	done := make(chan struct{})
	s := NewSender(Deps{CMux: func(string, string, string) { close(done) }})
	s.Send(config.NotifyConfig{DisableBell: true, DisableDesktop: true}, Note{Title: "t", Body: "b"})
	s.Wait(2 * time.Second)
	select {
	case <-done:
	default:
		t.Error("Wait returned before the delivery finished")
	}
}
