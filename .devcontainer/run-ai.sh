#!/bin/bash
#
# Tool-agnostic wrapper around whichever AI CLI the pipelines are configured
# to use. Called from every workflow invocation site:
#
#   bash .devcontainer/run-ai.sh "$PROMPT" 2>&1 | tee /tmp/ai-conversation.jsonl
#
# The workflow env block passes a single generic AI_API_KEY; this script
# translates it into whatever env var name the selected CLI reads.

set -euo pipefail

# Load shared tool selection: sets AI_TOOL and AI_BASE_URL.
source "$(dirname "${BASH_SOURCE[0]}")/ai-tool.env"

export PATH="$HOME/.local/bin:$PATH"

if [ "$#" -lt 1 ]; then
  echo "Usage: $0 <prompt>" >&2
  exit 2
fi

PROMPT="$1"

: "${AI_API_KEY:?AI_API_KEY must be set in the workflow env block (maps to the active tools API key)}"

case "$AI_TOOL" in
  claude)
    export ANTHROPIC_API_KEY="$AI_API_KEY"
    export ANTHROPIC_BASE_URL="$AI_BASE_URL"
    # --dangerously-skip-permissions + --permission-mode=bypassPermissions are
    # required for Claude Code in non-interactive mode to edit files / run
    # commands without prompting.
    exec claude -p \
      --dangerously-skip-permissions \
      --permission-mode=bypassPermissions \
      --model "${CLAUDE_MODEL:-claude-sonnet-4-6}" \
      "$PROMPT"
    ;;
  codex)
    export OPENAI_API_KEY="$AI_API_KEY"
    # Codex reads base URL from ~/.codex/config.toml (written by configure-ai.sh).
    exec codex exec --json --dangerously-bypass-approvals-and-sandbox "$PROMPT"
    ;;
  *)
    echo "ERROR: unknown AI_TOOL='${AI_TOOL}' (expected 'claude' or 'codex')" >&2
    exit 1
    ;;
esac
