package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeFilePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		root     string
		expected string
	}{
		{
			name:     "already relative",
			path:     "internal/core/game.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "internal/core/game.go",
		},
		{
			name:     "absolute path with project root",
			path:     "Users/traq/dev/prv/tictactoe/internal/cli/cli.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "internal/cli/cli.go",
		},
		{
			name:     "nested absolute path",
			path:     "Users/traq/dev/prv/tictactoe/Users/traq/dev/prv/tictactoe/internal/core/core.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "internal/core/core.go",
		},
		{
			name:     "with leading slash",
			path:     "/Users/traq/dev/prv/tictactoe/cmd/game/main.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "cmd/game/main.go",
		},
		{
			name:     "cmd relative",
			path:     "cmd/game/main.go",
			root:     "",
			expected: "cmd/game/main.go",
		},
		{
			name:     "absolute without root",
			path:     "Users/traq/dev/prv/tictactoe/internal/core/core.go",
			root:     "",
			expected: "internal/core/core.go",
		},
		{
			name:     "home directory linux",
			path:     "home/user/project/internal/pkg/pkg.go",
			root:     "",
			expected: "internal/pkg/pkg.go",
		},
		{
			name:     "dot prefix",
			path:     "./cmd/app/main.go",
			root:     "",
			expected: "cmd/app/main.go",
		},
		{
			name:     "src directory",
			path:     "Users/dev/myproject/src/main.rs",
			root:     "",
			expected: "src/main.rs",
		},
		{
			name:     "config path",
			path:     "Users/traq/dev/prv/tictactoe/config/config.go",
			root:     "/Users/traq/dev/prv/tictactoe",
			expected: "config/config.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilePath(tt.path, tt.root)
			if result != tt.expected {
				t.Errorf("sanitizeFilePath(%q, %q) = %q, want %q", tt.path, tt.root, result, tt.expected)
			}
		})
	}
}

func TestSanitizeFilePath_RootOnlyStrippedAsPrefix(t *testing.T) {
	if got := sanitizeFilePath("internal/app/server.go", "/app"); got != "internal/app/server.go" {
		t.Errorf("got %q", got)
	}
	if got := sanitizeFilePath("app/server.go", "/app"); got != "server.go" {
		t.Errorf("got %q", got)
	}
	if got := sanitizeFilePath("application/server.go", "/app"); got != "application/server.go" {
		t.Errorf("got %q", got)
	}
}

func TestParseCmds_StripsPromptPrefix(t *testing.T) {
	got := parseCmds("$ go test ./...\n`$ go vet ./...`")
	want := []string{"go test ./...", "go vet ./..."}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFindPathInRecent_RejectsProse(t *testing.T) {
	if got := findPathInRecent([]string{"The response from `api/user.json` looks like this:"}); got != "" {
		t.Errorf("prose sentence yielded path %q", got)
	}
	if got := findPathInRecent([]string{"**cmd/app/main.go**"}); got != "cmd/app/main.go" {
		t.Errorf("bold header got %q", got)
	}
}

func TestStreamAndWriteFiles_GuessedPathNeverOverwrites(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written := runStreamLines(t, root, []string{
		"Example response:", "```json", `{"name":"alice"}`, "```",
	})
	data, _ := os.ReadFile(filepath.Join(root, "package.json"))
	if string(data) != "real\n" || len(written) != 0 {
		t.Errorf("existing file overwritten: %q written=%v", data, written)
	}
	written = runStreamLines(t, root, []string{"```html", "<html><body>x</body></html>", "```"})
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil || len(written) != 1 {
		t.Errorf("guessed path should still create new files: %v written=%v", err, written)
	}
}
