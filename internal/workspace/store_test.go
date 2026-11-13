package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	g := &Graph{Task: "do it", Title: "T", Nodes: []*Node{n("T1"), n("T2", "T1")}, NewRoles: []NewRole{{Name: "x", Description: "d"}}}
	g.Nodes[0].Role = "coder"
	g.Nodes[0].Status = StatusDone
	if err := Save(path, g); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", st, err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, g) {
		t.Fatalf("got %+v want %+v", got, g)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestLoadResetsRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	a := n("A")
	a.Status = StatusRunning
	if err := Save(path, &Graph{Nodes: []*Node{a}}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Nodes[0].Status != StatusTodo {
		t.Fatalf("status = %s", got.Nodes[0].Status)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), FileName))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadRejectsBadGraphs(t *testing.T) {
	cases := map[string]string{
		"nil node":   `{"nodes":[null]}`,
		"bad status": `{"nodes":[{"id":"T1","kind":"work","status":"weird"}]}`,
		"bad kind":   `{"nodes":[{"id":"T1","kind":"magic","status":"todo"}]}`,
		"cycle":      `{"nodes":[{"id":"T1","kind":"work","status":"todo","depends_on":["T2"]},{"id":"T2","kind":"work","status":"todo","depends_on":["T1"]}]}`,
		"empty id":   `{"nodes":[{"id":"","kind":"work","status":"todo"}]}`,
	}
	for name, body := range cases {
		path := filepath.Join(t.TempDir(), FileName)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		g, err := Load(path)
		if err == nil || g != nil {
			t.Errorf("%s: g=%v err=%v", name, g, err)
		}
	}
}
