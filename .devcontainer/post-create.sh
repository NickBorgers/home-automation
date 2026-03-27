#!/bin/bash
# Post-create setup script for devcontainer
# This script runs automatically when the devcontainer is created

set -e

echo "=== Setting up devcontainer ==="

# Fix DNS order to prioritize Tailscale MagicDNS
# (Also runs via postStartCommand on every container start)
echo "Checking DNS configuration..."
bash "$(dirname "$0")/fix-dns-order.sh"

# Compute repository root (parent of .devcontainer) so script works
# regardless of the local workspace folder name.
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "Setting up Go modules... (repo root: $REPO_ROOT)"
if [ -d "$REPO_ROOT/homeautomation-go" ]; then
    (cd "$REPO_ROOT/homeautomation-go" && go mod tidy && go mod download)
else
    echo "Warning: $REPO_ROOT/homeautomation-go not found; skipping Go module setup."
fi

# Git hooks
echo "Installing git hooks..."
if [ -d "$REPO_ROOT" ]; then
    (cd "$REPO_ROOT" && mkdir -p build)
    if [ -x "$REPO_ROOT/.githooks/install-hooks.sh" ] || [ -f "$REPO_ROOT/.githooks/install-hooks.sh" ]; then
        (cd "$REPO_ROOT" && bash .githooks/install-hooks.sh)
    else
        echo "Warning: .githooks/install-hooks.sh not found or not executable; skipping git hooks installation."
    fi
else
    echo "Warning: repository root $REPO_ROOT not found; skipping git hooks installation."
fi

# Set up gh CLI credentials from host token (written by initializeCommand)
REPO_TOKEN_FILE="$REPO_ROOT/.devcontainer/.gh-token"
if [ -s "$REPO_TOKEN_FILE" ]; then
    echo "Setting up GitHub CLI credentials..."
    GH_TOKEN=$(cat "$REPO_TOKEN_FILE")
    echo "$GH_TOKEN" | gh auth login --with-token 2>/dev/null && echo "gh auth configured." || echo "Warning: gh auth login failed."
    rm -f "$REPO_TOKEN_FILE"
else
    echo "Warning: No gh token found; gh CLI credentials not available."
fi

# Note: Claude Code and playwright-skill plugin are now pre-installed
# in the Dockerfile for faster rebuilds via Docker layer caching.

echo "=== Devcontainer setup complete ==="
