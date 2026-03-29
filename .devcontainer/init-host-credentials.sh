#!/bin/bash
# Runs on the HOST before the devcontainer starts.
# Extracts credentials from host CLI and writes them to files
# that postCreateCommand will consume inside the container.
# All token files are gitignored and deleted after use.

set -e
cd "$(dirname "$0")"

# GitHub CLI token
gh auth token > .gh-token 2>/dev/null || true
