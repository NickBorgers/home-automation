# Home Automation CI agent image.
#
# This image is used EXCLUSIVELY by CI agent workflows (ai-assistant,
# ai-diagnose-workflow-failure, ha-deprecation-check, ...). It is NOT the
# same image as `.devcontainer/Dockerfile` — that one is for human IDE
# workflows with lifecycle hooks, bind mounts, and oh-my-zsh niceties.
#
# Design goals:
#   * No @devcontainers/cli, no initializeCommand, no postCreateCommand.
#   * No bind mounts of host personal config. Credentials come in via env vars.
#   * Pre-install everything the Codex / Claude Code agent workflows need.
#   * Root-free: run as UID 1000 `ci` user so Claude Code's "no root" guard
#     doesn't bite any agent invocation that uses `--dangerously-skip-permissions`.
#
# Built and pushed by `.github/workflows/ci-image.yml` to
# `ghcr.io/nickborgers/home-automation-ci:latest`.

FROM golang:1.24-bookworm

# Version pins come in via --build-arg from .github/ci/versions.env
ARG CLAUDE_CODE_VERSION
ARG CODEX_CLI_VERSION
ARG CRUSH_CLI_VERSION
ARG NODE_MAJOR=20

# System packages: CLIs + libraries the agents' toolchain needs.
# Intentionally missing from the devcontainer list: zsh, fonts-noto-color-emoji,
# oh-my-zsh — all IDE/interactive affordances we don't need headless.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl git bash gnupg jq sudo gettext-base \
        bc ripgrep fzf tmux yamllint python3-yaml \
        libglib2.0-0 libnss3 libnspr4 libdbus-1-3 libatk1.0-0 \
        libatk-bridge2.0-0 libcups2 libdrm2 libxkbcommon0 libatspi2.0-0 \
        libxcomposite1 libxdamage1 libxfixes3 libxrandr2 libgbm1 libasound2 \
        fonts-liberation \
        libcairo2 libpango-1.0-0 \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI — pull from GitHub's own apt repo (latest stable).
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
        | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && chmod go+r /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
        > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Node.js LTS — required for the Claude Code plugin runtime and for
# Playwright. We pin via NODE_MAJOR and take whatever minor NodeSource
# publishes for that line.
RUN curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# Crush CLI — download and install the release tarball. Matches the runtime
# install path the devcontainer's post-create.sh uses, but baked at build
# time so CI runs don't pay the download cost on every invocation.
RUN set -eux; \
    case "$(dpkg --print-architecture)" in \
        amd64) arch=x86_64 ;; \
        arm64) arch=arm64 ;; \
        *) echo "unsupported arch: $(dpkg --print-architecture)" >&2; exit 1 ;; \
    esac; \
    archive="crush_${CRUSH_CLI_VERSION}_Linux_${arch}.tar.gz"; \
    tmp="$(mktemp -d)"; \
    curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused \
        -o "${tmp}/${archive}" \
        "https://github.com/charmbracelet/crush/releases/download/v${CRUSH_CLI_VERSION}/${archive}"; \
    tar -xzf "${tmp}/${archive}" -C "${tmp}"; \
    install -m 0755 "${tmp}/crush_${CRUSH_CLI_VERSION}_Linux_${arch}/crush" /usr/local/bin/crush; \
    rm -rf "${tmp}"

# Non-root user for the CI workload. UID 1000 matches the self-hosted
# runner's workspace file ownership, so workspace bind-mounts write back
# with sane permissions.
RUN groupadd --gid 1000 ci \
    && useradd --uid 1000 --gid ci --shell /bin/bash --create-home ci \
    && echo 'ci ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/ci \
    && chmod 0440 /etc/sudoers.d/ci

# Login shells re-source /etc/profile, which on Debian-based images resets
# PATH to the minimal set and drops /go/bin:/usr/local/go/bin (inherited
# from the golang base image) + /home/ci/.local/bin. Re-add them via a
# profile.d drop-in so both login and non-login invocations see the full
# toolchain.
RUN printf '%s\n' \
        'export PATH="/home/ci/.local/bin:/usr/local/go/bin:/go/bin:$PATH"' \
        'export TMPDIR="/home/ci/.claude/tmp"' \
        > /etc/profile.d/ci-path.sh \
    && chmod 0644 /etc/profile.d/ci-path.sh

USER ci
WORKDIR /home/ci

# Claude Code's native installer drops the binary in ~/.local/bin.
ENV PATH=/home/ci/.local/bin:/usr/local/go/bin:/go/bin:$PATH
# Avoid EXDEV errors when Claude Code's plugin installer does cross-fs rename.
ENV TMPDIR=/home/ci/.claude/tmp

RUN mkdir -p /home/ci/.claude/tmp \
             /home/ci/.claude/plugins/cache \
             /home/ci/.claude/plugins/marketplaces \
             /home/ci/.cache/ms-playwright

# Pre-configure Claude Code for headless use: baked-in onboarding flag so
# the CLI doesn't try to run the interactive first-run wizard.
RUN printf '{"hasCompletedOnboarding": true}\n' > /home/ci/.claude.json

# Install Claude Code (pinned).
RUN curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused https://claude.ai/install.sh \
        | bash -s -- "${CLAUDE_CODE_VERSION}"

# Install playwright-skill plugin + browsers used by AI reviewers.
RUN claude plugin marketplace add lackeyjb/playwright-skill \
    && claude plugin install playwright-skill@playwright-skill
RUN cd ~/.claude/plugins/cache/playwright-skill/playwright-skill/*/skills/playwright-skill \
    && npm install \
    && npx playwright install chromium chrome

# Install Codex CLI (pinned).
RUN curl -fsSL --retry 5 --retry-delay 2 --retry-connrefused \
        "https://github.com/openai/codex/releases/download/rust-v${CODEX_CLI_VERSION}/install.sh" \
    | sh -s -- --release "${CODEX_CLI_VERSION}"

# Default entrypoint is bash — callers pass `-c '…'` (or `-lc '…'` if they
# want a login shell; either works because /etc/profile.d/ci-path.sh
# reinstates the PATH).
ENTRYPOINT ["/bin/bash"]

