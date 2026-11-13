package plugins

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func TestCatalogEntriesValid(t *testing.T) {
	cat := Catalog()
	if len(cat) < 20 {
		t.Fatalf("catalog has %d entries, want at least 20", len(cat))
	}
	seen := map[string]bool{}
	for i, e := range cat {
		if !config.ValidName(e.Name) {
			t.Errorf("%q: invalid plugin name", e.Name)
		}
		if seen[e.Name] {
			t.Errorf("%q listed twice", e.Name)
		}
		seen[e.Name] = true
		if i > 0 && cat[i-1].Name >= e.Name {
			t.Errorf("catalog not sorted at %q", e.Name)
		}
		if !validRepo(e.Repo) {
			t.Errorf("%s: invalid repo %q", e.Name, e.Repo)
		}
		if e.Path != "" && !filepath.IsLocal(e.Path) {
			t.Errorf("%s: path %q is not local", e.Name, e.Path)
		}
		if e.Description == "" || strings.ContainsAny(e.Description, "\n\x1b") {
			t.Errorf("%s: bad description %q", e.Name, e.Description)
		}
		for _, s := range e.Skills {
			if !config.ValidName(s) {
				t.Errorf("%s: invalid skill name %q", e.Name, s)
			}
		}
		for _, a := range e.Agents {
			if !config.ValidName(a) {
				t.Errorf("%s: invalid agent name %q", e.Name, a)
			}
		}
	}
	// The plugins of scripts/install-plugins.sh are all there.
	for _, n := range []string{"superpowers", "ui-ux-pro-max", "modern-cpp", "modern-python", "insecure-defaults", "differential-review", "feature-dev", "pr-review-toolkit"} {
		if !seen[n] {
			t.Errorf("catalog lacks %s", n)
		}
	}
}

func TestFindCatalog(t *testing.T) {
	e, ok := FindCatalog("modern-cpp")
	if !ok || e.Repo != "trailofbits/skills" || e.Path != "plugins/modern-cpp" {
		t.Fatalf("modern-cpp = %+v ok=%v", e, ok)
	}
	if e.URL() != "https://github.com/trailofbits/skills" {
		t.Errorf("URL = %q", e.URL())
	}
	if _, ok := FindCatalog("no-such-plugin"); ok {
		t.Error("unknown name found")
	}
	if _, ok := FindCatalog(""); ok {
		t.Error("empty name found")
	}
}

func TestCatalogCopyIsIndependent(t *testing.T) {
	a := Catalog()
	a[0].Name = "changed"
	a[0].Skills = append(a[0].Skills, "x")
	if Catalog()[0].Name == "changed" {
		t.Error("Catalog returns shared storage")
	}
}

func TestSetFindAndState(t *testing.T) {
	_, proj := setup(t)
	mkPlugin(t, proj, "on", `{"name":"on"}`)
	mkPlugin(t, proj, "off", `{"name":"off"}`)
	if err := SetEnabled("off", false); err != nil {
		t.Fatal(err)
	}
	set := Load(proj)
	if got := set.State("on"); got != StateEnabled {
		t.Errorf("on = %v", got)
	}
	if got := set.State("off"); got != StateDisabled {
		t.Errorf("off = %v", got)
	}
	if got := set.State("none"); got != StateMissing {
		t.Errorf("none = %v", got)
	}
}
