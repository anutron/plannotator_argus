#!/bin/bash
# Test harness for install-claude-skills.sh and uninstall-claude-skills.sh
# Run: bash test/install-claude-skills_test.sh
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SCRIPT="$REPO_ROOT/install-claude-skills.sh"
UNINSTALL_SCRIPT="$REPO_ROOT/uninstall-claude-skills.sh"

PASS=0
FAIL=0
FAILURES=()

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

pass() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
fail() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); FAILURES+=("$1"); }

assert_symlink_target() {
    local label="$1" dst="$2" expected_target="$3"
    if [ ! -L "$dst" ]; then
        fail "$label – expected symlink at $dst but not found"
        return
    fi
    local actual
    actual="$(readlink "$dst")"
    if [ "$actual" = "$expected_target" ]; then
        pass "$label"
    else
        fail "$label – symlink target was '$actual', expected '$expected_target'"
    fi
}

assert_file_absent() {
    local label="$1" path="$2"
    if [ -e "$path" ] || [ -L "$path" ]; then
        fail "$label – expected $path to be absent but it exists"
    else
        pass "$label"
    fi
}

assert_file_contains() {
    local label="$1" file="$2" needle="$3"
    if grep -qF "$needle" "$file" 2>/dev/null; then
        pass "$label"
    else
        fail "$label – expected '$needle' in $file"
    fi
}

assert_file_not_contains() {
    local label="$1" file="$2" needle="$3"
    if grep -qF "$needle" "$file" 2>/dev/null; then
        fail "$label – did not expect '$needle' in $file"
    else
        pass "$label"
    fi
}

assert_output_contains() {
    local label="$1" output="$2" needle="$3"
    if echo "$output" | grep -qF "$needle"; then
        pass "$label"
    else
        fail "$label – expected '$needle' in output; got: $output"
    fi
}

# Make a fresh temp home with stub skill + snippet fixtures
make_temp_home() {
    local tmp
    tmp="$(mktemp -d)"
    mkdir -p "$tmp/.claude/skills"
    # Create stub skill dir and snippet so tests don't depend solely on sibling output
    mkdir -p "$tmp/stubs/claude/skills/plannotator-argus"
    echo "stub skill" > "$tmp/stubs/claude/skills/plannotator-argus/SKILL.md"
    mkdir -p "$tmp/stubs/claude/snippets"
    echo "stub snippet" > "$tmp/stubs/claude/snippets/plannotator-argus.md"
    echo "$tmp"
}

# The real repo src paths (populated by sibling agents, or stubs created at test init)
SKILL_SRC="$REPO_ROOT/claude/skills/plannotator-argus"
SNIPPET_SRC="$REPO_ROOT/claude/snippets/plannotator-argus.md"

# Verify scripts exist — if not, all tests will fail expectedly (TDD red phase)
if [ ! -f "$INSTALL_SCRIPT" ]; then
    echo "NOTE: $INSTALL_SCRIPT does not exist yet (expected in TDD red phase)"
fi
if [ ! -f "$UNINSTALL_SCRIPT" ]; then
    echo "NOTE: $UNINSTALL_SCRIPT does not exist yet (expected in TDD red phase)"
fi

echo ""
echo "=== install-claude-skills.sh tests ==="

# ---------------------------------------------------------------------------
# TEST 1: Installer creates skill symlink on clean install
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 1: clean install creates skill symlink"
T="$(make_temp_home)"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
assert_symlink_target "T1: skill symlink created" \
    "$T/.claude/skills/plannotator-argus" \
    "$SKILL_SRC"
assert_output_contains "T1: reports 'created'" "$OUTPUT" "created"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 2: Installer is idempotent – already linked correct target
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 2: idempotent re-run – already linked"
T="$(make_temp_home)"
ln -s "$SKILL_SRC" "$T/.claude/skills/plannotator-argus"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
assert_symlink_target "T2: symlink unchanged" \
    "$T/.claude/skills/plannotator-argus" \
    "$SKILL_SRC"
assert_output_contains "T2: reports 'already linked'" "$OUTPUT" "already linked"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 3: Installer repoints a stale symlink
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 3: stale symlink gets repointed"
T="$(make_temp_home)"
ln -s "/some/other/path" "$T/.claude/skills/plannotator-argus"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
assert_symlink_target "T3: symlink repointed" \
    "$T/.claude/skills/plannotator-argus" \
    "$SKILL_SRC"
assert_output_contains "T3: reports 'relinked'" "$OUTPUT" "relinked"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 4: Installer refuses to clobber a real file/dir
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 4: real file at target – skipped"
T="$(make_temp_home)"
mkdir -p "$T/.claude/skills/plannotator-argus"  # real dir, not a symlink
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
if [ -d "$T/.claude/skills/plannotator-argus" ] && [ ! -L "$T/.claude/skills/plannotator-argus" ]; then
    pass "T4: real dir left in place"
else
    fail "T4: expected real dir to remain untouched"
fi
assert_output_contains "T4: reports 'SKIPPED'" "$OUTPUT" "SKIPPED"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 5: --no-skill skips skill symlink entirely
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 5: --no-skill skips symlink"
T="$(make_temp_home)"
HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --no-snippet 2>&1 || true
assert_file_absent "T5: no symlink created" "$T/.claude/skills/plannotator-argus"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 6: Installer appends snippet to CLAUDE.md (no existing CLAUDE.md)
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 6: snippet appended to new CLAUDE.md"
T="$(make_temp_home)"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet 2>&1)" || true
assert_file_contains "T6: CLAUDE.md has BEGIN marker" \
    "$T/.claude/CLAUDE.md" \
    "<!-- BEGIN plannotator-argus"
assert_file_contains "T6: CLAUDE.md has END marker" \
    "$T/.claude/CLAUDE.md" \
    "<!-- END plannotator-argus -->"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 7: Snippet append is idempotent – re-run replaces, no duplicate
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 7: snippet re-run replaces, no duplicate"
T="$(make_temp_home)"
HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet 2>&1 || true
HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet 2>&1 || true
COUNT="$(grep -c "BEGIN plannotator-argus" "$T/.claude/CLAUDE.md" 2>/dev/null || echo 0)"
if [ "$COUNT" = "1" ]; then
    pass "T7: exactly one BEGIN marker (no duplicate)"
else
    fail "T7: expected exactly 1 BEGIN marker, found $COUNT"
fi
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 8: --snippet-dir symlinks instead of appending to CLAUDE.md
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 8: --snippet-dir creates symlink, not CLAUDE.md append"
T="$(make_temp_home)"
SNIP_DIR="$T/my-snippets"
mkdir -p "$SNIP_DIR"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet-dir "$SNIP_DIR" 2>&1)" || true
assert_symlink_target "T8: snippet symlinked into dir" \
    "$SNIP_DIR/plannotator-argus.md" \
    "$SNIPPET_SRC"
assert_file_absent "T8: no CLAUDE.md created" "$T/.claude/CLAUDE.md"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 9: CLAUDE_SNIPPETS_DIR env var symlinks instead of appending
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 9: CLAUDE_SNIPPETS_DIR env var symlinks snippet"
T="$(make_temp_home)"
SNIP_DIR="$T/env-snippets"
mkdir -p "$SNIP_DIR"
OUTPUT="$(HOME="$T" CLAUDE_SNIPPETS_DIR="$SNIP_DIR" bash "$INSTALL_SCRIPT" --no-skill 2>&1)" || true
assert_symlink_target "T9: snippet symlinked via env var" \
    "$SNIP_DIR/plannotator-argus.md" \
    "$SNIPPET_SRC"
assert_file_absent "T9: no CLAUDE.md created" "$T/.claude/CLAUDE.md"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 10: Non-TTY stdin uses default (yes) without hanging
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 10: non-TTY stdin uses default (yes)"
T="$(make_temp_home)"
OUTPUT="$(echo "" | HOME="$T" bash "$INSTALL_SCRIPT" 2>&1)" || true
# Should have installed skill (default yes) and attempted snippet (default yes)
if [ -L "$T/.claude/skills/plannotator-argus" ] || echo "$OUTPUT" | grep -qiE "(created|already linked|relinked|SKIPPED)"; then
    pass "T10: completed without hanging (skill action found)"
else
    fail "T10: non-TTY run did not complete or produced no skill action; output: $OUTPUT"
fi
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 11: --snippet-dir with already-linked snippet (idempotent)
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 11: --snippet-dir idempotent (already linked)"
T="$(make_temp_home)"
SNIP_DIR="$T/my-snippets"
mkdir -p "$SNIP_DIR"
ln -s "$SNIPPET_SRC" "$SNIP_DIR/plannotator-argus.md"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet-dir "$SNIP_DIR" 2>&1)" || true
assert_symlink_target "T11: symlink unchanged" \
    "$SNIP_DIR/plannotator-argus.md" \
    "$SNIPPET_SRC"
assert_output_contains "T11: reports 'already linked'" "$OUTPUT" "already linked"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 12: --snippet-dir repoints stale snippet symlink
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 12: --snippet-dir repoints stale symlink"
T="$(make_temp_home)"
SNIP_DIR="$T/my-snippets"
mkdir -p "$SNIP_DIR"
ln -s "/some/other/snippet.md" "$SNIP_DIR/plannotator-argus.md"
OUTPUT="$(HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet-dir "$SNIP_DIR" 2>&1)" || true
assert_symlink_target "T12: snippet repointed" \
    "$SNIP_DIR/plannotator-argus.md" \
    "$SNIPPET_SRC"
assert_output_contains "T12: reports 'relinked'" "$OUTPUT" "relinked"
rm -rf "$T"

echo ""
echo "=== uninstall-claude-skills.sh tests ==="

# ---------------------------------------------------------------------------
# TEST 13: Uninstaller removes owned skill symlink
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 13: uninstall removes owned skill symlink"
T="$(make_temp_home)"
ln -s "$SKILL_SRC" "$T/.claude/skills/plannotator-argus"
OUTPUT="$(HOME="$T" bash "$UNINSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
assert_file_absent "T13: symlink removed" "$T/.claude/skills/plannotator-argus"
assert_output_contains "T13: reports 'removed'" "$OUTPUT" "removed"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 14: Uninstaller leaves foreign symlink untouched
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 14: foreign symlink not removed"
T="$(make_temp_home)"
ln -s "/some/foreign/path" "$T/.claude/skills/plannotator-argus"
OUTPUT="$(HOME="$T" bash "$UNINSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
if [ -L "$T/.claude/skills/plannotator-argus" ]; then
    pass "T14: foreign symlink left in place"
else
    fail "T14: foreign symlink was removed but shouldn't have been"
fi
assert_output_contains "T14: reports 'not owned'" "$OUTPUT" "not owned"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 15: Uninstaller leaves real dir untouched (not owned)
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 15: real dir at skill path not removed"
T="$(make_temp_home)"
mkdir -p "$T/.claude/skills/plannotator-argus"
OUTPUT="$(HOME="$T" bash "$UNINSTALL_SCRIPT" --yes --no-snippet 2>&1)" || true
if [ -d "$T/.claude/skills/plannotator-argus" ] && [ ! -L "$T/.claude/skills/plannotator-argus" ]; then
    pass "T15: real dir left in place"
else
    fail "T15: real dir was removed but shouldn't have been"
fi
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 16: Uninstaller strips snippet block from CLAUDE.md
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 16: snippet block stripped from CLAUDE.md"
T="$(make_temp_home)"
mkdir -p "$T/.claude"
cat > "$T/.claude/CLAUDE.md" <<'HEREDOC'
# My config

Some existing content.

<!-- BEGIN plannotator-argus (managed by install-claude-skills.sh) -->
stub snippet
<!-- END plannotator-argus -->

More existing content.
HEREDOC
OUTPUT="$(HOME="$T" bash "$UNINSTALL_SCRIPT" --no-skill --snippet 2>&1)" || true
assert_file_not_contains "T16: BEGIN marker removed" \
    "$T/.claude/CLAUDE.md" \
    "BEGIN plannotator-argus"
assert_file_contains "T16: other content preserved" \
    "$T/.claude/CLAUDE.md" \
    "Some existing content."
assert_file_contains "T16: trailing content preserved" \
    "$T/.claude/CLAUDE.md" \
    "More existing content."
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 17: Uninstaller is idempotent – nothing installed
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 17: uninstall idempotent – nothing installed"
T="$(make_temp_home)"
OUTPUT="$(HOME="$T" bash "$UNINSTALL_SCRIPT" --yes 2>&1)" || EXIT=$?
if [ "${EXIT:-0}" -eq 0 ]; then
    pass "T17: exits 0 when nothing to remove"
else
    fail "T17: exited non-zero ($EXIT) when nothing installed"
fi
assert_output_contains "T17: reports 'not present'" "$OUTPUT" "not present"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 18: Uninstaller strips snippet but leaves surrounding blank lines tidy
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 18: no excess blank lines after snippet strip"
T="$(make_temp_home)"
mkdir -p "$T/.claude"
printf "Line before.\n\n<!-- BEGIN plannotator-argus (managed by install-claude-skills.sh) -->\nstub snippet\n<!-- END plannotator-argus -->\n\nLine after.\n" \
    > "$T/.claude/CLAUDE.md"
HOME="$T" bash "$UNINSTALL_SCRIPT" --no-skill --snippet 2>&1 || true
# Should not have 3+ consecutive blank lines
if awk '/^$/{blanks++; if(blanks>2) exit 1} /[^ ]/{blanks=0}' "$T/.claude/CLAUDE.md"; then
    pass "T18: no excess blank lines"
else
    fail "T18: excess blank lines after snippet removal"
fi
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 19: Uninstaller removes snippet-dir symlink when provided
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 19: uninstall removes snippet-dir owned symlink"
T="$(make_temp_home)"
SNIP_DIR="$T/my-snippets"
mkdir -p "$SNIP_DIR"
ln -s "$SNIPPET_SRC" "$SNIP_DIR/plannotator-argus.md"
OUTPUT="$(HOME="$T" bash "$UNINSTALL_SCRIPT" --no-skill --snippet --snippet-dir "$SNIP_DIR" 2>&1)" || true
assert_file_absent "T19: snippet-dir symlink removed" "$SNIP_DIR/plannotator-argus.md"
assert_output_contains "T19: reports 'removed'" "$OUTPUT" "removed"
rm -rf "$T"

# ---------------------------------------------------------------------------
# TEST 20: Installer appends to existing CLAUDE.md (preserves existing content)
# ---------------------------------------------------------------------------
echo ""
echo "-- TEST 20: snippet appended after existing CLAUDE.md content"
T="$(make_temp_home)"
mkdir -p "$T/.claude"
echo "# Existing content" > "$T/.claude/CLAUDE.md"
HOME="$T" bash "$INSTALL_SCRIPT" --no-skill --snippet 2>&1 || true
assert_file_contains "T20: existing content preserved" \
    "$T/.claude/CLAUDE.md" \
    "# Existing content"
assert_file_contains "T20: BEGIN marker appended" \
    "$T/.claude/CLAUDE.md" \
    "<!-- BEGIN plannotator-argus"
rm -rf "$T"

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo "================================================================"
echo "Results: $PASS passed, $FAIL failed"
if [ ${#FAILURES[@]} -gt 0 ]; then
    echo ""
    echo "Failed tests:"
    for f in "${FAILURES[@]}"; do
        echo "  - $f"
    done
fi
echo "================================================================"

[ "$FAIL" -eq 0 ]
