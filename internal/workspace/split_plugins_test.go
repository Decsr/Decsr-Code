package workspace

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
)

// splitOpts has the plugin "sp" installed (skills tdd, debug), "off"
// installed but disabled, and a catalog with sp, off, modern-cpp and csharp.
func splitOpts(t *testing.T) SplitOptions {
	t.Helper()
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	mkHomePlugin(t, home, "sp", map[string]string{
		"skills/tdd/SKILL.md":   "---\ndescription: Test first\n---\nbody",
		"skills/debug/SKILL.md": "---\ndescription: " + strings.Repeat("long ", 60) + "\n---\nbody",
	})
	mkHomePlugin(t, home, "off", map[string]string{"skills/x/SKILL.md": "---\ndescription: x\n---\nbody"})
	if err := plugins.SetEnabled("off", false); err != nil {
		t.Fatal(err)
	}
	return SplitOptions{Plugins: plugins.Load(root), Catalog: []plugins.CatalogEntry{
		{Name: "sp", Repo: "o/sp", Description: "Superpowers", Skills: []string{"tdd", "debug"}},
		{Name: "off", Repo: "o/off", Description: "Disabled one", Skills: []string{"x"}},
		{Name: "modern-cpp", Repo: "o/mc", Description: "Modern C++", Skills: []string{"modern-cpp"}},
		{Name: "csharp", Repo: "o/cs", Description: "C# helpers", Skills: []string{"dotnet", "nuget"}},
	}}
}

func TestSplitPromptListsSkillsAndCatalog(t *testing.T) {
	opts := splitOpts(t)
	p := buildSplitPrompt("task", testRoles(), "ctx", nil, 0, opts)
	for _, want := range []string{
		"## Installed skills", "- sp:tdd: Test first", "- sp:debug: long long",
		"## Plugins you can suggest", "- modern-cpp: Modern C++ Skills: modern-cpp.", "- off (disabled): Disabled one Skills: x.",
		"- csharp: C# helpers Skills: dotnet, nuget.",
		"===PLUGINS===", `"skills":[]`, "never give a", "at most 5",
	} {
		if !strings.Contains(strings.ToLower(p), strings.ToLower(want)) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	// Installed and enabled plugins are not offered again; disabled skills are not listed.
	if strings.Contains(p, "- sp: Superpowers") || strings.Contains(p, "- off:x") || strings.Contains(p, "off:x:") {
		t.Errorf("prompt offers an installed plugin:\n%s", p)
	}
	// The description is cut to about 120 characters.
	for _, l := range strings.Split(p, "\n") {
		if strings.HasPrefix(l, "- sp:debug") && len([]rune(l)) > len("- sp:debug: ")+121 {
			t.Errorf("description not cut: %d runes", len([]rune(l)))
		}
	}
	if strings.Contains(p, "{{") {
		t.Error("unfilled placeholder")
	}
}

func TestSplitPromptEmptyPluginSections(t *testing.T) {
	p := BuildSplitPrompt("task", testRoles(), "ctx")
	if !strings.Contains(p, "(none installed)") || !strings.Contains(p, "\n(none)\n") {
		t.Errorf("empty sections missing:\n%s", p)
	}
}

func TestSplitPromptCapsSkillLines(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	files := map[string]string{}
	for i := 0; i < 70; i++ {
		files["skills/s"+string(rune('a'+i/26))+string(rune('a'+i%26))+"/SKILL.md"] = "---\ndescription: d\n---\nbody"
	}
	mkHomePlugin(t, home, "big", files)
	p := buildSplitPrompt("t", testRoles(), "", nil, 0, SplitOptions{Plugins: plugins.Load(root)})
	if n := strings.Count(p, "\n- big:"); n != 60 {
		t.Errorf("%d skill lines, want 60", n)
	}
	if !strings.Contains(p, "- … and 10 more") {
		t.Error("no overflow line")
	}
}

func TestParseSplitSkillsAndPlugins(t *testing.T) {
	opts := splitOpts(t)
	out := `===TASKS===
[{"key":"T1","title":"a","description":"d","role":"coder","depends_on":[],"skills":["sp:tdd","sp:tdd","sp:nope","modern-cpp:modern-cpp","evil:thing","not valid"]},
 {"key":"T2","title":"b","description":"d","role":"coder","depends_on":[],"skills":"sp:debug"},
 {"key":"T3","title":"c","description":"d","role":"coder","depends_on":[],"skills":["off:x", "csharp:dotnet"]}]
===NEW_ROLES===
[]
===PLUGINS===
[{"name":"modern-cpp","reason":"C++ code\u001b[31m here"},{"name":"modern-cpp","reason":"dup"},{"name":"made-up","reason":"x"},{"name":"sp","reason":"already there"}]
===END===`
	g, err := ParseSplitOpts("t", out, testRoles(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	// T1: installed sp:tdd kept once; modern-cpp skill kept because the lead asked for the plugin.
	if got := g.Nodes[0].Skills; !reflect.DeepEqual(got, []string{"sp:tdd", "modern-cpp:modern-cpp"}) {
		t.Errorf("T1 skills = %v", got)
	}
	if got := g.Nodes[1].Skills; !reflect.DeepEqual(got, []string{"sp:debug"}) {
		t.Errorf("T2 skills = %v", got)
	}
	// T3: a disabled plugin's skill and a skill of a plugin not asked for are dropped.
	if len(g.Nodes[2].Skills) != 0 {
		t.Errorf("T3 skills = %v", g.Nodes[2].Skills)
	}
	if len(g.PluginRequests) != 1 || g.PluginRequests[0].Name != "modern-cpp" || g.PluginRequests[0].Status != PluginPending ||
		strings.ContainsRune(g.PluginRequests[0].Reason, 0x1b) {
		t.Errorf("requests = %+v", g.PluginRequests)
	}
	joined := strings.Join(g.SplitWarnings, "\n")
	for _, w := range []string{"T1: skill", "sp:nope", "evil:thing", "T3: skill", "off:x", "made-up"} {
		if !strings.Contains(joined, w) {
			t.Errorf("warnings lack %q:\n%s", w, joined)
		}
	}
}

func TestParseSplitPluginsMaxFiveAndMissingSection(t *testing.T) {
	opts := splitOpts(t)
	for i := 0; i < 6; i++ {
		opts.Catalog = append(opts.Catalog, plugins.CatalogEntry{Name: "extra" + string(rune('a'+i)), Repo: "o/e", Skills: []string{"s"}})
	}
	var asks []string
	for _, e := range opts.Catalog {
		asks = append(asks, `{"name":"`+e.Name+`","reason":"r"}`)
	}
	out := "===TASKS===\n[{\"key\":\"T1\",\"title\":\"a\",\"description\":\"d\",\"role\":\"coder\",\"depends_on\":[]}]\n===PLUGINS===\n[" + strings.Join(asks, ",") + "]\n===END==="
	g, err := ParseSplitOpts("t", out, testRoles(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	// sp is installed and enabled, so it is skipped: five of the rest remain.
	if len(g.PluginRequests) != 5 {
		t.Errorf("%d requests: %+v", len(g.PluginRequests), g.PluginRequests)
	}
	g, err = ParseSplitOpts("t", happy, testRoles(), nil, opts)
	if err != nil || len(g.PluginRequests) != 0 || len(g.SplitWarnings) != 0 {
		t.Errorf("no PLUGINS section: %v %+v %v", err, g.PluginRequests, g.SplitWarnings)
	}
}

func TestParseSplitPluginsOnlyCatalog(t *testing.T) {
	// Without options nothing is accepted: no skill, no plugin.
	out := `===TASKS===
[{"key":"T1","title":"a","description":"d","role":"coder","depends_on":[],"skills":["sp:tdd"]}]
===PLUGINS===
[{"name":"modern-cpp","reason":"r"}]
===END===`
	g, err := ParseSplit("t", out, testRoles())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes[0].Skills) != 0 || len(g.PluginRequests) != 0 {
		t.Errorf("accepted without catalog: %v %v", g.Nodes[0].Skills, g.PluginRequests)
	}
}

func TestSplitOptsUsesOptionsInPrompt(t *testing.T) {
	opts := splitOpts(t)
	fake := &chunkRunner{chunks: []string{happy}}
	if _, _, err := SplitOpts(t.Context(), fake, "m", "task", testRoles(), "ctx", nil, 0, opts, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.req.SystemPrompt, "- sp:tdd: Test first") {
		t.Error("SplitOpts did not pass the skills to the prompt")
	}
}

func TestGraphSkillsAndRequestsRoundTrip(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Title: "a", Kind: KindWork, Status: StatusTodo, Skills: []string{"sp:tdd", "bad id", "sp:tdd"}},
	}, PluginRequests: []PluginRequest{
		{Name: "modern-cpp", Reason: "why", Status: PluginPending},
		{Name: "csharp", Status: "weird"},
		{Name: "Bad Name", Status: PluginPending},
	}}
	root := t.TempDir()
	s := NewSession()
	s.Graph = g
	if err := s.Save(root); err != nil {
		t.Fatal(err)
	}
	back, err := LoadSession(root, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Graph.Nodes[0].Skills; !reflect.DeepEqual(got, []string{"sp:tdd"}) {
		t.Errorf("skills = %v", got)
	}
	if got := back.Graph.PluginRequests; !reflect.DeepEqual(got, []PluginRequest{{Name: "modern-cpp", Reason: "why", Status: PluginPending}}) {
		t.Errorf("requests = %+v", got)
	}
	// Old sessions without the fields still load.
	old := &Graph{Nodes: []*Node{{ID: "T1", Title: "a", Kind: KindWork, Status: StatusTodo}}}
	s2 := NewSession()
	s2.Graph = old
	if err := s2.Save(root); err != nil {
		t.Fatal(err)
	}
	if b2, err := LoadSession(root, s2.ID); err != nil || len(b2.Graph.PluginRequests) != 0 {
		t.Errorf("old session: %v", err)
	}
}

func TestGraphCloneCopiesSkillsAndRequests(t *testing.T) {
	g := &Graph{Nodes: []*Node{{ID: "T1", Kind: KindWork, Skills: []string{"a:b"}}}, PluginRequests: []PluginRequest{{Name: "x", Status: PluginPending}}}
	c := g.Clone()
	c.Nodes[0].Skills[0] = "z:z"
	c.PluginRequests[0].Status = PluginDeclined
	if g.Nodes[0].Skills[0] != "a:b" || g.PluginRequests[0].Status != PluginPending {
		t.Error("Clone shares skills or requests")
	}
}

func TestGraphPendingAndSetPluginStatus(t *testing.T) {
	g := &Graph{PluginRequests: []PluginRequest{{Name: "a", Status: PluginPending}, {Name: "b", Status: PluginDeclined}}}
	if got := g.PendingPlugins(); len(got) != 1 || got[0].Name != "a" {
		t.Errorf("pending = %+v", got)
	}
	if !g.SetPluginStatus("a", PluginInstalled) || g.SetPluginStatus("zz", PluginInstalled) {
		t.Error("SetPluginStatus result wrong")
	}
	if len(g.PendingPlugins()) != 0 {
		t.Error("still pending")
	}
}

func TestAppendCarriesSkillsAndRequests(t *testing.T) {
	g := &Graph{Nodes: []*Node{n("T1")}, PluginRequests: []PluginRequest{{Name: "a", Status: PluginDeclined}}}
	g.AddCheckDeferred()
	add := &Graph{Nodes: []*Node{{ID: "T1", Title: "x", Kind: KindWork, Skills: []string{"sp:tdd"}}},
		PluginRequests: []PluginRequest{{Name: "a", Status: PluginPending}, {Name: "b", Status: PluginPending}}}
	g.Append(add, 2)
	if got := g.Node("T2").Skills; !reflect.DeepEqual(got, []string{"sp:tdd"}) {
		t.Errorf("skills = %v", got)
	}
	add.Nodes[0].Skills[0] = "changed:x"
	if g.Node("T2").Skills[0] != "sp:tdd" {
		t.Error("Append shares the skills slice")
	}
	if len(g.PluginRequests) != 2 || g.PluginRequests[0].Status != PluginDeclined || g.PluginRequests[1].Name != "b" {
		t.Errorf("requests = %+v", g.PluginRequests)
	}
}

func TestExecUnionOfRoleAndNodeSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mkHomePlugin(t, home, "sp", map[string]string{
		"skills/role-one/SKILL.md": "---\ndescription: d\n---\nROLE-ONE-BODY",
		"skills/lead-one/SKILL.md": "---\ndescription: d\n---\nLEAD-ONE-BODY",
	})
	cfg := configWithRoleSkills("docs_writer", "sp:role-one")
	f := &execFake{text: "done\n===SUMMARY===\nok"}
	ex := newTestExec(t, cfg, f, nil)
	g := &Graph{Nodes: []*Node{{ID: "T1", Title: "Docs", Kind: KindWork, Role: "docs_writer",
		Skills: []string{"sp:lead-one", "sp:role-one", "sp:ghost"}}}}
	res, err := ex.Run(t.Context(), g, g.Nodes[0], func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	p := f.reqs[0].SystemPrompt
	i, j := strings.Index(p, "### sp:role-one"), strings.Index(p, "### sp:lead-one")
	if i < 0 || j < 0 || i > j || strings.Count(p, "ROLE-ONE-BODY") != 1 {
		t.Errorf("skill order/dedupe wrong (role %d, lead %d):\n%s", i, j, p)
	}
	if !containsStr(res.Warnings, `skill "sp:ghost" not found`) {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func configWithRoleSkills(role string, skills ...string) config.Config {
	cfg := config.DefaultConfig()
	cfg.Agents[role] = config.AgentConfig{Description: "Writes docs.", UserSkills: skills}
	return cfg
}

func TestEngineSetSkillsRules(t *testing.T) {
	g := &Graph{Nodes: []*Node{
		{ID: "T1", Kind: KindWork, Status: StatusTodo},
		{ID: "T2", Kind: KindWork, Status: StatusDone},
		{ID: "T3", Kind: KindWork, Status: StatusRunning},
		{ID: "T4", Kind: KindWork, Status: StatusFailed},
		{ID: "CHECK", Kind: KindCheck, Status: StatusTodo},
	}}
	e := NewEngine(g, newFakeExec(), 3, "")
	if err := e.SetSkills("T1", []string{"sp:tdd", "sp:tdd", "bad id"}); err != nil {
		t.Fatal(err)
	}
	if got := e.Snapshot().Node("T1").Skills; !reflect.DeepEqual(got, []string{"sp:tdd"}) {
		t.Errorf("skills = %v", got)
	}
	if err := e.SetSkills("T4", nil); err != nil {
		t.Errorf("failed node should accept skills: %v", err)
	}
	for _, id := range []string{"T2", "T3", "CHECK", "nope"} {
		if err := e.SetSkills(id, []string{"sp:tdd"}); err == nil {
			t.Errorf("SetSkills %s should fail", id)
		}
	}
}
