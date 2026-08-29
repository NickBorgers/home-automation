# Plugin System Architecture

This document describes the plugin architecture for the Golang home automation system, including plugin interfaces, lifecycle management, and the override mechanism for private plugins.

## Table of Contents

1. [Overview](#overview)
2. [Plugin Architecture](#plugin-architecture)
3. [Core Plugin Interfaces](#core-plugin-interfaces)
4. [Plugin Lifecycle](#plugin-lifecycle)
5. [Existing Plugins](#existing-plugins)
6. [Creating New Plugins](#creating-new-plugins)
7. [Private Plugin Override System](#private-plugin-override-system)
8. [Testing Plugins](#testing-plugins)
9. [Best Practices](#best-practices)

---

## Overview

The home automation system uses a **plugin-based monolith** architecture where each automation domain is implemented as a separate plugin. Plugins are compiled into a single binary but maintain clear separation of concerns through well-defined interfaces.

### Key Principles

1. **Loose Coupling** - Plugins interact through the State Manager and HA Client, not directly with each other
2. **State-Driven** - Plugins subscribe to state changes and react accordingly
3. **Idempotent Operations** - Plugins can be reset without side effects
4. **Shadow State Tracking** - Plugins record their decision-making for observability
5. **Read-Only Mode Support** - All plugins must respect the READ_ONLY flag

### Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                    Main Application (cmd/main.go)                │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐   │
│  │   HA Client  │  │State Manager │  │  Shadow State Tracker│   │
│  │  (internal/  │  │  (internal/  │  │    (internal/        │   │
│  │   ha/)       │  │   state/)    │  │    shadowstate/)     │   │
│  └──────┬───────┘  └──────┬───────┘  └──────────┬───────────┘   │
│         │                 │                      │               │
│         │                 │                      │               │
│  ┌──────▼─────────────────▼──────────────────────▼───────────┐  │
│  │                    Plugin Layer                            │  │
│  │                                                            │  │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐  │  │
│  │  │  Energy  │ │ Lighting │ │  Music   │ │ State        │  │  │
│  │  │  Plugin  │ │  Plugin  │ │  Plugin  │ │ Tracking     │  │  │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────────┘  │  │
│  │                                                            │  │
│  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐  │  │
│  │  │ Security │ │   TV     │ │  Sleep   │ │ Load         │  │  │
│  │  │  Plugin  │ │  Plugin  │ │ Hygiene  │ │ Shedding     │  │  │
│  │  └──────────┘ └──────────┘ └──────────┘ └──────────────┘  │  │
│  │                                                            │  │
│  │  ┌──────────┐ ┌──────────────────────────────────────┐    │  │
│  │  │ Day Phase│ │     Reset Coordinator                │    │  │
│  │  │  Plugin  │ │     (orchestrates plugin resets)     │    │  │
│  │  └──────────┘ └──────────────────────────────────────┘    │  │
│  └────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Plugin Architecture

### Directory Structure

Each plugin lives in its own package under `internal/plugins/`:

```
homeautomation-go/
├── internal/
│   └── plugins/
│       ├── energy/              # Energy State plugin
│       │   ├── manager.go       # Main plugin implementation
│       │   ├── config.go        # Configuration loading
│       │   └── manager_test.go  # Unit tests
│       ├── lighting/            # Lighting Control plugin
│       │   ├── manager.go
│       │   ├── config.go
│       │   ├── config_test.go
│       │   └── manager_test.go
│       ├── music/               # Music Management plugin
│       │   ├── manager.go
│       │   ├── config.go
│       │   └── manager_test.go
│       ├── security/            # Security plugin
│       │   ├── manager.go
│       │   └── manager_test.go
│       ├── tv/                  # TV Monitoring plugin
│       │   ├── manager.go
│       │   └── manager_test.go
│       ├── sleephygiene/        # Sleep Hygiene plugin
│       │   ├── manager.go
│       │   └── manager_test.go
│       ├── loadshedding/        # Load Shedding plugin
│       │   ├── manager.go
│       │   └── manager_test.go
│       ├── statetracking/       # State Tracking plugin
│       │   ├── manager.go
│       │   └── manager_test.go
│       ├── dayphase/            # Day Phase plugin
│       │   ├── manager.go
│       │   └── manager_test.go
│       └── reset/               # Reset Coordinator
│           ├── coordinator.go
│           └── coordinator_test.go
```

### Plugin Dependencies

Plugins receive their dependencies through constructor injection:

```go
type Manager struct {
    haClient      ha.HAClient        // For calling Home Assistant services
    stateManager  *state.Manager     // For reading/writing state variables
    config        *Config            // Plugin-specific configuration (optional)
    logger        *zap.Logger        // Structured logging
    readOnly      bool               // Whether to skip HA writes

    // Subscription tracking for cleanup
    haSubscriptions    []ha.Subscription
    stateSubscriptions []state.Subscription

    // Shadow state tracking (optional)
    shadowTracker *shadowstate.Tracker
}
```

---

## Core Plugin Interfaces

### Plugin Interface

While there is no explicit `Plugin` interface defined, all plugins follow this implicit contract:

```go
// Implicit Plugin interface
type Plugin interface {
    // Start begins the plugin's operation
    // - Sets up subscriptions to state changes
    // - Starts any background goroutines
    // - Returns error if initialization fails
    Start() error

    // Stop gracefully shuts down the plugin
    // - Unsubscribes from all state changes
    // - Stops any background goroutines
    // - Releases resources
    Stop()
}
```

### Resettable Interface

Plugins that support the system-wide reset mechanism implement the `Resettable` interface:

```go
// Defined in internal/plugins/reset/coordinator.go
type Resettable interface {
    // Reset re-evaluates all conditions and recalculates state
    // - Clears any rate limiters or timers
    // - Re-applies current state conditions
    // - Returns error if reset fails
    Reset() error
}
```

### ShadowStateProvider Interface

Plugins that track their decision-making for observability implement shadow state:

```go
// Implicit ShadowStateProvider interface
type ShadowStateProvider interface {
    // GetShadowState returns the current shadow state for the plugin
    GetShadowState() shadowstate.PluginShadowState
}
```

---

## Plugin Lifecycle

### Startup Sequence

Plugins are started in a specific order in `cmd/main.go`:

```
1. State Tracking Plugin (first - computes derived states used by others)
2. Day Phase Plugin (provides dayPhase and sunevent state)
3. Energy Plugin (provides energy level states)
4. Music Plugin
5. Lighting Plugin
6. Security Plugin
7. Sleep Hygiene Plugin
8. Load Shedding Plugin
9. TV Plugin
10. Reset Coordinator (last - needs all plugins registered)
```

**Important:** The State Tracking plugin must start first because other plugins depend on computed states like `isAnyoneHome` and `isAnyoneAsleep`.

### Startup Example

```go
// In cmd/main.go
func main() {
    // ... initialization ...

    // Start plugins in order
    stateTrackingManager := statetracking.NewManager(client, stateManager, logger, readOnly)
    if err := stateTrackingManager.Start(); err != nil {
        logger.Fatal("Failed to start State Tracking Manager", zap.Error(err))
    }
    defer stateTrackingManager.Stop()

    // Register shadow state provider
    shadowTracker.RegisterPluginProvider("statetracking", func() shadowstate.PluginShadowState {
        return stateTrackingManager.GetShadowState()
    })

    // ... more plugins ...
}
```

### Shutdown Sequence

Plugins are stopped in reverse order using Go's `defer` mechanism:

```go
// Deferred calls execute in LIFO order
defer resetCoordinator.Stop()      // Stops last (registered last)
defer tvManager.Stop()
defer loadSheddingManager.Stop()
defer sleepHygieneManager.Stop()
defer securityManager.Stop()
defer lightingManager.Stop()
defer musicManager.Stop()
defer energyManager.Stop()
defer dayPhaseManager.Stop()
defer stateTrackingManager.Stop()  // Stops first (registered first)
```

---

## Existing Plugins

### State Tracking Plugin (`statetracking`)

**Purpose:** Computes derived state variables from individual presence and sleep states.

**State Variables Managed:**
- `isAnyOwnerHome` - Computed from `isNickHome || isCarolineHome`
- `isAnyoneHome` - Computed from presence states
- `isAnyoneAsleep` - Computed from `isMasterAsleep || isGuestAsleep`
- `isEveryoneAsleep` - Computed from sleep states
- `isAnyoneHomeAndAwake` - Computed from `isAnyoneHome && !isAnyoneAsleep`

**Events Subscribed:**
- `isNickHome`, `isCarolineHome`, `isAssistantHere`
- `isMasterAsleep`, `isGuestAsleep`
- `isHaveGuests`

### Day Phase Plugin (`dayphase`)

**Purpose:** Calculates and updates the current day phase based on time and sun events.

**State Variables Managed:**
- `dayPhase` - Current phase: morning, day, sunset, dusk, winddown, night
- `sunevent` - Current sun event: sunrise, sunset, dusk, night

**Configuration:** Uses `schedule_config.yaml` for day phase transition times.

### Energy Plugin (`energy`)

**Purpose:** Tracks energy availability from battery and solar sources.

**State Variables Managed:**
- `batteryEnergyLevel` - Battery charge level category
- `solarProductionEnergyLevel` - Solar production category
- `currentEnergyLevel` - Combined energy availability
- `isFreeEnergyAvailable` - Legacy metered-grid flag, retired (always false)

**HA Entities Subscribed:**
- `sensor.span_panel_span_storage_battery_percentage_2`
- `sensor.energy_next_hour`
- `sensor.energy_production_today_remaining`

**Configuration:** Uses `energy_config.yaml` for battery, solar, and indicator-light thresholds.

### Music Plugin (`music`)

**Purpose:** Manages Sonos music playback based on presence, sleep, and time of day.

**State Variables Managed:**
- `musicPlaybackType` - Current music mode (morning, day, evening, winddown, sleep, etc.)
- `currentlyPlayingMusicUri` - Currently playing track/playlist

**Events Subscribed:**
- `dayPhase`, `isAnyoneHome`, `isAnyoneAsleep`
- `isMasterAsleep`, `isGuestAsleep`, `isAssistantHere`

**Configuration:** Uses `music_config.yaml` for playlists and speaker groups.

### Lighting Plugin (`lighting`)

**Purpose:** Activates lighting scenes based on day phase and occupancy.

**State Variables Subscribed:**
- `dayPhase`, `sunevent`
- `isAnyoneHome`, `isAnyoneAsleep`, `isAnyoneHomeAndAwake`
- `isTVPlaying`

**Configuration:** Uses `hue_config.yaml` for room-to-scene mappings and conditional logic.

### Security Plugin (`security`)

**Purpose:** Handles lockdown, doorbell notifications, and garage automation.

**Features:**
- Auto-lockdown when everyone asleep or no one home
- Doorbell TTS notifications with rate limiting
- Garage door auto-open on owner return
- Vehicle arrival notifications

**State Variables Subscribed:**
- `isEveryoneAsleep`, `isAnyoneHome`
- `isExpectingSomeone`, `didOwnerJustReturnHome`

### TV Plugin (`tv`)

**Purpose:** Monitors TV and Apple TV state to update state variables.

**State Variables Managed:**
- `isAppleTVPlaying` - Whether Apple TV is actively playing
- `isTVon` - Whether TV/sync box is powered on
- `isTVPlaying` - Whether content is actively playing

**HA Entities Subscribed:**
- `media_player.sony_xr_65a80k`
- `switch.sync_box_power`
- `select.sync_box_hdmi_input`

### Sleep Hygiene Plugin (`sleephygiene`)

**Purpose:** Manages wake-up sequences and sleep-related automations.

**Features:**
- Time-based wake-up triggers from `alarmTime`
- Fade-out sleep music sequence
- Wake-up light activation
- Stop screens reminder

**State Variables Subscribed:**
- `isMasterAsleep`, `alarmTime`
- `musicPlaybackType`

### Load Shedding Plugin (`loadshedding`)

**Purpose:** Adjusts thermostat settings based on energy availability.

**State Variables Subscribed:**
- `currentEnergyLevel`

**Actions:**
- Sets thermostat to hold mode during low energy
- Widens temperature range to reduce consumption
- Restores normal settings when energy is abundant

### Reset Coordinator (`reset`)

**Purpose:** Orchestrates system-wide resets when the `reset` state variable is triggered.

**Features:**
- Watches `reset` boolean state variable
- Calls `Reset()` on all registered plugins
- Auto-clears the reset flag after execution

### Tesla Fleet API Plugin (`tesla`)

**Purpose:** Polls the Tesla Fleet API for vehicle and charge state, holds OAuth tokens, signs vehicle commands on request, and sets the backup reserve on the Powerwall systems. The plugin never issues commands autonomously.

Setting the backup reserve is the one Powerwall operation with no local path: the gateway API on the house LAN only reads, so the command has to go through Tesla's cloud. Powerwall telemetry is the opposite and is deliberately absent here — the gateways are free and higher resolution, so nothing in this plugin polls Tesla for energy data.

**State Variables Managed:** None. Reads from and writes to Tesla directly; does not read or write Home Assistant state.

**HA Entities Subscribed:** None.

**HTTP Endpoints:**
- `GET /api/tesla/login` — starts OAuth authorization flow
- `GET /api/tesla/callback` — completes authorization, stores tokens
- `GET /api/shadow/tesla` — returns shadow state (vehicle state, charge state, Fleet API usage)
- `GET /api/tesla/energy/sites` — lists the Powerwall systems and their site ids

No endpoint changes home state. Setting a backup reserve is a capability on the Manager, next to the vehicle Commander, and is not reachable over HTTP. `READ_ONLY=true` refuses the change and logs what it would have sent.

**Cost note:** Tesla bills per request against a monthly credit. The poll interval (`TESLA_POLL_MINUTES`, default 5) is a recurring charge. `/api/shadow/tesla` exposes `requestCount` and `commandCount` to track spend. Listing sites and changing a reserve each cost a request too, so they are meant for occasional use rather than polling.

**Configuration:** Environment only — see `.env.example` for `TESLA_CLIENT_ID`, `TESLA_CLIENT_SECRET`, `TESLA_DOMAIN`, `TESLA_REDIRECT_URI`, `TESLA_PRIVATE_KEY`, `TESLA_TOKEN_STORE`, `TESLA_VIN`. Powerwall site ids are not configured; the caller passes one per command.

---

## Creating New Plugins

### Constructor Standard (2026+)

**All new plugins should use `StandardDeps` and `OptionalDeps`** from `homeautomation/internal/plugin` for consistent constructor signatures. This eliminates the inconsistent parameter ordering across existing plugins.

> **Note:** Existing plugins (18 total) will NOT be migrated — this standard applies to new plugins only.

```go
import intplugin "homeautomation/internal/plugin"

func NewManager(deps intplugin.StandardDeps, opts intplugin.OptionalDeps, config *MyConfig) *Manager {
    return &Manager{
        ctx:          deps.Ctx,
        haClient:     deps.HAClient,
        stateManager: deps.StateManager,
        logger:       deps.Logger.Named("myplugin"),
        readOnly:     deps.ReadOnly,
        registry:     deps.Registry,
        timeProvider: opts.TimeProviderOrDefault(),
        timezone:     opts.TimezoneOrDefault(),
        ntfyClient:   opts.NtfyClient,
        config:       config,
    }
}
```

**`StandardDeps`** contains dependencies every plugin needs:
- `Ctx` — shutdown context for graceful cancellation
- `HAClient` — Home Assistant client (`ha.HAClient`)
- `StateManager` — state variable system (`*state.Manager`)
- `Logger` — structured logger (`*zap.Logger`)
- `ReadOnly` — read-only mode flag
- `Registry` — shadow state subscription registry

**`OptionalDeps`** contains dependencies not all plugins need:
- `TimeProvider` — testable time (use `opts.TimeProviderOrDefault()` for safe nil handling)
- `Timezone` — timezone for schedule calculations (use `opts.TimezoneOrDefault()`)
- `NtfyClient` — push notifications (may be nil)
- `Latitude`, `Longitude` — geographic coordinates for sun calculations

**Benefits:**
- Consistent parameter order across all new plugins
- Adding a new standard dependency requires updating one struct, not every constructor
- Self-documenting which dependencies are required vs optional
- Helper methods (`TimeProviderOrDefault()`, `TimezoneOrDefault()`) handle nil safely

**In `register.go`**, construct the deps from the plugin context:

```go
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
    haClient := pkgha.UnwrapClient(ctx.HAClient)
    stateManager := pkgstate.UnwrapManager(ctx.StateManager)

    deps := intplugin.StandardDeps{
        Ctx:          ctx.ShutdownCtx,
        HAClient:     haClient,
        StateManager: stateManager,
        Logger:       ctx.Logger,
        ReadOnly:     ctx.ReadOnly,
        Registry:     ctx.Registry,
    }
    opts := intplugin.OptionalDeps{
        TimeProvider: ctx.TimeProvider,
        Timezone:     ctx.Timezone,
        NtfyClient:   ctx.NtfyClient,
    }

    manager := NewManager(deps, opts, config)
    return &pluginAdapter{manager: manager}, nil
}
```

### Legacy Pattern (Pre-2026)

The following pattern is used by existing plugins and is documented here for reference. **New plugins should use the [Constructor Standard](#constructor-standard-2026) above.**

### Step 1: Create Package Structure

```bash
mkdir -p internal/plugins/myplugin
touch internal/plugins/myplugin/manager.go
touch internal/plugins/myplugin/manager_test.go
```

### Step 2: Implement Manager

```go
package myplugin

import (
    "fmt"

    "homeautomation/internal/ha"
    "homeautomation/internal/shadowstate"
    "homeautomation/internal/state"

    "go.uber.org/zap"
)

type Manager struct {
    haClient      ha.HAClient
    stateManager  *state.Manager
    logger        *zap.Logger
    readOnly      bool

    // Subscriptions for cleanup
    haSubscriptions    []ha.Subscription
    stateSubscriptions []state.Subscription

    // Shadow state tracking
    shadowTracker *shadowstate.MyPluginTracker
}

func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool) *Manager {
    return &Manager{
        haClient:           haClient,
        stateManager:       stateManager,
        logger:             logger.Named("myplugin"),
        readOnly:           readOnly,
        haSubscriptions:    make([]ha.Subscription, 0),
        stateSubscriptions: make([]state.Subscription, 0),
        shadowTracker:      shadowstate.NewMyPluginTracker(),
    }
}

func (m *Manager) Start() error {
    m.logger.Info("Starting MyPlugin Manager")

    // Subscribe to relevant state changes
    sub, err := m.stateManager.Subscribe("someStateVar", m.handleStateChange)
    if err != nil {
        return fmt.Errorf("failed to subscribe to someStateVar: %w", err)
    }
    m.stateSubscriptions = append(m.stateSubscriptions, sub)

    // For cross-plugin correlation tracking, use SubscribeWithContext instead.
    // This provides a correlation ID that can be logged for tracing event chains.
    ctxSub, err := m.stateManager.SubscribeWithContext("anotherVar", m.handleStateChangeWithContext)
    if err != nil {
        return fmt.Errorf("failed to subscribe with context: %w", err)
    }
    m.stateSubscriptions = append(m.stateSubscriptions, ctxSub)

    // Subscribe to HA entity changes
    haSub, err := m.haClient.SubscribeStateChanges("sensor.my_sensor", m.handleSensorChange)
    if err != nil {
        return fmt.Errorf("failed to subscribe to sensor: %w", err)
    }
    m.haSubscriptions = append(m.haSubscriptions, haSub)

    m.logger.Info("MyPlugin Manager started successfully")
    return nil
}

func (m *Manager) Stop() {
    m.logger.Info("Stopping MyPlugin Manager")

    // Unsubscribe from HA entities
    for _, sub := range m.haSubscriptions {
        sub.Unsubscribe()
    }
    m.haSubscriptions = nil

    // Unsubscribe from state changes
    for _, sub := range m.stateSubscriptions {
        sub.Unsubscribe()
    }
    m.stateSubscriptions = nil

    m.logger.Info("MyPlugin Manager stopped")
}

func (m *Manager) Reset() error {
    m.logger.Info("Resetting MyPlugin - re-evaluating state")

    // Re-evaluate conditions and reapply state
    // Clear any rate limiters or cached values

    m.logger.Info("Successfully reset MyPlugin")
    return nil
}

func (m *Manager) GetShadowState() *shadowstate.MyPluginShadowState {
    return m.shadowTracker.GetState()
}

// State change handlers
func (m *Manager) handleStateChange(key string, oldValue, newValue interface{}) {
    // Update shadow state with current inputs
    m.shadowTracker.UpdateCurrentInputs(...)

    // Process the state change
    value, ok := newValue.(bool)
    if !ok {
        m.logger.Error("Invalid type for state", zap.Any("value", newValue))
        return
    }

    // Perform action
    m.performAction(value)
}

// Context-aware handler for cross-plugin correlation tracking (added in PR #572).
// The EventContext contains a CorrelationID for tracing event chains across plugins.
func (m *Manager) handleStateChangeWithContext(ctx *state.EventContext, key string, oldValue, newValue interface{}) {
    // Log with correlation ID for cross-plugin tracing in Gravwell
    m.logger.Debug("Handling state change with context",
        zap.String("correlation_id", ctx.CorrelationID),
        zap.String("key", key),
        zap.Any("old", oldValue),
        zap.Any("new", newValue))

    // The correlation ID can be passed to downstream operations for full event tracing
    m.processWithCorrelation(ctx.CorrelationID, newValue)
}

func (m *Manager) handleSensorChange(entity string, oldState, newState *ha.State) {
    // Process HA entity state change
    m.logger.Info("Sensor changed",
        zap.String("entity", entity),
        zap.String("old", oldState.State),
        zap.String("new", newState.State))
}

func (m *Manager) performAction(value bool) {
    // Record action in shadow state
    m.shadowTracker.RecordAction(...)

    if m.readOnly {
        m.logger.Info("READ-ONLY: Would perform action", zap.Bool("value", value))
        return
    }

    if err := m.haClient.CallService("domain", "service", map[string]interface{}{
        "entity_id": "my.entity",
    }); err != nil {
        m.logger.Error("Failed to call service", zap.Error(err))
    }
}
```

### Step 3: Register in main.go

```go
// In cmd/main.go

import "homeautomation/internal/plugins/myplugin"

func main() {
    // ... existing initialization ...

    // Start MyPlugin Manager
    myPluginManager := myplugin.NewManager(client, stateManager, logger, readOnly)
    if err := myPluginManager.Start(); err != nil {
        logger.Fatal("Failed to start MyPlugin Manager", zap.Error(err))
    }
    defer myPluginManager.Stop()
    logger.Info("MyPlugin Manager started successfully")

    // Register shadow state provider
    shadowTracker.RegisterPluginProvider("myplugin", func() shadowstate.PluginShadowState {
        return myPluginManager.GetShadowState()
    })

    // Add to Reset Coordinator
    resetCoordinator := reset.NewCoordinator(stateManager, logger, readOnly, []reset.PluginWithName{
        // ... existing plugins ...
        {Name: "MyPlugin", Plugin: myPluginManager},
    })
}
```

---

## Private Plugin Override System

For security-sensitive plugins that should not be open-source, the system supports a compile-time override mechanism.

### Overview

- **Public repo** contains a reference/sample implementation
- **Private repo** contains actual security logic
- Private plugin **overrides** the public one when imported

### Implementation Steps

#### Step 1: Create Public Plugin API (`pkg/plugin/`)

Create a `pkg/` directory (not `internal/`) for external package imports:

```go
// pkg/plugin/interfaces.go
package plugin

type Plugin interface {
    Name() string
    Start() error
    Stop()
}

type Resettable interface {
    Reset() error
}

type ShadowStateProvider interface {
    GetShadowState() ShadowState
}

type Factory func(ctx *Context) (Plugin, error)
```

```go
// pkg/plugin/context.go
package plugin

type Context struct {
    HAClient     HAClient      // Interface wrapping ha.HAClient
    StateManager StateManager  // Interface wrapping state.Manager
    Logger       *zap.Logger
    ReadOnly     bool
    ConfigDir    string
    SoCoCliURL   string        // SoCo-CLI HTTP API base URL (optional, for direct speaker control and Tidal playback)
}
```

#### Step 2: Create Plugin Registry

```go
// pkg/plugin/registry.go
package plugin

const (
    PriorityDefault  = 0
    PriorityOverride = 100
)

type PluginInfo struct {
    Name        string
    Description string
    Priority    int
    Factory     Factory
}

type Registry struct {
    mu      sync.RWMutex
    plugins map[string]PluginInfo
    order   []string
}

func (r *Registry) Register(info PluginInfo) error {
    r.mu.Lock()
    defer r.mu.Unlock()

    if existing, exists := r.plugins[info.Name]; exists {
        log.Printf("Plugin %s being overridden (priority %d -> %d)",
            info.Name, existing.Priority, info.Priority)
    }

    // Higher priority wins
    if existing, exists := r.plugins[info.Name]; !exists || info.Priority >= existing.Priority {
        r.plugins[info.Name] = info
        if !exists {
            r.order = append(r.order, info.Name)
        }
    }
    return nil
}
```

#### Step 3: Public Plugin Registration

```go
// internal/plugins/security/register.go
package security

import "github.com/NickBorgers/node-red/homeautomation-go/pkg/plugin"

func init() {
    plugin.Register(plugin.PluginInfo{
        Name:        "security",
        Description: "Reference security plugin",
        Priority:    plugin.PriorityDefault,  // Can be overridden
        Factory:     createPlugin,
    })
}
```

#### Step 4: Private Override

Create a private repository (e.g., `github.com/NickBorgers/homeautomation-security`):

```go
// In private repo: register.go
package security

import "github.com/NickBorgers/node-red/homeautomation-go/pkg/plugin"

func init() {
    plugin.Register(plugin.PluginInfo{
        Name:        "security",
        Description: "Private security plugin",
        Priority:    plugin.PriorityOverride,  // Overrides public
        Factory:     New,
    })
}
```

#### Step 5: Usage in main.go

```go
import (
    // Public plugin (reference implementation)
    _ "github.com/NickBorgers/node-red/homeautomation-go/internal/plugins/security"

    // Private override (uncomment for private builds)
    // _ "github.com/NickBorgers/homeautomation-security"
)
```

### How Override Works

**Public build:**
```
Plugin "security" registered (priority 0)
Starting plugin: security (Reference security plugin)
```

**Private build:**
```
Plugin "security" registered (priority 0)
Plugin "security" being overridden (priority 0 -> 100)
Starting plugin: security (Private security plugin)
```

---

## Testing Plugins

### Unit Test Structure

```go
// manager_test.go
package myplugin

import (
    "testing"

    "homeautomation/internal/ha"
    "homeautomation/internal/state"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "go.uber.org/zap/zaptest"
)

func TestManager_Start(t *testing.T) {
    logger := zaptest.NewLogger(t)
    mockClient := ha.NewMockClient()
    stateManager := state.NewManager(mockClient, logger, true)

    manager := NewManager(mockClient, stateManager, logger, true)

    err := manager.Start()
    require.NoError(t, err)
    defer manager.Stop()

    // Verify subscriptions were created
    assert.Greater(t, len(manager.stateSubscriptions), 0)
}

func TestManager_HandleStateChange(t *testing.T) {
    tests := []struct {
        name     string
        input    interface{}
        expected string
    }{
        {"when true", true, "action_performed"},
        {"when false", false, "no_action"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Setup
            logger := zaptest.NewLogger(t)
            mockClient := ha.NewMockClient()
            stateManager := state.NewManager(mockClient, logger, true)
            manager := NewManager(mockClient, stateManager, logger, true)

            // Execute
            manager.handleStateChange("key", nil, tt.input)

            // Assert
            shadowState := manager.GetShadowState()
            assert.Equal(t, tt.expected, shadowState.LastAction)
        })
    }
}

func TestManager_Reset(t *testing.T) {
    logger := zaptest.NewLogger(t)
    mockClient := ha.NewMockClient()
    stateManager := state.NewManager(mockClient, logger, true)

    manager := NewManager(mockClient, stateManager, logger, true)
    manager.Start()
    defer manager.Stop()

    err := manager.Reset()
    assert.NoError(t, err)
}
```

### Testing with Mock HA Client

The `ha.NewMockClient()` provides a mock implementation for testing:

```go
mockClient := ha.NewMockClient()

// Set expected states
mockClient.SetState("sensor.my_sensor", &ha.State{
    State: "42",
    Attributes: map[string]interface{}{
        "unit": "kW",
    },
})

// Verify service calls
assert.Eventually(t, func() bool {
    calls := mockClient.GetServiceCalls()
    return len(calls) > 0 && calls[0].Service == "expected_service"
}, time.Second, 10*time.Millisecond)
```

### Running Tests

```bash
# Run all tests with caching (preferred)
make unit-tests
make integration-tests

# Full validation (same as CI, no cache)
make pre-push
```

---

## Verbal Notifications

Plugins that need to make spoken announcements **must** use the shared `notify.Notifier`
instead of constructing HA service calls directly. The notifier:

1. **Synthesizes** the message to MP3 via the Kokoro TTS server (configured by `TTS_ENDPOINT`).
2. **Serves** the MP3 at a LAN HTTP URL reachable by Sonos speakers (`TTS_SERVE_URL`).
3. **Plays** the announcement via `media_player.play_media` with `announce: true` and
   `extra.volume = awake_volume_percent`. The Sonos integration snapshots the queue,
   current track, position, and volume, ducks to the configured level, plays the
   announcement, then restores everything — so music resumes seamlessly.

Deferable announcements are dropped while master is asleep; Urgent announcements always play.

### Accessing the notifier

The notifier is available on `plugin.Context` as `ctx.Notifier`:

```go
func createPlugin(ctx *plugin.Context) (plugin.Plugin, error) {
    notifier := ctx.Notifier
    if notifier == nil {
        // Fall back to a non-functional mock so the plugin can still
        // load when the notifier is not wired (tests, partial bootstraps).
        notifier = &notify.MockNotifier{}
    }
    manager := NewManager(..., notifier, ...)
    return &pluginAdapter{manager: manager}, nil
}
```

### Calling Announce

```go
import "homeautomation/pkg/notify"

// Deferable announcement (dropped while master is asleep; e.g. presence, vacuum errors)
m.notifier.Announce(m.ctx, "Casper needs attention: dustbin missing",
    notify.WithSpeakers([]string{"media_player.kitchen", "media_player.front_room"}),
)

// Urgent announcement (e.g. doorbell, intruder, EV-charger overheat)
m.notifier.Announce(m.ctx, "Person detected outside",
    notify.WithSpeakers(securitySpeakers),
    notify.WithUrgency(notify.UrgencyUrgent),
)
```

`WithSpeakers` is optional — if omitted, the notifier uses the `default_speakers` list from `configs/notification_config.yaml`.

### Urgency levels

- **`UrgencyDeferable`** (default) — dropped entirely while master is asleep; returns `ErrSuppressedAsleep`. Use for: vacuum errors, presence announcements, EV-charger battery levels.
- **`UrgencyUrgent`** — always uses `awake_volume_percent` regardless of sleep state. Use for: doorbell, vehicle arrival, intruder detection, EV-charger safety alerts, anything else that **must** be heard.

### Read-only mode

Plugins **should not** check `m.readOnly` before calling `Announce`. The notifier itself logs the intended announcement and skips the service call when the application is in read-only mode. This centralises read-only handling and avoids divergent per-plugin behavior.

### Testing

Use `notify.MockNotifier` to assert announcement behavior in unit tests without making any real service calls:

```go
mockNotifier := &notify.MockNotifier{}
manager := NewManager(..., mockNotifier, ...)
// trigger the code path that should announce
calls := mockNotifier.Calls()
require.Len(t, calls, 1)
assert.Equal(t, "Doorbell ringing", calls[0].Message)
assert.Equal(t, notify.UrgencyUrgent, calls[0].Urgency)
```

Configuration lives at `configs/notification_config.yaml` — see that file for the full set of tunables (default speakers, awake volume, restore delay, TTS engine).

---

## Best Practices

### 1. Always Respect Read-Only Mode

```go
func (m *Manager) performAction() {
    if m.readOnly {
        m.logger.Info("READ-ONLY: Would perform action")
        return
    }

    // Actual action
    m.haClient.CallService(...)
}
```

### 2. Clean Up Subscriptions

```go
func (m *Manager) Stop() {
    for _, sub := range m.haSubscriptions {
        sub.Unsubscribe()
    }
    m.haSubscriptions = nil

    for _, sub := range m.stateSubscriptions {
        sub.Unsubscribe()
    }
    m.stateSubscriptions = nil
}
```

### 3. Track Shadow State for Observability

```go
func (m *Manager) handleChange(...) {
    // 1. Update current inputs
    m.shadowTracker.UpdateCurrentInputs(inputs)

    // 2. Snapshot inputs before action
    m.shadowTracker.SnapshotInputsForAction()

    // 3. Record the action taken
    m.shadowTracker.RecordAction(action, reason)

    // 4. Perform actual action
    if !m.readOnly {
        m.performAction()
    }
}
```

### 4. Use Named Loggers

```go
func NewManager(...) *Manager {
    return &Manager{
        logger: logger.Named("myplugin"),  // Creates "myplugin" prefix
    }
}
```

### 5. Handle Errors Gracefully

```go
func (m *Manager) handleStateChange(key string, old, new interface{}) {
    value, ok := new.(bool)
    if !ok {
        m.logger.Error("Invalid type for state",
            zap.String("key", key),
            zap.Any("value", new))
        return  // Don't crash, just log and skip
    }

    // Continue processing...
}
```

### 6. Implement Reset Properly

```go
func (m *Manager) Reset() error {
    m.logger.Info("Resetting MyPlugin")

    // Clear rate limiters
    m.mu.Lock()
    m.lastNotification = time.Time{}
    m.mu.Unlock()

    // Re-evaluate current conditions
    currentState, err := m.stateManager.GetBool("relevantState")
    if err != nil {
        return fmt.Errorf("failed to get state: %w", err)
    }

    if currentState {
        m.performAction()
    }

    m.logger.Info("Successfully reset MyPlugin")
    return nil
}
```

### 7. Use Constants for Magic Values

```go
const (
    NotificationRateLimit = 20 * time.Second
    RetryDelay            = 5 * time.Second
    MaxRetries            = 3
)
```

### 8. Document State Dependencies

Include a comment block documenting which state variables the plugin reads and writes:

```go
// Manager handles energy state calculations.
//
// State Variables Read:
//   - isGridAvailable (bool)
//   - batteryEnergyLevel (string)
//   - solarProductionEnergyLevel (string)
//
// State Variables Written:
//   - currentEnergyLevel (string)
//   - isFreeEnergyAvailable (bool, legacy metered-grid flag, retired (always false))
//
// HA Entities Subscribed:
//   - sensor.span_panel_span_storage_battery_percentage_2
//   - sensor.energy_next_hour
//   - sensor.energy_production_today_remaining
type Manager struct {
    // ...
}
```

---

## Implementation Status

The plugin system has been fully implemented with the following components:

### Public Package (`pkg/`)

| File | Description |
|------|-------------|
| `pkg/plugin/interfaces.go` | Core Plugin, Resettable, ShadowStateProvider interfaces |
| `pkg/plugin/context.go` | Context struct for plugin initialization |
| `pkg/plugin/registry.go` | Global registry with priority-based override |
| `pkg/plugin/registry_test.go` | Comprehensive tests (98.4% coverage) |
| `pkg/ha/interfaces.go` | Public HA client interface |
| `pkg/ha/adapter.go` | Adapter wrapping internal ha.Client |
| `pkg/state/interfaces.go` | Public state manager interface |
| `pkg/state/adapter.go` | Adapter wrapping internal state.Manager |

### Reference Security Plugin

| File | Description |
|------|-------------|
| `internal/plugins/security/register.go` | Plugin registration with `init()` |
| `internal/plugins/security/manager.go` | Reference implementation |

### Private Security Plugin (Separate Repo)

Located at `../homeautomation-security/`:

| File | Description |
|------|-------------|
| `go.mod` | Module definition with replace directive |
| `security.go` | Private implementation with override priority |
| `README.md` | Usage and development guide |

**Private Features:**
- Garage auto-close after 5-minute timeout

### Usage Example

To use the private security plugin:

```go
// In go.mod, add:
require github.com/NickBorgersOnLowSecurityNode/homeautomation-security v0.0.0

// In main.go, import:
import (
    _ "homeautomation/internal/plugins/security"  // Reference (auto-registers)
    _ "github.com/NickBorgersOnLowSecurityNode/homeautomation-security"  // Override
)
```

---

## Related Documentation

- **[ARCHITECTURE.md](../architecture/ARCHITECTURE.md)** - Overall project architecture and status
- **[VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md)** - System diagrams and flow charts
- **[CONCURRENCY_LESSONS.md](./CONCURRENCY_LESSONS.md)** - Concurrency patterns
- **[../../homeautomation-go/README.md](../../homeautomation-go/README.md)** - User guide

---

**Last Updated:** 2026-02-12
**Go Version:** 1.24
