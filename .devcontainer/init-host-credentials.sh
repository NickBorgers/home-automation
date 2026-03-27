#!/bin/bash
# Runs on the HOST before the devcontainer starts.
# Extracts credentials from host keychain/CLI and writes them to files
# that postCreateCommand will consume inside the container.
# All token files are gitignored and deleted after use.

set -e
cd "$(dirname "$0")"

# GitHub CLI token
gh auth token > .gh-token 2>/dev/null || true

# Claude Code OAuth token (macOS keychain → extract access token)
if command -v security &>/dev/null; then
    security find-generic-password -s "Claude Code-credentials" -w 2>/dev/null \
        | python3 -c "import json,sys; print(json.load(sys.stdin)['claudeAiOauth']['accessToken'])" \
        > .claude-token 2>/dev/null || true
fi
