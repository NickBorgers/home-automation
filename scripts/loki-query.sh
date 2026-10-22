#!/usr/bin/env bash
# Query production logs in Loki. Keeps the newest <limit> matches, printed oldest-first as "<UTC time> <line>".
#
# Usage: scripts/loki-query.sh '<LogQL>' [since] [limit]
#   scripts/loki-query.sh '{service="home-automation"} |= "Kitchen"' 2h
#   scripts/loki-query.sh '{service="home-automation"} | json | level="error"' 1h
#
# Services: home-automation, home-assistant, soco-cli, ci-runner.
# Needs tailnet access. No token. Override the server with LOKI_URL.
set -euo pipefail

query="${1:?usage: $0 '<LogQL>' [since] [limit]}"
since="${2:-1h}"
limit="${3:-500}"
loki_url="${LOKI_URL:-https://loki.featherback-mermaid.ts.net}"

curl -sf -m 60 -G "${loki_url}/loki/api/v1/query_range" \
  --data-urlencode "query=${query}" \
  --data-urlencode "since=${since}" \
  --data-urlencode "limit=${limit}" \
  --data-urlencode "direction=backward" |
  jq -r 'if .status == "error" then error("Loki query error: \(.error)")
         else [.data.result[].values[]] | sort_by(.[0] | tonumber)[]
           | "\(.[0] | tonumber / 1e9 | strftime("%Y-%m-%dT%H:%M:%SZ")) \(.[1])"
         end'
