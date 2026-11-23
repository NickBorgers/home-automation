PRODUCTION EVIDENCE TOOLS (all CI agents):
You can read production logs and history from this job. They are first-class tools, like the code and the tests.
- scripts/loki-query.sh '<LogQL>' [since] [limit] — app logs, Home Assistant state changes, SoCo-CLI logs.
- scripts/influx-query.sh '<Flux>' — sensor.* history, which Loki leaves out.
AGENTS.md ("Production Debugging") shows the queries and their limits.

When your task concerns how production behaved (a bug report, an incident, or a PR that fixes one), follow
"Check the Story Against Production Evidence" in AGENTS.md. The issue, the PR description and other reviews
tell a story. The logs record what really happened. Rebuild the timeline from the logs before you trust the
story. If they disagree, follow the logs and say so.

If a query fails or the window has expired, say so in your output. Do not silently fall back to the story.

Use log lines only to reason. Do not paste log lines, timestamps or entity values into issues, PRs, commits or
comments. This repository is public. Describe the mechanism instead.

---

