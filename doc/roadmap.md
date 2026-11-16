# Roadmap

`decsr` stays a terminal-first orchestrator. These phases borrow workflow
ideas from the desktop app Orca (stablyai/orca) without its desktop or mobile
surface. Phases are built one at a time, in order. Each one is tested and
committed before the next starts.

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

- [ ] A role can be set to `mode: agent` or `mode: text`, with the defaults
      above.
- [ ] In agent mode the node result is the worktree diff.
- [ ] Claude agent mode refuses any tool outside the fixed list.
- [ ] The shell command opens a split under `--tmux` and suspends the TUI
      otherwise.
- [ ] `@T3 …` and `m` start `attempt 2` of T3, with history kept.
- [ ] A reply to a running node waits until the run ends.

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

- [ ] Reports are parsed, and a missing report becomes `done`.
- [ ] The lead reviews once per finished batch.
- [ ] At L1 a proposal card waits for `y`/`n`; at L2/L3 changes apply with
      `auto ›` lines.
- [ ] A `question` report stops the node until you reply.
- [ ] The sixth review round does not start without your answer.

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

- [ ] `decsr automation install` writes the crontab line, and
      `decsr automation run <name> --tmux` starts a detached run.
- [ ] An automation without L2/L3 or without a budget is refused.
- [ ] An overlapping run is skipped, and an empty run notifies without a PR.
- [ ] A rate-limited node shows the reset time and retries at L2/L3, up to 3
      times.

**Builds on.** `--tmux`, autonomy levels, budgets, notifications, `/pr` and
`/pr watch`, starting from an issue, runners.

## Out of scope

- Remote execution over SSH or cloud VMs.
- A mobile app.
- Design mode and an embedded browser.
- Switching between multiple accounts.

ntfy notifications and `--tmux` over SSH cover most remote needs.

## How phases are delivered

- Each phase ends with a report of what changed and how to try it.
- You test it and ask for the commit.
- The next phase starts only with your OK.
