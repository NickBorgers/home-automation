#!/bin/bash
# Post-create setup script for devcontainer
# This script runs automatically when the devcontainer is created

set -e

echo "=== Setting up devcontainer ==="

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
claude plugin marketplace add lackeyjb/playwright-skill
claude plugin install playwright-skill@playwright-skill

echo "Setting up Playwright (installing browser)..."
cd ~/.claude/plugins/marketplaces/playwright-skill/skills/playwright-skill
npm run setup

echo "=== Devcontainer setup complete ==="
echo "Note: Restart Claude Code to load the playwright-skill plugin"
