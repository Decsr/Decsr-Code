package runner

import (
	"encoding/json"
)

// readOnlyShell are the shell commands (program and subcommand) a read-only
// node may run: they inspect and never build, test or write. find is left out
// (-exec, -delete).
var readOnlyShell = []string{
	"ls", "cat", "head", "tail", "wc", "pwd", "which", "file", "stat", "du", "tree", "rg", "grep",
	"git status", "git diff", "git log", "git show", "git blame", "git grep",
	"go doc", "go list",
}

// readOnlyDeny are shell patterns refused for a read-only node even when the
// command above is allowed: the flags and the redirection that write a file or
// run a program. A deny rule beats an allow rule.
var readOnlyDeny = []string{
	"git diff*--output*", "git log*--output*", "git show*--output*",
	"tree*-o*", "rg*--pre*", "git grep*-O*",
	"*>*",
}

// claudeReadOnlyTools is the allow list of a read-only node on claude.
func claudeReadOnlyTools() []string {
	tools := []string{"Read", "Glob", "Grep", "WebSearch", "WebFetch"}
	for _, c := range readOnlyShell {
		tools = append(tools, "Bash("+c+":*)")
	}
	return tools
}

// claudeReadOnlyDeny is the --disallowedTools list of a read-only node.
func claudeReadOnlyDeny() []string {
	out := make([]string, len(readOnlyDeny))
	for i, d := range readOnlyDeny {
		out[i] = "Bash(" + d + ")"
	}
	return out
}

// OpenCodeReadOnlyConfig is the OPENCODE_CONFIG_CONTENT of a read-only node:
// no edits, no shell except the read-only commands, then the deny rules. The
// last matching rule of a resource wins, so the allow rules follow the blanket
// deny and the deny rules follow the allow rules.
func OpenCodeReadOnlyConfig() string {
	type rule struct {
		Action   string `json:"action"`
		Resource string `json:"resource"`
		Effect   string `json:"effect"`
	}
	rules := []rule{{"edit", "*", "deny"}, {"shell", "*", "deny"}}
	for _, c := range readOnlyShell {
		rules = append(rules, rule{"shell", c + " *", "allow"})
	}
	for _, d := range readOnlyDeny {
		rules = append(rules, rule{"shell", d, "deny"})
	}
	b, _ := json.Marshal(struct {
		Permissions []rule `json:"permissions"`
	}{rules})
	return string(b)
}
