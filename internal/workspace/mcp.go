package workspace

import (
	"encoding/json"
	"strings"
	"unicode"
)

// maxMCPName is the longest MCP server name a node may carry.
const maxMCPName = 80

// mcpNames is the "mcp" field of a sub-task: a list of server names, or one
// name. It is not split on spaces, because server names can have them
// ("claude.ai Google Drive"). Any other shape gives an empty list.
type mcpNames []string

func (l *mcpNames) UnmarshalJSON(data []byte) error {
	var many []string
	if json.Unmarshal(data, &many) == nil {
		*l = many
		return nil
	}
	var one string
	if json.Unmarshal(data, &one) == nil && strings.TrimSpace(one) != "" {
		*l = []string{one}
	}
	return nil
}

// ValidMCPName reports whether name can be a server name: not empty, at most
// maxMCPName characters, no control characters.
func ValidMCPName(name string) bool {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > maxMCPName {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// CleanMCPNames keeps the valid names once each (trimmed) and returns the
// names it dropped.
func CleanMCPNames(names []string) (kept, dropped []string) {
	seen := map[string]bool{}
	for _, n := range names {
		t := strings.TrimSpace(n)
		switch {
		case !ValidMCPName(t):
			dropped = append(dropped, cutRunes(Sanitize(n), 40))
		case !seen[t]:
			seen[t] = true
			kept = append(kept, t)
		}
	}
	return kept, dropped
}
