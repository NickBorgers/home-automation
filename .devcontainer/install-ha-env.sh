#!/bin/bash
#
# Runs INSIDE the container. Installs the .env staged by stage-ha-env.sh into
# homeautomation-go/.env when that file is missing. Never overwrites an
# existing .env. Safe to call repeatedly, and after homeautomation-go is
# recreated. Same script in home-automation and home-automation-plus-security.
set -e

WORKSPACE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAGED="$WORKSPACE/.devcontainer/.ha-env"
TARGET="$WORKSPACE/homeautomation-go/.env"

[ -d "$WORKSPACE/homeautomation-go" ] || exit 0
[ ! -e "$TARGET" ] || exit 0
if [ ! -s "$STAGED" ]; then
    echo "[install-ha-env] No staged .env; copy homeautomation-go/.env.example to .env if you need to run the app."
    exit 0
fi

install -m 600 "$STAGED" "$TARGET"
echo "[install-ha-env] Installed homeautomation-go/.env."
