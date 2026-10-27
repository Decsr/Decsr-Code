package index

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestBuildFingerprintsAndDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bravo"), 0o644); err != nil {
		t.Fatal(err)
	}

	prev, err := BuildFingerprints(dir, []string{"a.txt", "b.txt"})
	if err != nil {
		t.Fatal(err)
	}

	// Identical inputs → empty diff.
	added, modified, deleted := DiffFingerprints(prev, prev)
	if len(added)+len(modified)+len(deleted) != 0 {
		t.Errorf("expected empty diff, got +%v ~%v -%v", added, modified, deleted)
	}

	// Modify a.txt and add c.txt; delete b.txt.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("charlie"), 0o644); err != nil {
		t.Fatal(err)
	}
	curr, _ := BuildFingerprints(dir, []string{"a.txt", "c.txt"})
	added, modified, deleted = DiffFingerprints(prev, curr)
	sort.Strings(added)
	sort.Strings(modified)
	sort.Strings(deleted)
	if len(modified) != 1 || modified[0] != "a.txt" {
		t.Errorf("modified mismatch: %v", modified)
	}
	if len(added) != 1 || added[0] != "c.txt" {
		t.Errorf("added mismatch: %v", added)
	}
	if len(deleted) != 1 || deleted[0] != "b.txt" {
		t.Errorf("deleted mismatch: %v", deleted)
	}
}

func TestSaveLoadFingerprints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "fp.json")
	fp := map[string]string{"x": "abc", "y": "def"}
	if err := SaveFingerprints(path, fp); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFingerprints(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded["x"] != "abc" || loaded["y"] != "def" {
		t.Errorf("round-trip mismatch: %v", loaded)
	}

	// Missing file → empty map, no error.
	missing, err := LoadFingerprints(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Errorf("expected empty map for missing file, got %v", missing)
	}
}

func TestEmbedMeta_RoundTripAndCompat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "meta.json")
	if _, ok := LoadMeta(p); ok {
		t.Fatal("missing meta must report !ok")
	}
	m := EmbedMeta{Model: "openai:a", Dim: 4}
	if err := SaveMeta(p, m); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadMeta(p)
	if !ok || got != m {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if !got.Compatible(EmbedMeta{Model: "openai:a", Dim: 4}) {
		t.Error("same meta should be compatible")
	}
	if got.Compatible(EmbedMeta{Model: "openai:b", Dim: 4}) {
		t.Error("model change must be incompatible")
	}
	if got.Compatible(EmbedMeta{Model: "openai:a", Dim: 8}) {
		t.Error("dim change must be incompatible")
	}
}
