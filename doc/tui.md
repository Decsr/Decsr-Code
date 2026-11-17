# TUI Reference

DECSR ships with a full-screen terminal interface built around the **Workflow
Cockpit**. It is designed for one question: *what is the pipeline doing, and
what needs me now?*

> **Default screen:** `decsr` with no arguments opens the [Workspace](workspace.md)
> (prompt, node graph, assign agents; saved sessions per project, `decsr resume`).
> This reference covers the classic pipeline cockpit, reached from the Home
> launcher with `/config` (or `/menu`) in the Workspace.

The run header is two lines. Line 1 is the task title, an optional pipeline
tag, a 7-segment phase strip, an optional state label (`FAILED`, `GATE`,
`DONE`), the elapsed time, and total tokens. In the strip, `▰` is a finished
or active phase and `▱` is pending: done is green, active is the accent colour, a gate turns
the segment amber, and a failure turns it red. Line 2 is the agent strip, one
chip per agent: a glyph (a spinner for the running agent, `✓` done, `○`
waiting, `!` error), the short label, its tokens (`—` if none), and `×calls`.
Below 110 columns the strip uses a compact form.

A **call** is one token-usage report from an agent. The Coder may report a
whole subtask in a single call.

The run screen has three panes:

1. **Workflow** — the phase spine and the subtask tree.
2. **Center** — the selected work item: activity, plan, agents, tokens, files,
   tests, or raw output.
3. **Run inspector** — gate, tokens, tests, agent state, changes, and the next
   action.

The old CPU/MEM/NET system monitor has been removed. No host metrics are shown.

> **Recommended font:** DECSR cannot set its own font; it uses your terminal's.
> [Monaspace Krypton](https://monaspace.githubnext.com) is a good fit.

---

## Workflow spine

The left pane is the run's navigation:

```text
✓ Context     1m
✓ Spec        4m
✓ Plan        2m
▸ Build       18m
  ✓ T1 Add session model       coder · 4.2k
  ✓ T2 Wire login handler      coder · 6.1k
  ▸ T3 Add auth middleware     coder · 3.1k · dep T2
  ○ T4 Integration tests       qa      · dep T2
  ○ T5 Update docs             coder   · dep T1,T3
○ Verify
○ Review
  QA · UX · Security
○ Done
```

- `✓` done, `▸` active, `○` pending, `!` failed, `⏸` gate.
- Subtasks show owner, token usage, and dependencies.
- The active phase shows its current detail under its label.
- `j`/`k` or `↑`/`↓` moves the selection. `Enter` moves focus to the center.
- Long runs auto-scroll the rail to keep the selected item visible.

## Center tabs

| Key | Tab | Shows |
|---|---|---|
| `1` | Activity | Structured agent steps: read, edit, command, test, write, errors. |
| `2` | Plan | The full subtask table: status, owner, tokens, dependencies. |
| `3` | Agents | PM, Coder, QA, UX, and Security with state, current handoff, tokens, last action, `calls N · last call X · avg Y`, and a per-agent sparkline. |
| `4` | Tokens | Total, share bar, per-agent bars, tokens-per-call chart, call log, subtask usage. |
| `5` | Files | Files observed from agent activity (reads, edits, writes, tests). |
| `6` | Tests | Test-first status per subtask and the global verification state. |
| `7` | Output | Raw agent output for the selected/active agent. |

Activity is the default. Raw output is one key away rather than the main view.
The tab row shows full labels (`1 Activity  2 Plan …`) when there is room and
compacts to `1 Act 2 Plan …`, then `1Act 2Plan …`, on narrow panes. The active
tab is highlighted and carries a `▸` marker, so it is visible without color.
The focused pane's title shows a `▌` marker.

## Run inspector

The right pane shows:

- **GATE** — the pending approval, or `none`.
- **TOKENS** — total, share bar, a sparkline of recent calls, and per-agent rows.
- **TESTS** — waiting, running, pass, or fail; last test command.
- **AGENTS** — status, current work item and `×calls` for each agent. The running agent is highlighted in its color with a spinner.
- **CHANGES** — observed files and fix rounds.
- **ERROR** — the failure detail when the run stops.
- **NEXT** — the next action, e.g. “Finish T3 → global verification”.

## Tokens

Tokens are shown in the header, the inspector, and the Tokens tab. The Tokens
tab has these sections, top to bottom:

1. **TOTAL** — total tokens (with `~` right after the number when estimated),
   input, output, and the call count.
2. **Share bar and legend** — each agent's share of the total.
3. **PER AGENT** — input (`█`) and output (`▓`) bars per agent.
4. **TOKENS PER CALL** — a column chart with one column per call. Phase letters
   mark where each phase starts (`C` context, `S` spec, `P` plan, `B` build,
   `V` verify, `R` review, `D` done). `+N earlier calls` shows how many calls
   are scrolled out of the chart.
5. **SUBTASK USAGE** — tokens per subtask.
6. **CALL LOG** — newest first (last section): `#n`, time, agent, phase,
   subtask, in, out, total. `#n` counts every call in the run, even after the
   log is capped.

```text
TOTAL 65.3k   in 53.0k   out 12.3k   10 calls

██████████████████████████████████████████████████████████████████████
■ PM 10%  ■ Coder 69%  ■ QA 20%

PER AGENT
PM           7.0k    ×2 █████▓▓
Coder       45.2k    ×6 █████████████████████████████████████▓▓▓▓▓▓▓▓▓
QA          13.1k    ×2 ███████████▓▓
UX              0    ×0
Security        0    ×0

TOKENS PER CALL
          ▁ █
        ▃ █ █
      ▄ █ █ █   ▄
    ▅ █ █ █ █   █ ▄
    █ █ █ █ █   █ █
█ ▃ █ █ █ █ █ ▇ █ █
█ █ █ █ █ █ █ █ █ █
█ █ █ █ █ █ █ █ █ █
──────────────────────────────────────────────────────────────────────
S P B         V R

SUBTASK USAGE
  T1   10.2k  Add session model
  T2   18.0k  Wire login handler

CALL LOG
#10  09:59:26  QA       review  —      5.0k    900   5.9k
#9   09:59:26  QA       review  —      6.0k   1.2k   7.2k
#8   09:59:26  Coder    verify  —      3.0k    700   3.7k
#7   09:59:26  Coder    build   T5     8.5k   2.0k  10.5k
```

The share bar is a single run of `█`; agents differ by color only, and the
legend uses the same colors. In the per-call chart each column is colored by
the agent that made the call.


- A trailing `~` means the runner estimated the count locally.
- `—` means usage has not been reported for that item. With no data at all,
  `no usage reported yet` replaces the share bar, charts and log; TOTAL and
  SUBTASK USAGE still render.
- Per-subtask counters are scoped to the subtask; agent totals include the
  whole run.
- The session log keeps the last 500 calls. Totals are lifetime and are not
  affected by the cap.

## Workspace look

The Workspace uses a calmer style than the cockpit: one accent colour (blue),
green for done and local runners, amber for what needs you (unassigned,
warnings), red for failures and dim text for metadata. Status is always a glyph
and a word. Panes share one faint divider; only node cards, overlays and the
prompt have rounded borders. Runner glyphs follow the `ui.icons` setting
(`unicode`, `ascii`, `nerd`); see [workspace.md](workspace.md#runner-glyphs).

From 100 columns the Workspace has a sidebar with the nodes and saved sessions
beside the main pane, which shows an activity feed by default (`g` switches to
the node graph, `b` toggles the sidebar); see [workspace.md](workspace.md#layout).

## Themes

`ui.theme` picks the colours: `auto` (default), `dark` or `light`. `auto` asks
the terminal for its background once at startup, before the TUI starts, and
finishes the query before the first key is read. It does not ask, and uses dark,
when stdin or stdout is not a terminal, `$TERM` is `dumb`, or decsr runs inside
tmux or screen (they cannot answer for the outer terminal); set `ui.theme:
light` there to get the light theme. A terminal that does not answer also gives
dark. An invalid value counts as `auto`, and `decsr doctor` warns about it. All
screens share the colour tokens and components in `internal/ui` (header, pane
label, card, list row, hint bar, input box, overlay and status glyphs).

## Keyboard

| Key | Action |
|---|---|
| `Tab` / `Shift+Tab` | Cycle focus: Workflow → Center → Inspector. |
| `↑`/`↓` or `j`/`k` | Move in the focused pane. |
| `1`–`7` | Switch center tab. |
| `p` / `a` / `x` / `f` / `t` / `o` | Plan / Agents / Tokens / Files / Tests / Output. |
| `i` or `Ctrl+T` | Toggle the inspector. |
| `Enter` | Focus the center pane / inspect the selected item. |
| `Ctrl+A` | Approve the current human gate. |
| `Ctrl+O` | Reopen the pending gate artifact. |
| `Ctrl+G` | Open the git diff view. |
| `Ctrl+C` | Open the PM chat. |
| `Ctrl+X` | Cancel the run. |
| `Ctrl+P` | Pause to change the model. |
| `Ctrl+E` | Resume the build-fix loop after a failure. |
| `?` | Open or close the help overlay (also on the Home launcher). |
| `q` | Quit. |
| `Esc` | Cancel / return; when the run is done, return to the menu. |

## Gates

When the pipeline needs approval, the inspector shows the artifact under
**GATE** and the workflow rail marks the phase with `⏸`. `Enter` or `Ctrl+O`
opens the artifact viewer; `Ctrl+A` approves; the viewer supports edit/regenerate
where applicable.

The context pack remains the first gate, followed by the task spec and the task
plan.

## Compact layout (80×24)

At 80 columns the inspector collapses. The workflow rail and center stay
visible, the footer shows a compact token/test summary, and the same keys work.
At 60×12 or smaller the cockpit asks for a larger terminal.

## Languages

DECSR detects the project language from marker files in the project root, in
this order (first match wins), unless `project.language` is set. Build, test
and dependency commands follow the language:

| Language | Marker files | Build | Test |
|---|---|---|---|
| `go` | `go.mod` | `go build -o <null device> ./...` | `go test -count=1 ./...` |
| `rust` | `Cargo.toml` | `cargo build` | `cargo test` |
| `cpp` / `c` | `CMakeLists.txt` | `cmake -S . -B build && cmake --build build` | `ctest --test-dir build --output-on-failure` |
| `csharp` | `*.sln`, `*.csproj` | `dotnet build --nologo` | `dotnet test --nologo` |
| `java` | `pom.xml`, `build.gradle`, `build.gradle.kts` | `mvn -q compile` / `gradle compileJava -q` | `mvn -q test` / `gradle test` |
| `python` | `pyproject.toml`, `requirements.txt`, `setup.py` | none | `python -m pytest` when pytest is installed, otherwise `python -m unittest` (`python3` when `python` is not on PATH) |
| `ruby` | `Gemfile` | none | none |
| `elixir` | `mix.exs` | none | none |
| `typescript` / `javascript` | `package.json` (`tsconfig.json` selects TypeScript) | `npm run build --if-present` | `npm test` |

`package.json` is checked last because compiled-language repositories often
carry one for tooling.

When no marker file matches, DECSR counts the source files directly in the
project root and in `src/` (non-recursive) by extension and picks the most
common language: `.go`, `.py`, `.cs`, `.java`, `.cpp`/`.cc`/`.cxx`, `.c`, `.rs`,
`.ts`, `.js`, `.rb`. Ties go to the first language in that list. An empty
directory has no language until one is chosen.

In Project Setup the language list starts with `auto`, which shows what is
detected (for example `auto (cpp)`). Choosing `auto` saves no language, so
detection stays in effect.

## Home launcher

`/config` (or `/menu`) in the Workspace opens Home, in the galaxy look: the
DECSR logo and the pipeline stages on top, then the project card (name,
language, branch, default runner and the agents) on the left and the action
card on the right. The actions are Workspace (`w`), classic pipeline (`1`),
open a project (`o`), Global settings (`g`), connect a provider (`k`), Model
memory (`m`), Project setup (`s`), reset artifacts (`c`) and quit (`q`).

When the project has saved sessions or there are recent projects, they are
listed under the project card. Use the arrow keys to move and `Tab` to switch
between the actions and these lists. `Enter` on a project switches to it, and
`Enter` on a session opens the Workspace on that session. `r` resumes the
interrupted task, `?` opens help. Below 86 columns the cards stack.

## First-run setup

`decsr` opens a three-step setup before the Workspace when no runner is usable
(the same rule as `decsr doctor`'s required backend check). It never opens
inside an automation. `decsr setup` opens it any time.

1. Pick a runner. Each row shows what was found: a CLI on PATH, an API key from
   the environment or saved, a local server running. A CLI that is not installed
   shows an install hint and cannot be chosen.
2. An API runner without a key asks for it with the connect card, tests it and
   saves it. Other runners skip this step.
3. The choice is saved as the global default of all roles in
   `~/.decsr/config.yaml`, replacing the models set for another runner, and a
   compact doctor result is shown. `Enter` opens the Workspace.

`Esc` skips the setup for this run; on the key step it goes back.

## Setup screens

**Global Settings** chooses the provider, model and reasoning effort for all
agents, in three cards: Default Provider (a row of tabs, `←→`; providers that
cannot be reached are struck through and marked `(offline)`), Default Model and
Effort. A model list shows seven rows and scrolls with `↑ N more` / `↓ N more`
markers. The effort list follows the selected model. `Tab` moves between the
cards, `[` and `]` scroll a wide screen sideways.

**Project Setup** has the project name, the Agent Overrides list and the
programming language, one card each. `Enter` on an agent opens an editor in the
card with Runner, Model, Effort and Mode (text or agent) for that agent: `↑↓`
moves between the fields and `←→` changes the focused one.

**Enter saves the current choice**, shows a green **`✓ Saved`** indicator in the
footer, and advances to the next section. The screen stays open. **Esc** closes
as “Done” (saved choices persist); **Ctrl+S** saves the remaining choices and
closes.

The agent editor has four fields:

1. **Runner** — OpenCode, Claude, Ollama, oMLX, Codex, Copilot, LM Studio,
   DeepSeek, OpenAI, OpenRouter, Groq, DeepSeek Harness, each saved custom
   provider (shown as `<name> (custom)`), or `+ custom…` to add one.
2. **Model** — the provider's model list. An API provider whose `/models` list
   is empty or fails, and DeepSeek Harness, show a text input instead: type the
   model id (blank uses dsh's default).
3. **Effort** — the levels supported by that model, or provider default.
4. **Mode** — the runner default, `text` or `agent`.

**Claude models.** The model list (Global Settings and the per-agent model step)
shows the aliases `fable`, `opus`, `sonnet` and `haiku` (each with a dim
`latest …` hint), then the full names `claude-fable-5-2`, `claude-opus-5-5`,
`claude-sonnet-5-5` and `claude-haiku-5-5`. The saved string is passed unchanged
as `claude --model`. The last row, `type a model id…`, opens a text input: Enter
saves the id (no spaces), Esc returns to the list. A saved id that is not listed
shows as `<id> (custom)` and stays selected. When a role has no model, DECSR
passes `--model claude-opus-5-5` and, when its effort is empty or `default`,
`--effort high`; an explicit model or effort is passed unchanged. The first line
of the section then reads `default → claude-opus-5-5 · high effort`, the default
row carries a dim `default` hint, the effort list labels its default entry
`default (high)`, and an unset model starts the cursor on `claude-opus-5-5`. The
same line shows in the per-agent model step of a role with no model.

The provider row of Global Settings offers the same runners. A custom provider
is stored as `runner: openai` plus `provider: <name>` and uses the base URL
saved with it; switching to another runner removes `provider`. If the config
names a custom provider that is no longer saved, it shows as `<name> (missing)`
with the hint `decsr connect custom`.

API providers have two more keys, on the provider row and in the per-agent
runner step (in the model step `u` also works while a list is shown; with the
typed model input use the provider row, because letters go to the input):

| Key | Action |
|---|---|
| `k` | Add or replace the API key in a masked input, tested with `GET /models`. On `+ custom…` it asks for a name, base URL and key. The key is saved in `~/.decsr/credentials.json` (`0600`) and is never shown. |
| `u` | Edit the base URL. https is required; http only for `localhost` and `127.0.0.1`. Enter saves, Esc cancels. For OpenAI, OpenRouter, Groq and DeepSeek the URL is the `base_url` override of the agent (of every agent in Global Settings) in `config.yaml` or `project.yaml`, written with the next Enter or Ctrl+S; an empty input removes the override. For a custom provider the URL is saved at once in `credentials.json` with the key unchanged, and cannot be empty. |

The effective URL is shown under the API key line, with `(override)` when a
config override is set. The model list is fetched again after a URL change.

Saving the effort step writes that agent's override and returns to the agent
list. Custom Workspace roles (see [workspace.md](workspace.md)) are listed after
the built-in agents and keep their description when saved. Project Setup shows only project-level settings; global provider/model/
effort are configured from **Global Settings** (`g`).

The chosen effort is stored as `agents.<role>.effort` in
`~/.decsr/config.yaml` or `.decsr/project.yaml` and is passed to the provider:

| Provider | Effort path |
|---|---|
| Claude | `claude --effort <level>` |
| Codex | `codex exec -c model_reasoning_effort="<level>"` |
| Copilot | `copilot --effort <level>` |
| OpenCode | `opencode run -m provider/model#<level>` (cloud models only) |
| Ollama | `/api/chat` → `"think": "<level>"` |
| LM Studio / oMLX | `/v1/chat/completions` → `"reasoning_effort": "<level>"` |
| DeepSeek / OpenAI / OpenRouter / Groq / custom providers | `/v1/chat/completions` → `"reasoning_effort": "<level>"` when `effort` is set in the config; the setup screen lists no levels for them |
