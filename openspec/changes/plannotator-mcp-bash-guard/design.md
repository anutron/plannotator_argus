## Context

The daemon ships five MCP tools (`mcp__argus__plannotator_annotate`, `_review`, `_setup_goal`, `_last`, `_session_result`) plus a `POST /hook` endpoint. Those are the only Plannotator code paths that work inside an argus task sandbox — direct `plannotator <verb>` invocations EPERM on the session-file write at `~/.plannotator/sessions/<pid>.json`.

Two of the three entry points are already protected:

- **ExitPlanMode** is routed through `~/.local/bin/plannotator-hook`, wired in `~/.claude/settings.json` (operator-owned, installer-safe).
- **Direct MCP calls** are available because the daemon registers the five tools with argus on startup.

The third entry point — Claude reading the Plannotator skill files and following their "Run: `plannotator annotate <path>`" instruction — is still broken. The skill files instruct Claude to use Bash with the `plannotator` binary, and Plannotator's installer refreshes those files on every update, so we cannot patch them in place.

## Goals / Non-Goals

**Goals:**

- Inside an argus task sandbox, force the MCP path for the four Plannotator verb invocations the skills generate.
- Outside an argus sandbox (host shell, non-argus projects), leave Bash invocations of `plannotator` alone — direct execution is the right path there.
- Survive a Plannotator installer refresh. The fix lives at `~/.local/bin/plannotator-bash-guard` and is wired via `~/.claude/settings.json`, neither of which the installer touches.
- Give Claude a verb-specific redirect message so the swap from `Bash(plannotator <verb> ...)` to `mcp__argus__plannotator_<verb>(...)` requires no further reasoning.

**Non-Goals:**

- **Don't patch the upstream skills.** They're rewritten by the Plannotator installer; any edit dies on next refresh.
- **Don't modify the daemon, MCP tools, or hook endpoint.** The MCP path already works — we're just steering Claude onto it.
- **Don't try to silence the prompt itself.** The hook returns a deny + reason to Claude; it does not reach into Plannotator's skill text or Claude's prompt to suppress the original instruction.
- **Don't guard non-Bash tool calls.** Only Bash needs intercepting — Claude can't accidentally invoke the binary through Read/Edit/Write.

## Decisions

### D1. PreToolUse(Bash) hook

The guard is a `PreToolUse` hook with matcher `Bash`. Claude Code invokes it before every Bash call, passing the tool input as JSON on stdin. The script returns one of two responses:

- **Allow** — exit 0 with empty stdout/stderr.
- **Deny** — exit 2 with a JSON document on stderr: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"},"systemMessage":"<reason>"}`.

The deny path includes a verb-specific redirect message so Claude can swap to the MCP tool without further reasoning. This is the documented PreToolUse contract per Claude Code's hook-development skill.

**Alternatives considered:**

- **Stop hook that rewrites Claude's message after the failure.** Rejected — the EPERM has already happened, the session is half-broken, and recovering is messy.
- **Replace the `plannotator` binary on `$PATH` with a shim that routes to MCP.** Rejected — there's no way for a binary spawned by Claude Code's Bash tool to talk back to the model; the redirect needs the hook channel.
- **Patch the upstream skill files.** Rejected — they're refreshed by the Plannotator installer.

### D2. Match by verb prefix, anchored

The matched commands are exactly `plannotator annotate|review|last|setup-goal`. The regex is anchored on both sides so it doesn't false-positive on lookalikes:

- `plannotator-argus status` — does not match (hyphen, not whitespace).
- `plannotator-hook < /tmp/x` — does not match.
- `plannotator --version` — does not match (`--` is not a verb).
- `plannotator` with no verb — does not match.
- `plannotator-archive` — does not match (hyphen prefix).

Allowed leading characters: start-of-string, whitespace, or `/` (to catch `/usr/local/bin/plannotator annotate foo`). Allowed trailing characters: whitespace or end-of-string.

**Alternatives considered:**

- **Match any command containing `plannotator`.** Rejected — too broad. Daemon ops (`plannotator-argus`) and the hook wrapper (`plannotator-hook`) live alongside the binary and must continue to work.
- **Restrict to commands where `plannotator` is the first token.** Rejected — misses compound invocations like `cd foo && plannotator annotate bar`, which Claude generates occasionally.

### D3. Gate on `$PWD` under `~/.argus/worktrees/`

The guard is a no-op anywhere outside an argus task worktree. Detection is a simple prefix check on `$PWD`: starts with `${HOME}/.argus/worktrees/`. No env var lookup, no calls back to the argus daemon.

**Why prefix check, not env var:** Argus tasks don't necessarily set a marker env var, and even when they do, env vars get clobbered in compound shell invocations. `$PWD` is set by the shell and reliable.

**Alternatives considered:**

- **Always intercept, regardless of cwd.** Rejected — would block direct `plannotator annotate` on the host shell, defeating its primary use case.
- **Check for an argus task ID env var.** Rejected — unreliable across compound commands, and the prefix check is simpler.

### D4. Use jq for parsing and message construction

Both stdin parsing (`.tool_input.command`) and stderr emission (the deny JSON) go through jq. The repo already requires `bash` and `curl` for the daemon hook wrapper; adding `jq` as a third dependency is consistent with the rest of Aaron's hook infrastructure (`hooks/log-skill-use.sh`, `hooks/remind-session-topic.sh` etc. all use jq) and is preinstalled on macOS via Homebrew.

Using jq for emission means quotes, newlines, and embedded backticks in the redirect message are escaped correctly with no manual quoting gymnastics.

### D5. Verb-specific redirect message

Each of the four verbs has a tailored redirect with the exact MCP tool name and its arg mapping. The intent is that Claude reads the deny reason once and knows what to do — no follow-up search through the daemon's MCP schema required.

```
Direct invocation of `plannotator annotate` is blocked inside argus task sandboxes —
the session file write to ~/.plannotator/sessions/<pid>.json EPERMs. Use the MCP tool instead:

mcp__argus__plannotator_annotate
  Args: cwd=$PWD, path=<the path/URL you passed to `plannotator annotate`>

The tool returns {session_id, status: "pending"} immediately. Poll
mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>) until
status="complete" to get the result.
```

The polling instruction is appended to all four verbs because every starter tool returns the same envelope shape.

## Risks / Trade-offs

- **PreToolUse fires on every Bash call.** Cost is one bash + one jq invocation per Bash tool use. Empirically <10 ms; trivial in context.
- **`set -uo pipefail` not `-e`.** The guard deliberately swallows parsing errors and falls through to "allow" — it's better to let an unexpected payload through than to brick every Bash invocation if jq can't parse the stdin. This is a safety-fail-open posture matching the rest of the install (a broken plannotator-hook also falls back to a direct plannotator call).
- **Path-based detection of argus sandboxes.** If Aaron ever relocates argus worktrees, the prefix string in the guard needs updating. Same applies to `plannotator-hook` (it doesn't currently check cwd, but the project layout is the same).
- **Skill file refresh doesn't reach Claude until next session.** When Aaron updates Plannotator, Claude in an already-open session still has the old skill text in memory. The hook fires regardless of skill text, so this doesn't matter for enforcement; it's only relevant when the hook is uninstalled and a stale skill is the only remaining instruction.

## Migration Plan

1. `./deploy/install.sh` writes the guard to `~/.local/bin/plannotator-bash-guard` alongside the existing `plannotator-hook` wrapper. Idempotent.
2. Operator adds the `PreToolUse → Bash → plannotator-bash-guard` stanza to `~/.claude/settings.json` (one-time; README documents the exact stanza). The existing `PermissionRequest → ExitPlanMode → plannotator-hook` stanza already lives in the same file, so the change is appending one entry.
3. In a fresh argus task, follow `/plannotator-annotate <path>`. The Bash call is denied, Claude reads the redirect, calls `mcp__argus__plannotator_annotate`, browser opens, annotation completes via `mcp__argus__plannotator_session_result`.

Rollback: remove the `PreToolUse → Bash` stanza from `~/.claude/settings.json`, or run `./deploy/uninstall.sh` (which removes the guard script). With the script gone the hook entry becomes a no-op (Claude Code logs a missing-command warning and proceeds).

## Open Questions

- **Does Claude promptly swap on first deny, or does it sometimes retry the same Bash invocation?** Empirical question — answer determines whether the deny message needs to be even more directive. Initial test should show it.
- **Should the guard also intercept `plannotator improve-context` and `plannotator archive`?** Today the daemon has no MCP tools for those. If they ever break inside argus, the right fix is adding MCP tools first, then extending the guard's regex. Out of scope for v1.
