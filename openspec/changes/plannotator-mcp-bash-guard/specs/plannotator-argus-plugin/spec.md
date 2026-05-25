## ADDED Requirements

### Requirement: Bash invocation guard for argus sandboxes

The plugin SHALL ship a `PreToolUse(Bash)` hook script at `~/.local/bin/plannotator-bash-guard` that intercepts direct `plannotator <verb>` invocations inside argus task worktrees and instructs Claude to use the corresponding `mcp__argus__plannotator_*` tool instead. The hook SHALL be a no-op for any Bash invocation outside an argus task worktree, so installing it globally does not break direct `plannotator` use on the host shell.

#### Scenario: Direct annotate call inside an argus worktree is denied with a redirect

- **WHEN** Claude's Bash tool is about to run `plannotator annotate <path>` and `$PWD` starts with `${HOME}/.argus/worktrees/`
- **THEN** the hook exits 2 with a JSON document on stderr whose `hookSpecificOutput.permissionDecision` is `"deny"` and whose `systemMessage` names `mcp__argus__plannotator_annotate`, maps the `path` argument to the tool's `path` parameter, and instructs Claude to poll `mcp__argus__plannotator_session_result(cwd=$PWD, session_id=<id>)` for the result

#### Scenario: Direct review call inside an argus worktree is denied with a redirect

- **WHEN** Claude's Bash tool is about to run `plannotator review` (with or without `--git` or a `pr_url`) and `$PWD` starts with `${HOME}/.argus/worktrees/`
- **THEN** the hook exits 2 with a deny JSON whose `systemMessage` names `mcp__argus__plannotator_review` and documents that `pr_url` and `git=true` are optional pass-through arguments

#### Scenario: Direct last call inside an argus worktree is denied with a redirect

- **WHEN** Claude's Bash tool is about to run `plannotator last` and `$PWD` starts with `${HOME}/.argus/worktrees/`
- **THEN** the hook exits 2 with a deny JSON whose `systemMessage` names `mcp__argus__plannotator_last` and supplies the `cwd=$PWD` arg

#### Scenario: Direct setup-goal call inside an argus worktree is denied with a redirect

- **WHEN** Claude's Bash tool is about to run `plannotator setup-goal <mode> <bundle_path>` and `$PWD` starts with `${HOME}/.argus/worktrees/`
- **THEN** the hook exits 2 with a deny JSON whose `systemMessage` names `mcp__argus__plannotator_setup_goal` and maps `mode` (interview|facts) and `bundle_path` to the tool's parameters

#### Scenario: Compound shell invocation is intercepted

- **WHEN** Claude's Bash tool is about to run a compound command such as `cd foo && plannotator annotate bar` and `$PWD` starts with `${HOME}/.argus/worktrees/`
- **THEN** the hook matches the embedded `plannotator annotate` and exits 2 with the deny JSON

#### Scenario: Absolute-path invocation is intercepted

- **WHEN** Claude's Bash tool is about to run a command with the binary's absolute path, e.g. `/usr/local/bin/plannotator annotate foo`, and `$PWD` starts with `${HOME}/.argus/worktrees/`
- **THEN** the hook matches because the regex allows `/` as a leading delimiter, and exits 2 with the deny JSON

#### Scenario: Verb invocation outside an argus worktree passes through

- **WHEN** Claude's Bash tool is about to run `plannotator annotate <path>` and `$PWD` is not under `${HOME}/.argus/worktrees/`
- **THEN** the hook exits 0 with empty stdout/stderr and the Bash tool proceeds as if the hook were not installed

#### Scenario: Daemon CLI is not intercepted

- **WHEN** Claude's Bash tool is about to run `plannotator-argus status` (or any other `plannotator-argus` subcommand) regardless of `$PWD`
- **THEN** the hook exits 0 and does not emit a deny JSON, because the regex requires whitespace (not a hyphen) between `plannotator` and the verb

#### Scenario: Hook wrapper is not intercepted

- **WHEN** Claude's Bash tool is about to run `plannotator-hook < /tmp/payload` (or any other invocation of the ExitPlanMode wrapper) regardless of `$PWD`
- **THEN** the hook exits 0 and does not emit a deny JSON

#### Scenario: Non-verb plannotator invocations are not intercepted

- **WHEN** Claude's Bash tool is about to run `plannotator --version`, `plannotator --help`, or bare `plannotator` regardless of `$PWD`
- **THEN** the hook exits 0 because no verb token matches the regex

#### Scenario: Empty or malformed hook payload allows the call

- **WHEN** the hook receives empty stdin or a payload that jq cannot parse
- **THEN** the hook exits 0 with empty stdout/stderr (fail-open), so a broken or unexpected payload never bricks Bash invocations
