package context

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/chunker"
	"github.com/Decsr/Decsr-Code/internal/embedder"
	"github.com/Decsr/Decsr-Code/internal/index"
	"github.com/Decsr/Decsr-Code/internal/safefile"
)

// indexSubdir is the workspace-relative directory holding the persisted index.
const (
	indexSubdir          = ".decsr/index"
	indexFile            = "chunks.gob"
	fingerprintsFileName = "fingerprints.json"
	metaFileName         = "embed-meta.json"
)

// SemanticSearch returns deduplicated repository-relative file paths for the
// top-K chunk hits matching query. When the index is disabled or unavailable,
// it returns (nil, nil).
func (p ProjectContext) SemanticSearch(ctx context.Context, query string, k int) ([]string, error) {
	if p.idx == nil || k <= 0 {
		return nil, nil
	}
	hits, err := p.idx.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(hits))
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		if _, dup := seen[h.Path]; dup {
			continue
		}
		seen[h.Path] = struct{}{}
		out = append(out, h.Path)
	}
	return out, nil
}

// SemanticSearchSymbols returns the top-K chunk hits grouped as a
// file → declaration-name map, suitable as symbol targets for scoped source
// rendering. Whole-file chunks (empty name) are skipped because they carry no
// symbol to scope to. Names are deduplicated per file while preserving
// score order. Returns (nil, nil) when the index is disabled or unavailable.
func (p ProjectContext) SemanticSearchSymbols(ctx context.Context, query string, k int) (map[string][]string, error) {
	if p.idx == nil || k <= 0 {
		return nil, nil
	}
	hits, err := p.idx.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}
	targets := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, h := range hits {
		name := strings.TrimSpace(h.ChunkName)
		if name == "" {
			continue
		}
		if seen[h.Path] == nil {
			seen[h.Path] = make(map[string]bool)
		}
		if seen[h.Path][name] {
			continue
		}
		seen[h.Path][name] = true
		targets[h.Path] = append(targets[h.Path], name)
	}
	if len(targets) == 0 {
		return nil, nil
	}
	return targets, nil
}

// buildOrRefreshIndex creates/updates a persistent semantic index over the
// project's source files. It only re-embeds files whose SHA-256 changed since
// the previous run. Returns nil when indexing is disabled or fails — callers
// must treat the index as best-effort.
func buildOrRefreshIndex(root string, files []string, emb embedder.Embedder) (index.Index, error) {
	idxPath := filepath.Join(root, indexSubdir, indexFile)
	fpPath := filepath.Join(root, indexSubdir, fingerprintsFileName)

	metaPath := filepath.Join(root, indexSubdir, metaFileName)

	idx := index.New(emb)
	curMeta := index.MetaFor(emb)
	prev, _ := index.LoadFingerprints(fpPath)
	savedMeta, haveMeta := index.LoadMeta(metaPath)
	// A missing/corrupt index, or one built by a different (or unknown)
	// embedding model, cannot be diffed against: drop the fingerprints so
	// every file is re-embedded into a fresh index.
	if loadErr := idx.Load(idxPath); loadErr != nil || (len(prev) > 0 && (!haveMeta || !savedMeta.Compatible(curMeta))) {
		prev = map[string]string{}
		idx = index.New(emb)
	}

	curr, err := index.BuildFingerprints(root, withinSizeLimit(root, indexableFiles(files)))
	if err != nil {
		return idx, err
	}
	added, modified, deleted := index.DiffFingerprints(prev, curr)

	idx.Remove(append(deleted, modified...))

	toIndex := append(added, modified...)
	sort.Strings(toIndex)

	ctx := context.Background()
	for _, path := range toIndex {
		data, err := safefile.ReadFile(root, path)
		if err != nil {
			continue
		}
		chunks, err := chunker.Split(path, data)
		if err != nil || len(chunks) == 0 {
			continue
		}
		paths := make([]string, len(chunks))
		for i := range chunks {
			paths[i] = path
		}
		if err := idx.Add(ctx, chunks, paths); err != nil {
			// Skip this file and leave it out of the saved fingerprints so
			// the next run retries it; don't fail the whole build.
			delete(curr, path)
			continue
		}
	}

	if err := idx.Persist(idxPath); err != nil {
		return idx, err
	}
	if err := index.SaveMeta(metaPath, index.MetaFor(emb)); err != nil {
		return idx, err
	}
	if err := index.SaveFingerprints(fpPath, curr); err != nil {
		return idx, err
	}
	return idx, nil
}

// withinSizeLimit drops files larger than maxFileBytes (generated bundles,
// vendored blobs) so they are neither read nor embedded. Files that cannot be
// stat'ed are kept; the later read reports the real error.
func withinSizeLimit(root string, files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if st, err := os.Stat(filepath.Join(root, f)); err == nil && st.Size() > maxFileBytes {
			continue
		}
		out = append(out, f)
	}
	return out
}

func indexableFiles(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if isSourceFile(f) {
			out = append(out, f)
		}
	}
	return out
}
