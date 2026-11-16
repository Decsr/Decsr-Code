package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func readSpecFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "speckit", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseSpecKit(t *testing.T) {
	for _, name := range []string{"specs/003-login/tasks.md", "tasks_crlf.md"} {
		l := ParseSpecKit(readSpecFixture(t, name))
		if len(l.Tasks) != 13 {
			t.Fatalf("%s: %d tasks, want 13 (fenced example must not count)", name, len(l.Tasks))
		}
		t1, t3, t7, t10 := l.Tasks[0], l.Tasks[2], l.Tasks[6], l.Tasks[9]
		if !t1.Done || t1.ID != "T001" || t1.Phase != 1 {
			t.Errorf("%s: T001 = %+v", name, t1)
		}
		if t3.ID != "T003" || !t3.Parallel || t3.Text != "Configure linting in .golangci.yml" {
			t.Errorf("%s: odd spacing T003 = %+v", name, t3)
		}
		if t7.Story != "US1" || !t7.Parallel || t7.Phase != 3 || !strings.HasPrefix(t7.Name, "Phase 3: User Story 1") {
			t.Errorf("%s: T007 = %+v", name, t7)
		}
		if !reflect.DeepEqual(t10.Needs, []string{"T009"}) || t10.Parallel {
			t.Errorf("%s: T010 = %+v", name, t10)
		}
		if strings.ContainsAny(t7.Line, "\r") || strings.ContainsAny(t7.Text, "\r") {
			t.Errorf("%s: CR left in %q", name, t7.Text)
		}
	}
}

func buildFixture(t *testing.T, o SpecImport) SpecResult {
	t.Helper()
	o.Feature, o.Dir = "Login with email", "specs/003-login"
	return BuildSpecGraph(ParseSpecKit(readSpecFixture(t, "specs/003-login/tasks.md")), o)
}

func TestBuildSpecGraphDependencies(t *testing.T) {
	r := buildFixture(t, SpecImport{HasQA: true})
	if r.Imported != 11 || r.Skipped != 2 || r.Phases != 4 {
		t.Fatalf("counts = %d imported, %d skipped, %d phases", r.Imported, r.Skipped, r.Phases)
	}
	if err := r.Graph.Validate(); err != nil {
		t.Fatal(err)
	}
	deps := map[string][]string{}
	title := map[string]string{}
	for _, n := range r.Graph.Nodes {
		deps[strings.Fields(n.Title)[0]] = nil
		title[n.ID] = strings.Fields(n.Title)[0]
	}
	for _, n := range r.Graph.Nodes {
		var d []string
		for _, id := range n.DependsOn {
			d = append(d, title[id])
		}
		deps[title[n.ID]] = d
	}
	want := map[string][]string{
		"T002": nil,
		"T003": nil, // [P]: previous phase only (none)
		"T004": {"T002", "T003"},
		"T005": {"T002", "T003"},
		"T006": {"T002", "T003"},
		"T007": {"T004", "T005", "T006"},
		"T008": {"T004", "T005", "T006"},
		"T009": {"T004", "T005", "T006"},
		"T010": {"T004", "T005", "T006", "T009", "T007", "T008"},
		"T012": {"T007", "T008", "T009", "T010"},
		"T013": {"T007", "T008", "T009", "T010", "T012"},
	}
	// T004 is sequential: it follows T002/T003 (previous phase) only.
	for id, w := range want {
		got := deps[id]
		if !sameSet(got, w) {
			t.Errorf("%s depends on %v, want %v", id, got, w)
		}
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func TestBuildSpecGraphNodeText(t *testing.T) {
	r := buildFixture(t, SpecImport{})
	n := r.Graph.Nodes[0]
	if n.ID != "T1" || n.Title != "T002 Initialize Go module with dependencies" || n.Kind != KindWork || n.Role != "" {
		t.Fatalf("node = %+v", n)
	}
	for _, w := range []string{"T002 Initialize Go module", "Phase: Phase 1: Setup", "Spec: specs/003-login/spec.md, plan: specs/003-login/plan.md"} {
		if !strings.Contains(n.Description, w) {
			t.Errorf("description %q lacks %q", n.Description, w)
		}
	}
	long := SpecTask{ID: "T020", Text: strings.Repeat("word ", 40)}
	if got := specTitle(long); len([]rune(got)) > 80 || !strings.HasPrefix(got, "T020 word") || !strings.HasSuffix(got, "…") {
		t.Errorf("long title = %q", got)
	}
}

func TestSuggestSpecRole(t *testing.T) {
	cases := map[string]string{
		"Contract test for POST /login in internal/auth/login_test.go": "qa",
		"Add widget in src/widget.spec.ts":                             "qa",
		"Integration journey in tests/integration/test_login.py":       "qa",
		"Create User model in internal/models/user.go":                 "coder",
		"Read spec.md and update docs":                                 "coder",
	}
	for text, want := range cases {
		if got := SuggestSpecRole(text, true); got != want {
			t.Errorf("%q = %s, want %s", text, got, want)
		}
		if got := SuggestSpecRole(text, false); got != "coder" {
			t.Errorf("%q without qa = %s", text, got)
		}
	}
	r := buildFixture(t, SpecImport{HasQA: true})
	byTitle := map[string]string{}
	for _, n := range r.Graph.Nodes {
		byTitle[strings.Fields(n.Title)[0]] = n.SuggestedRole
	}
	if byTitle["T007"] != "qa" || byTitle["T008"] != "qa" || byTitle["T009"] != "coder" {
		t.Errorf("roles = %v", byTitle)
	}
}

func TestBuildSpecGraphPhase(t *testing.T) {
	r := buildFixture(t, SpecImport{Phase: 3})
	if r.Imported != 4 || r.Phases != 1 || r.Skipped != 1 {
		t.Fatalf("counts = %+v", r)
	}
	for _, n := range r.Graph.Nodes[:3] {
		if len(n.DependsOn) != 0 && n.Title[:4] != "T010" {
			t.Errorf("%s should start fresh, depends on %v", n.Title, n.DependsOn)
		}
	}
	if len(r.Graph.Nodes[0].DependsOn) != 0 {
		t.Errorf("first node depends on %v", r.Graph.Nodes[0].DependsOn)
	}
}

func TestSpecFeatureName(t *testing.T) {
	if got := SpecFeatureName("# Feature Specification: Login with email\r\n", "003-login"); got != "Login with email" {
		t.Errorf("got %q", got)
	}
	if got := SpecFeatureName("no heading", "003-login"); got != "003-login" {
		t.Errorf("got %q", got)
	}
}

func TestFindSpecTasks(t *testing.T) {
	root := t.TempDir()
	if _, err := FindSpecTasks(root, ""); err == nil {
		t.Fatal("want an error without specs")
	}
	mk := func(f string, age time.Duration) string {
		p := filepath.Join(root, "specs", f, "tasks.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("- [ ] T001 x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old, newest := mk("001-a", time.Hour), mk("002-b", time.Minute)
	if got, err := FindSpecTasks(root, ""); err != nil || got != newest {
		t.Fatalf("latest = %q, %v", got, err)
	}
	if got, err := FindSpecTasks(root, "specs/001-a"); err != nil || got != old {
		t.Fatalf("dir = %q, %v", got, err)
	}
	if got, err := FindSpecTasks(root, "specs/001-a/tasks.md"); err != nil || got != old {
		t.Fatalf("file = %q, %v", got, err)
	}
	if _, err := FindSpecTasks(root, "specs/009-x"); err == nil {
		t.Fatal("want an error for a missing path")
	}
}
