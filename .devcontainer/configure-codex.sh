#!/bin/bash

set -euo pipefail

export PATH="$HOME/.local/bin:$PATH"

# Codex CLI is baked into the devcontainer image. Verify it's available.
if ! command -v codex &>/dev/null; then
  echo "ERROR: codex not found — expected it baked into the devcontainer image" >&2
  exit 1
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
