package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// ── parser ───────────────────────────────────────────────────────────────────

func TestParsePluginArgs(t *testing.T) {
	ok := []struct {
		in   string
		want pluginCmd
	}{
		{"", pluginCmd{}},
		{"  ", pluginCmd{}},
		{"list", pluginCmd{verb: "list"}},
		{"reload", pluginCmd{verb: "reload"}},
		{"stats", pluginCmd{verb: "stats"}},
		{"available", pluginCmd{verb: "available"}},
		{"search modern c++", pluginCmd{verb: "search", arg: "modern c++"}},
		{"info tb", pluginCmd{verb: "info", arg: "tb"}},
		{"install tb", pluginCmd{verb: "install", arg: "tb"}},
		{"enable tb", pluginCmd{verb: "enable", arg: "tb"}},
		{"disable tb", pluginCmd{verb: "disable", arg: "tb"}},
		{"remove tb", pluginCmd{verb: "remove", arg: "tb"}},
		{"import tb", pluginCmd{verb: "import", arg: "tb"}},
		{"marketplace add acme/skills", pluginCmd{verb: "market-add", arg: "acme/skills"}},
		{"marketplace remove acme/skills", pluginCmd{verb: "market-remove", arg: "acme/skills"}},
	}
	for _, c := range ok {
		got, err := parsePluginArgs(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q = %+v, %v; want %+v", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"list x", "search", "info", "info a b", "install", "enable a b", "remove", "import", "reload now",
		"marketplace", "marketplace add", "marketplace add notarepo", "marketplace add a/b/c", "marketplace add ../x", "marketplace list", "marketplace remove a b", "frobnicate"} {
		if _, err := parsePluginArgs(in); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
}

// ── data ─────────────────────────────────────────────────────────────────────

func TestDiscoverRowsAndFilter(t *testing.T) {
	setHome(t)
	curated := plugins.Catalog()
	markets := []plugins.Marketplace{
		{Repo: "acme/m", Entries: []plugins.MarketEntry{
			{Name: curated[0].Name, Repo: "acme/m"}, // the catalog wins
			{Name: "acme-lint", Description: "Lints Rust crates", Repo: "acme/m", Path: "p/lint"},
		}},
		{Repo: "zed/m", Trusted: true, Entries: []plugins.MarketEntry{{Name: "acme-lint", Repo: "zed/m"}, {Name: "zed-fmt", Description: "Formats", Repo: "zed/m"}}},
	}
	rows := discoverRows(markets)
	if len(rows) != len(curated)+2 {
		t.Fatalf("%d rows, catalog %d", len(rows), len(curated))
	}
	lint, ok := findDiscover(rows, "acme-lint")
	if !ok || lint.market != "acme/m" || lint.trusted {
		t.Errorf("acme-lint = %+v", lint)
	}
	if fmtRow, _ := findDiscover(rows, "zed-fmt"); !fmtRow.trusted || fmtRow.market != "zed/m" {
		t.Errorf("zed-fmt = %+v", fmtRow)
	}
	if first, _ := findDiscover(rows, curated[0].Name); first.market != "" || !first.trusted {
		t.Errorf("catalog entry = %+v", first)
	}
	if got := filterDiscover(rows, "RUST lint"); len(got) != 1 || got[0].e.Name != "acme-lint" {
		t.Errorf("filter = %+v", got)
	}
	if got := filterDiscover(rows, "  "); len(got) != len(rows) {
		t.Error("blank filter keeps all")
	}
	if got := filterDiscover(rows, "zzzz-no-such"); len(got) != 0 {
		t.Errorf("no match = %d", len(got))
	}
	if got := filterDiscover(rows, "zed/m"); len(got) != 1 {
		t.Errorf("filter by marketplace = %d", len(got))
	}
}

func TestPluginStatsAggregation(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	node := func(id, role string, skills []string, in, out int, usd float64, known bool, ended time.Time) *workspace.Node {
		return &workspace.Node{ID: id, Role: role, Skills: skills, Kind: workspace.KindWork, Status: workspace.StatusDone,
			InputTokens: in, OutputTokens: out, CostUSD: usd, CostKnown: known, EndedAt: ended}
	}
	ss := []workspace.Session{
		{ID: "a", UpdatedAt: now, Graph: &workspace.Graph{Nodes: []*workspace.Node{
			node("T1", "coder", []string{"sp:tdd", "sp:debug", "cpp:modern"}, 1000, 500, 0.5, true, ago(2*time.Hour)),
			node("T2", "sp:reviewer", nil, 200, 0, 0.1, true, ago(26*time.Hour)),
			node("T3", "coder", []string{"sp:tdd"}, 100, 100, 0, false, ago(time.Hour)),
			{ID: "T4", Role: "coder", Skills: []string{"sp:tdd"}, Kind: workspace.KindWork, Status: workspace.StatusTodo}, // never ran
			node("T5", "coder", []string{"plain-skill"}, 50, 50, 0.1, true, ago(time.Hour)),
		}}},
		{ID: "b", UpdatedAt: ago(40 * 24 * time.Hour), Graph: &workspace.Graph{Nodes: []*workspace.Node{
			node("T1", "coder", []string{"cpp:modern"}, 10, 10, 0.01, true, ago(40*24*time.Hour)),
		}}},
		{ID: "empty"},
	}
	got := buildPluginStats(ss)
	sp, cpp := got["sp"], got["cpp"]
	if sp == nil || cpp == nil || len(got) != 2 {
		t.Fatalf("stats = %v", got)
	}
	if sp.nodes != 3 || sp.use.in != 1300 || sp.use.out != 600 {
		t.Errorf("sp = %+v", sp)
	}
	if !strings.HasPrefix(sp.use.costCell(), "≥") { // T3 has no price
		t.Errorf("sp cost = %q", sp.use.costCell())
	}
	if !sp.last.Equal(ago(time.Hour)) {
		t.Errorf("sp last = %v", sp.last)
	}
	if cpp.nodes != 2 || !cpp.last.Equal(ago(2*time.Hour)) {
		t.Errorf("cpp = %+v", cpp)
	}
}

func TestLastUsedCell(t *testing.T) {
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local)
	use := func(d time.Duration) *pluginUse { return &pluginUse{last: now.Add(-d)} }
	cases := []struct {
		u       *pluginUse
		enabled bool
		want    string
		warn    bool
	}{
		{nil, true, "never used", false},
		{&pluginUse{}, true, "never used", false},
		{use(10 * time.Second), true, "used just now", false},
		{use(30 * time.Minute), true, "used 30m ago", false},
		{use(2 * time.Hour), true, "used 2h ago", false},
		{use(14 * time.Hour), true, "used today", false}, // 01:00
		{use(20 * time.Hour), true, "used yesterday", false},
		{use(5 * 24 * time.Hour), true, "used 5d ago", false},
		{use(34 * 24 * time.Hour), true, "unused 34d ⚠", true},
		{use(34 * 24 * time.Hour), false, "used 34d ago", false},
		{use(29 * 24 * time.Hour), true, "used 29d ago", false},
	}
	for _, c := range cases {
		got, warn := lastUsedCell(c.u, now, c.enabled)
		if got != c.want || warn != c.warn {
			t.Errorf("%+v enabled=%v = %q,%v; want %q,%v", c.u, c.enabled, got, warn, c.want, c.warn)
		}
	}
	morning := time.Date(2026, 11, 18, 3, 0, 0, 0, time.Local)
	if got, _ := lastUsedCell(&pluginUse{last: morning.Add(-time.Minute)}, time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local), true); got != "used today" {
		t.Errorf("12h ago the same day = %q", got)
	}
}

// ── panel ────────────────────────────────────────────────────────────────────

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// claudeFixture writes a Claude Code plugin "cc-tool" with a skill and a role.
func claudeFixture(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins", "cache", "mk", "cc-tool", "1.0")
	for rel, body := range map[string]string{
		".claude-plugin/plugin.json": `{"name":"cc-tool","version":"1.0"}`,
		"skills/dig/SKILL.md":        "---\ndescription: Digs\n---\nbody",
		"agents/digger.md":           "---\ndescription: Digs deep\n---\nyou dig",
	} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	idx := fmt.Sprintf(`{"version":2,"plugins":{"cc-tool@mk":[{"scope":"user","installPath":%q,"version":"1.0"}]}}`, dir)
	p := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	if err := os.WriteFile(p, []byte(idx), 0o600); err != nil {
		t.Fatal(err)
	}
}

func pluginPanelHarness(t *testing.T) *wsHarness {
	t.Helper()
	home := setHome(t)
	wsWritePlugin(t, home, "tb", map[string]string{"skills/audit/SKILL.md": "---\ndescription: Audit\n---\nbody"})
	wsWritePlugin(t, home, "off-one", map[string]string{"skills/a/SKILL.md": "---\ndescription: A\n---\nbody", "skills/b/SKILL.md": "---\ndescription: B\n---\nbody"})
	if err := plugins.SetEnabled("off-one", false); err != nil {
		t.Fatal(err)
	}
	claudeFixture(t, home)
	h := newWSHarnessAt(t, t.TempDir())
	h.size(120, 40)
	h.toGraph()
	h.key("tab")
	return h
}

func TestPluginPanelInstalledTab(t *testing.T) {
	h := pluginPanelHarness(t)
	h.openPanelCmd("/plugin")
	if h.m.overlay != wsOverlayPanel || h.m.panel.kind != panelPlugin {
		t.Fatalf("no plugin panel:\n%s", h.view())
	}
	for _, w := range []string{"Installed", "Discover", "Marketplaces", "Stats", "● tb", "○ off-one", "◌ cc-tool", "decsr", "claude",
		"1 skill", "2 skills", "never used", "not loaded · i to import", "off-one · off · Space turns it on"} {
		h.want(w)
	}
	// Space turns the plugin under the cursor (off-one) on and off again
	h.key(" ")
	h.want("plugin off-one enabled")
	h.want("● off-one")
	h.key(" ")
	h.want("plugin off-one disabled")
	h.want("○ off-one")
	h.key("down")
	h.want("tb · 1 skill: audit")
	// i on a plugin of Claude Code imports it
	h.key("down")
	h.key("i")
	h.want("imported cc-tool from Claude Code")
	h.want("hooks and MCP servers stay unused")
	if !h.m.hasPlugin("cc-tool") {
		t.Fatal("not loaded after the import")
	}
	h.runCmd(h.m.pluginDataCmd(false))
	h.wantNot("◌ cc-tool")
	if _, ok := workspace.FindRole(h.m.cfg, "cc-tool:digger"); !ok {
		t.Error("the role of the imported plugin is not offered")
	}
}

func TestPluginPanelRemoveAsksFirst(t *testing.T) {
	h := pluginPanelHarness(t)
	h.openPanelCmd("/plugin")
	h.key("x")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "rmplugin" || !strings.HasSuffix(h.m.confirm.text, "y/n") {
		t.Fatalf("no question:\n%s", h.view())
	}
	h.key("n")
	if h.m.overlay != wsOverlayPanel || !h.m.hasPlugin("off-one") {
		t.Fatal("n must return to the panel and keep the plugin")
	}
	h.key("x")
	h.key("y")
	if h.m.overlay != wsOverlayPanel {
		t.Fatalf("y must return to the panel: %v", h.m.overlay)
	}
	h.want("plugin off-one removed")
	if h.m.hasPlugin("off-one") {
		t.Error("plugin still loaded")
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".decsr", "plugins", "off-one")); err == nil {
		t.Error("folder left on disk")
	}
	// a plugin of Claude Code cannot be removed from here
	h.key("down", "x")
	h.want("belongs to Claude Code")
}

func TestPluginPanelDiscoverFiltersAndInstalls(t *testing.T) {
	h := pluginPanelHarness(t)
	fake := &fakeInstall{t: t}
	h.m.deps.installPlugin = fake.install
	h.openPanelCmd("/plugin")
	h.key("tab")
	all := len(discoverRows(nil))
	h.want(fmt.Sprintf("%d of %d", all, all))
	h.key("m", "o", "d", "e", "r", "n", "-", "c")
	h.want("1 of ")
	h.want("modern-cpp")
	h.wantNot("modern-python")
	if h.m.panel.ps.Filter != "modern-c" {
		t.Errorf("filter = %q", h.m.panel.ps.Filter)
	}
	h.key("backspace")
	if h.m.panel.ps.Filter != "modern-" {
		t.Errorf("backspace: %q", h.m.panel.ps.Filter)
	}
	h.key("c")
	cmd := h.key("enter")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "plugin" {
		t.Fatalf("no install question:\n%s", h.view())
	}
	cmd = h.key("y")
	h.send(runInstall(t, cmd))
	if !reflect.DeepEqual(fake.calls, []string{"modern-cpp"}) {
		t.Fatalf("installs = %v", fake.calls)
	}
	if h.m.overlay != wsOverlayPanel {
		t.Errorf("overlay after the install = %v", h.m.overlay)
	}
	h.want("plugin modern-cpp installed")
	h.want("✓ installed")
	// Esc clears the filter first, then closes
	h.key("esc")
	if h.m.panel.ps.Filter != "" || h.m.overlay != wsOverlayPanel {
		t.Fatal("first Esc clears the filter")
	}
	h.key("esc")
	if h.m.overlay != wsOverlayNone {
		t.Fatal("second Esc closes")
	}
}

func TestPluginPanelMarketplacesAddRefreshRemove(t *testing.T) {
	h := pluginPanelHarness(t)
	var fetched []string
	h.m.deps.fetchMarket = func(_ context.Context, repo string) (plugins.MarketFetch, error) {
		fetched = append(fetched, repo)
		if repo == "bad/one" {
			return plugins.MarketFetch{}, errors.New("bad/one has no .claude-plugin/marketplace.json")
		}
		return plugins.MarketFetch{Skipped: 2, Entries: []plugins.MarketEntry{{Name: "acme-lint", Description: "Lints", Repo: repo, Path: "p/lint"}}}, nil
	}
	h.openPanelCmd("/plugin")
	h.key("tab", "tab")
	h.want("no marketplaces added")
	h.key("a")
	if !h.m.panel.inputing {
		t.Fatal("a opens the entry line")
	}
	h.want("Marketplace (owner/repo):")
	for _, r := range "not-a-repo" {
		h.key(string(r))
	}
	h.key("enter")
	h.want("type owner/repo")
	h.key("a")
	for _, r := range "acme/market" {
		h.key(string(r))
	}
	h.runCmd(h.key("enter"))
	h.want("marketplace acme/market added: 1 plugin (2 skipped")
	h.want("⚠ untrusted")
	h.want("1 plugin")
	if got := plugins.LoadMarketplaces(); len(got) != 1 || got[0].Repo != "acme/market" || got[0].Trusted || len(got[0].Entries) != 1 {
		t.Fatalf("saved = %+v", got)
	}
	// r refreshes and keeps the trust
	if err := plugins.TrustMarketplace("acme/market"); err != nil {
		t.Fatal(err)
	}
	h.m.panel.markets = plugins.LoadMarketplaces()
	h.runCmd(h.key("r"))
	h.want("refreshed")
	if got := plugins.LoadMarketplaces(); !got[0].Trusted || len(fetched) != 2 {
		t.Errorf("after refresh: %+v, fetched %v", got, fetched)
	}
	// a failing fetch is one error line and changes nothing
	h.key("a")
	for _, r := range "bad/one" {
		h.key(string(r))
	}
	h.runCmd(h.key("enter"))
	h.want("has no .claude-plugin/marketplace.json")
	if len(plugins.LoadMarketplaces()) != 1 {
		t.Error("a failed fetch added a marketplace")
	}
	// x asks, n keeps, y removes
	h.key("x")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "rmmarket" {
		t.Fatalf("no question:\n%s", h.view())
	}
	h.key("n")
	if len(plugins.LoadMarketplaces()) != 1 || h.m.overlay != wsOverlayPanel {
		t.Fatal("n keeps the marketplace")
	}
	h.key("x", "y")
	h.want("marketplace acme/market removed")
	if len(plugins.LoadMarketplaces()) != 0 {
		t.Error("marketplace file still lists it")
	}
}

func TestPluginTrustFlow(t *testing.T) {
	h := pluginPanelHarness(t)
	fake := &fakeMarketInstall{t: t}
	h.m.deps.installMarket = fake.install
	if err := plugins.SaveMarketplaces([]plugins.Marketplace{{Repo: "acme/market", Entries: []plugins.MarketEntry{{Name: "acme-lint", Description: "Lints Rust", Repo: "acme/market", Path: "p/lint"}}}}); err != nil {
		t.Fatal(err)
	}
	h.openPanelCmd("/plugin")
	h.key("tab")
	h.key("a", "c", "m", "e", "-")
	h.want("⚠ untrusted")
	h.want("acme/market")
	h.key("enter")
	if h.m.confirm.kind != "trust" {
		t.Fatalf("an untrusted marketplace must ask for trust first:\n%s", h.view())
	}
	wantText(t, h.m.confirm.text, "Trust acme/market? Plugins can add skills, roles and commands that agents will follow. y/n")
	h.key("n")
	if plugins.LoadMarketplaces()[0].Trusted || len(fake.calls) != 0 {
		t.Fatal("n must trust nothing")
	}
	h.key("enter", "y")
	if !plugins.LoadMarketplaces()[0].Trusted {
		t.Fatal("y records the trust")
	}
	if h.m.confirm.kind != "mplugin" || h.m.overlay != wsOverlayConfirm {
		t.Fatalf("the install question follows:\n%s", h.view())
	}
	wantText(t, h.m.confirm.text, "Install plugin acme-lint from github.com/acme/market (marketplace acme/market)? y/n")
	cmd := h.key("y")
	h.send(runInstall(t, cmd))
	if !reflect.DeepEqual(fake.calls, []string{"acme/market:acme-lint:p/lint"}) {
		t.Fatalf("installs = %v", fake.calls)
	}
	h.want("plugin acme-lint installed")
	// the second install from the marketplace asks no trust question
	h.m.panel.markets = plugins.LoadMarketplaces()
	h.key("backspace", "backspace", "backspace", "backspace", "backspace")
	h.key("a", "c", "m", "e", "-")
	h.wantNot("⚠ untrusted")
}

type fakeMarketInstall struct {
	t     *testing.T
	calls []string
}

func (f *fakeMarketInstall) install(_ context.Context, repo string, e plugins.CatalogEntry) error {
	f.calls = append(f.calls, repo+":"+e.Name+":"+e.Path)
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".decsr", "plugins", e.Name)
	for rel, body := range map[string]string{".claude-plugin/plugin.json": `{"name":"` + e.Name + `"}`, "skills/s1/SKILL.md": "---\ndescription: d\n---\nbody"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	return nil
}

func TestPluginPanelStatsTab(t *testing.T) {
	h := pluginPanelHarness(t)
	now := h.m.now()
	s := workspace.NewSession()
	s.Graph = &workspace.Graph{Nodes: []*workspace.Node{
		{ID: "T1", Title: "A", Status: workspace.StatusDone, Role: "coder", Skills: []string{"tb:audit"}, Kind: workspace.KindWork, InputTokens: 800, OutputTokens: 200, CostUSD: 0.2, CostKnown: true, EndedAt: now.Add(-2 * time.Hour)},
		{ID: "T2", Title: "B", Status: workspace.StatusDone, Role: "tb:auditor", Kind: workspace.KindWork, InputTokens: 500, OutputTokens: 0, CostUSD: 0.1, CostKnown: true, EndedAt: now.Add(-3 * time.Hour)},
	}}
	s.UpdatedAt = now
	if err := s.Save(h.root); err != nil {
		t.Fatal(err)
	}
	h.openPanelCmd("/plugin")
	h.want("used 2h ago") // the last used column of the Installed tab
	h.key("tab", "tab", "tab")
	for _, w := range []string{"PLUGIN", "NODES", "TOKENS", "COST", "LAST USED", "tb", "1,500", "$0.30", "never used"} {
		h.want(w)
	}
	// /plugin stats prints the same numbers as one line
	h.key("esc")
	h.m.input.SetValue("/plugin stats")
	h.runCmd(h.key("enter"))
	h.want("1 of 2 plugins used · 2 nodes · 1,500 tokens · $0.30")
}

func TestPluginDirectForms(t *testing.T) {
	h := pluginPanelHarness(t)
	fake := &fakeInstall{t: t}
	h.m.deps.installPlugin = fake.install
	h.typeCmd("/plugin list")
	h.want("2 plugins: off-one (off), tb")
	h.typeCmd("/plugin search c++")
	h.want("modern-cpp")
	h.typeCmd("/plugin search zzzz-none")
	h.want("no plugin matches")
	h.typeCmd("/plugin info tb")
	h.want("tb 1.2.3 · decsr (user) · on · 1 skill")
	h.typeCmd("/plugin info off-one")
	h.want("off-one 1.2.3 · decsr (user) · off · 2 skills")
	h.typeCmd("/plugin info cc-tool")
	h.want("Claude Code (mk) · not loaded")
	h.typeCmd("/plugin info modern-cpp")
	h.want("not installed · curated catalog")
	h.typeCmd("/plugin info nothing-here")
	h.want("no plugin named")
	h.typeCmd("/plugin available")
	h.want("/plugin search")
	h.typeCmd("/plugin enable off-one")
	h.want("plugin off-one enabled")
	h.typeCmd("/plugin disable off-one")
	h.want("plugin off-one disabled")
	h.typeCmd("/plugin disable nope")
	h.want("no plugin named")
	h.typeCmd("/plugin import cc-tool")
	h.want("imported cc-tool from Claude Code: 1 skill · 1 role")
	h.typeCmd("/plugin import cc-tool")
	h.want("already installed")
	h.typeCmd("/plugin import nothing")
	h.want("Claude Code has no plugin named")
	h.typeCmd("/plugin remove tb")
	if h.m.overlay != wsOverlayConfirm || h.m.confirm.kind != "rmplugin" {
		t.Fatalf("remove asks first:\n%s", h.view())
	}
	h.key("y")
	h.want("plugin tb removed")
	h.typeCmd("/plugin remove tb")
	h.want("no plugin named")
	h.typeCmd("/plugin reload")
	h.want("plugins reloaded: 2 found")
	h.typeCmd("/plugin marketplace remove acme/none")
	h.want("not added")
	h.typeCmd("/plugin marketplace add nonsense")
	h.want("is not owner/repo")
	h.typeCmd("/plugin install modern-cpp extra")
	h.want("usage: /plugin")
	h.m.deps.fetchMarket = func(_ context.Context, repo string) (plugins.MarketFetch, error) {
		return plugins.MarketFetch{Entries: []plugins.MarketEntry{{Name: "acme-lint", Repo: repo}}}, nil
	}
	h.m.input.SetValue("/plugin marketplace add acme/market")
	h.runCmd(h.key("enter"))
	h.want("marketplace acme/market added: 1 plugin")
	h.typeCmd("/plugin marketplace add acme/market")
	h.want("already added")
	h.typeCmd("/plugin search lint")
	h.want("acme-lint")
	h.typeCmd("/plugin marketplace remove acme/market")
	h.want("marketplace acme/market removed")
}

func TestPluginManagerRefusesWhileNodesRun(t *testing.T) {
	h := pluginPanelHarness(t)
	h.m.deps.installPlugin = (&fakeInstall{t: t}).install
	if err := h.eng.Assign("T1", "coder"); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.key("tab", "r")
	if h.m.running() == 0 {
		t.Fatal("node did not start")
	}
	h.key("tab")
	for _, line := range []string{"/plugin disable tb", "/plugin remove tb", "/plugin import cc-tool", "/plugin reload"} {
		h.typeCmd(line)
		h.want(msgStopFirst)
	}
	h.eng.StopAll()
}

func TestPluginPanelWithoutClaudeCodeOrSessions(t *testing.T) {
	setHome(t)
	h := newWSHarnessAt(t, t.TempDir())
	h.size(80, 24)
	h.openPanelCmd("/plugin")
	h.want("no plugins installed")
	h.key("tab")
	h.want("Discover")
	h.key("tab")
	h.want("no marketplaces added")
	h.key("tab")
	h.want("no plugins installed")
}
