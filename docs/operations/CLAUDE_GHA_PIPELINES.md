# Claude Code GitHub Actions Pipelines

This document describes the complex Claude Code-based GitHub Actions pipelines in this repository. These pipelines enable AI-powered issue resolution, PR creation, code review, and automated test failure fixes.

## Overview

The repository uses several interconnected workflows that leverage Claude Code to automate software development tasks:

```
┌──────────────────────────────────────────────────────────────────┐
│                        TRIGGER EVENTS                             │
├──────────────────────────────────────────────────────────────────┤
│  Issue Opened  │  @claude Mention  │  PR Tests Complete          │
└───────┬────────┴────────┬──────────┴─────────┬───────────────────┘
        │                 │                    │
        ▼                 ▼                    ▼
┌───────────────┐ ┌───────────────┐ ┌──────────────────┐
│ claude.yml    │ │ claude.yml    │ │ claude-code-     │
│ resolve-issue │ │ claude job    │ │ review.yml       │
└───────────────┘ └───────────────┘ └──────────────────┘
        │                 │                    │
        ▼                 ▼                    ▼
┌──────────────────────────────────────────────────────────────────┐
│                       DEVCONTAINER EXECUTION                      │
│  Claude Code runs inside a cached devcontainer with full access   │
└──────────────────────────────────────────────────────────────────┘
```

## Workflow Files

| Workflow | File | Trigger | Purpose |
|----------|------|---------|---------|
| Claude Code | `claude.yml` | @claude mentions, new issues | Respond to requests, auto-resolve issues |
| Claude Code Review | `claude-code-review.yml` | PR Tests completion | Multi-agent code review, fix failures |
| PR Tests | `pr-tests.yml` | PRs, pushes to claude/** | Run tests, trigger review pipeline |

---

## 1. Claude Code (`claude.yml`)

The main workflow that enables Claude to respond to requests and automatically resolve issues.

### Triggers

- **Issue Comment**: When a comment contains `@claude`
- **PR Review Comment**: When a review comment contains `@claude`
- **PR Review**: When a review body contains `@claude`
- **New Issue**: When an issue is opened

### Jobs

#### 1.1 `build-devcontainer`

Builds and caches a devcontainer image to speed up subsequent runs.

- Pushes to `ghcr.io/nickborgers/home-automation-devcontainer`
- Uses Docker layer caching

#### 1.2 `claude`

Responds to `@claude` mentions in comments.

**Condition**: Only runs if the comment/body contains `@claude`

**Workflow**:
1. Detects if the context is a PR or issue
2. Checks out the appropriate branch (PR head or default)
3. Runs Claude Code inside the devcontainer
4. Claude implements requested changes or responds to questions
5. For PRs: commits and pushes changes to the PR branch
6. For issues: creates branches and opens PRs as needed

**Key Configuration**:
```yaml
claude --print \
  --model opus \
  --dangerously-skip-permissions \
  --max-turns 400 \
  "$PROMPT"
```

#### 1.3 `resolve-issue`

Automatically resolves newly opened issues.

**Condition**: Runs on `issues: [opened]` event

**Workflow**:
1. Labels issue with `claude-started`
2. Analyzes the issue title and body
3. Explores the codebase to understand context
4. Implements fixes or features
5. Creates a branch (`claude/issue-{number}`)
6. Opens a PR with the solution

**Max Turns**: 600 (longer for complex issues)

### Artifacts

- `claude-conversation-log`: Full conversation log in JSONL format
- `claude-session-data`: Session state for debugging
- Retention: 7 days

---

## 2. Claude Code Review (`claude-code-review.yml`)

A sophisticated multi-agent review system that runs after PR tests complete.

### Triggers

- **workflow_run**: When "PR Tests" workflow completes
- **workflow_dispatch**: Manual trigger with PR details

### Concurrency Control

```yaml
concurrency:
  group: claude-review-${{ branch_name }}
  cancel-in-progress: false  # Complete first review, don't restart
```

### Jobs Flow

```
get-context
     │
     ▼
build-devcontainer
     │
     ├──────────────────────────────────────────┐
     │                                          │
     ▼ (tests failed)                           ▼ (tests passed)
fix-test-failures                          claude-review
     │                                          │
     │ (triggers new test run)                  ▼
     │                                     test-review
     │                                          │
     │                                          ▼
     │                                   concurrency-review
     │                                          │
     │                                          ▼
     │                                      docs-review
     │                                          │
     └──────────────────────────────────────────┤
                                                ▼
                                       all-reviews-passed
```

### Jobs

#### 2.1 `get-context`

Extracts PR and issue context for downstream jobs.

**Outputs**:
- `pr_number`, `pr_title`, `pr_body_b64`: PR details (base64 encoded for special chars)
- `issue_number`, `issue_title`, `issue_body_b64`: Linked issue details
- `tests_passed`: Whether the triggering test run passed
- `fix_attempts`: Number of previous fix attempts (from labels)
- `triggering_sha`: SHA that triggered the workflow (for conflict detection)

#### 2.2 `fix-test-failures`

Automatically fixes failing tests (up to 3 attempts).

**Condition**: Tests failed AND fix_attempts < 3

**Workflow**:
1. Checks if author pushed newer commits (avoids conflicts)
2. Analyzes failed test logs: `gh run view ${RUN_ID} --log-failed`
3. Identifies root cause (failing tests, coverage < 65%, linting)
4. Fixes issues in code
5. Commits and pushes fix
6. Triggers new PR Tests run
7. If tests pass, triggers Claude Code Review

**Labels**: Adds `claude-fix-attempt-N` label to track attempts

**Max Attempts**: 3 (after which human intervention is required)

#### 2.3 `claude-review`

General code quality review.

**Condition**: Tests passed

**Focus Areas**:
- Shadow state pattern compliance
- Proper mutex usage for WebSocket writes
- Race conditions
- Code coverage (65% minimum)
- Error wrapping with context
- Table-driven test patterns

**Actions**: Fixes high-severity issues directly

#### 2.4 `test-review`

QA-focused test review.

**Focus Areas**:
- Missing tests for new features
- Tests with timing dependencies (may fail at certain times of day)
- Slow tests
- Test quality issues

**Actions**: Adds test coverage for high-severity gaps

#### 2.5 `concurrency-review`

Specialized concurrency/race condition review.

**Focus Areas**:
- Missing mutex locks on shared state
- Incorrect `sync.RWMutex` usage
- WebSocket write serialization (`writeMu`)
- Goroutine leaks (missing context cancellation)
- Channel deadlocks

**Reference**: `docs/reference/CONCURRENCY_LESSONS.md`

**Actions**: Fixes high-severity concurrency bugs

#### 2.6 `docs-review`

Documentation synchronization review.

**Focus Areas**:
| Code Change | Docs to Update |
|-------------|----------------|
| New plugin | VISUAL_ARCHITECTURE.md, ARCHITECTURE.md |
| Plugin removed | VISUAL_ARCHITECTURE.md, ARCHITECTURE.md |
| New Subscribe() | State Variable Dependency Graph |
| State variable added | migration_mapping.md, VISUAL_ARCHITECTURE.md |
| Concurrency fix | CONCURRENCY_LESSONS.md |
| Plugin logic | Relevant logic flow diagram |

**Actions**: Updates documentation, validates Mermaid diagrams

#### 2.7 `all-reviews-passed`

Aggregator job for branch protection.

**Purpose**: Creates a single status check that indicates all reviews completed

**Creates Commit Status**: `All Required Agent Reviews` on the PR head SHA

---

## 3. PR Tests (`pr-tests.yml`)

Standard test workflow that gates merging and triggers Claude reviews.

### Triggers

- Pull requests to any branch
- Pushes to `claude/**` branches
- Manual trigger with optional ref

### Jobs

| Job | Description |
|-----|-------------|
| `changes` | Detects which files changed (path filtering) |
| `style-checks` | Go formatting, linting |
| `unit-tests` | Unit tests with coverage |
| `integration-tests` | Integration test suite |
| `config-tests` | YAML validation, Spotify URI checks |
| `diagram-validation` | Generated diagram freshness |
| `docker-build` | Build validation + smoke test |
| `all-tests-passed` | Aggregator for branch protection |

### Path Filtering

Only runs relevant jobs based on changed files:
- `code`: `homeautomation-go/**`, `configs/**`, `.github/workflows/**`
- `docker`: Dockerfile, go.mod, go.sum, *.go, configs

---

## Required Secrets

| Secret | Used By | Purpose |
|--------|---------|---------|
| `CLAUDE_CODE_OAUTH_TOKEN` | claude.yml, claude-code-review.yml | Claude Code authentication |
| `WORKFLOW_PAT` | claude.yml, claude-code-review.yml | Push workflow file changes, create PRs that trigger workflows |
| `PRIVATE_REPO_TRIGGER_TOKEN` | notify-pr-merged.yml | Cross-repo workflow triggers |

### Why `WORKFLOW_PAT`?

GitHub's `GITHUB_TOKEN` has limitations:
1. Cannot push changes to workflow files (`.github/workflows/`)
2. PRs created with `GITHUB_TOKEN` don't trigger other workflows

The `WORKFLOW_PAT` is a Personal Access Token with `repo` and `workflow` scopes.

---

## Branch Protection Integration

The workflows create commit statuses that can be used in branch protection rules:

| Status Context | Created By | When |
|----------------|------------|------|
| `All Required Tests` | fix-test-failures | After automated fix passes tests |
| `All Required Agent Reviews` | all-reviews-passed | After all review jobs complete |

### Recommended Branch Protection Settings

```yaml
Required status checks:
  - "All Required Tests"
  - "All Required Agent Reviews"

Required approvals: 0  # Claude provides automated review
```

---

## Debugging

### View Conversation Logs

1. Go to the workflow run in GitHub Actions
2. Download the `claude-conversation-log` artifact
3. Parse the JSONL file for the full conversation

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| "Detected newer commits - aborting" | Author pushed while Claude was fixing | Normal - Claude yields to human |
| Max fix attempts reached | Tests keep failing after 3 tries | Human intervention needed |
| Devcontainer build slow | First run or cache miss | Subsequent runs use cached image |
| Claude doesn't respond | Comment doesn't contain `@claude` | Ensure @claude is in comment |

### Manual Triggers

All workflows support `workflow_dispatch` for manual testing:

```bash
# Trigger Claude Code Review manually
gh workflow run "Claude Code Review" \
  -f head_ref="your-branch" \
  -f head_repo="owner/repo" \
  -f pr_number="123" \
  -f tests_passed="true" \
  -f run_id="12345678"
```

---

## Architecture Decisions

### Why Devcontainers?

1. **Consistent Environment**: Same tools and dependencies across all runs
2. **Caching**: Devcontainer image is cached in GHCR for fast startup
3. **Tool Access**: Claude Code runs with full access to git, gh CLI, make, go, etc.

### Why Sequential Reviews?

The review jobs run sequentially (not in parallel) because:
1. Each reviewer may push fixes
2. Later reviewers see all previous changes
3. Avoids merge conflicts between reviewers

### Why Max 3 Fix Attempts?

- Prevents infinite loops if the issue can't be fixed automatically
- Leaves clear trail via labels (`claude-fix-attempt-1`, etc.)
- Human can review after 3 attempts

### Why Artifact Upload on `always()`?

Conversation logs are uploaded even on failure to help debug:
- What Claude tried
- Where it failed
- Full context for investigation

---

## Extending the Pipelines

### Adding a New Review Specialist

1. Add a new job after the appropriate dependency
2. Follow the pattern:
   ```yaml
   new-review:
     needs: [get-context, build-devcontainer, previous-review]
     if: |
       always() &&
       needs.get-context.outputs.pr_number != '' &&
       needs.get-context.outputs.tests_passed == 'true'
     # ... steps ...
   ```
3. Add to `all-reviews-passed` needs and check
4. Document the new reviewer's focus area

### Modifying Claude Prompts

The prompts are embedded in the workflow YAML in the `runCmd` block. Key sections:
- `YOUR TASK`: What Claude should do
- `IMPORTANT RULES`: Repository-specific constraints
- Post-task actions: How to report results

---

## Related Documentation

- [BRANCH_PROTECTION.md](./BRANCH_PROTECTION.md) - Branch protection setup
- [DOCKER.md](./DOCKER.md) - Container image details
- [../reference/SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Pattern Claude reviews for
- [../reference/CONCURRENCY_LESSONS.md](../reference/CONCURRENCY_LESSONS.md) - Concurrency reviewer reference
