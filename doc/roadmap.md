# Roadmap

`decsr` stays a terminal-first orchestrator. These phases borrow workflow
ideas from the desktop app Orca (stablyai/orca) without its desktop or mobile
surface. Phases are built one at a time, in order. Each one is tested and
committed before the next starts.

Phases 5–8 improve the UI/UX, borrowing Orca's interaction ideas. Each UI
phase starts with text mockups that you approve before anything is built.

## Principles

- Terminal first. Every feature works in the TUI and over SSH.
- Deterministic context handling. decsr never asks a model to write a summary
  of a node. It cuts budgeted excerpts, like Orca's handoff.
- Every automatic action is visible (an `auto ›` line) and bounded (budgets,
  caps).
- Nothing merges into your checkout, and nothing merges a PR, on its own.
- Usage limits come only from data the CLIs and APIs return. decsr never calls
  private account endpoints.

## Phase 1 — Visibility

**Goal.** See what every node is doing while it runs, and keep what happened
in the session. Build the context pieces that later phases reuse.

Structured activity events from the runners are already in place: Claude
stream-json (thinking, tools, results), the OpenCode live JSON stream, Codex
`exec --json`, and decsr's own file writes with `+`/`−` counts and Build & test
commands. This phase builds the UI and the context handling on top.

**Features**

- Node card: a running card shows the last 5 activity steps, for example
  `▸ go test ./… 12s`, `✎ Edited cmd/version.go +18 −2`, `· thinking…`.
- When the node is done, the steps fold into `Worked for 1m 12s ›`. `Space`
  expands them.
- Output view (`o`): shows the full timeline. `t` toggles raw text.
- Sidebar row: shows the current action while the node runs.
- Thinking: one dim line with the first words. The full text shows only when
  expanded.
- Saved per node in the session: at most 200 steps, each detail cut to 500
  characters. Full detail stays in memory only. A resumed session shows what
  happened.
- Stall: no activity for 60 s shows `no activity for 1m` on the card and in
  the sidebar. After 5 min a NeedsYou notification is sent.
- Context size per node on the card and in the inspector: `ctx 18k`. The model
  limit (`ctx 18k / 128k`) shows only when it is known (config or local
  model).
- Fix handoff: a fix node gets a budgeted excerpt of at most 8,000 characters
  from the failed work. Order: failing commands with output tails, then
  errors, then files touched. Reasoning is dropped. An omission marker shows
  where text was cut.
- Auto-compact: at autonomy L2 and L3, `/compact` runs automatically when 10
  or more nodes are done, with an `auto ›` line. At L1 decsr only suggests it.

**Done when**

- [x] A running node shows its last 5 steps and a stall notice after 60 s.
- [x] A finished node shows `Worked for …`, and `Space`, `o` and `t` work.
- [x] A resumed session shows the saved steps, capped at 200 per node.
- [x] `ctx` shows on the card and in the inspector, with the limit when known.
- [x] A fix node's prompt contains the excerpt, never more than 8,000
      characters.
- [x] At L2/L3 the tenth done node triggers `/compact`; at L1 it only suggests.

**Builds on.** Runners, node cards and inspector, sessions, Build & test and
fix nodes, autonomy levels, `/compact`, notifications.

## Phase 2 — Agent mode and reply to a node

**Goal.** Let a runner work as a full coding agent in the node's worktree,
and let you talk to a node after it has run.

**Features**

- Per-role `mode: text | agent`. Defaults match today's behaviour: OpenCode
  and Codex are `agent`, Claude is `text`.
- In agent mode decsr takes the node worktree's diff as the result and ignores
  file blocks in the text.
- Claude in agent mode gets a fixed set of pre-approved tools: `Read`, `Edit`,
  `Write`, `Glob`, `Grep`, and `Bash` limited to the project's build/test
  commands plus read-only git (`git status`, `git diff`, `git log`,
  `git show`). Everything else is refused. There are no interactive
  permission prompts in this phase. Approving permissions from decsr may come
  later.
- Terminal access: open a shell in the node's worktree. Under `--tmux` it is a
  tmux split. Otherwise decsr suspends the TUI.
- Reply to a node: type `@T3 <message>` in the prompt (`@` completes node IDs)
  or press `m` on a selected node (it fills `@T3 `).
- A reply starts a new attempt of the same node, not a child node. History is
  kept, the card shows `attempt 2`, and the result is merged again.
- Context for a reply is rebuilt by replay: the node's task, its summary, a
  budgeted activity excerpt and your message. This works for every runner.
  Resuming a CLI's own session (`claude --resume`) is a possible later
  optimization.
- A reply to a running node is queued and starts when the current run
  finishes. `s` still stops the run.

**Done when**

- [x] A role can be set to `mode: agent` or `mode: text`, with the defaults
      above.
- [x] In agent mode the node result is the worktree diff.
- [x] Claude agent mode refuses any tool outside the fixed list.
- [x] The shell command opens a split under `--tmux` and suspends the TUI
      otherwise.
- [x] `@T3 …` and `m` start `attempt 2` of T3, with history kept.
- [x] A reply to a running node waits until the run ends.

**Builds on.** Roles and runners, worktree isolation, `/merge`, `--tmux`,
the activity excerpt from Phase 1.

## Phase 3 — Coordinator mode

**Goal.** Let the lead steer the graph while it runs: read the reports, then
add, change or cancel nodes.

**Features**

- Every worker ends with a structured report: `status: done | blocked |
  question`, a summary and an optional question. decsr parses it. A missing
  report counts as `done`, with the last text as the summary.
- The lead reviews each batch, which is when all currently running nodes have
  reported. It proposes adding, changing or cancelling nodes.
- Applying proposals follows the autonomy level:

  | Level | Proposals |
  |-------|-----------|
  | L1 | A proposal card; accept with `y`, reject with `n` |
  | L2, L3 | Applied automatically, each with an `auto ›` line |

- A node's `question` always waits for you. You answer by replying to the node
  (Phase 2).
- Loop guard: at most 5 review rounds per task. After that a NeedsYou
  notification asks whether to continue. The budget gate still applies.

**Done when**

- [x] Reports are parsed, and a missing report becomes `done`.
- [x] The lead reviews once per finished batch.
- [x] At L1 a proposal card waits for `y`/`n`; at L2/L3 changes apply with
      `auto ›` lines.
- [x] A `question` report stops the node until you reply.
- [x] The sixth review round does not start without your answer.

**Builds on.** The lead, the node graph, autonomy levels, budgets,
notifications, reply to a node (Phase 2).

## Phase 4 — Automations and usage limits

**Goal.** Run recurring tasks unattended and stay safe when a provider
limits usage.

**Features**

- Automations live in `.decsr/automations.yaml`. Each has a name, a schedule,
  a task or an issue query, an autonomy level and a budget.
- The system scheduler runs them. `decsr automation install` writes a crontab
  line that calls `decsr automation run <name> --tmux`. There is no decsr
  background service.
- Safety rules:
  - An automation needs autonomy L2 or L3 and a budget.
  - A run is skipped if the previous run of the same automation is still
    active.
  - A run that changes nothing only notifies "nothing to do". No PR is opened.
- Usage limits are shown only from data the CLIs and APIs return: rate-limit
  errors with reset times, Codex rate-limit events, API usage. decsr never
  uses private account endpoints.
- A limited node shows `waiting · resets 14:30`. At L2/L3 it retries
  automatically after the reset. At L1 it waits for you. A notification is
  sent either way.
- At most 3 automatic retries per node. Then the node fails with the limit
  message.

**Done when**

- [x] `decsr automation install` writes the crontab line, and
      `decsr automation run <name> --tmux` starts a detached run.
- [x] An automation without L2/L3 or without a budget is refused.
- [x] An overlapping run is skipped, and an empty run notifies without a PR.
- [x] A rate-limited node shows the reset time and retries at L2/L3, up to 3
      times.

**Builds on.** `--tmux`, autonomy levels, budgets, notifications, `/pr` and
`/pr watch`, starting from an issue, runners.

## Phase 5 — Consistency and theme

**Goal.** Every screen looks and behaves like the Workspace, and light and dark
terminals both work.

**Features**

- One shared set of UI components in an internal package: header, pane label,
  card, list row, hint bar, input box, overlay and status glyphs. All screens
  use it. The old galaxy styles are deleted once unused.
- Restyled screens: Home, Global Settings, Project Setup, Model Memory, the
  project picker, help, the artifact viewer, file preview, the editor, and
  chat/negotiate.
- Home becomes an Orca-style launcher: recent projects and sessions on the
  left, actions on the right. Plain `decsr` still opens the Workspace.
- The classic pipeline is frozen: it gets only the shared header, hint bar and
  colours. There is no new UI work for it, and its removal is decided later.
- Light and dark palettes, chosen with `ui.theme: auto | dark | light`. `auto`
  asks the terminal for its background once at startup with a short timeout
  and falls back to dark when it does not answer (common inside tmux).

**Done when**

- [x] No screen uses the old galaxy styles.
- [x] Home is the launcher, and plain `decsr` still opens the Workspace.
- [x] Every listed screen uses the shared components.
- [x] `ui.theme` works, and `auto` falls back to dark.
- [x] The classic pipeline shows the shared header, hint bar and colours.

**Builds on.** The Workspace look (Phase 1 and earlier), setup screens, `ui`
settings.

## Phase 6 — Inbox, palette and first run

**Goal.** Never miss something that needs you, find anything fast, and make the
first start easy.

**Features**

- Inbox: a count at the top of the sidebar (`● 3 need you`) and a list opened
  with `N`. Enter jumps to the item.
- The inbox lists:
  - nodes waiting on a question, a block or a usage limit
  - L1 review cards
  - failed nodes without a fix
  - nodes stalled for 5 minutes or more
  - pending plugin prompts
  - budget warnings
  - the last automation results that failed or need you
- Actionable items clear themselves when resolved. Informational items
  (automation results, budget warnings) are cleared with `x`.
- Ctrl+K palette: one search box across inbox items, commands, nodes, sessions
  and projects, in that order, with fuzzy matching. It sits next to the
  existing `/` and `@` palettes.
- First-run setup: opens when no runner is usable at startup (the same rule as
  `decsr doctor`'s required backend check). Pick a runner, connect a key if
  needed, see a short doctor result, and land in the Workspace. `Esc` skips
  it, and `decsr setup` opens it any time.

**Done when**

- [x] The sidebar count and the `N` list show all listed item types and clear
      as specified.
- [x] Ctrl+K finds items of every kind.
- [x] The first-run setup opens only when no runner is usable.
- [x] `decsr setup` opens the first-run setup.

**Builds on.** Waiting nodes, reviews, limits, automations, notifications,
`decsr connect`, `decsr doctor`.

## Phase 7 — Lean runners and dsh

**Goal.** Cheaper, more stable worker runs, and a runner for the DeepSeek
Harness CLI.

**Features**

- Lean Claude runs. Every `claude` run (text and agent mode) starts with
  `--strict-mcp-config --mcp-config '{"mcpServers":{}}'`,
  `--setting-sources project,local` and
  `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`. `--bare` is not used: it breaks
  subscription logins. A role with `mcp: true` skips the two MCP flags, so the
  user's own MCP servers load.
- Prompt order for prefix caching. The worker prompt is now static intro,
  output format, role, then project context; the sub-task is only the user
  message. The split and review prompts put their static rules and output
  format above the dynamic sections. The start of every prompt is the same from
  call to call.
- Better failure excerpts. When a failing command's output is cut, the end is
  kept as before and lines of the cut part that mention an error, failure,
  panic or exception are kept too, with 2 lines of context, in the same
  character budget. Omission markers stay accurate.
- The `dsh` runner. It runs `dsh --profile headless --json -` with the task on
  stdin and reads the event stream: text, thinking, tool calls and results,
  usage and the final reason. A run succeeds only when dsh exits 0 and the
  final reason is `completed`. Agent mode uses the `workspace-write` preset
  with the shared git directory granted; text mode and read-only roles use the
  `read-only` preset. Approvals are never asked for. The model
  (`provider/model`) and effort are set with a temporary patch file, which is
  removed after the run. The DeepSeek key comes from the environment or the
  key saved by `decsr connect deepseek`.

**Done when**

- [x] Claude runs carry the lean flags and the env variable, and `mcp: true`
      removes only the MCP flags.
- [x] The worker, split and review prompts keep their static part first.
- [x] A cut command output keeps its error lines and the end within the
      budget.
- [x] `runner: dsh` works in text and agent mode, with model, effort, usage and
      rate-limit detection.

**Builds on.** Agent mode, the runner factory, handoff excerpts, `decsr
connect`.

## Phase 8 — Rendering

**Goal.** Make long text and code changes easy to read.

**Features**

- Markdown with Charm's `glamour` in feed messages (yours and the lead's),
  node summaries, report questions and message items in the output view. It
  renders at full width, falls back to plain text below 60 columns, and is
  never used inside the sidebar or graph cards.
- Syntax-highlighted diffs with `chroma` (already a dependency).

**Done when**

- [x] Those places render Markdown, with the plain fallback.
- [x] The diff view highlights code by language.

Approved details: the glamour style is built from the decsr theme tokens (no
`#` on headings, `•` bullets, muted quotes); text stays plain while it is
streaming, and `ui.markdown: false` turns Markdown off. Code uses chroma
`github-dark` on a dark theme and `github` on a light one. The diff view lexes
each hunk as an old and a new side, so multi-line comments colour right, and
skips files with more than 2000 changed lines.

**Builds on.** The feed, output view, diff view.

## Out of scope

- Remote execution over SSH or cloud VMs.
- A mobile app.
- Design mode and an embedded browser.
- Switching between multiple accounts.

ntfy notifications and `--tmux` over SSH cover most remote needs.

## How phases are delivered

- Each UI phase (5–8) starts with text mockups. You approve them before the
  phase is built.
- Each phase ends with a report of what changed and how to try it.
- You test it and ask for the commit.
- The next phase starts only with your OK.
