#!/usr/bin/env bash
# Query long-run Home Assistant history in InfluxDB with Flux. Prints the CSV that InfluxDB returns.
#
# Usage: scripts/influx-query.sh '<Flux>'
#   scripts/influx-query.sh 'from(bucket: "homeassistant") |> range(start: -1h)
#     |> filter(fn: (r) => r.entity_id == "example_humidity" and r._field == "value")'
#
# Buckets: homeassistant (full detail, 1 year), homeassistant_longterm (downsampled, kept forever).
# Needs tailnet access. No token. Read-only. Override the server with INFLUX_URL.
set -euo pipefail

query="${1:?usage: $0 '<Flux>'}"
influx_url="${INFLUX_URL:-https://influxdb.featherback-mermaid.ts.net}"
influx_org="${INFLUX_ORG:-c4fe3cad96baf8db}"

curl -sSf -m 60 -X POST "${influx_url}/api/v2/query?org=${influx_org}" \
  -H 'Content-Type: application/vnd.flux' \
  -H 'Accept: application/csv' \
  --data-binary "${query}"
