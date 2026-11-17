package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/ranking"
)

const rankTitle = "Rank"

// wsRankMsg is the result of a /rank fetch.
type wsRankMsg struct{ rep ranking.Report }

// cmdRank fetches the rankings from Artificial Analysis with the saved key,
// saves them and shows the table. The fetch runs off the UI thread; the view
// says "fetching" until it ends.
func (m workspaceModel) cmdRank(string) (tea.Model, tea.Cmd) {
	if m.rankBusy {
		return m.openInfo(rankTitle, []string{"Fetching the rankings from Artificial Analysis…"}), nil
	}
	m.rankBusy = true
	settings := ranking.FromConfig(m.cfg)
	cmd := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return wsRankMsg{rep: ranking.Run(ctx, ranking.RunOptions{Settings: settings})}
	}
	return m.openInfo(rankTitle, []string{"Fetching the rankings from Artificial Analysis…", "", ranking.Attribution}), cmd
}

func (m workspaceModel) onRank(msg wsRankMsg) (tea.Model, tea.Cmd) {
	m.rankBusy = false
	if m.overlay == wsOverlayInfo && m.infoName == rankTitle {
		m.infoText = msg.rep.Lines()
		m.infoTop = 0
	} else if msg.rep.Err != nil {
		m.setNotice("! rank: "+msg.rep.Err.Error(), true)
	}
	return m, nil
}
