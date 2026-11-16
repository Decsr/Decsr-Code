package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Truncate cuts s to at most w cells, ending in "…" when it was cut.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// TruncateMiddle cuts s to at most w cells by dropping its middle, which keeps
// both ends of a path: "internal/…/model.go".
func TruncateMiddle(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	keep := w - 1
	head := (keep + 1) / 2
	tail := keep - head
	return ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, sw-tail, "")
}

// pad fills s with spaces to w cells; a longer s is returned unchanged.
func pad(s string, w int) string {
	if n := w - ansi.StringWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// fit cuts or pads s to exactly w cells.
func fit(s string, w int) string {
	return pad(Truncate(s, w), w)
}

// RightAlign puts right at the right edge of a line of width cells. The left
// side is truncated first; when right alone is too wide it is cut and left is
// dropped. A result never exceeds width.
func RightAlign(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	rw := ansi.StringWidth(right)
	if rw >= width {
		return Truncate(right, width)
	}
	if right == "" {
		return Truncate(left, width)
	}
	room := width - rw - 1
	l := Truncate(left, room)
	return pad(l, width-rw) + right
}
