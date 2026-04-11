#!/bin/bash

set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"

# Claude Code CLI is baked into the devcontainer image. Verify it's available.
if ! command -v claude &>/dev/null; then
  echo "ERROR: claude not found — expected it baked into the devcontainer image" >&2
  exit 1
fi

# Claude Code reads ANTHROPIC_API_KEY and ANTHROPIC_BASE_URL straight from the
# environment — no on-disk config file needed. The workflow env block is
# responsible for setting those values; this script only handles git identity.

AI_GIT_NAME_DEFAULT="claude[bot]"
AI_GIT_EMAIL_DEFAULT="claude[bot]@users.noreply.github.com"
AI_GIT_NAME="${AI_GIT_NAME:-$AI_GIT_NAME_DEFAULT}"
AI_GIT_EMAIL="${AI_GIT_EMAIL:-$AI_GIT_EMAIL_DEFAULT}"

if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
  git config --global user.name "${AI_GIT_NAME}"
  git config --global user.email "${AI_GIT_EMAIL}"

  export GIT_AUTHOR_NAME="${AI_GIT_NAME}"
  export GIT_AUTHOR_EMAIL="${AI_GIT_EMAIL}"
  export GIT_COMMITTER_NAME="${AI_GIT_NAME}"
  export GIT_COMMITTER_EMAIL="${AI_GIT_EMAIL}"
fi

echo "Claude Code configured for LiteLLM proxy."
