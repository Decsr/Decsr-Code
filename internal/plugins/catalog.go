package plugins

import (
	_ "embed"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"github.com/Decsr/Decsr-Code/internal/config"
)

//go:embed catalog.json
var catalogJSON []byte

// CatalogEntry is one plugin decsr knows how to install: a directory of a
// GitHub repository.
type CatalogEntry struct {
	Name        string   `json:"name"`
	Repo        string   `json:"repo"` // GitHub owner/repo
	Path        string   `json:"path"` // directory inside the repo, "" for the root
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
	Agents      []string `json:"agents"`
	Tags        []string `json:"tags"`
}

// URL is the GitHub page of the repository.
func (e CatalogEntry) URL() string { return "https://github.com/" + e.Repo }

var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validRepo(s string) bool { return repoRe.MatchString(s) }

var (
	catalogOnce sync.Once
	catalog     []CatalogEntry
)

// Catalog returns the embedded list of installable plugins, sorted by name.
// An entry with an unusable name, repo or path is left out. The result is a
// copy: callers may change it.
func Catalog() []CatalogEntry {
	catalogOnce.Do(func() {
		var all []CatalogEntry
		if err := json.Unmarshal(catalogJSON, &all); err != nil {
			return
		}
		seen := map[string]bool{}
		for _, e := range all {
			if !config.ValidName(e.Name) || seen[e.Name] || !validRepo(e.Repo) {
				continue
			}
			if e.Path != "" && !filepath.IsLocal(e.Path) {
				continue
			}
			seen[e.Name] = true
			catalog = append(catalog, e)
		}
		sort.Slice(catalog, func(i, j int) bool { return catalog[i].Name < catalog[j].Name })
	})
	out := make([]CatalogEntry, len(catalog))
	for i, e := range catalog {
		e.Skills = append([]string(nil), e.Skills...)
		e.Agents = append([]string(nil), e.Agents...)
		e.Tags = append([]string(nil), e.Tags...)
		out[i] = e
	}
	return out
}

// FindCatalog returns the catalog entry with the given plugin name.
func FindCatalog(name string) (CatalogEntry, bool) {
	for _, e := range Catalog() {
		if e.Name == name {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// State is how a plugin stands in a Set.
type State int

const (
	StateMissing State = iota // not installed
	StateDisabled
	StateEnabled
)

// State reports whether the plugin called name is installed and enabled.
func (s Set) State(name string) State {
	for _, p := range s.Plugins {
		if p.Name == name {
			if p.Enabled {
				return StateEnabled
			}
			return StateDisabled
		}
	}
	return StateMissing
}
