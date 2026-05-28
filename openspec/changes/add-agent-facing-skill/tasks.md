**Design doc:** `openspec/changes/add-agent-facing-skill/design.md`

## 1. Installer tests

- [ ] 1.1 Write a failing test harness (`install-claude-skill_test.sh` or equivalent under `test/`) that runs `install-claude-skill.sh` against a temp `HOME` and asserts each scenario in `specs/plannotator-argus-plugin/spec.md`: clean install creates the skill symlink (`created`), re-run reports already-linked (`ok`), a stale symlink is repointed (`relinked`), a real file is left untouched (`SKIPPED`), `--snippet-dir`/`$CLAUDE_SNIPPETS_DIR` symlinks the snippet, and absent both prints the snippet path without failing
- [ ] 1.2 Confirm the harness fails first (no installer yet) — Prove-It

## 2. Skill artifact

**Depends on:** none

- [ ] 2.1 Create `claude/skills/plannotator-argus/SKILL.md` with frontmatter `name: plannotator-argus` and a `description` that leads with the argus-awareness gate
- [ ] 2.2 Body §1 — argus gate: cwd under `~/.argus/worktrees/` OR `ARGUS_TASK_ID` set; on failure, stop and use the `plannotator` CLI directly
- [ ] 2.3 Body §2 — what this is (daemon runs outside the sandbox; drive via MCP because direct `plannotator` writes EPERM)
- [ ] 2.4 Body §3 — the async session-poll pattern (verb → `{session_id, pending}` → poll `plannotator_session_result` until `complete`; may take minutes; `wait_seconds` max 25)
- [ ] 2.5 Body §4 — tool surface table for all five tools (when to call, required args, return shape), derived from the descriptions in `internal/daemon/run.go`
- [ ] 2.6 Body §5 — decision rules (annotate vs review vs last vs setup_goal; everything-waits-on session_result)
- [ ] 2.7 Body §6 — common Bash mistakes (direct `plannotator <verb>` EPERMs / is denied by the guard; git+PR ops belong to iris; always pass `cwd=$PWD`)
- [ ] 2.8 Body §7 — composition with `iris` (host-side git/PR) and `hera` (orchestration); plannotator-argus owns only the review/annotation UI seam
- [ ] 2.9 Body §8 — at least two worked workflows (review current branch; annotate a spec doc; iris-opens-PR → review the PR URL), each as an ordered tool-call sequence with the poll step shown

## 3. Snippet artifact

**Depends on:** none

- [ ] 3.1 Create `claude/snippets/plannotator-argus.md` with `tags` and `audience` frontmatter
- [ ] 3.2 First content line is the gate: "If `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`, ignore this section."
- [ ] 3.3 Condensed orientation body (what the plugin is, the async poll rule, the don't-use-`Bash(plannotator …)` rule) — a tight always-loaded subset of the skill, markdown-formatted per the repo's rendering rules

## 4. Installer

**Depends on:** Stage 1

- [ ] 4.1 Write `install-claude-skill.sh` at repo root: resolve the repo's `claude/` dir from the script location; `set -euo pipefail`
- [ ] 4.2 Implement a `link_target` helper with the D6 classification (created / ok / relinked / SKIPPED-not-a-symlink); never delete a real file
- [ ] 4.3 Symlink the skill: `~/.claude/skills/plannotator-argus` → `claude/skills/plannotator-argus` (mkdir -p `~/.claude/skills` first)
- [ ] 4.4 Snippet handling: if `--snippet-dir <path>` or `$CLAUDE_SNIPPETS_DIR` resolves to an existing dir, symlink the snippet there via the same helper; else print the snippet's absolute path + one-line wiring instruction
- [ ] 4.5 Print a per-target changed/skipped summary; exit 0 on all benign cases; `chmod +x`
- [ ] 4.6 Run the Stage 1 harness; iterate until green

## 5. Docs + verification

**Depends on:** Stage 2, Stage 3, Stage 4

- [ ] 5.1 Add a short "Agent-facing skill" section to `README.md` pointing at `install-claude-skill.sh` and explaining the snippet env-var/flag options
- [ ] 5.2 Run `install-claude-skill.sh` locally; verify `~/.claude/skills/plannotator-argus` resolves to the repo and re-running reports `ok`
- [ ] 5.3 Test-drive: confirm a fresh argus-sandbox session reaches for the skill when asked "how do I review my branch with this plugin" (record the result)
- [ ] 5.4 `openspec validate add-agent-facing-skill --strict` passes
