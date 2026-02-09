package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper functions are now provided by pkg/testutil and re-exported via mock_ha_server.go:
// - FilterServiceCalls(calls, domain, service)
// - FindServiceCallWithData(calls, domain, service, dataKey, dataValue)
// - FindServiceCallWithEntityID(calls, domain, service, entityID)

// Lowercase aliases for backward compatibility with existing tests
func filterServiceCalls(calls []ServiceCall, domain, service string) []ServiceCall {
	return FilterServiceCalls(calls, domain, service)
}

func findServiceCallWithData(calls []ServiceCall, domain, service, dataKey string, dataValue interface{}) *ServiceCall {
	return FindServiceCallWithData(calls, domain, service, dataKey, dataValue)
}

// TestScenario_MockServerServiceCallTracking validates that the mock server
// correctly tracks service calls for testing automation behavior
func TestScenario_MockServerServiceCallTracking(t *testing.T) {
	t.Parallel()
	server, _, manager, cleanup := setupTest(t)
	defer cleanup()

	// Clear any initialization calls
	server.ClearServiceCalls()

	t.Log("GIVEN: No service calls have been made")
	calls := server.GetServiceCalls()
	assert.Equal(t, 0, len(calls), "Should start with no service calls")

	// WHEN: We make various service calls through the manager
	t.Log("WHEN: Making service calls through the state manager")

	// Boolean service call
	err := manager.SetBool("isNickHome", true)
	require.NoError(t, err)

	// Number service call
	err = manager.SetNumber("alarmTime", 1234567890.0)
	require.NoError(t, err)

	// String service call
	err = manager.SetString("dayPhase", "evening")
	require.NoError(t, err)

	// Wait for all service calls to be tracked
	waitForServiceCallCount(t, server, 3, "Should have at least 3 service calls")

	// THEN: All service calls should be tracked
	t.Log("THEN: Verifying all service calls were tracked")
	calls = server.GetServiceCalls()

	t.Logf("Total service calls tracked: %d", len(calls))

	// Should have at least our 3 calls
	assert.GreaterOrEqual(t, len(calls), 3, "Should track at least our 3 service calls")

	// Verify specific service calls were made
	boolCall := server.FindServiceCall("input_boolean", "turn_on", "input_boolean.nick_home")
	assert.NotNil(t, boolCall, "Should find input_boolean.turn_on call")
	if boolCall != nil {
		assert.Equal(t, "input_boolean", boolCall.Domain)
		assert.Equal(t, "turn_on", boolCall.Service)
		t.Logf("Found boolean service call: %s.%s for %v", boolCall.Domain, boolCall.Service, boolCall.ServiceData["entity_id"])
	}

	numberCall := server.FindServiceCall("input_number", "set_value", "input_number.alarm_time")
	assert.NotNil(t, numberCall, "Should find input_number.set_value call")
	if numberCall != nil {
		assert.Equal(t, "input_number", numberCall.Domain)
		assert.Equal(t, "set_value", numberCall.Service)
		t.Logf("Found number service call: %s.%s with value=%v", numberCall.Domain, numberCall.Service, numberCall.ServiceData["value"])
	}

	textCall := server.FindServiceCall("input_text", "set_value", "input_text.day_phase")
	assert.NotNil(t, textCall, "Should find input_text.set_value call")
	if textCall != nil {
		assert.Equal(t, "input_text", textCall.Domain)
		assert.Equal(t, "set_value", textCall.Service)
		assert.Equal(t, "evening", textCall.ServiceData["value"])
		t.Logf("Found text service call: %s.%s with value=%s", textCall.Domain, textCall.Service, textCall.ServiceData["value"])
	}

	// Test count function
	boolCallCount := server.CountServiceCalls("input_boolean", "turn_on")
	assert.GreaterOrEqual(t, boolCallCount, 1, "Should have at least one input_boolean.turn_on call")
	t.Logf("Total input_boolean.turn_on calls: %d", boolCallCount)

	// WHEN: We clear service calls
	t.Log("WHEN: Clearing service calls")
	server.ClearServiceCalls()

	// THEN: No calls should be tracked
	t.Log("THEN: Service call tracking should be empty")
	calls = server.GetServiceCalls()
	assert.Equal(t, 0, len(calls), "Should have no calls after clearing")
}

// TestScenario_ServiceCallFiltering tests the helper functions for filtering service calls
func TestScenario_ServiceCallFiltering(t *testing.T) {
	t.Parallel()
	// Create some test service calls
	calls := []ServiceCall{
		{Domain: "scene", Service: "activate", ServiceData: map[string]interface{}{"entity_id": "scene.living_room_evening"}},
		{Domain: "scene", Service: "activate", ServiceData: map[string]interface{}{"entity_id": "scene.bedroom_night"}},
		{Domain: "light", Service: "turn_on", ServiceData: map[string]interface{}{"entity_id": "light.living_room"}},
		{Domain: "input_boolean", Service: "turn_on", ServiceData: map[string]interface{}{"entity_id": "input_boolean.test"}},
	}

	// Test filterServiceCalls
	sceneActivations := filterServiceCalls(calls, "scene", "activate")
	assert.Equal(t, 2, len(sceneActivations), "Should find 2 scene.activate calls")

	lightCalls := filterServiceCalls(calls, "light", "turn_on")
	assert.Equal(t, 1, len(lightCalls), "Should find 1 light.turn_on call")

	// Test findServiceCallWithData
	livingRoomScene := findServiceCallWithData(calls, "scene", "activate", "entity_id", "scene.living_room_evening")
	assert.NotNil(t, livingRoomScene, "Should find living room scene call")

	nonexistent := findServiceCallWithData(calls, "scene", "activate", "entity_id", "scene.nonexistent")
	assert.Nil(t, nonexistent, "Should not find nonexistent scene")
}
