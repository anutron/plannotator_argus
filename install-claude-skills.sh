#!/bin/bash
# Install agent-facing Claude skills and snippets for plannotator-argus.
# Idempotent: safe to re-run. Reports what changed.
#
# Usage:
#   ./install-claude-skills.sh              # interactive (Y/n prompts)
#   ./install-claude-skills.sh -y           # yes to all prompts
#   ./install-claude-skills.sh --no-skill   # skip skill symlink
#   ./install-claude-skills.sh --no-snippet # skip snippet install
#   ./install-claude-skills.sh --snippet-dir <path>  # symlink snippet into dir
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
# Prints question, returns 0 for yes, 1 for no.
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
        # stdin is not a TTY – use default
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
        "")
            [ "$default" = "y" ] && return 0 || return 1
            ;;
        *)
            [ "$default" = "y" ] && return 0 || return 1
            ;;
    esac
}

# ---------------------------------------------------------------------------
# link_target <src> <dst>
# Creates or updates a symlink from dst -> src using idempotent classification.
# ---------------------------------------------------------------------------
link_target() {
    local src="$1" dst="$2"
    if [ -L "$dst" ]; then
        local current
        current="$(readlink "$dst")"
        if [ "$current" = "$src" ]; then
            echo "  ok (already linked): $dst"
        else
            rm "$dst"
            ln -s "$src" "$dst"
            echo "  relinked: $dst"
        fi
    elif [ -e "$dst" ]; then
        echo "  SKIPPED (exists, not a symlink): $dst"
    else
        local parent
        parent="$(dirname "$dst")"
        mkdir -p "$parent"
        ln -s "$src" "$dst"
        echo "  created: $dst"
    fi
}

# ---------------------------------------------------------------------------
# install_snippet_to_claude_md
# Appends (or replaces) the plannotator-argus block in ~/.claude/CLAUDE.md
# ---------------------------------------------------------------------------
install_snippet_to_claude_md() {
    local claude_md="$HOME/.claude/CLAUDE.md"
    local snippet_content
    snippet_content="$(cat "$SNIPPET_SRC")"

    mkdir -p "$HOME/.claude"

    if [ -f "$claude_md" ] && grep -qF "$BEGIN_MARKER" "$claude_md"; then
        # Replace existing block using awk (BSD-compatible, no temp file in place)
        # Write snippet to a temp file to avoid awk -v newline limitations
        local tmp snip_tmp
        tmp="${claude_md}.tmp.$$"
        snip_tmp="${claude_md}.snip.$$"
        cat "$SNIPPET_SRC" > "$snip_tmp"
        awk -v begin="$BEGIN_MARKER" -v end="$END_MARKER" -v snip_file="$snip_tmp" '
            BEGIN { in_block=0 }
            $0 == begin {
                in_block=1
                print begin
                while ((getline line < snip_file) > 0) print line
                close(snip_file)
                next
            }
            in_block && $0 == end {
                in_block=0
                print end
                next
            }
            in_block { next }
            { print }
        ' "$claude_md" > "$tmp" && mv "$tmp" "$claude_md"
        rm -f "$snip_tmp"
        echo "  updated block in: $claude_md"
    else
        # Append new block
        printf "\n%s\n%s\n%s\n" "$BEGIN_MARKER" "$snippet_content" "$END_MARKER" >> "$claude_md"
        echo "  appended to: $claude_md"
    fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
echo "plannotator-argus skill installer"
echo ""

# -- Skill --
if prompt_yn "Symlink the plannotator-argus skill into ~/.claude/skills/?" "y" "$DO_SKILL"; then
    echo "skill:"
    link_target "$SKILL_SRC" "$HOME/.claude/skills/plannotator-argus"
else
    echo "skill: skipped"
fi

echo ""

# -- Snippet --
if [ -n "$SNIPPET_DIR" ] && [ -d "$SNIPPET_DIR" ]; then
    # Snippet-dir mode: symlink into the directory, skip CLAUDE.md prompt
    echo "snippet (via snippet-dir: $SNIPPET_DIR):"
    link_target "$SNIPPET_SRC" "$SNIPPET_DIR/plannotator-argus.md"
elif prompt_yn "Append the plannotator-argus snippet to ~/.claude/CLAUDE.md?" "y" "$DO_SNIPPET"; then
    echo "snippet:"
    install_snippet_to_claude_md
else
    echo "snippet: skipped"
fi

echo ""
echo "done."
