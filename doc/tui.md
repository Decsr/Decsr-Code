# TUI Reference

DECSR ships with a full-screen terminal user interface (TUI). This
document explains every panel and indicator you will see, with concrete
examples.

The screen is split into three areas:

1. **Agent panels** — one panel per active agent, showing live streaming output.
2. **Status bar** — a single line at the bottom with run state, stage progress,
   runner/model, elapsed time, and keyboard shortcuts.
3. **System Monitor** — an optional side panel (toggle with `Ctrl+T`) showing
   CPU, memory, network, and token usage.

> **Recommended font:** DECSR cannot set its own font; it uses your terminal's.
> [Monaspace Krypton](https://monaspace.githubnext.com) (free, OFL-licensed) is
> a good fit: a clean, highly legible monospace face.

---

## Setup screens

**Global Settings** and **Project Setup** choose each agent's runner, model, and
reasoning effort. The effort list follows the selected model: it shows only the
levels that model/provider exposes, plus **`(provider default)`**. Selecting the
default leaves the decision to the provider.

**Enter saves the current choice**, shows a green **`✓ Saved`** indicator in the
footer, and advances to the next section. The screen stays open so you can
continue configuring. **Esc** closes as “Done” (already-saved choices persist);
**Ctrl+S** saves the remaining choices and closes.

Project Setup edits per-agent overrides in three steps:

1. **Runner** — OpenCode, Claude, Ollama, oMLX, Codex, Copilot, or LM Studio.
2. **Model** — the provider's model list.
3. **Effort** — the levels supported by that model, or provider default.

Saving the effort step writes that agent's override and returns to the agent
list so you can configure the next agent. Project Setup shows only project-level
settings; global provider/model/effort are configured from **Global Settings**
(`g`).

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

OpenCode and Copilot read their effort choices from the OpenCode model cache;
Ollama reads `thinking.values` from `/api/show`; LM Studio reads model
capabilities. Providers without machine-readable metadata use a conservative
static list. An effort the selected model does not offer is shown as a warning
in the setup screen and is not applied.

---

## Agent panels

Each agent renders a panel with a header and a scrolling body of streamed
output. The header shows the agent role and its current status:

```
┌─ ■ CODER ───────── ▘ RUNNING ─┐
```

| Indicator     | Meaning                                                        |
|---------------|----------------------------------------------------------------|
| `○ WAITING`   | Agent is queued and has not started yet.                       |
| `▘ RUNNING`   | Agent is actively producing output (animated spinner).         |
| `▘ FIXING`    | Agent is re-running to fix issues found by a quality gate.     |
| `✓ DONE`      | Agent finished successfully.                                    |
| `✗ ERROR`     | Agent failed (e.g. build error, runner crash, quota/rate limit).|
| `⏸ GATE`      | Agent output is waiting for your approval before continuing.   |

The body streams tokens from the model as they arrive. When a panel has more
output than fits on screen, a scroll indicator appears; use `↑`/`↓` to scroll.

---

## Status bar

The bottom line is laid out as:

```
main  ● coder   ▸ Stage 2/5: Must Have — Auth        copilot/claude-opus-4.8 · high  ◷ 3m 12s   ↑↓ scroll  Ctrl+R req  …  v0.9.0
```

Reading left to right:

| Segment                         | Example                                  | Meaning                                                                 |
|---------------------------------|------------------------------------------|-------------------------------------------------------------------------|
| **Git branch**                  | `main`                                   | The current branch of the project.                                      |
| **State**                       | `● coder`, `⏸ … — waiting for approval`  | What the pipeline is doing right now (running agent, gate, done, error).|
| **Stage info**                  | `▸ Stage 2/5: Must Have — Auth`          | Current implementation stage; scrolls as a marquee if it is too long.   |
| **Runner / model**              | `copilot/claude-opus-4.8 · high`         | The active runner, model, and effort for the running agent.              |
| **Elapsed timer**               | `◷ 3m 12s`                               | Time since coding started (appears once the Coder begins).              |
| **Keyboard shortcuts**          | `↑↓ scroll  Ctrl+R req  …`               | Available actions (see below).                                          |
| **Version**                     | `v0.9.0`                                 | DECSR version.                                               |

### Keyboard shortcuts

| Key        | Action                                                       |
|------------|--------------------------------------------------------------|
| `↑` / `↓`  | Scroll the focused panel.                                     |
| `Ctrl+R`   | Open the requirements file.                                   |
| `Ctrl+G`   | Open the git diff view.                                       |
| `Ctrl+C`   | Open the PM chat / requirements conversation.                 |
| `Ctrl+T`   | Toggle the System Monitor panel.                              |
| `Ctrl+A`   | Approve the current human-approval gate.                      |
| `Ctrl+O`   | Reopen the pending approval artifact after closing it.        |
| `Ctrl+E`   | Toggle the error banner.                                      |
| `Ctrl+X`   | Cancel the current run.                                       |
| `q`        | Quit.                                                         |

### Context pack gate

When the context-first workflow is enabled, the first gate is the repository
context pack. The artifact viewer opens automatically:

| Key | Action |
|---|---|
| `e` | Edit the pack (add your context in section 7) |
| `Ctrl+S` | Save edits |
| `Ctrl+A` | Approve |
| `r` | Regenerate (your user block is preserved) |
| `Esc` | Close the viewer |
| `Ctrl+O` | Reopen the gate |

See [`context-pack.md`](context-pack.md) for the document format.

---

## System Monitor (`Ctrl+T`)

The System Monitor shows live host metrics and, most importantly, per-agent
token usage. It has four sections: **CPU**, **MEM**, **NET**, and **TOKENS**.

```
⚡ SYSTEM MONITOR
  up 4h 12m • 10 cores

── CPU ──
  34.2%   load: 2.1 1.8 1.6

── MEM ──
  61.0%   12.4 GB / 32.0 GB

── NET ──
  ▼ 1.2 MB/s   ▲ 240 KB/s

── TOKENS ──
 Total: 75.6k tok
 pm          ↓11.9k ↑2.2k~
 coder       ↓33.7k ↑4.5k~
 qa          ↓27.0k ↑1.8k~
```

### NET vs. TOKENS arrows

Note that the two sections use **different** glyphs:

- **NET** uses solid triangles for network throughput:
  - `▼` = **download** rate (bytes received per second).
  - `▲` = **upload** rate (bytes sent per second).
- **TOKENS** uses thin arrows for token counts:
  - `↓` = **input** tokens (the prompt the agent sent *to* the model).
  - `↑` = **output** tokens (the text the model generated *back*).

### Reading the TOKENS section

- **`Total: 75.6k tok`** — the sum of input + output tokens across all agents
  for this run.
- Each following line is one agent: its role, then `↓` input tokens and `↑`
  output tokens.
- A trailing **`~`** means the counts are **estimated** locally rather than
  reported exactly by the CLI. This is normal for local models and fallback
  runners that do not return precise usage; runners that report exact usage
  (e.g. cloud APIs) omit the `~`.

**Worked example:**

```
 coder       ↓33.7k ↑4.5k~
```

> The `coder` agent consumed roughly **33,700 input tokens** (the prompts,
> context, and code it fed to the model) and produced roughly **4,500 output
> tokens** (the code and explanations it generated). The `~` indicates these
> figures are estimated.

```
 pm          ↓11.9k ↑2.2k
```

> The `pm` agent used **exactly 11,900 input** and **2,200 output** tokens —
> no `~`, so these came directly from the runner's reported usage.

### Number formatting

Token and rate values are abbreviated for compactness:

| Raw value     | Displayed |
|---------------|-----------|
| `950`         | `950`     |
| `2,200`       | `2.2k`    |
| `33,700`      | `33.7k`   |
| `1,250,000`   | `1.3M`    |

When more agents are active than fit in the section, the list auto-scrolls
through them.
