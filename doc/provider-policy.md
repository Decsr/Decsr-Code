# Provider policy check

decsr drives other vendors' CLIs and APIs, so each feature must stay inside
their terms. The strictest case today is the claude CLI on a Pro or Max
subscription login. Run every check below on a new feature; mark each one pass,
or fail with the guard that fixes it.

## Sources

Re-read these when a check is unclear or a rule may have changed:

- Claude Code legal and compliance: https://code.claude.com/docs/en/legal-and-compliance
- Anthropic Consumer Terms (section 2 accounts, section 3 automated access): https://www.anthropic.com/legal/consumer-terms
- Claude Code with a Pro or Max plan: https://support.claude.com/en/articles/11145838
- Usage limits: https://support.claude.com/en/articles/11647753
- For other runners (codex, opencode, dsh, API presets), the vendor's own terms.

## Checks

1. **Person present.** Anything that runs an agent with nobody at the session
   (cron automations, `/autonomy 3`, auto-retry after a limit, auto-continue)
   needs an API key for claude, or an explicit opt-in that warns first
   (`allow_subscription`, `--yes`). Use `runner.ClaudeAuthMode()` to tell
   `api-key` from `subscription`. Consumer Terms section 3 bans "automated or
   non-human means" except with an API key.
2. **Official binary only.** Run the installed, unmodified `claude` program
   through its documented flags. Patch nothing, and call no model endpoint as
   the user.
3. **Login tokens untouched.** Never read, copy, store or send
   `~/.claude/.credentials.json`, keychain entries or OAuth tokens, and never
   call private account endpoints such as `/api/oauth/usage`. Usage and limits
   come only from what the CLI prints (stream-json, error text, status-line
   data) or from an API the user gave a key for.
4. **Limits respected.** Hitting a limit waits for the reset or for the user.
   Never rotate accounts, never retry in a tight loop, and on a subscription
   claude keeps auto-retry off by default (`workspace.limit_auto_retry`).
5. **Ordinary, individual usage.** Fan-out stays bounded: `max_parallel`
   (3 by default, 8 at most) and `budget_usd`. Nothing runs around the clock
   like a service, and nothing serves other people through the user's login.
6. **No account sharing.** One user's login runs that user's own work only.
   Never offer decsr as a hosted service on a subscription login.
7. **Visible and documented.** Every automatic action prints an `auto ›` line.
   When a feature touches checks 1 to 6, update `doctor`, the README section
   "Using the claude runner" and `doc/automations.md` to match.

## Known state

- Interactive Workspace with the user present: pass.
- Cron automations: guarded (subscription needs `allow_subscription`).
- Routing in unattended runs: guarded (`routingConnected` drops claude).
- Refused commands (claude runner): a command is allowed only by the user's
  key press on its card; no autonomy level and no automation run allows one.
- Installs from the chat (decsr plugins, `claude plugin install`, `claude mcp
  add`): pass. Installs: proposed by the lead, run by decsr only after the
  user's key press; never `-y`; secrets asked hidden and never stored by decsr.
  The lead only has read-only `claude ... list|get` lookups, workers get no
  install command, automation runs skip installs, and decsr runs the unmodified
  `claude` binary through its documented flags.
- MCP: only nodes the lead marks and the user approves load MCP, claude runner
  only, tools limited to the named servers, in text and agent mode with the
  same allow-list limits. decsr runs no MCP server itself, a
  plan with an MCP node always waits for the user's `y`, and an automation run
  stops instead of starting it.
- `/autonomy 3` on a subscription: **open**, it needs a warning or a block.
- Lead chat: read-only tools, user-started turns only; auto-compact runs only
  in front of a user-started turn.
- Plan limits in `/usage` and `/status`: pass. They come only from the
  `rate_limit_event` lines the claude CLI prints during runs the user started,
  saved in `~/.decsr/plan-limits.json`. decsr calls no account endpoint, starts
  no run to refresh them, and nothing schedules, retries or waits based on
  these numbers.
