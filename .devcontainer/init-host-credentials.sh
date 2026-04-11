#!/bin/bash
# Runs on the HOST before the devcontainer starts.
# Extracts credentials from host CLI and writes them to files
# that postCreateCommand will consume inside the container.
# All token files are gitignored and deleted after use.

set -e
cd "$(dirname "$0")"

# Ensure every devcontainer.json bind-mount source exists before Docker
# tries to resolve it. Missing bind-mount sources are dangerous: Docker
# silently materializes them as root-owned directories on the host (on
# Linux) or errors out at container start (on macOS / Docker Desktop).
# Creating user-owned placeholders here is a cheap no-op when the files
# already exist, and guarantees the devcontainer can spin up on any
# contributor's machine. post-create.sh uses `-s` (non-empty) guards
# before wiring anything in, so empty placeholders are installed but
# never activated.
[ -e "$HOME/.claude"           ] || mkdir -p "$HOME/.claude"
[ -e "$HOME/.bashrc"           ] || touch    "$HOME/.bashrc"
[ -d "$HOME/code/util"         ] || mkdir -p "$HOME/code/util"
[ -e "$HOME/code/util/profile" ] || touch    "$HOME/code/util/profile"

# GitHub CLI token
gh auth token > .gh-token 2>/dev/null || true

# Claude Code credentials and config
CLAUDE_CREDS_FILE=".claude-credentials"
rm -f "$CLAUDE_CREDS_FILE"
CLAUDE_CREDS="$HOME/.claude/.credentials.json"
if [ -f "$CLAUDE_CREDS" ]; then
    cp "$CLAUDE_CREDS" "$CLAUDE_CREDS_FILE"
    chmod 600 "$CLAUDE_CREDS_FILE"
    echo "[init-host-credentials] Claude Code credentials captured."
else
    echo "[init-host-credentials] No Claude Code credentials found — skipping."
fi

CLAUDE_CONFIG_FILE=".claude-config"
rm -f "$CLAUDE_CONFIG_FILE"
CLAUDE_CONFIG="$HOME/.claude.json"
if [ -f "$CLAUDE_CONFIG" ]; then
    cp "$CLAUDE_CONFIG" "$CLAUDE_CONFIG_FILE"
    chmod 600 "$CLAUDE_CONFIG_FILE"
    echo "[init-host-credentials] Claude Code config captured."
else
    echo "[init-host-credentials] No Claude Code config found — skipping."
fi
