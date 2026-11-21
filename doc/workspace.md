# Workspace

The Workspace is the screen `decsr` opens by default. It is a chat with a lead
agent. Ask it about the project and it answers; give it work and it splits the
work into sub-tasks, you decide which agent does each one, and the assigned
sub-tasks run in parallel. Every sub-task is a node in a
graph, so you can see who does what and what is waiting on what.

It is one screen. The top line shows `◆ decsr`, the project and, when nodes run
in worktrees, the session branch, with the progress (`2/5 done · 1 failed ·
44.4k tok`) dim on the right; the session title is the bold line under it. Then
come the node graph and the inspector, separated by one faint divider, the last
lines of the conversation, the prompt in a rounded box and a hint bar. Panes
have no frame: each is named by a dim label line (`Graph`, `Inspector`), and
only node cards, overlays and the prompt have (rounded) borders. When the
graph needs fewer rows than the screen gives it, the conversation takes the
spare rows and shows more of the chat. Before the first
task the graph pane shows `No nodes yet.` and, if this project has saved
sessions, `N saved sessions - /resume`. Each project keeps its own sessions, so
you can leave and continue later (see [Sessions](#sessions)).

Nothing runs until you press a key. The classic pipeline (Context, Spec, Plan,
Code, QA and so on) is unchanged; see [Reaching the classic pipeline](#reaching-the-classic-pipeline).

## Flow

1. **Chat.** The prompt starts focused. No repository compaction runs at
   start; the lead gets a short project map and reads the files itself (see
   [The lead chat](#the-lead-chat)). Type a message and press
   `Enter` (`Alt+Enter` adds a new line). A second task in the same session is
   added to the existing graph (see [Sessions](#sessions)); you can send it
   while nodes run (see [Chatting while nodes run](#chatting-while-nodes-run)).
2. **Split.** When the message assigns work, the lead (the `pm` agent's runner
   and model) splits it into
   nodes. Each node has a title, a description, the nodes it depends on, the
   role the lead gives it and a `writes` flag (see [Lean runs](#lean-runs)).
   If the message is a question or asks for an opinion, the lead
   answers it in the chat and adds no nodes. If the reply is unusable (no result, a dependency cycle, a
   dependency on a node that does not exist) you see one error line and can
   send the task again. If the lead gives several sub-tasks but no role for any
   of them, it is asked once more for the complete answer. The lead's full raw
   answer of the last split is saved to `.decsr/workspace_split.txt`, so you can
   see what it actually said. After a successful split the focus moves to the
   graph and the first new node is selected.
3. **Assign.** The lead assigns each node a role that is defined, so the node
   is ready as soon as the plan is approved. Press `a` on a node to pick another
   role. A role the lead invents is shown on the plan card as
   `new · uses the pm's agent`; `y` creates it for this session (see
   [Approval](#approval)). `A` still accepts suggestions that are defined roles.
4. **Run.** After `y` on the plan card the nodes start; `r` runs the selected
   node, `R` runs every assigned node as soon as the nodes it depends on are
   done. Up to 3 nodes run at once. A plan of read-only nodes starts at once.
5. **Check.** When a node writes, DECSR adds a last node, **Build & test**,
   that depends on all the others and runs your language's build and test
   commands (no agent involved), but only if a node changed a file that is not
   documentation. See [Build & test and fix nodes](#build--test-and-fix-nodes)
   for when its commands are chosen and when it is skipped.

## Layout

From 100 columns the screen has a **sidebar** on the left (about 22% of the
width, 28 to 34 columns), a single `│` divider and the **main pane**. Under 100
columns there is no sidebar; `b` shows it as a box over the left side.

```
 Add JSON output to the vers…  │  Activity · g graph
 2/5 · 1 failed                │  You  17:00
 ✓ T1 Parse the --json flag    │    add a --json flag to the version command
    ⬡ claude:sonnet        2m  │  ╭────────────────────────────────────────╮
 ! T3 Tests for --json         │  │ ✓ T1 Parse the --json flag    17:01 ↗ │
    ⬡ claude:sonnet    failed  │  │ Worked for 1m 10s                      │
 ? T4 Document the flag        │  │ Edited cmd/version.go           +18 −2 │
    → docs                     │  ╰────────────────────────────────────────╯
```

**Sidebar.** The session title and progress (`2/5 · 1 failed`) on top, then
two rows per node: the status glyph, id and title, and under it the runner
glyph, short model and the age on the right (`now` while running, then `2m`,
`1h`, `3d` since it ended; `failed` for a failed node; an unassigned node shows
`→ role` for the lead's suggestion). While a node runs, the second row shows
what it is doing instead (`▸ go test ./…`, `✎ cmd/version.go`, `· thinking…`);
after 60 s without activity it shows `no activity for 2m` in the warning colour. Below a divider, `Sessions` lists up to
five other saved sessions of the project (`○ title  2/5`); `Enter` on one
resumes it like `/resume` does, and is refused while nodes run. The list
scrolls when it is taller than the pane.

**Main pane.** It has two modes; `g` switches between them and the pane label
shows the other one (`Activity · g graph`, `Graph · g activity`).

- **Activity** (default) is a feed in time order. Your messages, the lead's
  answers and system lines read like a chat. Each node that has started gets a
  card at the time it started: the status glyph, id, title, runner, model and
  start time; `Working for 37s · ctx 18k` and its last 5 activity steps while it
  runs (the last output line for a node without activity); `Worked for 1m 10s ›`
  folded, the summary (3 lines at most) and the files it changed once done
  (`Edited cmd/version.go   +18 −2`, `Added` for a new file, at most 5 rows and
  then `+3 more files`); `Failed after 2m`, the error and the keys that help on
  failure. The counts come from git and exist when the node ran in its own
  worktree; otherwise only the file names are shown. The selected card has an
  accent border and a failed one a red border. The feed follows the newest
  item until you scroll up.

  **Activity steps.** Every step is one line, a glyph, the text and a dim meta
  on the right: `▸ go test ./…  12s` (then `✓ 3.2s`, or `✗ exit 1 · 3.2s`),
  `✎ Edited cmd/version.go  +18 −2`, `◦ Read cmd/version.go`,
  `⌕ Searched "flag.Parse"`, `⚙ <tool>`, `◷ Waiting · <reason>`, `! <error>`,
  `› <message>` and `· thinking… "first words"`. A running step is accent
  coloured, a failed one red. When the node ends, the steps fold into `Worked
  for 1m 12s ›`; `Space` expands them into the whole saved timeline (at most 200
  steps, `… 37 earlier steps` when older ones were dropped, the thinking text
  wrapped under its line). `ctx 18k` is the estimated size of the worker prompt;
  `ctx 18k / 128k` shows when the model limit is known. Nodes of older sessions
  keep the old card: `Space` shows the last 15 output lines.

  **Stall.** A running node with no activity for 60 s shows
  `no activity for 1m` (minutes rounded down) on its card and in the sidebar.
  After 5 minutes one `needs you` notification is sent for that stall
  (`T3 has had no activity for 5m`); a new one follows only after the node was
  active again.
- **Graph** is the node graph below, with the conversation under it and the
  inspector beside it when there is room (the main pane is at least 100 columns).


A card and a sidebar row always show the runner glyph and the model the node
runs. A role without a model of its own uses the lead's model when both use the
same runner; if none is known, the runner name is shown instead (`⬡ opencode`).
The card header ends with `⬡ model · 17:01 ↗`.

**Bottom bar.** One dim line under the key hints: the tokens used per runner
(`⬡ opencode 44.4k  ◆ deepseek 12.1k`), then the cost (`$0.38 / $5.00`, or
`$0.38` without a budget, or `tokens only` when no price is configured), and on
the right the session branch and pull request (`⎇ decsr/… · PR #12 ● checks
passing`). On a narrow terminal the runners, then the branch, drop out first.
The cost turns yellow at the warn share of the budget and red at 100%.

**Prompt chips.** The last row inside the prompt box shows dim chips:
`⚠ worktree` or `shared` (the isolation of the session), the lead's runner and
model (`◆ deepseek-v4-flash`) and, when a budget is set, `$0.38/$5`. The chips
are display only; change the lead's model in `/config`. They are hidden when
the terminal is too short for a two-line prompt (under 24 rows).

In Activity mode there is no inspector pane; `i` opens it as a box for the
selected node. One node is selected at a time, shared by the sidebar, the feed
and the graph.

## The node graph

Columns go left to right by dependency depth. Each node is a rounded card: the
first row has the id (dim) and the title (bold), the second row has the runner
glyph, the role and the short model (dim) on the left and the status on the
right. Lines join a node to the nodes that depend on it. The selected card is
filled and has an accent border; a failed card has a red border.

```
╭────────────────────────────────╮      ╭────────────────────────────────╮
│ T1  Parse the --json flag      │──┬──▶│ T3  Tests for --json           │
│ ✳ coder · sonnet        ✓ done │  │   │ ⬡ qa                 ! failed  │
╰────────────────────────────────╯  │   ╰────────────────────────────────╯
```

The card width follows the graph pane: the widest card (18 to 44 cells) that
lets every column fit. Titles are cut only when that is not enough. When even
18 cells per column do not fit, the graph shows only whole columns and scrolls
sideways to follow the selected node; a dim `+2 ›` at the right edge (or
`‹ +1` at the left) counts the columns hidden on that side.

A node with no role yet shows `→ role` in dim on the left of its second row,
the role the lead suggested, and `? unassigned` on the right. On a narrow card
the role name is cut first; the arrow and the status never are.

A longer-range dependency (to a node that is not in the column directly to the
left) is not drawn; it is listed in the node's footer and in the inspector.
The inspector shows the selected node's description, role, dependencies, files,
warnings, summary or error, tokens and output; `i` shows or hides it. The
description gets all the room the other sections leave free, and the output
section keeps at least four lines while the node runs. Section labels are dim
title-case words (`Role`, `Description`, `Files`, `Summary`, `Tokens`,
`Output`). A node's second line and the inspector show the runner glyph and the
short model name (the part after the last `/`).

### Runner glyphs

The glyph tells which runner a role uses. The glyph set is the `ui.icons`
setting (`unicode`, `ascii` or `nerd`; default `unicode`):

```yaml
# .decsr/project.yaml or ~/.decsr/config.yaml
ui:
  icons: ascii
```

| Runner | `unicode` | `ascii` | Colour |
|---|---|---|---|
| claude | `✳` | `[C]` | orange |
| codex | `◎` | `[X]` | muted |
| opencode (the default) | `⬡` | `[O]` | muted |
| copilot | `◈` | `[G]` | muted |
| deepseek | `◆` | `[D]` | blue |
| openai | `○` | `[A]` | muted |
| ollama, lmstudio, mlx (local) | `●` | `[L]` | green |
| any other | `·` | `[?]` | muted |

`nerd` uses Nerd Font code points and falls back to the `unicode` glyph for a
runner that has none.

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
| `◇` | waiting | The node ended with a question or a blocked report and waits for your reply (see [Node reports and waiting nodes](#node-reports-and-waiting-nodes)). |

A `⚠` after the status on a node card means the node has warnings
(see [Warnings](#warnings)).

One node failing does not stop its siblings. Nodes that depend on it stay
blocked and say why.

## Keys

The screen has three focus areas: the prompt, the sidebar (when shown) and the
main pane (the feed or the graph). It starts on the prompt. `Tab` goes prompt,
sidebar, main pane and back to the prompt; `Shift+Tab` goes the other way. Under
100 columns the sidebar is skipped. The hint bar at the bottom shows the keys of
the area that has focus.

**Prompt focus.** Every printable key is text, so you can type `q`, `r`, `a`,
`?` or `/` anywhere in a task without triggering anything.

| Key | Action |
|---|---|
| `Enter` | Send the task, or run the command when the text starts with `/` (see [Commands](#commands)) |
| `Alt+Enter` | New line in the task |
| `/` | At the start of the text: open the command list |
| `@` | At the start of the text: list the work and fix nodes (`T3 · title · status`); type to filter |
| `@T3 message` | Reply to node T3 (see [Reply to a node](#reply-to-a-node)) |
| `Up` / `Down`, `Tab` | In the command list or the node list: move, complete the name (`Enter` also completes a node) |
| `Right` | Empty prompt with a suggestion shown: fill the prompt with it (does not send) |
| `Esc` | Hide the suggestion; close the command list; otherwise go to the main pane (when it has nodes) |
| `PgUp` / `PgDn` | Scroll the activity feed (the conversation in Graph mode) |
| `?` | Help (only when the prompt is empty) |
| `Ctrl+K` | Open the go-to palette (see [Palette](#palette)); works in every focus area |
| `Ctrl+C` | Quit (press twice) |

**Suggestions.** An empty prompt shows a dim italic suggestion in place of
"Type a task, / for commands", and the hint bar adds `→ accept suggestion`.
It is picked from the session state, with no model call, and only while no
node runs and no overlay is open:

1. Build & test failed and has no fix node: `fix the failing build and tests`
2. Every node is done: `/compact` with 10 or more done nodes, otherwise
   `review the changes and add any missing tests`
3. A fresh session where a `specs/*/tasks.md` has open tasks (see
   [Auto-detect](#import-from-spec-kit)): `/import speckit`
4. A fresh session in a project with saved sessions: `/resume`

`Right` fills the prompt with it; `Enter` then sends or runs it. `Esc` hides
it. It stays hidden until the graph or the saved sessions change.

**Main pane and sidebar focus.** Single keys act on the selected node. The
node keys work with focus on the sidebar or on the main pane, in either mode.

| Key | Action |
|---|---|
| arrows or `h j k l` | Graph mode: move between nodes. Activity mode: select the previous or next card. Sidebar: move through the nodes, then the saved sessions |
| `PgUp` / `PgDn`, `Ctrl+U` / `Ctrl+D` | Activity mode: scroll the feed a page |
| `G` | Activity mode: jump to the newest item and follow it again |
| `Space` | Activity mode: expand or collapse the selected card (its whole activity timeline; the last 15 output lines for a node without activity) |
| `g` | Switch the main pane between Activity and Graph (text in the prompt) |
| `b` | Hide or show the sidebar; under 100 columns it opens as a box (`Esc` or `b` closes it) |
| `Enter` or `o` | Open the node: its step timeline and its changes (in the sidebar, on a saved session: resume it) |
| `i` | Show or hide the inspector. In Graph mode with a main pane of 100 columns or more it sits beside the graph; otherwise, and in Activity mode, it opens as a box; `i` or `Esc` closes it |
| `v` | Show or hide the live pane under the graph; it appears while the selected node runs, on a terminal 30 rows or taller |
| `a` | Pick the role for the selected node |
| `A` | Accept all suggestions that are defined roles |
| `d` | Open the diff of the selected node (see [Diff and review notes](#diff-and-review-notes)) |
| `D` | Open the diff of the whole session branch |
| `m` | Reply to the selected node: focus the prompt filled with `@T3 `. It answers the question of a waiting node `◇` (see [Reply to a node](#reply-to-a-node)) |
| `!` | Open a shell in the selected node's worktree (tmux split under tmux; see [Agent mode](#agent-mode)) |
| `K` | Choose the plugin skills of the selected node (see [How the lead picks skills](#how-the-lead-picks-skills)) |
| `r` | Run the selected node |
| `R` | Run every assigned node as dependencies finish |
| `s` | Stop the selected node |
| `S` | Stop all running nodes |
| `f` | Add a fix node after Build & test has failed |
| `N` | Open the inbox: what needs you (see [Inbox](#inbox)) |
| `/` or `c` | Go to the prompt (`/` also types the slash) |
| `Tab` / `Shift+Tab` | Next / previous focus area (see above) |
| `?` | Help |
| `q` | Quit (asks first while nodes run) |

**Live pane.** While the selected node runs, a pane about 10 rows tall sits at
the bottom of the graph. It starts with the running line, `⠋ claude · opus-5-5
· effort medium · 1m 12s · ↑ 182k (164k cached) ↓ 2.1k` (`~` marks estimated
tokens, the effort and cached parts are left out when empty), then one
timeline of the node's text and steps in the order they happen, as many of the
newest lines as fit, with the current step on a spinner. Text wraps to the
pane; thinking shows as a single `thinking…` line, never its text. On claude the
text streams token by token; opencode and dsh show it per message, and codex
shows it at the end. The chat still gets only the final answer. From 100 columns the files edited
so far with `+`/`−` counts (below that, `N files changed`). `v` hides or shows
it for the session; `i` still toggles the inspector only. Running cards show
`· 184k tok` after the duration.

**Output view.** `o` or `Enter` opens the node: the running line (frozen with
`✓` or `✗` and the final time for a finished node) over the timeline of the
node and its changes. From 120 columns the timeline is on the left and the
changes on the right (the file list with `+`/`−`, under it the diff of the
selected file with syntax colours) and `Tab` moves the focus between them;
narrower, one pane shows at a time and `Tab` switches. In the changes pane the
arrows scroll, `n`/`p` pick the next or previous file and `←`/`→` pan. While
the node runs the changes reload on each file edit and every 2 seconds, keeping
the selected file and the scroll; a finished node shows its commit. `d` and
`D` open the full-screen diff with review notes.
The header shows the node and its status, and `ctx`. `j`/`k` or the arrows move the
cursor over the steps (`g`/`G` jump to the first and last), `Enter` or `Space`
expands the step under the cursor to its detail (the full text while decsr
still holds it, else the 500 characters saved in the session), and `t` toggles
the raw output text. A node without activity but with output (an old session)
opens in raw mode. After a reply the node has earlier attempts: `[` and `]` show the
previous and next attempt (its summary or error and its timeline, when that
was kept), and `]` past the last earlier attempt returns to the current one.
`Esc` or `q` returns, `s` stops the node, and `PgUp`/`PgDn` scroll.

The old `n` key (new task) is gone; use `/clear`.

Quitting while nodes run asks for confirmation, then stops them and kills the
agent processes.

## Inbox

The first line of the sidebar shows `● 3 need you` in the warning colour while
something needs you, and nothing when it does not. `N` (graph, activity or
sidebar focus; in the prompt `N` is text) opens the inbox, a list of what needs
you and, under `Info`, what is only worth a look:

```
╭─ Needs you · 4 ──────────────────────────────────────────╮
│ ▌? T3 asks     Which JSON field name for the commit?  2m │
│  ◷ T5 limit    claude · resets 14:30           retry 1/3 │
│  ◆ Lead review 3 changes waiting for y / n            5m │
│  ! T2 failed   go test: expected key "commit"        12m │
│                                                          │
│  Info                                                    │
│  · Budget      $4.10 of $5.00 (82%)                      │
│  · deps-bump   automation failed: T1 failed           1h │
╰──────────────────────────────────────────────────────────╯
```

Needs you (the count in the sidebar):

- a node that asks a question or is blocked (`T3 asks`, `T3 blocked`)
- a node that waits for a usage limit, with the reset time and `retry n/3`
- a lead review that waits for `y` or `n`
- a failed node (not one you stopped)
- a running node with no activity for 5 minutes or more
- a plugin question the lead asked and nobody answered

Info (not counted):

- the budget, once the spending reaches the warn percent
- the last run of each automation (from `.decsr/automations/<name>.log`) when
  it failed, needs you, timed out or hit the budget

Items that need you are not stored: the list is derived from the session each
time it is drawn, so an item goes away by itself when you answer the node,
retry it, apply the review or answer the plugin question. An info item stays
until you clear it. Clearing is saved in the session (`inbox_dismissed`) under
an ID that contains the value, so a new budget level (the percent in steps of
10) or a new automation run shows again.

| Key | Action |
|---|---|
| `Up` / `Down`, `k` / `j` | Move |
| `Enter` | Go to the item: a node is selected and the feed scrolls to it; a review is focused in the feed; a plugin question opens; a budget item opens `/usage budget`; an automation opens the last 20 lines of its log |
| `x` | Clear the selected info item |
| `Esc`, `N` | Close |

The notifications you already get (`needs you`, failed) are unchanged; the inbox
only adds the list.

## Palette

`Ctrl+K` opens "Go to" from any focus area, including the prompt (there it
replaces the terminal's delete-to-end-of-line; the text in the prompt is not
touched). Type to search; the groups always come in this order and a group
without a match is hidden (`No matches` when all are empty):

1. Needs you: the inbox items (a search also finds the info ones)
2. Commands: every slash command, custom and plugin ones included, with its
   description
3. Nodes: ID, title and status
4. Sessions: the saved sessions of this project
5. Projects: the recent projects that still exist

An empty query lists what needs you and the first 5 rows of each other group.
Matching is fuzzy: the letters of the query must appear in order, ignoring
case. Contiguous matches, prefixes and word starts rank higher. A description
or a path matches only when it contains the query.

| Key | Action |
|---|---|
| `Up` / `Down` | Move |
| `Enter` | Inbox item: the same as in the inbox. Command: runs now only if it is on a fixed list of read-only ones (`/help`, `/status`, `/usage`, `/roles`, `/resume`, `/project`); every other command, such as `/pr`, `/merge`, `/clear`, `/compact`, `/plugin`, `/coordinator` or a custom one, is put into the prompt as `/cmd ` for you to complete and send. Node: select it. Session: load it, as `/resume` does. Project: switch to it, as `/project` does |
| `Esc`, `Ctrl+K` | Close |

Loading a session and switching project are refused while nodes run
(`stop the running nodes first`), as with `/resume` and `/project`. The `/`
and `@` lists in the prompt are unchanged.

## Commands

Type `/` at the start of the prompt to open the command list. A bare `/` shows
every command under its group header (Session, Run, Agents, Usage, Project and,
last, Plugin commands). While you type, the list keeps the commands that match
and ranks them: names that start with what you typed first, then names that
contain it, then names that hold the letters in order. Each row shows the
name (the matched letters underlined in the group colour), a short args hint,
the description and, at the right, the group. The list is a box of fixed size,
as wide as the prompt box and always the same height (eight rows, a hint line
and the borders), whether the query is bare or filtered, short lists are padded
with blank rows and no match says `no command matches /xyz` in the first row.
The name and args columns have fixed widths and a longer text is cut with `…`,
so the columns never move. The `@` node list has a fixed height too. The hint
line says `↓ N more · ↑↓ Tab` when rows are hidden. Move with `Up`/`Down`, complete with `Tab`,
run with `Enter`. `Enter` runs a command only
when the text is an exact command name or alias, when you moved the highlight
with `Up`/`Down` (or completed with `Tab`), or when exactly one command matches
the prefix. Otherwise it prints `✗ /co matches several commands: /compact,
/config, /context, /coordinator, /copy, /doctor - keep typing or press Tab` and leaves the text
alone. An unknown command prints `unknown command /x - /help lists them`.

`/help` opens a panel with the groups, one line each, and `/help <command>`
shows the forms of one command and an example. Command results are one line
that starts with a glyph: `✓` done, `✗` error, `⚠` warning, `›` information.
With `NO_COLOR` set no colour is used and the glyphs still carry the meaning.

A command that manages something opens a panel when it has no arguments and
acts directly when it has. A panel is a box in the colour of the command's
group (Usage amber, Agents pink, Project teal) with the keys in its bottom
border. `Tab` and `Shift+Tab` (or `Left` and `Right`) switch tabs, `Up`/`Down`
and `PgUp`/`PgDn` scroll or move the cursor, `Esc` closes. Panels that read
files, probe the machine or run the doctor checks do it in the background and
fill in when they are done. With `NO_COLOR` the active tab is shown in
brackets.

**Tasks that start with a slash.** A single line whose first word contains a
second `/` (for example `/usr/lib is missing libfoo, fix the build`) is sent as
a task, not run as a command. To send a task that starts with a command name,
double the slash: `//compact the logs` sends `/compact the logs`. A text pasted
into the prompt (or typed in one burst) is inserted as text; it is never sent or
run until you press `Enter`. With graph focus, a paste moves the focus to the
prompt and lands there.

**Session**

| Command | What it does |
|---|---|
| `/help [command]` | The commands by group; `/help <command>` shows its forms and an example |
| `/clear` | Save this session and start a new, empty one: no chat, graph, summary or lead conversation (asks first when nodes are unfinished) |
| `/compact [focus]` | The lead summarizes the chat and the nodes, a new lead conversation starts from the summary, and finished nodes are folded (see [Compact](#compact)) |
| `/resume` | Pick a saved session of this project |
| `/rename <title>` | Set the title of this session (saved) |
| `/export` | Write the session to `.decsr/exports/<session id>.md` (title, transcript, summary, each node with role, status, files and summary) |
| `/copy` | Copy the last answer of the lead or of a node to the clipboard with OSC 52, which works over SSH and tmux (tmux needs `set-clipboard on`); at most 100 KB |
| `/quit` (`/exit`) | Leave the Workspace (asks first while nodes run) |

**Run**

| Command | What it does |
|---|---|
| `/answer` | Show the question or refused-command card that was hidden with `Esc` (see [Question cards](#question-cards)) |
| `/check` | Run Build & test now, even when nothing changed: adds the node when the graph has none, or resets it to todo (see [Build & test and fix nodes](#build--test-and-fix-nodes)) |
| `/stop [node]` | Stop one running node (the `s` key), or every node without an argument (the `S` key) |
| `/retry <node>` | Run a node again (the `r` key) |
| `/merge` | Merge the session's branch into your current branch (see [Isolation](#isolation)) |
| `/diff` | The full diff of this session in the diff view: the session branch against its base in a worktree session, the working tree against `HEAD` (untracked files included) in a shared checkout. The notes and keys are those of [Diff and review notes](#diff-and-review-notes) |
| `/pr` | Push the session branch and open (or update) a pull request; `/pr watch` also turns failing CI and review comments into fix nodes, `/pr stop` ends it (see [Pull requests](#pull-requests)) |

Approving or cancelling a plan is `y` and `n` on the plan card; `/approve` and
`/reject` print that hint (see [Approval](#approval)).

**Agents**

| Command | What it does |
|---|---|
| `/roles` (`/agents`) | List all roles, with their skills, and create a new one; `/roles save <name>` writes a role created from a plan card for this session to `project.yaml` |
| `/model` | Panel with the tabs Roles, Rankings and Profiles. Roles lists every role with its runner, model and effort; `Enter` edits the row (`Tab` picks the field, `Left`/`Right` cycle through the connected runners, the ranked models of the runner and its effort levels, `Enter` ends the edit) and `s` saves the changed roles in `project.yaml` (refused while nodes run; plugin roles are not edited here). Rankings is the table of `/model rank`; `r` fetches it again. Profiles is the list of `/model profile`: `Enter` applies one, `d` deletes one, the last row saves the current roles |
| `/model rank` | Open the Rankings tab and fetch the model rankings from Artificial Analysis with the key saved by `decsr connect artificialanalysis`, save them to `~/.decsr/rankings.json` and show the table (sorted by balance score, with the request count and the models that map to no runner). The fetch runs in the background; with no key the view says how to connect and shows the built-in fallback tiers. Data: Artificial Analysis (artificialanalysis.ai) |
| `/model profile [save <name>\|apply <name>]` | The Profiles tab, or save the current roles as a profile, or apply one (see [Profiles](#profiles)) |
| `/routing [auto-connected\|auto-role\|manual]` | Show the routing mode and where it comes from (this session, a profile, the project or the default); with a mode, set it for this session and save it to `project.yaml` (see [Agent routing](#agent-routing)) |
| `/autonomy [1\|2\|3]` | How much runs by itself (see [Autonomy](#autonomy)) |
| `/coordinator [on\|off\|now]` | Show or set, for this session, whether the lead reviews each finished batch; `now` runs one review now (see [Coordinator mode](#coordinator-mode)) |

**Usage**

| Command | What it does |
|---|---|
| `/usage` | Panel with the tabs Session, Budget, History and Limits. Session: the totals of the session (tokens in and out, cache, cost), the plan limits, then the tokens and cost per role with its agent (`runner · model · effort`), a total row and the budget meter; `n` switches to one row per node. Plan limits are like the claude `/usage` screen: one meter per window (`Current session (5h)`, `Current week (all models)`, and the Opus or Sonnet week when claude reports them) with the share used, a `⚠ over 75%` mark past the warning level and the reset time. They come from the `rate_limit_event` lines the claude CLI prints during every run (the lead, a claude node, the classic pipeline) and are kept in `~/.decsr/plan-limits.json`, so they show the state of the last claude run (`as of 14:02`, marked `(stale)` when a window has reset since) and update on the next one. decsr never asks the account for them, never starts a run to refresh them and nothing waits or retries because of them. Codex, opencode and dsh report none. Budget: the spending per runner, the models without a price and the budget; `Enter` types an amount or `off`. History: sessions, tokens and cost of today, 7 days and 30 days, a sparkline of the daily tokens and the table by agent, read from the saved sessions of this project; a cost is only what the configured prices give, `–` when there is no price. Limits: the same plan limits, then the last limit each runner printed when it limited a node, with its reset time, read from the saved sessions. decsr asks no account or usage service for it |
| `/usage cost` | The Session tab with a row per node |
| `/usage budget [amount\|off]` | The Budget tab; `/usage budget 10` sets this session's budget, `/usage budget off` removes that override (see [Cost and budget](#cost-and-budget)) |
| `/status` | One panel: version, project and language, session title and whether it works in a worktree or the shared checkout; the runners with how they are available (claude shows `subscription (unattended runs need a key)` or `API key`); the providers with masked keys; plugins on and off; routing, autonomy, coordinator, parallel limit and the budget used; today's sessions, tokens and cost; and `⚠ N warnings · /doctor for details` from the doctor checks |
| `/context` | What the lead and the workers receive: the project overview, the context pack, the session summary, the lead prompt and the worker prompt of a sample role, each with its size in characters and about as many tokens (one per four characters). The Workspace does not send project memory, so that row is marked as not sent |

**Project**

| Command | What it does |
|---|---|
| `/project` | Switch to another project (see [Switching project](#switching-project)) |
| `/config [list\|get <key>\|set <key> <value>]` | Panel of the settings by section with their values; `Enter` edits one with the same checks as `set`. `get` shows one, or `set` changes one and save it in `project.yaml`. Keys are dotted, such as `workspace.max_parallel`, `workspace.isolation`, `workspace.budget_usd`, `workspace.coordinator`, `workspace.auto_compact`, `routing.mode`, `ui.theme`, `ui.icons`, `project.language`, `project.build_cmd`; a value is checked before it is saved. `ui.theme` and `ui.tmux` apply at the next start. `/config` and `/config list` open the panel; `/menu` opens the classic menu |
| `/provider [list\|add <name>\|remove <name>\|test <name>]` | Panel of the saved providers and the keys set in the environment, masked: `a` adds, `x` removes (asks first), `t` tests the key under the cursor; `list` prints them in the chat; `add` opens the key entry (see [Connect a provider](#connect-a-provider)); `remove` asks first and deletes the saved key; `test` lists the models of the provider with its key |
| `/memory [search <query>\|add <text>]` | Panel of the project memory (`MEMORY.md`, the pinned facts); `search` shows the best matches of the memory index and `add` appends a fact |
| `/doctor` | Runs the setup checks of `decsr doctor` in the background and lists each as `✓`, `⚠` or `✗` with the fix hint |
| `/plugin` (`/plugins`) | Plugin manager panel (Installed, Discover, Marketplaces, Stats); direct forms `list`, `search <q>`, `info`, `install`, `enable`, `disable`, `remove`, `import`, `reload`, `stats` and `marketplace add\|remove <owner/repo>` (see [Plugins](#plugins)) |
| `/issue <N\|URL\|bead-id> [--force]` | Fetch a GitHub issue or a bead and send it to the lead as the task (see [Start from an issue](#start-from-an-issue)) |
| `/import speckit [--phase N] [path]` | Add the open tasks of a Spec Kit `tasks.md` to the graph (see [Import from Spec Kit](#import-from-spec-kit)) |
| `/menu` | Leave for the Home launcher: settings, project setup, classic pipeline |

**Moved.** `/cost` is `/usage cost`, `/budget` is `/usage budget`, `/rank` is
`/model rank`, `/connect` is `/provider add <name>`, `/review` is
`/coordinator now` (`y` and `n` answer a waiting review) and `/profile` is
`/model profile save|apply <name>`. Typing an old name prints one line with
the new one and runs nothing.

**Plugin and custom commands**

| Command | What it does |
|---|---|
| `/<name> [args]` | A [custom command](#custom-commands-and-skills) |
| `/<plugin>:<name> [args]` | A command of a [plugin](#plugins) |

`/clear`, `/compact`, `/merge`, `/pr`, `/project`, `/resume` and applying a profile are refused
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
- `/clear` saves the session and starts a new one with an empty chat, an empty
  graph, no summary and a new lead conversation. Nothing of the old session is
  sent to the lead again. The old one stays in `/resume`, with its own lead
  conversation. `/clear` is refused while the lead is working or nodes run.
- `/resume` lists the project's sessions newest first (`title - 3/7 done - 2h
  ago`, the current one marked). `Enter` loads one (the current session is saved
  first), `d` deletes one after `y/n`, `Esc` closes. Nodes that were running
  when you left come back as not started.
- A broken session file is skipped in the list.
- `decsr resume` opens the Workspace with this list already showing.
- The first time a project with an old `.decsr/workspace.json` is opened, it is
  wrapped in a session and renamed to `workspace.json.migrated`.

### Compact

`/compact [focus]` asks the lead for one summary of the conversation. It gets the
earlier summary, the chat lines of the current lead conversation, each node
with its status, role, files and result, the open questions (nodes that ask or
failed) and the optional focus. The call has no tools and the summary aims at
2,000 tokens. Then:

- `Session.Summary` becomes that summary and the lead conversation is dropped:
  the next message starts a new conversation seeded with the summary.
- The chat shows `compacted · ≈42K → 3K tokens`: the size before is the prompt
  size of the lead's last turn (an estimate of what the call read when there was
  no turn yet), the size after is an estimate of the summary.
- Finished nodes are folded as described next and a second line says how many
  (`folded 3 finished nodes; kept 1 that unfinished nodes still depend on`).
- When the call fails nothing changes and the error is shown. `/compact` while
  nodes run is refused, and it says `nothing to compact` when the session has no
  chat, summary or nodes yet.

Folding a finished work or fix node (status done) removes it from the graph and written into the session summary as one line:
`- T1 <title> [<role>]: <first 400 characters of its summary> (files: a, b)`,
but only when no unfinished node depends on it (every node that depends on it is
folded too). A finished node that a todo, running or failed node still needs
stays, so that node keeps the work it builds on; the notice says how many were
kept, for example `compacted 3 finished nodes; kept 1 that unfinished nodes
still depend on`. Nodes that stay keep their edges to each other; a dependency
on a removed node is dropped. Build & test stays. The summary is capped at
12,000 characters (oldest lines go first). It is given to the lead on the first
message of its next conversation and to every worker as an `Earlier in this
session` section of its context (the most recent 4,000 characters).

At autonomy level 2 and 3 the finished nodes are folded by themselves, without
a model call, once 10 or more work and
fix nodes are done and nothing runs (not while automation is paused or the
budget is reached). It writes `auto › compacted 10 finished nodes` and runs
again only after the count has dropped below 10 and reached it again. At
level 1 the prompt suggests `/compact` instead.

**Auto-compact.** `workspace.auto_compact` (on by default, `/config`) compacts
the lead's conversation by itself, only in front of a message you send: when
the previous lead turn used more than 70% of the model's context window (200K
for claude, 1M for a `1m` model, 128K for other runners; `max_context_tokens`
of the `pm` agent overrides it), decsr runs the compaction first, prints
`auto › compacted the conversation (≈150K → 3K tokens)` and then sends your
message as the first turn of the new conversation. If the compaction fails, the
message is sent in the old conversation. Nothing compacts in the background, on
a timer or in an unattended run; with the setting off only `/compact` does.

The cost of folded nodes is kept (`removed_cost_usd` in the graph) and still counts toward the spent total and the budget; the same holds for nodes the lead cancels.

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

`/model profile` opens a list of saved profiles. `Enter` applies one: its roles are
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

## Connect a provider

`/provider add` opens a list of the API providers (DeepSeek, OpenAI, OpenRouter, Groq,
saved custom providers and "custom provider…"), each marked `✓ saved`, `✓ env`
or `not set`. `/provider add deepseek` skips the list. After you choose one, paste
the key into the masked input (it shows dots, never the key). `Enter` tests the
key with `GET <base_url>/models` and saves it to `~/.decsr/credentials.json`;
`Esc` cancels. A rejected key (401 or 403) is not saved; when the provider
cannot be reached you can save the key anyway with `y`.

On success the conversation shows `Connected deepseek (key saved, 12 models)`
and the overlay asks what should use the provider: `1` the lead only, `2` all
roles, `3` nothing now. When the provider has no default model you pick one from
the models it returned. The choice is written to `~/.decsr/config.yaml` (runner
and model only, never the key) and applies at once. The same command exists in
the shell as `decsr connect`, and `/config` offers it with `k` on an API
provider.

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

### The plugin manager

`/plugin` opens a panel (teal, like the other Project commands) with four tabs.

- **Installed.** One row per plugin: `●` on, `○` off, and `◌` for a plugin Claude
  Code has installed that decsr does not load. The columns are the name, the
  source (`decsr` or `claude`), how many skills, roles and commands it has, and
  when it was last used (`used 2h ago`, `used today`, `never used`, and
  `unused 34d ⚠` for an enabled plugin nobody used for 30 days). The line under
  the list shows what the plugin under the cursor holds. `Space` turns it on or
  off, `i` shows its info (for a `◌` row it imports the plugin), `x` removes it
  after a y/n question. A plugin with `hooks/`, `.mcp.json` or `mcpServers` is
  marked: hooks and MCP servers are not used by decsr. On and off change
  `~/.decsr/plugins/disabled` (one name per line); they and remove are refused
  while nodes are running, and a node assigned a role that is gone loses that
  assignment.
- **Discover.** The curated catalog, then the plugins of the marketplaces you
  added. Type to filter (name, description, tags, skills, agents, marketplace);
  the first line shows `N of M`. `Enter` installs the plugin under the cursor
  after a question; the install runs in the background. A plugin of a
  marketplace shows `⚠ untrusted` until you trust that marketplace.
- **Marketplaces.** `a` adds a marketplace, `x` removes it (after a question),
  `r` reads it again. A marketplace is a GitHub repository with
  `.claude-plugin/marketplace.json` in Claude's format: a `plugins` list whose
  `source` is a relative path inside the repo (`"./plugins/x"`) or
  `{"source":"github","repo":"owner/repo"}`. Other source kinds are skipped and
  the result line says how many. The repo is read with a shallow `git clone`
  into a temporary directory off the UI thread, with a time limit; nothing in
  it is run. The list is kept in `~/.decsr/marketplaces.yaml` (repo, trusted,
  fetched_at and the entries). The first install from a marketplace asks
  `Trust <owner/repo>? Plugins can add skills, roles and commands that agents
  will follow. y/n`; a yes is recorded and the install question follows. If a
  marketplace lists a name the curated catalog has, the catalog entry is shown.
  The curated catalog itself still installs only its exact entries.
- **Stats.** Per plugin: the nodes that ran with one of its skills
  (`<plugin>:<skill>` in the node's skills) or one of its roles
  (`<plugin>:<agent>`), their tokens and cost, and the last use, from the saved
  sessions of this project (the last year) and the live session. The same data
  gives the last-used column of the Installed tab.

Claude Code plugins: the Installed tab lists what Claude Code has installed on
this machine, read from `~/.claude/plugins/installed_plugins.json`. Only the
`~/.claude/plugins` folder is read (an install path outside
`~/.claude/plugins/cache` is ignored); login files and settings under `~/.claude`
are never opened. `/plugin import <name>` (or `i` on a `◌` row) copies the plugin
directory to `~/.decsr/plugins/<name>` in the layout above (a plugin without a
`plugin.json` gets one written) and reloads. Hooks and MCP servers stay unused.
An existing `~/.decsr/plugins/<name>` is never replaced.

Direct forms, each printing one result line:
`/plugin list`, `search <q>`, `info <name>`, `install <name>`, `enable <name>`,
`disable <name>`, `remove <name>`, `import <name>`, `reload`, `stats`,
`marketplace add <owner/repo>` and `marketplace remove <owner/repo>`. `install`
and `remove` ask first. `/plugin available` prints a hint to `/plugin search`.

### How the lead picks skills

When the lead splits a task it is told which skills the enabled plugins offer
(`- <plugin>:<skill>: <description>`, descriptions cut to about 120
characters, at most 60 lines) and which catalog plugins are not installed or
are disabled. For each sub-task it lists in `skills` only the skill ids that
agent really needs, often none, and never a skill for another language or
stack than the sub-task touches. Ids that are not an enabled plugin's skill
(or a skill of a plugin the lead asked for, see below) are dropped and the chat
says so.

The skills a node gets are the role's own `skills:` followed by the ones the
lead chose, each once. A skill that does not resolve shows the usual
`skill "..." not found` warning. The inspector lists them under Skills, each
marked `(role)` or `(lead)`, and `(not installed)` when no enabled plugin
provides it. On a todo or failed work node `K` opens a checklist of the
installed skills to change the lead's choice (the role's skills are shown but
fixed; `Space` toggles, `Enter` saves, `Esc` cancels). It is refused while the
node runs or after it is done. The choice is saved with the session.

### Installing a plugin the lead asks for

The lead can also name plugins that would help but are not installed (or are
installed and disabled) under PLUGINS, with a one-sentence reason (at most 5,
only plugins of the built-in catalog; anything else is ignored). After the
split a dialog asks about each one:

```
The lead wants plugin modern-cpp: <reason>. Install from github.com/trailofbits/skills?
  y install   n skip   a skip all
```

For a disabled plugin the text says it is installed but disabled and `y`
enables it. Nodes do not run while a question is open or an install runs.
`y` clones the repository (depth 1) into `~/.decsr/plugin-src/<owner>-<repo>`
(or updates the clone), copies the plugin folder without `.git` to
`~/.decsr/plugins/<name>` and reloads plugins and roles; an existing copy is
replaced only once the new one is complete. A failure is shown in the chat and
the plugin stays uninstalled. Each request ends up `installed` or `declined`
in the session; a declined plugin is not asked about again in that session,
and the nodes keep the skill id (the inspector shows `(not installed)`). A
request left pending (for example in a resumed session) shows as the ghost
prompt `/plugin install <name>`.

The Discover tab and `/plugin search` list the catalog. `/plugin install <name>`
asks the same question and installs one; the lead's requests only ever name
catalog plugins, and a name that is neither in the catalog nor in an added
marketplace is refused.

### Installing from the chat

Ask the lead in the chat: "install the code-review plugin", "add the GitHub MCP
server". The lead looks the thing up with read-only commands (decsr's catalog
and the marketplaces you added are in its prompt; it may also run `claude plugin
list --available --json`, `claude plugin list --json`, `claude plugin
marketplace list --json`, and `claude mcp list`). With exactly
one match it proposes an install; with several or none it asks you in its reply
(numbered options, one marked Recommended) or asks for the URL or command. It
never invents a source. Workers are never given install commands.

A proposal is an `===INSTALL===` section in the lead's reply, a JSON array of
actions (`kind`, `name`, `source`, `args`, `transport`, `env`, `headers`,
`scope`, `why`), closed by `===END===`. An action with an unknown kind, no name
or source, or a scope other than `local` or `user` is dropped with a chat
warning. decsr then shows one card per action, oldest first:

```
⬇ Install · Claude Code plugin · code-review@claude-plugins-official
Reviews pull requests with specialised agents.
runs: claude plugin install code-review@claude-plugins-official --scope local
scope: this project only (local)
› y install · n skip
```

Only `y` and `n` act on the card (no Enter, no number, no cursor), and only when
the prompt is not focused, so typing never answers it. Esc hides it, `/answer`
brings it back. Nothing installs without the `y`, at every autonomy level; an
automation run never installs: it prints `⚠ install needs you: skipped <name>`
and leaves an info item in the inbox. An install can run while nodes run (it
does not touch their worktrees). Esc cancels a running install; it also stops
after 3 minutes.

| Kind | What `y` does | Scope |
|---|---|---|
| `decsr-plugin` | decsr's own installer, as `/plugin install` (the card is the confirmation; a marketplace you have not trusted asks its trust question first) | all projects (`~/.decsr/plugins`) |
| `claude-plugin` | `claude plugin install <plugin>@<marketplace> --scope local --json` | `local` = this project only; `user` only when you asked for all projects |
| `claude-mcp` | `claude mcp add --scope local --transport <t> <name> …` (argv, no shell) | same |

Untrusted mark: the card shows `⚠ untrusted: …` for a Claude Code plugin whose
marketplace is not `claude-plugins-official` (or not named), for a decsr plugin
of a marketplace you have not trusted, and for every MCP server (it runs or
connects to third-party code); a stdio server adds `this server will run:
<program args>`, an http or sse server `connects to: <url>`.

When a marketplace declares a command that runs while a Claude Code plugin
installs, claude reports it instead of installing. decsr then shows a second
card with the command in full and `⚠ this plugin runs a command when installed`;
`y` runs the install again with `--accept-command <sha256>` for exactly that
command. decsr never passes `-y`.

Secrets: the lead writes `<ask>` where a secret belongs (an env value, a header
such as `Authorization: Bearer <ask>`, an argument); a literal value under a
name like `*_KEY`, `*_TOKEN` or `Authorization` is replaced by `<ask>` too. The
card masks it as `••••`. After `y`, a hidden input asks for each value. The
value goes only into the argv of the one `claude mcp add` command: decsr does
not write it to the chat, the session file, the logs or its config (an error
line that echoes it is masked). claude itself stores it in its own config, and
other users of the machine can see it in the process list while the command
runs. After a successful `claude mcp add` the chat adds `workers use it only on
nodes the lead marks for MCP`: all other workers still run without MCP servers
(see [MCP in nodes](#mcp-in-nodes)).

Each outcome is one chat line: `✓ installed code-review (plugin, this
project)`, `✓ installed github (MCP server, this project)` or `✗ install
failed: <first error line>`. The session keeps name, kind, scope, result and
time of each install (no secret value); `/compact` and the lead's next message
carry them, so the lead knows what was installed.

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
Open decsr, run `/plugin` to see the plugins and `/model profile apply plugins` to apply
the profile.

## The lead chat

Every message you type goes to the lead as one conversation. The lead answers
in the chat (Markdown) and creates nodes only when the message assigns work.

- **Reading.** The lead reads the project itself, with read-only tools. For
  claude that is `Read`, `Grep`, `Glob` and `Bash` limited to `git log`,
  `git diff`, `git status` and `git show` (`--permission-mode dontAsk`, no
  `Write` or `Edit`, no MCP servers, no web, no build or test commands). codex
  runs in its read-only sandbox, dsh in its read-only preset; opencode gets the same
  read-only permission config as a read-only node. The lead
  never edits a file: every change is made by a worker node, with the role and
  agent the lead picks.
- **When it creates nodes.** When you assign work: a change, a fix, a feature,
  a refactor, tests or docs. Also one read-only reviewer node for a job too big
  to read in one turn (a whole-project audit). A question, an explanation, an
  opinion or a small review is answered in the chat. A reply that has no node
  section is a plain answer: no plan card, no error and no second call. The
  reply text that comes with new nodes is shown above the `split into ...` line.
- **One conversation.** The session keeps the lead's own CLI conversation
  (`lead_session_id`, `lead_runner`, `lead_model` in the session file). The
  first turn keeps it and later turns resume it (claude `--resume`, codex
  `exec resume`, opencode `--session`, dsh `--session-id`), sending only your
  message and the live nodes, so the system prompt and the history stay cached.
  When the lead's runner or model changes, or a resume fails, a new conversation
  starts with the summary and the chat says so (`⚠ lead: could not resume the
  conversation, started fresh with the summary`). Runners without tools (the API
  presets: deepseek, openai, openrouter, groq, custom) make one call per
  message without a conversation, with the project overview and the summary
  every time.
- **Project map.** The first message of a conversation carries a map instead of
  file contents: the language, which of `AGENTS.md`, `CLAUDE.md`, `README*` and
  `CONTRIBUTING*` exist, up to 30 paths under `doc/` and `docs/`, the 20 newest
  `.decsr/exports/*.md`, the context pack if there is one and the project
  memory notes (cut at 2K characters). The lead reads what it needs. `CLAUDE.md`
  is not sent to claude, which loads it itself.
- **Activity line.** While the lead works, one muted line under the message
  shows its step: `lead · reading internal/runner/claude.go`,
  `lead · searching "RateLimit"`, `lead · git log`. When the reply ends it
  becomes `read 4 files · 2 searches` (nothing when no tool was used).
- **Usage.** The lead's tokens count as the lead (`pm`) in `/usage`.
- **Starting over.** `/clear` starts a new session and a new conversation;
  `/compact` summarizes the conversation and starts a new one from the summary;
  `workspace.auto_compact` does that before a message when the conversation is
  near the context limit (see [Compact](#compact)).

## Lean runs

A task runs only what it needs.

- **`writes`.** Each sub-task has `"writes": true|false` in the lead's output;
  a missing field means `true`. A node with `writes: false` runs read-only
  whatever its role, the same way a read-only role does.
- **Roles.** The lead assigns the role of each node. It is told to use only the
  roles the task needs: a review, question or audit is normally one sub-task by
  one role with `writes: false`, and `qa` or review sub-tasks are added only
  when you asked for them or the change is large.
- **Chat answer.** A question, an explanation, an opinion or a small review is
  answered by the lead in the chat after it read the files it needs: no nodes
  and no Build & test. A job that is too big to read in one turn (a whole-project
  audit) becomes one read-only node.
- **Answers.** When a read-only node ends (a `writes: false` task, or a role
  that cannot write, such as `reviewer`), its whole reply (without the closing
  `REPORT` block) is printed in the chat under `T1 · reviewer:`, rendered as
  Markdown like the lead's messages. Only the final reply is printed, not the
  notes the agent wrote before its tool calls (those stay in the output screen).
  No file is written for it. The run summary
  line for the node says `answer above`. The output screen keeps the full
  output as before.

## Chatting while nodes run

The prompt stays usable while nodes run: a task you type goes to the lead at
once and running nodes are never stopped. The lead is told the live graph:
for each work node its ID, title, status, role, a one-line description and the
files it touched so far (for a running node, whatever is known yet). Only two
things make it say `wait for the lead`: it is still planning a split or running
the coordinator review, or an earlier plan card has changes you have not
answered yet (`y` or `n` first).

The lead keeps the rules of a follow-up: a new sub-task that touches a file a
running node touches, or needs its result, depends on that node, so it waits in
the queue. Besides new sub-tasks it may return a `===CHANGES===` section to
change existing nodes the new task affects:

```
===CHANGES===
[{"node":"T3","action":"update","description":"...","depends_on":["T1"],"reason":"..."},
 {"node":"T4","action":"cancel","reason":"..."},
 {"node":"T2","action":"note","message":"...","reason":"..."}]
```

- `update` (a new description, and optionally a new `depends_on`) and `cancel`
  are only valid for a node that has not started (todo, or waiting without a
  run). A cancelled node is removed and the dependencies on it with it.
- `note` is for a node that ran. A running node keeps the note in its pending
  reply and continues with it after its current attempt. A finished node starts
  a new attempt, which resumes its conversation, as soon as it can start.

Entries that are not valid (unknown node or action, an update of a node that
already ran, an empty note, a dependency that does not exist) are dropped, and
a warning line says why.

The plan card shows one line per change with the lead's reason:

```
Approve plan · 1 node · mode auto-connected · 3 changes
  ...
~ T3 changed: also handle the --port flag (new task needs it)
✗ T4 cancelled: replaced by T7
↻ T2 after it finishes: keep the old flag name as an alias
```

A note to a finished node reads `starts again` instead of
`after it finishes`. The card is needed whenever there is any change or any new
node writes; a plan of only new read-only nodes and no changes starts at once.
`y` applies the changes and approves the new nodes, `e` edits the agent of a
new node and `n` drops the changes and the new nodes. A change that cannot be
applied any more (the node started in the meantime, or the new dependencies
would make a cycle) is reported in the chat and the rest still applies. The
pending changes are not saved: leave the Workspace and they are gone. In an
unattended run (autonomy level 2 or 3, an automation) the changes are applied
with the nodes, with an `auto ›` line, unless `routing.auto_approve` is `false`.

## What the lead and the workers receive

Every prompt is kept small, because the agent steps send it again and again.

- **One-shot calls** (the coordinator review, `/compact`, and the lead of a
  runner without tools) run claude with all built-in tools off (`--tools ""`), so no
  tool schemas are sent. Other runners have no such switch.
- **The lead** has a stable system prompt (rules, output format, roles with each
  description cut to one short line, the installed skills with a description of
  at most 80 characters, the plugin catalog on one line per plugin). Each message
  carries your text and, when nodes exist, the live graph and the next free id.
  The first message of a conversation also carries the project map and the
  summary of the earlier work (see [The lead chat](#the-lead-chat)). A lead
  without tools gets the project overview (structure, file list, recent commits,
  capped at 10K characters, without source excerpts) instead of the map.
- **A worker** gets the project context, the summaries and files of its
  dependencies, the skills of its role and its task. It does not get the source
  excerpts when it runs as an agent (it can read the files), nor the text its
  runner loads by itself (`CLAUDE.md` for claude, `AGENTS.md` for codex and
  opencode). The saved context pack is cut at 4K characters, the session
  summary at 2K characters and the contents of the dependency files at 24K
  characters in all (10K for one file); the files that do not fit are listed by
  name, and the agent reads them when it needs them.

## Agent routing

By default each node runs on the agent of its role. Routing lets the lead's
split hand small and medium nodes to a cheaper agent. It is set by
`routing.mode` in `~/.decsr/config.yaml`, `.decsr/project.yaml` or a profile
(a profile saved with `/model profile` stores the mode, and applying it writes the
mode to `project.yaml`); the session freezes the mode at its first split.

| Mode | Behaviour |
|------|-----------|
| `manual` (default) | Nodes use the role's agent. You can change the agent of a node by hand. |
| `auto-role` | A node may get another model or effort of the role's own runner. |
| `auto-connected` | A node may also get any other connected runner (binary on PATH or key available), for example a Claude lead's node on `dsh`. |

```yaml
routing:
  mode: auto-role
  auto_approve: true   # unattended runs approve the picks themselves (default); false waits for you
```

In the two auto modes the lead adds a `difficulty` (`small`, `medium`, `large`)
and a one-line `reason` to every sub-task, in the same call as the split. The
lead only grades: decsr picks the agent itself, from the saved rankings (see
`/model rank`) or the built-in fallback tiers, so the same grade gives the same pick.

- The pick never costs more per task than the role's own (model, effort) pair.
- `small` needs an intelligence index of at least 30; `medium` needs at least
  85% of the role pair's; `large` keeps the role's agent.
- Among the pairs that pass, the highest balance score wins; the model and the
  effort are chosen together. `auto-role` looks at the role's runner only.
- A role whose model is not ranked (a local model, a custom endpoint) keeps its
  agent, and unranked models are never picked automatically.

The prompt, skills, mode, MCP servers and read-only flag still come from the
role; only the runner, model and effort are replaced. If the picked runner is
not connected when the node starts, or its first call hits a rate limit, the
node runs on the role's agent instead and the node records why. Cost is priced
with the model that actually ran.

### MCP in nodes

A worker's own claude process runs MCP servers; decsr never runs one. A node
runs without MCP unless the lead marks it: a sub-task has an optional
`"mcp": ["github"]`, the server names as `claude mcp list` shows them (they can
contain spaces and colons, such as `claude.ai Google Drive` or
`plugin:open-design:open-design`). The lead looks the names up with that
read-only command and marks a node only when the task needs a server's tools
("read GitHub issue 12 through the GitHub MCP"). A name that is empty, longer
than 80 characters or has control characters is dropped with a chat warning.

For a marked node on the claude runner decsr drops the empty strict MCP config,
so claude loads the servers you configured, and adds one allow rule per named
server to `--allowedTools`: `mcp__<server>`, where `<server>` is the name with
every character outside `A-Za-z0-9_-` replaced by `_` (`claude.ai Google Drive`
becomes `mcp__claude_ai_Google_Drive`). The rule allows all tools of that
server; with `--permission-mode dontAsk` the tools of every other server are
refused. `--tools` still lists built-in tools only. Such a run (and a role with
`mcp: true`) also passes `--setting-sources user,project,local`, because
user-scope servers and claude.ai connectors are only read with the user source:
an MCP node therefore also loads your global Claude settings (hooks included),
and its tools are still limited by the allow list. A node without `mcp` keeps
today's arguments. The role option `mcp: true` still keeps all your servers for
every node of the role.

This works in both role modes. In text mode the node gets no built-in tools,
only the named servers' tools (`--tools ""` plus the `mcp__<server>` rules with
`--permission-mode dontAsk`), and decsr still writes the file blocks.

The plan card shows `uses MCP: github, google-workspace` under the node, and a
plan with an MCP node always waits for `y`, even when every node is read-only
and at every autonomy level. An automation run stops with `needs you: T2 uses
MCP` in the inbox instead of starting. On a node whose agent is not claude the
card shows `⚠ MCP needs a claude agent; T2 will run without it` (`e` switches
the agent); codex, opencode and dsh never get MCP. The lead and `/compact` see
which nodes used MCP (`mcp: github`).

### Approval

After a split, in every mode, the plan waits for your approval when any node
writes, and nothing starts until you give it. The Workspace shows one plan card
at the end of the feed, in manual routing too:

```
Approve agents · 3 nodes · mode auto-connected · est. $0.62
▸ T1 Data model          small   claude · haiku · low         ↓   $0.04
     coder · writes · small: claude haiku low is the best fit
  T2 Command parser      large   claude · opus · medium       =   $0.31
     coder · writes · large: the role's agent
  T3 Security pass       medium  claude · opus · medium       =   $0.20
     security · read-only · medium: the role's agent
```

Each node has one row: its ID and title, the grade (`small`, `medium`,
`large`), the agent as `runner · model · effort`, a marker and the estimated
cost per task (`–` when unknown). Dim on the next line are its role, `writes`
or `read-only`, and the reason. The marker compares the pick with the role's
own agent: `↓` cheaper, `=` the same, `↑` dearer (a manual pick), `≠` different
with an unknown cost.

A role the lead invented shows as `docs_writer (new · uses the pm's agent)`.
`y` creates it for this session only: it has no agent of its own, so it uses
the `pm` agent's runner, model and effort, and it is not written to
`project.yaml` until you run `/roles save docs_writer`. A session-only role is
not there after you leave the Workspace.

A plan whose nodes all have `writes: false`, all with a defined role, skips the
card and starts at once (subject to the budget and the parallel limit). In an
automation or at autonomy level 2 or 3 the usual unattended approval applies
instead.

| Key | Action |
|-----|--------|
| `y` | Approve every agent and start the nodes that can run |
| `n` | Cancel the split: its nodes are removed and a notice says which |
| `↑` `↓` | Select a node (the selected row is marked `▸`) |
| `e` | Edit the agent of the selected node |
| `a` | Pick another role for the selected node |

`e` opens the agent editor.
It has a runner (the role's own, the connected ranked runners and the local
ones), a model (`←` `→` cycle the ranked models of the runner, or type any id)
and an effort (`←` `→`). Where a ranking exists, the editor shows the balance
and cost of the choice and of each model. A pick that costs more than the
role's agent shows a warning but is allowed: it is your choice. `Enter` saves
it as a manual assignment; the node stays pending until you approve. A node
waiting for approval shows the status `approval`.

Unattended runs (autonomy levels 2 and 3, automations) approve and record the
picks themselves, with no card, unless `routing.auto_approve` is `false`, in
which case they wait for you.

`/routing` shows the mode in force and where it comes from; `/routing auto-role`
sets it for this session and saves it to `project.yaml`. It applies to the next
split.

## Roles

A role is a name, a one-line description and a runner, model and effort. The
seven built-in roles are `pm`, `coder`, `coder_fixer`, `qa`, `ux_reviewer`,
`security` and `reviewer`; they keep their own prompts. `reviewer` reads the
code and reports findings, opinions and summaries and never writes; `qa`
writes tests and reviews code quality and edge cases. Any other key under `agents:` in
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
Project Setup (`s` on the Home launcher) lists custom roles after the built-ins, lets
you change their runner, model and effort, and keeps every custom role, plus the
description and `read_only` flag of all roles, when it saves.

### Read-only roles

A role with `read_only: true` reports and never changes files: whatever file
blocks its reply contains are discarded, and the prompt tells it so. A
read-only node can read the project and run read-only shell commands on every
runner (see [Agent mode](#agent-mode)). `ux_reviewer`,
`security` and `reviewer` are always read-only. The role form has a third field, **Can
change files** (`yes` by default; toggle with `←`/`→` or `Space`), and a role
saved with `no` gets `read_only: true`. The picker and the inspector show
`read-only` next to such roles. Assigning one is allowed; DECSR says
`<role> is read-only: it will report, not change files`. A read-only node never
gets the "no files were written" warning.

### Agent mode

A role has a mode, `mode: text` or `mode: agent` (in `agents:` of
`.decsr/project.yaml`, kept by profiles):

```yaml
agents:
  coder:
    runner: claude
    mode: agent
```

| Mode | What happens |
|---|---|
| `text` | The model answers with file blocks and DECSR writes the files (see above). |
| `agent` | The runner works as a coding agent in the node's working directory (its worktree when isolation is on) and edits the files itself with its own tools. DECSR does not read file blocks; fenced blocks in the reply are just text. The node's files are what changed in the worktree (the commit that is merged) or the observed tree changes. |

Without `mode`, `opencode` and `codex` (and an empty runner, which is opencode)
use `agent`; every other runner uses `text`, except that a read-only node on
`claude` also uses `agent` (it gets tools to read; `mode: text` keeps it in text). Agent mode needs `claude`,
`opencode` or `codex`; with an API or local runner the node fails with
`agent mode needs claude, opencode or codex`, and `decsr doctor` prints one
`mode <role>` line for each such role. `mode: text` is not accepted for
`opencode`, because it has no read-only switch to keep it from editing files
itself; use `agent`, or another runner. For `codex`, text mode runs with
`--sandbox read-only` and agent mode with `--sandbox workspace-write` (a read-only
node uses `read-only` in both). The
inspector shows `Mode  agent` or `Mode  text` on the Role header, and the role
list and the picker show it after the model. Project Setup does not edit the
mode yet: set it in the YAML file.

In agent mode the prompt tells the agent to change files directly, not to print
file blocks, and to end with a short `===SUMMARY===`. A node whose run changed
nothing still gets the "no files were written" warning. A read-only role stays
read-only: its prompt forbids changes and the runner enforces it (see below).

**Claude in agent mode** runs with a fixed tool list and nothing else
(`--tools`, `--allowedTools` and `--permission-mode dontAsk`, so a tool that is
not on the list is refused instead of asking, whatever your own Claude settings
allow):

- `Read`, `Edit`, `Write`, `Glob`, `Grep`;
- `Bash` limited to the programs of the project's build and test commands. Each
  command is split on `&&`, `||`, `;` and `|`, and the program of each part
  is allowed (`cmake -S . -B build && cmake --build build` gives
  `Bash(cmake:*)`), plus the read-only `ls`, `cat`, `head`, `tail`, `wc`,
  `pwd`, `which`, `file`, `stat`, `du` and `tree`;
- `Bash` for each prefix in `project.allowed_commands`;
- read-only git: `git status`, `git diff`, `git log`, `git show`.

A read-only node gets a fixed set instead: `Read`, `Glob`, `Grep`, `WebSearch`,
`WebFetch` and `Bash` limited to `ls`, `cat`, `head`, `tail`, `wc`, `pwd`,
`which`, `file`, `stat`, `du`, `tree`, `rg`, `grep`, `git status`, `git diff`,
`git log`, `git show`, `git blame`, `git grep`, `go doc` and
`go list`. It gets no build or test command, no `find` and none of
`project.allowed_commands`. A deny list (`--disallowedTools`) refuses the
flags and the redirection that write a file: `git diff|log|show --output`,
`tree -o`, `rg --pre`, `git grep -O` and `>`.

A read-only node on the other runners: `opencode` runs with a permission config
(`OPENCODE_CONFIG_CONTENT`, on a private server with `--standalone`) that denies edits and every shell command except the
same read-only list, with the same deny rules after it. `codex` runs in its
`read-only` sandbox in agent mode too, and `dsh` in its read-only preset.
`copilot` cannot run a read-only node yet: the node fails with
`copilot can't run read-only nodes yet; pick another runner for this role`.
```yaml
# .decsr/project.yaml or ~/.decsr/config.yaml
project:
  allowed_commands:
    - /opt/Qt/Tools/CMake/bin/cmake
    - dpkg -l
```

A project list replaces the global one. Use it for a program called by absolute
path or a command that is not a build or test command. There are no interactive
permission prompts. In text mode Claude runs with all
tools off, as before.

**Shell in a node's worktree.** `!` on the selected node opens `$SHELL` (else
`/bin/sh`) in its worktree while that exists (running, failed, or conflicted
nodes whose worktree was kept); otherwise in the integration worktree (the
merged result), otherwise in the project root. A conversation line says which.
Under tmux (`$TMUX` set, which includes `decsr --tmux`) it runs
`tmux split-window -h -c <dir>` and the Workspace keeps running; otherwise the
Workspace is suspended until the shell exits.

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

## Reply to a node

Talk to a node after it ran, or while it runs. Type `@T3 <message>` in the
prompt (`@` at the start lists the work and fix nodes; `Tab` or `Enter` on an
item completes `@T3 `), or select the node and press `m`, which focuses the
prompt filled with `@T3 `. The message may span several lines (`Alt+Enter`).
Build & test takes no reply: `@CHECK ...` and `m` on it show an error. An
unknown ID shows an error and leaves the text in the prompt.

A reply starts a new attempt of the same node. It is not a child node.

- The node keeps its history. Its card shows `attempt 2` in the header, the
  sidebar row and the inspector (`Attempt  2 of 2`, `Reply  <first line>`).
  Expand the card (`Space`) to see the earlier attempts as folded lines such
  as `Attempt 1 · failed · Worked for 2m 10s ›` with their summary or error.
  The output view switches attempts with `[` and `]`.
- The result is merged again. With worktree isolation the new attempt gets a
  fresh worktree from the integration tip, which already holds what attempt 1
  merged, so the agent edits on top of it.
- A node that is done or failed moves its finished attempt into the history and
  starts at once, unless it must wait: a dependency is not done, no role is
  assigned, the parallel limit is reached, or the budget gate stops new nodes.
  The conversation then says `T3 waiting: <reason>` and the node starts like
  any ready node (`r`, or automatically while `R` run-all is on). A Build & test
  node that was done or failed is reset to todo, as after a fix node.
- A node that never ran keeps your message for its first run.
- Nodes that depend on the replied node are not re-run. If a done node that
  others depend on gets a new attempt, the node's output says
  `T5, T6 depend on T3 and were not re-run`. Run them again yourself.

**Reply to a running node.** The reply is queued (`queued for T3`; the card
shows `reply queued: "..."`, several replies are joined by a blank line). It
starts the next attempt when the current run ends, whether it succeeds, fails
or is stopped. `s` still stops the run, and stopping keeps the queued reply:
the next attempt starts right after. Closing decsr stops the node and leaves
the reply on a waiting node.

**What the new attempt gets.** A later attempt continues the node's own CLI
conversation, so the node remembers its earlier work and the repeated context
is served from the provider's cache. It sends only the new message: your reply,
the answer to a question card, `The command ... is now allowed`, or, for a retry
after a usage limit or a failure, `Your previous run stopped before it finished.
Continue where you left off.` The project context and the excerpt of the earlier
attempt are not built again. The node runs in the same working directory
(its worktree path) with the same allow list, model and effort.

The runner reports the id of its conversation in its JSON stream; decsr keeps it
on the node (`session_id`, with the runner and the attempt it came from) and in
each archived attempt, and resumes it with the CLI's own flag:

| Runner | Id comes from | Resume |
|---|---|---|
| claude | `system`/`init` event, `session_id` | `--resume <id>` (a node run or a lead turn no longer passes `--no-session-persistence`; one-shot calls such as the coordinator review, `/compact` and pings still do) |
| codex | `thread.started`, `thread_id` | `codex exec resume <id> -` (the prompt comes on stdin; the sandbox is set with `-c sandbox_mode=...`) |
| opencode | `sessionID` of the events | `--session <id>` |
| dsh | `session` event, `sessionId` | `--session-id <id>` |

Only the conversation of the same runner is resumed: a node moved to another
runner or role starts fresh. claude does not store its system prompt in the
session, so it is sent again with the resume (the context part rebuilt as for a
first attempt, but without the excerpt); codex, opencode and dsh keep it in
the first message of their conversation. Only each CLI's own session files
are used.

**When resuming is not possible.** A node without a session id, a runner that
cannot resume (the HTTP runners), or a resume that fails at start (the CLI says
the session does not exist, before any output) runs a fresh attempt instead.
That attempt rebuilds the context by replay, as before:

```
Previous attempt (attempt 1, failed):
<excerpt>

The user replied:
<message>
```

The excerpt is at most 8,000 characters: the previous summary, the commands
with the tail of their output (failed ones first), the errors, the files
touched and the last assistant message (at most 1,000 characters). Reasoning is
never included, and where text was cut it says `[… N characters omitted]`. The
chat says once `⚠ T1: could not resume its conversation, started fresh with a
summary` when a resume failed. opencode creates a session for an id it does not
know, so for opencode a lost session is not detected: the attempt runs in a new
conversation without the history.

The session keeps the earlier attempts of a node (summary, error, file
statistics, commit); the activity timeline is kept for the three most recent
ones. The budget gate and the autonomy level apply as for any node.

## Node reports and waiting nodes

Every work node ends its reply with a report, in text and agent mode and for
read-only roles too:

```
REPORT
status: done | blocked | question
summary: <one or two sentences>
question: <only when status is question: the exact question for the user>
options: <only when status is question: numbered lines, see below>
recommended: <number of the option the worker recommends>
next: <optional: at most 3 lines, one follow-up each>
```

decsr reads the last `REPORT` line of the reply. Key names are case-insensitive;
surrounding markdown fences or bold, extra spaces and CRLF line ends are
accepted, and a `summary` or `question` may span several lines until the next
key. An unknown `status` counts as `done`. A reply without a report counts as
`done`, and the node keeps the summary decsr extracts as before. The report
block is not part of the summary shown on the card. The report is stored on the
node (`report` in the session file) and in each archived attempt.

- `next` is optional and asked for only when real follow-up work remains
  outside the node's own sub-task; most reports have none. It holds at most 3
  lines, `<short imperative title> - <one sentence>` each (a numbered or dashed
  line is fine); more are dropped, and so are empty ones. It is kept as
  `report.next`. See [Suggested follow-ups](#suggested-follow-ups).
- `done`: the node is done, as before.
- `question` or `blocked`: the node ends **waiting** (`◇ waiting`), not done.
  Its changes are committed and merged into the session branch like those of a
  done node, so the next attempt starts from the partial work. Nodes that depend
  on it stay blocked and Build & test cannot run, but independent nodes keep
  running. A waiting node is not folded by `/compact` and counts as unfinished
  for the pull request (its question is listed under `Waiting for an answer`).
  The header and the sidebar count it (`2/5 done · 1 waiting`).
- The card shows `? <question>` (up to 4 lines) or `blocked: <summary>` in the
  warning colour and the hint `m reply`; the sidebar row says `asks a question`
  or `blocked`; the inspector shows `Report  question` and the question.
- A notification asks for you: `T3 asks: <question>` or `T3 is blocked:
  <summary>`.
- Autonomy levels 2 and 3 never answer or fix a waiting node: it always waits
  for you. The lead of [coordinator mode](#coordinator-mode) sees the question
  but never answers it.

### Question cards

A worker that asks a question gives 2 to 4 options and says which one it
recommends:

```
REPORT
status: question
summary: need a port for the health check
question: Which port should the health check use?
options:
1. 8080 - matches the dev server
2. 9090 - separate from app traffic
recommended: 1
```

Option lines are numbered (`1.`, `1)`) or start with `- `; the text before the
first ` - ` is the answer and the rest is the reason. A `recommended` number
outside the list is ignored, and a report without options still parses as
before (so does one for `blocked`). The options are stored on the node's
report (`options`, `recommended`).

When a node ends waiting with a question, the chat says `T2 asks: <question>`
(`T2 is blocked: <summary>` for a blocked report, with no card) and a card shows
under the feed:

```
◇ T2 asks · Add the /health endpoint
Which port should the health check use?
› 1. 8080 (Recommended) - matches the dev server
  2. 9090 - separate from app traffic
  3. Type your own answer
```

| Key | Action |
|---|---|
| `↑` `↓` | Move the cursor; it starts on the recommended option, else the first |
| `1`-`9` | Pick that option |
| `Enter` | Choose the option under the cursor |
| `Esc` | Hide the card; the node stays waiting and the inbox item stays |

Picking an option replies to the node with `Answer: <option>`, as `@T2 Answer:
<option>` would. `Type your own answer` focuses the prompt filled with `@T2 `.
`/answer` (or opening the question from the inbox) shows a hidden card again.
With several waiting questions one card shows at a time, oldest first. A
question without options gets a card with only `Type your own answer`. The card
takes keys only while the feed is shown and the prompt is not in focus, so
nothing is taken while you type; when a question arrives and the prompt is
empty, the focus moves to the feed.

### Refused commands

With the `claude` runner in agent mode, the final event of a run lists the tool
calls Claude Code refused because they are not on the allow list (see [Agent
mode](#agent-mode)); decsr reads `permission_denials` from it and keeps them on
the node for the current attempt (`denied` in the session file, cleared when a
new attempt starts). Commands Claude Code treats as read-only are not refused.
When a node ends (done, waiting or failed) with refused `Bash` commands, a card
shows after any question card of that node:

```
⚠ T2 was refused 1 command
cmake --build build -j8
› 1. Allow for T2 and retry (Recommended)
  2. Always allow `cmake` in this project and retry
  3. No
```

The program of a command is its first word after `VAR=val` assignments; a chain
split on `&&`, `||`, `;` and `|` lists each program that is not allowed yet.

- **Allow for T2**: the programs are added to the node's own list
  (`extra_allowed` in the session file) that its later attempts pass as extra
  allowed commands, then T2 gets the reply ``The command `<cmd>` is now allowed.
  Run it and continue.`` and starts its next attempt.
- **Always allow**: the programs are also appended to `project.allowed_commands`
  in `.decsr/project.yaml`; the node retries as above. Other nodes use the new
  entry from the next session.
- **No** (or `Esc`) hides the card.

Nothing is allowed without a key press, at every autonomy level and in
automation runs: the card waits for you. A role marked read-only never gets the
allow options; the chat shows an info line. A refused tool other than `Bash`
(`Write`, `Edit`, `WebFetch`, ...) also gets an info line only.

### Run summary

When a run ends, meaning nothing runs and nothing can start, the chat gets one
message built from the graph (no model writes it):

```
Run finished · 3 done · 1 waiting · 0 failed
✓ T1 Add /health endpoint - Added the handler and route. (2 files)
✓ T2 Write tests - Added table tests for the handler. (1 file)
◇ T3 Pick a port - waits for your answer
✓ Build & test passed
Next: answer T3, /merge to bring the result into your checkout, or type a follow-up task.
```

It lists each work or fix node that ended since the previous summary, with the
summary of its report (else of the node) cut to the chat width, the number of
files it changed, the short error of a failed node, and the Build & test line
(`passed`, `failed`, `not run` or `– Build & test skipped · nothing changed`).
A read-only node (a `writes: false` task or a role that cannot write) whose reply was printed in the chat reads `answer above`. The `Next:` line names only what applies:
waiting nodes, `/merge` when the session is isolated and Build & test passed,
and `@T1 ...` to retry a failed node. It appears at every autonomy level.

### Suggested follow-ups

When a run ends and any node that ended in it suggested follow-ups (the `next`
lines of its report, the same text suggested twice counts once), the run summary
adds them before the `Next:` line:

```
Suggested follow-ups
  1. T2: update the README for the new --port flag
  2. T3: add a test for the empty config file
  y plan them · n drop
```

The same choice shows as a card in the feed. `y` sends one follow-up task to the
lead, as if you had typed it (`Follow-ups suggested by the finished nodes:` and
one `- T2: ...` line each); the lead splits it and the plan card still needs
your `y` before anything runs. `n` drops them. The keys act only while the feed
has the focus, so they never take a letter you are typing in the prompt. `Esc`
hides the card and `/answer` brings it back. The follow-ups are kept until you
pick `y` or `n` or the next run summary replaces them.

Reply to the node (`m`, or `@T3 <message>`) to answer. The reply archives the
attempt, with its report, and starts the next one. Its context includes what the
node asked:

```
Previous attempt (attempt 1, waiting — asked a question):
<excerpt>

Your question was:
<question>

The user replied:
<message>
```

For a blocked node the heading is `waiting — blocked` and the block that follows
is `You reported:` with its summary. `r` on a waiting node is refused: reply
instead.

## Coordinator mode

Coordinator mode lets the lead steer the graph while it runs. After a batch of
nodes has finished, the lead reads the reports and may propose to add, change
or cancel nodes. It is off by default.

```yaml
workspace:
  coordinator: true   # default false
```

`/coordinator on` or `/coordinator off` overrides the config for one session
(saved with it); `/coordinator` alone shows the state and where it comes from.
The prompt box shows the chip `coord` while it is on.

**When a review runs.** A batch is over when no node runs and at least one node
ended (done, failed or waiting) since the last review. A node you stopped does
not count. The lead then runs one review. It does not run when the coordinator
is off, when automation is paused (`S`), when the budget is reached (the
conversation says `auto › budget reached, review skipped`), when there is no
graph, or when the round limit is reached. A node that waits for you is not
answered by the lead: its question is shown to the lead, and it keeps waiting
for your reply.

**What the lead sees.** The task, the session summary, a table with one row per
node (id, title, role, status, report status with its summary or question, the
first line of the error, the number of files touched) and, for failed nodes, a
budgeted excerpt of what failed (at most 6000 characters, the same excerpt
builder as fix nodes). It is the lead of the split: the `pm` role's runner and
model. The call is counted in the session's lead usage and in the budget. The
prompt is the template `ws-review`.

**What it answers.** One JSON object, bare or in a code fence:

```json
{"note": "one line", "proposals": [
  {"action": "add", "title": "...", "description": "...", "role": "coder", "depends_on": ["T1"]},
  {"action": "change", "id": "T4", "title": "...", "description": "...", "role": "...", "depends_on": ["T2"]},
  {"action": "cancel", "id": "T5", "reason": "..."}
]}
```

An empty `proposals` list means no changes. An answer that cannot be read gives
no proposals and a note saying so.

**Proposal rules.** Invalid proposals are skipped and shown with the reason;
the valid ones still count. At most 8 proposals are applied per review.

- `add` creates a node `T<n>`; its `depends_on` must name existing nodes (never
  `CHECK`, and no cycle). Build & test depends on it and goes back to `todo` if
  it had run. A role that is defined is assigned; an unknown one leaves the node
  unassigned with that role as the suggestion, as after a split.
- `change` edits the title, description, role or dependencies of a `todo` or
  `failed` node. A failed node goes back to `todo`.
- `cancel` removes a `todo`, `failed` or `waiting` node and drops the edges that
  pointed at it, and adds `- T5 cancelled by the lead: <reason>` to the session
  summary. The cost of a cancelled node stays in the spent total.
- A `done` or `running` node and the Build & test node are never touched.

**Who applies them.** At autonomy level 1 a **Lead review** card appears in the
activity feed: `+ add T7 ...`, `~ change T4 ...`, `x cancel T5 ...`, skipped
proposals dim with their reason, and a "needs you" notification. `y` applies all
valid proposals and `n` rejects them (graph, sidebar or feed focus, shown in the
hint bar; y and n are keys of the graph). The proposals
are checked again when you press `y`, so a node that changed meanwhile is
skipped. At level 2 and 3 the valid proposals are applied at once, each with a
line `auto › lead: added T7 ...`, and then the new nodes get their roles and run
as after a split. A resolved card folds to one line (`Lead review 2 · 3 changes
applied ›`); select it with the arrow keys and press `Space` to expand it. The
last 20 reviews are kept in the session.

**Loop guard.** At most 5 reviews run by themselves per task. The count resets
when a new task, issue or import adds nodes. After the fifth review a "needs
you" notification says `5 lead reviews done for this task - /coordinator now to
continue` and no sixth starts by itself. `/coordinator now` runs one review now, also
past the limit (it counts, and does not reset the counter). It is refused while
nodes run, while a review waits for `y` or `n`, and when the budget is reached.
`Esc` cancels a review that is running.

## Usage limits

When a provider limits usage, the node does not fail. It becomes a waiting node
with a limit (`◷ waiting · resets 14:30`), its dependents stay blocked, and
decsr either retries it by itself or waits for you.

**Where the reset time comes from.** Only from data the CLIs and APIs already
return; decsr never calls a private account endpoint. A limit that no node hit
is not shown.

| Source | Reset data |
|---|---|
| OpenAI-compatible, LM Studio, Ollama, oMLX APIs (HTTP 429) | `Retry-After` (seconds or date), `x-ratelimit-reset-*` durations such as `6m0s` or `1h2m`, and times in the error body |
| Claude CLI | the `result` event with `is_error`: text such as `resets 2pm (Asia/Ho_Chi_Minh)` or the older `usage limit reached\|<unix time>` |
| Codex CLI | `exec --json` carries no rate-limit payload; the limit arrives as the `message` of a `turn.failed` or `error` event ("Try again at 2:30 PM." or "Try again at Nov 17th, 2025 2:30 PM.") |
| OpenCode, Copilot CLI | the error text of the CLI |

The parser reads relative text ("try again in 3h 2m", "retry after 120
seconds"), clock times ("resets 2pm", "reset at 14:30", with an optional zone
such as `(Asia/Ho_Chi_Minh)` or `UTC`; a time already past today means
tomorrow) and `resets_at` / `reset_at` values (RFC3339 or unix seconds). When
nothing parses the reset time is unknown.

**Display.** The reset time is local time, with the date when it is not today.
The node card shows `◷ waiting · resets 14:30`, the provider message and
`retry 1/3` after the first automatic retry. The sidebar shows `limit · resets
14:30`, the graph shows `waiting`, the counters count it as waiting
(`2/5 done · 1 waiting`) and the status bar shows `⬡ claude · limited until
14:30` for each runner that has a limited node.

**Retries.**

- At autonomy level 2 and 3, when the automation is not paused and the budget
  is not reached, the node is retried 30 seconds after the reset time
  (`auto › T3 retry 1/3 after the claude limit reset`). A retry continues the
  same attempt; it is not a reply.
- `workspace.limit_auto_retry: true|false` turns the automatic retry on or off.
  When unset it is on, except for the `claude` runner on a subscription login
  (no `ANTHROPIC_API_KEY`, Bedrock, Vertex, Foundry or `apiKeyHelper`), where
  it is off: the notice says `auto-retry is off on a subscription login; press
  r to retry`.
- At level 1, or when the reset time is unknown, the node waits for you: `r`
  retries it. Retries you start with `r` do not count toward the automatic ones.
- A node gets at most 3 automatic retries. After the third limit hit it fails
  with `limit reached on <provider>: <message> (3 automatic retries used)`.
- A reply (`m`) to a limited node retries it with your message and resets the
  retry count.

**Notifications.** At level 1 or with an unknown reset time you get a
needs-you note (`T3 hit the claude limit — resets 14:30, press r to retry`). At
level 2 and 3 you get an info note (`T3 waits for the claude limit — retrying
after 14:30`). A node that gives up sends a failed note.

## Cost and budget

DECSR knows tokens, not prices. To see money, give it prices in
`.decsr/project.yaml` or `~/.decsr/config.yaml`. There are no built-in prices,
because they go stale. The key is a model id or a glob (`path.Match` syntax);
the value is USD per million tokens. Later layers override per key.

```yaml
pricing:
  "deepseek-*":     {input_per_mtok: 0.27, output_per_mtok: 1.10}
  "*/gpt-5*":       {input_per_mtok: 1.25, output_per_mtok: 10.00}
  "claude-sonnet-*": {input_per_mtok: 3.00, output_per_mtok: 15.00}
workspace:
  budget_usd: 5        # 0 = no budget
  budget_warn_pct: 80  # default 80
```

An exact key beats a glob; among globs the longest matching pattern wins. Local
runners (`ollama`, `lmstudio`, `mlx`) always cost $0. Runners that bill through
a subscription CLI (`claude`, `codex`, `copilot`, `opencode`) are priced only
when you configured a price for their model. A node's cost is stored in the
session (`cost_usd`, `cost_known`); the session total is the nodes plus the
lead's tokens, priced with the `pm` model.

**Unknown prices.** A model without a price has an unknown cost. The status bar
then shows `≥ $1.23` (at least), and `/usage budget` lists the runners and models that
have no price. The budget counts only the known cost.

**Budget.** Before it starts any node (`r`, auto mode, a fix or a follow-up)
the engine compares the known cost of the session with the budget. At the warn
share (`budget_warn_pct`) it prints a message once and sends a needs-you
notification. When the cost reaches the budget it prints `Budget reached: $5.02
of $5.00 — raise workspace.budget_usd or /usage budget <amount> to continue` and
sends the notification; no new node starts and ready nodes stay ready. Nodes
that are already running are not stopped, so the final cost can pass the budget.
`/usage budget 10` sets the budget of this session only (saved in the session as
`budget_usd`); `/usage budget off` removes that override; `/usage budget` alone shows the
spending per runner. The cost of a node adds up over its attempts: a retried node
keeps the cost of the earlier runs, so they stay in the total and in the budget
check. The tokens shown for the node are those of its latest run. If any attempt
had no price, the node's cost is unknown (the status bar shows `≥`) and only
the priced attempts are counted.

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
They show as `⚠` on the node card (when it fits), in a `Warnings`
section of the inspector above the summary, and as `N warnings` in the progress
text at the top right.

## Isolation

```yaml
workspace:
  isolation: ""   # "worktree", "shared" or empty for auto
```

When the project is in a git repository with at least one commit (the default,
"auto"), every node works in its own git worktree, so parallel nodes cannot
overwrite each other's files and an agent CLI that edits files itself only
touches its own copy. `shared` runs all nodes in your checkout, as before;
`worktree` asks for isolation explicitly (it still falls back to shared outside
a git repository).

- The session gets an integration branch `decsr/<session id>` with a worktree
  in `.decsr/worktrees/<session id>/_integration`, created when the first node
  starts. It starts from a snapshot of your working tree: your last commit plus
  uncommitted changes to tracked files (taken with `git stash create`, so your
  files and your stash list are not touched). The snapshot is one plain commit
  on top of your last commit, `decsr: uncommitted changes at session start`.
- Each node gets the branch `decsr/<session id>-<node id>` and a worktree in
  `.decsr/worktrees/<session id>/<node id>`, created from the current tip of
  the integration branch. A node that depends on another sees the files that
  node wrote.
- When a node succeeds, its changes are committed (`<node id>: <title>`, with
  your git identity, or `decsr` when none is set) and merged into the
  integration branch; then its worktree and branch are removed. Merges are
  done one at a time. A node that fails or is stopped leaves nothing behind.
- If a node's changes conflict with nodes that were merged before it, the
  merge is aborted and the node ends failed with `merge conflict with earlier
  nodes in: <files>`. Its branch is kept; run the node again to redo it on top
  of the latest work.
- Build & test runs in the integration worktree, so it checks the combined
  result. Fix nodes are ordinary nodes: they get their own worktree and are
  merged when they succeed.
- `.decsr/worktrees/` is added to `.git/info/exclude`, so git ignores it.

**Untracked files are not copied.** Only tracked files exist in a worktree. If
your tree has untracked files, the first node shows a warning; `git add` the
ones the nodes (or the build) need. Ignored files such as `node_modules` or
`.env` are not there either.

**Getting the result back.** Nothing reaches your checkout until you type
`/merge`. It merges `decsr/<session id>` into the branch you have checked out
(`git merge --no-ff`) and removes the session's worktrees; the branch stays,
for example to push it. `/merge` refuses when no node has been merged yet, while
nodes are running, or when your checkout has uncommitted changes to tracked
files: commit or stash them first. Because the session started from a snapshot
that included those uncommitted changes, merging brings them back as commits,
which is why the tree must be clean. If the merge conflicts with your branch,
it is aborted, the files are listed and nothing changes. After Build & test
passes, the Workspace reminds you of `/merge`.

`/clear` and deleting a session remove that session's worktrees (not its
branches). Quitting leaves them, so `/resume` continues where you stopped.
Read-only roles run in a worktree too, but whatever they change is dropped.
The classic pipeline does not use worktrees.

## Diff and review notes

Press `d` on a node (sidebar or main pane, Activity or Graph mode) to see what
it changed, full screen. Finished cards in the feed show `d diff` in their dim
action row. `D` opens the diff of the whole session: everything the session
branch `decsr/<session>` holds that your current branch does not
(`git diff HEAD...decsr/<session>`); in a shared checkout it is the working
tree against `HEAD`, untracked files included. `/diff` does the same.

Where the diff comes from:

- **Worktree isolation, finished node:** the node's own commit. Its SHA is saved
  in the session as `commit`; it stays reachable through the merge into the
  session branch after the node branch is deleted. If the commit is gone (a
  manual `git gc` or branch surgery), the view says so in one line.
- **Worktree isolation, running node, or a node whose merge conflicted:** the
  node's worktree against the point it started from, including new files.
  Untracked files are read with `git diff --no-index /dev/null <file>`, which
  leaves the worktree's index alone.
- **Shared checkout:** `git diff HEAD` limited to the files the node reported,
  plus those files when untracked. The view says that it may include changes
  from other nodes or from you.
- Nodes that finished before `commit` was recorded show a message instead.

The view has the title (`T1 · Parse the --json flag · 3 files +61 −3`), a key
line, a file list on the left (from 100 columns; narrower, a one-line
`[ file 1/3: cmd/version.go ]` selector) and the diff with old and new line
numbers. Long lines are cut with `…`; `←`/`→` pan. Added and removed rows have a
subtle tinted background over the full width.

| Key | Action |
|---|---|
| `j` / `k`, arrows up and down | Move the line cursor (`▌`) |
| `PgDn` / `PgUp`, `Ctrl+D` / `Ctrl+U`, `Space` | Move a page |
| `g` / `G` | First / last row of the file |
| `n` / `p` | Next / previous hunk (crossing into the next or previous file) |
| `]` / `[`, `Tab` / `Shift+Tab` | Next / previous file |
| `←` / `→`, `h` / `l`, `0` | Pan 8 columns / back to the start |
| `c` | Add or edit a note on the cursor line (`Enter` saves, `Esc` cancels) |
| `x` | Delete the note on the cursor line |
| `S` | Send the notes of this node (see below) |
| `Esc` or `q` | Back |

**Review notes.** A note sits under its line in a small box (`Note · line 27`
and the text). Notes are saved in the session file under `notes` (node, path,
line, side `new` or `old`, text), so they survive a restart; a removed line
carries its note on the old side. Notes are only for node diffs, not for `D`.

`S` turns the notes of the open node into one follow-up node,
`Address review notes on T1`, with the role of the source node (unassigned when
it has none or the role is not defined) and no dependencies. Its description
lists every note as `path:line — text`, followed by the line and the lines
around it from the diff. The notes are then cleared, and the conversation says
`Added T7 from 2 review notes — press r to run`.

## Start from an issue

`/issue <ref>` turns an issue into the task of the session:

```
/issue 42                                  # GitHub issue of this repository ("#42" works too)
/issue https://github.com/o/r/issues/42    # a full URL, also of another repository
/issue decsr-ab1                           # a bead of this project (bd)
/issue 42 --force                          # also start a closed issue
```

The Workspace fetches the issue (`gh issue view`, or `bd show <id> --json`),
shows `Issue #42: Fix flaky login test` in the conversation and sends the lead
`Issue #42: <title>`, the body (cut at 8 KB) and `Labels: …`, exactly as if you
had typed it. The session remembers the issue (`issue` in the session file).
Digits, `#N` and a GitHub issue URL mean GitHub, anything else is a bead id.
`/issue` is refused while nodes run, and for a closed issue unless you add
`--force`. `gh` must be logged in; `bd` must be on PATH for beads.

From the shell, `decsr issue <ref> [--force]` (or `decsr --issue <ref>`) opens
the Workspace with a fresh session and runs the same steps after it starts.

The pull request of that session links the issue: `Closes #42` on its own line
(`Closes owner/repo#42` for another repository), or `Refs <bead-id>` for a bead.
DECSR does not change the bead's status.

## Import from Spec Kit

`/import speckit [--phase N] [path]` turns the task list of a
[Spec Kit](https://github.com/github/spec-kit) feature into nodes, without a
lead split:

```
/import speckit                     # the newest specs/*/tasks.md
/import speckit specs/003-login     # a feature directory or a tasks.md
/import speckit --phase 2           # only "Phase 2"; dependencies start fresh
```

Without a path the most recently modified `specs/*/tasks.md` is used and the
chat says which. The command is refused while nodes run.

The parser reads what `/speckit.tasks` writes: `## Phase N: Name` headings and
lines like `- [ ] T012 [P] [US1] Create User model in src/models/user.py`.
Lines in `[x]` are skipped and counted, checkpoint lines, notes and fenced
examples are ignored. Each open task becomes a work node:

- Title: `T012 Create User model in src/models/user.py` (the Spec Kit ID first,
  cut to about 80 characters). The node ID stays `T<n>` like any other node.
- Description: the full task line, the phase, and
  `Spec: specs/<feature>/spec.md, plan: specs/<feature>/plan.md` so the worker
  reads them.
- The graph title is the first heading of `spec.md` (or the directory name).
  Nodes are added to the current session, or start the graph when it is empty.
  The Build & test node is added or updated as after a lead split.

Dependencies follow Spec Kit's implement rules (phase by phase, `[P]` tasks run
together, sequential tasks in order):

1. A task depends on every imported task of the previous imported phase.
2. Inside a phase a task without `[P]` waits for the task before it and for the
   `[P]` tasks directly before it, so a service waits for its models.
3. A `[P]` task depends on the previous phase only.
4. `(depends on T012, T013)` in a task adds those tasks when they are imported.

With `--phase N` only that phase is imported, so rule 1 has nothing to refer to.

Roles are suggestions, shown as `-> role`; press `A` to accept them, as after a
lead split. A task that mentions tests (`test`, `_test.go`, `test_*.py`,
`*.spec.ts`) suggests `qa` when that role exists, everything else `coder`.

An import follows the autonomy level like a lead split: at level 2 and 3 the
suggested roles that are defined are accepted and the run starts.

Limits: a list of more than 40 tasks is imported anyway, with a hint to import
it phase by phase.

**Write-back.** Every imported node remembers its task (`spec: {file, task_id}`
in the session file). When the node finishes successfully (done; failed and
stopped nodes do nothing, read-only roles count), DECSR changes the first line
`- [ ] T003 ...` of that task in `tasks.md` to `[x]` and leaves everything else
as it was (indentation, `*` bullets, CRLF line endings, trailing newline). A
task that is already `[x]` is left alone. When the line is gone because you
edited the file, the node gets the warning `tasks.md: T003 not found, not marked
done`. A failed write is a node warning too and never fails the node. Where the
tick goes:

- Worktree isolation and `tasks.md` tracked by git: the tick is edited in the
  integration worktree, after the node's merge, and committed there as
  `decsr: mark T003 done in specs/003-login/tasks.md`. It also happens when the
  node changed no code. The tick reaches your checkout with `/merge` and shows
  in the pull request, and your checkout stays clean, so `/merge` is not blocked.
- `tasks.md` not tracked, or shared isolation: the file in your checkout is
  edited directly (a temp file and a rename, the file mode is kept). An untracked
  file does not block `/merge`.

**Uncommitted spec files.** Worktrees are made from a snapshot of your tracked
files, so untracked files are not in them. When `tasks.md`, `spec.md` or
`plan.md` of the feature is untracked and the session uses worktrees, the
import still runs and adds one line: `specs/003-login is not committed: agents
in worktrees can't read spec.md/plan.md — run git add specs/003-login && git
commit, then import again`. Tracked files with uncommitted changes are fine:
the snapshot includes them.

**Auto-detect.** In a fresh session without nodes (and after `/clear` or an
import) DECSR scans `specs/*/tasks.md` once for the most recently modified file
with at least one open `- [ ] T<n>` task, at most 200 feature directories deep.
The empty state then shows `Spec Kit: specs/003-login has 12 open tasks —
/import speckit`, and `/import speckit` becomes the prompt suggestion. Projects
without a `specs/` directory are not affected.

## Pull requests

`/pr` publishes the session as a GitHub pull request, and `/pr watch` keeps it
green. It uses the `gh` CLI (it must be on `PATH` and logged in, `gh auth
login`) and git; DECSR never talks to GitHub itself.

```yaml
workspace:
  pr_base: ""          # base branch; empty = the branch checked out in your project
  pr_remote: ""        # remote to push to; empty = origin
  pr_poll_seconds: 60  # /pr watch polling period; at least 15
  max_ci_fixes: 3      # CI fix rounds before DECSR stops adding fix nodes
  pr_auto_fix: false   # run the fix nodes of /pr watch without asking
```

**`/pr`** needs worktree isolation (see [Isolation](#isolation)), at least one
node merged into `decsr/<session id>`, no node running, `gh` logged in, and the
remote. It refuses when the base branch is the session branch. It then:

1. pushes `decsr/<session id>` to the remote (`git push -u`; if the push is
   rejected because someone else pushed to the PR branch, e.g. a reviewer
   applied a suggestion on GitHub, `/pr` stops with an error and overwrites
   nothing: pull their commits into the session branch or push manually);
2. if an open pull request already exists for that branch, stops there and shows
   its URL; otherwise runs `gh pr create` with the base branch above. The title
   is the first line of the session's first task (72 characters at most; `decsr
   session <id>` when there is none). The body is plain Markdown: the task, a
   table of the nodes (id, title, role, status, number of files), the summary of
   each finished node and the Build & test result;
3. prints the URL in the conversation and remembers the pull request number, the
   URL, the review comments already handled and the number of CI fix rounds in
   the session file, so `/resume` carries on.

Only the session branch is pushed: your own branch and your uncommitted files
stay local. The first commit of the branch is `decsr: uncommitted changes at
session start` when your tree had changes, otherwise the branch starts at your
current commit.

**`/pr watch`** does the same and then polls every `pr_poll_seconds`. `/pr
watch` again, `/pr stop`, `/clear`, switching session or leaving the Workspace
ends it. Each poll looks at:

- **CI checks** (`gh pr checks`). When all checks pass you get a note in the
  conversation and a notification ("PR checks passed"). When a check fails, once
  per head commit of the pull request, DECSR fetches the failed log of the
  GitHub Actions run (`gh run view --log-failed`, the last 150 lines, at most
  8 KB) and adds a fix node `F<n>` titled `Fix CI: <check names>` with that log
  in its description. Checks that are not Actions runs get a node without a log.
- **Review comments** (`gh api` for the pull request's line comments and for
  reviews that request changes). New ones, all of them, go into one node `Address
  review comments`, one `path:line — text` line each; their ids are remembered so
  they are never added twice.

Fix nodes get the role `coder_fixer` (the one normal fix nodes suggest), and
Build & test is queued behind them. They are not run for you: the conversation
says `press r to run`. With `pr_auto_fix: true` they start at once. When a fix
node finishes and is merged into the session branch while you are watching, the
branch is pushed again, so CI runs on the fix.

Limits: after `max_ci_fixes` CI fix rounds DECSR adds no more CI fix nodes, says
so and sends a failure notification. Review comment nodes are not counted.
A poll that fails (network, `gh` error) is shown in the status line and retried
at the next tick. Build & test is not run again before the automatic push.

## Build & test and fix nodes

The `CHECK` node ("Build & test") needs no agent. It is added when a split has
at least one node that writes (a plan of read-only nodes gets none), and a
follow-up split adds it, or moves and resets it, only when one of its new nodes
writes. It runs the build and test commands for the project's language (see
Project Setup for the language) once every other node is done.

For a C or C++ project with a `CMakePresets.json` (and optional
`CMakeUserPresets.json`) it uses the presets: the first non-hidden configure
preset that has a build preset, built with `cmake --preset <name> && cmake
--build --preset <build>`, and tested with `ctest --preset <test>` when a test
preset exists, else `ctest --test-dir <binaryDir> --output-on-failure`. When the
preset's `CMAKE_MAKE_PROGRAM` lives under a `Tools/<name>/` directory that also
holds `CMake/bin/cmake`, that `cmake` and `ctest` are used instead of the ones
on `PATH`. Without presets the commands are `cmake -S . -B build && cmake
--build build` and `ctest --test-dir build --output-on-failure`. Set
`project.build_cmd` and `project.test_cmd` in the project config to replace the
detected build and test commands.

When its turn comes it runs only if a node ended after the last passed run of
Build & test and changed a file that is not documentation (`*.md`, anything
under `doc/` or `docs/`, `LICENSE*`), judged from the node's files, the files
seen on disk and its diff stats. Otherwise it ends as `skipped · nothing
changed`: it is done, but not passed, so the run summary says `– Build & test
skipped`, no `/merge` hint is given, and autonomy levels 2 and 3 add no fix node
and open no pull request after it. `/check` runs it now whatever changed.

When the task is split, DECSR resolves those commands if it can and stores them
on the node. If none are known yet (for example the project has no files), the
node is added without commands and they are resolved when it runs, so a
language that is detected by then is used. If there are still none when it
runs, it finishes with a warning instead of passing silently. If it fails, select it and press `f`: DECSR adds a
fix node (`F1`, `F2`, ...; a number is never reused in a session, not even after
`/compact`) that receives the failure output, suggested role
`coder_fixer`. Assign it, run it, and Build & test is queued again behind it.

## Autonomy levels

By default nothing runs until you press `r` or `R`. `workspace.autonomy` (or
`/autonomy` for one session) lets DECSR do more by itself, one step at a time.
Raise the level only when you trust Build & test to catch a bad result.

| Level | Name | What runs by itself |
|---|---|---|
| 1 | manual (default) | Nothing. You accept roles, run nodes, `/merge` and `/pr`. |
| 2 | assisted | After a split (also from `/issue` and `/import speckit`): accepts every suggested role that is defined, as `A` does, and runs everything, as `R` does. Nodes whose suggested role is not defined stay unassigned and you get a "needs you" note. When Build & test fails it adds one fix node, as `f` does, and runs it; Build & test then runs again behind it. At most `workspace.max_auto_fixes` (default 2) per session; after that it sends a failure note and stops. |
| 3 | unattended | Level 2, and when Build & test passes it pushes the branch, opens or updates the pull request (`/pr`) and starts `/pr watch` with the fix nodes of CI and review comments running without asking, for this session only (the config `pr_auto_fix` is not changed). |

```yaml
workspace:
  autonomy: 2        # 1 | 2 | 3; 0 or empty = 1
  max_auto_fixes: 2  # automatic fix nodes per session after a failed Build & test
```

`/autonomy` shows the level and what it does; `/autonomy 2` sets it for the
session (saved with the session, over the config value). The prompt box shows
`L2 assisted` or `L3 unattended` above level 1. Every automatic action adds a
dim line to the conversation that starts with `auto ›`, for example
`auto › accepted 4 roles and started the run` or
`auto › Build & test failed, added F1 (1/2)`.

Safety rules:

- Automation never runs `/merge` and never merges the pull request.
- Level 3 needs a budget (`workspace.budget_usd` above 0, or `/usage budget`) and
  worktree isolation. `/autonomy 3` without them is refused and sets level 2.
  A configured level 3 without them is announced once and behaves as level 2.
- A reached budget stops automation at every level: no new fix nodes and no
  push. DECSR says so and sends a note.
- `S` (stop all) pauses automation until you run a node (`r`, `R`) or set
  `/autonomy` again. The prompt box shows `auto paused` meanwhile.

## Notifications

When something needs you, DECSR can alert you outside the terminal, so you can
leave a long run alone. The Workspace sends a short note (title `decsr` or
`decsr · <project>`, body up to 140 characters) for:

- a split that leaves sub-tasks waiting for roles ("Assign roles: 3 sub-tasks
  are waiting");
- a plugin question that waits for y or n;
- a node that failed ("T2 failed: <first line of the error>");
- Build & test passed (with "/merge to apply" when nodes run in worktrees) or
  failed;
- the end of a run ("Run finished: 4 done, 1 failed").

There is no note per node start or per output chunk, and an identical note is
sent only once in 10 seconds. The classic pipeline sends notes for approval
gates, errors, rate limits and completion.

Channels, all on by default except the webhook, each best-effort:

| Channel | What it does | Switch off |
|---------|--------------|------------|
| cmux | cmux notification, only when cmux runs | `disable_cmux: true` |
| bell | `\a` written to the terminal (`/dev/tty`); not on Windows | `disable_bell: true` |
| desktop | `notify-send` on Linux (needs `DISPLAY` or `WAYLAND_DISPLAY`), `osascript` on macOS | `disable_desktop: true` |
| webhook | HTTP POST of each note to `webhook` | leave `webhook` empty |

```yaml
# .decsr/project.yaml or ~/.decsr/config.yaml
notifications:
  disable_bell: false
  disable_desktop: false
  webhook: https://ntfy.sh/<your-topic>   # phone push through ntfy
  webhook_format: ntfy                    # ntfy (default) | slack | discord
```

With `ntfy` the body is the text and the title, priority (high for "needs you"
and failures) and tags go in headers. `slack` and `discord` post JSON. The
webhook URL may contain a secret, so DECSR shows only its host in messages and
`decsr doctor`, which also has a `notify` line listing the active channels.

## Where state is stored

Graph and conversation are saved with the session, in
`.decsr/sessions/<id>.json`, after every change. If you quit or crash with
unfinished work, run `decsr resume` or type `/resume` to reopen it. Nodes that
were running are reloaded as not started. The Workspace does not use the
classic pipeline's run state or beads issues. Profiles live in
`~/.decsr/profiles/`, custom commands and skills in `.decsr/commands/` and
`.decsr/skills/` (project) or the same folders under `~/.decsr/` (user).

## Detached runs

`decsr --tmux` runs the whole Workspace inside a tmux session, so closing the
terminal or losing an SSH connection does not stop your runs. It also works
with other flags, e.g. `decsr --tmux --issue 42`.

- Session name: `decsr-<slug>-<hash>`, where the slug is the project directory
  name lowercased with every character outside `[a-z0-9-]` turned into `-`,
  and the hash is 4 hex digits of the sha1 of the absolute path.
- The project path is stored in the tmux user option `@decsr_project`.
- If the session exists, `decsr --tmux` attaches to it (with `--issue` it
  refuses: attach and use `/issue`). Inside tmux (`$TMUX` set) it just runs.
- `decsr ls` lists the sessions (name, project, attached or detached, created).
- `decsr attach [name|project-path]` attaches; without an argument it uses the
  session of the current directory, or the only decsr session, and lists them
  when several are running.
- Detach with the tmux prefix and `d` (the Workspace footer shows the keys,
  `Ctrl+b d` by default). Quitting decsr ends the session. Notifications tell
  you when a run needs you.
- `ui.tmux: true` in the config makes plain `decsr` behave like `decsr --tmux`.
- Needs tmux; not supported on Windows. `decsr doctor` checks for tmux.

## Activity, context and fix excerpts

- Each node keeps a journal of what its agent did (thinking, commands, file
  reads and edits, errors). It is saved in the session: at most 200 steps per
  node, each detail cut to 500 characters. Older steps are dropped and counted.
  The full detail (up to 2,000 characters) stays in memory only. A re-run starts
  the journal empty.
- A worker node records the estimated size of its prompt (`ctx`), with the
  model limit when `max_context_tokens` is set for its role. Build & test nodes
  have no context size.
- A fix node gets a `What failed:` excerpt of up to 8,000 characters: failing
  commands with the end of their output first, then errors, then the files
  touched. Reasoning and messages are left out, and `[… N characters omitted]`
  shows where text was cut.

## Reaching the classic pipeline

- Type `/menu` in the Workspace to open the Home launcher. Its
  Classic pipeline action (`1`) starts the old New Task flow; Workspace (`w`)
  returns here, and `Enter` on a saved session opens the Workspace on it.
- Changes made in the menu (settings, a different project) apply when you come
  back to the Workspace, which then opens in the selected project.
- `decsr task ...` and `decsr run --requirements ...` behave as before, and
  `--ui plain` never opens the Workspace.
