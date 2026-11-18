package tui

import "github.com/Decsr/Decsr-Code/internal/ranking"

// wsRankMsg is the result of a /model rank fetch.
type wsRankMsg struct{ rep ranking.Report }
