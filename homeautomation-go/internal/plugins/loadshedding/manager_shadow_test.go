package loadshedding

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// TestLoadSheddingShadowState_CaptureInputs tests that all subscribed inputs are captured
func TestLoadSheddingShadowState_CaptureInputs(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)

	// Set some state BEFORE starting manager (so initial state exists)
	if err := stateManager.SetString("currentEnergyLevel", "green"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}

	// Create and start load shedding manager (Start() registers subscriptions)
	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Get shadow state (inputs are captured during Start)
	shadowState := manager.GetShadowState()

	// Verify inputs were captured
	if len(shadowState.Inputs.Current) == 0 {
		t.Error("Expected current inputs to be captured, got empty map")
	}

	if val, ok := shadowState.Inputs.Current["currentEnergyLevel"]; !ok {
		t.Error("Expected currentEnergyLevel to be in current inputs")
	} else if val != "green" {
		t.Errorf("Expected currentEnergyLevel='green', got '%v'", val)
	}
}

// TestLoadSheddingShadowState_RecordEnableAction tests that enable actions update shadow state correctly
func TestLoadSheddingShadowState_RecordEnableAction(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)
	if err := stateManager.SetString("currentEnergyLevel", "red"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}

	// Create load shedding manager
	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)

	// Record an enable action
	reason := "Energy state is red (low battery) - restricting HVAC"
	manager.recordAction(true, "enable", reason, true, tempLowRestricted, tempHighRestricted, "test_trigger")

	// Verify shadow state was updated
	shadowState := manager.GetShadowState()

	if !shadowState.Outputs.Active {
		t.Error("Expected Active=true after enable action")
	}

	if shadowState.Outputs.LastActionType != "enable" {
		t.Errorf("Expected LastActionType='enable', got '%s'", shadowState.Outputs.LastActionType)
	}

	if shadowState.Outputs.LastActionReason != reason {
		t.Errorf("Expected LastActionReason='%s', got '%s'", reason, shadowState.Outputs.LastActionReason)
	}

	if !shadowState.Outputs.ThermostatSettings.HoldMode {
		t.Error("Expected HoldMode=true after enable action")
	}

	if shadowState.Outputs.ThermostatSettings.TempLow != tempLowRestricted {
		t.Errorf("Expected TempLow=%.1f, got %.1f", tempLowRestricted, shadowState.Outputs.ThermostatSettings.TempLow)
	}

	if shadowState.Outputs.ThermostatSettings.TempHigh != tempHighRestricted {
		t.Errorf("Expected TempHigh=%.1f, got %.1f", tempHighRestricted, shadowState.Outputs.ThermostatSettings.TempHigh)
	}

	// Verify inputs were captured
	if len(shadowState.Inputs.Current) == 0 {
		t.Error("Expected current inputs to be captured, got empty map")
	}

	if len(shadowState.Inputs.AtLastAction) == 0 {
		t.Error("Expected at-last-action inputs to be captured, got empty map")
	}

	// Verify timestamp is recent
	if time.Since(shadowState.Outputs.LastActionTime) > 5*time.Second {
		t.Errorf("Expected recent LastActionTime, got %v", shadowState.Outputs.LastActionTime)
	}
}

// TestLoadSheddingShadowState_RecordDisableAction tests that disable actions update shadow state correctly
func TestLoadSheddingShadowState_RecordDisableAction(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)
	if err := stateManager.SetString("currentEnergyLevel", "green"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}

	// Create load shedding manager
	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)

	// First enable load shedding
	manager.recordAction(true, "enable", "Test enable", true, tempLowRestricted, tempHighRestricted, "test_trigger")

	// Then disable it
	reason := "Energy state is green (battery restored) - returning to normal HVAC"
	manager.recordAction(false, "disable", reason, false, 0, 0, "test_trigger")

	// Verify shadow state was updated
	shadowState := manager.GetShadowState()

	if shadowState.Outputs.Active {
		t.Error("Expected Active=false after disable action")
	}

	if shadowState.Outputs.LastActionType != "disable" {
		t.Errorf("Expected LastActionType='disable', got '%s'", shadowState.Outputs.LastActionType)
	}

	if shadowState.Outputs.LastActionReason != reason {
		t.Errorf("Expected LastActionReason='%s', got '%s'", reason, shadowState.Outputs.LastActionReason)
	}

	if shadowState.Outputs.ThermostatSettings.HoldMode {
		t.Error("Expected HoldMode=false after disable action")
	}

	if shadowState.Outputs.ThermostatSettings.TempLow != 0 {
		t.Errorf("Expected TempLow=0, got %.1f", shadowState.Outputs.ThermostatSettings.TempLow)
	}

	if shadowState.Outputs.ThermostatSettings.TempHigh != 0 {
		t.Errorf("Expected TempHigh=0, got %.1f", shadowState.Outputs.ThermostatSettings.TempHigh)
	}
}

// TestLoadSheddingShadowState_GetShadowState tests that GetShadowState returns accurate snapshot
func TestLoadSheddingShadowState_GetShadowState(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager and load shedding manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)
	if err := stateManager.SetString("currentEnergyLevel", "yellow"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}

	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)

	// Record some state
	manager.subHelper.CaptureInitialInputs()

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify it's a valid shadow state
	if shadowState == nil {
		t.Fatal("Expected non-nil shadow state")
	}

	if shadowState.Plugin != "loadshedding" {
		t.Errorf("Expected plugin='loadshedding', got '%s'", shadowState.Plugin)
	}

	if shadowState.Metadata.PluginName != "loadshedding" {
		t.Errorf("Expected PluginName='loadshedding', got '%s'", shadowState.Metadata.PluginName)
	}

	// Verify maps are initialized
	if shadowState.Inputs.Current == nil {
		t.Error("Expected Current inputs map to be initialized")
	}

	if shadowState.Inputs.AtLastAction == nil {
		t.Error("Expected AtLastAction inputs map to be initialized")
	}
}

// TestLoadSheddingShadowState_ConcurrentAccess tests thread safety with concurrent access
func TestLoadSheddingShadowState_ConcurrentAccess(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager and load shedding manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)
	if err := stateManager.SetString("currentEnergyLevel", "green"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}

	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)

	// Run concurrent operations
	done := make(chan bool)

	// Writer goroutine - updates shadow state
	go func() {
		for i := 0; i < 100; i++ {
			manager.recordAction(i%2 == 0, "test_action", "Concurrent test", true, 65.0, 80.0, "concurrent_test")
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	// Reader goroutine - reads shadow state
	go func() {
		for i := 0; i < 100; i++ {
			_ = manager.GetShadowState()
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	// Another writer goroutine - updates inputs
	go func() {
		for i := 0; i < 100; i++ {
			manager.subHelper.CaptureInitialInputs()
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	// Wait for all goroutines to complete
	<-done
	<-done
	<-done

	// If we get here without a race condition, test passes
	// (run with -race flag to detect races)
}

// TestLoadSheddingShadowState_InterfaceImplementation tests that LoadSheddingShadowState implements PluginShadowState
func TestLoadSheddingShadowState_InterfaceImplementation(t *testing.T) {
	t.Parallel()
	shadowState := shadowstate.NewLoadSheddingShadowState()

	// Verify interface methods work
	_ = shadowState.GetCurrentInputs()
	_ = shadowState.GetLastActionInputs()
	_ = shadowState.GetOutputs()
	_ = shadowState.GetMetadata()

	// If this compiles, the interface is implemented correctly
}

// TestLoadSheddingShadowState_InputSnapshot tests that inputs are snapshotted correctly
func TestLoadSheddingShadowState_InputSnapshot(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)
	if err := stateManager.SetString("currentEnergyLevel", "red"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}

	// Start manager to register subscriptions
	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Record action with red energy level
	manager.recordAction(true, "enable", "Low battery", true, tempLowRestricted, tempHighRestricted, "test_trigger")

	// Verify at-last-action inputs captured red
	shadowState := manager.GetShadowState()
	if val, ok := shadowState.Inputs.AtLastAction["currentEnergyLevel"]; !ok || val != "red" {
		t.Errorf("Expected at-last-action currentEnergyLevel='red', got '%v'", val)
	}

	// Now change energy level to green
	if err := stateManager.SetString("currentEnergyLevel", "green"); err != nil {
		t.Fatalf("Failed to change energy level: %v", err)
	}
	manager.subHelper.CaptureInitialInputs()

	// Verify current inputs changed but at-last-action stayed the same
	shadowState = manager.GetShadowState()
	if val, ok := shadowState.Inputs.Current["currentEnergyLevel"]; !ok || val != "green" {
		t.Errorf("Expected current currentEnergyLevel='green', got '%v'", val)
	}
	if val, ok := shadowState.Inputs.AtLastAction["currentEnergyLevel"]; !ok || val != "red" {
		t.Errorf("Expected at-last-action currentEnergyLevel='red' (unchanged), got '%v'", val)
	}
}

// TestLoadSheddingShadowState_MultipleActions tests that multiple actions are tracked correctly
func TestLoadSheddingShadowState_MultipleActions(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)

	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)

	// Record enable action
	if err := stateManager.SetString("currentEnergyLevel", "red"); err != nil {
		t.Fatalf("Failed to set energy level: %v", err)
	}
	manager.recordAction(true, "enable", "Battery low", true, tempLowRestricted, tempHighRestricted, "test_trigger")

	// Verify enabled
	shadowState := manager.GetShadowState()
	if !shadowState.Outputs.Active {
		t.Error("Expected Active=true after enable")
	}
	firstActionTime := shadowState.Outputs.LastActionTime

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Record disable action
	if err := stateManager.SetString("currentEnergyLevel", "green"); err != nil {
		t.Fatalf("Failed to change energy level: %v", err)
	}
	manager.recordAction(false, "disable", "Battery restored", false, 0, 0, "test_trigger")

	// Verify disabled
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.Active {
		t.Error("Expected Active=false after disable")
	}
	secondActionTime := shadowState.Outputs.LastActionTime

	// Verify action times are different
	if firstActionTime == secondActionTime {
		t.Error("Expected different action times for two different actions")
	}
}

// TestLoadSheddingShadowState_HandleEnergyChange tests shadow state updates on energy change
func TestLoadSheddingShadowState_HandleEnergyChange(t *testing.T) {
	t.Parallel(
	// Create mock HA client and state manager
	)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), true)

	// Initialize currentEnergyLevel
	if err := stateManager.SetString("currentEnergyLevel", "green"); err != nil {
		t.Fatalf("Failed to set initial energy level: %v", err)
	}

	manager := NewManager(context.Background(), mockClient, stateManager, zap.NewNop(), true, nil)

	// Start the manager to enable subscriptions
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate energy change to red
	if err := stateManager.SetString("currentEnergyLevel", "red"); err != nil {
		t.Fatalf("Failed to change energy level: %v", err)
	}

	// Give the handler time to process
	time.Sleep(50 * time.Millisecond)

	// Verify shadow state was updated
	shadowState := manager.GetShadowState()

	if val, ok := shadowState.Inputs.Current["currentEnergyLevel"]; !ok {
		t.Error("Expected currentEnergyLevel in current inputs")
	} else if val != "red" {
		t.Errorf("Expected current currentEnergyLevel='red', got '%v'", val)
	}

	// Note: In read-only mode, load shedding won't actually be activated,
	// so we just verify the inputs were updated
}
