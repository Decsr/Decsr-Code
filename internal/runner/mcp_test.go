package runner

import (
	"reflect"
	"strings"
	"testing"
)

func TestMCPServerRule(t *testing.T) {
	for in, want := range map[string]string{
		"github":                         "mcp__github",
		"google-workspace":               "mcp__google-workspace",
		"claude.ai Claude Docs":          "mcp__claude_ai_Claude_Docs",
		"claude.ai Google Drive":         "mcp__claude_ai_Google_Drive",
		"plugin:open-design:open-design": "mcp__plugin_open-design_open-design",
		"my_server":                      "mcp__my_server",
		"a.b":                            "mcp__a_b",
	} {
		if got := MCPServerRule(in); got != want {
			t.Errorf("MCPServerRule(%q) = %q, want %q", in, got, want)
		}
	}
	if got := MCPServerRules([]string{"a b", "a.b", "c"}); !reflect.DeepEqual(got, []string{"mcp__a_b", "mcp__c"}) {
		t.Errorf("rules = %q", got)
	}
}

func TestClaudeArgsForMCP(t *testing.T) {
	base := AgentTools([]string{"go test ./..."}, nil, true)
	const strict = "--strict-mcp-config"

	// A node without MCP keeps the empty strict config.
	plain := strings.Join(claudeBaseArgs(true, false, base, false), " ")
	if !strings.Contains(plain, strict+` --mcp-config {"mcpServers":{}}`) {
		t.Errorf("plain args = %s", plain)
	}

	// A node with MCP servers: no strict flag, one rule per server, --tools as before.
	withRules := append(append([]string(nil), base...), MCPServerRules([]string{"github", "claude.ai Google Drive"})...)
	mcp := strings.Join(claudeBaseArgs(true, true, withRules, false), " ")
	if strings.Contains(mcp, strict) || strings.Contains(mcp, "--mcp-config") {
		t.Errorf("mcp args keep the strict config: %s", mcp)
	}
	if !strings.Contains(mcp, ",mcp__github,mcp__claude_ai_Google_Drive --permission-mode dontAsk") {
		t.Errorf("mcp args lack the allow rules: %s", mcp)
	}
	toolsOf := func(args string) string {
		_, rest, _ := strings.Cut(args, "--tools ")
		tools, _, _ := strings.Cut(rest, " ")
		return tools
	}
	if !strings.Contains(mcp, "--setting-sources user,project,local") || !strings.Contains(plain, "--setting-sources project,local ") {
		t.Errorf("setting sources: mcp=%s plain=%s", mcp, plain)
	}
	if toolsOf(mcp) != toolsOf(plain) || strings.Contains(toolsOf(mcp), "mcp__") {
		t.Errorf("--tools changed: %q vs %q", toolsOf(mcp), toolsOf(plain))
	}

	// Text mode: built-in tools stay off; MCP rules ride along with dontAsk.
	textRules := MCPServerRules([]string{"github", "claude.ai Google Drive"})
	text := strings.Join(claudeBaseArgs(false, true, textRules, false), " ")
	if strings.Contains(text, strict) || !strings.Contains(text, "--setting-sources user,project,local") ||
		toolsOf(text) != "" || !strings.Contains(text, "--allowedTools mcp__github,mcp__claude_ai_Google_Drive --permission-mode dontAsk") {
		t.Errorf("text mcp args = %s", text)
	}
	textPlain := strings.Join(claudeBaseArgs(false, false, nil, false), " ")
	if !strings.Contains(textPlain, strict+` --mcp-config {"mcpServers":{}}`) || !strings.Contains(textPlain, "--setting-sources project,local ") ||
		strings.Contains(textPlain, "--allowedTools") || strings.Contains(textPlain, "--permission-mode") {
		t.Errorf("text plain args = %s", textPlain)
	}

	// The role option mcp: true keeps all servers and adds no rules.
	role := strings.Join(claudeBaseArgs(true, true, base, false), " ")
	if strings.Contains(role, strict) || strings.Contains(role, "mcp__") || !strings.Contains(role, "--setting-sources user,project,local") {
		t.Errorf("role mcp args = %s", role)
	}
}
