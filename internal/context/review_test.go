package context

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Decsr/Decsr-Code/internal/embedder"
	"github.com/Decsr/Decsr-Code/internal/index"
)

func TestBuildIndex_CorruptGobIsRebuilt(t *testing.T) {
	fake := &fakeEmbedder{}
	dir, cfg := setupSemanticRepo(t, fake)
	if _, err := Collect(dir, cfg); err != nil {
		t.Fatal(err)
	}
	gob := filepath.Join(dir, indexSubdir, indexFile)
	if err := os.WriteFile(gob, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	pc, err := Collect(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := pc.SemanticSearch(context.Background(), "func Authenticate(user, pass string) bool { return true }", 3)
	if err != nil || len(hits) == 0 {
		t.Fatalf("expected rebuilt index to return hits, got %v %v", hits, err)
	}
}

func TestBuildIndex_MissingGobIsRebuilt(t *testing.T) {
	fake := &fakeEmbedder{}
	dir, cfg := setupSemanticRepo(t, fake)
	if _, err := Collect(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, indexSubdir, indexFile)); err != nil {
		t.Fatal(err)
	}
	pc, err := Collect(dir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := pc.SemanticSearch(context.Background(), "func Render() string { return \"home\" }", 5)
	if len(hits) == 0 {
		t.Fatal("expected hits after rebuilding from missing gob")
	}
}

type namedEmbedder struct {
	fakeEmbedder
	name string
}

func (n *namedEmbedder) Name() string { return n.name }

func TestBuildIndex_ModelChangeRebuilds(t *testing.T) {
	a := &namedEmbedder{name: "m:a"}
	dir, cfg := setupSemanticRepo(t, &a.fakeEmbedder)
	_ = cfg
	files := []string{"auth/login.go", "home/home.go"}
	if _, err := buildOrRefreshIndex(dir, files, a); err != nil {
		t.Fatal(err)
	}
	before := a.calls
	if _, err := buildOrRefreshIndex(dir, files, a); err != nil {
		t.Fatal(err)
	}
	if a.calls != before {
		t.Fatal("unchanged model must not re-embed")
	}
	b := &namedEmbedder{name: "m:b"}
	if _, err := buildOrRefreshIndex(dir, files, b); err != nil {
		t.Fatal(err)
	}
	if b.calls == 0 {
		t.Fatal("model change must trigger full re-embed")
	}
}

func TestBuildIndex_LegacyWithoutMetaRebuildsOnce(t *testing.T) {
	a := &namedEmbedder{name: "m:a"}
	dir, _ := setupSemanticRepo(t, &a.fakeEmbedder)
	files := []string{"auth/login.go", "home/home.go"}
	if _, err := buildOrRefreshIndex(dir, files, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, indexSubdir, metaFileName)); err != nil {
		t.Fatal(err)
	}
	before := a.calls
	if _, err := buildOrRefreshIndex(dir, files, a); err != nil {
		t.Fatal(err)
	}
	if a.calls == before {
		t.Fatal("missing meta must trigger a rebuild")
	}
	before = a.calls
	_, _ = buildOrRefreshIndex(dir, files, a)
	if a.calls != before {
		t.Fatal("second run after rebuild must be incremental")
	}
}

type failOnBig struct{ fakeEmbedder }

func (f *failOnBig) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	for _, s := range texts {
		if strings.Contains(s, "POISON") {
			return nil, errors.New("too large")
		}
	}
	return f.fakeEmbedder.Embed(ctx, texts)
}

func TestBuildIndex_FailingFileIsSkippedAndRetried(t *testing.T) {
	dir, _ := setupSemanticRepo(t, &fakeEmbedder{})
	if err := os.WriteFile(filepath.Join(dir, "bad.js"), []byte("POISON"), 0o600); err != nil {
		t.Fatal(err)
	}
	emb := &failOnBig{}
	files := []string{"auth/login.go", "bad.js", "home/home.go"}
	idx, err := buildOrRefreshIndex(dir, files, embedder.Embedder(emb))
	if err != nil {
		t.Fatalf("one failing file must not abort the build: %v", err)
	}
	hits, _ := idx.Search(context.Background(), "func Render() string { return \"home\" }", 10)
	if len(hits) == 0 {
		t.Fatal("other files should still be indexed")
	}
	fp, _ := index.LoadFingerprints(filepath.Join(dir, indexSubdir, fingerprintsFileName))
	if _, ok := fp["bad.js"]; ok {
		t.Error("failed file must not be fingerprinted so it is retried")
	}
	if _, ok := fp["auth/login.go"]; !ok {
		t.Error("successful file should be fingerprinted")
	}
}

func TestSelectChunksWithinBudget_HugeFirstChunkTruncated(t *testing.T) {
	content := []byte(strings.Repeat("日本語😀việt ", 5000))
	out := selectChunksWithinBudget("bundle.js", content, maxSourceFileSize)
	if len(out) > maxSourceFileSize+100 {
		t.Fatalf("len=%d, want near budget %d", len(out), maxSourceFileSize)
	}
	if !utf8.ValidString(out) {
		t.Fatal("invalid utf8")
	}
}

func TestSystemPromptFragment_DeterministicAndUTF8(t *testing.T) {
	long := strings.Repeat("việt日本😀", 2000)
	p := ProjectContext{
		IsBrownfield:  true,
		SourceFiles:   map[string]string{"b.go": long, "a.go": "x", "c.go": "y", "d.go": "z"},
		AlwaysInclude: map[string]string{"B": long, "A": long, "C": "c"},
		UnstagedDiff:  long,
	}
	first := p.SystemPromptFragment()
	if !utf8.ValidString(first) {
		t.Fatal("fragment is not valid utf8")
	}
	for i := 0; i < 20; i++ {
		if p.SystemPromptFragment() != first {
			t.Fatal("fragment order is not deterministic")
		}
	}
}

func TestCollectSourceFiles_SkipsHugeFiles(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxFileBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	_ = os.WriteFile(filepath.Join(dir, "bundle.js"), big, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "ok.js"), []byte("let a = 1\n"), 0o600)
	got := collectSourceFiles(dir, []string{"bundle.js", "ok.js"}, nil)
	if _, ok := got["bundle.js"]; ok {
		t.Error("huge file should be skipped")
	}
	if _, ok := got["ok.js"]; !ok {
		t.Error("small file should be kept")
	}
}
