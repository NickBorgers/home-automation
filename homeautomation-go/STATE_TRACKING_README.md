# State Tracking and Configuration

This document describes the State Tracking and Configuration functionality implemented in the Golang home automation system, equivalent to the Node-RED "State Tracking" and "Configuration" tabs.

## Overview

The implementation provides:
1. **Derived State Computation** - Automatically calculates derived states like `isAnyoneHome` and `isEveryoneAsleep`
2. **Auto Sleep Detection** - Automatically detects when guests fall asleep based on door state
3. **Configuration File Loading** - Loads and manages YAML configuration files
4. **Sun Event Tracking** - Calculates sun events (sunrise, sunset, etc.)
5. **Day Phase Calculation** - Determines current day phase based on sun events and schedule

## State Tracking

### Derived States

The `DerivedStateHelper` (in `internal/state/helpers.go`) automatically manages derived states:

#### isAnyoneHome
```go
isAnyoneHome = isNickHome OR isCarolineHome
```

This state is automatically updated whenever `isNickHome` or `isCarolineHome` changes.

#### isEveryoneAsleep
```go
isEveryoneAsleep = isMasterAsleep AND isGuestAsleep
```

This state is automatically updated whenever `isMasterAsleep` or `isGuestAsleep` changes.

### Auto Sleep Detection

The helper automatically detects when a guest falls asleep based on:
- Guest bedroom door closes (transitions from open to closed)
- Someone is home (`isAnyoneHome == true`)
- Guests are present (`isHaveGuests == true`)
- Guest not already marked asleep (`isGuestAsleep == false`)

When all conditions are met, `isGuestAsleep` is automatically set to `true`.

### Usage

```go
import (
    "homeautomation/internal/state"
    "go.uber.org/zap"
)

// Create state manager
logger, _ := zap.NewDevelopment()
manager := state.NewManager(haClient, logger, false)

// Sync from Home Assistant
manager.SyncFromHA()

// Start derived state helper
helper := state.NewDerivedStateHelper(manager, logger)
helper.Start()
defer helper.Stop()

// Derived states are now automatically managed
// When isNickHome or isCarolineHome change, isAnyoneHome is updated automatically
```

## Configuration Management

### Configuration Loader

The `Loader` (in `internal/config/loader.go`) manages loading and reloading of YAML configuration files.

#### Supported Configuration Files

1. **music_config.yaml** - Music playlists, speakers, and volume settings
2. **hue_config.yaml** - Philips Hue lighting configurations
3. **schedule_config.yaml** - Daily schedules (wake, dusk, winddown, bed, night times)

#### Schedule Configuration Format

```yaml
schedule:
  - begin_wake: "05:00"
    wake: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  # ... entries for each day of week (0-6)
```

### Usage

```go
import (
    "homeautomation/internal/config"
    "go.uber.org/zap"
)

// Create loader
logger, _ := zap.NewDevelopment()
loader := config.NewLoader("/path/to/config/dir", logger)

// Load all configuration files
err := loader.LoadAll()

// Get configurations
musicConfig := loader.GetMusicConfig()
hueConfig := loader.GetHueConfig()
scheduleConfig := loader.GetScheduleConfig()

// Get today's parsed schedule
todaysSchedule, err := loader.GetTodaysSchedule()
// todaysSchedule.Wake, todaysSchedule.Dusk, etc. are time.Time values for today

// Start auto-reload (reloads daily at 00:01)
loader.StartAutoReload()
defer loader.Stop()
```

## Sun Event Tracking & Day Phase Calculation

### Sun Event Calculator

The `Calculator` (in `internal/dayphase/calculator.go`) manages sun event tracking and day phase calculation.

#### Sun Events

The calculator simplifies detailed sun events into:
- `morning` - Dawn through sunrise
- `day` - Daytime hours
- `sunset` - Golden hour and sunset
- `dusk` - Civil twilight
- `night` - Night time

#### Day Phases

Day phases combine sun events with schedule overrides:
- `morning` - Sunrise until noon
- `day` - Noon until golden hour
- `sunset` - Golden hour through sunset
- `dusk` - After sunset twilight
- `winddown` - Evening transition before night
- `night` - Late evening and night hours

### Usage

```go
import (
    "homeautomation/internal/dayphase"
    "homeautomation/internal/config"
    "go.uber.org/zap"
)

// Create calculator (Austin, TX coordinates, local timezone)
logger, _ := zap.NewDevelopment()
timezone, _ := time.LoadLocation("America/Chicago")
calc := dayphase.NewCalculator(32.85486, -97.50515, timezone, logger)

// Update sun times (call on startup and every 6 hours)
calc.UpdateSunTimes()

// Get current sun event
sunEvent := calc.GetSunEvent()
// Returns: morning, day, sunset, dusk, or night

// Calculate current day phase (with schedule)
schedule, _ := configLoader.GetTodaysSchedule()
dayPhase := calc.CalculateDayPhase(schedule)
// Returns: morning, day, sunset, dusk, winddown, or night

// Start periodic updates (every 6 hours)
stopChan := calc.StartPeriodicUpdate()
defer close(stopChan)
```

## Integration Example

Here's how to integrate all components:

```go
package main

import (
    "homeautomation/internal/config"
    "homeautomation/internal/dayphase"
    "homeautomation/internal/ha"
    "homeautomation/internal/state"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewDevelopment()

    // Connect to Home Assistant
    haClient := ha.NewHAClient("wss://homeassistant/api/websocket", "token", logger)
    haClient.Connect()
    defer haClient.Disconnect()

    // Create and sync state manager
    manager := state.NewManager(haClient, logger, false)
    manager.SyncFromHA()

    // Start derived state helper
    stateHelper := state.NewDerivedStateHelper(manager, logger)
    stateHelper.Start()
    defer stateHelper.Stop()

    // Load configurations
    configLoader := config.NewLoader("/path/to/configs", logger)
    configLoader.LoadAll()
    configLoader.StartAutoReload()
    defer configLoader.Stop()

    // Setup sun event tracking with timezone
    timezone, _ := time.LoadLocation("America/Chicago")
    dayPhaseCalc := dayphase.NewCalculator(32.85486, -97.50515, timezone, logger)
    sunStopChan := dayPhaseCalc.StartPeriodicUpdate()
    defer close(sunStopChan)

    // Get current day phase
    schedule, _ := configLoader.GetTodaysSchedule()
    currentDayPhase := dayPhaseCalc.CalculateDayPhase(schedule)
    logger.Info("Current day phase", zap.String("phase", string(currentDayPhase)))

    // Your automation logic here...
}
```

## Testing

All components have comprehensive test coverage:

```bash
# Preferred: uses caching (skips if code unchanged)
make unit-tests
make integration-tests
```

### Test Coverage

- **State Helpers**: 8 tests covering derived states and auto-sleep detection
- **Config Loader**: 7 tests covering YAML loading and schedule parsing
- **Day Phase Calculator**: 6 tests covering sun events and phase calculation

All tests include race detection and edge case handling.

## Architecture Notes

### Design Decisions

1. **Subscription-Based Updates**: The `DerivedStateHelper` uses subscriptions to automatically update derived states, matching Node-RED's event-driven behavior.

2. **Separate Packages**: Configuration and day phase logic are in separate packages for modularity and testability.

3. **Auto-Reload**: Configuration files are automatically reloaded daily at 00:01, matching Node-RED's cron schedule.

4. **Periodic Sun Updates**: Sun times are recalculated every 6 hours to account for changing seasons.

### Differences from Node-RED

1. **No HomeKit Direct Integration**: The Golang version exposes states via Home Assistant entities. HomeKit sync happens through HA's HomeKit Bridge integration.

2. **Type Safety**: All states are type-checked at compile time, preventing runtime type errors.

3. **Better Testing**: Comprehensive unit and integration tests ensure correctness.

4. **Explicit Dependencies**: All dependencies are clearly defined in `go.mod` rather than NPM packages.

## Future Enhancements

Potential improvements for future iterations:

1. **File Watcher**: Add file watching to reload configs immediately when files change (instead of daily cron).

2. **Configuration Validation**: Add schema validation for YAML files to catch configuration errors early.

3. **Metrics**: Add Prometheus metrics for state changes, config reloads, etc.

4. **Web UI**: Add a simple web interface to view current states and day phase.

5. **More Derived States**: Add additional derived states as needed (e.g., `isAnyoneAsleep`, `isAnyOwnerHome`).

## See Also

- [Node-RED Analysis](../docs/archive/NODE_RED_TABS_ANALYSIS.md) - Detailed analysis of original Node-RED implementation
- [Architecture](../docs/architecture/ARCHITECTURE.md) - Overall system architecture and migration status
- [Main README](./README.md) - Golang project overview
