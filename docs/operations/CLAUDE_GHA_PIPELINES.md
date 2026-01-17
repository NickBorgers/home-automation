# Claude Code GitHub Actions Pipelines

This document describes the complex Claude Code-based GitHub Actions pipelines in this repository. These pipelines enable AI-powered issue resolution, PR creation, code review, and automated test failure fixes.

## Overview

The repository uses several interconnected workflows that leverage Claude Code to automate software development tasks:

```
┌──────────────────────────────────────────────────────────────────┐
│                        TRIGGER EVENTS                             │
├──────────────────────────────────────────────────────────────────┤
│  Issue Opened  │  @claude Mention  │  PR Tests Complete/Failed   │
└───────┬────────┴────────┬──────────┴─────────┬───────────────────┘
        │                 │                    │
        ▼                 ▼                    ▼
┌───────────────┐ ┌───────────────┐ ┌──────────────────┐
│ claude.yml    │ │ claude.yml    │ │ claude-code-     │
│ resolve-issue │ │ claude job    │ │ review.yml       │
└───────────────┘ └───────────────┘ └──────────────────┘
        │                 │                    │
        ▼                 ▼                    │
┌──────────────────────────────────────────────┤
│                                              │
│  DEVCONTAINER EXECUTION                      │◄───────────────────┐
│  Claude Code runs inside a cached            │                    │
│  devcontainer with full access               │                    │
│                                              │                    │
└──────────────────────────────────────────────┘                    │
                                                                    │
                       ┌────────────────────────────────────────────┘
                       │
                       ▼
              ┌────────────────────┐
              │ claude-diagnose-   │
              │ workflow-failure   │  (on workflow failures)
              └────────────────────┘
```

## Security

**Important:** These workflows implement authorization checks to prevent prompt injection attacks. See [CI_CD_SECURITY.md](./CI_CD_SECURITY.md) for the full security model.

Key security measures:
- **Authorization checks**: Only repository collaborators/members/owners can trigger Claude
- **Devcontainer isolation**: Always built from `main` branch, never from PR branches
- **External PR handling**: Fork PRs trigger tests but NOT Claude reviews

## Workflow Files

| Workflow | File | Trigger | Purpose |
|----------|------|---------|---------|
| Claude Code | `claude.yml` | @claude mentions, new issues | Respond to requests, auto-resolve issues |
| Claude Code Review | `claude-code-review.yml` | PR Tests completion, PR reopened | Multi-agent code review, fix failures, merge decision |
| PR Tests | `pr-tests.yml` | PRs, pushes to claude/** | Run tests, trigger review pipeline |
| Claude Diagnose Workflow Failure | `claude-diagnose-workflow-failure.yml` | Any workflow failure | Diagnose Actions config problems (not test failures) |

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
  --model opus \  # Opus for open-ended requests
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
- **pull_request**: When a PR is reopened (resets reviews to allow re-review)
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
     ├─── (reviews_already_passed = true) ──► SKIP (just create commit status)
     │
     ▼
build-devcontainer
     │
     ├──────────────────────────────────────────┐
     │                                          │
     ▼ (tests failed)                           ▼ (tests passed)
fix-test-failures                         design-review  ◄─ Intent/design validated FIRST
     │                                          │
     │ (triggers new test run)                  ▼
     │                                     claude-review  ◄─ Code quality review
     │                                          │
     │                                          ▼
     │                                     test-review
     │                                          │
     │                                          ▼
     │                                   concurrency-review
     │                                          │
     │                                          ▼
     │                                      docs-review
     │                                          │
     │                                          ▼
     │                                    merge-decision
     │                                          │
     └──────────────────────────────────────────┤
                                                ▼
                                       all-reviews-passed
                                     (adds agent-reviews-passed label)
```

### Review Skip Mechanism

To avoid redundant reviews on every push, the workflow uses an `agent-reviews-passed` label:

1. After all reviews complete successfully, the label is added to the PR
2. Subsequent pushes detect the label and skip the full review cycle
3. When a PR is **reopened**, the label is removed to allow re-review

This prevents review agents from running repeatedly on the same PR while still allowing re-reviews when needed.

### Jobs

#### 2.1 `get-context`

Extracts PR and issue context for downstream jobs.

**Outputs**:
- `pr_number`, `pr_title`, `pr_body_b64`: PR details (base64 encoded for special chars)
- `issue_number`, `issue_title`, `issue_body_b64`: Linked issue details
- `tests_passed`: Whether the triggering test run passed
- `fix_attempts`: Number of previous fix attempts (from labels)
- `triggering_sha`: SHA that triggered the workflow (for conflict detection)
- `reviews_already_passed`: Whether the `agent-reviews-passed` label exists (skips re-review)

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
8. If tests still fail, triggers another fix attempt (self-retry)
9. After 3 failed attempts, posts a comment to the PR requesting human intervention

**Self-Retry Mechanism**: Because `workflow_run` events don't fire for workflows triggered via `workflow_dispatch`, the fix job manually triggers the next fix attempt when tests still fail. This ensures the retry loop works correctly regardless of how the workflow was initiated.

**Failure Notification**: When all 3 fix attempts are exhausted, a comment is automatically posted to the PR:
- Indicates automated fix failed
- Links to the most recent failed test run
- Requests human intervention

**Labels**: Adds `claude-fix-attempt-N` label to track attempts

**Max Attempts**: 3 (after which human intervention is required)

#### 2.3 `design-review`

Design validation and critical design review specialist. **Runs first** (before code review) because intent and design must be validated before code quality matters.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Purpose**: This reviewer serves two functions:
1. **Intent Validation**: Ensures the PR actually implements what was requested in the linked issue and/or PR description
2. **Design Critique**: Critically reviews the design decisions for quality, trade-offs, and potential issues

**Focus Areas**:
- Does the PR solve the stated problem?
- Are there gaps between requirements and implementation?
- Is this a good design for the problem?
- What predictable challenges or edge cases exist?
- What trade-offs does the design bring?
- Are there simpler or more robust alternatives?
- Does it fit with existing architecture?
- Will it scale and be maintainable?

**For Design-Heavy PRs** (new patterns, architectures, structural changes):
- Extensibility and flexibility
- Error handling and failure modes
- Performance implications
- Coupling and cohesion
- Testability

**Reference**: `docs/architecture/ARCHITECTURE.md`, `docs/reference/SHADOW_STATE.md`

**Max Turns**: 450 (high turn count for thorough analysis)

**Actions**: Posts design analysis with intent validation, strengths, concerns, and suggestions

#### 2.4 `claude-review`

General code quality review. Runs **after design-review** because code quality only matters if the intent and design are correct.

**Condition**: Tests passed

**Focus Areas**:
- Shadow state pattern compliance
- Proper mutex usage for WebSocket writes
- Race conditions
- Code coverage (65% minimum)
- Error wrapping with context
- Table-driven test patterns

**Actions**: Fixes high-severity issues directly

#### 2.5 `test-review`

QA-focused test review.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Focus Areas**:
- Missing tests for new features
- Tests with timing dependencies (may fail at certain times of day)
- Slow tests
- Test quality issues

**Actions**: Adds test coverage for high-severity gaps

#### 2.6 `concurrency-review`

Specialized concurrency/race condition review.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Focus Areas**:
- Missing mutex locks on shared state
- Incorrect `sync.RWMutex` usage
- WebSocket write serialization (`writeMu`)
- Goroutine leaks (missing context cancellation)
- Channel deadlocks

**Reference**: `docs/reference/CONCURRENCY_LESSONS.md`

**Actions**: Fixes high-severity concurrency bugs

#### 2.7 `docs-review`

Documentation synchronization review.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Focus Areas**:
| Code Change | Docs to Update |
|-------------|----------------|
| New plugin | VISUAL_ARCHITECTURE.md, ARCHITECTURE.md |
| Plugin removed | VISUAL_ARCHITECTURE.md, ARCHITECTURE.md |
| New Subscribe() | State Variable Dependency Graph |
| State variable added | migration_mapping.md, VISUAL_ARCHITECTURE.md |
| Concurrency fix | CONCURRENCY_LESSONS.md |
| Plugin logic | Relevant logic flow diagram |
| Workflow changes (.github/workflows/*.yml) | CLAUDE_GHA_PIPELINES.md |

**Actions**: Updates documentation, validates Mermaid diagrams

#### 2.8 `merge-decision`

Final decision maker that synthesizes all reviews and makes a go/no-go call.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running, verifies that "All Required Tests" commit status is `success` on the current HEAD.

**Responsibilities**:
1. Reads all previous review comments on the PR
2. Checks for merge conflicts with main branch
3. Resolves merge conflicts if the PR should be merged
4. Makes a GO/NO-GO decision based on all review results

**Decision Criteria**:
- **GO**: All reviews passed or had only minor non-blocking issues, no merge conflicts (or resolved them)
- **NO-GO**: Any review found blocking issues, unresolvable merge conflicts, or tests are failing

**Actions**: Posts a final comment with the merge decision (🟢 GO or 🔴 NO-GO)

**Output**: The job outputs the actual decision (`GO` or `NO-GO`) which is used by `all-reviews-passed` to determine the overall workflow result

#### 2.9 `all-reviews-passed`

Aggregator job for branch protection.

**Purpose**: Creates a single status check that indicates all reviews completed AND the merge decision is GO

**Creates Commit Status**: `All Required Agent Reviews` on the PR head SHA

**Merge Decision Check**: This job checks the actual merge decision output from `merge-decision`, not just whether the job succeeded. If the merge decision is `NO-GO`, the workflow fails even if all review jobs completed successfully. This ensures the summary accurately reflects whether the PR is ready to merge.

**Post-Review Actions**:
1. Creates the `All Required Agent Reviews` commit status
2. Adds the `agent-reviews-passed` label to the PR (only when merge decision is GO)
3. Posts a summary comment showing all agent statuses in a table format, including the actual merge decision verdict (🟢 GO or 🔴 NO-GO)

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
| `style-checks` | Go formatting, linting (`make ci-style-checks`) |
| `unit-tests` | Unit tests with coverage (`make ci-unit-tests`) |
| `integration-tests` | Integration test suite (`make ci-integration-tests`) |
| `config-tests` | YAML validation, Spotify URI checks |
| `diagram-validation` | Generated diagram freshness (`make validate-diagrams`) |
| `docker-build` | Build validation + smoke test (DEV_MODE) |
| `all-tests-passed` | Aggregator for branch protection |

### Docker Smoke Test

The `docker-build` job includes a container startup smoke test:
1. Starts the container in `DEV_MODE` (uses mock Home Assistant server)
2. Waits up to 30 seconds for the container to become healthy
3. Verifies the `/health` endpoint responds
4. Checks `/dashboard` and `/api/shadow` endpoints (non-fatal)

This catches initialization panics that wouldn't be detected by unit tests alone.

### Path Filtering

Only runs relevant jobs based on changed files:
- `code`: `homeautomation-go/**`, `configs/**`, `.github/workflows/**`
- `docker`: Dockerfile, go.mod, go.sum, *.go, configs

---

## 4. Claude Diagnose Workflow Failure (`claude-diagnose-workflow-failure.yml`)

Automatically diagnoses workflow failures to determine if they're GitHub Actions configuration problems (which need issues filed) versus normal test/code failures (which are handled by the existing review pipeline).

### Triggers

- **workflow_run**: When any monitored workflow completes with failure
  - PR Tests
  - Build and Push Docker Image
  - Publish-Screenshots
  - Trigger Private Security Rebuild
  - Notify Private Repo of PR Merge
  - Claude Code
  - Claude Code Review

### Concurrency Control

```yaml
concurrency:
  group: diagnose-failure-${{ workflow_run.id }}
  cancel-in-progress: false
```

### Jobs

#### 4.1 `check-failure`

Pre-flight checks before running diagnosis.

**Checks**:
1. Workflow actually failed (not success, cancelled, or skipped)
2. Not diagnosing the diagnosis workflow itself (prevents infinite loops)
3. No existing open issue for this workflow run

**Outputs**: Workflow metadata for the diagnosis job

#### 4.2 `diagnose-failure`

Runs Claude to analyze the failure and classify it.

**Classification Categories**:

| Category | Description | Action |
|----------|-------------|--------|
| `TEST_FAILURE` | Unit tests, integration tests, code compilation | No issue - handled by Claude Code Review |
| `CONFIG_FAILURE` | Issues in configs/ (YAML validation, etc.) | No issue - handled by Claude Code Review |
| `INFRASTRUCTURE_FAILURE` | Transient external service availability issues | No issue - self-resolves |
| `ACTIONS_FAILURE` | GitHub Actions workflow definition problems | **Create issue** |

**ACTIONS_FAILURE Examples** (issues created):
- Workflow YAML syntax errors
- Missing or invalid action references
- Invalid triggers or event configurations
- Missing required secrets or environment variables
- Permission issues with GitHub tokens
- Docker build failures in workflow steps (not Dockerfile)
- Job dependency issues
- Runner environment issues

**INFRASTRUCTURE_FAILURE Examples** (no issue - transient):
- GitHub Cache Service errors (502, 503, timeouts, EOF)
- Container registry availability issues (MCR, GHCR, Docker Hub)
- Network connectivity issues (EOF, connection reset, timeouts)
- Rate limiting from external services
- DNS resolution or TLS/SSL handshake failures to external services

**NOT ACTIONS_FAILURE** (no issue):
- Go test failures (`make unit-tests`, `make integration-tests`)
- Code compilation errors (`go build`)
- Style/lint check failures (staticcheck, gofmt)
- Coverage below threshold
- Application Dockerfile build failures

### Issue Format

When an `ACTIONS_FAILURE` is detected, an issue is created with:
- Title: `Workflow Failure: {workflow_name} run #{run_id} - Actions Configuration Issue`
- Labels: `bug`, `github-actions`
- Body: Failure analysis, relevant logs, suggested fix, files to review

---

## Required Secrets

| Secret | Used By | Purpose |
|--------|---------|---------|
| `CLAUDE_CODE_OAUTH_TOKEN` | claude.yml, claude-code-review.yml, claude-diagnose-workflow-failure.yml | Claude Code authentication |
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
| Max fix attempts reached | Tests keep failing after 3 tries | Human intervention needed (PR will have a comment) |
| "Automated Fix Failed" comment | All 3 fix attempts exhausted | Review the linked test run, fix manually |
| Review job skipped | Tests haven't passed on current commit | Wait for tests to pass or fix them first |
| Reviews skipped with "already passed" | `agent-reviews-passed` label present | Close and reopen PR to re-run reviews |
| Devcontainer build slow | First run or cache miss | Subsequent runs use cached image |
| Claude doesn't respond | Comment doesn't contain `@claude` | Ensure @claude is in comment |
| Workflow failure issue not created | Diagnosed as TEST_FAILURE or CONFIG_FAILURE | Expected - these are handled by Claude Code Review |

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

### Model Selection (Opus vs Sonnet)

The pipelines use a mix of Claude Opus and Claude Sonnet models, selected based on task complexity:

| Pipeline/Job | Model | Rationale |
|-------------|-------|-----------|
| **claude.yml - claude** | Opus | Open-ended requests need maximum capability for design thinking and complex reasoning |
| **claude.yml - resolve-issue** | Opus | Full issue resolution requires design, implementation, and multi-file changes |
| **claude-code-review.yml - fix-test-failures** | Sonnet | Debugging task with clear error messages; has 3 retry attempts as safety net |
| **claude-code-review.yml - claude-review** | Opus | Code quality review may require nuanced understanding of patterns and architecture |
| **claude-code-review.yml - test-review** | Sonnet | Checklist-based review with clear criteria (missing tests, slow tests) |
| **claude-code-review.yml - concurrency-review** | Opus | Race conditions require nuanced understanding of concurrent programming |
| **claude-code-review.yml - docs-review** | Opus | Documentation updates require accurate changes when PR opener missed them |
| **claude-code-review.yml - merge-decision** | Sonnet | Summarization task synthesizing existing review results |
| **claude-diagnose-workflow-failure.yml** | Sonnet | Classification task with clear decision tree (3 categories) |

**When to use Opus:**
- Open-ended, creative tasks
- Design and architecture decisions
- Complex debugging requiring deep reasoning
- Tasks where subtle issues could have major impact

**When to use Sonnet:**
- Checklist-based reviews with clear criteria
- Classification tasks with defined categories
- Summarization of existing information
- Tasks with built-in retry mechanisms

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
       needs.get-context.outputs.reviews_already_passed != 'true' &&
       needs.get-context.outputs.tests_passed == 'true'
     # ... steps ...
   ```
3. Add to `merge-decision` needs to include the new reviewer's result
4. Add to `all-reviews-passed` needs and check
5. Add to the summary comment in `all-reviews-passed`
6. Document the new reviewer's focus area in this file

### Modifying Claude Prompts

The prompts are embedded in the workflow YAML in the `runCmd` block. Key sections:
- `YOUR TASK`: What Claude should do
- `IMPORTANT RULES`: Repository-specific constraints
- Post-task actions: How to report results

---

## Related Documentation

- [CI_CD_SECURITY.md](./CI_CD_SECURITY.md) - **Security model for Claude-powered workflows** (authorization, threat model)
- [BRANCH_PROTECTION.md](./BRANCH_PROTECTION.md) - Branch protection setup
- [DOCKER.md](./DOCKER.md) - Container image details
- [../reference/SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Pattern Claude reviews for
- [../reference/CONCURRENCY_LESSONS.md](../reference/CONCURRENCY_LESSONS.md) - Concurrency reviewer reference
