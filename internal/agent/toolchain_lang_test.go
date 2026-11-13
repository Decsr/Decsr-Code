package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/config"
)

func langRoot(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCoderBuildCmdByLanguage(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"CMakeLists.txt", "cmake -S . -B build && cmake --build build"},
		{"App.csproj", "dotnet build --nologo"},
		{"pyproject.toml", ""},
		{"", ""},
	}
	for _, tc := range tests {
		var files []string
		if tc.file != "" {
			files = append(files, tc.file)
		}
		a := &CoderAgent{root: langRoot(t, files...)}
		if got := a.buildCmd(); got != tc.want {
			t.Errorf("buildCmd with %q = %q, want %q", tc.file, got, tc.want)
		}
	}
}

func TestCoderBuildCmdConfigOverridesMarkers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Project.Language = "java"
	a := &CoderAgent{root: langRoot(t, "go.mod", "pom.xml"), cfg: cfg}
	if got := a.buildCmd(); got != "mvn -q compile" {
		t.Errorf("buildCmd = %q", got)
	}
}

func TestCoderDefaultTestCmds(t *testing.T) {
	tests := []struct {
		file string
		want []string
	}{
		{"CMakeLists.txt", []string{"ctest --test-dir build --output-on-failure"}},
		{"App.sln", []string{"dotnet test --nologo"}},
		{"", nil},
	}
	for _, tc := range tests {
		var files []string
		if tc.file != "" {
			files = append(files, tc.file)
		}
		a := &CoderAgent{root: langRoot(t, files...)}
		if got := a.defaultTestCmds(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("defaultTestCmds with %q = %v, want %v", tc.file, got, tc.want)
		}
	}
}

func TestQAFallbackTestCmdsNonGo(t *testing.T) {
	withTest := func(cmd string) config.Config {
		cfg := config.DefaultConfig()
		cfg.Project.TestCmd = cmd
		return cfg
	}
	tests := []struct {
		name string
		file string
		cfg  config.Config
		want []string
	}{
		{"python user cmd", "pyproject.toml", withTest("pytest -q"), []string{"pytest -q"}},
		{"go user cmd", "go.mod", withTest("go test ./..."), []string{goBuildCmd, "go test ./..."}},
		{"cmake", "CMakeLists.txt", config.DefaultConfig(), []string{"cmake -S . -B build && cmake --build build", "ctest --test-dir build --output-on-failure"}},
		{"empty", "", config.DefaultConfig(), []string{"echo 'no test commands available'"}},
	}
	for _, tc := range tests {
		var files []string
		if tc.file != "" {
			files = append(files, tc.file)
		}
		a := &QAAgent{root: langRoot(t, files...), cfg: tc.cfg}
		if got := a.fallbackTestCmds(); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
