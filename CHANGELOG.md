# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- Workspace: the lead is a chat. Every message goes to the lead as one
  conversation and the lead answers in the chat by default; it creates nodes
  only when the message assigns work (or for a job too big to read in one turn,
  as one read-only reviewer node). It reads the project itself with read-only
  tools (claude: `Read`, `Grep`, `Glob` and `git log|diff|status|show`, dontAsk,
  no MCP; codex and dsh in their read-only modes) and never edits files. A reply
  without a node section is a plain answer: no plan card, no error, no second
  call. The lead's CLI conversation is kept per session and resumed on each
  turn (a new one starts, with a chat line, when the lead's agent changes or a
  resume fails); the first message carries a project map (language, instruction
  files, up to 30 `doc/` and `docs/` paths, the 20 newest exports, memory notes)
  instead of file contents. Runners without tools keep the one-shot call.
- Workspace: `/clear` starts a truly fresh session (no chat, graph, summary or
  lead conversation; the old session stays in `/resume`). `/compact [focus]`
  now has the lead summarize the conversation and the nodes, starts a new lead
  conversation from the summary and still folds finished nodes; the chat shows
  `compacted · ≈42K → 3K tokens`.

### Added
- Workspace: MCP in nodes. The lead may mark a sub-task with `"mcp": ["github"]`
  (server names from `claude mcp list`). On the claude runner only that node
  loads your MCP servers and may use only the named servers' tools
  (`mcp__<server>` allow rules); other nodes stay without MCP. The plan card
  shows `uses MCP: …` and always waits for `y`, at every autonomy level; an
  automation run stops with `needs you: T2 uses MCP`. Another runner gets a
  warning on the card and runs without MCP. The lead and `/compact` see `mcp: …`.
- Workspace: install from the chat. Asked to install something, the lead looks it
  up (decsr's catalog, the marketplaces you added, `claude plugin list
  --available`, `claude mcp list`: four read-only `claude` lookups were added to
  its tools) and proposes an `===INSTALL===` action: a decsr plugin, a Claude
  Code plugin (`claude plugin install <plugin>@<marketplace> --scope local`) or
  an MCP server for the claude runner (`claude mcp add --scope local …`; `user`
  scope only when you ask for all projects). decsr shows a card with the exact
  command and the `⚠ untrusted` mark where it applies, and runs it itself only
  after your `y`, at every autonomy level; automation runs skip it and leave an
  inbox item. A marketplace-declared command gets a second card and
  `--accept-command <sha256>`; `-y` is never used. Values marked `<ask>`
  (tokens, headers) are asked in a hidden input and never written to the chat,
  session or logs. Installs are kept in the session (no secrets) and reported
  to the lead.
- Workspace: a muted line under the lead's reply shows its current step
  (`lead · reading internal/runner/claude.go`, `lead · searching "x"`,
  `lead · git log`) and collapses to `read 4 files · 2 searches` when it ends.
- Workspace: `workspace.auto_compact` (on by default, listed in `/config`)
  compacts the lead's conversation before a message you send when its last turn
  used more than 70% of the model's context window, and prints an `auto ›`
  line. It never runs in the background, on a timer or in an unattended run.
- Workspace: `/usage` shows the plan limits like claude's own `/usage`: a meter
  per window (current session 5h, current week, Opus and Sonnet weeks when
  reported) with the share used, a warning past the warning level and the reset
  time, on the Session and Limits tabs; `/status` gets a `Plan limits` line.
  They are read from the `rate_limit_event` lines the claude CLI prints and
  kept in `~/.decsr/plan-limits.json`; decsr asks no account endpoint, starts
  no run to refresh them and does not act on them.
- Workspace: you can chat while nodes run. A task you type goes to the lead at
  once (it says "wait for the lead" only while the lead is still planning or
  reviewing, and asks you to answer an open plan card first). The lead gets the
  live graph: each node's ID, title, status, role, description and the files it
  touched so far. Besides new tasks it may return a `===CHANGES===` section to
  `update` or `cancel` a node that has not started, or send a `note` to a
  running node (it continues with it after its current attempt) or a finished
  one (new attempt, which resumes its conversation). The plan card shows one
  line per change with its reason (`~ T3 changed`, `✗ T4 cancelled`,
  `↻ T2 after it finishes`); `y` applies, `n` drops. Running nodes are never
  stopped, and a new task that touches what a running node touches waits for it
  as a dependency.
- A later attempt of a Workspace node (your reply, a question-card answer,
  allow-and-retry, a retry after a usage limit or a failure) resumes the node's
  own CLI conversation and sends only the new message, so the node remembers
  its work and the repeated context is cached. The session id is read from the
  runner's JSON stream (claude `--resume`, codex `exec resume`, opencode
  `--session`, dsh `--session-id`) and kept on the node. When a conversation
  cannot be resumed the attempt starts fresh with the excerpt of the earlier
  one and the chat says so.
- A node can suggest follow-ups with an optional `next:` of at most 3 lines in
  its report. The run summary lists the follow-ups of the nodes that ended
  (`y plan them · n drop`); `y` sends them to the lead as one task and the plan
  card still asks before anything runs.
- `/plugin` is a manager panel with the tabs Installed, Discover, Marketplaces
  and Stats. It turns plugins on and off, removes them, imports the plugins
  Claude Code has installed (`/plugin import <name>`; only `~/.claude/plugins`
  is read, hooks and MCP servers stay unused), installs from the curated
  catalog and from marketplaces (GitHub repos with `.claude-plugin/marketplace.json`,
  kept in `~/.decsr/marketplaces.yaml`, asking for trust on the first install)
  and shows the nodes, tokens, cost and last use of each plugin from the saved
  sessions. New direct forms: `list`, `search`, `info`, `remove`, `import`,
  `stats` and `marketplace add|remove`. `/plugin available` points to
  `/plugin search`.
- Workspace slash commands are grouped (Session, Run, Agents, Usage, Project,
  Plugin commands), each with forms and an example. `/` lists them under group
  headers; while you type the list ranks the matches, underlines the matched
  letters in the group colour and shows the group at the right.
  `/help` opens a panel of the groups and `/help <command>` shows the forms
  of one command.
- New commands: `/stop [node]`, `/retry <node>`, `/rename <title>` and `/copy`
  (the last answer to the clipboard through OSC 52). `/config` lists, shows
  and changes real settings (`/config set workspace.max_parallel 4`, saved in
  `project.yaml` after validation) and `/menu` opens the classic menu.
  `/provider [list|add|remove|test <name>]` manages saved API keys.
- New forms: `/usage cost`, `/usage budget [amount|off]`, `/model rank`,
  `/model profile [save|apply <name>]` and `/coordinator now`.
- Panels for the slash commands. `/usage` has the tabs Session, Budget,
  History (sessions, tokens and cost of today, 7 and 30 days, a sparkline and
  a table by agent, from the saved sessions) and Limits (the last limit each
  runner printed). `/status` is one panel with the runners and how claude
  logs in, the providers, plugins, run settings, today's usage and the doctor
  warnings. `/model` edits the runner, model and effort of each role and
  saves them in `project.yaml`, and holds the rankings and the profiles.
  `/provider` adds, removes and tests keys, `/config` edits settings.
- New commands `/context` (what the lead and the workers receive, with sizes),
  `/doctor`, `/memory [search <query>|add <text>]` and `/diff` (the session
  diff; in a shared checkout the working tree against `HEAD`).
- Theme: a colour per command group, per runner (claude, deepseek, codex,
  opencode, local) and per role (`ui.RoleColor`, `ui.RunnerColor`). Graph
  cards, the plan card, the sidebar and the status bar use them. Shared
  components for panels, tables, meters, sparklines and result lines
  (`✓` `✗` `⚠` `›`) in `internal/ui`. `NO_COLOR` is respected.
- `project.build_cmd` replaces the detected build command, like `project.test_cmd`
  does for tests. It applies to Workspace Build & test, the classic pipeline
  and the project context.
- C and C++ projects with a `CMakePresets.json` (and optional
  `CMakeUserPresets.json`) build and test through the presets instead of
  `cmake -S . -B build`. When the preset's `CMAKE_MAKE_PROGRAM` sits under a
  `Tools/<name>/` directory that holds `CMake/bin/cmake` (a Qt install), that
  `cmake` and `ctest` are used. Projects without presets are unchanged.

### Changed
- Workspace: the `/` command list is a box of fixed size (as wide as the prompt
  box, always the same height), bare or filtered, with fixed name and args
  columns where long names are cut with `…`; the `@` node list keeps a fixed
  height too.
- Fewer tokens per call. One-shot claude calls (the lead's split, a direct
  answer, the coordinator review) now run with `--tools ""`: before, the
  built-in tool schemas were still sent (about 15K input tokens per call, and a
  chance of stray tool turns). The split prompt cuts role and skill
  descriptions, lists catalog plugins compactly and gets the project context
  without source excerpts, capped at 10K characters. A worker gets no source
  excerpts when it runs as an agent, nothing that its runner loads by itself
  (`CLAUDE.md` for claude, `AGENTS.md` for codex and opencode), at most 4K
  characters of the context pack, 24K characters of dependency files (the
  rest is listed by name) and a 2K-character session summary.
- `/cost`, `/budget`, `/rank`, `/connect`, `/review`, `/profile`, `/approve`
  and `/reject` moved (`/usage cost`, `/usage budget`, `/model rank`,
  `/provider add`, `/coordinator now`, `/model profile`, and `y` / `n` on the
  plan card). Typing an old name prints the new one and runs nothing.
  `/config` no longer opens the classic menu; `/menu` does.
- Workspace: a task runs only what it needs. Each sub-task has a `writes`
  flag (missing means true) and a `writes: false` node runs read-only whatever
  its role. The lead assigns the roles itself and is told to use only the roles
  the task needs, so a review or question becomes one read-only node.
  Build & test is added only when a node writes, and when its turn comes it is
  skipped (`skipped · nothing changed`, done but not passed) unless a node
  changed a file that is not documentation. A plan whose nodes all have
  `writes: false` starts without the plan card. The approval card is now the
  plan card: it also shows each node's role and `writes` or `read-only`, and a
  role the lead invented as `new · uses the pm's agent`, which `y` creates for
  the session only (`/roles save <name>` keeps it in `project.yaml`).
- claude runner on a subscription login: `decsr automation install` prints a
  warning that cites Anthropic's Consumer Terms and usage policy and asks for
  confirmation (`--yes` accepts it; without a terminal it refuses without
  `--yes`), and `automation run` is refused unless the automation sets
  `allow_subscription: true`. A node that hit a usage limit is no longer
  retried by itself on a subscription login (press `r`);
  `workspace.limit_auto_retry` overrides this. `decsr doctor` has a `claude
  auth` line. An API key, Bedrock, Vertex, Foundry or `apiKeyHelper` counts as
  API use and keeps the old behaviour.
- Panels and list overlays (/status, /usage, /help, Go to, Needs you, role and
  session lists) now share one fixed frame, 112 x 32 at most and never larger
  than the screen minus the margins, so the box no longer grows or shrinks with
  its text or when you switch tabs.
- Home and the Workspace share one DECSR logo (cyan, violet, pink, amber
  gradient across the letters), one brand glyph and one set of role and runner
  colours. Home menu items, cards, the prompt box, /status and /config use a
  different colour per group instead of blue.

### Added
- Workspace: a direct answer. When the project context already answers the
  task, the lead replies in the chat (`===ANSWER===`) with no nodes and no
  Build & test.
- Workspace: a `reviewer` built-in role that reads the code and reports,
  never writes.
- Workspace: the full reply of a `writes: false` node is printed in the chat as
  `T1 · reviewer:` followed by the Markdown body; the run summary says
  `answer above`.
- Workspace: `/check` runs Build & test now, whatever changed.
- Workspace: questions with options. A worker's `REPORT` may carry `options:`
  (numbered lines) and `recommended:`; a waiting question shows a card with the
  options, the recommended one first under the cursor, and `Type your own
  answer`. `1`-`9` or `Enter` replies, `Esc` hides the card, `/answer` brings
  it back. A blocked report is said in the chat, with no card.
- Workspace: a summary in the chat when a run ends, built from the graph: the
  nodes that ended with their summary and file count, Build & test, and the
  next steps that apply.
- Workspace: refused-command card for the `claude` runner. The `permission_denials`
  of the result event are kept on the node; a refused `Bash` command offers
  `Allow for T2 and retry`, `Always allow ... in this project and retry`
  (`project.allowed_commands`) or `No`. Nothing is allowed without a key press,
  and read-only roles are never offered it.
- Workspace: approval card for agent routing. After a split one card lists
  each node with its grade, `runner · model · effort`, a marker against the
  role's agent, the estimated cost and the reason. `y` approves and starts the
  runnable nodes, `n` cancels the split, `↑↓` select and `e` edits the agent
  of a node (runner, model, effort, with the balance and cost of each choice
  and a warning, not a block, when a pick costs more than the role's agent).
  `/approve` and `/reject` do the same from the prompt, and `/routing [mode]`
  shows or sets the routing mode for the session and `project.yaml`.
- Workspace: agent routing. `routing.mode` is `manual` (default: nodes use the
  role's agent), `auto-role` (a cheaper model or effort of the role's own
  runner) or `auto-connected` (any connected runner). In the two auto modes
  the lead grades each sub-task small, medium or large with a one-line reason,
  and decsr picks the (runner, model, effort) pair with the best balance score
  that costs no more than the role's own pair per task, keeps at least 30
  intelligence for small and 85% of the role's for medium, and leaves large
  nodes on the role's agent. Every split, in all three modes, waits for the
  approval of the proposed agents before anything starts (`y` approves, `n`
  cancels the split); unattended runs approve by themselves unless
  `routing.auto_approve` is `false`. A node whose picked runner is not
  connected, or that hits a rate limit on its first call, runs on the role's
  agent and records why. The mode is saved in the profile, `project.yaml` and
  the session, and each node keeps its pick.
- Model rankings: `decsr connect artificialanalysis` saves an Artificial
  Analysis key, `decsr models rank [--json]` and `/rank` in the Workspace fetch
  the rankings on demand, save them to `~/.decsr/rankings.json` and show every
  (model, effort) pair with a balance score, the request count and the models
  that map to no runner. Missing efforts are estimated (`~`); without a key a
  built-in relative-tier table is shown. New config: `routing.aliases`,
  `routing.weights`, `routing.fallback`. Data: Artificial Analysis
  (artificialanalysis.ai).
- Workspace: a live view of a running node. A pane under the graph shows the
  running line (runner, model, effort, time, tokens in and out), the last steps
  and the files edited so far; `v` hides it. `Enter` on a node now opens a
  combined screen with the step timeline and the changes (file list and
  highlighted diff, refreshed while the node runs), `Tab` switches panes, `t`
  still shows the raw output. Running cards show the total tokens.

- Setup: DeepSeek Harness (`dsh`) now has a model pick list like Claude and
  OpenCode, in Global settings and the agent editor. `(default)` leaves the
  model blank; the other rows are the ids the DeepSeek API lists. A saved model
  that is not listed stays selected. Without a DeepSeek key or when the list
  cannot be fetched, the model is typed as before, with a hint to run
  `decsr connect deepseek`.

- Config: `project.allowed_commands` lists extra command prefixes (for example
  `/opt/Qt/Tools/CMake/bin/cmake` or `dpkg -l`) that a writing Claude agent-mode
  role may run.

### Changed
- `dsh` is reported as needing a key when `DEEPSEEK_API_KEY` is unset and no key
  is saved by decsr: the runner checks, `decsr doctor` and first-run setup
  point to `decsr connect deepseek` (first-run opens the key step). A key kept
  only by dsh's own credentials is not visible to decsr.
- Home and the setup screens (Global settings, Project setup, Model memory) are
  back to the galaxy layout. The other screens keep the shared look and the
  `ui.theme` setting. Sessions and recent projects are listed on Home, `Enter`
  on a session opens it, `k` connects a provider, and the agent editor keeps
  its Mode field.

### Fixed
- Every row of the panels (`/usage`, `/status`, `/model`, `/provider`, `/config`,
  `/context`, `/doctor`, `/memory`, `/plugin`) is checked to fit the frame at 80
  and 120 columns, by cell width and by terminal width.
- `decsr models rank` and `/rank` print the Artificial Analysis credit only when
  the table shows Artificial Analysis data, not for the built-in fallback tiers.
- Claude agent mode no longer refuses `cmake --preset ...`, a cmake or ctest
  called by absolute path, or chains such as `ls; cat file | head`. The allow
  list now holds the program of each part of the build and test commands plus
  read-only `ls`, `cat`, `head`, `tail`, `wc`, `pwd`, `which`, `file`, `stat`,
  `du` and `tree`, instead of the whole command lines.
- Setup screen: the `dsh` runner (DeepSeek Harness) is now listed, so an agent
  configured with it is no longer shown as OpenCode or rewritten to it on save.
  The model is typed (blank uses dsh's default) and the effort row is shown.
- Workspace: a node the lead marks with `"mcp"` now gets the MCP tools in text
  mode too (the default when a role sets no mode). It had none there. The node
  still has no built-in tools; only the named servers' tools are allowed.

## [0.7.1] - 2026-11-17

### Added
- Native Windows support: commands run through `sh` when available (Git for
  Windows, MSYS2) else `cmd.exe`, and cancelling a command kills its whole
  process tree.

## [0.7.0] - 2026-11-17

### Added
- `dsh` runner for the DeepSeek Harness CLI (headless profile), in text and
  agent mode. `model` is `provider/model`; the key comes from
  `DEEPSEEK_API_KEY` or the key saved with `decsr connect deepseek`.
- Markdown in the Workspace: your and the lead's messages, node summaries,
  report questions and output-view messages render with a style that follows
  the theme. Plain text below 60 columns, while a message streams, or with
  `ui.markdown: false`.
- Syntax-highlighted diffs: the diff view colours code by language, one hunk at
  a time, and keeps the add and remove tint. Files over 2000 changed lines stay
  plain.
- Role option `mcp: true` keeps your own MCP servers for a Claude role.
- Inbox and go-to palette in the Workspace. The sidebar shows `● 3 need you`
  while nodes wait on a question, a block or a usage limit, a lead review waits
  for `y`/`n`, a node failed or stalled for 5 minutes, or a plugin question is
  open; `N` lists them with budget warnings and failed automation runs, `Enter`
  goes to the item and `x` clears an info item (saved in the session). `Ctrl+K`
  opens one search over inbox items, commands, nodes, sessions and projects
  with fuzzy matching. See `doc/workspace.md`.
- First-run setup: when no runner is usable at startup, `decsr` opens a
  three-step screen to pick a runner, connect an API key if needed and see a
  short doctor result before the Workspace. The choice becomes the global
  default of all roles. `Esc` skips it; `decsr setup` opens it any time.
- `ui.theme: auto | dark | light` selects the colour theme. `auto` asks the
  terminal for its background at startup and falls back to dark when it does
  not answer. The Workspace now works on light terminals. `decsr doctor` warns
  about an invalid value.
- Automations: `.decsr/automations.yaml` defines scheduled unattended runs with
  a name, a cron schedule, a task or a GitHub issue query, autonomy 2 or 3, a
  budget and a timeout. `decsr automation list|run|install|uninstall` manage
  them; `install` writes an idempotent crontab line per automation that calls
  `decsr automation run <name> --tmux`, which starts a detached tmux session.
  A run is skipped while the previous one is active (lock file), a run that
  changes nothing only notifies "nothing to do" and opens no pull request, and
  every run appends a line to `.decsr/automations/<name>.log`. `decsr doctor`
  reports the automations. See `doc/automations.md`.
- Usage limits in the Workspace. A node that hits a provider rate limit no
  longer fails: it waits with `◷ waiting · resets 14:30`, read from the
  `Retry-After` and `x-ratelimit-reset-*` headers, the Claude and Codex CLI
  error text or the error body. At autonomy level 2 and 3 it retries 30
  seconds after the reset, up to 3 times, then fails with the limit message;
  at level 1 `r` retries it. A notification is sent either way and the status
  bar shows `⬡ claude · limited until 14:30`.
- Coordinator mode in the Workspace (`workspace.coordinator`, default off;
  `/coordinator on|off` per session). After a batch of nodes finishes the lead
  reviews the graph, with the node reports and a budgeted excerpt of the
  failures, and proposes adding, changing or cancelling nodes. At autonomy
  level 1 a Lead review card waits for `y` or `n`; at level 2 and 3 the valid
  proposals apply at once with `auto › lead:` lines and the new nodes run. At
  most 5 reviews run by themselves per task, then a notification asks you to
  `/review`, which runs one review on demand. Reviews are kept in the session.
- Workspace nodes end with a structured `REPORT` (`status: done | blocked |
  question`, a summary and, for a question, the question). A node that asks a
  question or is blocked ends in the new `◇ waiting` state: its changes are
  merged, its dependents stay blocked, you get a notification (`T3 asks: ...`)
  and a reply (`m`) starts the next attempt with the question in its context. A
  missing report counts as done.
- Per-role `mode: text | agent` in the workspace. In agent mode Claude,
  OpenCode and Codex change files in the node's worktree with their own tools
  and DECSR ignores file blocks; Claude only gets `Read`, `Edit`, `Write`,
  `Glob`, `Grep`, the project's build and test commands and read-only git.
  `opencode` and `codex` default to `agent`, other runners to `text`;
  `decsr doctor` flags a mode the runner cannot run.
- `!` on a workspace node opens a shell in its worktree (a tmux split under
  tmux, otherwise the TUI is suspended).
- Reply to a workspace node with `@T3 <message>` (`@` completes node IDs) or
  `m` on the node. A reply starts a new attempt of the same node: the card
  shows `attempt 2`, earlier attempts are kept (the output view switches with
  `[` and `]`) and the result is merged again. The new attempt gets the
  previous summary, a budgeted excerpt of what the earlier attempt did and your
  message, for every runner. A reply to a running node is queued until the run
  ends; a stop keeps it.
- Workspace nodes keep an activity journal in the session (200 steps per node,
  500 characters of detail each), the estimated prompt size (`ctx`) of worker
  nodes, and fix nodes get a `What failed:` excerpt of up to 8,000 characters
  (failing commands, then errors, then files touched).
- Workspace node cards show the last 5 activity steps while a node runs and
  fold them into `Worked for 1m 12s ›` when it ends (`Space` expands the whole
  timeline); `ctx 18k` / `ctx 18k / 128k` on the card and in the inspector,
  `Activity  42 steps (3 failed)` in the inspector, the current action in the
  sidebar row, and a timeline in the output view (`j`/`k`, `Enter` to expand a
  step, `t` for the raw text).
- A running node with no activity for 60 s shows `no activity for 1m` on its
  card and sidebar row; after 5 minutes one needs-you notification is sent.
- At autonomy level 2 and 3 `/compact` runs by itself when 10 or more nodes are
  done and nothing runs.
- `/config` lists every saved custom provider as `<name> (custom)` plus a
  `+ custom…` entry (name, base URL, key) in the provider row and the per-agent
  runner step, and selecting one writes `runner: openai` with `provider: <name>`.
  `u` edits the base URL of OpenAI, OpenRouter, Groq, DeepSeek (saved as the
  `base_url` override) and of custom providers (saved with the provider), and
  the model list is fetched again. A provider whose model list is empty or
  fails takes a typed model id.
- Connect a provider: `decsr connect <deepseek|openai|openrouter|groq|custom>`
  reads the API key hidden (or from stdin), tests it with `GET /models`, saves it
  in `~/.decsr/credentials.json` (0600, refused when readable by others) and
  can make the provider the lead's or every role's runner. `decsr connect
  --list` and `decsr disconnect <name>` manage saved keys. `/connect` in the
  Workspace and `k` in `/config` do the same with a masked input. `openai`,
  `openrouter` and `groq` are new runner presets next to `deepseek`; the key is
  taken from the environment variable first, then the saved credential, and
  `decsr doctor` shows its source. `provider:` in an agent config picks a saved
  custom provider.
- Autonomy levels in the Workspace: `workspace.autonomy` (1 manual, 2 assisted,
  3 unattended) and `/autonomy` accept roles and run after a split, add up to
  `workspace.max_auto_fixes` fix nodes after a failed Build & test and, at level
  3 (needs a budget and worktree isolation), open and watch the pull request.
  It never merges, and `S` pauses it.
- `/import speckit [--phase N] [path]` adds the open tasks of a Spec Kit
  `tasks.md` to the Workspace graph, with phase and `[P]` dependencies and
  suggested roles.
- Spec Kit write-back: a finished node imported from `tasks.md` ticks its task
  to `[x]`, in the integration worktree (committed, so it reaches `/merge` and
  the pull request) when the file is tracked, else in your checkout. The import
  warns when the feature's spec files are not committed and agents in worktrees
  could not read them, and the empty Workspace detects a `specs/*/tasks.md` with
  open tasks and suggests `/import speckit`.
- Diff view with line notes in the Workspace: `d` opens the diff of a node
  (its commit, or the live worktree while it runs), `D` the diff of the whole
  session. Put a note on a line with `c` and press `S` to send the notes as one
  follow-up node (`path:line — text` with code context). Nodes now record their
  own `commit`, and notes are saved in the session file.
- Detached runs: `decsr --tmux` runs the Workspace inside a tmux session
  (`decsr-<project>-<hash>`) that survives a closed terminal or dropped SSH
  connection; `decsr attach [name|project-path]` reconnects and `decsr ls`
  lists the sessions. `ui.tmux: true` makes plain `decsr` do the same, and the
  Workspace shows the tmux detach keys. Not supported on Windows.
- Money budgets in the Workspace: `pricing:` (USD per million tokens per model
  id or glob; no built-in prices) prices every node, `workspace.budget_usd`
  and `budget_warn_pct` stop new nodes from starting once the known cost
  reaches the budget (running nodes are not killed) and warn once with a
  needs-you notification. `/budget` shows the spending per runner and the
  models without a price; `/budget 10` sets the budget of the session. See
  `doc/workspace.md`.
- A bottom status bar with the tokens per runner, the cost against the budget,
  the session branch and the pull request, and prompt chips for the isolation
  mode, the lead's runner and model and the budget.
- `/issue <number|URL|bead-id> [--force]` starts a Workspace run from a GitHub
  issue (`gh issue view`) or a bead (`bd show`): the issue becomes the lead's
  task, the session remembers it and its pull request gets `Closes #N`
  (`Closes owner/repo#N` for another repository, `Refs <id>` for a bead).
  `decsr issue <ref>` and `decsr --issue <ref>` open the Workspace and run it.
  See `doc/workspace.md`.
- Workspace layout: a left sidebar (from 100 columns; `b` shows it as a box on
  narrow terminals) with the session title, one two-row entry per node (status,
  runner, model, age) and the other saved sessions, and an activity feed as the
  default main pane: the conversation and one card per started node with its
  duration, summary, changed files with `+added −removed` counts, error and
  next keys. `g` switches to the node graph, `Space` expands a card, `G` jumps
  to the newest item, and `Tab` cycles prompt, sidebar and main pane. Nodes
  that ran in a worktree now store `file_stats` (lines added and removed per
  file) in the session. See `doc/workspace.md`.
- `ui.icons` (`unicode`, `ascii` or `nerd`; default `unicode`) picks the runner
  glyphs shown on Workspace node cards and in the inspector. See
  `doc/workspace.md`.
- `/pr` pushes the session branch and creates (or finds) its pull request with
  `gh`: title from the first task, Markdown body with the node table and Build
  & test result. `/pr watch` polls the pull request: failing CI becomes a
  `Fix CI` node with the log tail (once per head commit, up to
  `workspace.max_ci_fixes`), new review comments become one `Address review
  comments` node, and a merged fix node is pushed again. `/pr stop` ends it. New
  `workspace` options: `pr_base`, `pr_remote`, `pr_poll_seconds`,
  `max_ci_fixes`, `pr_auto_fix`. See `doc/workspace.md`.
- "Needs you" notifications. The Workspace alerts you when a split waits for
  roles, a plugin question waits for an answer, a node or Build & test fails,
  Build & test passes, or a run ends. Channels: terminal bell, desktop
  (`notify-send` or `osascript`), cmux and an optional webhook (`ntfy`, `slack`
  or `discord` format). New `notifications` options: `disable_bell`,
  `disable_desktop`, `webhook`, `webhook_format`. The classic pipeline uses the
  same channels. `decsr doctor` reports the active channels. See
  `doc/workspace.md`.
- Workspace nodes can run in isolated git worktrees: each node gets its own
  branch and worktree, finished nodes are merged into a session branch
  `decsr/<session>`, and the new `/merge` command merges that branch into your
  checkout. Set `workspace.isolation` to `worktree` or `shared`; empty picks
  worktrees inside a git repository with a commit. See `doc/workspace.md`.
- CLI runners (`claude`, `codex`, `copilot`, `opencode`) start their process in
  the directory the worker was given (`CompletionRequest.Dir`).
- `openai` runner for any OpenAI-compatible chat completions API and a
  `deepseek` preset (text mode, no tools). Set `base_url` and `api_key_env`
  per agent; the key is read from that environment variable and never stored.
  DeepSeek is also selectable in the setup screen.
- `decsr doctor` checks git, the git repo, `.decsr` write access, config
  layers, runner reachability and API keys (never printed), and optional tools
  (`tmux`, `gh`, terminal). It prints a fix hint per problem and exits 1 when a
  required check fails.
- One-line install: `scripts/install.sh` downloads the latest release (or
  `DECSR_VERSION`), verifies its checksum and installs `decsr` to
  `~/.local/bin` (or `DECSR_INSTALL_DIR`).
- Releases publish a Homebrew cask to `Decsr/homebrew-tap` when the
  `HOMEBREW_TAP_GITHUB_TOKEN` secret is set (skipped otherwise).

### Changed
- Claude worker runs are lean: no MCP servers, only project and local
  settings, and no nonessential traffic. A small worker run sends about 40
  times fewer tokens with a typical global setup.
- Worker, split and review prompts put their fixed text first so provider
  prompt caching hits; the worker task is sent only as the user message.
- Input token counts include cache reads and writes for Claude, OpenCode and
  dsh, so the context meter and budgets no longer undercount.
- A fix node's failure excerpt keeps error, fail, panic and exception lines
  (with 2 lines around each) from the part of a long output that is cut.
- Code highlighting uses `github-dark` or `github` to match the theme.
- The README is rewritten for end users around the Workspace.
- One look for every screen. The shared components in `internal/ui` (header,
  pane label, list row, card, hint bar, input box, overlay) now draw Home, the
  setup screens, the artifact viewer, file preview, editor, chat, the project
  picker, help and the classic pipeline chrome; the old galaxy palette is
  gone. Home is a launcher: recent projects and sessions on the left, actions
  on the right, and `Enter` on a session opens the Workspace on it. Global
  Settings shows Model and Effort side by side and lists unreachable
  providers on an `offline:` line; Project Setup edits an agent's runner,
  model, effort and mode in one editor. Columns keep a space around the
  divider.
- Claude roles without a model now use claude-opus-5-5 with high effort.
- Codex runs with `--sandbox read-only` (text) or `--sandbox workspace-write`
  (agent) instead of `--full-auto`, which current Codex versions reject.
- The `/compact` suggestion at level 1 now appears from 10 done nodes (was 6).
- A fix node's description no longer repeats the failing command's output
  under `Failure output:` when it has a `What failed:` excerpt.
- The token total moved from the Workspace header to the bottom status bar.
- Node cards and sidebar rows show the runner glyph and the effective model
  also when the node's model is the runner's default; the card header reads
  `⬡ model · 17:01 ↗`.
- A calmer Workspace look: no double borders (panes share one divider, rounded
  borders only on node cards, overlays and the prompt), dim title-case labels
  instead of all-caps ones, bold titles with dim metadata, a runner glyph and
  short model on each node card and in the inspector, node cards that use the
  free width, spare graph rows given to the conversation, a one-line header
  with the progress on the right and a one-line failure banner, and a small
  semantic colour set (accent, green, amber, red, dim).
- Workspace nodes now run in their own git worktrees by default in a git
  repository with a commit; set `workspace.isolation: shared` for the old
  behaviour (all nodes in your checkout).

### Fixed
- The session branch no longer starts from the raw `git stash create` commit
  ("WIP on …" and "index on …" commits): uncommitted changes at session start
  are one plain commit on top of HEAD, so `/merge` and pull requests carry no
  stash-shaped history.
- Claude model list includes fable and full model names, accepts a typed model
  id, and shows which model an empty setting uses.

## [0.6.0] - 2026-11-13

### Added
- The lead picks plugin skills per sub-task: it sees the skills of the enabled
  plugins and a built-in catalog of installable plugins, lists only the skills
  each sub-task needs (`skills` in the split) and may ask for plugins that are
  not installed or disabled. Unknown skills and plugins are dropped.
- Nodes keep their skills: they are added to the role's own skills when the
  node runs, shown in the inspector as `(role)`, `(lead)` or `(not installed)`,
  and editable with `K` on a todo or failed node.
- Plugin requests: after a split the Workspace asks, per plugin, whether to
  install (or enable) it (`y`, `n`, `a` skips all). `/plugin available` lists
  the catalog and `/plugin install <name>` installs a catalog plugin after the
  same confirmation. Installs use git (shallow clone into
  `~/.decsr/plugin-src`) and only ever come from the built-in catalog.

## [0.5.0] - 2026-11-13

### Added
- Plugins: Claude-format plugins in `.decsr/plugins/` and `~/.decsr/plugins/`
  add commands (`/<plugin>:<command>`), skills (`<plugin>:<skill>`, usable in a
  role's `skills:`) and agent roles (`<plugin>:<agent>`). `/plugin` lists them
  and enables, disables or reloads one. Frontmatter (`description`,
  `argument-hint`) is also read in `.decsr/commands/*.md`.
- Workspace suggested prompts: the empty prompt shows the likely next step as
  ghost text (fix a failed Build & test, `/compact` or a review when all
  nodes are done, `/resume` in a fresh session). `Right` accepts it, `Esc`
  hides it.
- `scripts/install-plugins.sh` installs a recommended plugin set into
  `~/.decsr/plugins` and writes a `plugins` profile that maps their skills to
  the built-in roles (`--dry-run`, `--uninstall`).

## [0.4.0] - 2026-11-13

### Added
- Workspace sessions: each project keeps resumable sessions in
  `.decsr/sessions/<id>.json` (transcript, summary, graph). `decsr resume` and
  `/resume` pick one, `/clear` starts a new one. A second task is appended to
  the graph. An old `.decsr/workspace.json` is migrated once.
- `/compact` folds finished nodes into the session summary without a model call.
- Profiles: `~/.decsr/profiles/<name>.yaml` stores role settings; `/profile`
  saves, applies and deletes them, and `profile:` is recorded in
  `.decsr/project.yaml`.
- `/project` switches project from inside the Workspace (recent projects or a
  typed path). DECSR changes directory and reopens the Workspace there; the
  starting project and switches are added to the recent projects list.
- Custom commands (`.decsr/commands/<name>.md`, `~/.decsr/commands/`, with
  `$ARGUMENTS`) and role skills (`.decsr/skills/<name>.md`, `~/.decsr/skills/`,
  listed under `skills:` on a role).
- Commands `/status`, `/cost`, `/export` (writes `.decsr/exports/<session id>.md`),
  `/config` (alias of `/menu`) and `/exit`; `/agents` as an alias of `/roles`.

### Changed
- `/compact` keeps a finished node when an unfinished node still depends on it,
  and says how many it kept. Workers get the session summary in their context.
- A follow-up task may depend on nodes already in the graph; the lead is told
  the existing IDs and the next free number, and a reused key no longer points
  at the old node.
- `/` palette: `Enter` runs a command only on an exact name, an arrow/`Tab`
  choice, or a single match; otherwise it lists the matches. A first word with a
  second `/` is a task, and `//text` sends `/text` as a task.
- Applying a profile is refused while nodes run. Saving a profile stores each
  role's own values, not inherited ones.
- Under 100 columns `i` opens the inspector as a box over the graph.
- A paste or burst of input is inserted into the prompt as text, also from
  graph focus; it never sends or runs by itself.
- `/project` keeps spaces in a typed path; a recent project that is a file is
  shown as missing.
- Fix node IDs (`F<n>`) are not reused in a session. Terminal escape sequences
  in lead output, titles, descriptions, session files and custom commands are
  removed; command files with NUL bytes and session files over 8 MB are skipped.
  Placeholders such as `{{TASK}}` inside a role or skill are no longer expanded
  in the worker and split prompts. `split into 1 node` is singular.
- The Workspace is one screen: node graph, inspector, chat strip and prompt,
  with `Tab` switching focus between prompt and graph. In the prompt every
  printable key is text; single-key node actions work in graph focus only.
- `/resume` now lists the project's saved sessions instead of reopening the
  last graph. The `n` key is removed; use `/clear`.
- `decsr resume` opens the Workspace with the session list showing (it used to
  print "not implemented").

## [0.3.0] - 2026-11-13

### Added
- Workspace: a chat-first, node-based screen. A lead agent splits a task into
  nodes, you assign a role to each, and assigned nodes run in parallel
  (`workspace.max_parallel`, default 3). A Build & test node closes the graph,
  and `f` adds a fix node when it fails. State is kept in `.decsr/workspace.json`.
  See `doc/workspace.md`.
- Custom roles: any extra key under `agents:` in `.decsr/project.yaml`, with a
  `description` field. They are listed in Project Setup after the built-ins and
  are kept, with their description and `read_only` flag, when saved there.
- Read-only roles (`read_only: true`): their node reports and never changes
  files. The role form has a "Can change files" toggle; the picker and the
  inspector mark read-only roles.
- Node warnings (`⚠` on the node, a WARNINGS section in the inspector, a count
  in the summary line) for refused or missing writes and for parallel writes to
  the same file.
- The Build & test node is always added after a split. Its commands are
  resolved at split time when known, otherwise when the node runs.
- Agent writes in the Workspace refuse `.git/` and `.decsr/` and enforce
  `project.scope.allow` / `project.scope.deny`.
- Later nodes are shown the current content of the files written by the nodes
  they depend on.
- Home menu item `WORKSPACE` (`w`).
- Language auto-detection from marker files, with build, test and dependency
  commands for Go, C/C++ (CMake), C#/.NET, Java (Maven/Gradle), Python, Rust,
  JavaScript/TypeScript, Ruby and Elixir.
- Without marker files, the language is detected from the most common source
  file extension in the project root and `src/`.
- Project Setup language list starts with `auto`, which clears the saved
  language so it is detected from the project.
- `?` opens a keyboard help overlay on the home screen and in the cockpit.

### Changed
- `decsr` opens the Workspace instead of the Home menu. `/menu` opens the Home
  menu; the old `NEW TASK` item is now `CLASSIC PIPELINE`. `decsr task`,
  `decsr run`, `--ui plain` and `--dry-run` are unchanged.
- The config no longer defaults the project language to Go; the language
  picker lists python, java, csharp, cpp and c first.
- Home project card: one-line rows that never wrap, a collapsed AGENTS row
  when all agents share a runner and model, an always-visible LANG row.
- Cockpit tabs use full labels when wide and mark the active tab with `▸`; the
  footer is a key-hint bar that always ends with `? help`.
- Model Memory is drawn as a panel with mode chips.

### Fixed
- Non-Go projects are no longer asked for a Go module path.
- A user-set `test_cmd` on a non-Go project is no longer prefixed with
  `go build`.

## [0.2.1] - 2026-11-10

### Changed
- TUI run screen rebuilt as a workflow cockpit: workflow spine with subtask
  tree, Activity-first center tabs (Plan, Agents, Tokens, Files, Tests,
  Output), and a run inspector for gates, tokens, tests, agents, changes and
  next action. The CPU/MEM/NET system monitor is removed.
- Tokens are tracked per agent and per subtask; the workflow rail shows
  dependencies, owners and subtask progress.
- TUI theme switched to a deep-space blue Galaxy × Pixel palette that matches
  the README artwork. The old violet palette stays in code as
  `themeGalaxyViolet`.
- The cockpit header is now two lines: task title, phase strip and elapsed
  time on the first, and an agent strip (state, tokens, calls per agent) on the
  second.

### Added
- Per-call token tracking: the Tokens tab shows a total with share bar, per
  agent input/output bars, a tokens-per-call chart, a call log (last 500
  calls), and subtask usage. The inspector and Agents tab gain sparklines and
  per-agent call counts.
- The focused pane title shows a `▌` marker.

### Fixed
- Beads integration works with bd 1.3.1 (issues are created via `bd create`
  and the issue type is decoded), so sub-task tracking, Resume Task and Done
  Tasks work again.
- Regenerate on the task-spec gate now produces a fresh spec instead of a
  cached one.
- `q` in the Ctrl+R requirements picker goes back instead of quitting.
  Quitting decsr stops running agents instead of leaving them orphaned.
- PM chat (Ctrl+C) uses the configured PM runner, model and effort.
- Starting a new task no longer deletes the open `decsr.log` and
  `runlog.jsonl`.
- New Task and Reset Artifacts ask for confirmation when they would delete a
  plan or report, and Reset shows feedback. `r` resumes, `d` opens Done Tasks,
  and Home/End navigate the menu.
- Cockpit: the elapsed clock ticks while waiting, the inspector toggle hides
  the run pane, stage timers are measured correctly, and token counters
  survive pause/resume.
- Cockpit tabs: Files lists only files written in this run and keeps
  extensions visible, Tests labels are shown once, and Activity and Output
  follow the newest entries.
- The inline artifact editor can add lines in long files and warns before
  discarding unsaved edits.
- Global Settings keeps each agent's effort when the model changes, and the
  Model Memory input replaces the prefilled value.
- Build checks no longer leave a compiled binary in the project.
- `decsr clean` keeps project memory and prints a readable summary.
  `decsr report` explains when there is nothing to show and lists the
  pipeline reports. `decsr agent` lists the current roles.
- Smaller fixes: attach picker Home/End/PgUp/PgDn, recent-project removal
  writes `[]`, the module-path prompt handles a go.mod without a module line,
  prompt-delete feedback, the summary counts QA test files, and the research
  report heading is no longer glued to narration.

## [0.2.0] - 2026-10-28

### Added
- Per-agent reasoning effort: `agents.<role>.effort` in global/project config,
  with a model-aware effort picker in Global Settings and Project Setup.
- Setup screens now save each choice on Enter, show a `✓ Saved` indicator, and
  stay open for the next setting. Esc closes as “Done”; Ctrl+S saves and closes.
  Project Setup no longer shows the global provider/model/effort cards.
- Runner plumbing for effort: Claude `--effort`, Codex
  `model_reasoning_effort`, Copilot `--effort`, OpenCode `#variant`, Ollama
  `think`, and LM Studio/oMLX `reasoning_effort`.
- Effort discovery where the provider exposes metadata: OpenCode/Copilot model
  cache, Ollama `/api/show` thinking values, and LM Studio model capabilities.
- Effort is shown in the status bar next to the active runner/model.

### Changed
- TUI chrome refresh: shared pixel header, keycap, chip and panel helpers;
  square borders, a quadrant-block spinner, and emoji-free home/setup screens.

## [0.1.1] - 2026-10-27

### Changed
- CI and release workflows moved to `actions/checkout` v7, `actions/setup-go`
  v7 and `goreleaser/goreleaser-action` v7 (Node 24 runtime).
- README: the retired Go Report Card badge is replaced with a Go Reference
  (pkg.go.dev) badge.

### Fixed
- Release binaries are no longer stamped `+dirty`: GoReleaser's `dist/` output
  directory is now git-ignored.

## [0.1.0] - 2026-10-23

### Added
- **Context-first workflow**: before planning, the PM reads the repository and
  writes a compact, user-editable Markdown context pack
  (`.decsr/context_pack.md`) covering architecture, key files, commands,
  conventions, and risks. The pack includes a preserved `USER-CONTEXT` block for
  extra text, notes, and URLs. After approval the PM asks for the task, then
  planning proceeds with the pack as the primary context.
- New `pm-compact` prompt, `contextpack` marker helpers, and the
  `compacting` pipeline state / `CONTEXT` phase-bar gate.
- `project.context.pack.disable` and `project.context.pack.max_output_tokens`
  configuration, plus `--no-context-pack` on `task` and `run`.
- TUI: `Ctrl+O` reopens a pending approval gate; the negotiation input is
  prefilled with the CLI task description after context approval.
- `doc/context-pack.md` documenting the format, editing flow, and configuration.

### Changed
- **Rebranded to DECSR**: the module path is now `github.com/Decsr/Decsr-Code`,
  the binary and command directory are `decsr` / `cmd/decsr`, the core package
  is `internal/decsr`, and runtime paths use `.decsr/` / `~/.decsr/config.yaml`.
- Runtime names updated: `decsr.log`, `DECSR_LOG_LEVEL`, `DECSR_LOG_FORMAT`,
  and the `decsr-task` / `decsr-subtask` beads labels.
- TUI title, notifications, docs, CI/release names, and license holder updated.
- OpenCode remains the default runner; only project branding and attribution
  references were removed.

## [0.9.0] - 2026-06-09

### Added
- **Phased pipeline with pre-approval plan review**: the PM decomposes a task
  into ordered sub-tasks (beads) and the plan is pre-reviewed by Security and
  QA before the human approval gate (`task_plan.md`).
- **Run timing and stats** in the final summary.
- **Additional LLM backends**: GitHub Copilot CLI, LM Studio, and MLX (Apple
  Silicon) runners join OpenCode, Claude CLI, Codex CLI, and Ollama.

### Changed
- **Docs**: README updated to reflect the current 5-agent roster (PM, Coder,
  QA, UX Reviewer, Security), the full runner list, the actual
  Negotiate → Decompose → Implement (TDD) → Quality Review → Fix pipeline, and
  updated prerequisites (Go 1.26+, Git, an LLM backend, and `bd`/beads for
  resumable runs). Removed the experimental/early-stage notice.

### Removed
- **Response Language selector**: agent responses have always been generated
  exclusively in English, so the non-functional "Response Language" picker was
  removed from the Global Settings and Project Setup screens (and the matching
  `RESP LANG` row from the home panel). The `prompt_language` config key is now
  fully ignored.

## [0.7.0] - 2026-06-05

### Added
- **Claude Opus 4.8**: added `claude-opus-4.8` to the curated GitHub Copilot
  model list (`internal/runner/copilot.go`), available in project and global
  setup.
- **Project Memory**: new `internal/memory` package that persists project
  facts and decisions as Markdown and indexes them in a local SQLite store
  (FTS5/BM25) so new tasks automatically recall relevant past context.
  Documented in `doc/memory.md`.
- **Embedder backends**: new `internal/embedder` package with an
  OpenAI-compatible backend (`openai.go`) and a local backend
  (`cybertron.go`) for optional hybrid semantic memory search.
- **TUI reference docs**: new `doc/tui.md` documenting every panel, status
  indicator, keyboard shortcut, and token notation (`↓` input / `↑` output /
  `~` estimated) with examples.
- **Beads integration**: durable, multi-session task tracking (`.beads/`).

### Changed
- **PM/QA chat-based workflow**: replaced the rigid linear pipeline and the
  standalone `internal/orchestrator` package with a conversational flow. The
  **PM** agent now owns planning, decomposition, negotiation, and arbitration;
  the **QA** agent owns test generation, test verification, and review.
- **Agent roster** is now PM, Coder, QA, Security, and UX Reviewer. The
  Planner, Architect, Reviewer, and standalone Tester agents were removed and
  their responsibilities merged into PM and QA (deleted `architect.go`,
  `planner.go`, `reviewer.go`, and the `internal/orchestrator` package).

### Fixed
- **Go module path prompt**: entering a module path no longer returns to the
  main menu — it now resumes the originally chosen action (e.g. proceeds
  straight into the PM chat for a new task) (`internal/tui/startup.go`).

## [0.5.0] - 2026-05-18

### Added
- **Architect agent**: new pipeline role (`internal/agent/architect.go`,
  prompt `prompts/embedded/architect-system.md`, registered in
  `cmd/orchestrator/main.go`). Default skills: `agentic-engineering`,
  `architecture-decision-records`, `codebase-onboarding`.
- **MLX runner**: local Apple MLX backend (`internal/runner/mlx.go` with
  tests), wired into the runner factory and `IsLocalRunner`.
- **GitHub Copilot runner**: new `copilot` runner
  (`internal/runner/copilot.go` with tests), available via the factory.
- **Beads integration**: new `internal/beads` package for durable task
  tracking, blockers and multi-session handoff.
- **PM decomposition**: new `prompts/embedded/pm-decompose.md` and
  expanded PM agent logic (decompose / negotiate flows) with
  `pm_decompose_test.go` and `pm_negotiate_test.go`.
- **Tester update flow**: new `prompts/embedded/tester-update.md` and
  significant expansion of `internal/agent/tester.go` to update existing
  tests.
- **Coder no-op handling**: detect rewrites that produce no changes and
  skip file writes (`coder_nochanges_test.go`, refactor of `coder.go`).
- **Go declaration chunker**: new `internal/chunker/go_decl.go` for
  chunking by Go top-level declarations.
- **Runner ping**: new `internal/runner/ping.go` health-check utility for
  runners.
- **Ollama improvements**: unwrap JSON envelopes and improve plain-text
  output handling in `internal/runner/ollama.go`.
- **TUI**: substantial rework across `model.go`, `setup.go`, `sysmon.go`,
  `artifact_viewer.go`, `statusbar.go`, `styles.go`, `control.go`,
  `startup.go` and `home.go` (with updated tests).

### Changed
- **Runner factory**: `promptLanguage` argument is now ignored. All
  agents always respond in English regardless of the user's locale. The
  parameter is kept only for signature compatibility; `SkillRunner` no
  longer carries a `promptLanguage` field.
- **Orchestrator**: significant refactor of `internal/orchestrator/orchestrator.go`
  to integrate the new roles.
- **Planner**: updates to `internal/agent/planner.go`, `planner-system.md`
  and `coder-initial.md` prompts.
- **executil**: simplification of `internal/executil/command.go`.
- New `RoleArchitect` in `internal/bus/types.go`; minor updates to
  `artifacts`, `prompts/loader`, `config` and `cmd/orchestrator`.

### Removed
- **QA agent**: removed `internal/agent/qa.go`,
  `prompts/embedded/qa-system.md`, the `qa` entry in default config and
  its registration in `cmd/orchestrator/main.go`.
- **cpulimit package**: removed `internal/cpulimit/` together with all
  `cpulimit.Apply(...)` call sites. The `ReservedCores` project config
  field has been dropped from defaults.
- **omlx_claude runner**: removed `internal/runner/omlx_claude.go`
  (introduced and rolled back within this cycle).

### Breaking Changes
- Per-locale agent responses are no longer supported. Any configuration
  relying on `promptLanguage` to force a non-English response language
  will silently fall back to English.
- The `qa` agent role and its configuration entry have been removed.
  Configurations referencing `agents.qa` should be cleaned up.
- The `project.reserved_cores` configuration option has been removed.

[0.5.0]: https://github.com/Open-Makers/ai-coding-agents-orchestrator/releases/tag/v0.5.0

