# Codex GitHub Actions Pipelines

This document describes the Codex-based GitHub Actions pipelines in this repository. These pipelines enable AI-powered issue resolution, PR creation, code review, and automated test failure fixes.

## Overview

The repository uses several interconnected workflows that leverage Codex to automate software development tasks:

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           TRIGGER EVENTS                                  │
├──────────────────────────────────────────────────────────────────────────┤
│  Issue Opened  │  @codex Mention   │  PR Tests Complete/Failed  │ Monthly │
└───────┬────────┴────────┬──────────┴─────────┬─────────────────┴────┬────┘
        │                 │                    │                      │
        ▼                 ▼                    ▼                      ▼
┌───────────────┐ ┌───────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ ai-assistant  │ │ ai-assistant  │ │ ai-code-review   │ │ ha-deprecation-  │
│ resolve-issue │ │ codex job     │ │ review.yml       │ │ check.yml        │
└───────────────┘ └───────────────┘ └──────────────────┘ └────────┬─────────┘
        │                 │                    │                   │
        ▼                 ▼                    │          (creates issues)
┌──────────────────────────────────────────────┤                   │
│                                              │◄──────────────────┘
│  DEVCONTAINER EXECUTION                      │◄───────────────────┐
│  Codex runs inside a cached                  │                    │
│  devcontainer with full access               │                    │
│                                              │                    │
└──────────────────────────────────────────────┘                    │
                                                                    │
                       ┌────────────────────────────────────────────┘
                       │
                       ▼
              ┌────────────────────┐
              │ ai-diagnose-       │
              │ workflow-failure   │  (on workflow failures)
              └────────────────────┘
```

## Security

**Important:** These workflows implement authorization checks to prevent prompt injection attacks. See [CI_CD_SECURITY.md](./CI_CD_SECURITY.md) for the full security model.

Key security measures:
- **Authorization checks**: Only repository collaborators/members/owners can trigger Codex
- **Devcontainer isolation**: Always built from `main` branch, never from PR branches
- **Fork PR blocking**: Fork PRs are blocked from running on the self-hosted runner (security). Maintainers must test fork contributions manually

## Workflow Files

| Workflow | File | Trigger | Purpose |
|----------|------|---------|---------|
| Codex | `ai-assistant.yml` | @codex mentions, new issues | Respond to requests, auto-resolve issues |
| Codex Code Review | `ai-code-review.yml` | PR Tests completion, PR reopened | Multi-agent code review, fix failures, merge decision |
| PR Tests | `pr-tests.yml` | PRs, pushes to all branches | Run tests, trigger review pipeline |
| Codex Diagnose Workflow Failure | `ai-diagnose-workflow-failure.yml` | Any workflow failure | Diagnose Actions config problems (not test failures) |
| HA Deprecation Check | `ha-deprecation-check.yml` | Monthly schedule, manual dispatch | Scan HA release notes for deprecated APIs, create issues |

---

## 1. Codex (`ai-assistant.yml`)

The main workflow that enables Codex to respond to requests and automatically resolve issues.

### Triggers

- **Issue Comment**: When a comment contains `@codex`
- **PR Review Comment**: When a review comment contains `@codex`
- **PR Review**: When a review body contains `@codex`
- **New Issue**: When an issue is opened

### Concurrency Control

```yaml
concurrency:
  group: codex-interactive-${{ issue_or_pr_number }}
  cancel-in-progress: true  # Cancel in-flight runs so the newest request executes
```

Scoped by issue/PR number so that:
- Two rapid `@codex` mentions on the **same** PR/issue cannot overlap; the newer invocation cancels the prior run and starts immediately
- `@codex` on **different** PRs/issues runs in parallel (separate concurrency groups)

`cancel-in-progress: true` prevents queued work from racing stale context—Codex immediately restarts with the newest request, ensuring the comment that triggered it is always the one being processed.

**What you’ll see in Actions:** the superseded run flips to a yellow "Cancelled" status within a few seconds and its summary panel notes that a newer run for the same concurrency group started. The replacement run starts cleanly with the latest comment payload, so there’s no risk of Codex responding to stale instructions.

### Jobs

#### 1.1 `build-devcontainer`

Builds and caches a devcontainer image to speed up subsequent runs.

- **Guard**: Runs only on newly opened issues or when the triggering comment/review body explicitly includes `@codex`, matching the workflow's `if` conditions.
- Pushes to `ghcr.io/nickborgers/home-automation-devcontainer`
- **Skip optimization**: Skips the build entirely when `.devcontainer/` files haven't changed in the PR and the image already exists in GHCR. Falls back to always building for new issues and non-PR contexts.
- **Output**: `should-build` — consumed by downstream steps via conditional `if` guards
- **Why it matters**: The guard keeps routine comments (status updates, reactions, etc.) from burning self-hosted minutes—the container build triggers only when Codex is actually going to run.

#### 1.2 `codex`

Responds to `@codex` mentions in comments.

**Condition**: Only runs if the comment/body contains `@codex`

**Workflow**:
1. Detects if the context is a PR or issue
2. Checks out the appropriate branch (PR head or default)
3. Runs Codex inside the devcontainer
4. Codex implements requested changes or responds to questions
5. For PRs: commits and pushes changes to the PR branch
6. For issues: creates branches and opens PRs as needed

**Key Configuration**:
```yaml
codex exec \
  --json \
  --dangerously-bypass-approvals-and-sandbox \
  "$PROMPT"
```

#### 1.3 `resolve-issue`

Automatically resolves newly opened issues.

**Condition**: Runs on `issues: [opened, assigned]` event

**Workflow**:
1. Labels issue with `codex-started`
2. Analyzes the issue title, body, and **all comments** on the issue
3. Explores the codebase to understand context
4. Implements fixes or features
5. Creates a branch (`codex/issue-{number}`)
6. Opens a PR with the solution

**Max Turns**: 600 (longer for complex issues)

**Timeout**: 120 minutes

### Artifacts

- `codex-conversation-log`: Full conversation log in JSONL format
- Retention: 7 days

---

## 2. Codex Code Review (`ai-code-review.yml`)

A sophisticated multi-agent review system that runs after PR tests complete.

### Triggers

- **workflow_run**: When "PR Tests" workflow completes
- **pull_request**: When a PR is reopened (resets reviews to allow re-review)
- **workflow_dispatch**: Manual trigger with PR details

### Concurrency Control

```yaml
concurrency:
  group: codex-review-${{ branch_name }}
  cancel-in-progress: false  # Complete first review, don't restart
```

### Jobs Flow

```
get-context
     │
     ├─── (reviews_already_passed = true) ──► SKIP (just create commit status)
     │
     ├─── (config_only = true) ──► STREAMLINED (skip to merge-decision)
     │
     ▼
build-devcontainer
     │
     ├──────────────────────────────────────────┐
     │                                          │
     ▼ (tests failed)                           ▼ (tests passed, all run in parallel)
fix-test-failures                    ┌──────────┼──────────┐──────────┐──────────┐
     │                               │          │          │          │          │
     │ (triggers new test run)       ▼          ▼          ▼          ▼          ▼
     │                          design-   codex-    test-    concur-   docs-
     │                          review    review    review   rency-    review
     │                               │          │          │  review        │
     │                               └──────────┼──────────┘──────┘────────┘
     │                                          ▼
     │                                    merge-decision
     │                                          │
     └──────────────────────────────────────────┤
                                                ▼
                                       all-reviews-passed
                                     (adds agent-reviews-passed label)
```

### Draft PR Handling

Draft PRs are completely skipped by the review pipeline to support TDD workflows where tests may intentionally fail during development.

**When a PR is marked as draft:**
- `build-devcontainer` is skipped (no container needed)
- `fix-test-failures` is skipped (allows intentional test failures)
- All review jobs are skipped (design, code, test, concurrency, docs)
- `merge-decision` is skipped
- Summary comment indicates draft status with instructions to mark ready for review

**To enable reviews:** Mark the PR as "Ready for review" in GitHub. This triggers the PR Tests workflow, which then triggers the full review pipeline.

**Use case:** TDD workflows where you want to:
1. Write failing tests first
2. Push to get CI feedback on test structure
3. Implement the feature
4. Mark ready for review when tests pass

### Config-Only PR Optimization

PRs that only modify files in `configs/` receive streamlined review:

| Review Job | Skipped for Config-Only | Reason |
|------------|------------------------|--------|
| design-review | Yes | No design decisions to review |
| codex-review | Yes | No code to review |
| test-review | Yes | No tests to review |
| concurrency-review | Yes | No Go code to analyze |
| docs-review | Yes | Config changes don't need doc updates |
| merge-decision | No | Still runs to validate and approve |

The summary comment indicates the PR type and shows reviews as "⏭️ Skipped (config-only)".

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
- `is_draft`: Whether the PR is a draft (skips auto-fix and all reviews for TDD support)
- `config_only`: Whether the PR only modifies files in `configs/` (skips heavy reviews)
- `has_go_code`: Whether the PR modifies any `.go` files (required for concurrency review)
- `cross_pr_context_b64`: Base64-encoded digest of recent merged PRs and file change history (capped at 15KB), passed to design-review, code-review, and merge-decision

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
7. If tests pass, triggers Codex Code Review
8. If tests still fail, triggers another fix attempt (self-retry)
9. After 3 failed attempts, posts a comment to the PR requesting human intervention

**Self-Retry Mechanism**: Because `workflow_run` events don't fire for workflows triggered via `workflow_dispatch`, the fix job manually triggers the next fix attempt when tests still fail. This ensures the retry loop works correctly regardless of how the workflow was initiated.

**Failure Notification**: When all 3 fix attempts are exhausted, a comment is automatically posted to the PR:
- Indicates automated fix failed
- Links to the most recent failed test run
- Requests human intervention

**Labels**: Adds `codex-fix-attempt-N` label to track attempts

**Max Attempts**: 3 (after which human intervention is required)

#### 2.3 `design-review`

Design validation and critical design review specialist. Runs in parallel with other reviews.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Purpose**: This reviewer serves two functions:
1. **Intent Validation**: Ensures the PR actually implements what was requested in the linked issue and/or PR description (including **all comments** on the linked issue)
2. **Design Critique**: Critically reviews the design decisions for quality, trade-offs, and potential issues

**Focus Areas**:
- Does the PR solve the stated problem?
- Are there gaps between requirements and implementation?
- **Scope check**: Does the diff match the PR title/issue scope? Flags >300 lines of non-test code beyond what the title implies as BLOCKING scope creep
- **Production resilience**: New external integrations must have retry logic, appropriate timeouts, and graceful degradation
- **Cross-PR context**: Considers whether the PR contradicts or undermines recently merged changes
- Is this a good design for the problem?
- What predictable challenges or edge cases exist?
- Are there simpler or more robust alternatives?
- Does it fit with existing architecture?

**For Design-Heavy PRs** (new patterns, architectures, structural changes):
- Extensibility and flexibility
- Error handling and failure modes
- Performance implications
- Coupling and cohesion
- Testability

**Reference**: `docs/architecture/ARCHITECTURE.md`, `docs/reference/SHADOW_STATE.md`

**Max Turns**: 450 (high turn count for thorough analysis)

**Actions**: Posts design analysis with intent validation, concerns, and suggestions. Does not push fixes (comment-only; merge-decision applies fixes).

#### 2.4 `codex-review`

General code quality review. Runs in parallel with other reviews. Includes cross-PR context to detect divergence from recent changes.

**Condition**: Tests passed

**Focus Areas**:
- Shadow state pattern compliance
- Proper mutex usage for WebSocket writes
- Race conditions
- Code coverage (65% minimum)
- Error wrapping with context
- Table-driven test patterns

**Actions**: Comments on issues found. Does not push fixes (comment-only; merge-decision applies fixes).

#### 2.5 `test-review`

QA-focused test review.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Focus Areas**:
- Missing tests for new features
- Tests with timing dependencies (may fail at certain times of day)
- Slow tests
- Test quality issues
- **Test execution time analysis** (new/modified tests taking >1s or >5s)
- **Anti-pattern detection** (BLOCKING): race conditions in test helpers, assert.Eventually in shared helpers (should be require.Eventually), weakened assertions without justification, dead code path testing, time.Sleep without justification

**Test Performance Analysis**:
The test reviewer analyzes whether PRs increase test execution time:
- Identifies slow tests (>1s) and very slow tests (>5s)
- Checks for unnecessary `time.Sleep` calls, missing `t.Parallel()`, repeated expensive setup
- **PR-specific issues** are fixed directly (add `t.Parallel()`, reduce sleeps, optimize setup)
- **Infrastructure improvements** are filed as GitHub issues for broader test optimization opportunities

**Actions**: Comments on test issues with precise file:line references. Does not push fixes (comment-only; merge-decision applies fixes).

#### 2.6 `concurrency-review`

Specialized concurrency/race condition review.

**Condition**: Tests passed on current commit AND `has_go_code = true` (verified via commit status check and file classification)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Focus Areas**:
- Missing mutex locks on shared state
- Incorrect `sync.RWMutex` usage
- WebSocket write serialization (`writeMu`)
- Goroutine leaks (missing context cancellation)
- Channel deadlocks
- **Test code**: Mock servers with shared mutable state, test helpers with unprotected counters/accumulators

**Reference**: `docs/reference/CONCURRENCY_LESSONS.md`

**Actions**: Comments on concurrency issues with precise file:line references. Does not push fixes (comment-only; merge-decision applies fixes).

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
| Workflow changes (.github/workflows/*.yml) | AI_GHA_PIPELINES.md |

**Actions**: Comments on needed documentation updates. Does not push fixes (comment-only; merge-decision applies fixes).

#### 2.8 `merge-decision`

Final decision maker that synthesizes all reviews and makes a go/no-go call.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running, verifies that "All Required Tests" commit status is `success` on the current HEAD.

**Responsibilities**:
1. Reads all previous review comments on the PR
2. **Addresses all findings**: For every "Worth Considering" and "Blocking Issues" item, either fixes it, accepts it with justification, or creates a tracking issue
3. **Spot checks** the 2-3 most-changed files to verify reviewers addressed significant changes
4. **Cross-PR check**: Verifies the PR doesn't conflict with or undermine recently merged changes
5. Checks for merge conflicts with main branch
6. Resolves merge conflicts if the PR should be merged
7. Makes a GO/NO-GO decision based on all review results and its own verification

**Override Rule**: If the merge-decision agent finds a concrete issue that reviewers missed, its verdict is NO-GO regardless of individual approvals. It is the last line of defense.

**Decision Criteria**:
- **GO**: All reviews passed or had only minor non-blocking issues, no merge conflicts (or resolved them), and spot check found no missed issues
- **NO-GO**: Any review found blocking issues, unresolvable merge conflicts, tests are failing, or a concrete issue was found that reviewers missed

**Actions**: Applies fixes identified by reviewers, resolves merge conflicts, and posts a final comment with the merge decision (🟢 GO or 🔴 NO-GO). This is the **only agent that pushes commits** in the review pipeline.

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

Standard test workflow that gates merging and triggers Codex reviews. Runs on the self-hosted homelab runner.

**Fork PR blocking:** Fork PRs are blocked at the `changes` gate job (prevents arbitrary code execution on the self-hosted runner). The `all-tests-passed` aggregator job also includes the fork check directly since it uses `if: always()` which bypasses dependency skipping. Maintainers must manually test fork contributions.

### Triggers

- Pull requests to any branch (same-repo only; fork PRs are skipped)
- Pushes to all branches (`**`)
- Manual trigger with optional ref

### Jobs

| Job | Description |
|-----|-------------|
| `changes` | Detects which files changed (path filtering) |
| `style-checks` | Go formatting, linting (`make ci-style-checks`) |
| `unit-tests` | Unit tests with coverage (`make unit-tests-impl`) |
| `integration-tests` | Integration test suite (`make integration-tests-impl`) |
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

## 4. Codex Diagnose Workflow Failure (`ai-diagnose-workflow-failure.yml`)

Automatically diagnoses workflow failures to determine if they're GitHub Actions configuration problems (which need issues filed) versus normal test/code failures (which are handled by the existing review pipeline).

### Triggers

- **workflow_run**: When any monitored workflow completes with failure
  - PR Tests
  - Build and Push Docker Image
  - Publish-Screenshots
  - Trigger Private Security Rebuild
  - Notify Private Repo of PR Merge
  - Codex
  - Codex Code Review

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

Runs Codex to analyze the failure and classify it.

**Classification Categories**:

| Category | Description | Action |
|----------|-------------|--------|
| `TEST_FAILURE` | Unit tests, integration tests, code compilation | No issue - handled by Codex Code Review |
| `CONFIG_FAILURE` | Issues in configs/ (YAML validation, etc.) | No issue - handled by Codex Code Review |
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

## 5. HA Deprecation Check (`ha-deprecation-check.yml`)

Proactively scans Home Assistant release notes for deprecated APIs and checks if the codebase uses them. Creates GitHub issues for any deprecated usage found, which the existing `resolve-issue` job in `ai-assistant.yml` will auto-fix.

### Triggers

- **Schedule**: 5th of each month at 10am UTC (HA typically releases on the first Wednesday)
- **Manual**: `workflow_dispatch` with optional `ha_version` input (validated as `YYYY.M[.P]`) to check a specific release

### Concurrency Control

```yaml
concurrency:
  group: ha-deprecation-check
  cancel-in-progress: true
```

### Jobs

#### 5.1 `build-devcontainer`

Checks if the devcontainer image already exists in GHCR and only builds if missing. Since this is a monthly job, it relies on other workflows (`ai-assistant.yml`, `pr-tests.yml`) to keep the image up to date.

#### 5.2 `check-deprecations`

Runs Codex to perform a three-phase check:

1. **Gather Deprecations**: Fetches HA release notes from the blog, looking for deprecated service calls, entity attributes, WebSocket API changes, and renamed parameters
2. **Scan Codebase**: Searches plugin code and configs for usage of any deprecated APIs found
3. **Report**: Creates one GitHub issue per deprecated API found (with `ha-deprecation` label), skipping duplicates

**Issue Integration**: Issues are created with `@codex` in the title prefix, so the `resolve-issue` job in `ai-assistant.yml` automatically picks them up and creates fix PRs.

**Token Note**: Uses `WORKFLOW_PAT` (not `GITHUB_TOKEN`) so created issues trigger the `resolve-issue` workflow.

**Result Codes**:

| Result | Meaning |
|--------|---------|
| `NO_DEPRECATIONS_FOUND` | Codebase is up to date |
| `ISSUES_CREATED <N>` | Created N issues for deprecated API usage |
| `ALL_DUPLICATES` | Deprecations found but already tracked in existing issues |
| `FETCH_FAILED` | Could not retrieve HA release notes (workflow fails) |
| Unrecognized | Could not parse Codex output (workflow fails) |

### Artifacts

- `deprecation-check-output`: Full Codex output in JSONL format
- Retention: 30 days (longer than default since this runs monthly)

---

## Required Secrets

| Secret | Used By | Purpose |
|--------|---------|---------|
| `OPENAI_API_KEY` | ai-assistant.yml, ai-code-review.yml, ai-diagnose-workflow-failure.yml, ha-deprecation-check.yml | Codex authentication via LiteLLM |
| `WORKFLOW_PAT` | ai-assistant.yml, ai-code-review.yml, ha-deprecation-check.yml, update-ai-clis.yml | Push workflow file changes, create PRs/issues that trigger workflows |
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

Required approvals: 0  # Codex provides automated review
```

---

## Debugging

### View Conversation Logs

1. Go to the workflow run in GitHub Actions
2. Download the `codex-conversation-log` artifact
3. Parse the JSONL file for the full conversation

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| "Detected newer commits - aborting" | Author pushed while Codex was fixing | Normal - Codex yields to human |
| Max fix attempts reached | Tests keep failing after 3 tries | Human intervention needed (PR will have a comment) |
| "Automated Fix Failed" comment | All 3 fix attempts exhausted | Review the linked test run, fix manually |
| Review job skipped | Tests haven't passed on current commit | Wait for tests to pass or fix them first |
| Reviews skipped with "already passed" | `agent-reviews-passed` label present | Close and reopen PR to re-run reviews |
| Reviews skipped (draft PR) | PR is marked as draft | Mark PR as "Ready for review" to enable reviews |
| Reviews skipped (config-only) | PR only modifies `configs/` files | Expected - config PRs use streamlined review |
| Devcontainer build slow | First run or cache miss | Subsequent runs use cached image; builds are skipped entirely when `.devcontainer/` files are unchanged |
| Codex doesn't respond | Comment doesn't contain `@codex` | Ensure @codex is in comment |
| Workflow failure issue not created | Diagnosed as TEST_FAILURE or CONFIG_FAILURE | Expected - these are handled by Codex Code Review |

### Manual Triggers

All workflows support `workflow_dispatch` for manual testing:

```bash
# Trigger Codex Code Review manually
gh workflow run "Codex Code Review" \
  -f head_ref="your-branch" \
  -f head_repo="owner/repo" \
  -f pr_number="123" \
  -f tests_passed="true" \
  -f run_id="12345678"
```

---

## Architecture Decisions

### Model Selection

The pipelines standardize on `gpt-5-codex` via Codex and the LiteLLM proxy.

| Pipeline/Job | Model | Rationale |
|-------------|-------|-----------|
| `ai-assistant.yml` interactive and issue resolution | `gpt-5-codex` | Open-ended implementation and repo changes |
| `ai-code-review.yml` review and merge jobs | `gpt-5-codex` | Consistent review quality across the pipeline |
| `ai-diagnose-workflow-failure.yml` | `gpt-5-codex` | Failure triage and classification |
| `ha-deprecation-check.yml` | `gpt-5-codex` | Release-note scanning and issue creation |

The workflow display names are `Codex`, `Codex Code Review`, and `Codex Diagnose Workflow Failure`, with filenames `ai-assistant.yml`, `ai-code-review.yml`, and `ai-diagnose-workflow-failure.yml`.

### Why Devcontainers?

1. **Consistent Environment**: Same tools and dependencies across all runs
2. **Caching**: Devcontainer image is cached in GHCR for fast startup
3. **Tool Access**: Codex runs with full access to git, gh CLI, make, go, etc.

### Why Parallel Reviews?

The review jobs run in parallel (after context gathering and devcontainer build) to reduce wall-clock time from ~45 min to ~15-20 min. To prevent git conflicts from concurrent pushes:
1. All 5 parallel reviewers are **comment-only** — they have `contents: read` permissions and are explicitly instructed not to push
2. The merge-decision agent is the **sole agent that pushes fixes**, running sequentially after all reviews complete
3. This eliminates the conflict risk entirely while preserving the ability to auto-fix issues

### Why Max 3 Fix Attempts?

- Prevents infinite loops if the issue can't be fixed automatically
- Leaves clear trail via labels (`codex-fix-attempt-1`, etc.)
- Human can review after 3 attempts

### Why Artifact Upload on `always()`?

Conversation logs are uploaded even on failure to help debug:
- What Codex tried
- Where it failed
- Full context for investigation

---

## Extending the Pipelines

### Adding a New Review Specialist

1. Add a new job that depends on `get-context` and `build-devcontainer` (runs in parallel with other reviews)
2. Follow the pattern:
   ```yaml
   new-review:
     needs: [get-context, build-devcontainer]
     if: |
       needs.get-context.outputs.pr_number != '' &&
       needs.get-context.outputs.reviews_already_passed != 'true' &&
       needs.get-context.outputs.tests_passed == 'true'
     # ... steps ...
   ```
3. Add to `merge-decision` needs to include the new reviewer's result
4. Add to `all-reviews-passed` needs and check
5. Add to the summary comment in `all-reviews-passed`
6. Document the new reviewer's focus area in this file

### Modifying Codex Prompts

The prompts are embedded in the workflow YAML in the `runCmd` block. Key sections:
- `YOUR TASK`: What Codex should do
- `IMPORTANT RULES`: Repository-specific constraints
- Post-task actions: How to report results

---

## Related Documentation

- [CI_CD_SECURITY.md](./CI_CD_SECURITY.md) - **Security model for Codex-powered workflows** (authorization, threat model)
- [BRANCH_PROTECTION.md](./BRANCH_PROTECTION.md) - Branch protection setup
- [DOCKER.md](./DOCKER.md) - Container image details
- [../reference/SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Pattern Codex reviews for
- [../reference/CONCURRENCY_LESSONS.md](../reference/CONCURRENCY_LESSONS.md) - Concurrency reviewer reference
