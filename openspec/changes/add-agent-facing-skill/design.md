## Context

When a developer starts a Claude session inside an argus task worktree (cwd under `~/.argus/worktrees/`), the agent sees the plannotator-argus tools only as bare `mcp__argus__plannotator_*` names with one-line descriptions. It has no idea that:

- It is inside a sandbox where direct `plannotator <verb>` invocations EPERM on the session-file write.
- The verb tools are async – they return `{session_id, status:"pending"}` and the real result arrives only after a human annotates in a browser, retrieved by polling `plannotator_session_result`.
- This plugin owns only the annotation/review UI seam, and composes with sibling plugins (`iris` for host-side git/PR ops, `hera` for orchestration).

The daemon, its five MCP tools, and the in-flight `plannotator-mcp-bash-guard` (a `PreToolUse(Bash)` hook that denies direct `plannotator <verb>` and names the MCP tool to use instead) already exist. The guard is a runtime backstop; it fires only when the agent already reached for the wrong call. What is missing is the proactive orientation layer: a skill the model can read to learn the right path before making a mistake, and an optional always-loaded snippet for users who want that orientation in context unconditionally.

The decision (set by the handoff, not re-litigated here): these docs ship in this repo and install into `~/.claude/` via an installer, gated at runtime on argus-awareness. We do not modify argus core to inject CLAUDE.md fragments at worktree-create time.

## Goals / Non-Goals

**Goals:**

- Ship `claude/skills/plannotator-argus/SKILL.md` – agent-facing, self-gating on argus-awareness, covering tool surface, the async poll pattern, decision rules, sibling composition, common Bash mistakes, and 2–3 worked workflows.
- Ship `claude/snippets/plannotator-argus.md` – an optional always-loaded CLAUDE.md fragment whose first content line is the argus gate, with `tags`/`audience` frontmatter so it slots into a compile pipeline or a plain `~/.claude/CLAUDE.md` append.
- Ship `install-claude-skills.sh` at repo root – idempotent installer that prompts `(Y/n)` to symlink the skill and prompts `(Y/n)` to append the snippet to `~/.claude/CLAUDE.md`, with a clear changed/skipped report.
- Ship `uninstall-claude-skills.sh` at repo root – the reverse: prompts `(Y/n)` to remove the skill symlink and the appended snippet block, idempotently.
- Any developer who installs this plugin and runs the installer gets the same agent behavior – nothing is scoped to one user.

**Non-Goals:**

- No changes to argus core, the daemon, the Go code, or the MCP tool surface.
- No new top-level `CLAUDE.md` for the plugin repo (that file is for plugin developers; the skill is the agent-facing surface).
- No re-implementation of the bash-guard – it already exists in the `plannotator-mcp-bash-guard` change. The skill's "common Bash mistakes" section is the teaching counterpart to the guard's enforcement; they reinforce, not duplicate.
- The installer does not wire Claude Code hooks or `settings.json` – that stays in `deploy/install.sh` and the README.

## Decisions

### D1: Skill name `plannotator-argus`, new top-level `claude/` directory

Kebab-case `plannotator-argus` matches the README/binary naming and the skill-name convention (`anutron-install`, `airon-blog`). Artifacts live under a new `claude/` directory (`claude/skills/...`, `claude/snippets/...`) to keep agent-facing assets separate from `deploy/` (daemon ops) and `.claude/` (this repo's own dev-time skills/commands).

**Alternative considered:** put the skill under the existing `.claude/skills/`. Rejected – `.claude/` is this repo's developer tooling (openspec commands); the shipped, installable artifact is a distinct concern and a flat `claude/` source dir reads clearly as "the stuff the installer symlinks out."

### D2: The argus-awareness gate is the skill's first body section and the snippet's first content line

The skill `description` leads with the gate so the model only surfaces it inside argus sandboxes. The body opens with an explicit check (`cwd` under `~/.argus/worktrees/` OR `ARGUS_TASK_ID` set) and, on failure, a hard stop: "Not in an argus sandbox – these MCP tools aren't registered here. Use the `plannotator` CLI directly instead." The snippet's first content line is the inverse guard so an always-loaded fragment self-suppresses outside argus.

**Alternative considered:** gate only via the `description`. Rejected – the description controls retrieval, but once the body is in context the model should still be able to self-verify and bail, and the snippet (which is always loaded) needs an in-body gate regardless.

### D3: Teach the async session-poll pattern as the load-bearing rule

The single highest-value lesson: every verb tool (`annotate`, `review`, `setup_goal`, `last`) returns immediately with `{session_id, status:"pending"}`; the agent must poll `plannotator_session_result(cwd, session_id)` (long-polls up to `wait_seconds`, default 20, max 25) until `status:"complete"`. Because a human is annotating in a browser, resolution can take minutes – the skill instructs repeated polling, not a single call. This is given its own section and threaded through every worked workflow.

### D4: Installer + uninstaller are standalone idempotent scripts, separate from `deploy/install.sh`

`install-claude-skills.sh` and `uninstall-claude-skills.sh` at repo root own only the agent-facing artifacts. Daemon/LaunchAgent/hook/guard install stays in `deploy/install.sh`. Clean separation of "teach agents" from "run the daemon"; the in-flight bash-guard change already references `deploy/install.sh` and is left untouched. The plural "skills" name is generic – today it installs one skill, but the name accommodates more without a rename.

**Alternative considered:** fold skill-symlinking into `deploy/install.sh`. Rejected by the user in favor of separate, descriptively-named scripts.

### D5: Two `(Y/n)` prompts; snippet installs by appending to `~/.claude/CLAUDE.md`

The installer is interactive by default with two prompts, each defaulting to yes:

1. "Symlink the plannotator-argus skill into `~/.claude/skills/`? (Y/n)"
2. "Append the plannotator-argus snippet to `~/.claude/CLAUDE.md`? (Y/n)"

A "yes" to prompt 2 appends the snippet content to `~/.claude/CLAUDE.md` between idempotency markers (`<!-- BEGIN plannotator-argus ... -->` / `<!-- END plannotator-argus -->`); re-running replaces the marked block rather than duplicating it.

Non-interactive escape hatches (so the test harness and CI can exercise each branch without a TTY, and so compile-pipeline users opt out of the CLAUDE.md append):

- `-y` / `--yes` answers yes to both prompts.
- `--skill` / `--no-skill` and `--snippet` / `--no-snippet` decide a prompt explicitly and skip it.
- If `--snippet-dir <path>` or `$CLAUDE_SNIPPETS_DIR` resolves to an existing directory, the snippet is symlinked there (compile-pipeline path) instead of appended to `CLAUDE.md`, and prompt 2 is skipped. This honors setups that compile `~/.claude/CLAUDE.md` from snippets, where editing it directly would be wrong.
- When stdin is not a TTY and no decision flag was given, each prompt resolves to its default (yes) rather than hanging.

**Alternative considered:** symlink the snippet into a snippet dir as the primary path (the original handoff phrasing). Refined per user instruction – appending to `CLAUDE.md` is the default interactive behavior; the snippet-dir symlink survives only as the pipeline override.

### D6: Idempotency semantics

For each symlink target the installer classifies: **(a)** missing → create, report `created`; **(b)** already a symlink to the correct source → report `ok (already linked)`; **(c)** a symlink to a different source → repoint, report `relinked`; **(d)** a real file/dir (not a symlink) → refuse to clobber, report `SKIPPED (exists, not a symlink)` and leave it. The CLAUDE.md append is idempotent via the marker block. Exit 0 on every benign case; the script is safe to re-run.

### D7: Uninstaller mirrors the installer

`uninstall-claude-skills.sh` prompts `(Y/n)` to remove the skill symlink and prompts `(Y/n)` to remove the appended snippet. It only removes a symlink that points at this repo's `claude/...` source (a foreign symlink or a real file is reported and left untouched), and removes only the content between the CLAUDE.md markers (collapsing surrounding blank lines), leaving the rest of `CLAUDE.md` intact. It shares the same `-y`/`--no-*` flags and is safe to re-run (already-absent targets report `ok (not present)`).

## Risks / Trade-offs

- **Skill drifts from the actual tool descriptions** (in `internal/daemon/run.go`) → Mitigation: the skill's tool table is derived from those descriptions at write time, and the design names the source file so a future maintainer knows where the canonical descriptions live. A spec requirement pins the five tool names so a renamed tool surfaces as a spec mismatch.
- **Snippet always-loaded cost** → Mitigation: the snippet is opt-in (the skill alone satisfies the discoverability goal); its gate self-suppresses outside argus so it costs ~nothing in unrelated sessions.
- **Editing `~/.claude/CLAUDE.md` for a compile-pipeline user** → Mitigation: the `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` override symlinks into the snippet dir instead, so the append never touches a compiled CLAUDE.md.
- **Installer clobbering a user's real file** → Mitigation: D6 refuses to overwrite non-symlinks and reports them; the uninstaller only removes our own symlink/marker block, never a foreign file.
- **Gate false-negative inside a non-standard argus layout** → Mitigation: the gate accepts either the `~/.argus/worktrees/` path prefix or the `ARGUS_TASK_ID` env var, so an env-set sandbox still passes even if the path differs.

## Migration Plan

No migration. Pure additive: new `claude/` dir, new root install/uninstall scripts, new spec requirements. Rollback = run `uninstall-claude-skills.sh` (or delete the symlink and the CLAUDE.md marker block) and remove the `claude/` dir. No daemon or runtime impact.

## Acceptance criteria

**Skill content (skill requirement):**

- it should refuse to act and tell the agent to use the `plannotator` CLI directly when neither `~/.argus/worktrees/` cwd nor `ARGUS_TASK_ID` holds
- it should treat the sandbox as active if either condition holds
- it should document all five `mcp__argus__plannotator_*` tools with when-to-call guidance
- it should instruct the agent to poll `plannotator_session_result` until `status:"complete"` after every verb call
- it should list the direct `plannotator <verb>` Bash calls that EPERM in the sandbox and name the MCP tool to use instead
- it should describe how the plugin composes with `iris` and `hera`
- it should include at least two worked end-to-end workflows

**Snippet (snippet requirement):**

- it should carry `tags` and `audience` frontmatter
- it should open its body with the argus gate as the first content line

**Installer (installer requirement):**

- it should prompt `(Y/n)` before symlinking the skill and, on yes, symlink it into `~/.claude/skills/plannotator-argus`
- it should report `created` / `ok` / `relinked` / `SKIPPED` per target and be safe to re-run
- it should refuse to overwrite a real (non-symlink) file at a target path
- it should prompt `(Y/n)` before appending the snippet to `~/.claude/CLAUDE.md` and append between idempotency markers, replacing the block on re-run
- it should symlink the snippet into `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` instead of appending when that directory is provided
- it should resolve prompts to their default without hanging when stdin is not a TTY, and answer yes to both under `-y`/`--yes`

**Uninstaller (uninstaller requirement):**

- it should prompt `(Y/n)` before removing the skill symlink and remove it only when it points at this repo's source
- it should prompt `(Y/n)` before removing the appended snippet and remove only the marked block from `~/.claude/CLAUDE.md`
- it should report already-absent targets as `ok (not present)` and be safe to re-run

## Open Questions

None outstanding. Script names, prompt UX, and snippet-install strategy resolved during brainstorm and the follow-up instruction.
