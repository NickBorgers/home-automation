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
3. Decide whether this PR caused the failure. Follow "SCOPE" below before you change any code.
4. Fix the issues in the code
5. Run `make unit-tests` locally to verify your fix works
6. Commit and push your fixes

SCOPE: FIX ONLY WHAT THIS PR BROKE
This PR's changes are the files in: gh pr diff ${PR_NUMBER} --name-only
A failure is out of scope when both of these are true:
- The failing test, and the package it tests, are not in that list.
- Nothing in the PR's diff plausibly changes what that test does.
A random (flaky) failure in another plugin is the usual case. Example: PR #1383 changed the vacuum plugin, a
`tv` test failed at random, and a fix pushed into #1383 conflicted with a separate fix that merged to main.

For an out-of-scope failure, do NOT edit code and do NOT push. Instead:
1. Check for an open issue or PR about that test:
   gh issue list --state open --search "<TestName>"
   gh pr list --state open --search "<TestName>"
2. If there is none, open an issue. Name the test, quote the failure, and link run ${RUN_ID}.
3. Re-run only the failed jobs once: gh run rerun ${RUN_ID} --failed
   A passing re-run lets the reviews start by themselves.
4. Post this comment, then stop:
   gh pr comment ${PR_NUMBER} --body '## Fix Attempt ${ATTEMPT}/3 - Not caused by this PR

[Name the failing test and why it is unrelated to this PR]

Tracked in #<issue>. Re-ran the failed jobs. No changes pushed.

<!-- ai-fix-out-of-scope:${TRIGGERING_SHA} -->'

If some failures are in scope and some are not, fix only the in-scope ones and handle the others as above.

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
