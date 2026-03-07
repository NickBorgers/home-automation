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

Shadow state uses Go generics to eliminate per-plugin boilerplate. Every plugin's state is a `ShadowState[I, O]` parameterized by input and output types:

```go
// Generic shadow state container
type ShadowState[I ShadowInputs, O any] struct {
    Plugin   string        `json:"plugin"`
    Inputs   I             `json:"inputs"`
    Outputs  O             `json:"outputs"`
    Metadata StateMetadata `json:"metadata"`
}

// Action-heavy plugins (lighting, music, security, etc.) use ActionInputs
type ActionInputs struct {
    Current      map[string]interface{} `json:"current"`      // Current input values
    AtLastAction map[string]interface{} `json:"atLastAction"` // Inputs when last action taken
}

// Read-heavy plugins (energy, dayphase, statetracking, etc.) use ReadOnlyInputs
type ReadOnlyInputs struct {
    Current map[string]interface{} `json:"current"`
}

type StateMetadata struct {
    LastUpdated time.Time `json:"lastUpdated"`
    PluginName  string    `json:"pluginName"`
}
```

Plugin state types are defined as type aliases:

```go
type LightingShadowState = ShadowState[ActionInputs, LightingOutputs]   // action-heavy
type EnergyShadowState   = ShadowState[ReadOnlyInputs, EnergyOutputs]   // read-heavy
```

## Implementation Requirements

### CRITICAL: Track Raw Inputs

**Every plugin handler MUST update shadow state inputs when receiving data from Home Assistant or state changes.**

This is the most common bug: plugins update outputs but forget to track the raw inputs that triggered the computation.

### Pattern 1: SubscriptionHelper (Recommended)

Most plugins (13 of 18) use `SubscriptionHelper`, which **automatically captures shadow inputs before each handler runs**. This is the preferred pattern because it's impossible to forget.

```go
// SubscriptionHelper captures inputs AUTOMATICALLY before calling your handler.
// No manual updateShadowInputs() call needed.
func (m *Manager) setupSubscriptions() {
    m.subHelper.SubscribeToState("dayPhase", func(key string, oldValue, newValue interface{}) {
        // Shadow inputs already captured at this point
        // Just process the change and update outputs
        m.processChange(newValue)
        m.shadowTracker.UpdateSomeOutput(result)
    })

    m.subHelper.SubscribeToEntity("sensor.something", func(entityID string, oldState, newState *ha.State) {
        // Shadow inputs already captured
        m.processSensor(newState)
    })

    // Capture initial state at startup
    m.subHelper.CaptureInitialInputs()
}
```

### Pattern 2: Manual updateShadowInputs() (For Periodic/Time-Based Plugins)

Plugins that use periodic timers instead of subscriptions (e.g., `dayphase`, `sleephygiene`) must manually capture inputs since SubscriptionHelper only wraps subscription callbacks.

```go
// Manual pattern: call updateShadowInputs() at start of every handler
func (m *Manager) handlePeriodicUpdate() {
    // 1. FIRST: Update shadow state inputs
    m.updateShadowInputs()

    // 2. Then: Process and compute outputs
    computed := m.computeSomething()

    // 3. Update state variables
    m.stateManager.SetString("someOutput", computed)

    // 4. Update shadow state outputs
    m.shadowTracker.UpdateSomeOutput(computed)
}

// updateShadowInputs captures current input values
func (m *Manager) updateShadowInputs() {
    inputs := make(map[string]interface{})
    if val, err := m.stateManager.GetBool("someInput"); err == nil {
        inputs["someInput"] = val
    }
    m.shadowTracker.UpdateCurrentInputs(inputs)
}
```

### Anti-Pattern (BUG)

```go
// WRONG: Only updates outputs, forgets inputs (and doesn't use SubscriptionHelper)
func (m *Manager) handleSomeChange(entityID string, oldState, newState *ha.State) {
    if newState == nil {
        return
    }

    // BUG: No SubscriptionHelper and no manual updateShadowInputs()!

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
| Lighting | `LightingTracker` | Room states, active scenes, bridge monitor (bridgeStale, consecutiveFailures, recentFailures) |
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
tracker.SetBridgeMonitor(&shadowstate.LightingBridgeMonitor{...})

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
| `inputs.current` is empty | Inputs not captured before handler | Use `SubscriptionHelper` (auto-captures) or add manual `updateShadowInputs()` call |
| `lastUpdate` is zero time | Sensor updates not tracked | Add individual sensor update methods |
| Outputs correct but inputs wrong | Handler updates outputs but not inputs | Ensure subscriptions go through `SubscriptionHelper`, or add manual input capture |

## Adding Shadow State to New Plugins

1. **Define outputs struct** in `internal/shadowstate/types.go`:
   ```go
   type MyPluginOutputs struct {
       SomeField string    `json:"someField"`
       LastCalc  time.Time `json:"lastCalc,omitempty"`
   }
   ```

2. **Add type alias** in `internal/shadowstate/types.go`:
   ```go
   // Action-heavy plugin (has AtLastAction snapshot):
   type MyPluginShadowState = ShadowState[ActionInputs, MyPluginOutputs]

   // OR read-heavy plugin (current inputs only):
   type MyPluginShadowState = ShadowState[ReadOnlyInputs, MyPluginOutputs]
   ```

3. **Create tracker** in `internal/shadowstate/tracker.go` by embedding the generic base:
   ```go
   // Action-heavy plugin:
   type MyPluginTracker struct {
       ActionTracker[MyPluginOutputs]
   }
   func NewMyPluginTracker() *MyPluginTracker {
       return &MyPluginTracker{
           ActionTracker: NewActionTracker(newActionShadowState("myplugin", MyPluginOutputs{})),
       }
   }

   // OR read-heavy plugin:
   type MyPluginTracker struct {
       ReadOnlyTracker[MyPluginOutputs]
   }
   func NewMyPluginTracker() *MyPluginTracker {
       return &MyPluginTracker{
           ReadOnlyTracker: NewReadOnlyTracker(newReadOnlyShadowState("myplugin", MyPluginOutputs{})),
       }
   }
   ```
   The generic base provides `UpdateCurrentInputs()`, `SnapshotInputsForAction()` (action trackers only), and thread-safe locking. You only need to add plugin-specific output methods.

4. **Add tracker and SubscriptionHelper to manager**:
   ```go
   type Manager struct {
       // ...
       shadowTracker *shadowstate.MyPluginTracker
       subHelper     *shadowstate.SubscriptionHelper
   }
   ```

5. **Use SubscriptionHelper for subscriptions** (recommended):
   ```go
   m.subHelper = shadowstate.NewSubscriptionHelper(
       haClient, stateManager, registry, m.shadowTracker, "myplugin", logger,
   )
   // Subscribe via subHelper — inputs are captured automatically
   m.subHelper.SubscribeToState("someKey", m.handleSomeChange)
   m.subHelper.SubscribeToEntity("sensor.x", m.handleEntity)
   m.subHelper.CaptureInitialInputs()
   ```
   Only use manual `updateShadowInputs()` if the plugin has periodic/timer-based logic that doesn't go through subscriptions.

6. **Register with API** in `internal/api/server.go`

## Related Documentation

- [ARCHITECTURE.md](../architecture/ARCHITECTURE.md) - Overall architecture
- [VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md) - System diagrams
- [CONCURRENCY_LESSONS.md](./CONCURRENCY_LESSONS.md) - Thread safety patterns
