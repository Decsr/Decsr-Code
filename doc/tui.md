# TUI Reference

DECSR ships with a full-screen terminal interface built around the **Workflow
Cockpit**. It is designed for one question: *what is the pipeline doing, and
what needs me now?*

> **Default screen:** `decsr` with no arguments opens the [Workspace](workspace.md)
> (chat, split into nodes, assign agents). This reference covers the classic
> pipeline cockpit, reached from the Home menu with `/menu` in the Workspace.

The run header is two lines. Line 1 is the task title, an optional pipeline
tag, a 7-segment phase strip, an optional state label (`FAILED`, `GATE`,
`DONE`), the elapsed time, and total tokens. In the strip, `▰` is a finished
or active phase and `▱` is pending: done is cyan, active is azure, a gate turns
the segment gold, and a failure turns it red. Line 2 is the agent strip, one
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
| `?` | Open or close the help overlay (also on the home screen). |
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

## Setup screens

**Global Settings** and **Project Setup** choose each agent's runner, model, and
reasoning effort. The effort list follows the selected model.

**Enter saves the current choice**, shows a green **`✓ Saved`** indicator in the
footer, and advances to the next section. The screen stays open. **Esc** closes
as “Done” (saved choices persist); **Ctrl+S** saves the remaining choices and
closes.

Project Setup edits per-agent overrides in three steps:

1. **Runner** — OpenCode, Claude, Ollama, oMLX, Codex, Copilot, or LM Studio.
2. **Model** — the provider's model list.
3. **Effort** — the levels supported by that model, or provider default.

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
