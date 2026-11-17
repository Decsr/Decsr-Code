package tui

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Decsr/Decsr-Code/internal/workspace"
)

func shellModel(env map[string]string, split func(string) error) workspaceModel {
	m := workspaceModel{root: "/proj", sess: newWsSess("", &workspace.Session{})}
	m.deps.getenv = func(k string) string { return env[k] }
	m.deps.tmuxSplit = split
	return m
}

func lastLine(m workspaceModel) string {
	tr := m.sess.snapshot().Transcript
	if len(tr) == 0 {
		return ""
	}
	return tr[len(tr)-1].Text
}

func TestShellCommandUsesShellEnvAndDir(t *testing.T) {
	cmd := shellCommand("/work/t1", func(k string) string { return map[string]string{"SHELL": "/bin/zsh"}[k] })
	if cmd.Path != "/bin/zsh" && filepath.Base(cmd.Path) != "zsh" || cmd.Dir != "/work/t1" {
		t.Errorf("cmd = %v dir %q", cmd.Args, cmd.Dir)
	}
	cmd = shellCommand("/work/t1", func(string) string { return "" })
	want := "/bin/sh"
	if runtime.GOOS == "windows" {
		want = "cmd.exe"
	}
	if cmd.Args[0] != want {
		t.Errorf("fallback args = %v, want %s", cmd.Args, want)
	}
	if runtime.GOOS == "windows" {
		cmd = shellCommand("/work/t1", func(k string) string { return map[string]string{"COMSPEC": `C:\Windows\System32\cmd.exe`}[k] })
		if cmd.Args[0] != `C:\Windows\System32\cmd.exe` {
			t.Errorf("COMSPEC args = %v", cmd.Args)
		}
	}
}

func TestOpenShellUnderTmuxSplitsAndKeepsTUI(t *testing.T) {
	var gotDir string
	m := shellModel(map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}, func(dir string) error { gotDir = dir; return nil })
	cmd := m.openShell(&workspace.Node{ID: "T1"})
	if cmd == nil {
		t.Fatal("no command")
	}
	msg, ok := cmd().(wsShellDoneMsg)
	if !ok || msg.err != nil || gotDir != "/proj" {
		t.Fatalf("msg = %#v, dir = %q", msg, gotDir)
	}
	if got := lastLine(m); !strings.Contains(got, "tmux split in the project root") {
		t.Errorf("conversation = %q", got)
	}
}

func TestOpenShellWithoutTmuxSuspendsTheTUI(t *testing.T) {
	called := false
	m := shellModel(map[string]string{"SHELL": "/bin/sh"}, func(string) error { called = true; return nil })
	cmd := m.openShell(&workspace.Node{ID: "T1"})
	if cmd == nil || called {
		t.Fatalf("cmd = %v, tmux split called = %v", cmd, called)
	}
	if _, isDone := cmd().(wsShellDoneMsg); isDone {
		t.Error("without tmux the command must be an ExecProcess, not a done message")
	}
	if got := lastLine(m); !strings.Contains(got, "project root") {
		t.Errorf("conversation = %q", got)
	}
}
