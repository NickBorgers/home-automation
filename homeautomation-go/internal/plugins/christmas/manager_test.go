package christmas

import (
	"testing"
	"time"

	"homeautomation/internal/ha"

	"go.uber.org/zap"
)

// TestChristmasManager_ActivationTurnsOnHolidayLights tests that activating christmas turns on holiday lights
func TestChristmasManager_ActivationTurnsOnHolidayLights(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.christmas", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager (not read-only)
	manager := NewManager(mockHA, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate christmas being turned on
	mockHA.SimulateStateChange("input_boolean.christmas", "on")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify holiday lights were turned on with label via Target
	calls := mockHA.GetServiceCalls()
	foundLightCall := false
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if call.Target != nil && len(call.Target.LabelID) > 0 && call.Target.LabelID[0] == HolidayLightLabelID {
				foundLightCall = true
				break
			}
		}
	}

	if !foundLightCall {
		t.Errorf("Expected light.turn_on with target label_id '%s' to be called, but it was not. Calls: %+v", HolidayLightLabelID, calls)
	}
}

// TestChristmasManager_ActivationResetsToggle tests that christmas toggle is reset to off after activation
func TestChristmasManager_ActivationResetsToggle(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.christmas", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager (not read-only)
	manager := NewManager(mockHA, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate christmas being turned on
	mockHA.SimulateStateChange("input_boolean.christmas", "on")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify input_boolean.christmas was turned off
	calls := mockHA.GetServiceCalls()
	foundTurnOff := false
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.christmas" {
				foundTurnOff = true
				break
			}
		}
	}

	if !foundTurnOff {
		t.Errorf("Expected input_boolean.turn_off for input_boolean.christmas to be called, but it was not. Calls: %+v", calls)
	}
}

// TestChristmasManager_OffDoesNothing tests that turning off christmas does nothing
func TestChristmasManager_OffDoesNothing(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.christmas", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager (not read-only)
	manager := NewManager(mockHA, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate christmas being turned off
	mockHA.SimulateStateChange("input_boolean.christmas", "off")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify no service calls were made
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls when christmas is turned off, got %d calls: %+v", len(calls), calls)
	}
}

// TestChristmasManager_ReadOnlyMode tests that read-only mode prevents service calls
func TestChristmasManager_ReadOnlyMode(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.christmas", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager in read-only mode
	manager := NewManager(mockHA, logger, true, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Activate christmas
	mockHA.SimulateStateChange("input_boolean.christmas", "on")
	time.Sleep(100 * time.Millisecond)

	// Verify no service calls were made (all should be read-only)
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls in read-only mode, got %d calls: %+v", len(calls), calls)
	}
}

// TestChristmasManager_ShadowState tests that shadow state is properly tracked
func TestChristmasManager_ShadowState(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.christmas", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager
	manager := NewManager(mockHA, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Get initial shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.LightsActivated != 0 {
		t.Errorf("Expected initial LightsActivated to be 0, got %d", shadowState.Outputs.LightsActivated)
	}

	// Activate christmas
	mockHA.SimulateStateChange("input_boolean.christmas", "on")
	time.Sleep(100 * time.Millisecond)

	// Get shadow state after activation
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.LightsActivated != 1 {
		t.Errorf("Expected LightsActivated to be 1 after activation, got %d", shadowState.Outputs.LightsActivated)
	}
	if shadowState.Outputs.LastActivationTime.IsZero() {
		t.Errorf("Expected LastActivationTime to be set after activation")
	}
	if shadowState.Outputs.LastActionReason == "" {
		t.Errorf("Expected LastActionReason to be set after activation")
	}
}

// TestChristmasManager_Reset tests the Reset function
func TestChristmasManager_Reset(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	// Christmas is ON in HA
	mockHA.SetState("input_boolean.christmas", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager
	manager := NewManager(mockHA, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Clear any calls
	mockHA.ClearServiceCalls()

	// Reset should detect the HA state and activate
	if err := manager.Reset(); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify services were called (light turn on and toggle reset)
	calls := mockHA.GetServiceCalls()
	if len(calls) < 2 {
		t.Errorf("Expected at least 2 service calls after reset (light.turn_on and input_boolean.turn_off), got %d", len(calls))
	}

	// Verify light was turned on via target label_id
	foundLightCall := false
	for _, call := range calls {
		if call.Domain == "light" && call.Service == "turn_on" {
			if call.Target != nil && len(call.Target.LabelID) > 0 && call.Target.LabelID[0] == HolidayLightLabelID {
				foundLightCall = true
				break
			}
		}
	}
	if !foundLightCall {
		t.Errorf("Expected light.turn_on with target label_id to be called during reset")
	}
}

// TestChristmasManager_ResetWhenOff tests the Reset function when christmas is off
func TestChristmasManager_ResetWhenOff(t *testing.T) {
	// Setup
	mockHA := ha.NewMockClient()
	// Christmas is OFF in HA
	mockHA.SetState("input_boolean.christmas", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()

	// Create christmas manager
	manager := NewManager(mockHA, logger, false, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start christmas manager: %v", err)
	}
	defer manager.Stop()

	// Clear any calls
	mockHA.ClearServiceCalls()

	// Reset should not trigger any actions
	if err := manager.Reset(); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify no services were called
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls when christmas is off, got %d calls: %+v", len(calls), calls)
	}
}
