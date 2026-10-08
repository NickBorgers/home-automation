#!/bin/bash
# Run inside the container (from post-create.sh).
#
# Makes Claude Code authenticate with the short-lived OAuth ACCESS token that
# init-host-credentials.sh wrote on the host, by exporting it as
# CLAUDE_CODE_OAUTH_TOKEN from login and interactive shells.
#
# Why not copy ~/.claude/.credentials.json: Claude's refresh token rotates on
# use. A container holding its own copy can refresh it and invalidate the
# host's, which logs the host (and every other session) out. The access token
# cannot refresh anything; when it expires (about 7-8 hours) the container
# simply needs the host to run claude and the devcontainer to be restarted.
#
# The export line reads the token file when each shell starts, so a refreshed
# file (rewritten in place by init-host-credentials.sh on the next start) is
# picked up without recreating the container. Idempotent.
#
# Usage: wire-claude-token.sh <token-file>      (HOME selects the rc files)
set -euo pipefail

TOKEN_FILE="${1:?usage: wire-claude-token.sh <token-file>}"
MARK="# claude-oauth-token-export: $TOKEN_FILE"

for rc in .bashrc .profile .zshrc; do
  target="$HOME/$rc"
  [ -e "$target" ] || touch "$target"
  if ! grep -qF "$MARK" "$target"; then
    {
      printf '\n%s\n' "$MARK"
      printf '[ -s "%s" ] && CLAUDE_CODE_OAUTH_TOKEN="$(cat "%s")" && export CLAUDE_CODE_OAUTH_TOKEN\n' \
        "$TOKEN_FILE" "$TOKEN_FILE"
    } >> "$target"
  fi
done
echo "Claude Code will use the host access token from $TOKEN_FILE (no refresh token in the container)."

# A credentials file here would let the container refresh, and so rotate, a
# refresh token. Report it by name only; never print its contents.
stale="$HOME/.claude/.credentials.json"
if [ -e "$stale" ] && grep -q 'refreshToken' "$stale" 2>/dev/null; then
  echo "Warning: $stale holds a refresh token, which can invalidate another login" >&2
  echo "  if refreshed here. Delete it if it is a copy; if it is the host's file" >&2
  echo "  (host ~/.claude mounted over this home), do not run claude /login here." >&2
fi
