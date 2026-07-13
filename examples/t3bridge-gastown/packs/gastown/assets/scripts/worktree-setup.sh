#!/bin/sh
# worktree-setup.sh — idempotent git worktree creation for Gas City agents.
#
# Usage: worktree-setup.sh <rig-root> <target-dir> <agent-name> [--sync]
#
# Ensures the target directory is a git worktree of the rig repo.
# Called from pre_start in pack configs.

set -eu

RIG_ROOT="${1:?usage: worktree-setup.sh <rig-root> <target-dir> <agent-name> [--sync]}"
WT="${2:?missing target-dir}"
AGENT="${3:?missing agent-name}"
SYNC="${4:-}"

# Pre-flight: refuse to create worktrees when RIG_ROOT's git context resolves
# outside the rig. Without a .git pointer file in the rig root, git walks up
# to an enclosing repository (e.g. the city root) and every worktree created
# here registers in the wrong repo — wrong branches, wrong pushes, cross-rig
# leakage (gt-4rn / gs-v4m).
# Resolve symlinks: on macOS /var/folders/ is a symlink to /private/var/folders/,
# so cd -P is needed for the pattern match to work against git's resolved GITDIR.
RIG_ROOT_REAL=$(cd -P "$RIG_ROOT" 2>/dev/null && pwd) || RIG_ROOT_REAL="$RIG_ROOT"
GITDIR=$(git -C "$RIG_ROOT" rev-parse --absolute-git-dir 2>/dev/null) || {
    echo "worktree-setup: $RIG_ROOT is not a git repository" >&2
    exit 1
}
case "$GITDIR" in
    "$RIG_ROOT_REAL"/*) : ;;  # .repo.git or .git inside the rig — OK
    *)
        echo "worktree-setup: REFUSING to create worktree for $RIG_ROOT:" >&2
        echo "  its git dir resolves OUTSIDE the rig ($GITDIR)." >&2
        echo "  Likely a missing .git pointer file in the rig root (gt-4rn)." >&2
        exit 1
        ;;
esac

# Capture the rig root's origin URL for the post-creation guard below.
RIG_ORIGIN=$(git -C "$RIG_ROOT" remote get-url origin 2>/dev/null) || RIG_ORIGIN=""

mkdir -p "$(dirname "$WT")"

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
    if [ "$SYNC" = "--sync" ] && [ -n "$RIG_ORIGIN" ]; then
        git -C "$WT" fetch origin 2>/dev/null || true
        git -C "$WT" pull --rebase 2>/dev/null || true
    fi
    exit 0
fi

rmdir "$WT" 2>/dev/null || true

branch="gc/${AGENT}"
if ! git -C "$RIG_ROOT" worktree add -B "$branch" "$WT" HEAD; then
    echo "worktree-setup: failed to create worktree at $WT from $RIG_ROOT" >&2
    exit 1
fi

# Post-creation origin guard: the new worktree's origin must match the rig
# root's origin. A mismatch means the worktree was registered in the wrong
# repo (e.g., because a parent-repo walk slipped past the pre-flight check).
if [ -n "$RIG_ORIGIN" ]; then
    WT_ORIGIN=$(git -C "$WT" remote get-url origin 2>/dev/null) || WT_ORIGIN=""
    if [ "$WT_ORIGIN" != "$RIG_ORIGIN" ]; then
        echo "worktree-setup: CRITICAL: worktree origin ($WT_ORIGIN) != rig origin ($RIG_ORIGIN)" >&2
        echo "  Worktree was registered in the wrong repo. Removing." >&2
        git -C "$RIG_ROOT" worktree remove --force "$WT" 2>/dev/null || rm -rf "$WT"
        exit 1
    fi
fi

exit 0
