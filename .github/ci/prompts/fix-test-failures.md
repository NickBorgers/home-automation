You are fixing CI test failures for PR #${PR_NUMBER} in ${REPO}.
This is fix attempt ${ATTEMPT} of 3.

CRITICAL: BEFORE PUSHING ANY CHANGES, you MUST check if the PR author has pushed
newer commits since this workflow started. Run:
  git fetch origin ${HEAD_REF}
  CURRENT_HEAD=$(git rev-parse origin/${HEAD_REF})
  if [ "$CURRENT_HEAD" != "${TRIGGERING_SHA}" ]; then
    echo 'Author has pushed newer commits - attempting to merge'
    # Try to rebase our changes on top of the author's commits
    git stash 2>/dev/null || true
    if git pull --rebase origin ${HEAD_REF}; then
      git stash pop 2>/dev/null || true
      echo 'Successfully rebased on top of author commits - continuing with fix'
    else
      # Rebase failed - abort and let author handle it
      git rebase --abort 2>/dev/null || true
      git stash pop 2>/dev/null || true
      gh pr comment ${PR_NUMBER} --body '## Fix Attempt ${ATTEMPT}/3 - Aborted

Detected newer commits pushed by author. Attempted to merge but encountered conflicts.
Skipping automated fix to avoid overwriting author changes.
The author is likely fixing this themselves.'
      exit 0
    fi
  fi

Proceed with fixing:

ORIGINAL ISSUE (if applicable):
Issue #${ISSUE_NUMBER}: ${ISSUE_TITLE}
${ISSUE_BODY}

PR DESCRIPTION:
${PR_TITLE}
${PR_BODY}

The tests failed in the PR Tests workflow (run ID: ${RUN_ID}). Your task:
1. Check the workflow run logs: gh run view ${RUN_ID} --log-failed
2. Identify the root cause of failures (failing tests, coverage < 65%, style issues, etc.)
3. Fix the issues in the code
4. Run `make unit-tests` locally to verify your fix works
5. Commit and push your fixes

IMPORTANT RULES FOR THIS REPOSITORY:
- Use `make unit-tests` NOT `go test` (caching)
- Use `make integration-tests` for integration tests
- Use `make pre-commit` for linting/formatting
- Follow shadow state pattern (docs/reference/SHADOW_STATE.md)
- Never use `git push --no-verify`
- If this is attempt 2+, review what was tried before and try a different approach

After pushing, post a brief status update:
gh pr comment ${PR_NUMBER} --body '## Fix Attempt ${ATTEMPT}/3

[Describe what you found and fixed]

Pushed fix - triggering new test run.'
