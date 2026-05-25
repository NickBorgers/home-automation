You are an autonomous agent resolving a merge conflict on PR #${PR_NUMBER} in ${REPO}.

PR TITLE: ${PR_TITLE}

PR BODY:
${PR_BODY}

LINKED ISSUE CONTEXT:
Issue #${ISSUE_NUMBER}: ${ISSUE_TITLE}
${ISSUE_BODY}

PR COMMENTS AND REVIEW CONTEXT:
${PR_REVIEW_CONTEXT}

PR HEAD BRANCH: ${HEAD_REF}
BASE BRANCH:    ${BASE_REF}
TRIGGERING SHA: ${TRIGGERING_SHA}

CRITICAL: BEFORE PUSHING ANY CHANGES, verify the PR head SHA hasn't moved
since this workflow started. Run:

  git fetch origin ${HEAD_REF}
  CURRENT_HEAD=$(git rev-parse origin/${HEAD_REF})
  if [ "$CURRENT_HEAD" != "${TRIGGERING_SHA}" ]; then
    git rebase --abort 2>/dev/null || true
    gh pr comment ${PR_NUMBER} --body 'Aborted automated conflict resolution: the PR head moved (${TRIGGERING_SHA} → '"$CURRENT_HEAD"') after this workflow started. Re-run /autoresolve once your push has settled if you still want help.'
    exit 0
  fi

YOUR TASK:

1. Confirm the PR is still conflicting:
     gh pr view ${PR_NUMBER} --json mergeStateStatus,mergeable
   If mergeStateStatus is anything other than DIRTY (e.g., CLEAN, BEHIND, BLOCKED, UNSTABLE),
   comment on the PR explaining there is no conflict to resolve and exit cleanly.

2. Check out the PR head:
     git fetch origin ${HEAD_REF}
     git checkout ${HEAD_REF}
     git fetch origin ${BASE_REF}

3. Rebase onto the latest base. Prefer rebase over merge to keep ai/issue-XXXX branches linear:
     git rebase origin/${BASE_REF}

4. For each conflicted file, resolve in-context:
   - Read both sides of the conflict markers AND the surrounding code on the new base.
   - Write a resolution that preserves the PR's stated intent (re-read the PR body / linked
     issue and PR review comments above if you are unsure what behavior the PR is meant to deliver).
   - Apply the same scope discipline as a fresh implementation: minimum diff, no
     refactors, no unrelated cleanups.
   - Then: git add <file> && git rebase --continue. Loop until the rebase reports clean.

5. If the rebase cannot be completed because the PR's intent has been superseded by what
   landed on ${BASE_REF} (e.g. the bug it fixes was already fixed differently, or the
   feature was removed), abort and let a human decide:
     git rebase --abort
     gh pr comment ${PR_NUMBER} --body '<explain why automated resolution is not safe and recommend close/redo>'
     exit 0

6. Validate locally before pushing:
     make unit-tests
   If tests fail because of the rebase, fix them while staying scoped to the PR's intent.
   Re-run until clean. Do not skip hooks. Do not lower coverage thresholds.

7. Push the rebased branch:
     git push --force-with-lease origin ${HEAD_REF}
   --force-with-lease will fail if anything has been pushed to the branch since you fetched
   it; treat that failure as the "author pushed newer commits" path above and abort cleanly
   with a comment.

8. Post a status comment on the PR:
     gh pr comment ${PR_NUMBER} --body 'Rebased ${HEAD_REF} onto ${BASE_REF}.

   Conflicts resolved:
   - <file>: <one-line description of how each conflict was resolved>

   Verification: make unit-tests passed.
   New head SHA: <new sha>'

IMPORTANT RULES FOR THIS REPOSITORY:
- Use `make unit-tests` NOT `go test` (caching).
- Use `make integration-tests` if integration tests are affected by the rebase.
- Use `make pre-commit` for linting/formatting if you had to touch source files.
- Follow the shadow state pattern (docs/reference/SHADOW_STATE.md).
- Never use `git push --no-verify`.
- Write the minimum change that resolves the conflict. Do NOT use the rebase as an excuse
  to refactor adjacent code or expand the PR's scope.
- Do not re-implement parts of the PR — your job is to reconcile the existing diff with
  the new base, not redesign the feature.

If you cannot resolve the conflict safely, leave the branch untouched and comment
explaining what you found. A skipped rebase is always better than a wrong rebase.
