package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
)

const cliKey = "sk-testkey-0123456789abcd"

// redirect sends every request to srv, so presets can be tested offline.
type redirect struct{ to *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.to.Scheme, r.to.Host
	return http.DefaultTransport.RoundTrip(req)
}

func modelsServer(t *testing.T, status int) (*httptest.Server, *string) {
	t.Helper()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":[{"id":"m-one"},{"id":"m-two"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

func connectEnv(t *testing.T, srv *httptest.Server, stdin string) (connectIO, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"DEEPSEEK_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY"} {
		t.Setenv(v, "")
	}
	out := &bytes.Buffer{}
	c := connectIO{In: strings.NewReader(stdin), Out: out, Err: out, Getenv: os.Getenv}
	if srv != nil {
		u, _ := url.Parse(srv.URL)
		c.Client = &http.Client{Transport: redirect{u}}
	}
	return c, out
}

func savedKey(t *testing.T, name string) (string, bool) {
	t.Helper()
	c, ok, err := credentials.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return c.APIKey, ok
}

func TestConnectAcceptedSavesAndWritesConfigWithoutKey(t *testing.T) {
	srv, auth := modelsServer(t, 200)
	c, out := connectEnv(t, srv, cliKey+"\n1\n")
	if err := runConnect([]string{"deepseek"}, c); err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if *auth != "Bearer "+cliKey {
		t.Errorf("auth = %q", *auth)
	}
	if k, ok := savedKey(t, "deepseek"); !ok || k != cliKey {
		t.Fatal("key not saved")
	}
	if strings.Contains(out.String(), cliKey) {
		t.Errorf("output echoes the key:\n%s", out)
	}
	if !strings.Contains(out.String(), "sk-…abcd") {
		t.Errorf("output lacks the masked key:\n%s", out)
	}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a := cfg.Agents["pm"]; a.Runner != "deepseek" || a.Model != "deepseek-v4-flash" {
		t.Errorf("pm = %+v", a)
	}
	if cfg.Agents["coder"].Runner != "" {
		t.Error("lead-only choice changed another role")
	}
	home, _ := os.UserHomeDir()
	raw, _ := os.ReadFile(filepath.Join(home, ".decsr", "config.yaml"))
	if strings.Contains(string(raw), cliKey) {
		t.Error("key written to config.yaml")
	}
}

func TestConnectAllRolesNeedsModelPickWhenNoDefault(t *testing.T) {
	srv, _ := modelsServer(t, 200)
	c, out := connectEnv(t, srv, cliKey+"\n2\n2\n")
	if err := runConnect([]string{"groq"}, c); err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	cfg, _ := config.Load(t.TempDir())
	for _, r := range []string{"pm", "coder", "qa"} {
		if a := cfg.Agents[r]; a.Runner != "groq" || a.Model != "m-two" {
			t.Errorf("%s = %+v", r, a)
		}
	}
}

func TestConnectRejectedIsNotSaved(t *testing.T) {
	srv, _ := modelsServer(t, 401)
	c, out := connectEnv(t, srv, cliKey+"\n")
	err := runConnect([]string{"openai"}, c)
	if err == nil || !strings.Contains(err.Error(), "key rejected") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := savedKey(t, "openai"); ok {
		t.Error("rejected key was saved")
	}
	if strings.Contains(out.String()+err.Error(), cliKey) {
		t.Error("key in output")
	}
}

func TestConnectNoTestSkipsTheRequest(t *testing.T) {
	srv, auth := modelsServer(t, 401)
	c, _ := connectEnv(t, srv, cliKey+"\n3\n")
	if err := runConnect([]string{"openai", "--no-test"}, c); err != nil {
		t.Fatal(err)
	}
	if *auth != "" {
		t.Error("a request was made with --no-test")
	}
	if _, ok := savedKey(t, "openai"); !ok {
		t.Error("not saved")
	}
}

func TestConnectNetworkErrorPrompt(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c, out := connectEnv(t, dead, cliKey+"\nn\n")
	if err := runConnect([]string{"deepseek"}, c); err == nil {
		t.Fatal("expected not saved")
	}
	if !strings.Contains(out.String(), "Save it anyway? [y/N]") || strings.Contains(out.String(), cliKey) {
		t.Errorf("output:\n%s", out)
	}
	if _, ok := savedKey(t, "deepseek"); ok {
		t.Error("saved after N")
	}
	c, _ = connectEnv(t, dead, cliKey+"\ny\n3\n")
	if err := runConnect([]string{"deepseek"}, c); err != nil {
		t.Fatal(err)
	}
	if _, ok := savedKey(t, "deepseek"); !ok {
		t.Error("not saved after y")
	}
}

func TestConnectCustomValidation(t *testing.T) {
	cases := []struct{ name, url, wantErr string }{
		{"Bad_Name", "https://x.example/v1", "invalid name"},
		{"deepseek", "https://x.example/v1", "built-in"},
		{"acme", "http://x.example/v1", "https"},
		{"acme", "ftp://x", "https"},
	}
	for _, tc := range cases {
		c, _ := connectEnv(t, nil, cliKey+"\n")
		err := runConnect([]string{"custom", "--name", tc.name, "--base-url", tc.url}, c)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s %s: err = %v", tc.name, tc.url, err)
		}
	}
}

func TestConnectCustomLocalHTTPAndConfig(t *testing.T) {
	srv, _ := modelsServer(t, 200)
	c, out := connectEnv(t, nil, "acme\n"+srv.URL+"\n"+cliKey+"\n1\n1\n")
	if err := runConnect([]string{"custom"}, c); err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	cred, ok, _ := credentials.Get("acme")
	if !ok || cred.BaseURL != srv.URL || cred.APIKey != cliKey {
		t.Fatalf("cred = %+v", cred)
	}
	cfg, _ := config.Load(t.TempDir())
	if a := cfg.Agents["pm"]; a.Runner != "openai" || a.Provider != "acme" || a.Model != "m-one" || a.BaseURL != "" {
		t.Errorf("pm = %+v", a)
	}
}

func TestConnectListAndDisconnect(t *testing.T) {
	c, out := connectEnv(t, nil, "")
	if err := credentials.Set("deepseek", credentials.Cred{APIKey: cliKey}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GROQ_API_KEY", "gsk-envkey-000000000")
	c.Getenv = os.Getenv
	if err := runConnect([]string{"--list"}, c); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "sk-…abcd") || !strings.Contains(s, "env (GROQ_API_KEY)") || strings.Contains(s, cliKey) || strings.Contains(s, "gsk-envkey") {
		t.Errorf("list:\n%s", s)
	}

	c.In = strings.NewReader("n\n")
	if err := runDisconnect([]string{"deepseek"}, c); err != nil {
		t.Fatal(err)
	}
	if _, ok := savedKey(t, "deepseek"); !ok {
		t.Error("deleted despite n")
	}
	if err := runDisconnect([]string{"deepseek", "--yes"}, c); err != nil {
		t.Fatal(err)
	}
	if _, ok := savedKey(t, "deepseek"); ok {
		t.Error("not deleted")
	}
	if err := runDisconnect([]string{"nope", "--yes"}, c); err == nil {
		t.Error("unknown name accepted")
	}
}

func TestConnectUnknownProvider(t *testing.T) {
	c, _ := connectEnv(t, nil, "")
	if err := runConnect([]string{"bogus"}, c); err == nil {
		t.Error("expected error")
	}
	if err := runConnect(nil, c); err == nil {
		t.Error("expected usage error")
	}
}

func TestConnectHiddenTTYInput(t *testing.T) {
	srv, _ := modelsServer(t, 200)
	c, out := connectEnv(t, srv, "3\n")
	c.IsTTY = true
	c.ReadSecret = func() (string, error) { return cliKey, nil }
	if err := runConnect([]string{"openrouter"}, c); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), cliKey) {
		t.Errorf("key echoed:\n%s", out)
	}
	if _, ok := savedKey(t, "openrouter"); !ok {
		t.Error("not saved")
	}
}
