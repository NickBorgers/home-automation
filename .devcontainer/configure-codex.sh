#!/bin/bash

set -euo pipefail

# Ensure codex is reachable. The devcontainer base image's nvm can
# alter PATH so that neither /usr/bin nor /usr/local/bin are searched.
# Append both to guarantee the npm-installed binaries are found.
for dir in /usr/local/bin /usr/bin; do
  case ":$PATH:" in
    *":$dir:"*) ;;          # already on PATH
    *) export PATH="$PATH:$dir" ;;
  esac
done

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

# Debug: show PATH and codex resolution (helps diagnose CI failures)
echo "DEBUG: PATH=$PATH"
echo "DEBUG: which codex = $(which codex 2>&1 || echo 'NOT FOUND')"
echo "DEBUG: ls -la /usr/local/bin/codex = $(ls -la /usr/local/bin/codex 2>&1 || echo 'MISSING')"
echo "DEBUG: ls -la /usr/bin/codex = $(ls -la /usr/bin/codex 2>&1 || echo 'MISSING')"

echo "Codex configured for LiteLLM proxy."
