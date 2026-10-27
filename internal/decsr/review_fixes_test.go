package decsr

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"log/slog"
)

func TestSaveNiceToHaveFileWritesAllKeys(t *testing.T) {
	dir := t.TempDir()
	ws, err := artifacts.EnsureWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	saveNiceToHaveFile(ws, map[string][]string{
		"QA Review":    {"qa item"},
		"PM Deferred":  {"pm item"},
		"Custom Phase": {"custom item"},
	}, slog.Default())
	data, err := ws.ReadFile(artifacts.NiceToHaveFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"qa item", "pm item", "custom item"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q in nice-to-have file:\n%s", want, data)
		}
	}
}

func TestBootstrapGoModuleName(t *testing.T) {
	dir := t.TempDir()
	m, err := bootstrapGoModule(dir, "Build REST API: users & auth", "")
	if err != nil {
		t.Fatal(err)
	}
	if m != "build-rest-api-users-auth" {
		t.Errorf("sanitized module = %q", m)
	}
	dir2 := t.TempDir()
	m, _ = bootstrapGoModule(dir2, "anything", "github.com/me/app")
	if m != "github.com/me/app" {
		t.Errorf("configured module not preferred: %q", m)
	}
	data, _ := os.ReadFile(filepath.Join(dir2, "go.mod"))
	if !strings.HasPrefix(string(data), "module github.com/me/app\n") {
		t.Errorf("go.mod = %q", data)
	}
	if sanitizeModuleName("!!!") != "app" {
		t.Error("expected app fallback")
	}
}

func TestSpecImpliesGoRespectsOtherLanguage(t *testing.T) {
	py := agent.TaskSpec{Title: "Build a Python CLI", Description: "todo app"}
	if specImpliesGo(py, "go") {
		t.Error("python spec must not imply go with default config")
	}
	if !specImpliesGo(agent.TaskSpec{Title: "todo app"}, "go") {
		t.Error("neutral spec with go config should imply go")
	}
	if !specImpliesGo(agent.TaskSpec{Title: "x in Go service"}, "python") {
		t.Error("explicit go mention should imply go")
	}
}

func TestTasksNeedingTestsSkipsClosed(t *testing.T) {
	tasks := []agent.SubTask{{Key: "A"}, {Key: "B"}, {Key: "C"}}
	got := tasksNeedingTests(tasks, map[string]string{"A": "1", "B": "2", "C": "3"}, map[string]bool{"2": true})
	if len(got) != 2 || got[0].Key != "A" || got[1].Key != "C" {
		t.Errorf("got %+v", got)
	}
}

func TestCodingStartedConcurrent(t *testing.T) {
	tr := &TaskRunner{}
	if !tr.CodingStarted().IsZero() {
		t.Fatal("expected zero before start")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.markCodingStarted()
			_ = tr.CodingStarted()
		}()
	}
	wg.Wait()
	first := tr.CodingStarted()
	tr.markCodingStarted()
	if first.IsZero() || !tr.CodingStarted().Equal(first) {
		t.Error("markCodingStarted must set once and keep the first value")
	}
}
