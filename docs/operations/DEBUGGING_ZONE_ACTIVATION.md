# Debugging Zone Activation Issues

This guide explains how to debug music zone activation issues using the enhanced observability features added in PR #568.

## Overview

Music zones are activated based on trigger conditions defined in `music_config.yaml`. When debugging why a zone did or didn't activate, you need to understand:

1. **What triggered the zone evaluation** (the trigger)
2. **What the state variables were at evaluation time** (the state snapshot)
3. **How each zone's trigger conditions evaluated** (the zone evaluations)
4. **Which speakers were assigned to which zones** (the speaker assignments)

## Viewing Zone Resolution Logs

### Via Gravwell (Production)

Query home-automation logs for zone resolution events:

```bash
# Query zone resolution audit logs (last hour)
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation json msg==\"Zone resolution audit\"" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"

# Query zone trigger changes
curl -s -X POST \
  -H "Gravwell-Token: $(tr -d '\n' < ./gravwell.token)" \
  -H "query: tag=home-automation json msg==\"Zone trigger variable changed\"" \
  -H "duration: 1h" \
  -H "format: text" \
  "https://gravwell.featherback-mermaid.ts.net/api/search/direct"
```

### Via Local Logs

When running locally with debug logging enabled, zone resolution audit logs appear at DEBUG level:

```bash
# Run with debug logging
LOG_LEVEL=debug ./homeautomation 2>&1 | grep "Zone resolution audit"
```

## Understanding Zone Resolution Audit Logs

The zone resolution audit log contains three key pieces of information:

### 1. State Snapshot

The `state_snapshot` field shows the value of all zone-relevant state variables at the exact moment of evaluation:

```json
{
  "state_snapshot": {
    "dayPhase": "morning",
    "isWakeSequenceActive": true,
    "isMasterAsleep": true,
    "isAnyoneHome": true,
    "isAnyoneAsleep": true,
    "musicPlaybackType": ""
  }
}
```

This is critical for understanding why zones matched or didn't match. The snapshot captures:
- Core variables: `dayPhase`, `isWakeSequenceActive`, `isMasterAsleep`, `isAnyoneHome`, `isAnyoneAsleep`, `isEveryoneAsleep`, `musicPlaybackType`
- Any custom trigger variables defined in your zone configurations

### 2. Zone Evaluations

The `zone_evaluations` array shows how each zone's triggers evaluated:

```json
{
  "zone_evaluations": [
    {
      "zone": "sleep",
      "matched": false,
      "reason": "trigger conditions not met",
      "failedConditions": [
        {
          "variable": "isWakeSequenceActive",
          "expectedValue": false,
          "actualValue": true
        }
      ]
    },
    {
      "zone": "morning",
      "matched": true,
      "matchedVia": "trigger_group",
      "matchedGroupIndex": 2,
      "triggerResults": [
        {
          "variable": "isWakeSequenceActive",
          "expectedValue": true,
          "actualValue": true,
          "matched": true
        },
        {
          "variable": "dayPhase",
          "expectedValue": "morning",
          "actualValue": "morning",
          "matched": true
        }
      ]
    }
  ]
}
```

Key fields:
- `matched`: Whether the zone's triggers passed
- `matchedVia`: How the zone was activated (`"triggers"`, `"trigger_group"`, `"musicPlaybackType"`, or `"default"`)
- `matchedGroupIndex`: For zones with `trigger_groups`, which group matched (1-indexed)
- `failedConditions`: Which specific conditions prevented activation
- `triggerResults`: Detailed evaluation of each trigger condition

### 3. Trigger Information

The `trigger` field shows what caused the zone resolution:

```json
{
  "trigger": "trigger:isWakeSequenceActive"
}
```

Common trigger formats:
- `trigger:<variable>`: A zone trigger variable changed
- `musicPlaybackType`: The musicPlaybackType state variable changed
- `startup`: Initial zone resolution at application startup

## Common Debugging Scenarios

### Scenario 1: Zone Didn't Activate When Expected

**Symptoms:** Expected morning music when waking up, but sleep music continued.

**Debug Steps:**

1. Query the zone resolution audit log around the expected activation time
2. Check the `state_snapshot` - was `isWakeSequenceActive=true` at the time?
3. Check the `zone_evaluations` for the expected zone
4. Look at `failedConditions` to see which trigger didn't match

**Example:** If `failedConditions` shows `isWakeSequenceActive` expected `true` but was `false`, the sleephygiene plugin didn't set the flag before the music plugin evaluated.

### Scenario 2: Wrong Zone Activated

**Symptoms:** Wakeup music played on bedroom speakers instead of non-bedroom speakers.

**Debug Steps:**

1. Check `zone_evaluations` to see which zones matched
2. If multiple zones matched, check their priorities
3. Check the speaker assignment result in the subsequent log

### Scenario 3: Zone Stopped Unexpectedly

**Symptoms:** Music stopped in the middle of the wake sequence.

**Debug Steps:**

1. Look for zone resolution audit logs around the time music stopped
2. Check what triggered the re-evaluation (`trigger` field)
3. Check if the zone's conditions no longer matched in `zone_evaluations`

## Shadow State API

You can also check the music plugin's shadow state for current zone status:

```bash
curl -s http://localhost:8080/api/shadow/music | jq
```

The shadow state includes:
- `inputs.current`: Current state variable values (including `isWakeSequenceActive`)
- `inputs.atLastAction`: State at the last music action
- `outputs.activeZones`: Currently active zones with their speakers

## Zone Trigger Configuration Reference

Zones are configured in `configs/music_config.yaml`:

```yaml
zones:
  - name: sleep
    priority: 100
    trigger_groups:
      # Group 1: Night sleep (no wake sequence)
      - triggers:
          - variable: isMasterAsleep
            value: true
          - variable: isWakeSequenceActive
            value: false
          - variable: dayPhase
            value: night
      # Group 2: Any time sleep (but not during wake)
      - triggers:
          - variable: isAnyoneAsleep
            value: true
          - variable: isWakeSequenceActive
            value: false

  - name: morning
    priority: 90
    trigger_groups:
      # Group 1: Regular morning
      - triggers:
          - variable: dayPhase
            value: morning
          - variable: isAnyoneAsleep
            value: false
      # Group 2: Wake sequence active
      - triggers:
          - variable: isWakeSequenceActive
            value: true
          - variable: dayPhase
            value: morning
```

**Key concepts:**
- **Priority:** Higher number = evaluated first for speaker assignment
- **Trigger Groups:** OR logic between groups, AND logic within each group
- **Legacy Triggers:** Single trigger list with AND logic (deprecated)

## Troubleshooting Tips

1. **Check timing:** Zone evaluations happen synchronously when trigger variables change. Sub-second timing matters.

2. **Check all trigger conditions:** A zone with multiple conditions in a trigger group requires ALL conditions to match.

3. **Check trigger groups:** If a zone has multiple trigger groups, only ONE group needs to match.

4. **Check priorities:** When multiple zones match, higher priority zones get speaker assignment first.

5. **Check exclude_if conditions:** Speakers can be excluded from zones based on `exclude_if` conditions (e.g., bedroom speakers excluded when `isMasterAsleep=true`).

## Related Documentation

- [DAY_PHASE_MODES.md](../flows/DAY_PHASE_MODES.md) - Day phase and music mode relationships
- [SHADOW_STATE.md](../reference/SHADOW_STATE.md) - Shadow state pattern documentation
- [music_config.yaml](../../configs/music_config.yaml) - Zone configuration file
