package evcharger

import (
	"context"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Helper to create a test manager
func createTestManager(mockHA *ha.MockClient, mockNtfy *ntfy.MockClient) *Manager {
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	return NewManager(context.Background(), mockHA, stateMgr, logger, false, registry, mockNtfy)
}

// Helper to create a test manager in read-only mode
func createTestManagerReadOnly(mockHA *ha.MockClient, mockNtfy *ntfy.MockClient) *Manager {
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	return NewManager(context.Background(), mockHA, stateMgr, logger, true, registry, mockNtfy)
}

func TestManager_OverheatDetection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overheat detection
	mockHA.SimulateStateChange(OverheatSensor, "on")

	// Verify switch was turned off
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == SwitchEntity {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("Expected switch to be turned off on overheat, but no turn_off call found")
	}

	// Verify notification was sent
	ntfyCalls := mockNtfy.GetCalls()
	if len(ntfyCalls) == 0 {
		t.Error("Expected ntfy notification to be sent on overheat")
	} else if ntfyCalls[0].Priority != ntfy.PriorityUrgent {
		t.Errorf("Expected urgent priority, got %d", ntfyCalls[0].Priority)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if !shadowState.Outputs.IsOverheat {
		t.Error("Expected IsOverheat to be true")
	}
	if shadowState.Outputs.SafetyEventCount != 1 {
		t.Errorf("Expected SafetyEventCount=1, got %d", shadowState.Outputs.SafetyEventCount)
	}
	if shadowState.Outputs.ShutoffCount != 1 {
		t.Errorf("Expected ShutoffCount=1, got %d", shadowState.Outputs.ShutoffCount)
	}
}

func TestManager_OverCurrentDetection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overcurrent detection
	mockHA.SimulateStateChange(OverCurrentSensor, "on")

	// Verify switch was turned off
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected switch to be turned off on over-current")
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if !shadowState.Outputs.IsOverCurrent {
		t.Error("Expected IsOverCurrent to be true")
	}
}

func TestManager_OverVoltageDetection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overvoltage detection
	mockHA.SimulateStateChange(OverVoltageSensor, "on")

	// Verify switch was turned off
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected switch to be turned off on over-voltage")
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if !shadowState.Outputs.IsOverVoltage {
		t.Error("Expected IsOverVoltage to be true")
	}
}

func TestManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Create manager in read-only mode
	manager := createTestManagerReadOnly(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overheat detection
	mockHA.SimulateStateChange(OverheatSensor, "on")

	// Verify switch was NOT turned off (read-only mode)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			t.Error("Expected no switch service calls in read-only mode")
		}
	}
}

func TestManager_Recovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overheat then recovery
	mockHA.SimulateStateChange(OverheatSensor, "on")
	mockHA.SimulateStateChange(OverheatSensor, "off")

	// Verify shadow state shows recovery
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.IsOverheat {
		t.Error("Expected IsOverheat to be false after recovery")
	}
	if shadowState.Outputs.LastRecovery == nil {
		t.Error("Expected LastRecovery to be set")
	} else if shadowState.Outputs.LastRecovery.ConditionType != "overheat" {
		t.Errorf("Expected recovery for 'overheat', got '%s'", shadowState.Outputs.LastRecovery.ConditionType)
	}
}

func TestManager_InitialStateCheck(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Pre-set the overheat sensor to "on" before starting
	mockHA.SetState(OverheatSensor, "on", nil)

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify switch was turned off because overheat was already active
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected switch to be turned off when overheat is already active on startup")
	}
}
