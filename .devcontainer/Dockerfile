# Devcontainer Dockerfile with pre-installed Claude Code and Playwright
# This caches system packages and tools so rebuilds are faster
# Consolidates devcontainer features into Dockerfile for better layer caching

FROM mcr.microsoft.com/devcontainers/go:1.24

# Install system packages: Playwright browser dependencies, zsh, and utilities
# Consolidates what was previously in common-utils feature
# Remove Yarn apt source from base image - its GPG key is expired/missing
RUN rm -f /etc/apt/sources.list.d/yarn.list \
    && apt-get update && apt-get install -y --no-install-recommends \
    # Shell and common utilities (from common-utils feature)
    zsh \
    # Core Chromium dependencies
    libglib2.0-0 libnss3 libnspr4 libdbus-1-3 libatk1.0-0 \
    libatk-bridge2.0-0 libcups2 libdrm2 libxkbcommon0 libatspi2.0-0 \
    libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2 \
    # Fonts
    fonts-liberation fonts-noto-color-emoji \
    # Playwright host dependencies (recommended)
    libcairo2 libpango-1.0-0 \
    # Utilities used by Claude Code and OpenCode
    bc ripgrep fzf tmux \
    # YAML validation
    yamllint \
    && rm -rf /var/lib/apt/lists/*

# Note: zsh is still installed above and oh-my-zsh ships in the base Go
# devcontainer image; users who want zsh can invoke it explicitly. The
# vscode user keeps the base image default login shell (bash).

# Install GitHub CLI (from github-cli feature)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Pin AI CLI versions so Dockerfile, post-create script, and updater workflow stay in sync
ARG CLAUDE_CODE_VERSION=2.1.143
ARG CODEX_CLI_VERSION=0.130.0
ARG CRUSH_CLI_VERSION=0.69.1

# Install Node.js LTS (needed for Claude Code plugins and Playwright build step)
ARG NODE_VERSION=20
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_VERSION}.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Crush is installed at container runtime (post-create) so devcontainer rebuilds
# stay fast even when its installer changes upstream.

# Create directories for vscode user with correct ownership
RUN mkdir -p /home/vscode/.claude/tmp \
    && mkdir -p /home/vscode/.claude/plugins/cache \
    && mkdir -p /home/vscode/.claude/plugins/marketplaces \
    && mkdir -p /home/vscode/.cache/ms-playwright \
    && chown -R vscode:vscode /home/vscode/.claude \
    && chown -R vscode:vscode /home/vscode/.cache

# Switch to vscode user for CLI/plugin installation
USER vscode
WORKDIR /home/vscode

# Ensure the native CLI installers land on PATH for subsequent RUN steps
ENV PATH=/home/vscode/.local/bin:$PATH

# Set TMPDIR to avoid EXDEV errors during plugin installation
ENV TMPDIR=/home/vscode/.claude/tmp

# Install Claude Code via the official installer (respects CLAUDE_CODE_VERSION)
RUN curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused https://claude.ai/install.sh \
    | bash -s -- ${CLAUDE_CODE_VERSION}

# Install playwright-skill plugin
RUN claude plugin marketplace add lackeyjb/playwright-skill \
    && claude plugin install playwright-skill@playwright-skill

# Install Playwright npm dependencies and the browsers used by AI reviews.
RUN cd ~/.claude/plugins/cache/playwright-skill/playwright-skill/*/skills/playwright-skill \
    && npm install \
    && npx playwright install chromium chrome

# Install Codex CLI (baked into image to avoid runtime downloads during CI runs)
RUN curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused \
    "https://github.com/openai/codex/releases/download/rust-v${CODEX_CLI_VERSION}/install.sh" \
    | sh -s -- --release "${CODEX_CLI_VERSION}"

# Switch back to root so onCreateCommand can create users dynamically.
# The devcontainer's remoteUser setting ensures all commands run as the correct
# non-root user. Claude Code refuses --dangerously-skip-permissions as root,
# but remoteUser ensures we're never root at runtime.
USER root
