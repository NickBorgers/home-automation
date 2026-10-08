#!/bin/bash
# Self-test for the Claude token handoff. Runs init-host-credentials.sh and
# wire-claude-token.sh against FAKE credentials in a throwaway HOME, so it
# never touches the real ~/.claude or this directory's token files.
# Usage: bash .devcontainer/test-claude-token.sh
set -euo pipefail

SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ACCESS="fake-access-token-AAAA"
REFRESH="fake-refresh-token-ZZZZ"
fail() { echo "FAIL: $*" >&2; exit 1; }

new_world() {   # $1 = expiry offset in seconds, or "none" for no login
  rm -rf "$TMP/home" "$TMP/dc"; mkdir -p "$TMP/home/.claude" "$TMP/dc"
  cp "$SRC"/init-host-credentials.sh "$SRC"/wire-claude-token.sh "$TMP/dc/"
  if [ "$1" != "none" ]; then
    printf '{"claudeAiOauth":{"accessToken":"%s","refreshToken":"%s","expiresAt":%s}}' \
      "$ACCESS" "$REFRESH" "$(( ($(date +%s) + $1) * 1000 ))" >"$TMP/home/.claude/.credentials.json"
  fi
}
run_init() { HOME="$TMP/home" PATH="$PATH" bash "$TMP/dc/init-host-credentials.sh" 2>&1; }
token_file="$TMP/dc/.claude-oauth-token"

# 1. Valid login: access token only, mode 600, no refresh token anywhere.
new_world 3600; echo "$REFRESH" >"$TMP/dc/.claude-credentials"
out="$(run_init)"
[ "$(cat "$token_file")" = "$ACCESS" ] || fail "token file should hold the access token"
[ "$(stat -c %a "$token_file")" = 600 ] || fail "token file should be mode 600"
[ ! -e "$TMP/dc/.claude-credentials" ] || fail "old credentials copy should be removed"
if grep -rqF "$REFRESH" "$TMP/dc"; then fail "refresh token leaked into .devcontainer"; fi
if printf '%s' "$out" | grep -qF "$REFRESH"; then fail "refresh token printed"; fi

# 2. Expired login: warns, leaves an empty file.
new_world -60
out="$(run_init)"
printf '%s' "$out" | grep -q expired || fail "expired token should warn"
[ ! -s "$token_file" ] || fail "expired token should not be written"

# 3. No login: warns, continues.
new_world none
out="$(run_init)"
printf '%s' "$out" | grep -q "No Claude login" || fail "missing login should warn"
[ ! -s "$token_file" ] || fail "no token expected"

# 4. Wiring twice exports once; login and interactive shells see the token.
new_world 3600; run_init >/dev/null
for _ in 1 2; do HOME="$TMP/home" bash "$TMP/dc/wire-claude-token.sh" "$token_file" >/dev/null 2>&1; done
for rc in .bashrc .profile .zshrc; do
  [ "$(grep -c 'claude-oauth-token-export' "$TMP/home/$rc")" = 1 ] || fail "$rc should have one export block"
done
for rc in .profile .bashrc; do
  got="$(HOME="$TMP/home" bash -c ". '$TMP/home/$rc'; printf %s \"\$CLAUDE_CODE_OAUTH_TOKEN\"")"
  [ "$got" = "$ACCESS" ] || fail "$rc should export the access token"
done
echo "OK: container receives the access token only"
