package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/ui"
)

// wsForm is one way to call a command: the text to type and what it does.
type wsForm struct{ usage, does string }

// removedCommands are the commands that moved: typing one prints the line and
// runs nothing. The functions behind them stay.
var removedCommands = map[string]string{
	"/approve": "use y / n on the plan card (/answer shows it again)",
	"/reject":  "use y / n on the plan card (/answer shows it again)",
	"/cost":    "use /usage cost",
	"/budget":  "use /usage budget [amount|off]",
	"/rank":    "use /model rank",
	"/connect": "use /provider add <name>",
	"/review":  "use /coordinator now",
	"/profile": "use /model profile save|apply <name>",
}

// removedHint is the one line printed for a removed command; "" when name is
// not one.
func removedHint(name string) string {
	if h, ok := removedCommands[name]; ok {
		return name + " has moved: " + h
	}
	return ""
}

// cmdGroups is the display order of the groups of the built-in commands, then
// the plugin and custom commands.
var cmdGroups = []string{ui.GroupNameSession, ui.GroupNameRun, ui.GroupNameAgents, ui.GroupNameUsage, ui.GroupNameProject, ui.GroupNamePlugin}

// builtinCommands is the table of the built-in commands, by group in display
// order. Every command has a short args hint for the palette, forms and one
// example for /help <command>.
func builtinCommands() []wsCommand {
	S, R, A, U, P := ui.GroupNameSession, ui.GroupNameRun, ui.GroupNameAgents, ui.GroupNameUsage, ui.GroupNameProject
	return []wsCommand{
		// Session
		{name: "/help", group: S, args: "[command]", desc: "commands by group; /help <command> shows its forms",
			forms:   []wsForm{{"/help", "list the commands by group"}, {"/help <command>", "show the forms and an example of one command"}},
			example: "/help plugin", run: workspaceModel.cmdHelp},
		{name: "/clear", group: S, desc: "save this session and start a new one",
			forms:   []wsForm{{"/clear", "save this session and start a new one; asks first when nodes are unfinished"}},
			example: "/clear", run: workspaceModel.cmdClear},
		{name: "/compact", group: S, desc: "fold finished nodes into the session summary",
			forms:   []wsForm{{"/compact", "fold the finished nodes into the summary the lead reads"}},
			example: "/compact", run: workspaceModel.cmdCompact},
		{name: "/resume", group: S, desc: "continue a saved session of this project",
			forms:   []wsForm{{"/resume", "list the saved sessions of this project and open one"}},
			example: "/resume", run: workspaceModel.cmdResume},
		{name: "/rename", group: S, args: "<title>", desc: "set the title of this session",
			forms:   []wsForm{{"/rename <title>", "set the title shown in the header and in /resume"}},
			example: "/rename Add JSON output", run: workspaceModel.cmdRename},
		{name: "/export", group: S, desc: "write this session to a markdown file",
			forms:   []wsForm{{"/export", "write the chat and the nodes to .decsr/exports/<session>.md"}},
			example: "/export", run: workspaceModel.cmdExport},
		{name: "/copy", group: S, desc: "copy the last answer to the clipboard",
			forms:   []wsForm{{"/copy", "copy the last answer of the lead or of a node (works over SSH and tmux)"}},
			example: "/copy", run: workspaceModel.cmdCopy},
		{name: "/quit", aliases: []string{"/exit"}, group: S, desc: "leave the workspace",
			forms:   []wsForm{{"/quit", "save the session and leave; asks first while nodes run"}},
			example: "/quit", run: func(m workspaceModel, _ string) (tea.Model, tea.Cmd) { return m.requestQuit(false) }},

		// Run
		{name: "/answer", group: R, desc: "show the question or refused command that waits for you",
			forms:   []wsForm{{"/answer", "show the question card or the refused command that waits for you"}},
			example: "/answer", run: workspaceModel.cmdAnswer},
		{name: "/check", group: R, desc: "run Build & test now, even when nothing changed",
			forms:   []wsForm{{"/check", "run the language's build and test commands now"}},
			example: "/check", run: workspaceModel.cmdCheck},
		{name: "/stop", group: R, args: "[node]", desc: "stop a running node, or every node",
			forms:   []wsForm{{"/stop", "stop every running node (the S key)"}, {"/stop <node>", "stop one running node (the s key)"}},
			example: "/stop T3", run: workspaceModel.cmdStop},
		{name: "/retry", group: R, args: "<node>", desc: "run a failed or stopped node again",
			forms:   []wsForm{{"/retry <node>", "run the node again (the r key)"}},
			example: "/retry T3", run: workspaceModel.cmdRetry},
		{name: "/merge", group: R, desc: "merge this session's worktree branch into your branch",
			forms:   []wsForm{{"/merge", "merge the session branch into the branch you have checked out"}},
			example: "/merge", run: workspaceModel.cmdMerge},
		{name: "/diff", group: R, desc: "the full diff of this session",
			forms:   []wsForm{{"/diff", "the session branch against its base, or the working tree when the checkout is shared"}},
			example: "/diff", run: workspaceModel.cmdDiff},
		{name: "/pr", group: R, args: "[watch|stop]", desc: "push the session branch and open a pull request",
			forms: []wsForm{{"/pr", "push the session branch and open a pull request"},
				{"/pr watch", "open the pull request and fix CI failures and review comments"},
				{"/pr stop", "stop watching the pull request"}},
			example: "/pr watch", run: workspaceModel.cmdPR},

		// Agents
		{name: "/roles", aliases: []string{"/agents"}, group: A, args: "[save <name>]", desc: "list roles, create a new one",
			forms: []wsForm{{"/roles", "list the roles; Enter on + new role creates one"},
				{"/roles save <name>", "keep a role of this session in project.yaml"}},
			example: "/roles save docs", run: func(m workspaceModel, args string) (tea.Model, tea.Cmd) {
				if args != "" {
					return m.cmdRolesSave(args)
				}
				return m.openRoles(), nil
			}},
		{name: "/model", group: A, args: "[rank|profile]", desc: "roles' models, rankings and profiles",
			forms: []wsForm{{"/model", "panel of the roles with their runner, model and effort; Enter edits one, s saves"},
				{"/model rank", "fetch the model rankings from Artificial Analysis and show them"},
				{"/model profile", "the saved profiles; Enter applies one"},
				{"/model profile save <name>", "save the current roles as a profile"},
				{"/model profile apply <name>", "apply a saved profile to the project"}},
			example: "/model profile save fast", run: workspaceModel.cmdModel},
		{name: "/routing", group: A, args: "[mode]", desc: "how agents are picked for the nodes of a split",
			forms: []wsForm{{"/routing", "show the routing mode"},
				{"/routing <mode>", "auto-connected: any connected runner; auto-role: the role's own runner; manual: each role's agent"}},
			example: "/routing auto-role", run: workspaceModel.cmdRouting},
		{name: "/autonomy", group: A, args: "[1|2|3]", desc: "how much runs by itself",
			forms:   []wsForm{{"/autonomy", "show the level"}, {"/autonomy <1|2|3>", "1 manual, 2 assisted, 3 unattended (needs a budget and worktrees)"}},
			example: "/autonomy 2", run: workspaceModel.cmdAutonomy},
		{name: "/coordinator", group: A, args: "[on|off|now]", desc: "the lead reviews each finished batch",
			forms: []wsForm{{"/coordinator", "show whether the coordinator is on"}, {"/coordinator on|off", "turn it on or off for this session"},
				{"/coordinator now", "run one lead review now (y applies the proposals, n rejects them)"}},
			example: "/coordinator now", run: workspaceModel.cmdCoordinatorForm},

		// Usage
		{name: "/usage", group: U, args: "[cost|budget]", desc: "tokens, cost and the budget",
			forms: []wsForm{{"/usage", "panel: this session, the budget, the history of the project and the limits runners reported"},
				{"/usage cost", "this session, with a row per node"},
				{"/usage budget", "the Budget tab: spending per runner and the budget"}, {"/usage budget <amount|off>", "set the budget of this session, or remove it"}},
			example: "/usage budget 10", run: workspaceModel.cmdUsage},
		{name: "/status", group: U, desc: "version, runners, providers, run settings and warnings",
			forms:   []wsForm{{"/status", "one panel: runners and how they log in, providers, plugins, run settings, today's usage and the doctor warnings"}},
			example: "/status", run: workspaceModel.cmdStatus},
		{name: "/context", group: U, desc: "what the lead and the workers receive",
			forms:   []wsForm{{"/context", "the parts of the prompts with their size in characters and about how many tokens"}},
			example: "/context", run: workspaceModel.cmdContext},

		// Project
		{name: "/project", group: P, desc: "switch to another project",
			forms:   []wsForm{{"/project", "list the recent projects and open one"}},
			example: "/project", run: workspaceModel.cmdProject},
		{name: "/config", group: P, args: "[list|get|set]", desc: "show and change settings",
			forms: []wsForm{{"/config", "panel of the settings with their values; Enter edits one"}, {"/config get <key>", "show one setting"},
				{"/config set <key> <value>", "change a setting and save it in project.yaml"}},
			example: "/config set workspace.max_parallel 4", run: workspaceModel.cmdConfig},
		{name: "/provider", group: P, args: "[list|add|remove|test]", desc: "API keys of the providers",
			forms: []wsForm{{"/provider", "panel of the saved providers with masked keys: a add, x remove, t test"}, {"/provider list", "print the saved providers with masked keys"},
				{"/provider add <name>", "save an API key (deepseek, openai, openrouter, groq or custom)"},
				{"/provider remove <name>", "delete the saved key of a provider"},
				{"/provider test <name>", "check that the key works"}},
			example: "/provider add deepseek", run: workspaceModel.cmdProvider},
		{name: "/memory", group: P, args: "[search <query>|add <text>]", desc: "the project memory",
			forms: []wsForm{{"/memory", "show MEMORY.md, the pinned facts of the project"},
				{"/memory search <query>", "search the project memory"}, {"/memory add <text>", "add a fact to MEMORY.md"}},
			example: "/memory search build", run: workspaceModel.cmdMemory},
		{name: "/doctor", group: P, desc: "check the setup",
			forms:   []wsForm{{"/doctor", "run the setup checks and show each as ok, warning or failure"}},
			example: "/doctor", run: workspaceModel.cmdDoctor},
		{name: "/plugin", aliases: []string{"/plugins"}, group: P, args: "[list|search|install|…]", desc: "manage plugins",
			forms: []wsForm{{"/plugin", "open the plugin manager: Installed, Discover, Marketplaces and Stats"},
				{"/plugin list", "list the plugins with their state"},
				{"/plugin search <q>", "find plugins of the catalog and of the added marketplaces"},
				{"/plugin info <name>", "show what a plugin holds"},
				{"/plugin install <name>", "install a plugin of the catalog or of a marketplace, after a question"},
				{"/plugin enable <name>", "load a disabled plugin"},
				{"/plugin disable <name>", "stop loading a plugin"},
				{"/plugin remove <name>", "delete an installed plugin, after a question"},
				{"/plugin import <name>", "copy a plugin Claude Code has installed into ~/.decsr/plugins"},
				{"/plugin reload", "scan the plugin folders again"},
				{"/plugin stats", "nodes, tokens and cost of the plugins in this project's sessions"},
				{"/plugin marketplace add <owner/repo>", "add a GitHub repo with .claude-plugin/marketplace.json"},
				{"/plugin marketplace remove <owner/repo>", "forget a marketplace"}},
			example: "/plugin install superpowers", run: workspaceModel.cmdPlugin},
		{name: "/issue", group: P, args: "<number|URL|id>", desc: "start a task from a GitHub issue or a bead",
			forms:   []wsForm{{"/issue <number|URL|bead-id>", "read the issue or bead and split it into nodes"}, {"/issue <ref> --force", "start even when the session has nodes"}},
			example: "/issue 42", run: workspaceModel.cmdIssue},
		{name: "/import", group: P, args: "speckit [path]", desc: "add the open tasks of a Spec Kit tasks.md",
			forms:   []wsForm{{"/import speckit [path]", "add the open tasks of a Spec Kit tasks.md to the graph"}, {"/import speckit --phase 2", "only the tasks of one phase"}},
			example: "/import speckit --phase 2", run: workspaceModel.cmdImport},
		{name: "/menu", group: P, desc: "open the classic menu",
			forms:   []wsForm{{"/menu", "leave the workspace for the classic menu (setup, pipeline)"}},
			example: "/menu", run: func(m workspaceModel, _ string) (tea.Model, tea.Cmd) { return m.requestQuit(true) }},
	}
}

// groupMembers lists the commands of one group in table order.
func groupMembers(cmds []wsCommand, group string) []wsCommand {
	var out []wsCommand
	for _, c := range cmds {
		if c.group == group {
			out = append(out, c)
		}
	}
	return out
}

// groupTag is the short group name shown at the right of a palette row.
func groupTag(group string) string {
	if group == ui.GroupNamePlugin {
		return "Plugin"
	}
	return group
}

// cmdKey is a command name without its slash, lower case.
func cmdKey(name string) string { return strings.ToLower(strings.TrimPrefix(name, "/")) }
