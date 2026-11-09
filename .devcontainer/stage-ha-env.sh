#!/bin/bash
#
# Runs on the HOST (from init-host-credentials.sh). Stages the Home Assistant
# .env for homeautomation-go so install-ha-env.sh can place it inside the
# container. Same script in home-automation and home-automation-plus-security.
#
# Source, first match wins:
#   $HOME_AUTOMATION_ENV_FILE
#   ${XDG_CONFIG_HOME:-~/.config}/home-automation/.env
# The source must have mode 600 (it holds a Home Assistant token).
# Output (git-ignored): .devcontainer/.ha-env
#
# One-time setup on the host:
#   mkdir -p ~/.config/home-automation
#   cp /path/to/existing/homeautomation-go/.env ~/.config/home-automation/.env
#   chmod 600 ~/.config/home-automation/.env
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STAGED="$SCRIPT_DIR/.ha-env"
SOURCE="${HOME_AUTOMATION_ENV_FILE:-${XDG_CONFIG_HOME:-$HOME/.config}/home-automation/.env}"

rm -f "$STAGED"
if [ ! -s "$SOURCE" ]; then
    echo "[stage-ha-env] No $SOURCE — skipping. Tests run without it; running the app needs a .env." >&2
    exit 0
fi

mode_oct="$(stat -c '%a' "$SOURCE" 2>/dev/null || true)"
mode_sym="$(stat -f '%A' "$SOURCE" 2>/dev/null || true)"
if [ "$mode_oct" != "600" ] && [ "$mode_sym" != "-rw-------" ]; then
    echo "[stage-ha-env] Ignoring $SOURCE: it must have mode 600 (chmod 600 it)." >&2
    exit 0
fi

install -m 600 "$SOURCE" "$STAGED"
echo "[stage-ha-env] Home Assistant .env staged." >&2
