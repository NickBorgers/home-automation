#!/bin/bash

set -euo pipefail

# Ensure npm global binaries are on PATH. The devcontainer base image uses
# nvm which can shadow /usr/bin; codex is installed globally via npm under
# /usr/bin but may not be reachable if nvm initializes a different prefix.
export PATH="/usr/bin:$PATH"

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
