package tui

import (
	"os"
	"strings"
	"sync"

	"github.com/Decsr/Decsr-Code/internal/executil"
)

var (
	detachOnce sync.Once
	detachText string
)

// detachHints appends the dim "detach: <prefix> d" hint when decsr runs in a
// detached tmux session (DECSR_TMUX=1). The prefix is read once.
func detachHints(hints []keyHint) []keyHint {
	if os.Getenv("DECSR_TMUX") != "1" {
		return hints
	}
	detachOnce.Do(func() {
		prefix := "C-b"
		if out, err := executil.Command("tmux", "show-options", "-gv", "prefix").Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p != "" {
				prefix = p
			}
		}
		detachText = detachHintText(prefix)
	})
	return append(hints[:len(hints):len(hints)], keyHint{"", "detach: " + detachText})
}

// detachHintText formats a tmux prefix ("C-b", "M-a") as "Ctrl+b d".
func detachHintText(prefix string) string {
	switch {
	case strings.HasPrefix(prefix, "C-") && len(prefix) > 2:
		prefix = "Ctrl+" + prefix[2:]
	case strings.HasPrefix(prefix, "M-") && len(prefix) > 2:
		prefix = "Alt+" + prefix[2:]
	}
	return prefix + " d"
}
