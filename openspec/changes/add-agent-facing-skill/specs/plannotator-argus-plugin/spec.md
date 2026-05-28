## ADDED Requirements

### Requirement: Agent-facing discoverability skill

The plugin SHALL ship an installable skill at `claude/skills/plannotator-argus/SKILL.md` that teaches a fresh Claude session running inside an argus task sandbox how and when to use the `mcp__argus__plannotator_*` tools. The skill's frontmatter `description` SHALL lead with the argus-awareness gate so the model only surfaces the skill inside argus sandboxes, and its body SHALL open with that gate so the model can self-verify and bail when it does not hold.

#### Scenario: Skill self-gates and bails outside an argus sandbox

- **WHEN** the skill is read and neither the cwd is under `~/.argus/worktrees/` nor `ARGUS_TASK_ID` is set
- **THEN** the skill instructs the agent to stop and use the `plannotator` CLI/binary directly because the `mcp__argus__plannotator_*` tools are not registered outside an argus sandbox

#### Scenario: Skill recognizes either gate condition

- **WHEN** the skill's gate is evaluated
- **THEN** it treats the sandbox as active if EITHER the cwd is under `~/.argus/worktrees/` OR the `ARGUS_TASK_ID` environment variable is set

#### Scenario: Skill documents every plannotator MCP tool

- **WHEN** the skill body is read inside an argus sandbox
- **THEN** it documents each of `mcp__argus__plannotator_annotate`, `mcp__argus__plannotator_review`, `mcp__argus__plannotator_setup_goal`, `mcp__argus__plannotator_last`, and `mcp__argus__plannotator_session_result` with guidance on when to call it, not merely what it does

#### Scenario: Skill teaches the async session-poll pattern

- **WHEN** the skill describes how to use any verb-starter tool (`annotate`, `review`, `setup_goal`, `last`)
- **THEN** it states that the tool returns immediately with `{session_id, status:"pending"}` and that the agent MUST poll `mcp__argus__plannotator_session_result` with the returned `session_id` until `status` is `"complete"`, repeating the poll because a human annotating in a browser may take minutes

#### Scenario: Skill lists the Bash calls that fail in the sandbox

- **WHEN** the skill's common-mistakes guidance is read
- **THEN** it identifies that direct `plannotator annotate|review|last|setup-goal` Bash invocations EPERM inside the sandbox and names the corresponding `mcp__argus__plannotator_*` tool to use instead, and states that `cwd` must be passed as `$PWD`

#### Scenario: Skill explains sibling-plugin composition

- **WHEN** the skill's composition guidance is read
- **THEN** it describes how plannotator-argus composes with `iris` (host-side git/PR operations) and `hera` (multi-agent orchestration), making clear that plannotator-argus owns only the annotation/review UI seam

#### Scenario: Skill provides worked workflows

- **WHEN** the skill is read
- **THEN** it includes at least two worked end-to-end workflows expressed as ordered sequences of `mcp__argus__plannotator_*` tool calls

### Requirement: Optional always-loaded CLAUDE.md snippet

The plugin SHALL ship an optional CLAUDE.md snippet at `claude/snippets/plannotator-argus.md` for users who want plannotator-argus orientation always loaded into context rather than only when the model reaches for the skill. The snippet SHALL carry compile-pipeline-friendly frontmatter and SHALL self-gate so it is inert outside argus sandboxes.

#### Scenario: Snippet opens with the argus gate

- **WHEN** the snippet is read
- **THEN** its first content line after the frontmatter is a gate instructing the model to ignore the section when `ARGUS_TASK_ID` is unset and `$PWD` is not under `~/.argus/worktrees/`

#### Scenario: Snippet carries compile-pipeline frontmatter

- **WHEN** the snippet's frontmatter is read
- **THEN** it includes `tags` and `audience` fields so it slots into a `claude-rules/snippets/` compile pipeline as well as a plain `~/.claude/CLAUDE.md` append

### Requirement: Idempotent agent-facing artifact installer

The plugin SHALL ship an idempotent installer at `install-claude-skills.sh` in the repo root that prompts the user `(Y/n)` to symlink the skill into `~/.claude/skills/` and prompts `(Y/n)` to append the snippet to `~/.claude/CLAUDE.md`. The installer SHALL be safe to re-run and SHALL report what changed. A `-y`/`--yes` flag SHALL answer yes to both prompts, and when stdin is not a TTY each prompt SHALL resolve to its default rather than blocking.

#### Scenario: Installer symlinks the skill on a clean install

- **WHEN** `install-claude-skills.sh` runs, the skill prompt is answered yes, and `~/.claude/skills/plannotator-argus` does not exist
- **THEN** it creates a symlink `~/.claude/skills/plannotator-argus` pointing at the repo's `claude/skills/plannotator-argus` directory and reports it as `created`

#### Scenario: Installer is idempotent on re-run

- **WHEN** `install-claude-skills.sh` runs with the skill prompt answered yes and `~/.claude/skills/plannotator-argus` is already a symlink to the repo's skill directory
- **THEN** it leaves the symlink in place and reports it as already linked rather than erroring

#### Scenario: Installer repoints a stale symlink

- **WHEN** `install-claude-skills.sh` runs with the skill prompt answered yes and `~/.claude/skills/plannotator-argus` is a symlink pointing at a different source
- **THEN** it repoints the symlink at the repo's skill directory and reports it as relinked

#### Scenario: Installer refuses to clobber a real file

- **WHEN** `install-claude-skills.sh` runs with the skill prompt answered yes and a real (non-symlink) file or directory already occupies `~/.claude/skills/plannotator-argus`
- **THEN** it leaves the existing path untouched and reports it as skipped because it is not a symlink

#### Scenario: Skill prompt answered no skips the symlink

- **WHEN** `install-claude-skills.sh` runs and the skill prompt is answered no (e.g. `--no-skill`)
- **THEN** it does not create or modify `~/.claude/skills/plannotator-argus`

#### Scenario: Installer appends the snippet to CLAUDE.md

- **WHEN** `install-claude-skills.sh` runs, the snippet prompt is answered yes, no snippet directory is configured, and `~/.claude/CLAUDE.md` does not already contain the snippet's marker block
- **THEN** it appends the snippet content to `~/.claude/CLAUDE.md` wrapped in `BEGIN`/`END` plannotator-argus markers

#### Scenario: Snippet append is idempotent on re-run

- **WHEN** `install-claude-skills.sh` runs with the snippet prompt answered yes and `~/.claude/CLAUDE.md` already contains the snippet's marker block
- **THEN** it replaces the content between the markers rather than appending a duplicate block

#### Scenario: Installer symlinks the snippet when a snippet dir is provided

- **WHEN** `install-claude-skills.sh` runs with `--snippet-dir <path>` or with `$CLAUDE_SNIPPETS_DIR` set to an existing directory
- **THEN** it symlinks `claude/snippets/plannotator-argus.md` into that directory using the same idempotent classification as the skill and does not append to `~/.claude/CLAUDE.md`

### Requirement: Agent-facing artifact uninstaller

The plugin SHALL ship an uninstaller at `uninstall-claude-skills.sh` in the repo root that reverses `install-claude-skills.sh`. It SHALL prompt `(Y/n)` to remove the skill symlink and `(Y/n)` to remove the appended snippet block, SHALL only remove artifacts it owns, and SHALL be safe to re-run. It SHALL accept the same `-y`/`--yes` and `--no-*` decision flags as the installer.

#### Scenario: Uninstaller removes the skill symlink it owns

- **WHEN** `uninstall-claude-skills.sh` runs with the skill prompt answered yes and `~/.claude/skills/plannotator-argus` is a symlink pointing at this repo's skill directory
- **THEN** it removes the symlink and reports it as removed

#### Scenario: Uninstaller leaves a foreign target untouched

- **WHEN** `uninstall-claude-skills.sh` runs and `~/.claude/skills/plannotator-argus` is a real file/directory or a symlink pointing at a different source
- **THEN** it leaves the path untouched and reports that it was not removed because it is not owned by this installer

#### Scenario: Uninstaller strips the snippet block from CLAUDE.md

- **WHEN** `uninstall-claude-skills.sh` runs with the snippet prompt answered yes and `~/.claude/CLAUDE.md` contains the snippet's marker block
- **THEN** it removes the content between and including the markers, leaving the rest of `~/.claude/CLAUDE.md` intact

#### Scenario: Uninstaller is idempotent when nothing is installed

- **WHEN** `uninstall-claude-skills.sh` runs and neither the skill symlink nor the snippet marker block is present
- **THEN** it reports each target as not present and exits without error
