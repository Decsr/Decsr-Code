package tokenutil

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// charsPerToken is the average characters per token for most LLMs.
// This heuristic is ~85% accurate and avoids external dependencies.
const charsPerToken = 4

// EstimateTokens returns an approximate token count for the given text.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return (len(text) + charsPerToken - 1) / charsPerToken
}

// Truncate shortens text to fit within the given token budget.
// Returns the original text unchanged if it fits. Appends a truncation marker
// when content is cut.
func Truncate(text string, maxTokens int) string {
	if maxTokens <= 0 || EstimateTokens(text) <= maxTokens {
		return text
	}
	maxChars := maxTokens * charsPerToken
	if maxChars >= len(text) {
		return text
	}

	// Cut at the last newline before the limit to avoid breaking mid-line.
	for maxChars > 0 && !utf8.RuneStart(text[maxChars]) {
		maxChars--
	}
	cut := text[:maxChars]
	if idx := strings.LastIndex(cut, "\n"); idx > maxChars/2 {
		cut = cut[:idx]
	}
	return cut + "\n... (truncated to fit token budget)"
}

// TruncateSourceFiles truncates a collection of source file contents
// to fit within a total token budget. Files are included in order until
// the budget is exhausted.
func TruncateSourceFiles(files map[string]string, maxTokens int) map[string]string {
	if maxTokens <= 0 {
		return files
	}

	result := make(map[string]string, len(files))
	remaining := maxTokens

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		content := files[name]
		tokens := EstimateTokens(content)
		if tokens <= remaining {
			result[name] = content
			remaining -= tokens
		} else if remaining > 200 {
			// Include a truncated version of this file.
			result[name] = Truncate(content, remaining-50) // leave room for marker
			remaining = 0
		}
		if remaining <= 0 {
			break
		}
	}
	return result
}
