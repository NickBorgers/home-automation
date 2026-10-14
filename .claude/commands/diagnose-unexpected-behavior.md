# Diagnose Unexpected Behavior

The user observed unexpected behavior in the home automation system. Help diagnose the issue.

## Context from User

$ARGUMENTS

## Diagnostic Process

Follow this order strictly - production evidence first, then code investigation:

### Step 1: Gather Production Logs

Query Loki for recent logs from both the Go application and Home Assistant (needs tailnet access, no token):

```bash
# Home automation Go application logs (last 1 hour)
scripts/loki-query.sh '{service="home-automation"}' 1h

# Home Assistant entity state changes (last 1 hour)
scripts/loki-query.sh '{service="home-assistant"}' 1h
```

If looking for specific patterns, add a line filter (`|=` contains, `|~` regex) or parse the JSON fields:
```bash
scripts/loki-query.sh '{service="home-automation"} |= "KEYWORD"' 2h
scripts/loki-query.sh '{service="home-automation"} | json | logger="lighting" | room="Kitchen"' 2h
```

Output is oldest-first. The leading time is when Loki ingested the line and can trail the app's own `ts` by a few seconds, so trust `ts` inside the line when ordering events.

### Step 2: Check Current Shadow State

Fetch the shadow state to understand what the system currently believes:

```bash
# All plugins shadow state
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow" | jq .

# Specific plugin (music, lighting, presence, energy, schedule, environmental)
curl -s "https://home-automation.featherback-mermaid.ts.net/api/shadow/PLUGIN_NAME" | jq .
```

### Step 3: Correlate Logs with Expected Behavior

Based on the user's description:
1. Identify the relevant time window
2. Find the triggering events in logs
3. Trace the decision chain through the plugins
4. Identify where the actual behavior diverged from expected

### Step 4: Investigate Code (Only After Log Analysis)

Once you have a hypothesis from logs, look at the relevant code:

- Plugin implementations: `homeautomation-go/internal/plugins/`
- State management: `homeautomation-go/internal/state/`
- Configuration files: `configs/`
- Flow documentation: `docs/flows/`

Key files by plugin:
- Music: `internal/plugins/music/manager.go`, `configs/music_config.yaml`
- Lighting: `internal/plugins/lighting/manager.go`, `configs/hue_config.yaml`
- Presence: `internal/plugins/presence/manager.go`
- Energy: `internal/plugins/energy/manager.go`, `configs/energy_config.yaml`
- Schedule: `internal/plugins/schedule/manager.go`, `configs/schedule_config.yaml`
- Environmental: `internal/plugins/environmental/manager.go`

### Step 5: Summarize Findings

Provide a clear summary:
1. What the user expected to happen
2. What actually happened (based on logs)
3. Why it happened (root cause from code analysis)
4. Recommended fix or explanation

## Common Diagnostic Patterns

- **"X should have turned on but didn't"**: Check if the triggering condition was met, look for the entity state change in HA logs, then trace through the plugin logic
- **"Y happened when it shouldn't have"**: Find the action in logs, trace backwards to find what triggered it
- **"Timing was wrong"**: Check schedule plugin state, day phase calculations, sun event times
- **"State seems stuck"**: Compare shadow state inputs vs outputs, look for missing state updates in logs
