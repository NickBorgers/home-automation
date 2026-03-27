#!/bin/bash
# Refreshes credentials inside the container from host-provided token files.
# Called by postStartCommand on EVERY container start (not just creation).
# initializeCommand writes fresh tokens from the host before this runs.

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Refresh gh CLI credentials
GH_TOKEN_FILE="$REPO_ROOT/.devcontainer/.gh-token"
if [ -s "$GH_TOKEN_FILE" ]; then
    echo "$( cat "$GH_TOKEN_FILE" )" | gh auth login --with-token 2>/dev/null \
        && echo "gh auth refreshed." || echo "Warning: gh auth refresh failed."
    rm -f "$GH_TOKEN_FILE"
fi

# Refresh Claude Code credentials
CLAUDE_CRED_FILE="$REPO_ROOT/.devcontainer/.claude-credentials"
if [ -s "$CLAUDE_CRED_FILE" ]; then
    mkdir -p "$HOME/.claude"
    cp "$CLAUDE_CRED_FILE" "$HOME/.claude/.credentials.json"
    chmod 600 "$HOME/.claude/.credentials.json"
    echo "Claude Code credentials refreshed."
    rm -f "$CLAUDE_CRED_FILE"
fi
