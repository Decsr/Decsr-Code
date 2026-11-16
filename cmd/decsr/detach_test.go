package main

import (
	"bytes"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/multiplexer"
)

type fakeRunner struct {
	calls  [][]string
	listed string
	err    error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) > 0 && args[0] == "list-sessions" {
		return f.listed, f.err
	}
	return "", nil
}

type fakeDeps struct {
	d      detachDeps
	r      *fakeRunner
	execd  []string
	out    *bytes.Buffer
	envMap map[string]string
}

func newFake(listed string) *fakeDeps {
	f := &fakeDeps{r: &fakeRunner{listed: listed}, out: &bytes.Buffer{}, envMap: map[string]string{}}
	f.d = detachDeps{
		runner:   f.r,
		lookPath: func(string) (string, error) { return "/usr/bin/tmux", nil },
		getenv:   func(k string) string { return f.envMap[k] },
		exe:      func() (string, error) { return "/bin/decsr", nil },
		goos:     "linux",
		exec:     func(_ string, args []string) error { f.execd = args; return nil },
		out:      f.out,
		now:      time.Now,
	}
	return f
}

func TestSessionNameSlug(t *testing.T) {
	n := multiplexer.SessionName("/home/u/My.Proj:v2")
	if !strings.HasPrefix(n, "decsr-my-proj-v2-") || strings.ContainsAny(n, ".:") {
		t.Errorf("bad name %q", n)
	}
	if len(n) != len("decsr-my-proj-v2-")+4 {
		t.Errorf("want 4 hex suffix: %q", n)
	}
	if multiplexer.SessionName("/a/app") == multiplexer.SessionName("/b/app") {
		t.Error("same base name must not collide")
	}
	if a, b := multiplexer.SessionName("/a/app"), multiplexer.SessionName("/a/app/"); a != b {
		t.Error("name must be stable")
	}
	if !strings.HasPrefix(multiplexer.SessionName("/"), "decsr-") {
		t.Error("root dir")
	}
}

func TestStripTmuxFlag(t *testing.T) {
	got, ok := stripTmuxFlag([]string{"--issue", "5", "--tmux", "--force"})
	if !ok || !reflect.DeepEqual(got, []string{"--issue", "5", "--force"}) {
		t.Errorf("got %v %v", got, ok)
	}
	if got, ok := stripTmuxFlag([]string{"resume"}); ok || len(got) != 1 {
		t.Errorf("got %v %v", got, ok)
	}
	if got, ok := stripTmuxFlag([]string{"--tmux=false"}); ok || len(got) != 0 {
		t.Errorf("got %v %v", got, ok)
	}
}

func TestTmuxEntryRecursionGuard(t *testing.T) {
	for _, env := range []string{"TMUX", "DECSR_TMUX"} {
		f := newFake("")
		f.envMap[env] = "1"
		rest, handled, err := tmuxEntry(f.d, []string{"--tmux", "resume"}, func() bool { return true })
		if err != nil || handled || !reflect.DeepEqual(rest, []string{"resume"}) {
			t.Errorf("%s: %v %v %v", env, rest, handled, err)
		}
		if len(f.r.calls) != 0 || f.execd != nil {
			t.Errorf("%s: must not touch tmux", env)
		}
	}
}

func TestWantsTmux(t *testing.T) {
	yes := func() bool { return true }
	if !wantsTmux(false, nil, yes) {
		t.Error("plain decsr with ui.tmux")
	}
	if wantsTmux(false, []string{"doctor"}, yes) {
		t.Error("subcommands are unaffected by ui.tmux")
	}
	if !wantsTmux(true, []string{"resume"}, func() bool { return false }) {
		t.Error("--tmux")
	}
}

func TestLaunchCreatesThenAttaches(t *testing.T) {
	f := newFake("")
	dir := t.TempDir()
	if err := launchDetached(f.d, dir, []string{"resume"}); err != nil {
		t.Fatal(err)
	}
	name := multiplexer.SessionName(dir)
	var created []string
	for _, c := range f.r.calls {
		if len(c) > 1 && c[1] == "new-session" {
			created = c
		}
	}
	if created == nil || created[4] != name || created[6] != dir {
		t.Fatalf("new-session call: %v", created)
	}
	if got := created[len(created)-1]; got != "DECSR_TMUX=1 '/bin/decsr' 'resume'" {
		t.Errorf("command = %q", got)
	}
	if !reflect.DeepEqual(f.execd, []string{"tmux", "attach-session", "-t", name}) {
		t.Errorf("attach = %v", f.execd)
	}
}

func TestLaunchExistingAttachesOnly(t *testing.T) {
	dir := t.TempDir()
	name := multiplexer.SessionName(dir)
	f := newFake(name + "\t0\t100\t" + dir + "\n")
	if err := launchDetached(f.d, dir, nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.r.calls {
		if len(c) > 1 && c[1] == "new-session" {
			t.Error("must not create")
		}
	}
	if f.execd == nil || f.execd[1] != "attach-session" {
		t.Errorf("attach = %v", f.execd)
	}
	f.execd = nil
	if err := launchDetached(f.d, dir, []string{"--issue", "5"}); err == nil || !strings.Contains(err.Error(), "/issue") {
		t.Errorf("want refusal, got %v", err)
	}
	if f.execd != nil {
		t.Error("must not attach on refusal")
	}
}

func TestSwitchClientInsideTmux(t *testing.T) {
	f := newFake("")
	f.envMap["TMUX"] = "x"
	if err := f.d.attach("/usr/bin/tmux", "decsr-a-1234"); err != nil || f.execd[1] != "switch-client" {
		t.Errorf("%v %v", f.execd, err)
	}
}

func TestRequireTmuxErrors(t *testing.T) {
	f := newFake("")
	f.d.goos = "windows"
	if err := launchDetached(f.d, t.TempDir(), nil); !errors.Is(err, errTmuxWindows) || !strings.Contains(err.Error(), "not supported on Windows") {
		t.Errorf("windows: %v", err)
	}
	f = newFake("")
	f.d.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	err := launchDetached(f.d, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "decsr doctor") {
		t.Errorf("not installed: %v", err)
	}
}

func sess(names ...string) []multiplexer.Session {
	var s []multiplexer.Session
	for _, n := range names {
		s = append(s, multiplexer.Session{Name: n, Project: "/p/" + n})
	}
	return s
}

func TestSelectSession(t *testing.T) {
	cwd := t.TempDir()
	mine := multiplexer.SessionName(cwd)

	if _, err := selectSession(nil, "", cwd); err == nil {
		t.Error("0 sessions must error")
	}
	if s, err := selectSession(sess("decsr-x-1111"), "", cwd); err != nil || s.Name != "decsr-x-1111" {
		t.Errorf("single: %v %v", s, err)
	}
	_, err := selectSession(sess("decsr-x-1111", "decsr-y-2222"), "", cwd)
	var amb errAmbiguous
	if !errors.As(err, &amb) || !strings.Contains(err.Error(), "decsr-y-2222") {
		t.Errorf("many: %v", err)
	}
	if s, err := selectSession(sess("decsr-x-1111", mine), "", cwd); err != nil || s.Name != mine {
		t.Errorf("cwd preferred: %v %v", s, err)
	}
	if s, err := selectSession(sess("decsr-x-1111", "decsr-y-2222"), "decsr-y-2222", cwd); err != nil || s.Name != "decsr-y-2222" {
		t.Errorf("by name: %v %v", s, err)
	}
	if s, err := selectSession(sess("decsr-x-1111", "decsr-y-2222"), "y-2222", cwd); err != nil || s.Name != "decsr-y-2222" {
		t.Errorf("by short name: %v %v", s, err)
	}
	if s, err := selectSession(sess("decsr-x-1111", mine), cwd, "/elsewhere"); err != nil || s.Name != mine {
		t.Errorf("by path: %v %v", s, err)
	}
	ss := []multiplexer.Session{{Name: "decsr-z-9999", Project: cwd}}
	if s, err := selectSession(ss, cwd, "/elsewhere"); err != nil || s.Name != "decsr-z-9999" {
		t.Errorf("by project option: %v %v", s, err)
	}
	if _, err := selectSession(sess("decsr-x-1111"), "nope", cwd); err == nil {
		t.Error("unknown arg must error")
	}
}

func TestAttachCommandAndLs(t *testing.T) {
	f := newFake("other\t1\t5\t\ndecsr-x-1111\t1\t1700000000\t/p/x\nbad line\ndecsr-y-2222\t0\t1700000100\t\n")
	if err := lsCommand(f.d); err != nil {
		t.Fatal(err)
	}
	out := f.out.String()
	for _, want := range []string{"NAME", "decsr-x-1111", "/p/x", "attached", "decsr-y-2222", "detached"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "other") {
		t.Error("non-decsr session listed")
	}
	if err := attachCommand(f.d, nil, "/nowhere"); !errors.As(err, new(errAmbiguous)) {
		t.Errorf("want ambiguous, got %v", err)
	}
	if err := attachCommand(f.d, []string{"x-1111"}, "/nowhere"); err != nil || f.execd[3] != "decsr-x-1111" {
		t.Errorf("%v %v", f.execd, err)
	}

	empty := newFake("")
	empty.r.err = errors.New("tmux: no server running on /tmp/tmux-1000/default")
	if err := lsCommand(empty.d); err != nil || !strings.Contains(empty.out.String(), "no decsr sessions") {
		t.Errorf("%v %q", err, empty.out.String())
	}
}

func TestParseSessionsRobust(t *testing.T) {
	got := multiplexer.ParseSessions("decsr-a-1\t2\tnotanumber\t/p with space\r\n\ndecsr-b-2\n")
	if len(got) != 2 || !got[0].Attached || got[0].Project != "/p with space" || !got[0].Created.IsZero() || got[1].Attached {
		t.Errorf("%+v", got)
	}
}

func TestDetachIntegrationRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	name := "decsr-test-" + strings.ReplaceAll(t.Name(), "/", "-") + "-" + time.Now().Format("150405.000")
	name = strings.ReplaceAll(name, ".", "")
	r := multiplexer.ExecRunner{}
	dir := t.TempDir()
	if _, err := r.Run("tmux", "new-session", "-d", "-s", name, "-c", dir, "sleep 30"); err != nil {
		t.Skipf("cannot start tmux: %v", err)
	}
	defer func() { _, _ = r.Run("tmux", "kill-session", "-t", name) }()
	_, _ = r.Run("tmux", "set-option", "-t", name, multiplexer.ProjectOption, dir)
	ss, err := multiplexer.ListSessions(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range ss {
		if s.Name == name {
			if s.Project != dir || s.Attached || s.Created.IsZero() {
				t.Errorf("session = %+v", s)
			}
			return
		}
	}
	t.Errorf("session %s not listed in %+v", name, ss)
}
