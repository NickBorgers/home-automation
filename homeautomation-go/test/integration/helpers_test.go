package integration

import (
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ntfy"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Test Helper Constants
// ============================================================================

const (
	// stateWaitTimeout is the maximum time to wait for state changes.
	// This should be long enough to handle slow CI environments but short
	// enough that tests don't hang for too long on actual failures.
	stateWaitTimeout = 2 * time.Second

	// statePollInterval is how often to check if the expected state is reached.
	// Smaller values make tests faster in normal conditions but increase CPU usage.
	statePollInterval = 10 * time.Millisecond
)

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
	assert.Eventually(t, func() bool {
		val, err := manager.GetBool(key)
		return err == nil && val == expected
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForStringState polls until the state variable has the expected string value or times out.
// Use this instead of time.Sleep after triggering state changes that should set string values.
func waitForStringState(t *testing.T, manager *state.Manager, key string, expected string, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, func() bool {
		val, err := manager.GetString(key)
		return err == nil && val == expected
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForStringStateOneOf polls until the state variable has one of the expected string values or times out.
// Use this when multiple values are acceptable (e.g., "red" or "yellow" are both valid low-energy states).
func waitForStringStateOneOf(t *testing.T, manager *state.Manager, key string, expected []string, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, func() bool {
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
	assert.Eventually(t, func() bool {
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

// waitForServiceCallCount polls until the expected number of service calls is reached.
// Use this when verifying that a specific number of service calls have been made.
func waitForServiceCallCount(t *testing.T, server *MockHAServer, expectedCount int, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, func() bool {
		calls := server.GetServiceCalls()
		return len(calls) >= expectedCount
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForServiceCall polls until any service call matching domain/service is found.
// Use this when you need to verify a service call was made without checking a specific entity.
func waitForServiceCall(t *testing.T, server *MockHAServer, domain, service string, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, func() bool {
		calls := server.GetServiceCalls()
		for _, call := range calls {
			if call.Domain == domain && call.Service == service {
				return true
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForCondition polls until the provided condition function returns true or times out.
// Use this for generic conditions that don't fit the other helper patterns.
func waitForCondition(t *testing.T, condition func() bool, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, condition, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForNtfyNotification polls until a notification with the given title is found in the mock Ntfy client.
// Use this when testing notification delivery through the Ntfy service.
func waitForNtfyNotification(t *testing.T, mockNtfy *ntfy.MockClient, title string, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, func() bool {
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
	assert.Eventually(t, func() bool {
		state := server.GetState(entityID)
		return state != nil && state.State == expectedState
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}

// waitForSubscriberNotification polls until the provided counter reaches the expected value.
// Use this instead of time.Sleep when waiting for subscriber callbacks to complete.
func waitForSubscriberNotification(t *testing.T, counter *int32, expected int32, msgAndArgs ...interface{}) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return atomic.LoadInt32(counter) >= expected
	}, stateWaitTimeout, statePollInterval, msgAndArgs...)
}
