package tui

import (
	"testing"

	"github.com/Decsr/Decsr-Code/internal/ranking"
)

func TestRankShowsFetchingThenTable(t *testing.T) {
	h := newWSHarness(t)
	t.Setenv(ranking.KeyEnv, "")
	next, cmd := h.m.cmdRank("")
	h.m = next.(workspaceModel)
	if cmd == nil || !h.m.rankBusy {
		t.Fatal("the fetch must run as a command")
	}
	h.want("Rank")
	h.want("Fetching the rankings")

	h.send(cmd())
	if h.m.rankBusy {
		t.Fatal("busy must clear")
	}
	h.want("decsr connect artificialanalysis")
	h.want("claude-opus-5-5")
	h.wantNot(ranking.Attribution) // only the fallback tiers are shown
	h.key("esc")
}

func TestRankIsListedAndModelsIsNot(t *testing.T) {
	h := newWSHarness(t)
	found := map[string]bool{}
	for _, c := range h.m.commandList() {
		found[c.name] = true
	}
	if !found["/rank"] || found["/models"] {
		t.Fatalf("commands: %v", found)
	}
}
