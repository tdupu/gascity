#!/bin/sh
# worktree-setup.sh — idempotent git worktree creation for Gas City agents.
#
# Usage: worktree-setup.sh <rig-root> <target-dir> <agent-name> [--sync]
#
# Ensures the target directory is a git worktree of the rig repo. For
# backward compatibility, the older <repo-dir> <agent-name> <city-root>
# signature still works and resolves the target under
# <city-root>/.gc/worktrees/<rig>/<agent-name>.
#
# Called from pre_start in pack configs. Runs before the session is created
# so the agent starts IN the worktree directory.

set -eu

RIG_ROOT="${1:?usage: worktree-setup.sh <rig-root> <target-dir> <agent-name> [--sync]}"
ARG2="${2:?missing target-dir}"
ARG3="${3:?missing agent-name}"

is_path_like() {
    # Legacy mode passes the city path as arg 3. Agent names are validated
    # elsewhere and are not expected to look like filesystem paths.
    case "$1" in
        */*|.*|*:*|*\\*) return 0 ;;
        *) return 1 ;;
    esac
}

if is_path_like "$ARG3"; then
    AGENT="$ARG2"
    CITY="$ARG3"
    RIG=$(basename "$RIG_ROOT")
    WT="$CITY/.gc/worktrees/$RIG/$AGENT"
    SYNC="${4:-}"
else
    WT="$ARG2"
    AGENT="$ARG3"
    SYNC="${4:-}"
fi

# Spawn-time guard: refuse to create worktrees when RIG_ROOT's git context
# resolves outside the rig root. Without a .git pointer file at the rig root,
# git discovery walks up to the enclosing city repo, and every worktree
# registered here binds to the wrong origin — wrong branches, wrong pushes,
# and cross-rig remote leakage (gt-4rn / gs-v4m).
# Resolve symlinks: on macOS /var/folders/ is a symlink to /private/var/folders/,
# so cd -P is needed for the pattern match to work against git's resolved GITDIR.
RIG_ROOT_REAL=$(cd -P "$RIG_ROOT" 2>/dev/null && pwd) || RIG_ROOT_REAL="$RIG_ROOT"
GITDIR=$(git -C "$RIG_ROOT" rev-parse --absolute-git-dir 2>/dev/null) || {
    echo "worktree-setup: $RIG_ROOT is not a git repository" >&2
    exit 1
}
case "$GITDIR" in
    "$RIG_ROOT_REAL"/*) : ;;  # .repo.git or .git is inside the rig — OK
    *)
        echo "worktree-setup: REFUSING to create worktree for $RIG_ROOT:" >&2
        echo "  git dir resolves OUTSIDE the rig root ($GITDIR)." >&2
        echo "  Ensure a .git pointer file exists at $RIG_ROOT (gt-4rn)." >&2
        exit 1
        ;;
esac

# Verify the rig root has an origin remote. A rig whose origin URL would
# come from a parent-repo walk (caught above) or from a bare city checkout
# risks push leakage; a non-empty URL here confirms we have the right repo.
RIG_ORIGIN=$(git -C "$RIG_ROOT" remote get-url origin 2>/dev/null) || {
    echo "worktree-setup: WARNING: $RIG_ROOT has no 'origin' remote — push from worktree will fail" >&2
}

# Idempotent: skip if worktree already exists.
if [ -d "$WT/.git" ] || [ -f "$WT/.git" ]; then
    # Idempotency origin guard: an existing worktree must be registered with
    # the same repo as the rig root. A stale session-home worktree from before
    # the .git pointer was set up could be a city-repo worktree — allowing it
    # would cause per-bead commits to land in the wrong remote (gs-xzr).
    if [ -n "$RIG_ORIGIN" ]; then
        WT_ORIGIN=$(git -C "$WT" remote get-url origin 2>/dev/null) || WT_ORIGIN=""
        if [ "$WT_ORIGIN" != "$RIG_ORIGIN" ]; then
            echo "worktree-setup: REFUSING to reuse $WT:" >&2
            echo "  existing worktree origin ($WT_ORIGIN) != rig origin ($RIG_ORIGIN)." >&2
            echo "  Remove this worktree and let gc recreate it: git -C $RIG_ROOT worktree remove --force $WT" >&2
            exit 1
        fi
    fi
    [ "$SYNC" = "--sync" ] && { git -C "$WT" fetch origin 2>/dev/null; git -C "$WT" pull --rebase 2>/dev/null || true; }
    exit 0
fi

mkdir -p "$(dirname "$WT")"

STAGE=""

merge_stage_entry() {
    SRC="$1"
    DST="$2"

    if [ -d "$SRC" ]; then
        mkdir -p "$DST"
        for ENTRY in "$SRC"/.[!.]* "$SRC"/..?* "$SRC"/*; do
            [ -e "$ENTRY" ] || continue
            merge_stage_entry "$ENTRY" "$DST/$(basename "$ENTRY")"
        done
        rmdir "$SRC" 2>/dev/null || true
        return 0
    fi

    if [ -e "$DST" ]; then
        return 0
    fi
    mv "$SRC" "$DST"
}

restore_stage() {
    [ -n "$STAGE" ] || return 0
    mkdir -p "$WT"
    for ENTRY in "$STAGE"/.[!.]* "$STAGE"/..?* "$STAGE"/*; do
        [ -e "$ENTRY" ] || continue
        merge_stage_entry "$ENTRY" "$WT/$(basename "$ENTRY")"
    done
    rmdir "$STAGE" 2>/dev/null || true
    STAGE=""
}

if [ -d "$WT" ] && [ "$(find "$WT" -mindepth 1 -maxdepth 1 | head -n 1)" ]; then
    STAGE=$(mktemp -d "$(dirname "$WT")/.gascity-worktree-stage.XXXXXX")
    find "$WT" -mindepth 1 -maxdepth 1 -exec mv {} "$STAGE"/ \;
    trap 'restore_stage' EXIT HUP INT TERM
fi

rmdir "$WT" 2>/dev/null || true
BRANCH="gc-$AGENT"
if git -C "$RIG_ROOT" show-ref --verify --quiet "refs/heads/$BRANCH"; then
    if ! GIT_LFS_SKIP_SMUDGE=1 git -C "$RIG_ROOT" worktree add "$WT" "$BRANCH"; then
        echo "worktree-setup: failed to create worktree at $WT from $RIG_ROOT (branch gc-$AGENT)" >&2
        restore_stage
        exit 1
    fi
else
    if ! GIT_LFS_SKIP_SMUDGE=1 git -C "$RIG_ROOT" worktree add "$WT" -b "$BRANCH"; then
        echo "worktree-setup: failed to create worktree at $WT from $RIG_ROOT (branch gc-$AGENT)" >&2
        restore_stage
        exit 1
    fi
fi

if [ -n "$STAGE" ]; then
    for ENTRY in "$STAGE"/.[!.]* "$STAGE"/..?* "$STAGE"/*; do
        [ -e "$ENTRY" ] || continue
        merge_stage_entry "$ENTRY" "$WT/$(basename "$ENTRY")"
    done
    rm -rf "$STAGE"
    STAGE=""
fi
trap - EXIT HUP INT TERM

# Post-creation origin guard: the new worktree's origin must match the rig
# root's origin. A mismatch means the worktree was registered in the wrong
# repo (e.g., because a parent-repo walk slipped past the pre-flight check).
if [ -n "$RIG_ORIGIN" ]; then
    WT_ORIGIN=$(git -C "$WT" remote get-url origin 2>/dev/null) || true
    if [ "$WT_ORIGIN" != "$RIG_ORIGIN" ]; then
        echo "worktree-setup: CRITICAL: worktree origin ($WT_ORIGIN) != rig origin ($RIG_ORIGIN)" >&2
        echo "  Worktree was registered in the wrong repo. Removing." >&2
        git -C "$RIG_ROOT" worktree remove --force "$WT" 2>/dev/null || rm -rf "$WT"
        restore_stage
        exit 1
    fi
fi

# Bead redirect for filesystem beads.
mkdir -p "$WT/.beads"
echo "$RIG_ROOT/.beads" > "$WT/.beads/redirect"

# Submodule init (best-effort).
git -C "$WT" submodule init 2>/dev/null || true

# Keep runtime ignores local to git metadata instead of mutating the tracked
# repository .gitignore.
EXCLUDE=$(git -C "$WT" rev-parse --git-path info/exclude)
case "$EXCLUDE" in
    /*) ;;
    *) EXCLUDE="$WT/$EXCLUDE" ;;
esac
mkdir -p "$(dirname "$EXCLUDE")"
touch "$EXCLUDE"

MARKER="# Gas City worktree infrastructure (local excludes)"
if ! grep -qF "$MARKER" "$EXCLUDE" 2>/dev/null; then
    if [ -s "$EXCLUDE" ] && [ "$(tail -c 1 "$EXCLUDE" 2>/dev/null || true)" != "" ]; then
        printf '\n' >> "$EXCLUDE"
    fi
    printf '%s\n' "$MARKER" >> "$EXCLUDE"
fi

append_exclude() {
    PATTERN="$1"
    grep -qxF "$PATTERN" "$EXCLUDE" 2>/dev/null || printf '%s\n' "$PATTERN" >> "$EXCLUDE"
}

append_exclude ".beads/redirect"
append_exclude ".beads/hooks/"
append_exclude ".beads/formulas/"
append_exclude ".logs/"
append_exclude "worktrees/"
append_exclude "__pycache__/"
append_exclude ".claude/"
append_exclude ".codex/"
append_exclude ".gemini/"
append_exclude ".opencode/"
append_exclude ".github/hooks/"
append_exclude ".github/copilot-instructions.md"
append_exclude "state.json"

# Optional sync.
[ "$SYNC" = "--sync" ] && { git -C "$WT" fetch origin 2>/dev/null; git -C "$WT" pull --rebase 2>/dev/null || true; }

exit 0
