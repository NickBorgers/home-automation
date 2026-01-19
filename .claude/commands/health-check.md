# Health Check

Perform a comprehensive health check of the home automation system by reviewing logs, shadow state, and comparing with Home Assistant.

## Step 1: Check Application Logs for Errors

Query for any error-level logs in the past hour:

```bash
# Error logs
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation json level==error" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"

# Warning logs
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation json level==warn" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"
```

## Step 2: Review Shadow State for All Plugins

Fetch the complete shadow state to verify all plugins are functioning:

```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow" | jq .
```

Check for:
- **Stale timestamps**: Any `last_updated` more than 15 minutes old indicates a problem
- **Empty inputs**: Plugins should have populated input sections
- **Missing plugins**: All expected plugins should be present (music, lighting, presence, energy, schedule, environmental)

## Step 3: Compare with Home Assistant State

Use the Home Assistant API to compare actual entity states with what the shadow state shows:

```bash
# Get HA token
HA_TOKEN=$(cat ./token)

# Check key entities - adjust entity IDs as needed
# Presence sensors
curl -s -H "Authorization: Bearer $HA_TOKEN" \
  "https://home-assistant.featherback-mermaid.ts.net/api/states/person.nick" | jq '{entity_id, state}'

curl -s -H "Authorization: Bearer $HA_TOKEN" \
  "https://home-assistant.featherback-mermaid.ts.net/api/states/person.caroline" | jq '{entity_id, state}'

# Energy/Solar
curl -s -H "Authorization: Bearer $HA_TOKEN" \
  "https://home-assistant.featherback-mermaid.ts.net/api/states/sensor.span_panel_current_power_production" | jq '{entity_id, state}'

# Check if HA WebSocket is connected by looking at recent activity
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation grep websocket" \
  -H "duration: 30m" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"
```

## Step 4: Check Recent Entity State Changes

Verify Home Assistant is sending state changes:

```bash
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-assistant" \
  -H "duration: 15m" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"
```

If no recent changes, the WebSocket connection may be down.

## Step 5: Verify Plugin-Specific Health

### Music Plugin
```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/music" | jq .
```
- Check: `musicPlaybackType`, `currentlyPlayingMusic` values are sensible

### Lighting Plugin
```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/lighting" | jq .
```
- Check: `dayPhase` matches expected time of day, `sunevent` is current

### Presence Plugin
```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/presence" | jq .
```
- Check: `isNickHome`, `isCarolineHome` match actual presence

### Energy Plugin
```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/energy" | jq .
```
- Check: `batteryEnergyLevel`, `currentEnergyLevel` are reasonable values

### Schedule Plugin
```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/schedule" | jq .
```
- Check: `isMasterAsleep` matches expected state, alarm times are set

### Environmental Plugin
```bash
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/environmental" | jq .
```
- Check: Humidity sensors are being tracked, values are reasonable

## Health Check Summary

After running all checks, summarize:

1. **Application Status**: Any errors or warnings in logs?
2. **WebSocket Connection**: Is HA connection active?
3. **Plugin Status**: Are all plugins reporting current data?
4. **State Consistency**: Does shadow state match HA reality?
5. **Recommendations**: Any issues that need attention?

## Quick Health Check (Single Command)

For a fast overview:

```bash
echo "=== Errors (last hour) ===" && \
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation json level==error" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct" && \
echo -e "\n=== Shadow State Summary ===" && \
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow" | jq 'to_entries | .[] | {plugin: .key, last_updated: .value.metadata.last_updated}'
```
