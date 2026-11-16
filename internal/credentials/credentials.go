// Package credentials stores API keys for hosted providers in
// ~/.decsr/credentials.json (directory 0700, file 0600). A key is never
// logged, printed or included in an error; List only returns masked keys.
package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// Dirname is the directory under the home directory, shared with the config.
	Dirname = ".decsr"
	// Filename is the credentials file inside the directory.
	Filename = "credentials.json"
	// DisplayPath is how the file is shown to the user.
	DisplayPath = "~/.decsr/credentials.json"

	version = 1
)

// Cred is one saved provider. BaseURL is only set for custom providers.
type Cred struct {
	APIKey  string    `json:"api_key"`
	BaseURL string    `json:"base_url,omitempty"`
	SavedAt time.Time `json:"saved_at"`
}

// Entry is the key-free view of a saved provider.
type Entry struct {
	Name    string
	BaseURL string
	Masked  string
	SavedAt time.Time
}

type fileData struct {
	Version   int             `json:"version"`
	Providers map[string]Cred `json:"providers"`
}

// Store is a credentials file location. The zero value uses the home directory.
type Store struct {
	// Dir is the directory holding credentials.json; "" means ~/.decsr.
	Dir string
}

// Default returns the store under the current user's home directory.
func Default() Store { return Store{} }

func (s Store) dir() (string, error) {
	if s.Dir != "" {
		return s.Dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("credentials: home directory unknown: %w", err)
	}
	return filepath.Join(home, Dirname), nil
}

// Path is the full path of the credentials file ("" when the home is unknown).
func (s Store) Path() string {
	d, err := s.dir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, Filename)
}

// PermError reports a credentials file that is readable by others.
type PermError struct{ Path string }

func (e *PermError) Error() string {
	return fmt.Sprintf("credentials file %s is accessible by other users — run: chmod 600 %s", e.Path, e.Path)
}

// CheckPerms returns a *PermError when the file exists with a mode broader
// than 0600 (always nil on Windows and when the file does not exist).
func (s Store) CheckPerms() error {
	p := s.Path()
	if p == "" || runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return &PermError{Path: p}
	}
	return nil
}

func (s Store) load() (fileData, error) {
	fd := fileData{Version: version, Providers: map[string]Cred{}}
	p := s.Path()
	if p == "" {
		return fd, errors.New("credentials: home directory unknown")
	}
	if err := s.CheckPerms(); err != nil {
		return fd, err
	}
	data, err := os.ReadFile(p) // #nosec G304 — fixed file under the user's directory
	if errors.Is(err, os.ErrNotExist) {
		return fd, nil
	}
	if err != nil {
		return fd, fmt.Errorf("credentials: read %s: %w", p, err)
	}
	if err := json.Unmarshal(data, &fd); err != nil {
		return fd, fmt.Errorf("credentials: %s is not valid JSON (the key is not shown)", p)
	}
	if fd.Providers == nil {
		fd.Providers = map[string]Cred{}
	}
	return fd, nil
}

func (s Store) save(fd fileData) error {
	d, err := s.dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return fmt.Errorf("credentials: create %s: %w", d, err)
	}
	fd.Version = version
	data, err := json.MarshalIndent(fd, "", "  ")
	if err != nil {
		return errors.New("credentials: encode failed")
	}
	tmp, err := os.CreateTemp(d, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("credentials: write in %s: %w", d, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("credentials: chmod: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("credentials: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("credentials: write: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(d, Filename)); err != nil {
		cleanup()
		return fmt.Errorf("credentials: replace file: %w", err)
	}
	return nil
}

// Get returns the saved entry for name. A file with loose permissions is
// refused with a *PermError.
func (s Store) Get(name string) (Cred, bool, error) {
	fd, err := s.load()
	if err != nil {
		return Cred{}, false, err
	}
	c, ok := fd.Providers[name]
	if !ok || c.APIKey == "" {
		return Cred{}, false, nil
	}
	return c, true, nil
}

// Set saves c under name, stamping SavedAt when it is zero.
func (s Store) Set(name string, c Cred) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("credentials: provider name is empty")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("credentials: API key is empty")
	}
	fd, err := s.load()
	if err != nil {
		return err
	}
	if c.SavedAt.IsZero() {
		c.SavedAt = time.Now().UTC()
	}
	fd.Providers[name] = c
	return s.save(fd)
}

// Delete removes name; deleting an unknown name is not an error.
func (s Store) Delete(name string) error {
	fd, err := s.load()
	if err != nil {
		return err
	}
	if _, ok := fd.Providers[name]; !ok {
		return nil
	}
	delete(fd.Providers, name)
	return s.save(fd)
}

// List returns the saved providers sorted by name, with masked keys only.
func (s Store) List() ([]Entry, error) {
	fd, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(fd.Providers))
	for name, c := range fd.Providers {
		out = append(out, Entry{Name: name, BaseURL: c.BaseURL, Masked: Mask(c.APIKey), SavedAt: c.SavedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Mask shows the first 3 and last 4 characters of a key ("sk-…a1b2"). Keys
// too short to hide anything are fully masked.
func Mask(key string) string {
	r := []rune(key)
	if len(r) < 12 {
		return "…"
	}
	return string(r[:3]) + "…" + string(r[len(r)-4:])
}

// Get, Set, Delete and List use the default store.
func Get(name string) (Cred, bool, error) { return Default().Get(name) }
func Set(name string, c Cred) error       { return Default().Set(name, c) }
func Delete(name string) error            { return Default().Delete(name) }
func List() ([]Entry, error)              { return Default().List() }
