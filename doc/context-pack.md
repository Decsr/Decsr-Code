# Repository Context Pack

The context pack is a PM-generated Markdown briefing of the repository. It is
created **before planning** and approved by you. It gives every planning and
review agent a shared, compact understanding of the project, and gives you a
place to add context that the code cannot express.

## Workflow

```
Collect repository context
        ↓
PM compacts it → .decsr/context_pack.md
        ↓
You review / edit / add text or URLs
        ↓
Approve (Ctrl+A) or regenerate (r)
        ↓
PM asks: "What would you like me to do?"
        ↓
PM negotiates the TaskSpec using the approved pack + your request
        ↓
Existing pipeline continues
```

The pack is the **primary planning context** after approval. Source code is
still attached separately to the coder and reviewers, so implementation detail
is not lost.

## The file

Location: `.decsr/context_pack.md`

Approval marker: `.decsr/context_pack.approved`

The generated document contains:

1. Project snapshot
2. Architecture at a glance
3. Key code map
4. Key code excerpts
5. Conventions and patterns
6. Constraints, risks and unknowns
7. **Additional Context — user-editable**
8. Open questions

### Your section

Add anything useful between these markers:

```markdown
## 7. Additional Context — user-editable
<!-- USER-CONTEXT:BEGIN -->
Add design notes, requirements, links, examples, constraints, or URLs here.
<!-- USER-CONTEXT:END -->
```

This block is **preserved when the pack is regenerated**. Content added outside
the markers is used for the current run but is replaced on regeneration.

URLs are recorded as references. DECSR does not fetch them; agents
with web access may use them, otherwise treat them as documentation pointers.

## Editing in the TUI

When the pack is generated, the artifact viewer opens automatically.

| Key | Action |
|---|---|
| `e` | Edit the raw Markdown |
| `Ctrl+S` | Save edits |
| `Ctrl+A` | Approve the pack |
| `r` | Regenerate the pack (your user block is preserved) |
| `Esc` | Close the viewer |
| `Ctrl+O` | Reopen the pending approval gate |
| `[/]` | Previous / next artifact |

Edit section 7, save, then approve.

## Configuration

```yaml
project:
  context:
    pack:
      disable: false            # true = skip the context pack phase
      max_output_tokens: 6000   # cap for the generated portion
```

The pack is enabled by default. `--no-context-pack` on `task` or `run` disables
it for one invocation.

## Plain / headless mode

Plain mode cannot edit or approve interactively. It generates the pack,
auto-approves it, logs the path, and uses the supplied task description:

```bash
decsr task --description "add a health endpoint" --ui plain
```

If no task description is supplied in plain mode, the run fails with an error.

## Resume

An approved pack (`context_pack.md` + `context_pack.approved`) is reused when a
run is resumed. If the pack is missing or unapproved, it is regenerated before
the pipeline continues.

## Safety

- The prompt instructs the PM to redact secrets and never invent paths or
  commands.
- The pack is written under `.decsr/` and is reviewed by you before it is
  used.
- Treat the pack as project documentation: do not paste credentials or private
  tokens into section 7.
