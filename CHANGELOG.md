# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

