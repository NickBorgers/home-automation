# Integration Tests

Comprehensive integration tests for the Home Automation Go client with a full mock Home Assistant WebSocket server.

## What This Tests

### 1. Mock Home Assistant Server
- Full WebSocket API implementation
- Simulates authentication flow
- State storage and retrieval
- Service call handling
- Event broadcasting with configurable delays
- Network latency simulation

### 2. Test Scenarios

#### Basic Functionality
- ✅ Connection and authentication
- ✅ Initial state synchronization
- ✅ State updates (Get/Set operations)
- ✅ All state types (bool, number, string, JSON)

#### Concurrency & Race Conditions
- ✅ Concurrent reads (50 goroutines × 100 reads)
- ✅ Concurrent writes (20 goroutines × 50 writes)
- ✅ Mixed read/write operations
- ✅ CompareAndSwap atomic operations

#### Deadlock Detection
- ✅ Subscription callbacks that trigger more state operations
- ✅ Rapid state changes from both server and client
- ✅ Multiple subscribers on same entity
- ✅ Subscription unsubscribe behavior

#### Edge Cases
- ✅ High-frequency state changes (1000+ changes)
- ✅ Multiple subscribers on same entity (per-subscription ID handling)
- ✅ Reconnection after disconnect
- ✅ Network latency handling

## Running the Tests

### Run All Integration Tests (Recommended)
```bash
make integration-tests    # Uses caching — skips if code unchanged
```

### Run with Docker (Isolated Environment)
```bash
docker-compose -f docker-compose.integration.yml up --build
```

### Run Specific Tests
When you need to run a single test or subset, use `go test` with a `-run` filter:
```bash
cd homeautomation-go

# Test only deadlock scenarios
go test -v -race -run TestSubscription ./test/integration/

# Test only concurrent operations
go test -v -race -run TestConcurrent ./test/integration/

# Test subscription bug
go test -v -run TestMultipleSubscribers ./test/integration/
```
> **Note:** Direct `go test` is appropriate here because `make` targets don't support `-run` filters. For running the full suite, always prefer `make integration-tests`.

## Test Status

✅ **All 13 integration tests passing!**

All critical bugs discovered during testing have been fixed:
- ✅ Concurrent WebSocket writes (fixed with `writeMu` mutex)
- ✅ Subscription memory leak (fixed with per-subscription IDs)
- ✅ All race conditions resolved
- ✅ No known failures

**NEW:** Multi-plugin integration scenario tests added!
- ✅ Mock server tracks all service calls for automation testing
- ✅ Helper functions for verifying automation behavior
- ✅ 6 multi-plugin integration scenarios validating real-world automation workflows
- ✅ Tests cover TV+Lighting, Energy+Lighting, Presence, Sleep, Day Phase coordination
- ✅ Validates plugin interactions without race conditions or conflicts

## What Each Test Does

### TestBasicConnection
- Verifies connection to mock HA server
- Tests initial state sync
- Confirms state updates propagate both ways

### TestStateChangeSubscription
- Subscribes to state changes
- Server triggers state change
- Verifies callback receives correct old/new values

### TestConcurrentReads
- Spawns 50 goroutines
- Each performs 100 concurrent reads
- Ensures no deadlocks on read operations

### TestConcurrentWrites
- Spawns 20 goroutines writing concurrently
- Tests mutex contention
- Ensures no race conditions or deadlocks

### TestConcurrentReadsAndWrites
- Mixed workload: 10 readers + 5 writers
- Runs for 3 seconds continuously
- Tests real-world concurrent access patterns

### TestSubscriptionWithConcurrentWrites ⚠️ **CRITICAL**
- Subscribes to state changes
- Callback triggers MORE state operations (potential deadlock)
- Server and client both trigger rapid changes
- Tests the exact scenario that could cause deadlock

**This test specifically checks for**:
1. Callback acquires lock to read state → ✅ Should work (lock released)
2. Callback calls SetBool → ✅ Should work (new lock acquisition)
3. SetBool triggers HA update → state change event → another callback → potential recursion

### TestMultipleSubscribersOnSameEntity
- Tests multiple subscribers to the same entity
- Verifies per-subscription IDs work correctly
- Confirms unsubscribe only removes specific handler

### TestCompareAndSwapRaceCondition
- 20 goroutines compete for CAS operation
- Tests atomicity of CompareAndSwapBool
- Ensures only one succeeds at acquiring lock

### TestReconnection
- Stops server mid-connection
- Verifies client detects disconnect
- Restarts server
- Confirms auto-reconnection with exponential backoff

### TestHighFrequencyStateChanges
- Sends 1000 rapid state changes
- Tests system under high load
- Verifies most events are received (allows ~20% loss)

### TestAllStateTypes
- Comprehensive test of all type operations
- Boolean, Number, String, JSON
- Ensures type safety and conversions work

### TestScenario_MockServerServiceCallTracking 🆕
- Validates mock server tracks service calls
- Tests GetServiceCalls(), FindServiceCall(), ClearServiceCalls()
- Verifies service call filtering and counting
- Foundation for automation behavior testing

### TestScenario_ServiceCallFiltering 🆕
- Tests helper functions for service call verification
- filterServiceCalls() - filter by domain/service
- findServiceCallWithData() - find calls with specific parameters
- Demonstrates scenario testing patterns

## Debugging Tips

> **Note:** The debugging commands below use `go test` directly because they require specific flags (`-timeout`, `GODEBUG`, output redirection) not available via `make` targets. For routine test runs, use `make integration-tests` instead.

### If Tests Hang (Deadlock)
```bash
cd homeautomation-go

# Run with timeout and race detector
go test -v -race -timeout=30s ./test/integration/

# Get goroutine dump on timeout
GODEBUG=gctrace=1 go test -v -timeout=10s ./test/integration/
```

### View Detailed Logs
```bash
cd homeautomation-go
go test -v ./test/integration/ 2>&1 | tee test-output.log
```

### Check for Race Conditions
```bash
cd homeautomation-go
# Race detector will print detailed reports
go test -race ./test/integration/
```

## Mock Server Features

### Configurable Event Delay
```go
server := NewMockHAServer(addr, token)
server.SetEventDelay(100 * time.Millisecond) // Simulate network latency
```

### Manual State Changes
```go
server.SetState("input_boolean.test", "on", map[string]interface{}{})
// Automatically broadcasts state_changed event to all clients
```

### State Inspection
```go
state := server.GetState("input_boolean.test")
fmt.Printf("Current state: %s\n", state.State)
```

### Service Call Tracking 🆕
```go
// Get all service calls made
calls := server.GetServiceCalls()

// Find specific service call
call := server.FindServiceCall("scene", "activate", "scene.living_room_evening")

// Count service calls
count := server.CountServiceCalls("light", "turn_on")

// Clear tracked calls
server.ClearServiceCalls()
```

**Use case:** Verify that automation plugins call the correct Home Assistant services with the correct parameters.

## Continuous Integration

Add to `.github/workflows/test.yml`:
```yaml
- name: Run Integration Tests
  run: docker-compose -f docker-compose.integration.yml up --abort-on-container-exit
```

## Performance Benchmarks

Expected performance on typical hardware:

- **Connection time**: <100ms
- **State sync (27 vars)**: <200ms
- **State change latency**: <50ms
- **Concurrent operations**: 5000+ ops/sec
- **No deadlocks**: Even under extreme concurrent load

## Known Issues

All previously identified issues have been resolved:

1. ✅ **Deadlock in SetBool** - FALSE ALARM (code is correct, lock released before client call)
2. ✅ **Subscription memory leak** - FIXED (per-subscription IDs implemented, TestMultipleSubscribersOnSameEntity passes)
3. ✅ **Race conditions** - All 11 tests pass with `-race` flag

No known bugs or issues remain. All integration tests pass successfully.

## Scenario-Based Testing (NEW)

### What is Scenario Testing?

Scenario tests validate end-to-end automation behavior by:
1. Simulating real-world events (e.g., time of day changes, sensor updates)
2. Verifying automation plugins respond correctly
3. Checking that correct Home Assistant services are called

### Example Scenario Test

```go
func TestScenario_DayPhaseChangeActivatesScenes(t *testing.T) {
    server, manager, cleanup := setupScenarioTest(t)
    defer cleanup()

    // GIVEN: Morning, someone is home
    server.SetState("input_text.day_phase", "morning", ...)
    server.ClearServiceCalls()

    // WHEN: Day phase changes to evening
    server.SetState("input_text.day_phase", "evening", ...)
    time.Sleep(500 * time.Millisecond)

    // THEN: Verify evening scenes were activated
    calls := server.GetServiceCalls()
    sceneActivations := filterServiceCalls(calls, "scene", "activate")
    assert.Greater(t, len(sceneActivations), 0)
}
```

### Current Status

✅ **Infrastructure complete** - Mock server tracking and helper functions working
✅ **Multi-plugin integration tests complete** - 6 comprehensive tests validating real-world scenarios
✅ **All tests passing** - No race conditions, deadlocks, or conflicts between plugins

### Multi-Plugin Integration Test Scenarios

Located in `scenario_multi_plugin_test.go`:

1. **TestScenario_TVPlaying_DimsLivingRoomLights** - TV + Lighting coordination
2. **TestScenario_LowEnergy_PluginsCoexist** - Energy + Lighting coexistence
3. **TestScenario_EveryoneLeaves_CoordinatedResponse** - Presence tracking across plugins
4. **TestScenario_SleepSequence_CoordinatesLighting** - Sleep state affects lighting
5. **TestScenario_DayPhaseChange_MultiPluginCoordination** - Time-based multi-plugin response
6. **TestScenario_SimultaneousStateChanges_NoRaceConditions** - Concurrent plugin safety

Run all scenario tests: `make integration-tests`
Run a specific scenario: `cd homeautomation-go && go test -v -race -run TestScenario_ ./test/integration/...`

## Next Steps

With all bugs fixed:
1. ✅ All infrastructure tests passing!
2. ✅ Scenario testing infrastructure complete
3. 🔨 Add scenario tests for each automation plugin (Lighting, Energy, TV, etc.)
4. Add benchmarks for performance regression testing
5. Add fuzzing tests for edge cases
6. Test with real Home Assistant instance in production-like scenarios
