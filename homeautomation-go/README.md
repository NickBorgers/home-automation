# Home Automation - Golang Client

A robust Golang client for managing Home Assistant state variables with type-safe operations, automatic synchronization, and real-time event subscriptions.

> **Implementation Details**: This project was implemented based on the design documented in [../docs/architecture/IMPLEMENTATION_PLAN.md](../docs/architecture/IMPLEMENTATION_PLAN.md), which outlines the architecture, design decisions, and migration strategy from the existing Node-RED implementation.

## Features

- **WebSocket Client**: Full Home Assistant WebSocket API implementation
- **Type-Safe State Management**: Strongly-typed getters/setters for 30 state variables (28 synced + 2 local-only)
- **Auto-Synchronization**: Bidirectional state sync between Golang and Home Assistant
- **Real-Time Updates**: Subscribe to state changes with callback handlers
- **Thread-Safe**: Concurrent access protection with mutexes
- **Atomic Operations**: CompareAndSwap for race-free boolean updates
- **Auto-Reconnection**: Automatic reconnection with exponential backoff
- **Comprehensive Testing**: >80% test coverage with mock client for testing
- **Automation Plugins**: Extensible plugin system for implementing home automation logic

## Automation Plugins

The system includes several automation plugins that implement intelligent home automation logic:

- **Energy State Manager**: Monitors battery levels, solar generation, and grid availability
- **Lighting Control Manager**: Activates scenes based on day phase, presence, and sleep status
- **Music Manager**: Selects appropriate music modes based on time of day and occupancy
- **TV Monitoring Manager**: Tracks TV and Apple TV playback states
- **Sleep Hygiene Manager**: Manages wake-up sequences, sleep music fade-out, and bedtime reminders
- **Load Shedding Manager**: Controls thermostat based on energy availability
- **Security Manager**: Handles lockdown and garage automation

## State Variables

The system manages 30 state variables across 3 types (28 synced with HA + 2 local-only):

### Booleans (18) - Synced with HA
- Home presence: `isNickHome`, `isCarolineHome`, `isToriHere`, `isAnyOwnerHome`, `isAnyoneHome`
- Sleep status: `isMasterAsleep`, `isGuestAsleep`, `isAnyoneAsleep`, `isEveryoneAsleep`
- Guest management: `isGuestBedroomDoorOpen`, `isHaveGuests`, `isExpectingSomeone`
- Media: `isAppleTVPlaying`, `isTVPlaying`, `isTVon`
- System: `isFadeOutInProgress`, `isFreeEnergyAvailable`, `isGridAvailable`

### Numbers (3) - Synced with HA
- `alarmTime`
- `remainingSolarGeneration`
- `thisHourSolarGeneration`

### Strings (7) - Synced with HA
- `dayPhase`
- `sunevent`
- `musicPlaybackType`
- `currentlyPlayingMusicUri` - URI of currently playing music (Spotify/HTTP)
- `batteryEnergyLevel`
- `currentEnergyLevel`
- `solarProductionEnergyLevel`

### Local-Only (2) - In-Memory
- `didOwnerJustReturnHome` (Boolean) - Transient state for return-home detection
- `currentlyPlayingMusic` (JSON) - Full music playback details, too large to store in HA

## Prerequisites

- Go 1.24 or higher
- Home Assistant instance with WebSocket API enabled
- Long-lived access token from Home Assistant

## Installation

1. **Clone or navigate to the project directory:**
   ```bash
   cd homeautomation-go
   ```

2. **Install dependencies:**
   ```bash
   go mod download
   ```

3. **Create environment configuration:**
   ```bash
   cp .env.example .env
   ```

4. **Edit `.env` with your Home Assistant credentials:**
   ```env
   HA_URL=wss://your-homeassistant/api/websocket
   HA_TOKEN=your_long_lived_access_token
   READ_ONLY=true
   TIMEZONE=America/New_York
   HTTP_PORT=8080
   ```

   **Configuration Options:**
   - `HA_URL`: WebSocket URL to your Home Assistant instance
     - Use `wss://` for HTTPS connections
     - Use `ws://` for HTTP connections (local development)
   - `HA_TOKEN`: Long-lived access token from Home Assistant
   - `READ_ONLY`: Set to `true` for read-only mode (recommended for parallel testing)
     - `true`: Only reads and monitors state, makes NO changes to Home Assistant
     - `false`: Can read and write state changes
   - `TIMEZONE` (Optional): Timezone for time-based automations (e.g., free energy windows)
     - Default: `UTC`
     - Examples: `America/New_York`, `America/Chicago`, `America/Los_Angeles`, `Europe/London`
     - See [IANA Time Zone Database](https://en.wikipedia.org/wiki/List_of_tz_database_time_zones) for all valid values
     - This ensures automations trigger at the correct local time, not UTC
   - `HTTP_PORT` (Optional): Port for the HTTP API server
     - Default: `8080`
     - The API provides endpoints for querying state (see HTTP API section below)

   **Read-Only Mode** is perfect for:
   - Running alongside your existing Node-RED setup
   - Testing and validation without risk
   - Monitoring state changes without interference

   To create a long-lived access token:
   - Go to Home Assistant → Profile → Long-Lived Access Tokens
   - Click "Create Token"
   - Copy the token to your `.env` file

## Usage

### Running the Demo Application

**Read-Only Mode (Recommended for first run):**
```bash
# Already set in .env: READ_ONLY=true
go run cmd/main.go
```

**Read-Write Mode:**
```bash
# Edit .env and set: READ_ONLY=false
go run cmd/main.go
```

**What the application does:**
1. Connects to Home Assistant
2. Syncs all 28 state variables
3. Displays current state
4. Subscribes to state changes
5. **If READ_ONLY=false**: Demonstrates setting values (temporarily toggles isExpectingSomeone and isFadeOutInProgress, then restores)
6. **If READ_ONLY=true**: Only monitors, makes NO changes
7. Monitors changes until interrupted (Ctrl+C)

### Using in Your Code

```go
package main

import (
    "homeautomation/internal/ha"
    "homeautomation/internal/state"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewProduction()

    // Create and connect HA client
    client := ha.NewClient("ws://homeassistant:8123/api/websocket", "your_token", logger)
    client.Connect()
    defer client.Disconnect()

    // Create state manager
    manager := state.NewManager(client, logger, false)
    manager.SyncFromHA()

    // Get boolean state
    isHome, _ := manager.GetBool("isNickHome")

    // Set boolean state
    manager.SetBool("isExpectingSomeone", true)

    // Get string state
    phase, _ := manager.GetString("dayPhase")

    // Set number state
    manager.SetNumber("alarmTime", 1668524400000)

    // Subscribe to changes
    manager.Subscribe("isAnyoneHome", func(key string, oldValue, newValue interface{}) {
        logger.Info("Someone came home or left!")
    })

    // Atomic operations
    swapped, _ := manager.CompareAndSwapBool("isFadeOutInProgress", false, true)
}
```

## HTTP API

The application includes an HTTP API server for querying the current state of all variables. This is useful for:
- Monitoring state from external tools
- Building dashboards
- Debugging and development
- Integration with other services

### API Endpoints

#### `GET /api/state`

Returns the current state of all variables in JSON format.

**Response Format:**
```json
{
  "booleans": {
    "isNickHome": true,
    "isCarolineHome": false,
    "isAnyoneHome": true,
    ...
  },
  "numbers": {
    "alarmTime": 7.5,
    "remainingSolarGeneration": 1234.56,
    "thisHourSolarGeneration": 500.0
  },
  "strings": {
    "dayPhase": "morning",
    "sunevent": "sunrise",
    "musicPlaybackType": "default",
    "batteryEnergyLevel": "high",
    "currentEnergyLevel": "medium",
    "solarProductionEnergyLevel": "high"
  },
  "jsons": {
    "currentlyPlayingMusic": {
      "uri": "spotify:track:...",
      "name": "Song Title",
      "artist": "Artist Name"
    }
  }
}
```

#### `GET /health`

Simple health check endpoint that returns `{"status": "ok"}`.

### Configuration

The HTTP API server is configured via environment variables:

```env
# Optional: HTTP API server port
# Default: 8080
HTTP_PORT=8080
```

### Usage Examples

**Using curl:**
```bash
# Get all state
curl http://localhost:8080/api/state

# Pretty print with jq
curl http://localhost:8080/api/state | jq .

# Get just booleans
curl http://localhost:8080/api/state | jq .booleans

# Check a specific variable
curl http://localhost:8080/api/state | jq .booleans.isNickHome

# Health check
curl http://localhost:8080/health
```

**From a web browser:**
```
http://localhost:8080/api/state
```

**From another application:**
```go
resp, _ := http.Get("http://localhost:8080/api/state")
var state StateResponse
json.NewDecoder(resp.Body).Decode(&state)
fmt.Printf("Is anyone home? %v\n", state.Booleans["isAnyoneHome"])
```

**Note:** The API server starts automatically when the application runs. No additional setup is required.

## Docker

The application can be run in Docker for easy deployment and isolation.

### Quick Start with Docker

**Build the image:**
```bash
# From repository root
make docker-build-go

# Or directly
docker build -t homeautomation:latest ./homeautomation-go/
```

**Run the container:**
```bash
# Make sure you have .env file configured first
make docker-run-go

# Or directly
docker run --rm -it --env-file homeautomation-go/.env homeautomation:latest
```

### Pull from GitHub Container Registry

Pre-built images are automatically published to GHCR:

```bash
# Pull latest version
docker pull ghcr.io/nickborgersonlowsecuritynode/home-automation:latest

# Run from GHCR
docker run --rm -it --env-file .env ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
```

### Docker Compose Example

```yaml
version: '3.8'
services:
  homeautomation:
    image: ghcr.io/nickborgersonlowsecuritynode/home-automation:latest
    container_name: homeautomation
    restart: unless-stopped
    env_file:
      - .env
```

### Makefile Commands

- `make docker-build-go` - Build Docker image
- `make docker-run-go` - Build and run container
- `make docker-push-go` - Push to GHCR

For detailed Docker documentation, see [../docs/deployment/DOCKER.md](../docs/deployment/DOCKER.md).

## Testing

### Run all tests:
```bash
go test ./... -v
```

### Run tests with coverage:
```bash
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Run specific package tests:
```bash
go test ./internal/ha -v
go test ./internal/state -v
```

### Run race detector:
```bash
go test ./... -race
```

## Architecture

```
homeautomation-go/
├── cmd/
│   └── main.go              # Demo application
├── internal/
│   ├── ha/                  # Home Assistant client
│   │   ├── client.go        # WebSocket client implementation
│   │   ├── types.go         # Message types and structs
│   │   ├── mock.go          # Mock client for testing
│   │   └── client_test.go   # Client tests
│   └── state/               # State manager
│       ├── manager.go       # State management logic
│       ├── variables.go     # 30 state variable definitions
│       └── manager_test.go  # State manager tests
├── go.mod                   # Go module definition
├── go.sum                   # Dependency checksums
├── .env.example             # Environment template
├── .gitignore              # Git ignore rules
└── README.md               # This file
```

## API Reference

### HAClient Interface

```go
Connect() error
Disconnect() error
IsConnected() bool
GetState(entityID string) (*State, error)
GetAllStates() ([]*State, error)
CallService(domain, service string, data map[string]interface{}) error
SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error)
SetInputBoolean(name string, value bool) error
SetInputNumber(name string, value float64) error
SetInputText(name string, value string) error
```

### StateManager Interface

```go
SyncFromHA() error
GetBool(key string) (bool, error)
SetBool(key string, value bool) error
GetString(key string) (string, error)
SetString(key string, value string) error
GetNumber(key string) (float64, error)
SetNumber(key string, value float64) error
GetJSON(key string, target interface{}) error
SetJSON(key string, value interface{}) error
CompareAndSwapBool(key string, old, new bool) (bool, error)
Subscribe(key string, handler StateChangeHandler) (Subscription, error)
GetAllValues() map[string]interface{}
```

## Common Operations

### Check if anyone is home
```go
anyoneHome, err := manager.GetBool("isAnyoneHome")
if anyoneHome {
    // Someone is home
}
```

### Update day phase
```go
manager.SetString("dayPhase", "evening")
```

### Subscribe to energy availability
```go
manager.Subscribe("isFreeEnergyAvailable", func(key string, old, new interface{}) {
    if new.(bool) {
        // Free energy is now available!
    }
})
```

### Atomic flag setting
```go
// Only set to true if currently false (prevents race conditions)
success, err := manager.CompareAndSwapBool("isFadeOutInProgress", false, true)
if success {
    // Successfully acquired the fade-out lock
    defer manager.SetBool("isFadeOutInProgress", false)
    // Do fade out operation
}
```

## Troubleshooting

### Connection Issues
- Verify HA_URL is correct and uses `ws://` protocol
- Check that WebSocket API is enabled in Home Assistant
- Ensure access token is valid and not expired

### Authentication Failures
- Regenerate your long-lived access token
- Check that the token has proper permissions

### State Not Syncing
- Verify that input_* entities exist in Home Assistant
- Check HA logs for errors
- Ensure entities are not read-only

### Test Failures
- Ensure no other process is using the test ports
- Check that all dependencies are installed
- Run `go mod tidy` to clean up dependencies

## Performance

- **Sync Time**: ~100-200ms for all 27 variables
- **State Change Latency**: <100ms from HA event to callback
- **Memory Usage**: ~10-20MB typical
- **Concurrent Operations**: Thread-safe for unlimited concurrent access

## Development

### Adding New State Variables

1. Add to `internal/state/variables.go`:
```go
{Key: "myNewVar", EntityID: "input_boolean.my_new_var", Type: TypeBool, Default: false}
```

2. Create corresponding entity in Home Assistant

3. Sync and use:
```go
value, _ := manager.GetBool("myNewVar")
manager.SetBool("myNewVar", true)
```

## License

See parent project for license information.

## Contributing

This is part of a larger home automation migration project. See the main project documentation for contribution guidelines.
