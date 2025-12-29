#!/bin/bash
# Claude Code PostToolUse Hook: Check if diagram updates may be needed
#
# This hook runs after Edit/Write operations and:
# 1. Reminds about diagram updates when plugin code changes
# 2. Validates Mermaid syntax when diagram files are edited

set -e

# Parse hook input from stdin (JSON with tool_input.file_path)
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null || echo "")

# Exit early if no file path
if [[ -z "$FILE_PATH" ]]; then
    exit 0
fi

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-/workspaces/home-automation}"

# Check if editing plugin code - remind about diagrams
if [[ "$FILE_PATH" =~ internal/plugins/ ]]; then
    # Extract plugin name from path
    PLUGIN_NAME=$(echo "$FILE_PATH" | sed -n 's|.*internal/plugins/\([^/]*\)/.*|\1|p')

    echo ""
    echo "--- Diagram Reminder ---"
    echo "Plugin code changed: $PLUGIN_NAME"
    echo ""
    echo "If this change affects state variables or subscriptions, update:"
    echo "  - docs/human/VISUAL_ARCHITECTURE.md (State Variable Dependency Graph)"
    echo ""
    echo "Run 'make validate-mermaid' to check diagram syntax."
    echo "------------------------"
fi

# Validate Mermaid syntax when editing diagram files directly
if [[ "$FILE_PATH" == *"VISUAL_ARCHITECTURE.md"* ]] || [[ "$FILE_PATH" == *"ARCHITECTURE.md"* ]]; then
    echo ""
    echo "--- Validating Mermaid Diagrams ---"

    cd "$PROJECT_DIR"

    if make validate-mermaid 2>&1; then
        echo "All diagrams valid"
    else
        echo ""
        echo "Diagram validation failed. Fix syntax errors before continuing."
        # Exit code 2 blocks the tool call in Claude Code
        exit 2
    fi

    echo "-----------------------------------"
fi

exit 0
