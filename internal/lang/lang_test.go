package lang

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkRoot(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetect(t *testing.T) {
	tests := []struct {
		files []string
		want  string
	}{
		{nil, ""},
		{[]string{"go.mod"}, "go"},
		{[]string{"CMakeLists.txt"}, "cpp"},
		{[]string{"App.sln"}, "csharp"},
		{[]string{"src.csproj"}, "csharp"},
		{[]string{"pom.xml"}, "java"},
		{[]string{"build.gradle.kts"}, "java"},
		{[]string{"pyproject.toml"}, "python"},
		{[]string{"requirements.txt"}, "python"},
		{[]string{"setup.py"}, "python"},
		{[]string{"Cargo.toml"}, "rust"},
		{[]string{"package.json"}, "javascript"},
		{[]string{"package.json", "tsconfig.json"}, "typescript"},
		{[]string{"Gemfile"}, "ruby"},
		{[]string{"mix.exs"}, "elixir"},
		{[]string{"CMakeLists.txt", "package.json"}, "cpp"},
		{[]string{"App.sln", "requirements.txt"}, "csharp"},
		{[]string{"go.mod", "package.json"}, "go"},
	}
	for _, tc := range tests {
		for i := 0; i < 5; i++ {
			if got := Detect(mkRoot(t, tc.files...)); got != tc.want {
				t.Fatalf("Detect(%v) = %q, want %q", tc.files, got, tc.want)
			}
		}
	}
}

func TestDetectSourceFallback(t *testing.T) {
	root := t.TempDir()
	if got := Detect(root); got != "" {
		t.Fatalf("empty = %q", got)
	}
	write := func(rel string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.cpp")
	if got := Detect(root); got != "cpp" {
		t.Fatalf("one cpp = %q", got)
	}
	write("src/b.cc")
	write("src/c.py")
	if got := Detect(root); got != "cpp" {
		t.Fatalf("two cpp one py = %q", got)
	}
	write("d.py")
	if got := Detect(root); got != "python" {
		t.Fatalf("tie must go to python (listed first), got %q", got)
	}
	if got := DetectMarkers(root); got != "" {
		t.Fatalf("DetectMarkers = %q, want empty", got)
	}
}

func TestDetectBracketedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj[1]")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "App.sln"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Detect(root); got != "csharp" {
		t.Fatalf("got %q", got)
	}
}

func TestCommandsForPythonInterpreter(t *testing.T) {
	defer func(old func(string) (string, error)) { lookPath = old }(lookPath)
	defer func(old func(string) bool) { hasPytest = old }(hasPytest)
	hasPytest = func(string) bool { return true }
	have := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, h := range names {
				if h == n {
					return "/bin/" + n, nil
				}
			}
			return "", os.ErrNotExist
		}
	}
	cases := []struct {
		have []string
		want string
	}{
		{[]string{"python"}, "python -m pytest"},
		{[]string{"python", "python3"}, "python -m pytest"},
		{[]string{"python3"}, "python3 -m pytest"},
		{nil, "python -m pytest"},
	}
	for _, c := range cases {
		lookPath = have(c.have...)
		got := CommandsFor("python", mkRoot(t)).Test
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("have %v: got %v, want %q", c.have, got, c.want)
		}
	}
}

func TestCommandsForPythonWithoutPytest(t *testing.T) {
	defer func(old func(string) (string, error)) { lookPath = old }(lookPath)
	defer func(old func(string) bool) { hasPytest = old }(hasPytest)
	hasPytest = func(string) bool { return false }
	lookPath = func(n string) (string, error) {
		if n == "python3" {
			return "/bin/python3", nil
		}
		return "", os.ErrNotExist
	}
	if got := CommandsFor("python", mkRoot(t)).Test; len(got) != 1 || got[0] != "python3 -m unittest" {
		t.Errorf("got %v", got)
	}
	lookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	if got := CommandsFor("python", mkRoot(t)).Test; len(got) != 1 || got[0] != "python -m unittest" {
		t.Errorf("got %v", got)
	}
}

func TestResolve(t *testing.T) {
	goDir := mkRoot(t, "go.mod")
	empty := mkRoot(t)
	if got := Resolve("java", goDir); got != "java" {
		t.Errorf("got %q", got)
	}
	if got := Resolve("", goDir); got != "go" {
		t.Errorf("got %q", got)
	}
	if got := Resolve("node", empty); got != "javascript" {
		t.Errorf("got %q", got)
	}
	if got := Resolve("", empty); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestCommandsFor(t *testing.T) {
	defer func(old func(string) (string, error)) { lookPath = old }(lookPath)
	lookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	defer func(old func(string) bool) { hasPytest = old }(hasPytest)
	hasPytest = func(string) bool { return true }
	cmakeBuild := "cmake -S . -B build && cmake --build build"
	ctest := "ctest --test-dir build --output-on-failure"
	npmBuild := "npm run build --if-present"
	tests := []struct {
		language string
		files    []string
		want     Commands
	}{
		{"go", nil, Commands{"go mod tidy", "go build -o " + os.DevNull + " ./...", []string{"go test -count=1 ./..."}}},
		{"cpp", nil, Commands{"", cmakeBuild, []string{ctest}}},
		{"c", nil, Commands{"", cmakeBuild, []string{ctest}}},
		{"csharp", nil, Commands{"dotnet restore", "dotnet build --nologo", []string{"dotnet test --nologo"}}},
		{"java", []string{"pom.xml"}, Commands{"", "mvn -q compile", []string{"mvn -q test"}}},
		{"java", nil, Commands{"", "gradle compileJava -q", []string{"gradle test"}}},
		{"python", []string{"requirements.txt"}, Commands{"pip install -r requirements.txt", "", []string{"python -m pytest"}}},
		{"python", []string{"pyproject.toml"}, Commands{"pip install -e .", "", []string{"python -m pytest"}}},
		{"python", nil, Commands{"", "", []string{"python -m pytest"}}},
		{"rust", nil, Commands{"cargo fetch", "cargo build", []string{"cargo test"}}},
		{"javascript", []string{"yarn.lock"}, Commands{"yarn install", npmBuild, []string{"npm test"}}},
		{"javascript", []string{"pnpm-lock.yaml"}, Commands{"pnpm install", npmBuild, []string{"npm test"}}},
		{"typescript", nil, Commands{"npm install", npmBuild, []string{"npm test"}}},
		{"", nil, Commands{}},
		{"kotlin", nil, Commands{}},
	}
	for _, tc := range tests {
		got := CommandsFor(tc.language, mkRoot(t, tc.files...))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("CommandsFor(%q, %v) = %+v, want %+v", tc.language, tc.files, got, tc.want)
		}
	}
}
