package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/logging"
)

// liveLogFixture builds a workspace with decsr.log held open (as the running
// process does), the bus log, generated artifacts, memory and requirements.
func liveLogFixture(t *testing.T) (dir string, handles map[string]*os.File) {
	t.Helper()
	dir = t.TempDir()
	handles = map[string]*os.File{}
	for _, name := range []string{logging.LogFileName, artifacts.BusLogFile} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		handles[name] = f
	}
	if err := os.MkdirAll(filepath.Join(dir, artifacts.MemoryDirName), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		artifacts.TaskPlanFile,
		artifacts.RequirementsFile,
		filepath.Join(artifacts.MemoryDirName, "MEMORY.md"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, handles
}

func assertLiveLogsSurvive(t *testing.T, dir string, handles map[string]*os.File) {
	t.Helper()
	for name, f := range handles {
		want, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s was deleted: %v", name, err)
		}
		if !os.SameFile(want, got) {
			t.Errorf("%s was replaced", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, artifacts.MemoryDirName, "MEMORY.md")); err != nil {
		t.Errorf("memory removed: %v", err)
	}
}

func TestCleanWorkspaceKeepsLiveLogs(t *testing.T) {
	for _, keepReq := range []bool{true, false} {
		dir, handles := liveLogFixture(t)
		cleanWorkspace(dir, keepReq)
		assertLiveLogsSurvive(t, dir, handles)
		if _, err := os.Stat(filepath.Join(dir, artifacts.TaskPlanFile)); !os.IsNotExist(err) {
			t.Errorf("task_plan.md should be removed, err=%v", err)
		}
	}
}

func TestCleanGeneratedArtifactsKeepsLiveLogs(t *testing.T) {
	dir, handles := liveLogFixture(t)
	(artifacts.Workspace{Dir: dir}).CleanGeneratedArtifacts()
	assertLiveLogsSurvive(t, dir, handles)
	if _, err := os.Stat(filepath.Join(dir, artifacts.TaskPlanFile)); !os.IsNotExist(err) {
		t.Errorf("task_plan.md should be removed, err=%v", err)
	}
}

func TestRequirementsChangeCleansKeepLiveLogs(t *testing.T) {
	dir, handles := liveLogFixture(t)
	m := &startupModel{wsDir: dir}
	m.cleanAfterEditorSave("brand new requirements")
	assertLiveLogsSurvive(t, dir, handles)
	if _, err := os.Stat(filepath.Join(dir, artifacts.TaskPlanFile)); !os.IsNotExist(err) {
		t.Errorf("task_plan.md should be removed, err=%v", err)
	}

	dir, handles = liveLogFixture(t)
	m = &startupModel{wsDir: dir}
	newReq := filepath.Join(t.TempDir(), "req.md")
	if err := os.WriteFile(newReq, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.cleanIfRequirementsChanged(newReq)
	assertLiveLogsSurvive(t, dir, handles)
}
