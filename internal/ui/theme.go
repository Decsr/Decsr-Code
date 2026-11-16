// Package ui holds the shared look of every DECSR screen: theme tokens, the
// theme switch and small pure components that render width-aware strings.
package ui

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// Theme tokens. Each is an adaptive colour, so one switch (Apply) flips every
// style built from them.
var (
	Text   = lipgloss.AdaptiveColor{Dark: "#d5e0f7", Light: "#1f2937"} // titles, body
	Muted  = lipgloss.AdaptiveColor{Dark: "#7d8db3", Light: "#6b7280"} // labels, metadata, hints
	Border = lipgloss.AdaptiveColor{Dark: "#2f4270", Light: "#d0d7e2"} // dividers, card borders
	Accent = lipgloss.AdaptiveColor{Dark: "#7c9cff", Light: "#3b5bdb"} // brand, focus, running
	OK     = lipgloss.AdaptiveColor{Dark: "#6ff2b4", Light: "#2f9e44"} // done, + lines
	Warn   = lipgloss.AdaptiveColor{Dark: "#ffd166", Light: "#b7791f"} // waiting, needs you, budget
	Fail   = lipgloss.AdaptiveColor{Dark: "#ff5c7a", Light: "#d6336c"} // failed, - lines
	Sel    = lipgloss.AdaptiveColor{Dark: "#1c2b4d", Light: "#e7ecfb"} // background of the selected row or card

	// Brand colours of runner glyphs.
	Claude   = lipgloss.AdaptiveColor{Dark: "#D97757", Light: "#c15f3c"}
	DeepSeek = lipgloss.AdaptiveColor{Dark: "#4D6BFE", Light: "#3b54d9"}

	// Diff tints: the background of added and removed lines.
	DiffAdd = lipgloss.AdaptiveColor{Dark: lerpHex("#0b1426", "#6ff2b4", 0.16), Light: lerpHex("#ffffff", "#2f9e44", 0.16)}
	DiffDel = lipgloss.AdaptiveColor{Dark: lerpHex("#0b1426", "#ff5c7a", 0.18), Light: lerpHex("#ffffff", "#d6336c", 0.18)}
)

// lerpHex blends two "#rrggbb" colours (t in 0..1).
func lerpHex(a, b string, t float64) string {
	parse := func(c string) (int, int, int) {
		var r, g, bl int
		_, _ = fmt.Sscanf(c, "#%02x%02x%02x", &r, &g, &bl)
		return r, g, bl
	}
	ar, ag, ab := parse(a)
	br, bg, bb := parse(b)
	mix := func(x, y int) int { return x + int(float64(y-x)*t+0.5) }
	return fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb))
}

// Theme modes of ui.theme.
const (
	ThemeAuto  = "auto"
	ThemeDark  = "dark"
	ThemeLight = "light"
)

// ValidTheme reports whether mode is a value ui.theme accepts ("" counts as auto).
func ValidTheme(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ThemeAuto, ThemeDark, ThemeLight:
		return true
	}
	return false
}

var light atomic.Bool

// IsDark reports the theme chosen by Apply; dark until Apply runs.
func IsDark() bool { return !light.Load() }

func setDark(dark bool) {
	light.Store(!dark)
	lipgloss.SetHasDarkBackground(dark)
}

// Apply selects the theme once at startup, before the Bubble Tea program
// starts and before the first render. "dark" and "light" force it. "auto", ""
// and any invalid value ask detect (nil uses TerminalDark) and fall back to
// dark when it reports not-ok.
//
// detect runs synchronously on the caller's goroutine and Apply starts no
// goroutine, so nothing can still be reading the terminal after Apply returns.
// A custom detector must therefore return promptly on its own.
func Apply(mode string, detect func() (dark bool, ok bool)) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ThemeDark:
		setDark(true)
		return
	case ThemeLight:
		setDark(false)
		return
	}
	if detect == nil {
		detect = TerminalDark
	}
	if dark, ok := detect(); ok {
		setDark(dark)
		return
	}
	setDark(true)
}

// TerminalDark is the default detector. It asks the terminal for its
// background colour, but only when that is safe and reliable: stdin and stdout
// are terminals, $TERM is not "dumb", and the program is not inside tmux or
// screen (they cannot answer for the outer terminal). Otherwise it reports
// not-ok, which means dark.
//
// The query is termenv's: it runs on the calling goroutine, reads the answer
// with select-based waits bounded by the library, and restores the terminal
// mode before returning, so no goroutine or pending read outlives the call.
func TerminalDark() (dark bool, ok bool) {
	return detectTerminal(os.Getenv, term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()), lipgloss.HasDarkBackground)
}

// detectTerminal is TerminalDark with its inputs injected: the environment,
// whether stdin and stdout are terminals, and the query itself.
func detectTerminal(getenv func(string) string, tty bool, query func() bool) (dark bool, ok bool) {
	if !tty {
		return true, false
	}
	switch t := getenv("TERM"); {
	case t == "" || t == "dumb" || strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux"):
		return true, false
	}
	if getenv("TMUX") != "" || getenv("STY") != "" {
		return true, false
	}
	return query(), true
}
