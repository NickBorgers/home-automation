package integration

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ntfy"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/require"
)

// ============================================================================
// Test Helper Constants
// ============================================================================

const (
	// defaultStateWaitTimeout is the default maximum time to wait for state changes.
	// This should be long enough to handle slow CI environments (where many parallel
	// tests compete for CPU) but short enough that tests don't hang too long on
	// actual failures. Override with TEST_WAIT_TIMEOUT env var (e.g. "10s").
	defaultStateWaitTimeout = 5 * time.Second

	// statePollInterval is how often to check if the expected state is reached.
	// Smaller values make tests faster in normal conditions but increase CPU usage.
	statePollInterval = 10 * time.Millisecond
)

// stateWaitTimeout is the maximum time to wait for state changes.
// Initialized from TEST_WAIT_TIMEOUT env var or defaultStateWaitTimeout.
var stateWaitTimeout = defaultStateWaitTimeout

func init() {
	if v := os.Getenv("TEST_WAIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			stateWaitTimeout = d
		}
	}
}

// ============================================================================
// State Polling Helpers
//
// These helpers replace hardcoded time.Sleep calls with polling-based waits
// that complete as soon as the expected state is reached. This makes tests:
// - More reliable: no false failures due to variable latency under load
// - Faster: tests complete as soon as state is ready, not after fixed delays
// - Self-documenting: each wait clearly states what condition is expected
// ============================================================================

// waitForBoolState polls until the state variable has the expected boolean value or times out.
// Use this instead of time.Sleep after triggering state changes that should set boolean values.
func waitForBoolState(t *testing.T, manager *state.Manager, key string, expected bool, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		val, err := manager.GetBool(key)
		return err == nil && val == expected
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForStringState polls until the state variable has the expected string value or times out.
// Use this instead of time.Sleep after triggering state changes that should set string values.
func waitForStringState(t *testing.T, manager *state.Manager, key string, expected string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		val, err := manager.GetString(key)
		return err == nil && val == expected
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForStringStateOneOf polls until the state variable has one of the expected string values or times out.
// Use this when multiple values are acceptable (e.g., "red" or "yellow" are both valid low-energy states).
func waitForStringStateOneOf(t *testing.T, manager *state.Manager, key string, expected []string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		val, err := manager.GetString(key)
		if err != nil {
			return false
		}
		for _, exp := range expected {
			if val == exp {
				return true
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCallWithEntity polls until a service call matching the criteria (including entity) is found.
// Use this when you need to verify a service call was made for a specific entity.
func waitForServiceCallWithEntity(t *testing.T, server *MockHAServer, domain, service, entityID string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		calls := server.GetServiceCalls()
		for _, call := range calls {
			if call.Domain == domain && call.Service == service {
				if id, ok := call.ServiceData["entity_id"].(string); ok && id == entityID {
					return true
				}
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCallWithEntitySince polls until a service call matching the criteria (including entity)
// is found among calls recorded after the given index.
func waitForServiceCallWithEntitySince(t *testing.T, server *MockHAServer, since int, domain, service, entityID string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		calls := server.GetServiceCallsSince(since)
		for _, call := range calls {
			if call.Domain == domain && call.Service == service {
				if id, ok := call.ServiceData["entity_id"].(string); ok && id == entityID {
					return true
				}
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCallCountSince polls until at least expectedCount service calls have been
// recorded after the given index.
func waitForServiceCallCountSince(t *testing.T, server *MockHAServer, since int, expectedCount int, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		calls := server.GetServiceCallsSince(since)
		return len(calls) >= expectedCount
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCallSince polls until any service call matching domain/service is found
// among calls recorded after the given index.
func waitForServiceCallSince(t *testing.T, server *MockHAServer, since int, domain, service string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		calls := server.GetServiceCallsSince(since)
		for _, call := range calls {
			if call.Domain == domain && call.Service == service {
				return true
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCallsToStabilizeSince waits until the service call count has been stable
// (unchanged) for at least stabilizeWindow, only considering calls after the given index.
func waitForServiceCallsToStabilizeSince(t *testing.T, server *MockHAServer, since int, stabilizeWindow time.Duration) {
	t.Helper()
	// First wait for at least one call to appear
	require.Eventually(t, func() bool {
		return len(server.GetServiceCallsSince(since)) > 0
	}, stateWaitTimeout, statePollInterval, "expected at least one service call since snapshot before checking stability")

	// Then wait for count to stop changing for stabilizeWindow
	lastCount := -1
	var stableStart time.Time
	require.Eventually(t, func() bool {
		count := len(server.GetServiceCallsSince(since))
		if count != lastCount {
			lastCount = count
			stableStart = time.Now()
			return false
		}
		return time.Since(stableStart) >= stabilizeWindow
	}, stateWaitTimeout, statePollInterval, "service calls should stabilize")
}

// waitForServiceCallQuiescenceSince waits until the service call count (since the given
// snapshot index) has been stable for at least stabilizeWindow. Unlike
// waitForServiceCallsToStabilizeSince, this does NOT require any new calls to appear —
// it's suitable when the action under test may or may not produce service calls.
func waitForServiceCallQuiescenceSince(t *testing.T, server *MockHAServer, since int, stabilizeWindow time.Duration) {
	t.Helper()
	lastCount := -1
	var stableStart time.Time
	require.Eventually(t, func() bool {
		count := len(server.GetServiceCallsSince(since))
		if count != lastCount {
			lastCount = count
			stableStart = time.Now()
			return false
		}
		return time.Since(stableStart) >= stabilizeWindow
	}, stateWaitTimeout, statePollInterval, "service calls should reach quiescence")
}

// waitForProcessing blocks until all in-flight HA event handler goroutines have completed.
// Use this instead of time.Sleep(50 * time.Millisecond) after server.SetState() or
// stateManager.SetBool()/SetString() calls to ensure all handlers have finished processing
// before continuing with assertions or taking service call snapshots.
//
// This is deterministic: it waits for actual handler completion rather than an arbitrary
// fixed delay, making tests both faster and more reliable.
func waitForProcessing(t *testing.T, manager *state.Manager) {
	t.Helper()
	manager.WaitForProcessing()
}

// waitForCondition polls until the provided condition function returns true or times out.
// Use this for generic conditions that don't fit the other helper patterns.
// Uses require.Eventually so that a timeout immediately fails the test instead of
// continuing with subsequent assertions that would produce confusing cascading failures.
func waitForCondition(t *testing.T, condition func() bool, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, condition, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCallsToStabilize waits until the service call count has been stable
// (unchanged) for at least stabilizeWindow, indicating all async goroutines have finished
// making service calls. Use this before inspecting service calls when fire-and-forget
// goroutines (e.g., zone orchestration) may still be in flight.
func waitForServiceCallsToStabilize(t *testing.T, server *MockHAServer, stabilizeWindow time.Duration) {
	t.Helper()
	// First wait for at least one call to appear
	require.Eventually(t, func() bool {
		return len(server.GetServiceCalls()) > 0
	}, stateWaitTimeout, statePollInterval, "expected at least one service call before checking stability")

	// Then wait for count to stop changing for stabilizeWindow
	lastCount := -1
	var stableStart time.Time
	require.Eventually(t, func() bool {
		count := len(server.GetServiceCalls())
		if count != lastCount {
			lastCount = count
			stableStart = time.Now()
			return false
		}
		return time.Since(stableStart) >= stabilizeWindow
	}, stateWaitTimeout, statePollInterval, "service calls should stabilize")
}

// waitForNtfyNotification polls until a notification with the given title is found in the mock Ntfy client.
// Use this when testing notification delivery through the Ntfy service.
func waitForNtfyNotification(t *testing.T, mockNtfy *ntfy.MockClient, title string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		calls := mockNtfy.GetCalls()
		for _, call := range calls {
			if call.Title == title {
				return true
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServerState polls until the mock server has the expected state for the given entity.
// Use this instead of time.Sleep when waiting for server-side state propagation.
func waitForServerState(t *testing.T, server *MockHAServer, entityID, expectedState string, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		state := server.GetState(entityID)
		return state != nil && state.State == expectedState
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForSubscriberNotification polls until the provided counter reaches the expected value.
// Use this instead of time.Sleep when waiting for subscriber callbacks to complete.
func waitForSubscriberNotification(t *testing.T, counter *int32, expected int32, msgAndArgs ...interface{}) {
	t.Helper()
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(counter) >= expected
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}
