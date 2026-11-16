# Automations

An automation is a Workspace run that starts by itself on a schedule: it takes
a task (or the oldest matching GitHub issue), runs at autonomy level 2 or 3
inside a budget, and exits when it is done. The system scheduler (cron) starts
it. decsr has no background service.

## Config

`.decsr/automations.yaml`:

```yaml
automations:
  - name: deps-bump            # a-z, 0-9 and dashes; unique
    schedule: "0 3 * * 1-5"    # 5-field cron, local time
    task: "Update Go dependencies and fix anything that breaks"
    autonomy: 3                # required: 2 or 3
    budget_usd: 2.5            # required: greater than 0
    timeout: 2h                # optional, default 2h

  - name: triage
    schedule: "30 6 * * *"
    issue_query: "label:decsr is:open"   # instead of task
    autonomy: 2
    budget_usd: 1
```

- Set exactly one of `task` and `issue_query`. With `issue_query` the run
  starts from the oldest open issue that matches (`gh issue list --search
  "<query> sort:created-asc" --limit 1`), the same way `decsr issue <N>`
  does. No match means `nothing to do`.
- `schedule` takes five fields: minute, hour, day of month, month, day of week
  (0-7, 0 and 7 are Sunday). Fields take numbers, `*`, `*/n`, ranges `a-b`,
  `a-b/n` and comma lists. When both day fields are restricted, either one
  matches, as in cron.
- Errors name the line and the automation, for example
  `line 7: automation "deps-bump": autonomy must be 2 or 3`. Every problem in
  the file is listed at once.

## Commands

```
decsr automation list
decsr automation run <name> [--tmux]
decsr automation install [name]
decsr automation uninstall [name]
```

- `list` shows the name, schedule, next run time, task or issue query,
  autonomy, budget, whether the crontab line is installed and the last result
  from the run log.
- `run <name>` opens the Workspace in the current terminal on a fresh session
  and runs the automation. `run <name> --tmux` creates a detached tmux session
  `decsr-auto-<project>-<name>` that runs the same command and never attaches;
  this is what cron uses, because cron has no terminal.
- `install` writes one crontab line per automation (or just `name`) and shows
  it. It is idempotent: a line with the same marker is replaced, other lines
  are not touched, and the crontab is only rewritten when it changes.
  `uninstall` removes the marked lines. Neither works on Windows.

The crontab line:

```
0 3 * * 1-5 cd '/home/me/proj' && PATH='/usr/bin:/bin' '/usr/local/bin/decsr' automation run deps-bump --tmux >> '/home/me/proj/.decsr/automations/deps-bump.log' 2>&1 # decsr:/home/me/proj:deps-bump
```

The line keeps the `PATH` of the shell that ran `install`, so the runners and
`gh` are found, and the absolute path of the decsr binary. After moving or
reinstalling decsr, run `decsr automation install` again; `decsr doctor` warns
when an installed line points at a missing binary.

## Safety rules

- An automation needs autonomy L2 or L3 and a budget. A file without them is
  refused, by `run` and by `install`.
- One run at a time: `.decsr/automations/<name>.lock` holds the pid and the
  start time. A new run that finds a live lock logs `skipped: previous run
  still active` and exits 0. A lock is stale when its process is gone or it is
  older than `timeout` + 10 minutes.
- A run that changes nothing only notifies `automation <name>: nothing to do`.
  It does not push and does not open a pull request.
- Level 3 never merges the pull request, like a normal level 3 session.
- The budget and the timeout end the run: `budget reached` or `timed out`
  (notification `automation <name> timed out`).

## When a run is done

The Workspace exits by itself when the engine is idle and no automatic action
is pending, and:

- level 2: Build & test has its final result (passed, or failed with the
  automatic fixes used up);
- level 3: also the pull request flow ended: its checks passed, or the CI fix
  rounds are used up, or the pull request failed, or the pull request has had
  no checks at all for 10 minutes of polling (`pr #N opened (no checks)`,
  Info notification; checks that appear within those 10 minutes are handled
  as usual);
- or nodes wait for you (a question, a blocked node, a node without a role):
  you get a `automation <name> needs you` notification and the session stays
  for `decsr resume`.

## Log

Every run appends one line to `.decsr/automations/<name>.log`:

```
start=2026-11-16T03:00:00+01:00 end=2026-11-16T03:12:41+01:00 cost=$0.42 result=pr #12 checks passed
```

Results: `pr #12 checks passed`, `pr #12 CI fixes used up`, `pr #12 opened (no checks)`, `build & test passed`
(level 2), `nothing to do`, `needs you`, `budget reached`, `timed out`,
`failed: <why>`, `skipped: previous run still active`, `interrupted`. The cron
output (`>> ... 2>&1`) goes to the same file; the lines that do not start with
`start=` are ignored by `decsr automation list`.

## Watching a run

```
decsr ls                       # lists decsr-auto-... next to the other sessions
decsr attach decsr-auto-<project>-<name>
```

Detach again with the tmux prefix and `d`; the run keeps going.
