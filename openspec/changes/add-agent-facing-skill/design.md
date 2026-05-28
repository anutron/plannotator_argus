## Context

When a developer starts a Claude session inside an argus task worktree (cwd under `~/.argus/worktrees/`), the agent sees the plannotator-argus tools only as bare `mcp__argus__plannotator_*` names with one-line descriptions. It has no idea that:

- It is inside a sandbox where direct `plannotator <verb>` invocations EPERM on the session-file write.
- The verb tools are **async** — they return `{session_id, status:"pending"}` and the real result arrives only after a human annotates in a browser, retrieved by polling `plannotator_session_result`.
- This plugin owns only the annotation/review UI seam, and composes with sibling plugins (`iris` for host-side git/PR ops, `hera` for orchestration).

The daemon, its five MCP tools, and the in-flight `plannotator-mcp-bash-guard` (a `PreToolUse(Bash)` hook that denies direct `plannotator <verb>` and names the MCP tool to use instead) already exist. The guard is a *runtime backstop*; it fires only when the agent already reached for the wrong call. What is missing is the *proactive* orientation layer: a skill the model can read to learn the right path before making a mistake, and an optional always-loaded snippet for users who want that orientation in context unconditionally.

The decision (set by the handoff, not re-litigated here): these docs ship **in this repo** and install into `~/.claude/` via an installer, gated at runtime on argus-awareness. We do **not** modify argus core to inject CLAUDE.md fragments at worktree-create time.

## Goals / Non-Goals

**Goals:**

- Ship `claude/skills/plannotator-argus/SKILL.md` — agent-facing, self-gating on argus-awareness, covering tool surface, the async poll pattern, decision rules, sibling composition, common Bash mistakes, and 2–3 worked workflows.
- Ship `claude/snippets/plannotator-argus.md` — an optional always-loaded CLAUDE.md fragment whose first content line is the argus gate, with `tags`/`audience` frontmatter so it slots into a compile pipeline or a plain `~/.claude/CLAUDE.md` append.
- Ship `install-claude-skill.sh` at repo root — idempotent symlink installer for the skill (always) and snippet (offered), with a clear changed/skipped report.
- Any developer who installs this plugin and runs the installer gets the same agent behavior — nothing is scoped to one user.

**Non-Goals:**

- No changes to argus core, the daemon, the Go code, or the MCP tool surface.
- No new top-level `CLAUDE.md` for the plugin repo (that file is for plugin *developers*; the skill is the agent-facing surface).
- No re-implementation of the bash-guard — it already exists in the `plannotator-mcp-bash-guard` change. The skill's "common Bash mistakes" section is the *teaching* counterpart to the guard's *enforcement*; they reinforce, not duplicate.
- The installer does not wire Claude Code hooks or settings.json — that stays in `deploy/install.sh` and the README.

## Decisions

### D1: Skill name `plannotator-argus`, new top-level `claude/` directory

Kebab-case `plannotator-argus` matches the README/binary naming and the skill-name convention (`anutron-install`, `airon-blog`). Artifacts live under a new `claude/` directory (`claude/skills/...`, `claude/snippets/...`) to keep agent-facing assets separate from `deploy/` (daemon ops) and `.claude/` (this repo's own dev-time skills/commands).

**Alternative considered:** put the skill under the existing `.claude/skills/`. Rejected — `.claude/` is this repo's *developer* tooling (openspec commands); the shipped, installable artifact is a distinct concern and a flat `claude/` source dir reads clearly as "the stuff the installer symlinks out."

### D2: The argus-awareness gate is the skill's first body section and the snippet's first content line

The skill `description` leads with the gate so the model only surfaces it inside argus sandboxes. The body opens with an explicit check (`cwd` under `~/.argus/worktrees/` **OR** `ARGUS_TASK_ID` set) and, on failure, a hard stop: *"Not in an argus sandbox — these MCP tools aren't registered here. Use the `plannotator` CLI directly instead."* The snippet's first content line is the inverse guard so an always-loaded fragment self-suppresses outside argus.

**Alternative considered:** gate only via the `description`. Rejected — the description controls *retrieval*, but once the body is in context the model should still be able to self-verify and bail, and the snippet (which is *always* loaded) needs an in-body gate regardless.

### D3: Teach the async session-poll pattern as the load-bearing rule

The single highest-value lesson: every verb tool (`annotate`, `review`, `setup_goal`, `last`) returns immediately with `{session_id, status:"pending"}`; the agent must poll `plannotator_session_result(cwd, session_id)` (long-polls up to `wait_seconds`, default 20, max 25) until `status:"complete"`. Because a human is annotating in a browser, resolution can take minutes — the skill instructs repeated polling, not a single call. This is given its own section and threaded through every worked workflow.

### D4: Installer is a standalone idempotent symlink script, separate from `deploy/install.sh`

`install-claude-skill.sh` at repo root owns only the agent-facing artifacts. It symlinks `claude/skills/plannotator-argus` → `~/.claude/skills/plannotator-argus` (always), and offers the snippet. Daemon/LaunchAgent/hook/guard install stays in `deploy/install.sh`. Clean separation of "teach agents" from "run the daemon"; the in-flight bash-guard change already references `deploy/install.sh` and is left untouched.

**Alternative considered:** fold skill-symlinking into `deploy/install.sh`. Rejected by the user in favor of a separate, descriptively-named script.

### D5: Snippet install is generic — env var or explicit flag, else print-and-instruct

The installer symlinks the snippet into a snippet directory only when one is given via `--snippet-dir <path>` or the `$CLAUDE_SNIPPETS_DIR` env var (and that dir exists). Otherwise it prints the snippet's absolute path and a one-line instruction (append to `~/.claude/CLAUDE.md`, or wire into a snippet compile pipeline). No user-specific path is hardcoded; a compile-pipeline user (e.g. one with `claude-rules/snippets/global/`) sets the env var once.

**Alternative considered:** probe a list of known snippet-dir locations. Rejected — guessing risks symlinking into the wrong place; explicit opt-in (flag/env) is safe and predictable.

### D6: Idempotency semantics

For each symlink target the installer classifies: **(a)** missing → create, report `created`; **(b)** already a symlink to the correct source → report `ok (already linked)`; **(c)** a symlink to a different source → repoint, report `relinked`; **(d)** a real file/dir (not a symlink) → refuse to clobber, report `SKIPPED (exists, not a symlink)` and leave it. Exit 0 on every benign case; the script is safe to re-run.

## Risks / Trade-offs

- **Skill drifts from the actual tool descriptions** (in `internal/daemon/run.go`) → Mitigation: the skill's tool table is derived from those descriptions at write time, and the design names the source file so a future maintainer knows where the canonical descriptions live. A spec requirement pins the five tool names so a renamed tool surfaces as a spec mismatch.
- **Snippet always-loaded cost** → Mitigation: the snippet is opt-in (the skill alone satisfies the discoverability goal); its gate self-suppresses outside argus so it costs ~nothing in unrelated sessions.
- **Symlink installer clobbering a user's real file** → Mitigation: D6 refuses to overwrite non-symlinks and reports them; never deletes a real file.
- **Gate false-negative inside a non-standard argus layout** (e.g. argus worktrees relocated) → Mitigation: the gate accepts *either* the `~/.argus/worktrees/` path prefix *or* the `ARGUS_TASK_ID` env var, so an env-set sandbox still passes even if the path differs.

## Migration Plan

No migration. Pure additive: new `claude/` dir, new root installer, new spec requirements. Rollback = delete the symlinks (`rm ~/.claude/skills/plannotator-argus`) and the `claude/` dir. No daemon or runtime impact.

## Acceptance criteria

**Skill content (§ skill):**

- it should refuse to act and tell the agent to use the `plannotator` CLI directly when neither `~/.argus/worktrees/` cwd nor `ARGUS_TASK_ID` holds
- it should document all five `mcp__argus__plannotator_*` tools with when-to-call guidance
- it should instruct the agent to poll `plannotator_session_result` until `status:"complete"` after every verb call
- it should list the direct `plannotator <verb>` Bash calls that EPERM in the sandbox and name the MCP tool to use instead
- it should describe how the plugin composes with `iris` and `hera`
- it should include at least two worked end-to-end workflows

**Snippet (§ snippet):**

- it should carry `tags` and `audience` frontmatter
- it should open its body with the argus gate as the first content line

**Installer (§ installer):**

- it should symlink the skill into `~/.claude/skills/plannotator-argus`
- it should report `created` / `ok` / `relinked` / `SKIPPED` per target and be safe to re-run
- it should refuse to overwrite a real (non-symlink) file at a target path
- it should symlink the snippet when `--snippet-dir` / `$CLAUDE_SNIPPETS_DIR` is provided, else print the snippet path and wiring instruction

## Open Questions

None outstanding. Installer name (`install-claude-skill.sh`) and snippet-install strategy resolved during brainstorm.
