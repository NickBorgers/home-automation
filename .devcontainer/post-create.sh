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

# Claude Code playwright-skill plugin
echo "Installing playwright-skill plugin..."

# Workaround for EXDEV error: /tmp and /home/vscode are on different filesystems
# in devcontainers. Setting TMPDIR to a path on the same filesystem fixes
# the "cross-device link not permitted" error during plugin installation.
export TMPDIR="$HOME/.claude/tmp"
mkdir -p "$TMPDIR"

claude plugin marketplace add lackeyjb/playwright-skill
claude plugin install playwright-skill@playwright-skill

# Note: Playwright browser dependencies (apt packages) are now installed
# in the Dockerfile for faster rebuilds via Docker layer caching.

echo "Setting up Playwright (installing browser)..."
cd ~/.claude/plugins/marketplaces/playwright-skill/skills/playwright-skill
npm run setup

echo "=== Devcontainer setup complete ==="
