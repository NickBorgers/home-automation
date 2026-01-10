# Branch Protection Rules

This document explains how to configure GitHub branch protection rules to enforce test requirements before merging pull requests.

## Overview

The repository uses GitHub Actions to automatically run tests on all pull requests. Branch protection rules ensure that PRs cannot be merged until all required tests pass.

## Required Status Checks

The following CI workflows must pass before a PR can be merged:

### 1. **All Required Tests** (Recommended)
   - **Workflow**: `PR Tests > All Required Tests`
   - **File**: `.github/workflows/pr-tests.yml`
   - **Checks**:
     - ✅ Go unit tests with race detector
     - ✅ Test coverage ≥70%
     - ✅ Integration tests (11/11 passing)
     - ✅ Config validation (YAML, Spotify URIs)

### 2. **Individual Test Jobs** (Alternative)
   If you prefer granular control, require these individual jobs:
   - `PR Tests > Go Tests (Required for PR Merge)`
   - `PR Tests > Config Validation Tests`

## Configuring Branch Protection

### For Repository Administrators

1. **Navigate to Settings**
   - Go to repository **Settings** → **Branches**

2. **Add Branch Protection Rule**
   - Click **Add rule** or **Add branch protection rule**
   - Branch name pattern: `main` (or `master`)

3. **Configure Protection Settings**

   #### Required Settings:
   - ✅ **Require a pull request before merging**
     - Optionally: Require approvals (1 or more reviewers)

   - ✅ **Require status checks to pass before merging**
     - ✅ **Require branches to be up to date before merging**
     - **Add required status checks**:
       - Search for and select: `All Required Tests`
       - OR individually select:
         - `Go Tests (Required for PR Merge)`
         - `Config Validation Tests`

   #### Recommended Settings:
   - ✅ **Require conversation resolution before merging**
   - ✅ **Do not allow bypassing the above settings**
   - ⚠️ Consider: **Require linear history** (prevents merge commits)

4. **Save Changes**
   - Click **Create** or **Save changes**

## Verifying Configuration

After configuring branch protection:

1. **Create a test PR** with intentionally failing tests
2. **Verify that the merge button is blocked** until tests pass
3. **Check the status checks section** shows required tests

## Test Workflow Details

### What Gets Tested

#### Go Tests (`homeautomation-go/`)
```bash
# Unit tests with race detector
go test ./... -race

# Coverage check (minimum 70%)
go test ./... -race -coverprofile=coverage.out

# Integration tests (concurrent load, deadlocks, race conditions)
go test -v -race ./test/integration/...
```

#### Config Tests
```bash
# YAML validation
make run-yamllint-music
make run-yamllint-hue

# Spotify URI validation
make run-spotify-validation-music
```

## For Developers

### Before Creating a PR

Run the same checks locally that CI will run:

```bash
cd homeautomation-go

# 1. Ensure everything compiles
go build ./...

# 2. Run all tests
go test ./...

# 3. Run with race detector
go test -race ./...

# 4. Check coverage
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 5. Run integration tests
go test -v -race ./test/integration/...
```

Or use the one-liner from `../../AGENTS.md`:
```bash
cd homeautomation-go && go build ./... && go test ./... && echo "✅ Ready to push"
```

### What Happens on PR Creation

1. **GitHub Actions triggers** the `PR Tests` workflow
2. **Tests run automatically** on every commit to the PR
3. **Status checks appear** on the PR page:
   - 🟡 Yellow: Tests running
   - 🟢 Green: All tests passed
   - 🔴 Red: Tests failed
4. **Merge button**:
   - Enabled when all required tests pass ✅
   - Blocked when tests fail or are running ❌

### If Tests Fail

1. **Review the workflow logs** in the Actions tab
2. **Fix the failing tests** locally
3. **Push the fixes** to the PR branch
4. **CI will re-run** automatically

## Troubleshooting

### "Merge button is blocked but tests passed"

**Possible causes**:
- Tests passed but branch is not up to date with base branch
- Required status check names don't match configured names
- Status checks haven't completed yet

**Solutions**:
- Update branch: Click "Update branch" button
- Verify status check names in Settings → Branches
- Wait for all checks to complete

### "Can't find status checks to require"

**Cause**: Status checks only appear after they've run at least once

**Solution**:
1. Merge this PR first (which adds the workflow)
2. Create another PR to trigger the workflow
3. The status checks will then be available to select

### "Tests pass locally but fail in CI"

**Common causes**:
- Go version mismatch (CI uses Go 1.23)
- Missing dependencies in `go.mod`/`go.sum`
- Race conditions only visible under CI load
- Environment-specific issues

**Solutions**:
```bash
# Ensure Go 1.23
go version

# Update dependencies
go mod tidy

# Run with race detector (CI does this)
go test -race ./...

# Check CI logs for specific error messages
```

## Workflow Files Reference

| Workflow | File | Purpose | Trigger |
|----------|------|---------|---------|
| PR Tests | `.github/workflows/pr-tests.yml` | **Primary PR testing** (Go tests + config validation) | All PRs, pushes to main/master/claude/** |
| Build and Push | `.github/workflows/docker-build-push.yml` | Docker build + push to GHCR | Pushes to main/master, version tags |

## Benefits of This Setup

✅ **Prevents broken code from being merged**
- All tests must pass before merge
- Catches issues before they reach main branch

✅ **Enforces code quality standards**
- Minimum 70% test coverage
- Race condition detection
- Integration test validation

✅ **Clear feedback for developers**
- See exactly which tests failed
- Links to workflow logs
- Automatic re-testing on new commits

✅ **Protects main branch stability**
- Main branch always has passing tests
- Reduces rollback frequency
- Maintains production readiness

## Related Documentation

- [AGENTS.md](../../AGENTS.md) - Development standards and test guide
- [homeautomation-go/test/integration/README.md](../../homeautomation-go/test/integration/README.md) - Integration test details
- [CONCURRENCY_LESSONS.md](../reference/CONCURRENCY_LESSONS.md) - Concurrency patterns and lessons learned

## Questions?

For help with branch protection configuration:
- See GitHub's [Branch Protection Rules Documentation](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/defining-the-mergeability-of-pull-requests/about-protected-branches)
- Check repository Settings → Branches for current rules
- Review workflow logs in Actions tab for test failures

---

**Last Updated**: 2025-11-15
