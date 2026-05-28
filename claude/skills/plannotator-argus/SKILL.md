---
name: plannotator-argus
description: Use ONLY inside an argus sandbox (cwd under ~/.argus/worktrees/ or ARGUS_TASK_ID set) to drive Plannotator's annotation & review UI via the mcp__argus__plannotator_* tools. Covers the async session-poll pattern, annotate-vs-review decision rules, sibling-plugin composition, and which Bash(plannotator …) calls EPERM in the sandbox.
---

# Plannotator inside argus

## Argus gate (check this first)

Are you in an argus sandbox? You are if EITHER of these holds:

- The current working directory (`$PWD`) is under `~/.argus/worktrees/`.
- The `ARGUS_TASK_ID` environment variable is set.

If NEITHER holds: STOP.

- Not in an argus sandbox; these MCP tools aren't registered here. Use the `plannotator` CLI/binary directly instead (for example `plannotator annotate <path>`).
- Do not use the rest of this skill.

If either condition holds, continue.

## What this is

The plannotator-argus daemon runs OUTSIDE the sandbox and drives a real Plannotator browser session on the host. You drive it through the `mcp__argus__plannotator_*` MCP tools because a direct `plannotator <verb>` call inside the sandbox EPERMs when Plannotator tries to write its session file at `~/.plannotator/sessions/<pid>.json`. The MCP tools move that work outside the sandbox, so they are the only working path here.

## The async session-poll pattern (the load-bearing rule)

Every verb-starter tool (`annotate`, `review`, `setup_goal`, `last`) returns IMMEDIATELY with an envelope like `{session_id, url, status:"pending"}`. That is NOT the result. The real result – a human's annotations or review feedback – only arrives after they finish working in the browser.

You MUST poll for it:

- After any verb call, take the returned `session_id` and call `mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>)`.
- That call long-polls up to `wait_seconds` (default 20, max 25) and returns whatever the session's current `status` is.
- Loop the call until `status` is `"complete"` (or `"failed"`). A human annotating in a browser may take minutes, so a single poll will usually still be `"pending"` – keep polling.
- On `"complete"`, read the `result` field and act on it. On `"failed"`, read `error`.
- Always pass `cwd=$PWD`.

## Tool surface

All five tools take `cwd` and you should always pass `cwd=$PWD`.

| Tool | When to call it | Required args | Returns |
| --- | --- | --- | --- |
| `mcp__argus__plannotator_annotate` | You want inline human feedback on a file, folder, or http(s) URL. | `cwd` (=$PWD), `path` (file under cwd, or an http(s):// URL) | Pending envelope `{session_id, url, status:"pending"}` |
| `mcp__argus__plannotator_review` | You want a human to review a code change – the current local branch diff (`git=true`) or a PR (`pr_url`). | `cwd` (=$PWD); plus `git` (bool) OR `pr_url` (string) | Pending envelope |
| `mcp__argus__plannotator_setup_goal` | You want to drive the setup-goal flow to build a goal package. | `cwd` (=$PWD), `mode` (`"interview"` or `"facts"`), `bundle_path` (path to a bundle JSON under cwd) | Pending envelope |
| `mcp__argus__plannotator_last` | You want the human to mark up your most recently rendered assistant message. | `cwd` (=$PWD) | Pending envelope |
| `mcp__argus__plannotator_session_result` | You need the real result of a session you started. Poll this. | `session_id`; pass `cwd` (=$PWD); optional `wait_seconds` (default 20, max 25) | `{session_id, status, url?, result?, error?}` |

## When to use what

- Get inline human feedback on a file, folder, or URL -> `annotate`.
- Get a code change reviewed (current branch diff, or a PR) -> `review`.
- Re-open your last message for the user to mark up -> `last`.
- Build a goal package -> `setup_goal`.
- After ANY of the above -> poll `session_result` until `status:"complete"`.

## Common Bash mistakes

- Do NOT call `Bash(plannotator annotate …)`, `Bash(plannotator review …)`, `Bash(plannotator last …)`, or `Bash(plannotator setup-goal …)`. Inside the sandbox these EPERM on the session-file write, and a `plannotator-bash-guard` PreToolUse hook actively DENIES them and names the MCP tool to use instead. Use the matching `mcp__argus__plannotator_*` tool.
- Do NOT use this plugin for git or PR operations. Those are not its job (see composition below).
- Do NOT forget `cwd` – every tool needs `cwd=$PWD` (the caller's worktree).

## Composition with sibling plugins

plannotator-argus owns ONLY the annotation/review UI seam. It does not do git, PRs, or orchestration. Compose it with siblings:

- `iris` – host-side git and GitHub PR operations from inside the sandbox (`mcp__argus__iris_push`, `mcp__argus__iris_gh_pr_create`, `mcp__argus__iris_gh_pr_view`, `mcp__argus__iris_status`, and more). Seam: use iris to push a branch and open or inspect a PR host-side, then feed the resulting PR URL into `mcp__argus__plannotator_review(pr_url=…)`.
- `hera` – multi-agent orchestration (`mcp__argus__hera_*`: orchestrator and worker messaging). Seam: an orchestrator can dispatch a "review this" task to a worker session, which drives `mcp__argus__plannotator_review`, polls for the result, and reports the annotations back.

## Worked workflows

### A. Review my current branch before opening a PR

1. `mcp__argus__plannotator_review(cwd=$PWD, git=true)` -> note the returned `session_id`.
2. Loop `mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>)` until `status:"complete"`.
3. Read `result` and act on each review comment.

### B. Annotate a spec or design doc

1. `mcp__argus__plannotator_annotate(cwd=$PWD, path="openspec/changes/foo/design.md")` -> note the `session_id`.
2. Loop `mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>)` until `status:"complete"`.
3. Read `result` and address each annotation.

### C. Review a PR opened via iris

1. `mcp__argus__iris_gh_pr_create(...)` -> take the returned PR URL.
2. `mcp__argus__plannotator_review(cwd=$PWD, pr_url="<url>")` -> note the `session_id`.
3. Loop `mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>)` until `status:"complete"`.
4. Read `result` and respond to the feedback.
