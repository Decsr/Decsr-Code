# Workspace

The Workspace is the screen `decsr` opens by default. You type a task in the
prompt, a lead agent splits it into sub-tasks, you decide which agent does each
one, and the assigned sub-tasks run in parallel. Every sub-task is a node in a
graph, so you can see who does what and what is waiting on what.

It is one screen: the node graph and the inspector on top, the last lines of
the conversation under them, and the prompt at the bottom. Before the first
task the graph pane shows `No nodes yet.` and, if this project has saved
sessions, `N saved sessions - /resume`. Each project keeps its own sessions, so
you can leave and continue later (see [Sessions](#sessions)).

Nothing runs until you press a key. The classic pipeline (Context, Spec, Plan,
Code, QA and so on) is unchanged; see [Reaching the classic pipeline](#reaching-the-classic-pipeline).

## Flow

1. **Chat.** The prompt starts focused. No repository compaction runs at
   start; the lead only gets a cheap, locally collected project summary (plus
   `.decsr/context_pack.md` if one already exists). Type a task and press
   `Enter` (`Alt+Enter` adds a new line). A second task in the same session is
   added to the existing graph (see [Sessions](#sessions)).
2. **Split.** The lead (the `pm` agent's runner and model) splits the task into
   nodes. Each node has a title, a description, the nodes it depends on and a
   *suggested* role. If the reply is unusable (no result, a dependency cycle, a
   dependency on a node that does not exist) you see one error line and can
   send the task again. If the lead gives several sub-tasks but no role for any
   of them, it is asked once more for the complete answer. The lead's full raw
   answer of the last split is saved to `.decsr/workspace_split.txt`, so you can
   see what it actually said. After a successful split the focus moves to the
   graph and the first new node is selected.
3. **Assign.** Every node starts unassigned. Press `a` on a node to pick its
   role, or `A` to accept every suggestion that is a defined role. If the lead
   suggests a role you have not defined, the picker offers to create it.
4. **Run.** `r` runs the selected node, `R` runs every assigned node as soon as
   the nodes it depends on are done. Up to 3 nodes run at once.
5. **Check.** DECSR always adds a last node, **Build & test**, that depends on
   all the others and runs your language's build and test commands (no agent
   involved). See [Build & test and fix nodes](#build--test-and-fix-nodes) for
   when its commands are chosen.

## The node graph

Columns go left to right by dependency depth. Each box shows the node id, its
title, its role and its status. Lines join a node to the nodes that depend on
it.

```
 T1 Add schema     ─┐
 coder  ✓ done      ├─ T3 Write handlers ──┐
 T2 Config flags   ─┘   coder  ● running   ├─ CHECK Build & test
 qa     ✓ done                             │  ⏸ blocked
                        T4 Docs ───────────┘
                        ?  unassigned
```

A longer-range dependency (to a node that is not in the column directly to the
left) is not drawn; it is listed in the node's footer and in the inspector.
The inspector shows the selected node's description, role, dependencies, files,
warnings, summary or error, tokens and output; `i` shows or hides it. The
description gets all the room the other sections leave free, and the output
section keeps at least four lines while the node runs. A node's second line and
the inspector show the short model name (the part after the last `/`), with the
runner in front when it is not `opencode` (`claude:sonnet`).

### Node states

Status is always a glyph plus a word, never color alone.

| Glyph | State | Meaning |
|---|---|---|
| `?` | unassigned | No role yet. Press `a`. |
| `⏸` | blocked | Assigned, but a dependency is not done. |
| `○` | ready | Assigned and nothing is blocking it. |
| spinner | running | The agent is working. |
| `✓` | done | Finished; files it wrote are listed. |
| `!` | failed | The run failed; the error is shown. |

A `⚠` after the status in a node's title border means the node has warnings
(see [Warnings](#warnings)).

One node failing does not stop its siblings. Nodes that depend on it stay
blocked and say why.

## Keys

The screen has two focus areas, the prompt and the graph. It starts on the
prompt. `Tab` or `Shift+Tab` switches between them; the hint bar at the bottom
shows the keys of the area that has focus.

**Prompt focus.** Every printable key is text, so you can type `q`, `r`, `a`,
`?` or `/` anywhere in a task without triggering anything.

| Key | Action |
|---|---|
| `Enter` | Send the task, or run the command when the text starts with `/` (see [Commands](#commands)) |
| `Alt+Enter` | New line in the task |
| `/` | At the start of the text: open the command list |
| `Up` / `Down`, `Tab` | In the command list: move, complete the name |
| `Right` | Empty prompt with a suggestion shown: fill the prompt with it (does not send) |
| `Esc` | Hide the suggestion; close the command list; otherwise go to the graph (when it has nodes) |
| `PgUp` / `PgDn` | Scroll the conversation |
| `?` | Help (only when the prompt is empty) |
| `Ctrl+C` | Quit (press twice) |

**Suggestions.** An empty prompt shows a dim italic suggestion in place of
"Type a task or / for commands", and the hint bar adds `→ accept suggestion`.
It is picked from the session state, with no model call, and only while no
node runs and no overlay is open:

1. Build & test failed and has no fix node: `fix the failing build and tests`
2. Every node is done: `/compact` with 6 or more done nodes, otherwise
   `review the changes and add any missing tests`
3. A fresh session in a project with saved sessions: `/resume`

`Right` fills the prompt with it; `Enter` then sends or runs it. `Esc` hides
it. It stays hidden until the graph or the saved sessions change.

**Graph focus.** Single keys act on the selected node.

| Key | Action |
|---|---|
| arrows or `h j k l` | Move between nodes |
| `Enter` or `o` | Open the full output of the node |
| `i` | Show or hide the inspector. Under 100 columns it opens as a box over the graph; `i` or `Esc` closes it |
| `a` | Pick the role for the selected node |
| `A` | Accept all suggestions that are defined roles |
| `r` | Run the selected node |
| `R` | Run every assigned node as dependencies finish |
| `s` | Stop the selected node |
| `S` | Stop all running nodes |
| `f` | Add a fix node after Build & test has failed |
| `/` or `c` | Go to the prompt (`/` also types the slash) |
| `Tab` | Go to the prompt |
| `?` | Help |
| `q` | Quit (asks first while nodes run) |

In the output view: `Esc` or `q` returns to the graph, `s` stops the node, and
the arrow and page keys scroll. The old `n` key (new task) is gone; use
`/clear`.

Quitting while nodes run asks for confirmation, then stops them and kills the
agent processes.

## Commands

Type `/` at the start of the prompt to filter the list by prefix, move with
`Up`/`Down`, complete with `Tab`, run with `Enter`. `Enter` runs a command only
when the text is an exact command name or alias, when you moved the highlight
with `Up`/`Down` (or completed with `Tab`), or when exactly one command matches
the prefix. Otherwise it prints `! /co matches several commands: /compact,
/config, /cost - keep typing or press Tab` and leaves the text alone. An
unknown command prints `unknown command /x - /help lists them`.

**Tasks that start with a slash.** A single line whose first word contains a
second `/` (for example `/usr/lib is missing libfoo, fix the build`) is sent as
a task, not run as a command. To send a task that starts with a command name,
double the slash: `//compact the logs` sends `/compact the logs`. A text pasted
into the prompt (or typed in one burst) is inserted as text; it is never sent or
run until you press `Enter`. With graph focus, a paste moves the focus to the
prompt and lands there.

| Command | What it does |
|---|---|
| `/help` | Keys and the full command list, including custom commands |
| `/clear` | Save this session and start a new, empty one (asks first when nodes are unfinished) |
| `/compact` | Fold finished nodes into the session summary (see [Compact](#compact)) |
| `/resume` | Pick a saved session of this project |
| `/project` | Switch to another project (see [Switching project](#switching-project)) |
| `/profile` | Save or apply a profile of role settings (see [Profiles](#profiles)) |
| `/roles` (`/agents`) | List all roles, with their skills, and create a new one |
| `/plugin` (`/plugins`) | List plugins; `/plugin enable <name>`, `/plugin disable <name>` and `/plugin reload` (see [Plugins](#plugins)) |
| `/status` | Project path, language, profile, session, node counts, roles with runner/model/effort, parallel limit, custom commands and skills found |
| `/cost` | Tokens in and out per node and per role, the lead's usage, and the total |
| `/export` | Write the session to `.decsr/exports/<session id>.md` (title, transcript, summary, each node with role, status, files and summary) |
| `/config` (`/menu`) | Leave for the classic menu: settings, project setup, classic pipeline |
| `/quit` (`/exit`) | Leave the Workspace (asks first while nodes run) |
| `/<name> [args]` | A [custom command](#custom-commands-and-skills) |
| `/<plugin>:<name> [args]` | A command of a [plugin](#plugins) |

`/clear`, `/compact`, `/project`, `/resume` and applying a profile are refused
while nodes are running: `stop the running nodes first`. Saving a profile stays
allowed.

## Sessions

A session is one conversation in one project: the transcript, a running
summary, and one graph. It is saved after every change to
`<project>/.decsr/sessions/<id>.json` (mode 0600). The id looks like
`20261113-104512-a1b2`. The session list of one project never shows another
project's sessions.

- A new session is not written to disk until it has a line.
- The transcript keeps the last 500 lines.
- A second task in the same session is **appended** to the graph: new nodes
  continue the numbering (`T5`, `T6`, ...), numbers are never reused, and the
  lead is shown the session summary and the current nodes. The lead is told to
  number new sub-tasks from the next free number and may list an existing node
  in `depends_on` when the new work needs it; such a dependency is kept as is
  (a dependency on an ID that is neither new nor in the graph still rejects the
  split). Build & test is reset to not started and waits for every work node
  again.
- `/clear` saves the session and starts a new one. The old one stays in `/resume`.
- `/resume` lists the project's sessions newest first (`title - 3/7 done - 2h
  ago`, the current one marked). `Enter` loads one (the current session is saved
  first), `d` deletes one after `y/n`, `Esc` closes. Nodes that were running
  when you left come back as not started.
- A broken session file is skipped in the list.
- `decsr resume` opens the Workspace with this list already showing.
- The first time a project with an old `.decsr/workspace.json` is opened, it is
  wrapped in a session and renamed to `workspace.json.migrated`.

### Compact

`/compact` makes no model call. A finished work or fix node (status done) is
removed from the graph and written into the session summary as one line:
`- T1 <title> [<role>]: <first 400 characters of its summary> (files: a, b)`,
but only when no unfinished node depends on it (every node that depends on it is
folded too). A finished node that a todo, running or failed node still needs
stays, so that node keeps the work it builds on; the notice says how many were
kept, for example `compacted 3 finished nodes; kept 1 that unfinished nodes
still depend on`. Nodes that stay keep their edges to each other; a dependency
on a removed node is dropped. Build & test stays. The summary is capped at
12,000 characters (oldest lines go first). It is given to the lead on the next
task and to every worker as an `Earlier in this session` section of its context
(the most recent 4,000 characters). Running it again with nothing to fold says
`nothing to compact`.

## Profiles

A profile is a named set of per-role settings (runner, model, effort,
description, `read_only`, skills), stored once for all projects in
`~/.decsr/profiles/<name>.yaml`:

```yaml
agents:
  coder:
    runner: claude
    model: sonnet
  qa:
    runner: opencode
    model: ollama/qwen3
    skills: [house-style]
  docs_writer:
    description: Writes user-facing documentation.
    read_only: true
```

`/profile` opens a list of saved profiles. `Enter` applies one: its roles are
written into the current project's `.decsr/project.yaml` (roles not in the
profile are left alone; an empty runner, model, effort or description in the
profile keeps the project's value), `profile: <name>` is recorded under `project:`, and
the graph's role labels are refreshed. Applying is refused while nodes run,
because it would switch their models in the middle of a batch. The row `save current roles as...` asks
for a name and saves each role's own configured values: a custom role that only
inherits its runner and model from `pm` is saved without them, and built-in
roles are saved without `description` and `read_only` (those are built in). `d` deletes a profile after `y/n`.
Names are 1-32 characters: lowercase letters, digits, `-` and `_`, starting with
a letter or digit. A profile that names an unknown runner is refused and nothing
is written. The summary line shows `profile: <name>` when one has been applied.

A project file or profile can only add to the global configuration: it cannot
clear a globally configured `skills` list (a non-empty list replaces it) or a
`read_only: true` flag (a missing or `false` value changes nothing).

## Switching project

`/project` lists the current project first, then recent projects (a directory
that no longer exists, or is a file, is dimmed with `(missing)` and cannot be
chosen), and a last row
`open path...` that takes a path (`~` is expanded; it must be an existing
directory; only a trailing line break is trimmed, spaces in the path count). Choosing one saves the session, changes the working directory and
reopens the Workspace there, with that project's config and sessions. The
projects you open are recorded in `~/.decsr/projects.json`, the same list the
classic project picker uses. If DECSR cannot change into the directory it prints
the error and reopens the Workspace in the current project.

## Custom commands and skills

Both are plain markdown files. A project file wins over a user file of the same
name. Names must match `[a-z0-9][a-z0-9_-]{0,31}`; files with other names, empty
files and files over 64 KB are ignored. Files are read when the command list
opens, so new ones appear without a restart.

**Command.** `<project>/.decsr/commands/<name>.md` or
`~/.decsr/commands/<name>.md` defines `/<name>`. Running it sends the file's
text as a task, with `$ARGUMENTS` replaced by what you typed after the name (if
the file has no `$ARGUMENTS`, the arguments are appended). A first line of
`# text` becomes the description in the command list; otherwise the first 60
characters are used. A custom command never replaces a built-in one.

```markdown
# Add tests for a package
Write table-driven tests for $ARGUMENTS.
Cover error paths. Do not change non-test files.
```

`/addtests ./internal/config` sends that text with the path filled in.

**Skill.** `<project>/.decsr/skills/<name>.md` or `~/.decsr/skills/<name>.md` is
a document a role can load. List it on the role:

```yaml
agents:
  coder:
    skills: [house-style]
```

and `.decsr/skills/house-style.md` holds, for example:

```markdown
Use `errors.Is`, never compare error strings.
Wrap errors with `%w`. Keep functions under 60 lines.
```

The text is added to the prompt of that role's nodes. A skill that is listed but
missing (or too large) does not fail the node; it shows as a warning on the
node.

## Plugins

DECSR loads Claude-format plugins: a folder with `.claude-plugin/plugin.json`
(`name` is required and must match `[a-z0-9][a-z0-9_-]{0,31}`; `description` and
`version` are shown). Put the folder in `<project>/.decsr/plugins/<dir>/` or
`~/.decsr/plugins/<dir>/`; a project plugin wins over a user plugin of the same
name. A plugin must be a real directory (a symlink is skipped). Files are read
only when they are regular files without NUL bytes and at most 64 KB. A
`plugin.json` that cannot be parsed skips the plugin and shows as a warning in
`/plugin`.

`commands/`, `skills/` and `agents/` are always scanned. `plugin.json` may also
list `commands`, `skills` or `agents` (a path or an array of paths relative to
the plugin; absolute paths and paths leaving the plugin are ignored), which are
scanned in addition.

- **Commands.** `commands/<file>.md` is the command `/<plugin>:<file>`. Its
  description is the frontmatter `description` (else the first `# heading`, else
  the first 60 characters); `argument-hint` is shown in the command list.
  `$ARGUMENTS` works as in custom commands. A built-in or custom command is never
  replaced.
- **Skills.** `skills/<name>/SKILL.md` is the skill `<plugin>:<name>`. List it on
  a role like any skill (`skills: [superpowers:brainstorming]`); the text after
  the frontmatter is added to the prompt. A skill of a missing or disabled
  plugin shows the usual `skill "..." not found` warning on the node.
- **Agents.** `agents/<file>.md` is the role `<plugin>:<file>`, offered to the
  lead and in the agent picker next to your own roles. The body (first 16 KB)
  is the role prompt; the runner, model and effort are those of `pm`. The role is
  read-only when the frontmatter has a `tools` list without `Write`, `Edit`,
  `MultiEdit` or `NotebookEdit`. Plugin roles are never written to `config.yaml`,
  `project.yaml` or a profile.

Markdown files may start with YAML frontmatter between `---` lines. The same
frontmatter works in `.decsr/commands/*.md`. A header that is not valid YAML is
read line by line instead: each `key: value` line at the start of a line is used
as it is. Only a header with no closing `---` is left in the text.

`/plugin` lists each plugin with its version, scope, state, commands, skills and
agent roles. A plugin with `hooks/`, `.mcp.json` or `mcpServers` is marked:
hooks and MCP servers are not used by decsr. `/plugin disable <name>` and
`/plugin enable <name>` change `~/.decsr/plugins/disabled` (one name per line)
and reload; `/plugin reload` rescans the folders. These are refused while nodes
are running, and a node assigned a role that is gone loses that assignment.
### Recommended set

`scripts/install-plugins.sh` (needs git; `--dry-run` previews, `--uninstall`
removes only what it installed) copies superpowers, ui-ux-pro-max,
modern-cpp, modern-python, insecure-defaults, differential-review,
feature-dev and pr-review-toolkit into `~/.decsr/plugins`, and writes a
`plugins` profile that gives the built-in roles skills:

| Role | Skills |
|---|---|
| pm | brainstorming, writing-plans |
| coder | test-driven-development, modern-cpp, modern-python |
| coder_fixer | systematic-debugging |
| qa | verification-before-completion, test-driven-development |
| security | differential-review |
| ux_reviewer | ui-ux-pro-max |

The profile lists only `skills` (the role's existing skills from
`~/.decsr/config.yaml`, then the plugin ones) and `read_only` (copied when
true). It has no runner, model, effort or description, and an empty value in a
profile keeps what the project already has, so applying it only adds skills.
Open decsr, run `/plugin` to see the plugins and `/profile plugins` to apply
the profile.

## Roles

A role is a name, a one-line description and a runner, model and effort. The
six built-in roles are `pm`, `coder`, `coder_fixer`, `qa`, `ux_reviewer` and
`security`; they keep their own prompts. Any other key under `agents:` in
`.decsr/project.yaml` is a custom role:

```yaml
agents:
  docs_writer:
    description: Writes and updates user-facing documentation.
    runner: claude        # optional
    model: sonnet         # optional
    effort: default       # optional
    read_only: true       # optional; default false
```

A custom role without a runner or model uses the `pm` agent's. Role names are
2-32 characters: lowercase letters, digits and `_`, starting with a letter.
Create roles from the assignment picker or `/roles`, or edit the file by hand.
Project Setup (`s` on the Home menu) lists custom roles after the built-ins, lets
you change their runner, model and effort, and keeps every custom role, plus the
description and `read_only` flag of all roles, when it saves.

### Read-only roles

A role with `read_only: true` reports and never changes files: whatever file
blocks its reply contains are discarded, and the prompt tells it so. `ux_reviewer`
and `security` are always read-only. The role form has a third field, **Can
change files** (`yes` by default; toggle with `←`/`→` or `Space`), and a role
saved with `no` gets `read_only: true`. The picker and the inspector show
`read-only` next to such roles. Assigning one is allowed; DECSR says
`<role> is read-only: it will report, not change files`. A read-only node never
gets the "no files were written" warning.

If you remove a role from the config after assigning it, the node becomes
unassigned again the next time the graph is loaded.

Every node runs through one generic worker: the role's prompt plus the
sub-task. Files the agent writes are saved into the project, inside the project
root. Writes to `.git/` and `.decsr/` are always refused, and so are paths that
`project.scope.deny` matches or that `project.scope.allow` (when set) does not
match, symlinks and paths that resolve outside the root. A refused write is not
fatal when other files were written; it shows up as a warning on the node.

Later nodes are shown the current content of the files written by the nodes
they depend on (directly or through other nodes), so they build on what is on
disk, including changes made by agents that ran after those nodes. A fix node
sees the files of every finished node. The listing is capped per file and in
total; files left out are named.

Only file blocks with an explicit path (a bold path line before the code block,
or the path in the fence tag) are written by the worker. A code block without
a path is treated as an illustration and ignored; DECSR never guesses a file
name from its content here.

Agents that run through a CLI (for example `opencode run`) edit files in the
project themselves. DECSR compares the project tree before and after each work
node (skipping `.git`, `.decsr`, `node_modules`, `__pycache__`, `build`, `dist`,
`target`, `bin`, `obj`, `.venv` and `venv`; the check is skipped for projects
with more than 20,000 files) and lists new or changed files that were not
written from file blocks as "by the agent's own tools" in the inspector. They
also count as the node's files for the context of later nodes. Because nodes can
run in parallel, a file changed by another running node may be listed too.

## Parallel runs

```yaml
workspace:
  max_parallel: 3   # nodes running at the same time; default 3
```

The lead is told to make nodes that run side by side touch different files and
to declare a dependency otherwise. If two nodes that ran at the same time wrote
the same file, both get a warning.

### Warnings

A node can finish with warnings: a file the agent wrote that was refused
(`not written: <path>: <reason>`), a work node that wrote no files
(`no files were written`), a file also written by a node that ran in parallel,
a read-only role that changed files on disk
(`read-only role changed files: <paths>`), or a Build & test node with no
commands to run. The parallel-overlap warning only looks at files written from
file blocks, not at files an agent changed with its own tools. Warnings do not fail the node.
They show as `⚠` in the node's title border (when it fits), in a `WARNINGS`
section of the inspector above the summary, and as `N warnings` in the summary
line at the top of the graph.

## Build & test and fix nodes

The `CHECK` node ("Build & test") needs no agent. It is always in the graph. It
runs the build and test commands for the project's language (see Project Setup
for the language) once every other node is done.

When the task is split, DECSR resolves those commands if it can and stores them
on the node. If none are known yet (for example the project has no files), the
node is added without commands and they are resolved when it runs, so a
language that is detected by then is used. If there are still none when it
runs, it finishes with a warning instead of passing silently. If it fails, select it and press `f`: DECSR adds a
fix node (`F1`, `F2`, ...; a number is never reused in a session, not even after
`/compact`) that receives the failure output, suggested role
`coder_fixer`. Assign it, run it, and Build & test is queued again behind it.

## Where state is stored

Graph and conversation are saved with the session, in
`.decsr/sessions/<id>.json`, after every change. If you quit or crash with
unfinished work, run `decsr resume` or type `/resume` to reopen it. Nodes that
were running are reloaded as not started. The Workspace does not use the
classic pipeline's run state or beads issues. Profiles live in
`~/.decsr/profiles/`, custom commands and skills in `.decsr/commands/` and
`.decsr/skills/` (project) or the same folders under `~/.decsr/` (user).

## Reaching the classic pipeline

- Type `/config` (or `/menu`) in the Workspace to open the Home menu. Its `CLASSIC PIPELINE`
  item (`1`) starts the old New Task flow; `WORKSPACE` (`w`) returns here.
- Changes made in the menu (settings, a different project) apply when you come
  back to the Workspace, which then opens in the selected project.
- `decsr task ...` and `decsr run --requirements ...` behave as before, and
  `--ui plain` never opens the Workspace.
