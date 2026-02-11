# Shadow State Architecture

This document describes the shadow state pattern used in the Go home automation system. Shadow state provides observability into plugin behavior by tracking inputs, outputs, and computation timestamps.

## Purpose

Shadow state serves three key purposes:

1. **Debugging**: When a plugin produces unexpected output, shadow state shows exactly what inputs triggered the computation
2. **Observability**: The `/api/shadow/{plugin}` endpoints expose real-time plugin state for monitoring
3. **Audit Trail**: By capturing inputs "at last action", we can understand why a specific action was taken

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Plugin Manager                                │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  ┌──────────────┐    ┌──────────────┐    ┌───────────────────┐     │
│  │  HA Client   │───►│   Handler    │───►│  Shadow Tracker   │     │
│  │ (raw input)  │    │  (business   │    │  (records state)  │     │
│  └──────────────┘    │   logic)     │    └───────────────────┘     │
│                      └──────────────┘              │                │
│                             │                      │                │
│                             ▼                      ▼                │
│                      ┌──────────────┐    ┌───────────────────┐     │
│                      │ State Manager│    │  API Endpoint     │     │
│                      │ (computed    │    │  /api/shadow/X    │     │
│                      │  outputs)    │    └───────────────────┘     │
│                      └──────────────┘                              │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

## Shadow State Structure

Every plugin's shadow state follows this structure:

```go
type PluginShadowState struct {
    Plugin   string                 `json:"plugin"`
    Inputs   PluginInputs           `json:"inputs"`
    Outputs  PluginOutputs          `json:"outputs"`
    Metadata ShadowMetadata         `json:"metadata"`
}

type PluginInputs struct {
    Current      map[string]interface{} `json:"current"`      // Current input values
    AtLastAction map[string]interface{} `json:"atLastAction"` // Inputs when last action taken
}

type ShadowMetadata struct {
    LastUpdated time.Time `json:"lastUpdated"`
    PluginName  string    `json:"pluginName"`
}
```

## Implementation Requirements

### Track Raw Inputs

**Plugins SHOULD update shadow state inputs when receiving data from Home Assistant or state changes.** Currently, `dayphase` and `sleephygiene` implement full `updateShadowInputs()` tracking. Other plugins track outputs via their shadow trackers but do not yet capture raw inputs.

When adding or modifying a plugin, implementing `updateShadowInputs()` is recommended for debugging and observability. It is not currently enforced across all plugins.

A common bug when implementing shadow state: plugins update outputs but forget to track the raw inputs that triggered the computation.

### Recommended Pattern

```go
// CORRECT: Update shadow inputs at start of every handler
func (m *Manager) handleSomeChange(entityID string, oldState, newState *ha.State) {
    if newState == nil {
        return
    }

    // 1. FIRST: Update shadow state inputs
    m.updateShadowInputs()

    // 2. Then: Process the change and compute outputs
    computed := m.computeSomething(newState.State)

    // 3. Update state variables
    m.stateManager.SetString("someOutput", computed)

    // 4. Update shadow state outputs
    m.shadowTracker.UpdateSomeOutput(computed)
}

// updateShadowInputs captures current input values
func (m *Manager) updateShadowInputs() {
    inputs := make(map[string]interface{})

    // Capture ALL subscribed state variables and HA entities
    if val, err := m.stateManager.GetBool("someInput"); err == nil {
        inputs["someInput"] = val
    }
    if state, err := m.haClient.GetState("sensor.something"); err == nil && state != nil {
        inputs["sensor.something"] = state.State
    }

    m.shadowTracker.UpdateCurrentInputs(inputs)
}
```

### Anti-Pattern (BUG)

```go
// WRONG: Only updates outputs, forgets inputs
func (m *Manager) handleSomeChange(entityID string, oldState, newState *ha.State) {
    if newState == nil {
        return
    }

    // BUG: No call to m.updateShadowInputs()!

    computed := m.computeSomething(newState.State)
    m.stateManager.SetString("someOutput", computed)

    // Only updating outputs leaves inputs.current empty
    m.shadowTracker.UpdateSomeOutput(computed)
}
```

## Plugin-Specific Trackers

Each plugin type has a dedicated tracker in `internal/shadowstate/tracker.go`:

| Plugin | Tracker | Key Outputs |
|--------|---------|-------------|
| Lighting | `LightingTracker` | Room states, active scenes |
| Security | `SecurityTracker` | Lockdown state, doorbell events |
| LoadShedding | `LoadSheddingTracker` | Active state, thermostat settings |
| SleepHygiene | `SleepHygieneTracker` | Wake sequence, fade-out progress |
| Energy | `EnergyTracker` | Energy levels, sensor readings |
| StateTracking | `StateTrackingTracker` | Derived states, timer states |
| DayPhase | `DayPhaseTracker` | Sun event, day phase |
| TV | `TVTracker` | TV state, HDMI input |
| Music | Embedded in state | Playback type, current music |

## Tracker Methods

### Standard Methods (all trackers)

```go
// Update current inputs (call in every handler)
tracker.UpdateCurrentInputs(map[string]interface{}{
    "inputName": value,
})

// Snapshot inputs when taking action (for audit trail)
tracker.SnapshotInputsForAction()

// Get current state (thread-safe copy)
state := tracker.GetState()
```

### Specialized Methods (per tracker)

Each tracker has methods specific to its outputs:

```go
// Energy tracker
tracker.UpdateBatteryPercentage(pct)
tracker.UpdateBatteryLevel(level)
tracker.UpdateSolarLevel(level)
tracker.UpdateOverallLevel(level)
tracker.UpdateFreeEnergyAvailable(available)
tracker.UpdateGridAvailable(available)
tracker.UpdateThisHourSolarKW(kw)
tracker.UpdateRemainingSolarKWH(kwh)

// Lighting tracker
tracker.RecordRoomAction(roomName, actionType, reason, activeScene, turnedOff)

// Security tracker
tracker.RecordLockdownAction(active, reason)
tracker.RecordDoorbellEvent(rateLimited, ttsSent, lightsFlashed)
```

## API Endpoints

Shadow state is exposed via REST API:

```bash
# Get all plugin states
GET /api/shadow

# Get specific plugin state
GET /api/shadow/lighting
GET /api/shadow/energy
GET /api/shadow/security
# etc.
```

## Testing Shadow State

When writing tests, verify that shadow state is properly updated:

```go
func TestHandler_UpdatesShadowState(t *testing.T) {
    manager := NewManager(...)

    // Trigger handler
    manager.handleSomeChange("entity", nil, &ha.State{State: "on"})

    // Verify shadow state
    state := manager.GetShadowState()

    // Check inputs were captured
    assert.NotEmpty(t, state.Inputs.Current)
    assert.Contains(t, state.Inputs.Current, "expectedInput")

    // Check outputs were updated
    assert.Equal(t, "expectedValue", state.Outputs.SomeField)
}
```

## Debugging with Shadow State

When debugging unexpected behavior:

1. **Check `/api/shadow/{plugin}`** to see current state
2. **Look at `inputs.current`** - are the expected inputs present?
3. **Check `sensorReadings.lastUpdate`** (energy plugin) - is it recent or zero time?
4. **Compare `inputs.current` vs `inputs.atLastAction`** - what changed?
5. **Check `lastComputations` timestamps** - are calculations running?

### Common Issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| `inputs.current` is empty | `updateShadowInputs()` not called | Add call at start of handlers |
| `lastUpdate` is zero time | Sensor updates not tracked | Add individual sensor update methods |
| Outputs correct but inputs wrong | Handler updates outputs but not inputs | Add `m.updateShadowInputs()` call |

## Adding Shadow State to New Plugins

1. **Create tracker type** in `internal/shadowstate/tracker.go`:
   ```go
   type MyPluginTracker struct {
       mu    sync.RWMutex
       state *MyPluginShadowState
   }
   ```

2. **Define state structure** in `internal/shadowstate/types.go`:
   ```go
   type MyPluginShadowState struct {
       Plugin   string           `json:"plugin"`
       Inputs   MyPluginInputs   `json:"inputs"`
       Outputs  MyPluginOutputs  `json:"outputs"`
       Metadata ShadowMetadata   `json:"metadata"`
   }
   ```

3. **Add tracker to manager**:
   ```go
   type Manager struct {
       // ...
       shadowTracker *shadowstate.MyPluginTracker
   }
   ```

4. **Implement `updateShadowInputs()`** method

5. **Call `updateShadowInputs()`** at the start of every handler

6. **Register with API** in `internal/api/server.go`

## Related Documentation

- [ARCHITECTURE.md](../architecture/ARCHITECTURE.md) - Overall architecture
- [VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md) - System diagrams
- [CONCURRENCY_LESSONS.md](./CONCURRENCY_LESSONS.md) - Thread safety patterns
