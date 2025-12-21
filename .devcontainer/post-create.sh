#!/bin/bash
# Post-create setup script for devcontainer
# This script runs automatically when the devcontainer is created

set -e

echo "=== Setting up devcontainer ==="

# Fix DNS order to prioritize Tailscale MagicDNS
# (Also runs via postStartCommand on every container start)
echo "Checking DNS configuration..."
bash "$(dirname "$0")/fix-dns-order.sh"

# Go module setup
echo "Setting up Go modules..."
cd /workspaces/home-automation/homeautomation-go
go mod tidy
go mod download

# Git hooks
echo "Installing git hooks..."
cd /workspaces/home-automation
mkdir -p build
bash .githooks/install-hooks.sh

# Claude Code playwright-skill plugin
echo "Installing playwright-skill plugin..."

# Workaround for EXDEV error: /tmp and /home/vscode are on different filesystems
# in devcontainers. Setting TMPDIR to a path on the same filesystem fixes
# the "cross-device link not permitted" error during plugin installation.
export TMPDIR="$HOME/.claude/tmp"
mkdir -p "$TMPDIR"

claude plugin marketplace add lackeyjb/playwright-skill
claude plugin install playwright-skill@playwright-skill

echo "Installing Playwright browser dependencies..."
# Install system dependencies required for Chromium browser
# Using explicit apt packages instead of 'npx playwright install-deps' to avoid
# supply chain risks from downloading and executing npm packages with npx
sudo apt-get update
sudo apt-get install -y --no-install-recommends \
    libglib2.0-0 libnss3 libnspr4 libdbus-1-3 libatk1.0-0 \
    libatk-bridge2.0-0 libcups2 libdrm2 libxkbcommon0 libatspi2.0-0 \
    libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2

echo "Setting up Playwright (installing browser)..."
cd ~/.claude/plugins/marketplaces/playwright-skill/skills/playwright-skill
npm run setup

echo "=== Devcontainer setup complete ==="
