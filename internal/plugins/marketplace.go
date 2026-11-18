package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Decsr/Decsr-Code/internal/config"
)

const (
	marketplaceFile = "marketplaces.yaml"
	marketManifest  = ".claude-plugin/marketplace.json"
	maxMarketBytes  = 2 << 20
)

// MarketEntry is a plugin a marketplace lists: a directory of a GitHub
// repository, like a catalog entry.
type MarketEntry struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Repo        string `yaml:"repo"`
	Path        string `yaml:"path,omitempty"`
}

// Marketplace is a GitHub repository with .claude-plugin/marketplace.json the
// user added. Its plugins are not trusted until the user says so.
type Marketplace struct {
	Repo      string        `yaml:"repo"`
	Trusted   bool          `yaml:"trusted"`
	FetchedAt time.Time     `yaml:"fetched_at"`
	Entries   []MarketEntry `yaml:"entries,omitempty"`
}

// Entry is the catalog form of e, which the installer takes.
func (e MarketEntry) Entry() CatalogEntry {
	return CatalogEntry{Name: e.Name, Repo: e.Repo, Path: e.Path, Description: e.Description}
}

// ValidRepo reports whether s is a GitHub owner/repo.
func ValidRepo(s string) bool { return validRepo(s) }

func marketplacePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, config.GlobalDir, marketplaceFile)
}

// LoadMarketplaces reads ~/.decsr/marketplaces.yaml, sorted by repo. A missing
// or broken file gives none; entries that are not usable are dropped.
func LoadMarketplaces() []Marketplace {
	p := marketplacePath()
	if p == "" {
		return nil
	}
	data, ok, _ := readFileMax(p, maxMarketBytes)
	if !ok {
		return nil
	}
	var file struct {
		Marketplaces []Marketplace `yaml:"marketplaces"`
	}
	if yaml.Unmarshal(data, &file) != nil {
		return nil
	}
	var out []Marketplace
	seen := map[string]bool{}
	for _, m := range file.Marketplaces {
		if !validRepo(m.Repo) || seen[m.Repo] {
			continue
		}
		seen[m.Repo] = true
		m.Entries = cleanEntries(m.Entries)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// SaveMarketplaces writes the list to ~/.decsr/marketplaces.yaml.
func SaveMarketplaces(ms []Marketplace) error {
	p := marketplacePath()
	if p == "" {
		return errors.New("home directory not found")
	}
	data, err := yaml.Marshal(struct {
		Marketplaces []Marketplace `yaml:"marketplaces"`
	}{ms})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// FindMarketplace returns the marketplace with the given repo.
func FindMarketplace(ms []Marketplace, repo string) (Marketplace, bool) {
	for _, m := range ms {
		if m.Repo == repo {
			return m, true
		}
	}
	return Marketplace{}, false
}

// SetMarketplace adds m or replaces the one with the same repo. A replaced
// marketplace keeps its trust.
func SetMarketplace(ms []Marketplace, m Marketplace) []Marketplace {
	for i := range ms {
		if ms[i].Repo == m.Repo {
			m.Trusted = ms[i].Trusted
			ms[i] = m
			return ms
		}
	}
	return append(ms, m)
}

// RemoveMarketplace drops the marketplace with the given repo.
func RemoveMarketplace(ms []Marketplace, repo string) []Marketplace {
	var out []Marketplace
	for _, m := range ms {
		if m.Repo != repo {
			out = append(out, m)
		}
	}
	return out
}

// TrustMarketplace marks the marketplace with the given repo as trusted and
// saves the list.
func TrustMarketplace(repo string) error {
	ms := LoadMarketplaces()
	for i := range ms {
		if ms[i].Repo == repo {
			ms[i].Trusted = true
			return SaveMarketplaces(ms)
		}
	}
	return fmt.Errorf("marketplace %s is not added", repo)
}

func cleanEntries(in []MarketEntry) []MarketEntry {
	var out []MarketEntry
	seen := map[string]bool{}
	for _, e := range in {
		if !config.ValidName(e.Name) || seen[e.Name] || !validRepo(e.Repo) {
			continue
		}
		if e.Path != "" && !filepath.IsLocal(e.Path) {
			continue
		}
		e.Description = sanitize(e.Description)
		seen[e.Name] = true
		out = append(out, e)
	}
	return out
}

// ParseMarketplace reads the marketplace.json of the repository repo. A
// plugin whose source is a relative path inside the repo, or {"source":
// "github","repo":"owner/repo"}, is listed; skipped counts the others (other
// source kinds, bad names or paths). A file that is not a marketplace is an
// error.
func ParseMarketplace(repo string, data []byte) (entries []MarketEntry, skipped int, err error) {
	var file struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Source      json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, 0, errors.New("marketplace.json is not valid JSON")
	}
	if file.Plugins == nil {
		return nil, 0, errors.New("marketplace.json lists no plugins")
	}
	for _, p := range file.Plugins {
		e := MarketEntry{Name: p.Name, Description: p.Description}
		var rel string
		var obj struct {
			Source string `json:"source"`
			Repo   string `json:"repo"`
		}
		switch {
		case json.Unmarshal(p.Source, &rel) == nil:
			rel = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(rel)), "./")
			if rel == "." {
				rel = ""
			}
			e.Repo, e.Path = repo, rel
		case json.Unmarshal(p.Source, &obj) == nil && obj.Source == "github":
			e.Repo = obj.Repo
		}
		if e.Repo == "" {
			continue
		}
		entries = append(entries, e)
	}
	entries = cleanEntries(entries)
	return entries, len(file.Plugins) - len(entries), nil
}

// MarketFetch is the result of reading a marketplace.
type MarketFetch struct {
	Entries []MarketEntry
	Skipped int
}

// FetchMarketplace shallow-clones the repository into a temporary directory,
// reads its marketplace.json and removes the clone. Nothing in it is run.
// url maps owner/repo to the clone URL.
func FetchMarketplace(ctx context.Context, repo string, url func(repo string) string) (MarketFetch, error) {
	if !validRepo(repo) {
		return MarketFetch{}, fmt.Errorf("%q is not owner/repo", sanitize(repo))
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	tmp, err := os.MkdirTemp("", "decsr-market-*")
	if err != nil {
		return MarketFetch{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	clone := filepath.Join(tmp, "repo")
	if err := fetchRepo(ctx, clone, url(repo)); err != nil {
		return MarketFetch{}, err
	}
	data, found, err := readFileMax(filepath.Join(clone, marketManifest), maxMarketBytes)
	if err != nil {
		return MarketFetch{}, err
	}
	if !found {
		return MarketFetch{}, fmt.Errorf("%s has no %s", repo, marketManifest)
	}
	entries, skipped, err := ParseMarketplace(repo, data)
	if err != nil {
		return MarketFetch{}, fmt.Errorf("%s: %w", repo, err)
	}
	return MarketFetch{Entries: entries, Skipped: skipped}, nil
}

// InstallMarketplace installs a plugin of a trusted marketplace: e must be
// exactly an entry the saved marketplace repo lists.
func InstallMarketplace(ctx context.Context, repo string, e CatalogEntry, url func(repo string) string) error {
	m, ok := FindMarketplace(LoadMarketplaces(), repo)
	if !ok || !m.Trusted {
		return fmt.Errorf("marketplace %s is not trusted", sanitize(repo))
	}
	for _, l := range m.Entries {
		if l.Name == e.Name && l.Repo == e.Repo && l.Path == e.Path {
			return install(ctx, l.Entry(), url)
		}
	}
	return fmt.Errorf("plugin %q is not listed by %s", sanitize(e.Name), repo)
}

// readFileMax is readFile with its own size cap.
func readFileMax(path string, limit int64) ([]byte, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false, nil
	}
	if fi.Size() > limit {
		return nil, false, fmt.Errorf("%s is larger than %d KB", filepath.Base(path), limit/1024)
	}
	data, err := os.ReadFile(path) // #nosec G304 -- regular file of the user's decsr folder or of a temporary clone
	return data, err == nil, err
}

// Remove deletes the plugin called name from ~/.decsr/plugins or the project's
// .decsr/plugins, and its line in the disabled file.
func Remove(projectRoot, name string) error {
	for _, p := range Load(projectRoot).Plugins {
		if p.Name != name {
			continue
		}
		parent := filepath.Dir(p.Dir)
		if parent != filepath.Join(projectRoot, config.GlobalDir, "plugins") && parent != Dir() {
			return fmt.Errorf("%s is outside the plugin folders", name)
		}
		if !isDir(p.Dir) {
			return fmt.Errorf("%s is not a plugin folder", name)
		}
		if err := os.RemoveAll(p.Dir); err != nil {
			return err
		}
		if readDisabled()[name] {
			return SetEnabled(name, true)
		}
		return nil
	}
	return fmt.Errorf("no plugin named %q", sanitize(name))
}
