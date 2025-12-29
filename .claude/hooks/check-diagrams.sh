#!/bin/bash
# Claude Code PostToolUse Hook: Check if diagram updates may be needed
#
# This hook runs after Edit/Write operations and:
# 1. Reminds about diagram updates when plugin code changes (once per plugin per session)
# 2. Reminds to validate Mermaid syntax when diagram files are edited
#
# Context window optimization: Uses session tracking to avoid repetitive output.
# Full reminders shown once per plugin/file, then minimal output on subsequent edits.

set -e

# Parse hook input from stdin (JSON with tool_input.file_path)
INPUT=$(cat)
FILE_PATH=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Check jq availability
if [[ $? -ne 0 ]] || ! command -v jq &>/dev/null; then
    echo "Warning: jq not installed, diagram hook disabled" >&2
    exit 0
fi

# Exit early if no file path
if [[ -z "$FILE_PATH" ]]; then
    exit 0
fi

# Session tracking directory - use PPID (Claude Code process) for stability across hook invocations
# Falls back to a shared directory if PPID isn't available
SESSION_DIR="/tmp/claude-diagram-hook-${PPID:-shared}"
mkdir -p "$SESSION_DIR" 2>/dev/null

# Clean up stale session directories older than 2 hours
find /tmp -maxdepth 1 -name "claude-diagram-hook-*" -type d -mmin +120 -exec rm -rf {} \; 2>/dev/null || true

# Check if editing plugin code - remind about diagrams
if [[ "$FILE_PATH" =~ internal/plugins/ ]]; then
    # Extract plugin name from path
    PLUGIN_NAME=$(echo "$FILE_PATH" | sed -n 's|.*internal/plugins/\([^/]*\)/.*|\1|p')

    if [[ -n "$PLUGIN_NAME" ]]; then
        MARKER_FILE="$SESSION_DIR/plugin-$PLUGIN_NAME"

        if [[ ! -f "$MARKER_FILE" ]]; then
            # First edit to this plugin - show full reminder
            echo ""
            echo "--- Diagram Reminder ---"
            echo "Plugin code changed: $PLUGIN_NAME"
            echo ""
            echo "If this change affects state variables or subscriptions, update:"
            echo "  - docs/human/VISUAL_ARCHITECTURE.md (State Variable Dependency Graph)"
            echo ""
            echo "Run 'make validate-mermaid' before committing."
            echo "------------------------"
            touch "$MARKER_FILE"
        fi
        # Subsequent edits: no output (saves context window)
    fi
fi

# Remind about validation when editing diagram files (don't auto-run to save time/context)
if [[ "$FILE_PATH" == *"VISUAL_ARCHITECTURE.md"* ]] || [[ "$FILE_PATH" == *"ARCHITECTURE.md"* ]]; then
    MARKER_FILE="$SESSION_DIR/diagram-edited"

    if [[ ! -f "$MARKER_FILE" ]]; then
        # First diagram edit - show reminder
        echo ""
        echo "Diagram file edited. Run 'make validate-mermaid' before committing."
        touch "$MARKER_FILE"
    fi
    # Subsequent edits: no output (saves context window)
fi

exit 0
