#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEVCONTAINER_DOCKERFILE="$SCRIPT_DIR/Dockerfile"

get_arg_version() {
  local arg_name=$1
  local value
  value=$(grep -oP "ARG ${arg_name}=\\K[^[:space:]]+" "$DEVCONTAINER_DOCKERFILE" | tail -n 1 || true)
  if [ -z "$value" ]; then
    echo "Failed to read ${arg_name} from ${DEVCONTAINER_DOCKERFILE}" >&2
    exit 1
  fi
  printf '%s\n' "$value"
}

CODEX_CLI_VERSION="$(get_arg_version CODEX_CLI_VERSION)"

export PATH="$HOME/.local/bin:$PATH"

# Fallback: install codex if not already present (e.g., if post-create.sh
# was skipped or if devcontainer features wiped the install).
if ! command -v codex &>/dev/null; then
  echo "codex not found, installing..."
  curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused "https://github.com/openai/codex/releases/download/rust-v${CODEX_CLI_VERSION}/install.sh" \
    | sh -s -- "${CODEX_CLI_VERSION}"
fi

mkdir -p "$HOME/.codex"

# Configure Codex CLI to use the self-hosted LiteLLM proxy (no-auth).
# OPENAI_API_KEY is set to a placeholder in devcontainer.json because
# the LiteLLM proxy doesn't require authentication.
cat > "$HOME/.codex/config.toml" <<'EOF'
model = "gpt-5-codex"
model_provider = "litellm"

[model_providers.litellm]
name = "LiteLLM"
base_url = "https://llm.featherback-mermaid.ts.net/v1"
env_key = "OPENAI_API_KEY"
EOF

echo "Codex configured for LiteLLM proxy."
