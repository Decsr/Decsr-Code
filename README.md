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
  <a href="#highlights">Highlights</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#features">Features</a> ·
  <a href="#keys">Keys</a> ·
  <a href="#backends">Backends</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="#classic-pipeline">Classic pipeline</a> ·
  <a href="#docs">Docs</a>
</p>

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## What is decsr?

`decsr` is a terminal app that runs a team of coding agents on your repository. You describe a task, a lead agent splits it into a graph of sub-tasks, and you pick a role and model for each node. Nodes run in parallel, each in its own git worktree. A final **Build & test** check runs, then you review the diff and merge it or open a pull request.

Nothing runs until you say so. You can mix Claude, Codex, OpenCode, Copilot, dsh, API models and local models in one task.

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Highlights

| | |
|---|---|
| **Parallel agents** — every node works in its own git worktree and branch, so agents never overwrite each other. | **Any backend per role** — CLI agents, API providers and local models, each with its own model and effort. |
| **You stay in control** — nothing runs until you press `r`. Autonomy levels 1-3 and a USD budget let you give more freedom step by step. | **Real code review** — read diffs with syntax highlighting, put notes on lines and send them back to an agent. |
| **Coordinator lead** — the lead can review each finished batch and propose new, changed or cancelled nodes. | **Pull requests and CI** — `/pr` opens the PR, `/pr watch` turns failing CI and review comments into fix nodes. |
| **Automations** — scheduled, unattended runs with a budget. | **Limits and inbox** — a rate-limited node waits instead of failing, and the inbox lists everything that needs you. |

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Quick start

<details>
<summary><b>1. Install</b></summary>

<br>

```bash
curl -fsSL https://raw.githubusercontent.com/Decsr/Decsr-Code/main/scripts/install.sh | sh
```

The script installs the latest release to `~/.local/bin` (no sudo) after checking its checksum. Set `DECSR_VERSION=v0.7.1` to pin a release or `DECSR_INSTALL_DIR` to change the directory.

Other options:

- **Homebrew** (once the tap is published): `brew install decsr/tap/decsr`
- **Prebuilt binaries** for Linux, macOS and Windows (amd64/arm64) on the [Releases page](https://github.com/Decsr/Decsr-Code/releases). Unpack and put `decsr` on your `PATH`.
  On Windows, verify commands run through `sh` when Git for Windows or MSYS2 provides one, otherwise through `cmd.exe`. tmux features are unavailable; WSL2 also works.
- **go install** (Go 1.26+): `go install github.com/Decsr/Decsr-Code/cmd/decsr@latest`
- **From source**: see [Build from source](#build-from-source).

You need a git repository to work in and at least one backend (see [Backends](#backends)). [bd (beads)](https://github.com/gastownhall/beads) is recommended for the classic pipeline only.

</details>

1. **Install** with the one-line script above (options are folded in the box).
2. **Check your setup:** `decsr doctor` checks git, config, runners and API keys, plus optional tools such as `tmux` and `gh`. It prints a fix hint for each problem.
3. **First run:** if no runner is usable, `decsr` opens a short setup (pick a runner, connect a key, see the doctor result). `Esc` skips it. Open it again any time with `decsr setup`.
4. **Start:** `cd your-repo && decsr`
5. **Describe a task** in the prompt and press `Enter`. The lead splits it into nodes.
6. **Assign roles:** `A` accepts the lead's suggestions, `a` picks a role for the selected node.
7. **Run:** press `r` for one node or `R` for all assigned nodes. Press `d` on a node to see its diff.
8. **Finish:** type `/merge` to bring the result into your branch, or `/pr` to open a pull request.

```text
 Add JSON output to the vers…  │  Activity · g graph
 2/5 · 1 failed                │  You  17:00
 ✓ T1 Parse the --json flag    │    add a --json flag to the version command
    ⬡ claude:sonnet        2m  │  ╭────────────────────────────────────────╮
 ! T3 Tests for --json         │  │ ✓ T1 Parse the --json flag    17:01 ↗ │
    ⬡ claude:sonnet    failed  │  │ Worked for 1m 10s                      │
 ? T4 Document the flag        │  │ Edited cmd/version.go           +18 −2 │
    → docs                     │  ╰────────────────────────────────────────╯
 Sessions                      │
 ○ Refactor config loader  3/3 │  ╭────────────────────────────────────────╮
                               │  │ Type a task, / for commands            │
                               │  ╰────────────────────────────────────────╯
```

The sidebar (from 100 columns) lists nodes and saved sessions. The feed shows one card per node. The prompt is at the bottom. Press `g` to switch the feed to the node graph.

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Features

### Workspace and graph

The lead splits your task into nodes with titles, dependencies and a suggested role. Up to 3 nodes run at once (`R` runs each node as soon as its dependencies are done). A last **Build & test** node runs your language's build and test commands. In a git repository every node gets its own worktree. Set `workspace.isolation: shared` to work in your checkout instead. See [Workspace](doc/workspace.md#flow).

### Roles and backends

A role is a name, a description, a runner, a model and an effort. Six built-in roles exist (`pm`, `coder`, `coder_fixer`, `qa`, `ux_reviewer`, `security`) and you can add your own under `agents:`. Use `mode: agent` to let a CLI agent edit files with its own tools, or `mode: text` to let decsr write the files. `read_only: true` makes a role report only. See [Roles](doc/workspace.md#roles) and [Agent mode](doc/workspace.md#agent-mode).

### Review and merge

`d` shows a node's diff and `D` the whole session. `c` adds a note to a line and `S` sends your notes to the agent as a new node. `!` opens a shell in the node's worktree. `/merge` merges the session branch. `/pr` pushes it and opens a pull request with `gh`. `/pr watch` turns failing CI and review comments into fix nodes. See [Diff and review notes](doc/workspace.md#diff-and-review-notes) and [Pull requests](doc/workspace.md#pull-requests).

### Autonomy, coordinator and budgets

Level 1 (default) runs nothing by itself. Level 2 accepts roles, runs everything and adds fix nodes after a failed Build & test. Level 3 also opens and watches the pull request, and needs a budget. Automation never merges. Turn on the coordinator and the lead reviews each finished batch. Set a budget and decsr warns at 80% and stops starting nodes at the limit. See [Autonomy levels](doc/workspace.md#autonomy-levels), [Coordinator mode](doc/workspace.md#coordinator-mode) and [Cost and budget](doc/workspace.md#cost-and-budget).

### Reply to nodes and node reports

Type `@T3 your message` (or press `m` on a node) to start a new attempt of that node. Earlier attempts are kept. Every node ends with a report. A node that asks a question or is blocked shows `◇ waiting` until you answer. See [Reply to a node](doc/workspace.md#reply-to-a-node) and [Node reports](doc/workspace.md#node-reports-and-waiting-nodes).

### Automations and notifications

`.decsr/automations.yaml` defines scheduled runs with a task or an issue query, autonomy 2 or 3 and a budget. `decsr automation install` writes the crontab lines. When something needs you, decsr can ring the bell, send a desktop notification or post to an ntfy, Slack or Discord webhook. A node that hits a provider limit waits until the reset and retries (autonomy 2 and 3). See [Automations](doc/automations.md), [Notifications](doc/workspace.md#notifications) and [Usage limits](doc/workspace.md#usage-limits).

### Sessions, profiles and extensions

Each project keeps its sessions in `.decsr/sessions/`. `decsr resume` or `/resume` continues one, `/clear` starts a new one and `/compact` folds finished nodes into a summary. `/profile` saves your role settings for any project. Markdown files in `.decsr/commands/` and `.decsr/skills/` add your own commands and skills. Claude-format plugins add commands, skills and roles (`/plugin`). See [Sessions](doc/workspace.md#sessions), [Profiles](doc/workspace.md#profiles), [Custom commands and skills](doc/workspace.md#custom-commands-and-skills) and [Plugins](doc/workspace.md#plugins).

### Issues and Spec Kit import

`/issue <N|URL|bead-id>` (or `decsr issue <ref>`) sends a GitHub issue or a bead to the lead as the task, and the pull request says `Closes #N`. `/import speckit` adds the open tasks of a Spec Kit `tasks.md` to the graph and ticks them off as nodes finish. See [Start from an issue](doc/workspace.md#start-from-an-issue) and [Import from Spec Kit](doc/workspace.md#import-from-spec-kit).

### Look and feel

Messages in the feed are rendered as Markdown (switch off with `ui.markdown: false`). Diffs are syntax-highlighted. The theme follows your terminal, or set `ui.theme: dark` or `light`. The sidebar shows `● 3 need you`, `N` opens the inbox and `Ctrl+K` opens a palette to search commands, nodes, sessions and projects. See [Themes](doc/tui.md#themes) and [Inbox](doc/workspace.md#inbox).

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Keys

With the prompt focused, every printable key is text. `Tab` moves focus between the prompt, sidebar and main pane. In the sidebar or main pane:

| Key | Action |
|---|---|
| `a` / `A` | Pick a role for the node / accept all suggested roles |
| `r` / `R` | Run the node / run all assigned nodes |
| `s` / `S` | Stop the node / stop all |
| `d` / `D` | Diff of the node / of the whole session |
| `m` | Reply to the node |
| `f` | Add a fix node after a failed Build & test |
| `o` or `Enter` | Open the node's full output |
| `Space` | Expand or collapse a card |
| `!` | Shell in the node's worktree |
| `g` | Switch between Activity and Graph |
| `N` | Inbox: what needs you |
| `Ctrl+K` | Go-to palette (works everywhere) |
| `?` | Help (empty prompt) |
| `q` | Quit |

In the diff view: `c` adds a note, `x` deletes it, `S` sends the notes, `n`/`p` jump between hunks.

| Command | What it does |
|---|---|
| `/merge` | Merge the session branch into your current branch |
| `/pr`, `/pr watch`, `/pr stop` | Open a pull request, watch CI and review comments, stop watching |
| `/issue`, `/import speckit` | Start from an issue / import Spec Kit tasks |
| `/autonomy 1\|2\|3`, `/coordinator on\|off`, `/budget` | Autonomy, coordinator and spending |
| `/approve`, `/reject`, `/routing` | Approve or cancel the agents of a split, show or set the routing mode |
| `/resume`, `/clear`, `/compact`, `/project`, `/profile` | Sessions, projects and profiles |
| `/connect`, `/roles`, `/plugin`, `/status`, `/cost`, `/rank`, `/export` | Providers, roles, plugins, status and reports |
| `/config` | Home launcher (settings and classic pipeline) |

`/help` lists every command. Full key reference: [Workspace keys](doc/workspace.md#keys) and [Commands](doc/workspace.md#commands).

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Backends

| Runner | Kind | Agent mode | Notes |
|---|---|---|---|
| `claude` | CLI | yes | Claude CLI. Worker runs are lean: they skip your global MCP servers and plugins. Set `mcp: true` on a role to keep them. |
| `codex` | CLI | yes | Codex CLI. |
| `opencode` | CLI | yes | OpenCode. The default runner. |
| `copilot` | CLI | no | GitHub Copilot CLI. |
| `dsh` | CLI | yes | DeepSeek Harness CLI (headless profile). Needs `dsh` on `PATH` and `DEEPSEEK_API_KEY` or a key saved with `decsr connect deepseek`. |
| `deepseek` | API | no | Preset. Key: `DEEPSEEK_API_KEY`. |
| `openai` | API | no | Preset. Needs `model`. Key: `OPENAI_API_KEY`. |
| `openrouter` | API | no | Preset. Key: `OPENROUTER_API_KEY`. |
| `groq` | API | no | Preset. Key: `GROQ_API_KEY`. |
| `openai` + `base_url` | API | no | Any OpenAI-compatible endpoint (Together, a gateway). |
| `ollama` | Local | no | `http://127.0.0.1:11434` |
| `lmstudio` | Local | no | `http://127.0.0.1:1234` |
| `mlx` | Local | no | oMLX on Apple Silicon, `http://127.0.0.1:8000` |

API and local runners answer in text mode (plain chat, no tools). Agent mode needs `claude`, `opencode`, `codex` or `dsh`.

Connect an API provider once and decsr keeps the key (also `/connect` in the Workspace):

```bash
decsr connect deepseek              # asks for the key (hidden), tests it, saves it
echo "$KEY" | decsr connect groq    # or pipe the key
decsr connect --list                # saved providers with masked keys
decsr disconnect deepseek           # delete a saved key
```

### Using the claude runner

The `claude` runner starts your own, unmodified `claude` CLI headlessly. It uses whatever login that CLI already has; decsr never reads or stores Claude credentials. Use it the way you use `claude` yourself: in a session you started and are present for. Unattended runs (cron automations) should use an API key (`ANTHROPIC_API_KEY`, or Bedrock, Vertex or Foundry), because Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) (section 3) do not allow automated access through a subscription login, and the advertised Pro, Max and Ultra [usage limits](https://code.claude.com/docs/en/legal-and-compliance) assume ordinary, individual usage. So on a subscription login decsr refuses to run automations that use `claude` unless the automation sets `allow_subscription: true`, asks you to confirm `decsr automation install` (`--yes` skips the question), and does not retry a node by itself after a usage limit (press `r`; `workspace.limit_auto_retry` overrides this). `decsr doctor` shows the login mode in its `claude auth` line.

### Model rankings

`decsr connect artificialanalysis` saves an [Artificial Analysis](https://artificialanalysis.ai) key (it is a data source, not a provider, so it never appears as a runner). `decsr models rank` then fetches the rankings once, saves them to `~/.decsr/rankings.json` and prints every model and effort sorted by a balance score (intelligence 50%, cost per task 30%, time per task 10%, context 10%; change the shares with `routing.weights`). `--json` prints the same as JSON. `/rank` does the same in the Workspace and shows the table in a scrollable view. Nothing is fetched unless you run one of them; the free tier allows 100 requests a day and the output says how many were used. Efforts without a measured row are estimated and marked `~`. With no key, the built-in relative tiers are shown. AA names that match no runner model are listed; add `routing.aliases` (`"AA name": "runner/model"`) to map them, and `routing.fallback` to edit the tiers.

Data: Artificial Analysis (artificialanalysis.ai)

Keys are saved in `~/.decsr/credentials.json` (mode `0600`) and never written to a yaml config. The environment variable wins over a saved key. See [Connect a provider](doc/workspace.md#connect-a-provider).

Each role can use a different runner, model, effort and mode:

```yaml
agents:
  coder:
    runner: claude
    model: sonnet
    effort: high
    mode: agent
    mcp: true            # keep my own MCP servers for this role (claude only)
  qa:
    runner: deepseek
    model: deepseek-v4-flash
  docs:
    runner: openai       # custom OpenAI-compatible endpoint
    base_url: https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
    model: deepseek/deepseek-chat
```

Effort levels depend on the model (for example `low`, `medium`, `high`, `xhigh`, `max`). `effort: default` leaves the choice to the provider.

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Configuration

Three layers, later ones win:

1. Built-in defaults (the project language is detected from marker files).
2. Global user config: `~/.decsr/config.yaml`.
3. Project config: `.decsr/project.yaml`.

```yaml
workspace:
  autonomy: 1          # 1 manual | 2 assisted | 3 unattended
  budget_usd: 5        # stop starting nodes at this spend (needs pricing:)
  coordinator: false   # lead reviews each finished batch
  isolation: ""        # "worktree", "shared" or empty for auto
ui:
  theme: auto          # auto | dark | light
  markdown: true       # false turns Markdown rendering off
  tmux: false          # true makes plain `decsr` run in tmux
notifications:
  webhook: https://ntfy.sh/<your-topic>
  webhook_format: ntfy # ntfy | slack | discord
```

Run `decsr --tmux` to keep decsr alive when the terminal closes. `decsr ls` lists running sessions and `decsr attach` comes back. Details: [Detached runs](doc/workspace.md#detached-runs).

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Classic pipeline

The original pipeline is still available from the Home launcher (`/config` in the Workspace, then Classic pipeline). It runs **Context → Spec → Plan → Tests → Implement → Build/Fix → Review → Done** with human approval gates before any code is written. You can also start it from the command line:

```bash
decsr task --description "Add JWT auth to the login endpoint"                 # TUI
decsr task --description "Add JWT auth to the login endpoint" --ui plain      # headless, for CI
decsr task --from-file task.md
decsr task --description "..." --branch feat/my-feature
decsr run --requirements requirements.md                                      # legacy greenfield
```

<p align="center">
  <img src="doc/assets/pipeline.svg" alt="Pipeline: context, negotiate, decompose, tests, code, build/fix, review, done" width="100%">
</p>

<details>
<summary><b>Approval gates and agents</b></summary>

<br>

The pipeline pauses for your approval before any code is written:

- **Repository context pack** — the PM writes a compact briefing (`.decsr/context_pack.md`) with a section you can edit. See [`doc/context-pack.md`](doc/context-pack.md).
- **Task spec** — title, scope and description negotiated with the PM.
- **Execution plan** — the decomposition into sub-tasks (`task_plan.md`), pre-reviewed by Security and QA.

Agents: **PM** (spec, plan, arbitration), **Coder** (code, builds, tests), **QA** (tests first, code review), **UX Reviewer** (UX/UI review) and **Security** (audit).

</details>

<details>
<summary><b>How the pipeline works</b></summary>

<br>

1. **Collect context** — scans the project and recalls relevant fragments from project memory.
2. **Compact context** *(human gate)* — the PM writes the context pack; you edit and approve it.
3. **Negotiate** *(human gate)* — the PM turns your request into a task spec; you approve or revise it in chat.
4. **Decompose** *(human gate)* — the PM splits the spec into ordered sub-tasks, pre-reviewed by Security and QA, rendered to `task_plan.md`. Sub-tasks are tracked as beads, so the run is resumable.
5. **Implement and review loop** *(autonomous, capped)*:
   - **A. Tests first:** QA writes tests for all sub-tasks.
   - **B. Implement:** the Coder implements each sub-task.
   - **C. Build, test, fix:** one project-wide loop.
   - **D. Quality review:** QA, UX Reviewer and Security review. The PM arbitrates; must-fix issues become new beads and the loop restarts.
6. **Finalize** — writes a summary and `nice_to_have.md`, updates memory and closes the beads.

An interrupted run can be resumed without re-negotiating.

</details>

<details>
<summary><b>Project memory</b></summary>

<br>

Project facts live as Markdown in `.decsr/memory/`: `MEMORY.md` (pinned facts added to every prompt), daily logs and per-task summaries. They are indexed locally for search. See [`doc/memory.md`](doc/memory.md).

```bash
decsr memory show
decsr memory search "JWT auth"
decsr memory add "Use sqlc for typed queries"
```

</details>

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

## Docs

- [`doc/workspace.md`](doc/workspace.md) — Workspace: graph, sessions, profiles, commands, roles, keys, review, PRs
- [`doc/automations.md`](doc/automations.md) — scheduled, unattended runs
- [`doc/tui.md`](doc/tui.md) — classic pipeline cockpit, keys and themes
- [`doc/memory.md`](doc/memory.md) — project memory
- [`doc/context-pack.md`](doc/context-pack.md) — context pack format and configuration
- [`doc/roadmap.md`](doc/roadmap.md) — what was built, phase by phase

## Build from source

You need Go 1.26+, `git` and `make`, plus at least one backend (see [Backends](#backends)).

```bash
git clone https://github.com/Decsr/Decsr-Code.git
cd Decsr-Code
make build          # builds ./bin/decsr
```

Run it from the repository you want agents to work on:

```bash
cd /path/to/your-repo
/path/to/Decsr-Code/bin/decsr
```

Or install it so `decsr` works from anywhere:

```bash
make install                              # installs to $(go env GOPATH)/bin, usually ~/go/bin
export PATH="$PATH:$(go env GOPATH)/bin"  # if that directory is not on PATH yet
decsr doctor                              # check git, config, runners and keys
decsr
```

To update, pull and install again. Pulling alone does not change the installed binary:

```bash
git pull && make install
```

## Development

```bash
make build      # build bin/decsr
make test       # run the test suite
make lint       # vet and lint
```

`make demo` records a terminal demo with [vhs](https://github.com/charmbracelet/vhs) from [`doc/demo.tape`](doc/demo.tape).

<p align="center"><img src="doc/assets/divider.svg" width="100%" alt=""></p>

<p align="center">
  <a href="LICENSE">MIT</a> © DECSR<br>
  <sub>✦ built in the terminal ✦</sub>
</p>
