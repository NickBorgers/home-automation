#!/bin/bash
# Runs on the HOST before the devcontainer starts.
# Extracts credentials from host CLI and writes them to files
# that postCreateCommand will consume inside the container.
# All token files are gitignored. They are deleted after use, except
# .claude-oauth-token, which every container shell reads.

set -e
cd "$(dirname "$0")"

# Ensure every devcontainer.json bind-mount source exists before Docker
# tries to resolve it. Missing bind-mount sources are dangerous: Docker
# silently materializes them as root-owned directories on the host (on
# Linux) or errors out at container start (on macOS / Docker Desktop).
# Creating user-owned placeholders here is a cheap no-op when the files
# already exist, and guarantees the devcontainer can spin up on any
# contributor's machine. post-create.sh uses `-s` (non-empty) guards
# before wiring anything in, so empty placeholders are installed but
# never activated.
[ -e "$HOME/.claude"           ] || mkdir -p "$HOME/.claude"
[ -e "$HOME/.bashrc"           ] || touch    "$HOME/.bashrc"
[ -d "$HOME/code/util"         ] || mkdir -p "$HOME/code/util"
[ -e "$HOME/code/util/profile" ] || touch    "$HOME/code/util/profile"

# GitHub CLI token
gh auth token > .gh-token 2>/dev/null || true

# Claude Code OAuth access token.
#
# Only the short-lived ACCESS token leaves the host. Claude's refresh token
# rotates on use, so a container holding it can refresh and invalidate the
# host's login (and every other session). The container exports the access
# token as CLAUDE_CODE_OAUTH_TOKEN (see wire-claude-token.sh); it cannot
# refresh anything. The file is rewritten in place on every start so the
# bind-mounted workspace always sees the current token.
CLAUDE_TOKEN_FILE=".claude-oauth-token"
rm -f .claude-credentials   # earlier revisions copied the whole file here

claude_json_field() {
    # $1 = field under claudeAiOauth; JSON on stdin; prints nothing on failure.
    python3 -c '
import json, sys
try:
    print(json.load(sys.stdin).get("claudeAiOauth", {}).get(sys.argv[1], ""))
except Exception:
    pass
' "$1" 2>/dev/null
}

claude_json=""
if [ -f "$HOME/.claude/.credentials.json" ]; then
    claude_json="$(cat "$HOME/.claude/.credentials.json")"
fi
claude_token="$(printf '%s' "$claude_json" | claude_json_field accessToken)"
claude_expires_ms="$(printf '%s' "$claude_json" | claude_json_field expiresAt)"
unset claude_json

: > "$CLAUDE_TOKEN_FILE"    # truncate in place; stale tokens never survive
chmod 600 "$CLAUDE_TOKEN_FILE"
now_ms=$(( $(date +%s) * 1000 ))
if [ -z "$claude_token" ]; then
    echo "[init-host-credentials] No Claude login on the host; the container will need its own login." >&2
elif [ -n "$claude_expires_ms" ] && [ "$claude_expires_ms" -le "$now_ms" ] 2>/dev/null; then
    echo "[init-host-credentials] The host Claude token has expired; run claude on the host, then restart the devcontainer." >&2
else
    printf '%s\n' "$claude_token" > "$CLAUDE_TOKEN_FILE"
    echo "[init-host-credentials] Claude access token captured (no refresh token)." >&2
fi
unset claude_token

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
