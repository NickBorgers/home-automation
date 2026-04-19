# AI GitHub Actions Pipelines

This document describes the AI-assistant-backed GitHub Actions pipelines in this repository. These pipelines enable AI-powered issue resolution, PR creation, code review, and automated test failure fixes.

The pipeline plumbing (file names, job names, labels, mention tag, secret name, branch prefix) is tool-agnostic: the underlying AI tool can be swapped without renaming anything. The commit author identity, however, names whichever tool is actually running — currently `claude[bot]`.

## Overview

The repository uses several interconnected workflows that leverage an AI assistant to automate software development tasks:

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           TRIGGER EVENTS                                  │
├──────────────────────────────────────────────────────────────────────────┤
│ Issue Opened / │ /autoresolve in │  PR Tests Complete/Failed  │ Monthly  │
│ ai-started     │ comment or      │                            │          │
│ unlabeled      │ review          │                            │          │
└───────┬────────┴────────┬──────────┴─────────┬─────────────────┴────┬────┘
        │                 │                    │                      │
        ▼                 ▼                    ▼                      ▼
┌───────────────┐ ┌───────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ ai-assistant  │ │ ai-assistant  │ │ ai-code-review   │ │ ha-deprecation-  │
│ resolve-issue │ │ ai job        │ │ review.yml       │ │ check.yml        │
└───────────────┘ └───────────────┘ └──────────────────┘ └────────┬─────────┘
        │                 │                    │                   │
        ▼                 ▼                    │          (creates issues)
┌──────────────────────────────────────────────┤                   │
│                                              │◄──────────────────┘
│  DEVCONTAINER EXECUTION                      │◄───────────────────┐
│  AI assistant runs inside a cached           │                    │
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
- **Authorization checks**: Only repository collaborators/members/owners can trigger the AI assistant
- **Devcontainer isolation**: Always built from `main` branch, never from PR branches
- **Fork PR blocking**: Fork PRs are blocked from running on the self-hosted runner (security). Maintainers must test fork contributions manually

## Workflow Files

| Workflow | File | Trigger | Purpose |
|----------|------|---------|---------|
| AI Assistant | `ai-assistant.yml` | New issues, ai-started label removal, `/autoresolve` comments | Auto-resolve issues, respond to comments |
| AI Code Review | `ai-code-review.yml` | PR Tests completion, PR reopened, `/review` comment | Multi-agent code review, fix failures, merge decision |
| PR Tests | `pr-tests.yml` | PRs, pushes to all branches | Run tests, trigger review pipeline |
| AI Diagnose Workflow Failure | `ai-diagnose-workflow-failure.yml` | Any workflow failure | Diagnose Actions config problems (not test failures) |
| HA Deprecation Check | `ha-deprecation-check.yml` | Monthly schedule, manual dispatch | Scan HA release notes for deprecated APIs, create issues |

---

## 1. AI Assistant (`ai-assistant.yml`)

The main workflow that enables the AI assistant to respond to requests and automatically resolve issues.

### Triggers

The workflow has three primary trigger paths, all gated by the `authorize` job:

- **Issue opened / assigned** (`issues: opened` / `issues: assigned`): Any issue opened by an authorized actor (OWNER / MEMBER / COLLABORATOR) fires the AI automatically. **No opt-in keyword required** in the title or body. This is the primary path for "please fix this."
- **`ai-started` label removed** (`issues: unlabeled`): The `resolve-issue` job stamps every issue it processes with an `ai-started` label. Removing that label from an open issue is the "try again" gesture — it re-fires the `resolve-issue` job so the AI takes another pass (useful when the previous attempt got stuck or produced the wrong PR). The unlabeled trigger is ignored unless the removed label is literally `ai-started` **and** the issue is still open.
- **`/autoresolve` in a comment or review** (fallback): When a comment, PR review, or PR review comment contains a plain-text `/autoresolve` token, the AI fires against that comment's context (PR branch or issue thread). This is the fallback path for directing the AI at an existing thread after opening. The `authorize` job strips fenced code blocks, inline code, HTML `<code>` blocks, markdown links, and HTML `<a>` links before searching for the token, so pasted examples and docs never retrigger the workflow.

Why `/autoresolve` and not `@something`? `@ai`, `@autoresolve`, and similar tags are all real GitHub usernames — using them in comments would ping live users. The slash-command style is unambiguously a bot directive and pings nobody.

The authorize job also short-circuits any comment whose body matches a known auto-generated review heading (e.g. `## Code Review`, `## Merge Decision`). This prevents the code-review pipeline from recursively triggering the AI with its own output.

### Concurrency Control

```yaml
concurrency:
  group: ai-interactive-${{ issue_or_pr_number }}
  cancel-in-progress: true  # Cancel in-flight runs so the newest request executes
```

Scoped by issue/PR number so that:
- Two rapid triggers on the **same** PR/issue cannot overlap; the newer invocation cancels the prior run and starts immediately
- Triggers on **different** PRs/issues run in parallel (separate concurrency groups)

`cancel-in-progress: true` prevents queued work from racing stale context — the AI assistant immediately restarts with the newest request so the freshest state is always the one being processed.

**When the guard trips (GitHub Actions summary messaging):**
- In-flight runs flip to the yellow `Cancelled` badge within seconds; the GitHub Actions run summary surfaces the callout "Superseded by another run in ai-interactive-<number>," so you immediately know a newer request preempted it.
- Runs blocked before they start show up in the Actions summary as `Skipped by concurrency guard` with the same superseded message, making it obvious the skip was intentional and not a workflow failure.
- Only the newest comment gets a response; stale runs never emit results because GitHub immediately launches a fresh execution with the latest payload.
- Treat the coloured cancellation/skip messaging as confirmation that the guard worked — no manual cleanup required.

### Jobs

#### 1.1 `build-devcontainer`

Builds and caches a devcontainer image to speed up subsequent runs.

- **Guard (critical)**: Runs **only** when the `authorize` job sets `should_run_ai=true`. For issue events this just means the actor is authorized and the event action is one of `opened`, `assigned`, or `unlabeled` on the `ai-started` label. For comment/review events it additionally requires a plain-text `/autoresolve` token in the body (ignoring fenced/inline code and markdown/HTML links) AND the body must not match any known auto-generated review heading.
- **Actions callout**: When the guard skips this job, the workflow summary surfaces the skip reason so maintainers understand why no rebuild occurred and that the container cache remains untouched.
- Pushes to `ghcr.io/nickborgers/home-automation-devcontainer`
- **Skip optimization**: Skips the build entirely when `.devcontainer/` files haven't changed in the PR and the image already exists in GHCR. Falls back to always building for new issues and non-PR contexts.
- **Output**: `should_build_devcontainer` — consumed by downstream steps via conditional `if` guards
- **Why it matters**: The guard keeps routine comments (status updates, reactions, etc.) from burning self-hosted minutes — the container build triggers only when the AI assistant is actually going to run.

#### 1.2 `ai`

Responds to `/autoresolve` invocations in comments, PR reviews, and PR review comments. Does **not** handle issue events — those are routed to `resolve-issue` below.

**Condition**: Executes only when `needs.authorize.outputs.should_run_ai == 'true'` AND the event type is `issue_comment`, `pull_request_review_comment`, or `pull_request_review`. The authorize job has already verified the comment body contains a plain-text `/autoresolve` and does not match an auto-generated review heading, so this job's `if:` is intentionally thin.

**Workflow**:
1. Detects if the context is a PR or issue
2. Checks out the appropriate branch (PR head or default)
3. Runs the AI assistant inside the devcontainer
4. Implements requested changes or responds to questions
5. For PRs: commits and pushes changes to the PR branch
6. For issues: creates branches and opens PRs as needed

**Key Configuration**:
```yaml
bash .devcontainer/run-ai.sh "$PROMPT"
```

`run-ai.sh` is a tool-agnostic wrapper that dispatches to whichever CLI is currently configured. It reads `.devcontainer/ai-tool.env` to decide which tool to invoke and translates the generic `AI_API_KEY` env var into the tool-specific name (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.). See [Swapping the Backing AI Tool](#swapping-the-backing-ai-tool) below.

#### 1.3 `resolve-issue`

Automatically resolves newly opened issues and re-runs on explicit retry requests.

**Condition**: Runs on `issues` events when:
- `action == 'opened'` — every new issue by an authorized actor
- `action == 'assigned'` — assignment is treated as an explicit re-invocation
- `action == 'unlabeled' && label.name == 'ai-started' && issue.state == 'open'` — the retry gesture: removing the `ai-started` label from a still-open issue tells the AI to take another pass

**Workflow**:
1. Labels issue with `ai-started` (idempotent — will no-op if already present)
2. Waits 5 minutes (on `opened` events only) to allow the issue author to refine the description before ingestion
3. Analyzes the issue title, body, and **all comments** on the issue (including any PR review feedback from a previous attempt)
4. Explores the codebase to understand context
5. Implements fixes or features
6. Creates a branch (`ai/issue-{number}`)
7. Opens a PR with the solution

> **Note:** The 5-minute delay (step 2) only fires when `action == opened`. Retries triggered via `assigned` or by removing the `ai-started` label skip the wait entirely.

**`ai-started` is sticky.** Once added, the workflow never automatically removes it. Its only consumer is the retry trigger itself: if you want the AI to try again on the same issue, hand-remove the label and a new `resolve-issue` run fires.

**Timeout**: 120 minutes

### Artifacts

- `ai-conversation-log`: Full conversation log in JSONL format
- Retention: 7 days

---

## 2. AI Code Review (`ai-code-review.yml`)

A sophisticated multi-agent review system that runs after PR tests complete.

### Triggers

- **workflow_run**: When "PR Tests" workflow completes
- **pull_request**: When a PR is reopened (resets reviews to allow re-review)
- **workflow_dispatch**: Manual trigger with PR details
- **issue_comment**: When a PR comment contains a plain-text `/review` token (collaborators/owners/members only)

#### The `/review` command

Posting `/review` as plain text in any PR comment re-triggers the full review cycle. This is useful when you want to force a re-review after making changes without reopening the PR, or when the automatic review was skipped.

Security checks applied before the command takes effect:
- The comment must be on a **PR**, not a plain issue (checked via `pull_request.url` field)
- The `/review` token must appear as **plain text** — not inside a fenced code block, inline code, or HTML `<code>` tag (enforced by `has_plain_token.py`)
- The comment author must be a repository **OWNER**, **MEMBER**, or **COLLABORATOR**

When all checks pass, the `agent-reviews-passed` label is removed so the full review cycle runs again.

### Concurrency Control

```yaml
concurrency:
  group: ai-review-${{ branch_name }}
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
     │                          design-    ai-       test-    concur-   docs-
     │                          review     review    review   rency-    review
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

**Devcontainer caching:** To keep AI runs fast while still picking up infrastructure changes, the `build-devcontainer` job hashes the `.devcontainer/` directory on the `main` branch and publishes the build as both `latest` and `treehash-<hash>` in GHCR. If the tree hash tag already exists, the build is skipped and the existing image is reused.

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
| ai-review | Yes | No code to review |
| test-review | Yes | No tests to review |
| concurrency-review | Yes | No Go code to analyze |
| docs-review | Yes | Config changes don't need doc updates |
| merge-decision | No | Still runs to validate and approve |

The summary comment indicates the PR type and shows reviews as "⏭️ Skipped (config-only)".

### Review Skip Mechanism

To avoid redundant reviews on every push, the workflow uses an `agent-reviews-passed` label:

1. After all reviews complete successfully, the label is added to the PR
2. Subsequent pushes detect the label and skip the full review cycle
3. When a PR is **reopened** or the **/review command** is used, the label is removed to allow re-review

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
7. If tests pass, triggers AI Code Review
8. If tests still fail, triggers another fix attempt (self-retry)
9. After 3 failed attempts, posts a comment to the PR requesting human intervention

**Self-Retry Mechanism**: Because `workflow_run` events don't fire for workflows triggered via `workflow_dispatch`, the fix job manually triggers the next fix attempt when tests still fail. This ensures the retry loop works correctly regardless of how the workflow was initiated.

**Failure Notification**: When all 3 fix attempts are exhausted, a comment is automatically posted to the PR:
- Indicates automated fix failed
- Links to the most recent failed test run
- Requests human intervention

**Labels**: Adds `ai-fix-attempt-N` label to track attempts

**Max Attempts**: 3 (after which human intervention is required)

#### 2.3 `design-review`

Design validation and critical design review specialist. Runs in parallel with other reviews.

**Condition**: Tests passed on current commit (verified via commit status check)

**Pre-Flight Check**: Before running the review, verifies that "All Required Tests" commit status is `success` on the current HEAD. If tests haven't passed, the review is skipped to avoid reviewing broken code that can't be merged anyway.

**Purpose**: This reviewer serves two functions:
1. **Intent Validation**: Two mandatory checks run on every PR:
   - **Alignment check** — did the PR solve the same problem the issue proposed (scope miss)? FAIL is blocking. Both `Alignment check:` and `Scope check:` verdicts always appear at the top of every review comment.
   - **Scope check** — did the PR do too much beyond what was asked (scope creep)?
2. **Design Critique**: Critically reviews the design decisions for quality, trade-offs, and potential issues

**Focus Areas**:
- Does the PR solve the stated problem?
- Are there gaps between requirements and implementation?
- **Alignment check**: Does the PR implement the same algorithm/data sources the linked issue proposed, or does it solve an adjacent/simpler problem? FAIL is blocking. Both `Alignment check:` and `Scope check:` verdicts always appear at the top of every review comment.
- **Scope check** (scope creep): Does the diff match the PR title/issue scope? Flags >300 lines of non-test code beyond what the title implies as BLOCKING scope creep.
- **Production resilience**: New external integrations must have retry logic, appropriate timeouts, and graceful degradation
- **Cross-PR context**: Considers whether the PR contradicts or undermines recently merged changes
- Is this a good design for the problem?
- What predictable challenges or edge cases exist?
- Are there simpler or more robust alternatives?
- Does it fit with existing architecture?

**Reference**: `docs/architecture/ARCHITECTURE.md`, `docs/reference/SHADOW_STATE.md`

**Actions**: Posts design analysis with intent validation, concerns, and suggestions. Does not push fixes (comment-only; merge-decision applies fixes).

#### 2.4 `ai-review`

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

Standard test workflow that gates merging and triggers AI reviews. Runs on the self-hosted homelab runner.

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

## 4. AI Diagnose Workflow Failure (`ai-diagnose-workflow-failure.yml`)

Automatically diagnoses workflow failures to determine if they're GitHub Actions configuration problems (which need issues filed) versus normal test/code failures (which are handled by the existing review pipeline).

### Triggers

- **workflow_run**: When any monitored workflow completes with failure
  - PR Tests
  - Build and Push Docker Image
  - Publish-Screenshots
  - Trigger Private Security Rebuild
  - Notify Private Repo of PR Merge
  - AI Assistant
  - AI Code Review

### Concurrency Control

```yaml
concurrency:
  group: diagnose-failure-${{ workflow_run.id }}
  cancel-in-progress: false
```

### Jobs

#### 4.1 `build-devcontainer`

Builds the devcontainer from the `main` branch when needed, tagging the resulting image with both `latest` and `treehash-<hash>`. If a matching tree hash tag already exists in GHCR, the job skips the rebuild and reuses the cached image, keeping diagnosis runs fast while still updating whenever `.devcontainer/` changes land on `main`.

#### 4.2 `check-failure`

Pre-flight checks before running diagnosis.

**Checks**:
1. Workflow actually failed (not success, cancelled, or skipped)
2. Not diagnosing the diagnosis workflow itself (prevents infinite loops)
3. No existing open issue for this workflow run

**Outputs**: Workflow metadata for the diagnosis job

#### 4.3 `diagnose-failure`

Runs the AI assistant to analyze the failure and classify it.

**Classification Categories**:

| Category | Description | Action |
|----------|-------------|--------|
| `TEST_FAILURE` | Unit tests, integration tests, code compilation | No issue - handled by AI Code Review |
| `CONFIG_FAILURE` | Issues in configs/ (YAML validation, etc.) | No issue - handled by AI Code Review |
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

Runs the AI assistant to perform a three-phase check:

1. **Gather Deprecations**: Fetches HA release notes from the blog, looking for deprecated service calls, entity attributes, WebSocket API changes, and renamed parameters
2. **Scan Codebase**: Searches plugin code and configs for usage of any deprecated APIs found
3. **Report**: Creates one GitHub issue per deprecated API found (with `ha-deprecation` label), skipping duplicates

**Issue Integration**: Issues are created with the standard `ha-deprecation` label; the `resolve-issue` job in `ai-assistant.yml` now fires automatically on every newly opened issue by an authorized actor (no keyword opt-in required), so these issues are picked up and turned into fix PRs as soon as they're created.

**Token Note**: Uses `WORKFLOW_PAT` (not `GITHUB_TOKEN`) so created issues trigger the `resolve-issue` workflow.

**Result Codes**:

| Result | Meaning |
|--------|---------|
| `NO_DEPRECATIONS_FOUND` | Codebase is up to date |
| `ISSUES_CREATED <N>` | Created N issues for deprecated API usage |
| `ALL_DUPLICATES` | Deprecations found but already tracked in existing issues |
| `FETCH_FAILED` | Could not retrieve HA release notes (workflow fails) |
| Unrecognized | Could not parse AI output (workflow fails) |

### Artifacts

- `deprecation-check-output`: Full AI output in JSONL format
- Retention: 30 days (longer than default since this runs monthly)

---

## Required Secrets

| Secret | Used By | Purpose |
|--------|---------|---------|
| `ANTHROPIC_API_KEY` | ai-assistant.yml, ai-code-review.yml, ai-diagnose-workflow-failure.yml, ha-deprecation-check.yml | Authenticates the Claude Code CLI via the self-hosted LiteLLM Anthropic-compatible passthrough |
| `WORKFLOW_PAT` | ai-assistant.yml, ai-code-review.yml, ha-deprecation-check.yml, update-ai-clis.yml | Push workflow file changes, create PRs/issues that trigger workflows |
| `PRIVATE_REPO_TRIGGER_TOKEN` | notify-pr-merged.yml | Cross-repo workflow triggers |

The workflows also pass `ANTHROPIC_BASE_URL=https://llm.featherback-mermaid.ts.net/anthropic/` into the devcontainer so the CLI hits the LiteLLM proxy rather than the public Anthropic API.

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

Required approvals: 0  # AI assistant provides automated review
```

---

## Debugging

### View Conversation Logs

1. Go to the workflow run in GitHub Actions
2. Download the `ai-conversation-log` artifact
3. Parse the JSONL file for the full conversation

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| "Detected newer commits - aborting" | Author pushed while AI was fixing | Normal - AI yields to human |
| Max fix attempts reached | Tests keep failing after 3 tries | Human intervention needed (PR will have a comment) |
| "Automated Fix Failed" comment | All 3 fix attempts exhausted | Review the linked test run, fix manually |
| Review job skipped | Tests haven't passed on current commit | Wait for tests to pass or fix them first |
| Reviews skipped with "already passed" | `agent-reviews-passed` label present | Close and reopen PR to re-run reviews |
| Reviews skipped (draft PR) | PR is marked as draft | Mark PR as "Ready for review" to enable reviews |
| Reviews skipped (config-only) | PR only modifies `configs/` files | Expected - config PRs use streamlined review |
| Devcontainer build slow | First run or cache miss | Subsequent runs use cached image; builds are skipped entirely when `.devcontainer/` files are unchanged |
| AI doesn't respond on a comment | Body missing a plain-text `/autoresolve` token, or token is inside a code block / link | Post a comment with `/autoresolve` as plain text (not inside backticks or a markdown link) |
| AI didn't run on a newly opened issue | Issue author is not an OWNER / MEMBER / COLLABORATOR | Check the `authorize` job logs — external contributors' issues are intentionally skipped |
| Want to re-run AI on an issue after it finished | `resolve-issue` only fires once per issue open | Remove the `ai-started` label from the (still-open) issue — that re-fires `resolve-issue` |
| Workflow failure issue not created | Diagnosed as TEST_FAILURE or CONFIG_FAILURE | Expected - these are handled by AI Code Review |

### Manual Triggers

All workflows support `workflow_dispatch` for manual testing:

```bash
# Trigger AI Code Review manually
gh workflow run "AI Code Review" \
  -f head_ref="your-branch" \
  -f head_repo="owner/repo" \
  -f pr_number="123" \
  -f tests_passed="true" \
  -f run_id="12345678"
```

---

## Architecture Decisions

### Model Selection

The pipelines currently invoke Claude Code CLI with `claude-sonnet-4-6` via the self-hosted LiteLLM Anthropic-compatible passthrough.

| Pipeline/Job | Model | Rationale |
|-------------|-------|-----------|
| `ai-assistant.yml` interactive and issue resolution | `claude-sonnet-4-6` | Open-ended implementation and repo changes |
| `ai-code-review.yml` review and merge jobs | `claude-sonnet-4-6` | Consistent review quality across the pipeline |
| `ai-diagnose-workflow-failure.yml` | `claude-sonnet-4-6` | Failure triage and classification |
| `ha-deprecation-check.yml` | `claude-sonnet-4-6` | Release-note scanning and issue creation |

The workflow display names are `AI Assistant`, `AI Code Review`, and `AI Diagnose Workflow Failure`, with filenames `ai-assistant.yml`, `ai-code-review.yml`, and `ai-diagnose-workflow-failure.yml`. Plumbing is tool-agnostic so the underlying CLI can be swapped without renaming anything.

### Swapping the Backing AI Tool

The pipelines route every CLI invocation through `.devcontainer/run-ai.sh`, which reads `.devcontainer/ai-tool.env` to decide which tool to actually run. Swapping between supported tools is a one-file change:

1. Edit `.devcontainer/ai-tool.env` and change `AI_TOOL` to the desired value (currently `claude` or `codex`). The `AI_BASE_URL` for that tool is selected automatically by the `case` block in the same file.
2. If the new tool needs a different API key, update the `ANTHROPIC_API_KEY` secret value in GitHub Actions settings — the secret name is kept for historical reasons, but the value can hold whatever key the new tool needs. (Workflow env blocks map it to a generic `AI_API_KEY` that `run-ai.sh` re-exports under the tool-specific env var name.)
3. Rebuild the devcontainer image once so the next CI run picks up the new `ai-tool.env`.

What each script does on a swap:

- **`ai-tool.env`** — Single source of truth for `AI_TOOL` + `AI_BASE_URL`. Sourced by both scripts below.
- **`configure-ai.sh`** — Verifies the selected tool's binary is in the devcontainer image, picks a matching bot git identity (`claude[bot]` / `codex[bot]`), and writes any on-disk config the CLI requires (e.g. `~/.codex/config.toml`).
- **`run-ai.sh`** — Takes `$PROMPT` as `$1`, exports the right tool-specific env vars (`ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL` for Claude, `OPENAI_API_KEY` for Codex), and `exec`s the CLI with the right flags.

Adding a new tool (e.g. a different backend) requires adding a `case` arm to both `configure-ai.sh` and `run-ai.sh`, plus baking the tool's binary into the devcontainer Dockerfile. Workflow YAML files do **not** need to change.

### Why Devcontainers?

1. **Consistent Environment**: Same tools and dependencies across all runs
2. **Caching**: Devcontainer image is cached in GHCR for fast startup
3. **Tool Access**: The AI assistant runs with full access to git, gh CLI, make, go, etc.

### Why Parallel Reviews?

The review jobs run in parallel (after context gathering and devcontainer build) to reduce wall-clock time from ~45 min to ~15-20 min. To prevent git conflicts from concurrent pushes:
1. All 5 parallel reviewers are **comment-only** — they have `contents: read` permissions and are explicitly instructed not to push
2. The merge-decision agent is the **sole agent that pushes fixes**, running sequentially after all reviews complete
3. This eliminates the conflict risk entirely while preserving the ability to auto-fix issues

### Why Max 3 Fix Attempts?

- Prevents infinite loops if the issue can't be fixed automatically
- Leaves clear trail via labels (`ai-fix-attempt-1`, etc.)
- Human can review after 3 attempts

### Why Artifact Upload on `always()`?

Conversation logs are uploaded even on failure to help debug:
- What the AI tried
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

### Modifying AI Prompts

The prompts are embedded in the workflow YAML in the `runCmd` block. Key sections:
- `YOUR TASK`: What the AI assistant should do
- `IMPORTANT RULES`: Repository-specific constraints
- Post-task actions: How to report results

---

## Related Documentation

- [CI_CD_SECURITY.md](./CI_CD_SECURITY.md) - **Security model for AI-powered workflows** (authorization, threat model)
- [BRANCH_PROTECTION.md](./BRANCH_PROTECTION.md) - Branch protection setup
- [DOCKER.md](./DOCKER.md) - Container image details
- [../reference/SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Pattern the AI assistant reviews for
- [../reference/CONCURRENCY_LESSONS.md](../reference/CONCURRENCY_LESSONS.md) - Concurrency reviewer reference
