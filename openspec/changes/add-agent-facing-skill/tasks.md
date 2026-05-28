**Design doc:** `openspec/changes/add-agent-facing-skill/design.md`

## 1. Installer + uninstaller tests

- [ ] 1.1 Write a failing test harness (`test/install-claude-skills_test.sh` or equivalent) that runs `install-claude-skills.sh` against a temp `HOME` (non-TTY, decisions driven by `-y`/`--no-*` flags) and asserts the installer scenarios in `specs/plannotator-argus-plugin/spec.md`: clean install creates the skill symlink (`created`); re-run reports already-linked (`ok`); stale symlink is repointed (`relinked`); a real file is left untouched (`SKIPPED`); `--no-skill` skips the symlink; snippet-yes appends the marker block to `CLAUDE.md`; snippet re-run replaces the block (no duplicate); `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` symlinks the snippet instead of appending
- [ ] 1.2 Extend the harness for `uninstall-claude-skills.sh`: removes an owned skill symlink; leaves a foreign target untouched; strips the snippet marker block from `CLAUDE.md`; is idempotent when nothing is installed
- [ ] 1.3 Confirm the harness fails first (no scripts yet) – Prove-It

## 2. Skill artifact

**Depends on:** none

- [ ] 2.1 Create `claude/skills/plannotator-argus/SKILL.md` with frontmatter `name: plannotator-argus` and a `description` that leads with the argus-awareness gate
- [ ] 2.2 Body §1 – argus gate: cwd under `~/.argus/worktrees/` OR `ARGUS_TASK_ID` set; on failure, stop and use the `plannotator` CLI directly
- [ ] 2.3 Body §2 – what this is (daemon runs outside the sandbox; drive via MCP because direct `plannotator` writes EPERM)
- [ ] 2.4 Body §3 – the async session-poll pattern (verb → `{session_id, pending}` → poll `plannotator_session_result` until `complete`; may take minutes; `wait_seconds` max 25)
- [ ] 2.5 Body §4 – tool surface table for all five tools (when to call, required args, return shape), derived from the descriptions in `internal/daemon/run.go`
- [ ] 2.6 Body §5 – decision rules (annotate vs review vs last vs setup_goal; everything-waits-on session_result)
- [ ] 2.7 Body §6 – common Bash mistakes (direct `plannotator <verb>` EPERMs / is denied by the guard; git+PR ops belong to iris; always pass `cwd=$PWD`)
- [ ] 2.8 Body §7 – composition with `iris` (host-side git/PR) and `hera` (orchestration); plannotator-argus owns only the review/annotation UI seam
- [ ] 2.9 Body §8 – at least two worked workflows (review current branch; annotate a spec doc; iris-opens-PR → review the PR URL), each as an ordered tool-call sequence with the poll step shown
- [ ] 2.10 Markdown renders correctly (blank lines around headings/lists/code; en-dashes not em-dashes)

## 3. Snippet artifact

**Depends on:** none

- [ ] 3.1 Create `claude/snippets/plannotator-argus.md` with `tags` and `audience` frontmatter
- [ ] 3.2 First content line is the gate: "If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section."
- [ ] 3.3 Condensed orientation body (what the plugin is, the async poll rule, the don't-use-`Bash(plannotator …)` rule) – a tight always-loaded subset of the skill, markdown-formatted per the repo's rendering rules

## 4. Installer

**Depends on:** Stage 1

- [ ] 4.1 Write `install-claude-skills.sh` at repo root: resolve the repo's `claude/` dir from the script location; `set -euo pipefail`; parse `-y/--yes`, `--skill/--no-skill`, `--snippet/--no-snippet`, `--snippet-dir <path>`
- [ ] 4.2 Implement a `prompt_yn` helper: `--yes`/explicit flag wins; non-TTY resolves to the default; otherwise read `(Y/n)` with default yes
- [ ] 4.3 Implement a `link_target` helper with the D6 classification (created / ok / relinked / SKIPPED-not-a-symlink); never delete a real file
- [ ] 4.4 Prompt 1: symlink the skill `~/.claude/skills/plannotator-argus` → `claude/skills/plannotator-argus` (mkdir -p `~/.claude/skills` first) via `link_target`
- [ ] 4.5 Prompt 2: if `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` resolves to an existing dir, symlink the snippet there via `link_target` (skip the prompt); else prompt and, on yes, append the snippet to `~/.claude/CLAUDE.md` between `<!-- BEGIN plannotator-argus ... -->` / `<!-- END plannotator-argus -->` markers, replacing any existing block
- [ ] 4.6 Print a per-target changed/skipped summary; exit 0 on all benign cases; `chmod +x`

## 5. Uninstaller

**Depends on:** Stage 1

- [ ] 5.1 Write `uninstall-claude-skills.sh` at repo root sharing the `prompt_yn` helper and decision flags
- [ ] 5.2 Prompt to remove the skill symlink: remove only when it is a symlink pointing at this repo's `claude/skills/plannotator-argus`; report `removed` / `ok (not present)` / `not owned`
- [ ] 5.3 Prompt to remove the snippet: strip the marker block from `~/.claude/CLAUDE.md` (collapse surrounding blank lines); if a snippet-dir symlink exists, remove that instead; report results
- [ ] 5.4 Idempotent when nothing is installed; `chmod +x`
- [ ] 5.5 Run the Stage 1 harness against both scripts; iterate until green

## 6. Docs + verification

**Depends on:** Stage 2, Stage 3, Stage 4, Stage 5

- [ ] 6.1 Add a short "Agent-facing skill" section to `README.md` covering `install-claude-skills.sh` / `uninstall-claude-skills.sh`, the two `(Y/n)` prompts, and the `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` override
- [ ] 6.2 Run `install-claude-skills.sh -y` locally; verify `~/.claude/skills/plannotator-argus` resolves to the repo and re-running reports `ok`; verify uninstall reverses it
- [ ] 6.3 Test-drive: confirm a fresh argus-sandbox session reaches for the skill when asked "how do I review my branch with this plugin" (record the result)
- [ ] 6.4 `openspec validate add-agent-facing-skill --strict` passes
