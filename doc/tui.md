# TUI Reference

DECSR ships with a full-screen terminal interface built around the **Workflow
Cockpit**. It is designed for one question: *what is the pipeline doing, and
what needs me now?*

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
| `3` | Agents | PM, Coder, QA, UX, and Security with state, current handoff, tokens, last action. |
| `4` | Tokens | Total, input/output, per-agent shares, per-subtask usage, estimates. |
| `5` | Files | Files observed from agent activity (reads, edits, writes, tests). |
| `6` | Tests | Test-first status per subtask and the global verification state. |
| `7` | Output | Raw agent output for the selected/active agent. |

Activity is the default. Raw output is one key away rather than the main view.

## Run inspector

The right pane shows:

- **GATE** — the pending approval, or `none`.
- **TOKENS** — total, per-agent bars, estimate marker.
- **TESTS** — waiting, running, pass, or fail; last test command.
- **AGENTS** — status and current work item for each agent.
- **CHANGES** — observed files and fix rounds.
- **ERROR** — the failure detail when the run stops.
- **NEXT** — the next action, e.g. “Finish T3 → global verification”.

## Tokens

Tokens are shown in the inspector and the Tokens tab:

```text
TOTAL 75.6k   in 62.1k   out 13.5k   ~
Coder   33.7k  ██████████  44%
QA      27.0k  ███████     35%
PM      11.9k  ███         15%
```

- `↓` / `↑` are input and output tokens in the old notation; the cockpit uses
  `in` / `out` for clarity.
- A trailing `~` means the runner estimated the count locally.
- `—` means usage has not been reported for that item.
- Per-subtask counters are scoped to the subtask; agent totals include the
  whole run.

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
list. Project Setup shows only project-level settings; global provider/model/
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
