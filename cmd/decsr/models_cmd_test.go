package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/ranking"
)

const aaList = `{"data":[{"name":"Claude Opus 5.5 (Max)","slug":"a","evaluations":{"artificial_analysis_intelligence_index":80},
"artificial_analysis_intelligence_index_cost":{"cost_per_task":{"total_cost":10}},"performance_v3":{"time_per_task_seconds":900}},
{"name":"Mystery Model","slug":"m"}]}`

func aaTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "aa-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(aaList))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func modelsOpts(t *testing.T, srv *httptest.Server, key string) ranking.RunOptions {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(ranking.KeyEnv, "")
	o := ranking.RunOptions{Key: key, Path: filepath.Join(home, "rankings.json"), Connected: map[string]bool{"claude": true}}
	if srv != nil {
		o.BaseURL = srv.URL
	}
	return o
}

func TestModelsRankTable(t *testing.T) {
	srv := aaTestServer(t)
	var out bytes.Buffer
	if err := runModels([]string{"rank"}, &out, modelsOpts(t, srv, "aa-key")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"MODEL", "BAL", "Claude Opus 5.5", "measured", "Requests used: 1", "Mystery Model", ranking.Attribution} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestModelsRankJSON(t *testing.T) {
	srv := aaTestServer(t)
	var out bytes.Buffer
	if err := runModels([]string{"rank", "--json"}, &out, modelsOpts(t, srv, "aa-key")); err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Candidates []map[string]any `json:"candidates"`
		Requests   int              `json:"requests"`
		Unmapped   []string         `json:"unmapped"`
		Attr       string           `json:"attribution"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if rep.Requests != 1 || len(rep.Candidates) == 0 || len(rep.Unmapped) != 1 || rep.Attr != ranking.Attribution {
		t.Fatalf("%+v", rep)
	}
}

func TestModelsRankNoKeyShowsFallback(t *testing.T) {
	var out bytes.Buffer
	if err := runModels([]string{"rank"}, &out, modelsOpts(t, nil, "")); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"decsr connect artificialanalysis", "claude-opus-5-5"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), ranking.Attribution) {
		t.Errorf("the fallback tiers carry no Artificial Analysis data, so no credit:\n%s", out.String())
	}
}

func TestModelsRankRejectedKeyFails(t *testing.T) {
	srv := aaTestServer(t)
	var out bytes.Buffer
	if err := runModels([]string{"rank"}, &out, modelsOpts(t, srv, "wrong")); err == nil {
		t.Fatal("a rejected key must fail")
	}
	if strings.Contains(out.String(), "wrong") {
		t.Fatal("the key must not be printed")
	}
}

func TestModelsUsage(t *testing.T) {
	if err := runModels(nil, &bytes.Buffer{}, ranking.RunOptions{}); err == nil {
		t.Fatal("usage expected")
	}
}

func TestConnectArtificialAnalysis(t *testing.T) {
	c, out := connectEnv(t, nil, "aa-secret-key-123\n")
	if err := runConnect([]string{"artificialanalysis"}, c); err != nil {
		t.Fatal(err)
	}
	if k, ok := savedKey(t, credentials.ArtificialAnalysis); !ok || k != "aa-secret-key-123" {
		t.Fatal("key not saved")
	}
	if strings.Contains(out.String(), "aa-secret-key-123") || strings.Contains(out.String(), "Use artificialanalysis for") {
		t.Fatalf("no key and no role question expected:\n%s", out.String())
	}
	var list bytes.Buffer
	c.Out = &list
	if err := connectList(c); err != nil || !strings.Contains(list.String(), "artificialanalysis") {
		t.Fatalf("--list shows it: %v\n%s", err, list.String())
	}
}
