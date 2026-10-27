#!/bin/bash
# Persist this project's Claude Code state -- memory, sessions, plans -- on the
# host. Runs on every container start (postStartCommand), because the bind
# mount in the first case below does not survive a restart.
#
# devcontainer.json mounts the host ~/.claude/projects writable at
# $CLAUDE_HOST_PROJECTS_DIR. Claude Code names each project directory after the
# working directory, and the repo is mounted at its host path, so both sides
# derive the same name.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if [ -z "${CLAUDE_HOST_PROJECTS_DIR:-}" ] || [ ! -d "$CLAUDE_HOST_PROJECTS_DIR" ]; then
    exit 0
fi

project_slug=$(printf '%s' "$REPO_ROOT" | sed 's/[^a-zA-Z0-9]/-/g')
project_link="$HOME/.claude/projects/$project_slug"
host_dir="$CLAUDE_HOST_PROJECTS_DIR/$project_slug"
mkdir -p "$HOME/.claude/projects" "$host_dir"

# Carry anything Claude Code wrote locally before the host copy was wired up,
# keeping the host's copy of any file that exists on both sides.
carry_local_state() {
    cp -rn "$project_link/." "$host_dir/" 2>/dev/null || true
}

# Field 5 of mountinfo is the mount point. Exact match: a mount *under* the
# link (the memory mount below) must not count.
is_mount_point() {
    awk -v p="$1" '$5 == p { found = 1 } END { exit !found }' /proc/self/mountinfo
}

if awk -v p="$project_link/" 'index($5, p) == 1 { found = 1 } END { exit !found }' /proc/self/mountinfo; then
    # The launcher (dcr/dcs in util/profile) already bind-mounts this project's
    # memory directory, writable, under the link path. A mount point cannot be
    # replaced by a symlink, so a symlink would leave sessions in the
    # container's own filesystem, lost when dcr recreates it. Bind the whole
    # host directory over the path instead. Its memory/ subdirectory is the
    # source of that memory mount, so the shadowed mount shows the same files.
    if is_mount_point "$project_link"; then
        echo "Project state at $project_link is already bound to the host; nothing to do"
    else
        carry_local_state
        sudo mount --bind "$host_dir" "$project_link"
        echo "Bound project state $project_link to the writable host mount"
    fi
else
    if [ -d "$project_link" ] && [ ! -L "$project_link" ]; then
        carry_local_state
        rm -rf "$project_link"
    fi
    ln -sfn "$host_dir" "$project_link"
    echo "Linked project state $project_link to the writable host mount"
fi
