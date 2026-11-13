package decsr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsProjectScaffolded(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  bool
	}{
		{"empty", nil, false},
		{"go.mod", []string{"go.mod"}, true},
		{"package.json", []string{"package.json"}, true},
		{"cmake", []string{"CMakeLists.txt"}, true},
		{"sln", []string{"App.sln"}, true},
		{"csproj", []string{"App.csproj"}, true},
		{"gradle kts", []string{"build.gradle.kts"}, true},
		{"source files only", []string{"main.go", "a.py"}, false},
	}
	for _, c := range cases {
		root := t.TempDir()
		for _, f := range c.files {
			if err := os.WriteFile(filepath.Join(root, f), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got := isProjectScaffolded(root); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
