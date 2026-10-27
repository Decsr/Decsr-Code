package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

type flakyEmbedder struct{ fail bool }

func (f *flakyEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail {
		return nil, errors.New("boom")
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}
func (f *flakyEmbedder) Dim() int { return 3 }

func TestIndexFile_RetriesEmbeddingAfterFailure(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "memory")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("hello retry world"), 0o600); err != nil {
		t.Fatal(err)
	}
	emb := &flakyEmbedder{fail: true}
	st, err := Open(filepath.Join(dir, "m.db"), root, Options{Embedder: emb})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if _, err := st.IndexFile(ctx, "a.md"); err == nil {
		t.Fatal("expected embed error")
	}
	emb.fail = false
	re, err := st.IndexFile(ctx, "a.md")
	if err != nil || !re {
		t.Fatalf("second IndexFile = %v, %v; want reindexed", re, err)
	}
	stats, _ := st.Stats(ctx)
	if stats.Embeddings == 0 {
		t.Fatal("embeddings missing after retry")
	}
}

func TestRerank_MissingVectorIsNeutral(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "memory")
	_ = os.MkdirAll(root, 0o750)
	_ = os.WriteFile(filepath.Join(root, "a.md"), []byte("zebracorn alpha"), 0o600)
	st, err := Open(filepath.Join(dir, "m.db"), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if _, err := st.IndexFile(ctx, "a.md"); err != nil { // no embedder: no vectors
		t.Fatal(err)
	}
	st.emb = fakeEmbedder{}
	hits, err := st.Search(ctx, "zebracorn", SearchOptions{K: 3, HybridAlpha: 0.5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("hits=%v err=%v", hits, err)
	}
	if hits[0].Score != hits[0].BM25 {
		t.Errorf("score = %v, want BM25 %v for chunk without vector", hits[0].Score, hits[0].BM25)
	}
}

func TestCapChars_RuneSafe(t *testing.T) {
	s := strings.Repeat("việt日本😀", 50)
	for n := 1; n < 60; n++ {
		if got := capChars(s, n); !utf8.ValidString(got) {
			t.Fatalf("capChars(n=%d) invalid utf8", n)
		}
	}
}

func TestTailTokens_RuneSafe(t *testing.T) {
	s := strings.Repeat("việt日本😀", 50)
	for n := 1; n < 20; n++ {
		if got := tailTokens(s, n); !utf8.ValidString(got) {
			t.Fatalf("tailTokens(n=%d) invalid utf8", n)
		}
	}
}

func TestChunkMarkdown_IgnoresHeadingsInFences(t *testing.T) {
	doc := "# Title\nintro\n```bash\n# not a heading\necho hi\n```\nmore\n"
	segs := splitByHeadings(doc)
	if len(segs) != 1 {
		t.Fatalf("got %d segments, want 1: %+v", len(segs), segs)
	}
}
