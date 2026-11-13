<p align="center">
  <img src="doc/assets/banner.svg" alt="DECSR" width="100%">
</p>

<p align="center"><b>Decsr Code</b> — command: <code>decsr</code></p>

<p align="center">
  <a href="https://github.com/Decsr/Decsr-Code/blob/main/LICENSE"><img src="https://img.shields.io/github/license/Decsr/Decsr-Code?style=flat-square&color=4a86ff&labelColor=0b1426" alt="License"></a>
  <a href="https://github.com/Decsr/Decsr-Code/releases"><img src="https://img.shields.io/github/v/release/Decsr/Decsr-Code?style=flat-square&color=4a86ff&labelColor=0b1426" alt="Latest Release"></a>
  <a href="https://github.com/Decsr/Decsr-Code/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/Decsr/Decsr-Code/ci.yml?branch=main&label=CI&style=flat-square&labelColor=0b1426" alt="CI"></a>
  <a href="https://github.com/Decsr/Decsr-Code/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/Decsr/Decsr-Code?style=flat-square&color=4a86ff&labelColor=0b1426" alt="Go version"></a>
  <a href="https://pkg.go.dev/github.com/Decsr/Decsr-Code"><img src="https://img.shields.io/badge/go.dev-reference-4a86ff?style=flat-square&logo=go&logoColor=white&labelColor=0b1426" alt="Go Reference"></a>
</p>

<p align="center">
  <a href="#features">Features</a> ·
  <a href="#install">Install</a> ·
  <a href="#usage">Usage</a> ·
  <a href="#pipeline">Pipeline</a> ·
  <a href="#theme-galaxy--pixel">Theme</a> ·
  <a href="#docs">Docs</a>
</p>

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## What Is It?

Decsr Code (command: `decsr`) is an open-source CLI tool written in Go that drives a team of specialized AI agents through a structured software development pipeline. Instead of chatting with a single LLM, you describe a task in plain text and DECSR runs a full **Context → Spec → Plan → Tests → Implement → Build/Fix → Review → Done** workflow — with human approval gates before any code is written.

It ships with a terminal UI (built with [Bubble Tea](https://github.com/charmbracelet/bubbletea)) and also supports a headless plain-text mode for CI or scripting.

### Why DECSR

Manual back-and-forth with AI coding assistants is slow, error-prone, and hard to reproduce. DECSR:

<table>
  <tr>
    <td width="50%" valign="top">✦ <b>Structures the work</b><br>into well-defined phases so nothing gets skipped.</td>
    <td width="50%" valign="top">✦ <b>Runs multiple specialist agents</b><br>(PM, Coder, QA, UX Reviewer, Security Auditor) — each with its own prompt, skills, and model.</td>
  </tr>
  <tr>
    <td width="50%" valign="top">✦ <b>Iterates automatically</b><br>— if tests fail or reviewers find must-fix issues, the Coder fixes and the entire quality gate restarts.</td>
    <td width="50%" valign="top">✦ <b>Keeps humans in the loop</b><br>— approval gates let you review and revise the task spec and execution plan before any code is written.</td>
  </tr>
</table>

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Features

### 5 Specialized Agents

| Agent | Role |
|-------|------|
| ![PM](https://img.shields.io/badge/PM-4a86ff?style=flat-square&labelColor=0b1426) | Negotiates the task spec, decomposes work into sub-tasks, and arbitrates review feedback |
| ![Coder](https://img.shields.io/badge/Coder-5ce1e6?style=flat-square&labelColor=0b1426) | Writes and fixes code, runs builds and tests |
| ![QA](https://img.shields.io/badge/QA-7cb8ff?style=flat-square&labelColor=0b1426) | Generates tests (TDD), verifies test runs, and reviews code quality, logic, and corner cases |
| ![UX Reviewer](https://img.shields.io/badge/UX%20Reviewer-bfe3ff?style=flat-square&labelColor=0b1426) | UX/UI heuristic review |
| ![Security](https://img.shields.io/badge/Security-ffd166?style=flat-square&labelColor=0b1426) | Security audit and vulnerability scanning |

### Workspace

`decsr` opens one screen: the node graph with a prompt under it. Describe a
task, a lead agent splits it into sub-tasks shown as a node graph, you assign a
role to each node, and assigned nodes run in parallel (3 at a time by default).
A final **Build & test** node checks the result. Nothing runs until you press
`r` or `R`. Roles are plain data (a name, a description and a model), so you can
add your own under `agents:` in `.decsr/project.yaml`.

Each project keeps its own sessions in `.decsr/sessions/`: `decsr resume` (or
`/resume`) continues one, `/clear` starts a new one, `/compact` folds finished
nodes into a summary. `/profile` saves the role settings once in
`~/.decsr/profiles/` and applies them to any project, `/project` switches
project, and markdown files in `.decsr/commands/` and `.decsr/skills/` add your
own `/commands` and role skills. The classic pipeline stays available from the
Home menu (`/config`). See [`doc/workspace.md`](doc/workspace.md).

### Interactive TUI

Terminal user interface built around a **Workflow Cockpit**: a phase
spine with the subtask tree, an Activity-first work pane, and a run inspector for
gates, tokens, tests, agents, and changes. Live token streaming, artifact viewer,
chat-based revision, and project picker are included.

The cockpit removes host CPU/memory/network metrics. It focuses on how the work is
decomposed, which agent owns each subtask, what they are doing, and where the
tokens went. Tokens are shown per agent and per subtask; a trailing `~` marks an
estimated count.

See [`doc/tui.md`](doc/tui.md) for the full reference of panes, tabs, status
indicators, keyboard shortcuts, and token notation.

### Configurable LLM Backends

Supports multiple runners out of the box:

**CLI runners** (drive an external coding-agent CLI):

- **OpenCode** (default)
- **Claude CLI**
- **Codex CLI**
- **GitHub Copilot CLI**

**Local model backends** (talk to a local OpenAI-compatible server):

- **Ollama** (`http://127.0.0.1:11434`)
- **LM Studio** (`http://127.0.0.1:1234`)
- **oMLX** (Apple Silicon, `http://127.0.0.1:8000`)

Each agent can use a different runner and model — configure globally (`~/.decsr/config.yaml`) or per-project (`.decsr/project.yaml`).

### Human Approval Gates

The pipeline pauses for your approval before any code is written:
- **Repository context pack** — the PM reads the repository and writes a compact Markdown briefing (`.decsr/context_pack.md`) with a user-editable section for extra text, notes, and URLs. Review, edit, and approve it before planning starts. See [`doc/context-pack.md`](doc/context-pack.md) for the format and configuration.
- **Task spec** — title, scope, and description negotiated with the PM
- **Execution plan** — the decomposition into sub-tasks (`task_plan.md`), pre-reviewed by Security and QA

You can review, revise via chat, and approve — all from the TUI.

<details>
<summary><b>Layered Configuration</b> — three-layer config with sensible defaults</summary>

<br>

Three-layer config with sensible defaults:

1. **Built-in defaults** — the language is auto-detected from marker files (Go, C/C++ with CMake, C#/.NET, Java, Python, Rust, JS/TS, Ruby, Elixir) and can be overridden with `project.language`
2. **Global user config** (`~/.decsr/config.yaml`) — your preferred models, runners, and reasoning effort
3. **Project config** (`.decsr/project.yaml`) — project-specific overrides (language, test commands, scoping)

Each agent can set a reasoning effort next to its runner and model:

```yaml
agents:
  coder:
    runner: opencode
    model: opencode/gpt-5.3-codex
    effort: high
```

Effort levels depend on the model/provider (for example `low`, `medium`,
`high`, `xhigh`, `max`); `effort: default` leaves the choice to the provider.
The TUI's setup screens show only the levels the selected model supports.

</details>

<details>
<summary><b>Local Model Memory Cap</b> — bound RAM / context for Ollama, LM Studio, oMLX</summary>

<br>

When running local models (Ollama, LM Studio, oMLX), the **Model Memory** screen lets you bound resource use: pick a **RAM limit** (auto-converted to a per-agent context window based on the model and your system RAM) or a fixed **context-token limit** applied verbatim to every agent. This keeps large local models from exhausting memory.

</details>

<details>
<summary><b>Skills System</b> — embedded skill documents injected into agent prompts</summary>

<br>

Agents are augmented with embedded skill documents (Go patterns, testing strategies, security checklists, etc.) that are injected into their system prompts.

</details>

<details>
<summary><b>Project Memory</b> — Markdown files in <code>.decsr/memory/</code></summary>

<br>

DECSR persists everything important about a project as Markdown files in `.decsr/memory/`:

- **`MEMORY.md`** — pinned facts and decisions, prepended verbatim to every agent prompt.
- **`memory/daily/YYYY-MM-DD.md`** — auto-appended event log per pipeline run.
- **`memory/tasks/<task-id>.md`** — per-task summary on completion.

Files are indexed into a local SQLite database (FTS5/BM25) so a new task recalls relevant fragments from past work. Optional embedder backends (OpenAI-compatible, Ollama) enable hybrid semantic search. See [`doc/memory.md`](doc/memory.md) for details.

```bash
decsr memory show
decsr memory search "JWT auth"
decsr memory add "Use sqlc for typed queries"
```

</details>

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Pipeline

<p align="center">
  <img src="doc/assets/pipeline.svg" alt="Pipeline: context, negotiate, decompose, tests, code, build/fix, review, done" width="100%">
</p>

<details>
<summary><b>How the pipeline works</b></summary>

<br>

### Pipeline Algorithm

DECSR runs a deterministic, resumable pipeline:

1. **Collect context** — scans the project (greenfield vs. brownfield, language, toolchain) and recalls relevant fragments from project memory.
2. **Compact context** *(human gate)* — the **PM** reads the repository and writes `.decsr/context_pack.md`: architecture, key files, commands, conventions, risks, and a **user-editable section** for extra text, notes, and URLs. You edit and approve it, then the PM asks what you want to do.
3. **Negotiate** *(human gate)* — the **PM** turns your task request into a structured `TaskSpec`; you approve or re-negotiate via chat.
4. **Decompose** *(human gate)* — the **PM** splits the spec into ordered sub-tasks. The plan is pre-reviewed by **Security** and **QA**, then rendered to `task_plan.md` for your approval. Sub-tasks are tracked as **beads** (durable issues) so the run is resumable.
5. **Implement + Quality Review loop** *(autonomous, capped iterations)*:
   - **Phase A — Tests first (TDD):** **QA** writes tests for *all* sub-tasks before any implementation.
   - **Phase B — Implement:** the **Coder** implements every sub-task in bead order.
   - **Phase C — Global build/test/fix:** one project-wide build → test → fix loop after all sub-tasks are implemented.
   - **Phase D — Quality review:** **QA**, **UX Reviewer**, and **Security** review the result. The **PM** arbitrates all feedback in a single verdict; must-fix issues become new beads and the loop restarts. Nice-to-haves are deferred.
6. **Finalize** — emits a summary and `nice_to_have.md`, updates project memory, closes the beads, and clears resume state.

Any interrupted run can be resumed: DECSR reloads the spec and sub-task plan, re-establishes build/test state, and finishes the remaining open beads without re-negotiating.

See [`doc/context-pack.md`](doc/context-pack.md) for the context pack format, editing flow, and configuration.

</details>

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Install

### Prerequisites

- **Go 1.26+**
- **Git** repository to work in
- A monospace terminal font — [Monaspace Krypton](https://monaspace.githubnext.com) is recommended for the TUI
- At least one LLM backend installed and configured — a CLI runner (**OpenCode**, **Claude CLI**, **Codex CLI**, or **GitHub Copilot CLI**) or a local model server (**Ollama**, **LM Studio**, or **oMLX**)
- **[bd (beads)](https://github.com/gastownhall/beads)** — recommended. Sub-tasks are tracked as durable beads, which makes runs resumable across sessions. Without `bd` the pipeline still runs, but multi-session resume of a bead-backed task is unavailable.

### Option 1 — Prebuilt binaries

Download an archive for Linux, macOS or Windows (amd64/arm64) from the [Releases page](https://github.com/Decsr/Decsr-Code/releases), unpack it and put `decsr` on your `PATH`.

### Option 2 — `go install`

```bash
go install github.com/Decsr/Decsr-Code/cmd/decsr@latest
```

### Option 3 — From source

```bash
# Clone and build
git clone https://github.com/Decsr/Decsr-Code.git
cd Decsr-Code
make build install

# Verify
decsr help
```

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Usage

**Interactive mode** — launch the Workspace: type a task, assign agents to the nodes, run them (`/config` opens the classic pipeline and requirements picker):

```bash
cd /path/to/your/project
decsr            # new session
decsr resume     # pick a saved session of this project
```

Or, from this repository:

```bash
make run
```

**Task from a description** — opens the TUI by default:

```bash
decsr task --description "Add JWT auth to the login endpoint"
```

**Headless mode** — plain-text output for CI or scripting:

```bash
decsr task --description "Add JWT auth to the login endpoint" --ui plain
```

**From a file:**

```bash
decsr task --from-file task.md
```

**On a feature branch:**

```bash
decsr task --description "Add JWT auth to the login endpoint" --branch feat/my-feature
```

**Legacy greenfield workflow:**

```bash
decsr run --requirements requirements.md
```

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Theme: Galaxy × Pixel

A deep-space navy base with starlight text and azure, comet and star-gold accents.

<p align="center">
  <img src="doc/assets/palette.svg" alt="Galaxy × Pixel palette" width="100%">
</p>

**Role legend:** PM azure `#4a86ff` · Coder comet cyan `#5ce1e6` · Coder fixer teal `#3fb8c0` · QA sky `#7cb8ff` · UX frost `#bfe3ff` · Security star gold `#ffd166` · PR ice `#9ee8ff`. Errors use pulsar red `#ff5c7a`, passing tests use aurora mint `#6ff2b4`, and gates use star gold.

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Docs

- [`doc/workspace.md`](doc/workspace.md) — Workspace: node graph, sessions, profiles, commands, roles, keys, and parallel runs
- [`doc/tui.md`](doc/tui.md) — workflow cockpit: panes, tabs, gates, keyboard shortcuts, and token notation
- [`doc/memory.md`](doc/memory.md) — project memory details
- [`doc/context-pack.md`](doc/context-pack.md) — context pack format, editing flow, and configuration

## Development

```bash
make build      # build bin/decsr
make test       # run the test suite
make lint       # vet and lint
```

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

<p align="center">
  <a href="LICENSE">MIT</a> © DECSR<br>
  <sub>✦ built in the terminal ✦</sub>
</p>
