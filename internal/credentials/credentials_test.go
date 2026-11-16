package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testKey = "sk-abcdef1234567890wxyz"

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), ".decsr")}
}

func TestSetGetRoundTripAndPerms(t *testing.T) {
	s := newStore(t)
	if err := s.Set("deepseek", Cred{APIKey: testKey}); err != nil {
		t.Fatal(err)
	}
	c, ok, err := s.Get("deepseek")
	if err != nil || !ok || c.APIKey != testKey || c.SavedAt.IsZero() {
		t.Fatalf("Get = %+v %v %v", c, ok, err)
	}
	if runtime.GOOS != "windows" {
		di, _ := os.Stat(s.Dir)
		fi, _ := os.Stat(s.Path())
		if di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
			t.Errorf("perms dir=%v file=%v", di.Mode().Perm(), fi.Mode().Perm())
		}
	}
	if _, ok, _ := s.Get("groq"); ok {
		t.Error("unknown provider found")
	}
}

func TestRefusesBroadPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes")
	}
	s := newStore(t)
	if err := s.Set("deepseek", Cred{APIKey: testKey}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Get("deepseek")
	var pe *PermError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Error("error leaks the key")
	}
	if err := s.Set("groq", Cred{APIKey: testKey}); err == nil {
		t.Error("Set must refuse a broad file too")
	}
	if _, err := s.List(); err == nil {
		t.Error("List must refuse a broad file")
	}
}

func TestListIsMaskedAndSorted(t *testing.T) {
	s := newStore(t)
	_ = s.Set("groq", Cred{APIKey: testKey})
	_ = s.Set("acme", Cred{APIKey: testKey, BaseURL: "https://x.example/v1"})
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].Name != "acme" {
		t.Fatalf("list = %+v %v", list, err)
	}
	if list[0].Masked != "sk-…wxyz" || list[0].BaseURL != "https://x.example/v1" {
		t.Errorf("entry = %+v", list[0])
	}
	if Mask("short") != "…" {
		t.Errorf("short key mask = %q", Mask("short"))
	}
}

func TestDeleteAndAtomicLeavesNoTemp(t *testing.T) {
	s := newStore(t)
	_ = s.Set("a", Cred{APIKey: testKey})
	_ = s.Set("b", Cred{APIKey: testKey})
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get("a"); ok {
		t.Error("a still present")
	}
	if _, ok, _ := s.Get("b"); !ok {
		t.Error("b lost")
	}
	if err := s.Delete("zzz"); err != nil {
		t.Errorf("delete unknown: %v", err)
	}
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if e.Name() != Filename {
			t.Errorf("stray file %s", e.Name())
		}
	}
}

func TestCorruptFileErrorHasNoKey(t *testing.T) {
	s := newStore(t)
	_ = os.MkdirAll(s.Dir, 0o700)
	_ = os.WriteFile(s.Path(), []byte(`{"providers":{"x":{"api_key":"`+testKey), 0o600)
	_, _, err := s.Get("x")
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestSetRejectsEmpty(t *testing.T) {
	s := newStore(t)
	if s.Set("", Cred{APIKey: "k"}) == nil || s.Set("a", Cred{}) == nil {
		t.Error("empty name or key accepted")
	}
}
