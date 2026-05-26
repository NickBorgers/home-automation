# Home Automation System Architecture

**Last Updated:** 2026-01-10
**Phase:** ✅ PRODUCTION
**Location:** `homeautomation-go/`

---

## Table of Contents

1. [Current Status](#current-status)
2. [Executive Summary](#executive-summary)
3. [System Architecture](#system-architecture)
4. [Core Components](#core-components)
5. [Automation Plugins](#automation-plugins)
6. [Data Flow](#data-flow)
7. [State Synchronization](#state-synchronization)
8. [Configuration Management](#configuration-management)
9. [Project Structure](#project-structure)
10. [Implementation Checklist](#implementation-checklist)
11. [Code Templates & Examples](#code-templates--examples)
12. [Production Roadmap](#production-roadmap)
13. [HomeKit Integration](#homekit-integration)
14. [Resources and References](#resources-and-references)

---

## Current Status

### What's Been Completed

✅ **Phase 1-5: MVP Implementation (COMPLETE)**
- ✅ Project setup with Go modules and dependencies
- ✅ Home Assistant WebSocket client implementation
- ✅ State Manager with 40 state variables
- ✅ Demo application with monitoring
- ✅ Comprehensive unit test suite
- ✅ Integration test suite with mock HA server
- ✅ Docker support with GHCR automation

✅ **Phase 6+: Plugin Implementation (COMPLETE)**
- ✅ State Tracking plugin (complete)
- ✅ Day Phase plugin (complete)
- ✅ Energy State plugin (complete)
- ✅ Lighting Control plugin (complete)
- ✅ Music Management plugin (complete)
- ✅ Sleep Hygiene plugin (complete)
- ✅ TV Monitoring plugin (complete)
- ✅ Environmental Monitoring plugin (complete)
- ✅ Security plugin (complete)
- ✅ Load Shedding plugin (complete)
- ✅ Sex Mode plugin (complete)
- ✅ Christmas plugin (complete)
- ✅ Infrastructure plugin (complete) - septic system monitoring, thermostat monitoring
- ✅ Water Flow plugin (complete) - pipe break detection
- ✅ Reset Coordinator (complete)
- ✅ Integration Watchdog plugin (complete) - periodic entity-state scanning and config-entry reload

### Critical Bug Fixes

✅ **Bug #1: Concurrent WebSocket Writes (FIXED)**
- Added `writeMu` mutex to protect WebSocket writes
- Location: `internal/ha/client.go`
- Severity: CRITICAL - Would cause panics in production

✅ **Bug #2: Subscription Memory Leak & Dispatch Races (FIXED)**
- Per-subscription IDs prevent collateral unsubscriptions
- Dispatch now snapshots handlers, runs synchronously, recovers from panics
- Locations: `internal/ha/client.go`, `internal/ha/mock.go`, `internal/state/manager.go`

### Test Coverage

**Unit Tests:** All passing ✅
- HA Client: >65% coverage
- State Manager: >65% coverage
- No race conditions detected

**Integration Tests:** 11/11 passing ✅
- 50 goroutines × 100 concurrent reads
- 20 goroutines × 50 concurrent writes
- High-frequency state changes (1000+ events)

### Deployment Status

- **Mode:** READ_WRITE (primary automation system)
- **Docker:** Available with GHCR push automation
- **Production Status:** ✅ In production, Node-RED deprecated

---

## Executive Summary

### Purpose

This document describes the architecture for a Golang-based home automation system that replaces the existing Node-RED implementation. The new system maintains exact functional parity with the current Node-RED flows while providing better performance, maintainability, and type safety.

### Goals

1. **1:1 Functional Migration** - Replicate all active Node-RED flows exactly as they currently behave
2. **Home Assistant as State Store** - Use HA input helpers (40 variables) as the persistent data store
3. **Modular Architecture** - Plugin-based design allows independent development and testing
4. **Configuration Compatibility** - Reuse existing YAML configuration files without modification
5. **Seamless Transition** - Successfully migrated from Node-RED

### Architecture Principles

1. **Plugin-Based Monolith**
   - Single compiled binary for simplified deployment
   - Each automation domain is a separate plugin
   - Plugins communicate via state changes and events

2. **Event-Driven Design**
   - All state changes trigger callbacks
   - Plugins subscribe to relevant state variables
   - Loose coupling between plugins

3. **Home Assistant as Source of Truth**
   - All persistent state stored in HA input helpers
   - Golang system maintains in-memory cache for performance
   - Bidirectional synchronization ensures consistency

4. **Configuration as Code**
   - YAML configuration files define behavior
   - No hardcoded logic in business rules
   - Git-tracked configuration for version control

---

## System Architecture

### High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Home Automation System (Golang)               │
│                                                                   │
│  ┌──────────────┐  ┌─────────────────────────────────────────┐  │
│  │    Main      │  │         Plugin Manager                   │  │
│  │  Application │  │                                          │  │
│  │              │  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ │  │
│  │  - Bootstrap │  │  │  Music   │ │ Lighting │ │ Security │ │  │
│  │  - Lifecycle │  │  │  Plugin  │ │  Plugin  │ │  Plugin  │ │  │
│  │  - Logging   │  │  └──────────┘ └──────────┘ └──────────┘ │  │
│  │              │  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ │  │
│  └──────┬───────┘  │  │  Sleep   │ │  Energy  │ │    TV    │ │  │
│         │          │  │  Plugin  │ │  Plugin  │ │  Plugin  │ │  │
│         │          │  └──────────┘ └──────────┘ └──────────┘ │  │
│  ┌──────▼───────┐  │  ┌──────────┐ ┌──────────┐ ┌──────────┐ │  │
│  │    Core      │  │  │  State   │ │   Load   │ │ Calendar │ │  │
│  │  Components  │  │  │ Tracking │ │ Shedding │ │  Plugin  │ │  │
│  │              │  │  └──────────┘ └──────────┘ └──────────┘ │  │
│  │ - State Mgr  │  │  ┌──────────┐                            │  │
│  │ - Config Ldr │  │  │ Nagging  │                            │  │
│  │ - HA Client  │  │  │  Plugin  │                            │  │
│  └──────┬───────┘  │  └──────────┘                            │  │
│         │          └─────────────────────────────────────────┘  │
│         │                                                         │
└─────────┼─────────────────────────────────────────────────────────┘
          │
          │ WebSocket
          │
┌─────────▼─────────────────────────────────────────────────────────┐
│                      Home Assistant                                │
│                                                                     │
│  ┌──────────────────┐  ┌──────────────┐  ┌───────────────────┐   │
│  │  Input Helpers   │  │   Devices    │  │   Services        │   │
│  │  (40 variables)  │  │              │  │                   │   │
│  │                  │  │ - Sonos      │  │ - call_service    │   │
│  │ - Booleans (27)  │  │ - Hue        │  │ - set_value       │   │
│  │ - Numbers (3)    │  │ - Apple TV   │  │ - turn_on/off     │   │
│  │ - Text (8)       │  │ - Bravia TV  │  │ - media_player.*  │   │
│  │ - Local-only (2) │  │ - Lutron     │  │                   │   │
│  │                  │  │ - Roborock   │  │                   │   │
│  └──────────────────┘  │ - Thermostat │  └───────────────────┘   │
│                        └──────────────┘                           │
└─────────────────────────────────────────────────────────────────────┘
```

### Visual Documentation

📊 For detailed Mermaid diagrams, see **[docs/human/VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md)**

Available diagrams:
- System architecture and data flow
- Plugin architecture and interactions
- Music Manager logic flow
- Lighting Control logic flow
- Energy State logic flow
- State variable dependency graph

---

## Core Components

### 1. State Manager

**Responsibility:** Manages the 40 state variables (38 synchronized + 2 local-only) and provides thread-safe access.

**Key Features:**
- In-memory cache of all HA input helpers
- Thread-safe read/write operations using sync.RWMutex
- Automatic synchronization with Home Assistant
- Callback mechanism on state changes
- Support for atomic compare-and-swap operations

**Interface:**
```go
type StateManager interface {
    GetBool(key string) (bool, error)
    SetBool(key string, value bool) error
    GetString(key string) (string, error)
    SetString(key string, value string) error
    GetNumber(key string) (float64, error)
    SetNumber(key string, value float64) error
    GetJSON(key string, target interface{}) error
    SetJSON(key string, value interface{}) error
    CompareAndSwapBool(key string, old, new bool) (bool, error)
    Subscribe(key string, handler StateChangeHandler) Subscription
    SyncFromHA() error
}
```

**State Variables (40 total):**

See **[docs/reference/migration_mapping.md](../reference/migration_mapping.md)** for complete mapping.

- **Boolean (27):** isNickHome, isCarolineHome, isAssistantHere, isAnyOwnerHome, isAnyoneHome, isAnyoneHomeAndAwake, isMasterAsleep, isGuestAsleep, isAnyoneAsleep, isEveryoneAsleep, isGuestBedroomDoorOpen, isHaveGuests, isAppleTVPlaying, isTVPlaying, isTVon, isFadeOutInProgress, isFreeEnergyAvailable, isGridAvailable, isExpectingSomeone, isNickOfficeOccupied, isKitchenOccupied, isPrimaryBedroomDoorOpen, isNickNearHome, isCarolineNearHome, isLockdown, reset, isFrontOfHousePersonPresent
- **Number (3):** alarmTime, remainingSolarGeneration, thisHourSolarGeneration
- **Text (8):** dayPhase, sunevent, musicPlaybackType, currentlyPlayingMusicUri, musicPlaylistRotation, batteryEnergyLevel, currentEnergyLevel, solarProductionEnergyLevel
- **Local-only (2):** didOwnerJustReturnHome (bool), currentlyPlayingMusic (JSON)

### 2. Home Assistant Client

**Responsibility:** Manages communication with Home Assistant via WebSocket.

**Features:**
- Connection management with auto-reconnect
- Entity state queries
- Service call execution
- Event subscription
- Rate limiting and retry logic
- **Thread-safe writes** (writeMu mutex)

**Interface:**
```go
type HAClient interface {
    Connect() error
    Disconnect() error
    IsConnected() bool
    GetState(entityID string) (*State, error)
    SetState(entityID string, state interface{}) error
    CallService(domain, service string, data map[string]interface{}) error
    SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error)
    SetInputBoolean(name string, value bool) error
    SetInputNumber(name string, value float64) error
    SetInputText(name string, value string) error
}
```

### 3. Config Loader

**Responsibility:** Loads and validates YAML configuration files.

**Supported Configs:**
- `music_config.yaml` - Music playback modes and playlists
- `hue_config.yaml` - Lighting scenes and room mappings
- `schedule_config.yaml` - Time-based schedules
- `energy_config.yaml` - Energy level thresholds

---

## Automation Plugins

Each plugin corresponds to a Node-RED flow and implements domain-specific automation logic.

### 1. State Tracking Plugin

**Node-RED Flow:** State Tracking

**Responsibilities:**
- Track presence (Nick, Caroline, Assistant home/away)
- Determine derived states (any owner home, anyone home)
- Track sleep states (master asleep, guest asleep, everyone asleep)
- Monitor door states (guest bedroom door, office occupancy)
- Announce arrivals/departures via TTS

**Key Automations:**
- **Presence Detection**: Listen to HA presence triggers → Update presence booleans
- **Derived Presence**: Calculate `isAnyOwnerHome`, `isAnyoneHome` from individual states
- **Sleep Detection**: Monitor bedroom lights/doors → Update sleep states
- **Arrival Notifications**: On owner arrival → Announce via TTS

**Events Consumed:** `ha.binary_sensor.*.changed`, `ha.light.primary_suite.*.changed`

**Events Published:** `state.presence.changed`, `state.sleep.changed`

### 2. Lighting Control Plugin ✅

**Node-RED Flow:** Lighting Control

**Responsibilities:**
- Activate lighting scenes based on day phase
- Respond to sun events (sunrise, sunset, dusk)
- Handle manual overrides
- Manage Christmas tree seasonal lighting

**Key Automations:**
- **Sun Event Scenes**: On sun event change → Activate appropriate scene
- **Day Phase Scenes**: When `dayPhase` changes → Apply scene to each room
- **TV Brightness**: Dim TV area when TV playing

**Events Consumed:** `state.dayPhase.changed`, `state.sunevent.changed`, `state.isAnyoneHome.changed`, `state.isTVPlaying.changed`

**Config File:** `hue_config.yaml`

### 3. Music Management Plugin

**Node-RED Flow:** Music

**Responsibilities:**
- Manage Sonos speaker groups and playback
- Select appropriate music mode based on context
- Handle volume management with fade in/out
- Prevent playback when inappropriate (sleep, away)
- Direct Sonos speaker control via SoCo-CLI HTTP API (volume, mute, group, playback, shuffle, repeat)
- Tidal playlist playback via SoCo-CLI sharelink mechanism

**Key Automations:**
- **Mode Selection**: Based on `dayPhase`, presence, sleep state → Determine music mode
- **Playback Control**: Mode change → Build participant groups → Set volumes → Start playback (all speaker commands via SoCo-CLI when configured, fallback to HA)
- **Shutdown on Exit**: Everyone leaves → Stop all playback

**Events Consumed:** `state.dayPhase.changed`, `state.isAnyoneHome.changed`, `state.isMasterAsleep.changed`, `state.isGuestAsleep.changed`, `state.isAssistantHere.changed`, `state.isTVPlaying.changed`

**Config File:** `music_config.yaml`

### 4. Sleep Hygiene Plugin ✅

**Node-RED Flow:** Sleep Hygiene

**Responsibilities:**
- Fade out sleep sounds in the morning
- Trigger wake-up sequences
- Coordinate with lighting for gentle wake
- Handle cuddle notifications

**Key Automations:**
- **Wake Detection**: Morning time + master occupied → Begin fade out
- **Fade Out**: Gradually reduce volume → Turn on bedroom lights → Switch to day music
- **Schedule-Based**: Read wakeup time from schedule config

**Events Consumed:** `state.dayPhase.changed`, `state.isMasterAsleep.changed`, `state.alarmTime.changed`

**Config File:** `schedule_config.yaml`

### 5. Energy State Plugin ✅

**Node-RED Flow:** Energy State

**Responsibilities:**
- Calculate current energy availability level
- Track solar generation (current hour, remaining day)
- Monitor battery state
- Clear retired metered-grid free-energy state

**Key Automations:**
- **Battery Level**: HA sensor → Convert to energy level enum → Update `batteryEnergyLevel`
- **Solar Calculation**: Solar forecast → Calculate remaining generation
- **Overall Level**: Combine battery + solar → Determine `currentEnergyLevel`

**Events Consumed:** `ha.sensor.battery_percentage.changed`, `ha.sensor.solar_generation.changed`

**Config File:** `energy_config.yaml`

### 6. Load Shedding Plugin

**Node-RED Flow:** Load Shedding

**Responsibilities:**
- Adjust thermostat settings based on energy state
- Widen temperature ranges when energy is scarce
- Restore comfort settings when energy is plentiful
- **Thermal battery**: Pre-condition house by shifting HVAC setpoints when energy is abundant (white level)

**Events Consumed:** `state.currentEnergyLevel.changed`, `state.isAnyoneHome.changed`, `state.isEveryoneAsleep.changed`

### 7. Security Plugin

**Node-RED Flow:** Security

**Responsibilities:**
- Automatic lockdown when everyone asleep or away
- Garage door automation on arrival
- Doorbell notifications
- "Expecting someone" mode

**Events Consumed:** `state.isEveryoneAsleep.changed`, `state.isAnyoneHome.changed`, `state.isExpectingSomeone.changed`

### 8. TV Monitoring Plugin ✅

**Node-RED Flow:** TV Monitoring and Manipulation

**Responsibilities:**
- Detect when TV or Apple TV is playing
- Control soundbar input selection
- Adjust TV brightness by time of day
- Manage sync box state

**Key Automations:**
- **Playback Detection**: Monitor Apple TV state → Update `isAppleTVPlaying` and `isTVPlaying`
- **TV State Tracking**: Sync box sensors → Determine `isTVOn`
- **Brightness Adjustment**: Day phase change → Set TV brightness level

**Events Consumed:** `ha.media_player.apple_tv.changed`, `ha.sensor.hue_sync.changed`, `state.dayPhase.changed`

### 9. Day Phase Plugin ✅

**Responsibilities:**
- Calculate current day phase based on time and sun position
- Track sun events (sunrise, sunset, dusk, etc.)
- Publish dayPhase and sunevent state variables

**Key Automations:**
- **Sun Position Tracking**: Monitor HA sun sensor → Calculate current sun event
- **Day Phase Calculation**: Combine time of day + sun position → Determine day phase (morning, day, sunset, dusk, winddown, night)

**Events Published:** `state.dayPhase.changed`, `state.sunevent.changed`

### 10. Sex Mode Plugin ✅

**Responsibilities:**
- Manage intimate mode with lighting and music coordination
- Control Eight Sleep pod temperature
- Preserve and restore previous music playback type

**Key Automations:**
- **Mode Activation**: On input_boolean.sex → Set lighting scene, switch music to "sex" mode
- **Pod Control**: Activate/deactivate Eight Sleep warming
- **State Preservation**: Store preSexMusicType for restoration after

**Events Consumed:** `input_boolean.sex`, `state.dayPhase.changed`, `state.isMasterAsleep.changed`

### 11. Christmas Plugin ✅

**Responsibilities:**
- Manage Christmas tree lighting based on season and presence
- Control tree on/off based on day phase

**Events Consumed:** `state.dayPhase.changed`, `state.isAnyoneHome.changed`

### 12. Reset Coordinator ✅

**Responsibilities:**
- Watch for reset boolean activation
- Orchestrate system-wide plugin resets
- Clear rate limiters and re-evaluate all conditions

**Events Consumed:** `state.reset.changed`

### 13. Sensor Config Plugin ✅

**Responsibilities:**
- Configure Zigbee sensor reporting thresholds at startup
- Set temperature reporting sensitivity (0.1°F units)
- Configure low temperature alerts for freeze prevention
- Set battery reporting thresholds

**Key Automations:**
- **Startup Configuration**: On plugin start → Configure all sensor thresholds via ZHA
- **Threshold Settings**: Temperature, battery, and alert thresholds set via number entities

**Config File:** `sensor_config.yaml`

### 14. EV Charger Safety Plugin ✅

**Responsibilities:**
- Monitor EV charger plug (Shelly Wave Plug US) for safety conditions
- Detect overheat, overcurrent, and overvoltage conditions
- Emergency shutoff with retry mechanism when any safety condition triggers
- Send urgent notifications via ntfy and TTS announcements

**Key Automations:**
- **Safety Monitoring**: Subscribe to binary safety sensors → On any "on" state → Emergency shutoff
- **Emergency Shutoff**: Turn off `switch.leaf_charger` with up to 3 retries on failure
- **Startup Check**: On plugin start → Verify no safety conditions already active
- **Notifications**: Rate-limited (5 min cooldown) ntfy alerts + TTS announcements to all speakers

**Entities Monitored:** `binary_sensor.leaf_charger_overheat_detected`, `binary_sensor.leaf_charger_over_current_detected`, `binary_sensor.leaf_charger_over_voltage_detected`, `sensor.leaf_charger_electric_consumption_w`, `switch.leaf_charger`

### 15. Vacuum Plugin ✅

**Responsibilities:**
- Monitor Casper's error sensor and announce actionable errors via TTS
- Announce a short recovery confirmation on configured speakers when an active
  error clears and someone is home
- Suppress announcements while master is asleep
- Re-announce unresolved errors on a configurable interval (default 2 hours) until cleared

**Key Automations:**
- **Error Announcement**: On error sensor transition from "No error" to a real error → TTS to common-area speakers
- **Unavailable Debounce**: Hold transient `"unavailable"` / `"unknown"` error
  sensor states for 60 seconds; discard them if Casper recovers inside the window
- **Clear Confirmation**: On real error transition back to "No error" while
  `isAnyoneHome=true` → TTS to the sitting room speaker by default
- **Quiet Hours**: When `isMasterAsleep=true` → notifier returns `ErrSuppressedAsleep`;
  vacuum records in shadow state and preserves repeat cadence
- **Repeat Cadence**: Background ticker re-announces unresolved errors every `repeat_interval`
- **Initial State**: On plugin start → if sensor already reports an error, announce immediately

**Entities Monitored:** `sensor.valetudo_mellowslimyloris_error` (configurable);
reads `isAnyoneHome` for clear confirmations

**Config File:** `vacuum_config.yaml` (also reserves shape for future schedule,
room sequencing, and per-room vacuum/mop parameters)

### 16. Integration Watchdog Plugin ✅

**Responsibilities:**
- Periodically scan configured Home Assistant entities for bad states (e.g., `unavailable`, `unknown`) or stale `last_updated` timestamps
- Trigger a HA config-entry reload via `homeassistant.reload_config_entry` WebSocket call when a detection rule fires
- Enforce per-target cooldown and daily-cap safeguards to prevent reload storms
- Track reload outcomes (success/failure, count, timestamps) in shadow state

**Key Automations:**
- **Bad-State Duration**: When all watched entities for a target have been in a bad state for `bad_state_duration_min` → reload
- **Stale Timestamp**: When the newest `last_updated` across watched entities is older than `stale_last_updated_min` → reload
- **Cooldown**: Block subsequent reloads for a target within `cooldown_min` after the last reload
- **Daily Cap**: Block reloads once `daily_max` reloads have fired in the current calendar day

**Entities Monitored:** Fully configurable per target via `integration_watchdog_config.yaml`

**Config File:** `configs/integration_watchdog_config.yaml`

---

## Data Flow

### Startup Sequence

```
1. Main Application Start
   ↓
2. Initialize Logger (zap)
   ↓
3. Load Configuration Files (YAML)
   ↓
4. Connect to Home Assistant (WebSocket)
   ↓
5. Initialize State Manager
   ↓
6. Sync State from HA (read all 40 state variables)
   ↓
7. Load and Initialize Plugins
   ↓
8. Start Plugins (begin subscriptions)
   ↓
9. System Ready - Begin Event Processing
```

### State Change Propagation

```
┌──────────────────┐
│  Home Assistant  │
│  Input Helper    │
│  Value Changes   │
└────────┬─────────┘
         │
         │ WebSocket Event
         ↓
┌────────────────────┐
│   HA Client        │
│  Event Listener    │
└────────┬───────────┘
         │
         │ Internal Callback
         ↓
┌────────────────────┐
│  State Manager     │
│  Update Cache      │
└────────┬───────────┘
         │
         │ Notify Subscribers
         ↓
┌────────────────────┐
│  Plugins           │
│  Event Handlers    │
│  Business Logic    │
└────────┬───────────┘
         │
         │ Actions (call HA services, update state)
         ↓
┌────────────────────┐
│   HA Client        │
│  Service Calls     │
└────────┬───────────┘
         │
         │ API Calls
         ↓
┌────────────────────┐
│  Home Assistant    │
│  Execute Action    │
└────────────────────┘
```

---

## State Synchronization

### Synchronization Strategy

**Bidirectional Sync:**
- **HA → Golang**: WebSocket events update in-memory cache immediately
- **Golang → HA**: All state changes written to HA input helpers via service calls

**Conflict Resolution:**
- Home Assistant is always the source of truth
- On startup, Golang loads all state from HA
- In case of sync failures, Golang retries with exponential backoff

### Computed State Registry

The system includes a **ComputedStateRegistry** for managing derived state variables that are automatically computed from other state variables. This provides centralized dependency tracking and automatic recomputation when inputs change.

**Registered Computed States:**

| Variable | Formula | Dependencies |
|----------|---------|--------------|
| `isAnyOwnerHome` | `isNickHome OR isCarolineHome` | isNickHome, isCarolineHome |
| `isAnyoneHome` | `isAnyOwnerHome OR isAssistantHere` (departure debounced 5 min) | isAnyOwnerHome, isAssistantHere |
| `isAnyoneAsleep` | `isMasterAsleep OR isGuestAsleep` | isMasterAsleep, isGuestAsleep |
| `isEveryoneAsleep` | `isMasterAsleep AND isGuestAsleep` | isMasterAsleep, isGuestAsleep |
| `isAnyoneHomeAndAwake` | `(isAnyOwnerHome && !isAnyoneAsleep) \|\| isAssistantHere \|\| wakeSequenceLatch` | isAnyOwnerHome, isAnyoneAsleep, isAssistantHere, isWakeSequenceActive |
| `solarProductionEnergyLevel` | Solar level from thresholds | thisHourSolarGeneration, remainingSolarGeneration |
| `currentEnergyLevel` | Combined battery + solar level | batteryEnergyLevel, solarProductionEnergyLevel |

**Key Features:**
- **Dependency-triggered updates**: Automatically recomputes when any dependency changes
- **Topological sorting**: Ensures correct initialization order (Level 1 before Level 2)
- **Wake sequence latch**: Special handling for morning wake-up scenarios
- **Callbacks**: Optional callbacks for shadow state updates when values change

**Architecture Decision: Plugin-computed vs Registry-computed:**

Some computed states remain in their plugins rather than the registry:

| State Variable | Location | Reason |
|---------------|----------|--------|
| `batteryEnergyLevel` | Energy plugin | Depends on sensor data (not state variables) |
| `dayPhase`, `sunevent` | DayPhase plugin | Time-based periodic computation, not dependency-triggered |

---

## Configuration Management

### Configuration Files

The system reuses existing YAML configuration files:

| Config File | Purpose |
|-------------|---------|
| `music_config.yaml` | Music modes, Spotify URIs, volumes, participants |
| `hue_config.yaml` | Lighting scenes, room mappings |
| `schedule_config.yaml` | Time-based schedules, wakeup times |
| `energy_config.yaml` | Energy level thresholds |
| `sensor_config.yaml` | Zigbee sensor thresholds and alert settings |

---

## Project Structure

```
homeautomation-go/
├── cmd/
│   └── main.go                      # ✅ Entry point
├── internal/
│   ├── ha/                          # ✅ Home Assistant WebSocket client
│   │   ├── client.go                # ✅ Main client (with writeMu fix)
│   │   ├── client_test.go           # ✅ Comprehensive tests
│   │   ├── types.go                 # ✅ HA message types
│   │   └── mock.go                  # ✅ Mock client for testing
│   ├── state/                       # ✅ State Manager
│   │   ├── manager.go               # ✅ State manager implementation
│   │   ├── manager_test.go          # ✅ Unit tests
│   │   └── variables.go             # ✅ 40 state variable definitions
│   ├── alert/                       # ✅ Unified alert dispatcher (ntfy + TTS)
│   │   ├── alert.go                 # ✅ Alerter interface and Manager implementation
│   │   ├── alert_test.go            # ✅ Unit tests
│   │   └── mock.go                  # ✅ MockAlerter for unit tests
│   ├── debounce/                    # ✅ Shared UnavailableDebouncer — debounces transient unavailable/unknown HA states
│   │   ├── unavailable.go           # ✅ UnavailableDebouncer implementation
│   │   └── unavailable_test.go      # ✅ Unit tests
│   ├── notify/                      # ✅ Shared verbal (TTS) notifier
│   │   ├── notify.go                # ✅ Notifier implementation (Kokoro TTS + media_player.play_media announce:true)
│   │   ├── config.go                # ✅ Config loader (notification_config.yaml)
│   │   └── mock.go                  # ✅ MockNotifier for unit tests
│   ├── tts/                         # ✅ Kokoro TTS client + local MP3 file server
│   │   ├── client.go                # ✅ Kokoro HTTP client (OpenAI-compatible /audio/speech)
│   │   ├── server.go                # ✅ LAN HTTP server — serves MP3s to Sonos speakers
│   │   ├── synthesizer.go           # ✅ Synthesizer interface + Service wiring
│   │   └── mock.go                  # ✅ MockSynthesizer for unit tests
│   └── plugins/                     # ✅ Automation plugins (17 total)
│       ├── statetracking/           # ✅ State Tracking plugin
│       ├── dayphase/                # ✅ Day Phase plugin
│       ├── energy/                  # ✅ Energy State plugin
│       ├── lighting/                # ✅ Lighting Control plugin
│       ├── music/                   # ✅ Music Management plugin
│       ├── sleephygiene/            # ✅ Sleep Hygiene plugin
│       ├── tv/                      # ✅ TV Monitoring plugin
│       ├── environmental/           # ✅ Environmental plugin (humidity, water leak)
│       ├── security/                # ✅ Security plugin
│       ├── sensorhealth/            # ✅ Sensor Health plugin (battery, node status, temp lockup)
│       ├── infrastructure/          # ✅ Infrastructure plugin (septic, thermostat monitoring)
│       ├── waterflow/               # ✅ Water Flow plugin (pipe break detection)
│       ├── loadshedding/            # ✅ Load Shedding plugin
│       ├── sexmode/                 # ✅ Sex Mode plugin
│       ├── christmas/               # ✅ Christmas plugin
│       ├── reset/                   # ✅ Reset Coordinator
│       └── integrationwatchdog/     # ✅ Integration Watchdog plugin
├── test/
│   └── integration/                 # ✅ Integration test suite
├── Dockerfile                       # ✅ Production container
├── docker-compose.yml               # ✅ Development environment
├── go.mod                           # ✅ Go module definition
└── README.md                        # ✅ User guide
```

---

## Implementation Checklist

### Phase 1-5: MVP ✅ COMPLETE

- ✅ Project setup with Go modules
- ✅ Home Assistant WebSocket client
- ✅ State Manager with 40 variables
- ✅ Demo application
- ✅ Comprehensive test suite
- ✅ Integration tests with mock HA server
- ✅ Docker support

### Phase 6: Plugin Implementation ✅ COMPLETE

- ✅ State Tracking plugin
- ✅ Day Phase plugin
- ✅ Energy State plugin
- ✅ Lighting Control plugin
- ✅ Music Management plugin
- ✅ Sleep Hygiene plugin
- ✅ TV Monitoring plugin
- ✅ Environmental plugin (humidity, water leak)
- ✅ Security plugin
- ✅ Sensor Health plugin (battery, node status, temp lockup)
- ✅ Infrastructure plugin (septic system, thermostat monitoring)
- ✅ Water Flow plugin (pipe break detection)
- ✅ Load Shedding plugin
- ✅ Sex Mode plugin
- ✅ Christmas plugin
- ✅ Reset Coordinator

### Success Criteria for MVP ✅ ACHIEVED

1. ✅ All 40 state variables sync from HA to Golang on startup
2. ✅ State changes in HA reflected in Golang cache within 1 second
3. ✅ State changes in Golang written to HA successfully
4. ✅ WebSocket reconnection works with exponential backoff
5. ✅ All unit tests pass with >65% coverage
6. ✅ Thread-safe concurrent access verified (5,000+ operations tested)
7. ✅ Integration test suite validates correctness
8. ✅ Critical concurrency bugs fixed
9. ✅ Docker deployment ready

---

## Code Templates & Examples

### HA WebSocket Authentication Flow
```
1. Connect WebSocket to ws://homeassistant.local:8123/api/websocket
2. Receive: {"type": "auth_required"}
3. Send: {"type": "auth", "access_token": "YOUR_TOKEN"}
4. Receive: {"type": "auth_ok", "ha_version": "..."}
5. Now authenticated - can send commands
```

### HA Message Examples
```json
// Get state
{"id": 1, "type": "get_states"}

// Subscribe to events
{"id": 2, "type": "subscribe_events", "event_type": "state_changed"}

// Call service
{
  "id": 3,
  "type": "call_service",
  "domain": "input_boolean",
  "service": "turn_on",
  "service_data": {"entity_id": "input_boolean.nick_home"}
}
```

---

## Production Roadmap

### Phase 7: Production Preparation (NEXT)

1. **Parallel Testing with Node-RED**
   - Run both systems side-by-side in READ_ONLY mode
   - Compare state synchronization behavior
   - Validate identical state tracking

2. **Performance Validation**
   - Long-running stability test (24+ hours)
   - Memory leak detection
   - Connection resilience testing

### Phase 8+: Full Production

3. **Complete Plugin Implementation**
   - Port remaining plugins from Node-RED
   - Add comprehensive tests

4. **Switch to Read-Write Mode**
   - Remove READ_ONLY restriction
   - Enable full state management
   - Deploy as primary automation system

5. **Deprecate Node-RED**
   - After validation period
   - Full cutover to Golang implementation

---

## HomeKit Integration

### Current Node-RED HomeKit Accessories (14 total)

The existing Node-RED implementation exposes 14 HomeKit accessories via NRCHKB:
- State Tracking: Masters Asleep, Guest Asleep, Have Guests
- Lighting: Bright, Christmas Tree
- Music: Airplay, Sex, Volume Restore
- Vacuum: Clean Kitchen, Clean Floors, Clean Entryway, Clean Master Bath, Clean Cat Area
- Configuration: Reset

### Migration Strategy

**Recommendation:** Use Home Assistant's native HomeKit integration instead of implementing a HomeKit bridge in Golang.

**Rationale:**
- Architectural consistency (everything through HA)
- Proven reliability of HA HomeKit integration
- Simpler implementation (no HAP protocol needed)

**Implementation:**
1. Create corresponding HA entities (`input_boolean`, `input_button`)
2. Configure HA HomeKit integration to expose these entities
3. Golang plugins subscribe to entity changes and trigger actions

---

## Resources and References

### Internal Documentation

- **[docs/human/VISUAL_ARCHITECTURE.md](../human/VISUAL_ARCHITECTURE.md)** - Mermaid diagrams
- **[docs/reference/SHADOW_STATE.md](../reference/SHADOW_STATE.md)** - Shadow state pattern
- **[docs/reference/PLUGIN_SYSTEM.md](../reference/PLUGIN_SYSTEM.md)** - Plugin interfaces
- **[docs/reference/migration_mapping.md](../reference/migration_mapping.md)** - State variable mapping
- **[docs/reference/CONCURRENCY_LESSONS.md](../reference/CONCURRENCY_LESSONS.md)** - Concurrency patterns
- **[homeautomation-go/README.md](../../homeautomation-go/README.md)** - User guide
- **[homeautomation-go/test/integration/README.md](../../homeautomation-go/test/integration/README.md)** - Integration tests

### External Documentation

- [Go Documentation](https://go.dev/doc/)
- [Home Assistant WebSocket API](https://developers.home-assistant.io/docs/api/websocket)
- [gorilla/websocket](https://pkg.go.dev/github.com/gorilla/websocket)
- [zap Logger](https://pkg.go.dev/go.uber.org/zap)

### Node-RED Reference (Archived)

- **Flow Configuration:** `docs/archive/flows.json` (archived for historical reference)

---

**Status:** PRODUCTION - Go implementation is the primary automation system
