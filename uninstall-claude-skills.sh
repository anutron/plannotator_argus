#!/bin/bash
# Uninstall agent-facing Claude skills and snippets for plannotator-argus.
# Only removes artifacts owned by this installer. Idempotent: safe to re-run.
#
# Usage:
#   ./uninstall-claude-skills.sh              # interactive (Y/n prompts)
#   ./uninstall-claude-skills.sh -y           # yes to all prompts
#   ./uninstall-claude-skills.sh --no-skill   # skip skill removal
#   ./uninstall-claude-skills.sh --no-snippet # skip snippet removal
#   ./uninstall-claude-skills.sh --snippet-dir <path>  # remove snippet symlink from dir
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="$SCRIPT_DIR"

SKILL_SRC="$REPO/claude/skills/plannotator-argus"
SNIPPET_SRC="$REPO/claude/snippets/plannotator-argus.md"

BEGIN_MARKER="<!-- BEGIN plannotator-argus (managed by install-claude-skills.sh) -->"
END_MARKER="<!-- END plannotator-argus -->"

# ---------------------------------------------------------------------------
# Parse flags
# ---------------------------------------------------------------------------
ASSUME_YES=0
DO_SKILL=""        # ""=ask, "yes"=yes, "no"=no
DO_SNIPPET=""      # ""=ask, "yes"=yes, "no"=no
SNIPPET_DIR=""

while [ $# -gt 0 ]; do
    case "$1" in
        -y|--yes)        ASSUME_YES=1 ;;
        --skill)         DO_SKILL="yes" ;;
        --no-skill)      DO_SKILL="no" ;;
        --snippet)       DO_SNIPPET="yes" ;;
        --no-snippet)    DO_SNIPPET="no" ;;
        --snippet-dir)   SNIPPET_DIR="$2"; shift ;;
        --snippet-dir=*) SNIPPET_DIR="${1#*=}" ;;
        *) echo "unknown option: $1" >&2; exit 1 ;;
    esac
    shift
done

# Honor $CLAUDE_SNIPPETS_DIR env var if --snippet-dir not given
if [ -z "$SNIPPET_DIR" ] && [ -n "${CLAUDE_SNIPPETS_DIR:-}" ] && [ -d "${CLAUDE_SNIPPETS_DIR}" ]; then
    SNIPPET_DIR="$CLAUDE_SNIPPETS_DIR"
fi

# ---------------------------------------------------------------------------
# prompt_yn <question> <default: y|n> [decision_override: yes|no|""]
# ---------------------------------------------------------------------------
prompt_yn() {
    local question="$1" default="$2" decision="${3:-}"
    if [ -n "$decision" ]; then
        [ "$decision" = "yes" ] && return 0 || return 1
    fi
    if [ "$ASSUME_YES" -eq 1 ]; then
        return 0
    fi
    if [ ! -t 0 ]; then
        [ "$default" = "y" ] && return 0 || return 1
    fi
    local prompt_str
    if [ "$default" = "y" ]; then
        prompt_str="$question (Y/n) "
    else
        prompt_str="$question (y/N) "
    fi
    printf "%s" "$prompt_str"
    local answer
    read -r answer
    answer="$(echo "$answer" | tr '[:upper:]' '[:lower:]')"
    case "$answer" in
        y|yes) return 0 ;;
        n|no)  return 1 ;;
        "")    [ "$default" = "y" ] && return 0 || return 1 ;;
        *)     [ "$default" = "y" ] && return 0 || return 1 ;;
    esac
}

# ---------------------------------------------------------------------------
# remove_owned_symlink <dst> <expected_src> <label>
# Removes symlink at dst only if it points at expected_src.
# ---------------------------------------------------------------------------
remove_owned_symlink() {
    local dst="$1" expected_src="$2" label="$3"
    if [ ! -e "$dst" ] && [ ! -L "$dst" ]; then
        echo "  ok (not present): $dst"
        return
    fi
    if [ -L "$dst" ]; then
        local current
        current="$(readlink "$dst")"
        if [ "$current" = "$expected_src" ]; then
            rm "$dst"
            echo "  removed: $dst"
        else
            echo "  not owned (left in place): $dst – points at '$current', not this repo's $label"
        fi
    else
        echo "  not owned (left in place): $dst – is a real file/directory, not a symlink"
    fi
}

# ---------------------------------------------------------------------------
# remove_snippet_from_claude_md
# Strips the BEGIN..END plannotator-argus block from ~/.claude/CLAUDE.md,
# collapsing extra blank lines left behind.
# ---------------------------------------------------------------------------
remove_snippet_from_claude_md() {
    local claude_md="$HOME/.claude/CLAUDE.md"

    if [ ! -f "$claude_md" ]; then
        echo "  ok (not present): $claude_md has no marker block"
        return
    fi

    if ! grep -qF "$BEGIN_MARKER" "$claude_md"; then
        echo "  ok (not present): no plannotator-argus block in $claude_md"
        return
    fi

    local tmp
    tmp="${claude_md}.tmp.$$"

    # Remove the block and collapse runs of 3+ blank lines into 2
    awk -v begin="$BEGIN_MARKER" -v end="$END_MARKER" '
        BEGIN { in_block=0; blank_run=0 }
        $0 == begin { in_block=1; next }
        in_block && $0 == end { in_block=0; next }
        in_block { next }
        /^[[:space:]]*$/ {
            blank_run++
            if (blank_run <= 2) print
            next
        }
        { blank_run=0; print }
    ' "$claude_md" > "$tmp"

    # Trim trailing blank lines from end of file
    awk 'NF{found=NR; line[NR]=$0} /^[[:space:]]*$/{line[NR]=$0}
         END{ for(i=1;i<=NR;i++) { if(i<=found || i<=NR && line[i] !~ /^[[:space:]]*$/) print line[i] } }
    ' "$tmp" > "${tmp}2"

    mv "${tmp}2" "$claude_md"
    rm -f "$tmp"

    echo "  removed block from: $claude_md"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
echo "plannotator-argus skill uninstaller"
echo ""

# -- Skill --
if prompt_yn "Remove the plannotator-argus skill symlink from ~/.claude/skills/?" "y" "$DO_SKILL"; then
    echo "skill:"
    remove_owned_symlink "$HOME/.claude/skills/plannotator-argus" "$SKILL_SRC" "skill"
else
    echo "skill: skipped"
fi

echo ""

# -- Snippet --
if prompt_yn "Remove the plannotator-argus snippet?" "y" "$DO_SNIPPET"; then
    echo "snippet:"
    # If a snippet-dir is configured, remove from there
    if [ -n "$SNIPPET_DIR" ]; then
        remove_owned_symlink "$SNIPPET_DIR/plannotator-argus.md" "$SNIPPET_SRC" "snippet"
    fi
    # Also remove from CLAUDE.md if present
    remove_snippet_from_claude_md
else
    echo "snippet: skipped"
fi

echo ""
echo "done."
