package index

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Decsr/Decsr-Code/internal/embedder"
)

// EmbedMeta identifies the embedding model that produced a persisted index.
// Vectors from different models (or dimensions) are not comparable.
type EmbedMeta struct {
	Model string `json:"model"`
	Dim   int    `json:"dim"`
}

// MetaFor describes emb. Embedders without a Name method are recorded as
// "unknown" and distinguished by dimension only.
func MetaFor(emb embedder.Embedder) EmbedMeta {
	m := EmbedMeta{Model: "unknown"}
	if emb == nil {
		return m
	}
	m.Dim = emb.Dim()
	if n, ok := emb.(interface{ Name() string }); ok {
		m.Model = n.Name()
	}
	return m
}

// Compatible reports whether vectors saved under m can be compared with
// vectors produced under cur. An unknown (zero) dimension on either side is
// not treated as a mismatch.
func (m EmbedMeta) Compatible(cur EmbedMeta) bool {
	if m.Model != cur.Model {
		return false
	}
	return m.Dim == 0 || cur.Dim == 0 || m.Dim == cur.Dim
}

// SaveMeta persists m to path (creating parent dirs as needed).
func SaveMeta(path string, m EmbedMeta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadMeta reads the metadata at path. ok is false when the file is missing
// or unreadable, meaning the originating model is unknown.
func LoadMeta(path string) (EmbedMeta, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- internal path under .decsr/
	if err != nil {
		return EmbedMeta{}, false
	}
	var m EmbedMeta
	if err := json.Unmarshal(data, &m); err != nil || m.Model == "" {
		return EmbedMeta{}, false
	}
	return m, true
}
