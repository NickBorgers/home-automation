#!/bin/bash
#
# Test caching utility for git hooks
#
# This script implements hybrid caching that tracks:
# 1. The current commit SHA
# 2. A hash of any uncommitted changes (staged + unstaged)
#
# If the combined state matches the cached state from the last successful
# test run, tests are skipped. This avoids re-running tests when the
# engineer already ran them manually before committing/pushing.
#
# Usage:
#   test-cache.sh <cache-name> <make-target>
#
# Examples:
#   test-cache.sh pre-commit pre-commit   # Cache pre-commit checks
#   test-cache.sh pre-push pre-push       # Cache pre-push validation
#
# Cache files are stored in .test-cache/ directory (gitignored)
#

set -e

CACHE_NAME="${1:-default}"
MAKE_TARGET="${2:-test-go}"

CACHE_DIR=".test-cache"
CACHE_FILE="${CACHE_DIR}/${CACHE_NAME}.hash"

# Ensure we're at repo root
cd "$(git rev-parse --show-toplevel)"

# Create cache directory if it doesn't exist
mkdir -p "$CACHE_DIR"

# Compute current state hash based on CONTENT, not commit metadata
# This ensures the hash is the same before and after commit if file contents match
compute_state_hash() {
    local tree_hash

    # Check if working tree is clean
    if git diff HEAD --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null; then
        # Clean working tree - use the tree hash of HEAD
        tree_hash=$(git rev-parse HEAD^{tree} 2>/dev/null || echo "empty")
    else
        # Dirty working tree - compute what the tree WOULD be after commit
        # git stash create makes a commit object of current state without stashing
        local stash_commit
        stash_commit=$(git stash create 2>/dev/null)
        if [ -n "$stash_commit" ]; then
            tree_hash=$(git rev-parse "${stash_commit}^{tree}" 2>/dev/null)
        else
            # Fallback: hash all tracked file contents directly
            tree_hash=$(git ls-files -z | xargs -0 cat 2>/dev/null | sha256sum | cut -d' ' -f1)
        fi
    fi

    echo "${tree_hash}"
}

# Get cached state hash (if exists)
get_cached_hash() {
    if [ -f "$CACHE_FILE" ]; then
        cat "$CACHE_FILE"
    else
        echo ""
    fi
}

# Save current state to cache
save_cache() {
    local state_hash="$1"
    echo "$state_hash" > "$CACHE_FILE"
}

# Clear cache (useful for forcing re-run)
clear_cache() {
    rm -f "$CACHE_FILE"
}

# Main logic
main() {
    local current_hash
    local cached_hash

    current_hash=$(compute_state_hash)
    cached_hash=$(get_cached_hash)

    # Debug output (only if DEBUG_CACHE is set)
    if [ -n "$DEBUG_CACHE" ]; then
        echo "[cache] Current state: $current_hash"
        echo "[cache] Cached state:  $cached_hash"
        echo "[cache] Cache file:    $CACHE_FILE"
    fi

    # Check if we can use cached result
    if [ "$current_hash" = "$cached_hash" ]; then
        echo ""
        echo "=============================================================================="
        echo "  CACHED: No changes since last successful '${MAKE_TARGET}' run"
        echo "  Skipping tests (state hash: ${current_hash:0:20}...)"
        echo ""
        echo "  To force re-run: rm ${CACHE_FILE}"
        echo "=============================================================================="
        echo ""
        return 0
    fi

    # State changed, need to run tests
    echo ""
    echo "State changed since last test run. Running '${MAKE_TARGET}'..."
    echo ""

    # Run the actual make target
    if make "$MAKE_TARGET"; then
        # Tests passed - save state to cache
        save_cache "$current_hash"
        echo ""
        echo "[cache] Test results cached for state: ${current_hash:0:20}..."
        return 0
    else
        # Tests failed - clear cache to ensure re-run
        clear_cache
        return 1
    fi
}

# Handle special commands
case "$1" in
    --clear)
        # Clear all caches
        rm -rf "$CACHE_DIR"
        echo "Test cache cleared"
        exit 0
        ;;
    --clear-one)
        # Clear specific cache
        if [ -n "$2" ]; then
            rm -f "${CACHE_DIR}/${2}.hash"
            echo "Cache cleared: $2"
        else
            echo "Usage: $0 --clear-one <cache-name>"
            exit 1
        fi
        exit 0
        ;;
    --status)
        # Show cache status
        echo "Test cache status:"
        echo ""
        if [ -d "$CACHE_DIR" ]; then
            for cache_file in "$CACHE_DIR"/*.hash; do
                if [ -f "$cache_file" ]; then
                    name=$(basename "$cache_file" .hash)
                    hash=$(cat "$cache_file")
                    echo "  $name: ${hash:0:40}..."
                fi
            done
        else
            echo "  No caches found"
        fi
        echo ""
        echo "Current state: $(compute_state_hash)"
        exit 0
        ;;
    --help|-h)
        echo "Usage: $0 <cache-name> <make-target>"
        echo "       $0 --clear          # Clear all caches"
        echo "       $0 --clear-one NAME # Clear specific cache"
        echo "       $0 --status         # Show cache status"
        echo ""
        echo "Environment:"
        echo "  DEBUG_CACHE=1  Show debug output"
        exit 0
        ;;
esac

main
