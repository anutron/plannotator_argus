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

The plugin SHALL ship an idempotent installer at `install-claude-skill.sh` in the repo root that symlinks the skill into the user's `~/.claude/skills/` directory and offers to install the snippet. The installer SHALL be safe to re-run and SHALL report what changed.

#### Scenario: Installer symlinks the skill on a clean install

- **WHEN** `install-claude-skill.sh` runs and `~/.claude/skills/plannotator-argus` does not exist
- **THEN** it creates a symlink `~/.claude/skills/plannotator-argus` pointing at the repo's `claude/skills/plannotator-argus` directory and reports it as `created`

#### Scenario: Installer is idempotent on re-run

- **WHEN** `install-claude-skill.sh` runs and `~/.claude/skills/plannotator-argus` is already a symlink to the repo's skill directory
- **THEN** it leaves the symlink in place and reports it as already linked rather than erroring

#### Scenario: Installer repoints a stale symlink

- **WHEN** `install-claude-skill.sh` runs and `~/.claude/skills/plannotator-argus` is a symlink pointing at a different source
- **THEN** it repoints the symlink at the repo's skill directory and reports it as relinked

#### Scenario: Installer refuses to clobber a real file

- **WHEN** `install-claude-skill.sh` runs and a real (non-symlink) file or directory already occupies `~/.claude/skills/plannotator-argus`
- **THEN** it leaves the existing path untouched and reports it as skipped because it is not a symlink

#### Scenario: Installer symlinks the snippet when a snippet dir is provided

- **WHEN** `install-claude-skill.sh` runs with `--snippet-dir <path>` or with `$CLAUDE_SNIPPETS_DIR` set to an existing directory
- **THEN** it symlinks `claude/snippets/plannotator-argus.md` into that directory using the same idempotent classification as the skill

#### Scenario: Installer prints the snippet path when no snippet dir is provided

- **WHEN** `install-claude-skill.sh` runs without `--snippet-dir` and without `$CLAUDE_SNIPPETS_DIR`
- **THEN** it prints the snippet's absolute path and a one-line instruction for wiring it in manually, without failing
