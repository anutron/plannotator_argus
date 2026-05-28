---
tags: [argus, plannotator]
audience: [shared]
---

If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section.

## Plannotator inside argus sandboxes

Plannotator runs as a daemon outside the sandbox; drive it exclusively via `mcp__argus__plannotator_*` MCP tools – never via `Bash(plannotator ...)`.

**Async poll rule:** Every verb tool (`plannotator_annotate`, `plannotator_review`, `plannotator_setup_goal`, `plannotator_last`) returns immediately with `{session_id, status:"pending"}`. A human annotates in a browser, so resolution can take minutes. Poll `mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>)` repeatedly until `status:"complete"`.

**Never do this in the sandbox** – the bash guard will deny it and EPERM on the session write:

- `Bash(plannotator annotate ...)` – use `mcp__argus__plannotator_annotate` instead
- `Bash(plannotator review ...)` – use `mcp__argus__plannotator_review` instead
- `Bash(plannotator last ...)` – use `mcp__argus__plannotator_last` instead
- `Bash(plannotator setup-goal ...)` – use `mcp__argus__plannotator_setup_goal` instead

Always pass `cwd=$PWD` to every `mcp__argus__plannotator_*` call.

For tool details, decision rules, sibling-plugin composition (`iris`, `hera`), and worked workflows, load the `plannotator-argus` skill.
