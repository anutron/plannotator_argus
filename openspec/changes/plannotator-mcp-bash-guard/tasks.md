**Design doc:** `openspec/changes/plannotator-mcp-bash-guard/design.md`

**Spec deltas:** `openspec/changes/plannotator-mcp-bash-guard/specs/plannotator-argus-plugin/spec.md`

## 1. Hook script

- [x] 1.1 Write `deploy/plannotator-bash-guard.sh`
- [x] 1.2 Read `tool_input.command` from stdin via `jq -r '.tool_input.command // empty'`; allow on empty payload
- [x] 1.3 Anchored regex match on `(^|[[:space:]/])plannotator[[:space:]]+(annotate|review|last|setup-goal)([[:space:]]|$)`; capture verb
- [x] 1.4 Allow if `$PWD` is not under `${HOME}/.argus/worktrees/`
- [x] 1.5 Build verb-specific redirect message (MCP tool name + arg mapping + polling reminder)
- [x] 1.6 Emit deny JSON via `jq -nc --arg reason ...` to stderr; exit 2
- [x] 1.7 `chmod 0755`

## 2. Deploy wiring

- [x] 2.1 `deploy/install.sh` copies `plannotator-bash-guard.sh` → `~/.local/bin/plannotator-bash-guard`
- [x] 2.2 `deploy/uninstall.sh` removes `~/.local/bin/plannotator-bash-guard`

## 3. Documentation

- [x] 3.1 README section "Forcing the MCP path for plannotator inside argus" with the `~/.claude/settings.json` stanza
- [x] 3.2 Document non-matching cases (`--version`, `plannotator-argus`, `plannotator-hook`) so future-Aaron knows the regex is intentional

## 4. Verification

- [x] 4.1 Outside argus, `plannotator annotate foo` → allow
- [x] 4.2 Inside argus, `plannotator annotate foo` → deny with annotate-specific redirect
- [x] 4.3 Inside argus, the other three verbs (`review`, `last`, `setup-goal`) → deny with verb-specific redirects
- [x] 4.4 Inside argus, `plannotator --version`, `plannotator-argus status`, `plannotator-hook < /tmp/x` → allow
- [x] 4.5 Inside argus, compound `cd foo && plannotator annotate bar` → deny
- [x] 4.6 Inside argus, absolute path `/usr/local/bin/plannotator annotate foo` → deny
- [x] 4.7 Malformed JSON / empty payload on stdin → allow (fail-open)
- [ ] 4.8 End-to-end: install the settings.json stanza in a fresh argus task; ask Claude to run `/plannotator-annotate <path>`; observe the deny + redirect, then the successful MCP call

## 5. OpenSpec hygiene

- [x] 5.1 Scaffold `openspec/changes/plannotator-mcp-bash-guard/` with proposal, design, tasks, and delta spec
- [ ] 5.2 `openspec validate plannotator-mcp-bash-guard --strict` passes
- [ ] 5.3 Commit + open PR off branch `mcp-bash-guard`
- [ ] 5.4 After merge + manual smoke (4.8), run `openspec archive plannotator-mcp-bash-guard`
