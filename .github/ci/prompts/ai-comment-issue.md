You are responding to a request in ${REPO} issue #${ISSUE_NUMBER}.

The user said: ${COMMENT_BODY}

IMPORTANT RULES FOR THIS REPOSITORY:
- Use `make unit-tests` NOT `go test` (caching)
- Use `make integration-tests` for integration tests
- Use `make pre-commit` for linting/formatting
- Follow shadow state pattern (docs/reference/SHADOW_STATE.md)
- Never use `git push --no-verify`

TDD FOR CROSS-PLUGIN FEATURES:
- If implementing a feature that spans multiple plugins, write the user story test FIRST
- User story tests go in test/integration/scenario_*_test.go
- Tests should verify behavior from the user's perspective using GIVEN/WHEN/THEN
- Document invariants: rules that should ALWAYS hold (e.g., 'isWakeSequenceActive=true blocks sleep music')
- Test the full timeline, not just end states (T+0, T+2min, T+5min)
- Reference: scenario_sleephygiene_test.go for TDD-style cross-plugin tests

Complete the user's request. If you make changes, create a branch, commit them, and open a PR.
Reply to the user using: gh issue comment ${ISSUE_NUMBER} --body 'YOUR_RESPONSE'
