# Workspace

The Workspace is the screen `decsr` opens by default. You type a task in a chat
box, a lead agent splits it into sub-tasks, you decide which agent does each
one, and the assigned sub-tasks run in parallel. Every sub-task is a node in a
graph, so you can see who does what and what is waiting on what.

Nothing runs until you press a key. The classic pipeline (Context, Spec, Plan,
Code, QA and so on) is unchanged; see [Reaching the classic pipeline](#reaching-the-classic-pipeline).

## Flow

1. **Chat.** The start screen is a chat box. No repository compaction runs at
   start; the lead only gets a cheap, locally collected project summary (plus
   `.decsr/context_pack.md` if one already exists). Type a task and press
   `Enter` (`Alt+Enter` adds a new line).
2. **Split.** The lead (the `pm` agent's runner and model) splits the task into
   nodes. Each node has a title, a description, the nodes it depends on and a
   *suggested* role. If the reply is unusable (no result, a dependency cycle, a
   dependency on a node that does not exist) you see one error line and can
   send the task again. If the lead gives several sub-tasks but no role for any
   of them, it is asked once more for the complete answer. The lead's full raw
   answer of the last split is saved to `.decsr/workspace_split.txt`, so you can
   see what it actually said.
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

On the start screen:

| Key | Action |
|---|---|
| `Enter` | Send the task to the lead |
| `Alt+Enter` | New line in the task |
| `/` | Open the command list |
| `PgUp` / `PgDn` | Scroll the chat |
| `?` | Help (when the box is empty) |
| `Esc` | Cancel the split while it runs |
| `Ctrl+C` | Quit |

On the graph:

| Key | Action |
|---|---|
| arrows or `h j k l` | Move between nodes |
| `Enter` or `o` | Open the full output of the node |
| `i` | Show or hide the inspector |
| `a` | Pick the role for the selected node |
| `A` | Accept all suggestions that are defined roles |
| `r` | Run the selected node |
| `R` | Run every assigned node as dependencies finish |
| `s` | Stop the selected node |
| `S` | Stop all running nodes |
| `f` | Add a fix node after Build & test has failed |
| `n` | New task (asks first if work is unfinished) |
| `/` | Command list |
| `?` | Help |
| `q` | Quit (asks first while nodes run) |

In the output view: `Esc` or `q` returns to the graph, `s` stops the node, and
the arrow and page keys scroll.

### Commands

Type `/` (or press `/` on the graph) to filter and run a command with `Enter`.

| Command | What it does |
|---|---|
| `/help` | Keyboard help |
| `/roles` | List all roles and create a new one |
| `/resume` | Reopen the last unfinished graph |
| `/menu` | Open the classic Home menu |
| `/quit` | Quit |

Quitting while nodes run asks for confirmation, then stops them and kills the
agent processes.

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
fix node (`F1`, `F2`, ...) that receives the failure output, suggested role
`coder_fixer`. Assign it, run it, and Build & test is queued again behind it.

## Where state is stored

The graph is saved to `.decsr/workspace.json` after every change. If you quit
or crash with unfinished work, use `/resume` on the start screen to reopen it.
Nodes that were running are reloaded as not started. The Workspace does not
use the classic pipeline's run state or beads issues.

## Reaching the classic pipeline

- Type `/menu` in the Workspace to open the Home menu. Its `CLASSIC PIPELINE`
  item (`1`) starts the old New Task flow; `WORKSPACE` (`w`) returns here.
- Changes made in the menu (settings, a different project) apply when you come
  back to the Workspace, which then opens in the selected project.
- `decsr task ...` and `decsr run --requirements ...` behave as before, and
  `--ui plain` never opens the Workspace.
