# Devcontainer Dockerfile with pre-installed Playwright dependencies
# This caches system packages so rebuilds are faster

FROM mcr.microsoft.com/devcontainers/go:1.25

# Install Playwright browser dependencies
# These are the system libraries required for Chromium to run
RUN apt-get update && apt-get install -y --no-install-recommends \
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
    && rm -rf /var/lib/apt/lists/*
