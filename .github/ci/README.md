# `.github/ci/` — CI agent container

This directory houses the **CI-only** container image used by every
automated agent workflow in this repo (`ai-assistant`,
`ai-diagnose-workflow-failure`, `ha-deprecation-check`, …). It exists
because running CI agents through `.devcontainer/` — the thing designed
for human IDE workflows — turned out to be fragile across the
self-hosted runner's shared-docker-socket topology, and because mixing
two very different use cases into one container definition caused a
recurring class of host-vs-runner-filesystem-namespace bugs.

## Files

| File | Purpose |
|---|---|
| `Dockerfile` | CI agent image recipe. Installs Go, Node, `gh`, Claude Code, Codex, Crush, yamllint, ripgrep, playwright-skill, etc. Runs as UID-1000 `ci` user. |
| `versions.env` | Single source of truth for CLI version pins (Claude Code, Codex, Crush, Node major). Loaded into `.github/workflows/ci-image.yml` and passed as `--build-arg`s. |
| `README.md` | This file. |

## Relationship to `.devcontainer/`

| Aspect | `.devcontainer/` | `.github/ci/` |
|---|---|---|
| Audience | Human developers in VS Code / Cursor / JetBrains | CI agent workflows |
| Lifecycle hooks | Yes (`initializeCommand`, `postCreateCommand`, `postStartCommand`) | None — built once, run many |
| Host bind mounts | Yes — `~/.claude`, `~/.bashrc`, `~/code/util/profile` | None |
| Credential passthrough | Copied from host files via `init-host-credentials.sh` | `-e` env vars at `docker run` time |
| Base image | `mcr.microsoft.com/devcontainers/go:1.24` (IDE tooling bundled) | `golang:1.24-bookworm` (leaner) |
| Default shell | bash via base image default (zsh removed in the cleanup) | bash |
| User | `vscode` (UID 1000 via `updateRemoteUserUID`) | `ci` (UID 1000 baked in) |
| Package cache | `ghcr.io/nickborgers/home-automation-devcontainer:latest` | `ghcr.io/nickborgers/home-automation-ci:latest` |
| Build pipeline | `.github/workflows/devcontainer-cache.yml` | `.github/workflows/ci-image.yml` |

Both images share version pins via `versions.env`: `.github/ci/Dockerfile`
consumes them via `ARG`s, and `.devcontainer/Dockerfile` should be updated
to do the same so the `update-ai-clis.yml` bumper has a single file to
edit.

## How CI agent workflows consume the image

Every agent workflow follows the same pattern (replacing
`devcontainers/ci@v0.3` / `run-devcontainer` composite action usage):

```yaml
- name: Log in to GHCR
  uses: docker/login-action@v3
  with:
    registry: ghcr.io
    username: ${{ github.actor }}
    password: ${{ secrets.GITHUB_TOKEN }}

- name: Pull CI image
  run: docker pull ghcr.io/nickborgers/home-automation-ci:latest

- name: Run agent
  env:
    OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
    GH_TOKEN: ${{ secrets.WORKFLOW_PAT }}
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
    ISSUE_NUMBER: ${{ github.event.issue.number }}
  run: |
    docker run --rm \
      --workdir /workspace \
      -v "${{ github.workspace }}:/workspace" \
      -e OPENAI_API_KEY -e GH_TOKEN -e ANTHROPIC_API_KEY \
      -e ISSUE_NUMBER \
      ghcr.io/nickborgers/home-automation-ci:latest \
      -lc '
        cd /workspace
        codex exec --json --dangerously-bypass-approvals-and-sandbox \
          "<prompt>"
      '
```

Key properties:
* No `initializeCommand`, no `postCreateCommand`.
* Workspace mount uses `${{ github.workspace }}`, which on the
  self-hosted runner resolves to a path under `/tmp/runner-work-…/` that
  is already bind-mounted from the dockergeneric host into the runner
  container. The inner `docker run -v` therefore passes a path that the
  outer dockerd can resolve. Same topology that the old devcontainer
  flow already relied on for the workspace mount — the only thing we're
  *not* doing here is mounting host-personal config at paths the runner
  container doesn't own.
* Credentials are passed through via `-e` env vars. The CLIs (`codex`,
  `claude`, `gh`) read them directly, no file-based credential staging.

## Rebuilding the image locally

For smoke testing before a PR:

```bash
# From repo root:
source .github/ci/versions.env
docker build \
  -f .github/ci/Dockerfile \
  --build-arg CLAUDE_CODE_VERSION="$CLAUDE_CODE_VERSION" \
  --build-arg CODEX_CLI_VERSION="$CODEX_CLI_VERSION" \
  --build-arg CRUSH_CLI_VERSION="$CRUSH_CLI_VERSION" \
  --build-arg NODE_MAJOR="$NODE_MAJOR" \
  -t home-automation-ci-local .

# Verify the toolchain:
docker run --rm home-automation-ci-local -lc '
  set -e
  claude --version
  codex --version
  crush --version
  gh --version
  go version
  node --version
  yamllint --version
  rg --version | head -1
'

# Exercise it against the repo workspace:
docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  home-automation-ci-local \
  -lc 'cd homeautomation-go && go build ./...'
```

## Version bumps

`.github/workflows/update-ai-clis.yml` is the canonical updater. It
should edit `.github/ci/versions.env` only; both the devcontainer and CI
images then pick up the new pin on their next rebuild.
