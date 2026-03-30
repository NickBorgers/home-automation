#!/bin/bash

set -euo pipefail

# Fallback: install codex if not already present (e.g., if post-create.sh
# was skipped or if devcontainer features wiped the install).
if ! command -v codex &>/dev/null; then
  echo "codex not found, installing..."
  sudo npm install -g "@openai/codex@0.117.0"
fi

mkdir -p "$HOME/.codex"

# Configure Codex CLI to use the self-hosted LiteLLM proxy (no-auth).
# OPENAI_API_KEY is set to a placeholder in devcontainer.json because
# the LiteLLM proxy doesn't require authentication.
cat > "$HOME/.codex/config.toml" << 'EOF'
model = "gpt-5-codex"
model_provider = "litellm"

[model_providers.litellm]
name = "LiteLLM"
base_url = "https://llm.featherback-mermaid.ts.net/v1"
env_key = "OPENAI_API_KEY"
EOF

echo "Codex configured for LiteLLM proxy."
