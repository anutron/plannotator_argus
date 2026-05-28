## Why

A Claude session inside an argus task worktree sees the plannotator-argus tools only as bare `mcp__argus__plannotator_*` names and one-line descriptions – it doesn't know it's sandboxed, that the verb tools are async (poll for the result), that direct `plannotator <verb>` calls EPERM, or how the plugin composes with sibling plugins. The daemon, the five MCP tools, and the runtime bash-guard already exist; what's missing is a proactive orientation layer the model can read before it makes the wrong call.

## What Changes

- Add `claude/skills/plannotator-argus/SKILL.md` – agent-facing skill whose `description` leads with the argus-awareness gate, covering tool surface, the async session-poll pattern, annotate-vs-review decision rules, sibling-plugin composition (`iris`, `hera`), common Bash mistakes, and worked workflows.
- Add `claude/snippets/plannotator-argus.md` – optional always-loaded CLAUDE.md fragment with `tags`/`audience` frontmatter whose first content line is the argus gate.
- Add `install-claude-skills.sh` at repo root – idempotent installer that prompts `(Y/n)` to symlink the skill into `~/.claude/skills/` and prompts `(Y/n)` to append the snippet to `~/.claude/CLAUDE.md` (between idempotency markers); `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` symlinks the snippet there instead. Reports `created`/`ok`/`relinked`/`SKIPPED` per target.
- Add `uninstall-claude-skills.sh` at repo root – reverses the installer: prompts `(Y/n)` to remove the skill symlink (only if it points at this repo) and to strip the appended snippet block from `~/.claude/CLAUDE.md`.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `plannotator-argus-plugin`: adds requirements for the agent-facing discoverability skill, the optional CLAUDE.md snippet, the idempotent skill installer, and the companion uninstaller.

## Impact

- **New files**: `claude/skills/plannotator-argus/SKILL.md`, `claude/snippets/plannotator-argus.md`, `install-claude-skills.sh`, `uninstall-claude-skills.sh`.
- **No code changes**: no Go, no daemon, no MCP surface, no argus-core changes.
- **No conflict** with the in-flight `plannotator-mcp-bash-guard` change – that change owns the runtime `PreToolUse(Bash)` enforcement; this change adds the proactive teaching layer. The skill's "common Bash mistakes" section references the guard's behavior but does not re-implement it.
- **Install footprint**: one symlink at `~/.claude/skills/plannotator-argus`, plus an optional snippet append to `~/.claude/CLAUDE.md` (or a snippet-dir symlink). No daemon or settings.json changes.
