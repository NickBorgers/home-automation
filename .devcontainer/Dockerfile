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
    # Utilities used by Claude Code
    bc \
    # YAML validation
    yamllint \
    && rm -rf /var/lib/apt/lists/* \
    # Set zsh as default shell for vscode user
    && chsh -s /usr/bin/zsh vscode

# Note: oh-my-zsh is already included in the base Go devcontainer image

# Install GitHub CLI (from github-cli feature)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update \
    && apt-get install -y gh \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js LTS (needed for Claude Code and Playwright)
ARG NODE_VERSION=20
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_VERSION}.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI globally
RUN npm install -g @anthropic-ai/claude-code

# Create directories for vscode user with correct ownership
RUN mkdir -p /home/vscode/.claude/tmp \
    && mkdir -p /home/vscode/.claude/plugins/cache \
    && mkdir -p /home/vscode/.claude/plugins/marketplaces \
    && mkdir -p /home/vscode/.cache/ms-playwright \
    && chown -R vscode:vscode /home/vscode/.claude \
    && chown -R vscode:vscode /home/vscode/.cache

# Switch to vscode user for plugin installation
USER vscode
WORKDIR /home/vscode

# Set TMPDIR to avoid EXDEV errors during plugin installation
ENV TMPDIR=/home/vscode/.claude/tmp

# Install playwright-skill plugin
RUN claude plugin marketplace add lackeyjb/playwright-skill \
    && claude plugin install playwright-skill@playwright-skill

# Install Playwright npm dependencies and browser
RUN cd ~/.claude/plugins/cache/playwright-skill/playwright-skill/*/skills/playwright-skill \
    && npm install \
    && npx playwright install chromium

# Switch back to root for any remaining operations
USER root
