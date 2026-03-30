#!/bin/bash
# Post-create setup script for devcontainer
# This script runs automatically when the devcontainer is created

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Setting up devcontainer ==="

# Fix DNS order to prioritize Tailscale MagicDNS
# (Also runs via postStartCommand on every container start)
echo "Checking DNS configuration..."
bash "$SCRIPT_DIR/fix-dns-order.sh"

# Compute repository root (parent of .devcontainer) so script works
# regardless of the local workspace folder name.
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "Setting up Go modules... (repo root: $REPO_ROOT)"
if [ -d "$REPO_ROOT/homeautomation-go" ]; then
    (cd "$REPO_ROOT/homeautomation-go" && go mod tidy && go mod download)
else
    echo "Warning: $REPO_ROOT/homeautomation-go not found; skipping Go module setup."
fi

# Git hooks
echo "Installing git hooks..."
if [ -d "$REPO_ROOT" ]; then
    (cd "$REPO_ROOT" && mkdir -p build)
    if [ -x "$REPO_ROOT/.githooks/install-hooks.sh" ] || [ -f "$REPO_ROOT/.githooks/install-hooks.sh" ]; then
        (cd "$REPO_ROOT" && bash .githooks/install-hooks.sh)
    else
        echo "Warning: .githooks/install-hooks.sh not found or not executable; skipping git hooks installation."
    fi
else
    echo "Warning: repository root $REPO_ROOT not found; skipping git hooks installation."
fi

# Set up gh CLI credentials from host token (written by initializeCommand)
REPO_TOKEN_FILE="$REPO_ROOT/.devcontainer/.gh-token"
if [ -s "$REPO_TOKEN_FILE" ]; then
    echo "Setting up GitHub CLI credentials..."
    GH_TOKEN=$(cat "$REPO_TOKEN_FILE")
    echo "$GH_TOKEN" | gh auth login --with-token 2>/dev/null && echo "gh auth configured." || echo "Warning: gh auth login failed."
    rm -f "$REPO_TOKEN_FILE"
else
    echo "Warning: No gh token found; gh CLI credentials not available."
fi

# Helper to read pinned versions from the Dockerfile (single source of truth)
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

echo "Installing AI coding assistants..."
export PATH="$HOME/.local/bin:$PATH"

CODEX_CLI_VERSION="$(get_arg_version CODEX_CLI_VERSION)"
CRUSH_CLI_VERSION="$(get_arg_version CRUSH_CLI_VERSION)"

# Install/update Codex via official installer
install_codex() {
    echo "Installing Codex CLI ${CODEX_CLI_VERSION}..."
    curl -fsSL "https://github.com/openai/codex/releases/download/rust-v${CODEX_CLI_VERSION}/install.sh" \
        | sh -s -- "${CODEX_CLI_VERSION}"
}

CURRENT_CODEX_VERSION="$(codex --version 2>/dev/null | awk '{print $2}' || true)"
if [ "$CURRENT_CODEX_VERSION" != "$CODEX_CLI_VERSION" ]; then
    install_codex
else
    echo "Codex CLI already at ${CURRENT_CODEX_VERSION}, skipping install."
fi

# Install/update Crush by downloading the release tarball
install_crush() {
    echo "Installing Crush CLI ${CRUSH_CLI_VERSION}..."
    local tmpdir
    tmpdir=$(mktemp -d)
    local arch
    case "$(uname -m)" in
        x86_64|amd64)
            arch="x86_64"
            ;;
        aarch64|arm64)
            arch="arm64"
            ;;
        *)
            echo "Unsupported architecture for Crush CLI: $(uname -m)" >&2
            rm -rf "$tmpdir"
            exit 1
            ;;
    esac
    local archive="crush_${CRUSH_CLI_VERSION}_Linux_${arch}.tar.gz"
    curl -fsSL -o "$tmpdir/${archive}" \
        "https://github.com/charmbracelet/crush/releases/download/v${CRUSH_CLI_VERSION}/${archive}"
    tar -xzf "$tmpdir/${archive}" -C "$tmpdir"
    sudo install -m 0755 "$tmpdir/crush_${CRUSH_CLI_VERSION}_Linux_${arch}/crush" /usr/local/bin/crush
    rm -rf "$tmpdir"
}

CURRENT_CRUSH_VERSION="$(crush --version 2>/dev/null | awk '{print $NF}' | sed 's/^v//' || true)"
if [ "$CURRENT_CRUSH_VERSION" != "$CRUSH_CLI_VERSION" ]; then
    install_crush
else
    echo "Crush CLI already at ${CURRENT_CRUSH_VERSION}, skipping install."
fi

bash "$SCRIPT_DIR/configure-codex.sh"

# Note: Claude Code and playwright-skill plugin are pre-installed in the
# Dockerfile for faster rebuilds via Docker layer caching.

echo "=== Devcontainer setup complete ==="
