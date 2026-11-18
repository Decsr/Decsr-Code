package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const marketJSON = `{"name":"m","plugins":[
	{"name":"rel","description":"Relative","source":"./plugins/rel"},
	{"name":"root","description":"Root","source":"./"},
	{"name":"gh","description":"GitHub","source":{"source":"github","repo":"acme/gh-plugin"}},
	{"name":"url","description":"Url","source":{"source":"url","url":"https://example.com/x.git"}},
	{"name":"escape","source":"../outside"},
	{"name":"abs","source":"/etc"},
	{"name":"bad name","source":"./x"},
	{"name":"badrepo","source":{"source":"github","repo":"not a repo"}},
	{"name":"rel","source":"./dup"},
	{"name":"nosource"}]}`

func TestParseMarketplace(t *testing.T) {
	got, skipped, err := ParseMarketplace("owner/market", []byte(marketJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]MarketEntry{
		"rel":  {Name: "rel", Description: "Relative", Repo: "owner/market", Path: "plugins/rel"},
		"root": {Name: "root", Description: "Root", Repo: "owner/market"},
		"gh":   {Name: "gh", Description: "GitHub", Repo: "acme/gh-plugin"},
	}
	if len(got) != len(want) {
		t.Fatalf("entries = %+v", got)
	}
	for _, e := range got {
		if w, ok := want[e.Name]; !ok || w != e {
			t.Errorf("entry %+v, want %+v", e, w)
		}
	}
	if skipped != 7 {
		t.Errorf("skipped = %d", skipped)
	}
}

func TestParseMarketplaceBadFile(t *testing.T) {
	for _, body := range []string{"", "{", "[]", `{"name":"x"}`, `{"plugins":"no"}`} {
		if _, _, err := ParseMarketplace("o/r", []byte(body)); err == nil {
			t.Errorf("%q parsed", body)
		}
	}
	if es, _, err := ParseMarketplace("o/r", []byte(`{"plugins":[]}`)); err != nil || len(es) != 0 {
		t.Errorf("empty marketplace: %v %v", es, err)
	}
}

func TestMarketplaceFileRoundTripAndTrust(t *testing.T) {
	home, _ := setup(t)
	if got := LoadMarketplaces(); got != nil {
		t.Fatalf("no file: %v", got)
	}
	ms := SetMarketplace(nil, Marketplace{Repo: "o/m", Entries: []MarketEntry{{Name: "a", Repo: "o/m", Path: "plugins/a"}, {Name: "bad name", Repo: "o/m"}}})
	ms = SetMarketplace(ms, Marketplace{Repo: "o/other"})
	if err := SaveMarketplaces(ms); err != nil {
		t.Fatal(err)
	}
	got := LoadMarketplaces()
	if len(got) != 2 || got[0].Repo != "o/m" || got[0].Trusted || len(got[0].Entries) != 1 {
		t.Fatalf("loaded %+v", got)
	}
	if err := TrustMarketplace("o/m"); err != nil {
		t.Fatal(err)
	}
	if err := TrustMarketplace("o/none"); err == nil {
		t.Error("trusted a marketplace that is not added")
	}
	got = LoadMarketplaces()
	if !got[0].Trusted {
		t.Error("trust not recorded")
	}
	// a refresh keeps the trust
	got = SetMarketplace(got, Marketplace{Repo: "o/m", Entries: nil})
	if !got[0].Trusted {
		t.Error("refresh dropped the trust")
	}
	if got = RemoveMarketplace(got, "o/m"); len(got) != 1 {
		t.Errorf("remove: %+v", got)
	}
	// a broken file lists nothing
	if err := os.WriteFile(filepath.Join(home, ".decsr", "marketplaces.yaml"), []byte("marketplaces: [oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	if LoadMarketplaces() != nil {
		t.Error("broken yaml listed")
	}
}

// marketUpstream makes a bare repo holding .claude-plugin/marketplace.json and
// plugins/modern-cpp.
func marketUpstream(t *testing.T) func(string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	work := filepath.Join(base, "work")
	write(t, filepath.Join(work, ".claude-plugin", "marketplace.json"),
		`{"name":"m","plugins":[{"name":"modern-cpp","description":"C++","source":"./plugins/modern-cpp"},{"name":"x","source":{"source":"url","url":"u"}}]}`)
	write(t, filepath.Join(work, "plugins", "modern-cpp", ".claude-plugin", "plugin.json"), `{"name":"modern-cpp"}`)
	write(t, filepath.Join(work, "plugins", "modern-cpp", "skills", "modern-cpp", "SKILL.md"), "---\ndescription: d\n---\nbody")
	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "v1")
	bare := filepath.Join(base, "bare.git")
	git(t, base, "clone", "-q", "--bare", work, bare)
	return func(string) string { return "file://" + bare }
}

func TestFetchMarketplaceAndInstallFromIt(t *testing.T) {
	home, proj := setup(t)
	url := marketUpstream(t)
	f, err := FetchMarketplace(context.Background(), "acme/market", url)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != 1 || f.Entries[0].Path != "plugins/modern-cpp" || f.Skipped != 1 {
		t.Fatalf("fetched %+v", f)
	}
	if _, err := FetchMarketplace(context.Background(), "not a repo", url); err == nil {
		t.Error("bad repo fetched")
	}
	entry := f.Entries[0]
	// not added, then added but untrusted: both refuse
	if err := InstallMarketplace(context.Background(), "acme/market", entry.Entry(), url); err == nil {
		t.Fatal("installed from a marketplace that is not added")
	}
	if err := SaveMarketplaces(SetMarketplace(nil, Marketplace{Repo: "acme/market", Entries: f.Entries})); err != nil {
		t.Fatal(err)
	}
	if err := InstallMarketplace(context.Background(), "acme/market", entry.Entry(), url); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted install = %v", err)
	}
	if err := TrustMarketplace("acme/market"); err != nil {
		t.Fatal(err)
	}
	// an entry the marketplace does not list is refused even when trusted
	forged := entry.Entry()
	forged.Path = "elsewhere"
	if err := InstallMarketplace(context.Background(), "acme/market", forged, url); err == nil {
		t.Error("forged entry installed")
	}
	if err := InstallMarketplace(context.Background(), "acme/market", entry.Entry(), url); err != nil {
		t.Fatal(err)
	}
	pl := find(Load(proj), "modern-cpp")
	if pl == nil || len(pl.Skills) != 1 {
		t.Fatalf("installed plugin: %+v (home %s)", pl, home)
	}
	// the curated catalog keeps its exact-entry rule
	if err := InstallWith(context.Background(), entry.Entry(), url); err == nil {
		t.Error("InstallWith accepted a marketplace entry that is not the catalog one")
	}
}
