#!/bin/bash
# Runs on the HOST before the devcontainer starts.
# Extracts credentials from host CLI and writes them to files
# that postCreateCommand will consume inside the container.
# All token files are gitignored and deleted after use.

set -e
cd "$(dirname "$0")"

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
