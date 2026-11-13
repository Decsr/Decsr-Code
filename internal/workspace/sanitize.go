package workspace

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// SanitizeText removes terminal escape sequences (CSI, OSC, …) and control
// characters from untrusted text. Newlines are kept and tabs become spaces.
func SanitizeText(s string) string {
	if strings.ContainsRune(s, '\x1b') {
		s = ansi.Strip(s)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\t", "    ")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		}
		return r
	}, s)
}

// Sanitize is SanitizeText for single-line text: every line break becomes a
// space.
func Sanitize(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(SanitizeText(s), "\n", " "))
}
