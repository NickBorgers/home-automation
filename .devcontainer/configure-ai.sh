#!/bin/bash

set -euo pipefail

# Load shared tool selection: sets AI_TOOL and AI_BASE_URL.
source "$(dirname "${BASH_SOURCE[0]}")/ai-tool.env"

export PATH="$HOME/.local/bin:$PATH"

# Dispatch per selected tool: verify the binary is available, pick a bot git
# identity that matches the tool actually running, and write any on-disk config
# the CLI requires.
case "$AI_TOOL" in
  claude)
    if ! command -v claude &>/dev/null; then
      echo "ERROR: claude not found — expected it baked into the devcontainer image" >&2
      exit 1
    fi
    AI_GIT_NAME_DEFAULT="claude[bot]"
    AI_GIT_EMAIL_DEFAULT="claude[bot]@users.noreply.github.com"
    # Claude Code reads ANTHROPIC_API_KEY / ANTHROPIC_BASE_URL straight from the
    # environment — run-ai.sh sets those at invocation time. Nothing to write here.
    ;;
  codex)
    if ! command -v codex &>/dev/null; then
      echo "ERROR: codex not found — expected it baked into the devcontainer image" >&2
      exit 1
    fi
    AI_GIT_NAME_DEFAULT="codex[bot]"
    AI_GIT_EMAIL_DEFAULT="codex[bot]@users.noreply.github.com"
    # Codex reads its provider config from ~/.codex/config.toml.
    mkdir -p "$HOME/.codex"
    cat > "$HOME/.codex/config.toml" <<EOF
model = "gpt-5-codex"
model_provider = "litellm"

[model_providers.litellm]
name = "LiteLLM"
base_url = "${AI_BASE_URL}"
env_key = "OPENAI_API_KEY"
EOF
    ;;
  *)
    echo "ERROR: unknown AI_TOOL='${AI_TOOL}' (expected 'claude' or 'codex')" >&2
    exit 1
    ;;
esac

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

echo "AI pipeline configured: AI_TOOL=${AI_TOOL} (bot identity: ${AI_GIT_NAME})"
