// Package lang detects a project's language from marker files and maps a
// language to its dependency, build and test commands.
package lang

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// lookPath is exec.LookPath, replaceable in tests.
var lookPath = exec.LookPath

// hasPytest reports whether the interpreter py can import pytest; replaceable
// in tests.
var hasPytest = func(py string) bool {
	return exec.Command(py, "-c", "import pytest").Run() == nil
}

// Commands are the shell commands for one language. Empty fields mean
// "nothing to run".
type Commands struct {
	Deps, Build string
	Test        []string
}

// markers is checked in order; the first hit wins. package.json is last
// because compiled-language repositories often carry one for tooling.
var markers = []struct {
	language string
	files    []string
	exts     []string
}{
	{language: "go", files: []string{"go.mod"}},
	{language: "rust", files: []string{"Cargo.toml"}},
	{language: "cpp", files: []string{"CMakeLists.txt"}},
	{language: "csharp", exts: []string{".sln", ".csproj"}},
	{language: "java", files: []string{"pom.xml", "build.gradle", "build.gradle.kts"}},
	{language: "python", files: []string{"pyproject.toml", "requirements.txt", "setup.py"}},
	{language: "ruby", files: []string{"Gemfile"}},
	{language: "elixir", files: []string{"mix.exs"}},
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hasExt reports whether root directly contains a file with one of exts.
func hasExt(root string, exts []string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		for _, want := range exts {
			if ext == want {
				return true
			}
		}
	}
	return false
}

// DetectMarkers returns the language of the project at root from top-level
// marker files only, or "" when none match.
func DetectMarkers(root string) string {
	for _, m := range markers {
		for _, f := range m.files {
			if exists(filepath.Join(root, f)) {
				return m.language
			}
		}
		if len(m.exts) > 0 && hasExt(root, m.exts) {
			return m.language
		}
	}
	if exists(filepath.Join(root, "package.json")) {
		if exists(filepath.Join(root, "tsconfig.json")) {
			return "typescript"
		}
		return "javascript"
	}
	return ""
}

// sourceExts maps source extensions to languages; the order breaks ties.
var sourceExts = []struct {
	language string
	exts     []string
}{
	{"go", []string{".go"}},
	{"python", []string{".py"}},
	{"csharp", []string{".cs"}},
	{"java", []string{".java"}},
	{"cpp", []string{".cpp", ".cc", ".cxx"}},
	{"c", []string{".c"}},
	{"rust", []string{".rs"}},
	{"typescript", []string{".ts"}},
	{"javascript", []string{".js"}},
	{"ruby", []string{".rb"}},
}

// detectBySource counts source files directly in root and root/src and returns
// the most common language, or "" when there are none.
func detectBySource(root string) string {
	counts := map[string]int{}
	for _, dir := range []string{root, filepath.Join(root, "src")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			for _, se := range sourceExts {
				for _, x := range se.exts {
					if ext == x {
						counts[se.language]++
					}
				}
			}
		}
	}
	best, bestN := "", 0
	for _, se := range sourceExts {
		if counts[se.language] > bestN {
			best, bestN = se.language, counts[se.language]
		}
	}
	return best
}

// Detect returns the language of the project at root from top-level marker
// files, falling back to the most common source-file extension, or "" when
// nothing matches.
func Detect(root string) string {
	if l := DetectMarkers(root); l != "" {
		return l
	}
	return detectBySource(root)
}

// Resolve returns the configured language (with "node" normalized to
// "javascript"), or the detected one when nothing is configured.
func Resolve(configured, root string) string {
	if configured == "" {
		return Detect(root)
	}
	if configured == "node" {
		return "javascript"
	}
	return configured
}

// CommandsFor returns the dependency, build and test commands for language.
func CommandsFor(language, root string) Commands {
	switch language {
	case "go":
		return Commands{"go mod tidy", "go build -o " + os.DevNull + " ./...", []string{"go test -count=1 ./..."}}
	case "cpp", "c":
		return Commands{"", "cmake -S . -B build && cmake --build build", []string{"ctest --test-dir build --output-on-failure"}}
	case "csharp":
		return Commands{"dotnet restore", "dotnet build --nologo", []string{"dotnet test --nologo"}}
	case "java":
		if exists(filepath.Join(root, "pom.xml")) {
			return Commands{"", "mvn -q compile", []string{"mvn -q test"}}
		}
		return Commands{"", "gradle compileJava -q", []string{"gradle test"}}
	case "python":
		py := "python"
		if _, err := lookPath("python"); err != nil {
			if _, err3 := lookPath("python3"); err3 == nil {
				py = "python3"
			}
		}
		runTests := py + " -m unittest"
		if hasPytest(py) {
			runTests = py + " -m pytest"
		}
		c := Commands{Test: []string{runTests}}
		if exists(filepath.Join(root, "requirements.txt")) {
			c.Deps = "pip install -r requirements.txt"
		} else if exists(filepath.Join(root, "pyproject.toml")) {
			c.Deps = "pip install -e ."
		}
		return c
	case "rust":
		return Commands{"cargo fetch", "cargo build", []string{"cargo test"}}
	case "javascript", "node", "typescript":
		c := Commands{Deps: "npm install", Build: "npm run build --if-present", Test: []string{"npm test"}}
		if exists(filepath.Join(root, "yarn.lock")) {
			c.Deps = "yarn install"
		} else if exists(filepath.Join(root, "pnpm-lock.yaml")) {
			c.Deps = "pnpm install"
		}
		return c
	}
	return Commands{}
}
