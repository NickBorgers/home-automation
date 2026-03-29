#!/bin/bash

set -euo pipefail

mkdir -p "$HOME/.codex"

cat > "$HOME/.codex/config.toml" << 'EOF'
model = "gpt-5.4"
model_provider = "litellm"

[model_providers.litellm]
name = "LiteLLM"
base_url = "https://llm.featherback-mermaid.ts.net/v1"
env_key = "OPENAI_API_KEY"
EOF

echo "Codex configured for LiteLLM proxy."
