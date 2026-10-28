# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- TUI run screen rebuilt as a workflow cockpit: workflow spine with subtask
  tree, Activity-first center tabs (Plan, Agents, Tokens, Files, Tests,
  Output), and a run inspector for gates, tokens, tests, agents, changes and
  next action. The CPU/MEM/NET system monitor is removed.
- Tokens are tracked per agent and per subtask; the workflow rail shows
  dependencies, owners and subtask progress.

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

