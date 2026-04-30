package evcharger

import (
	"context"
	"fmt"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// Helper to create a test manager
func createTestManager(mockHA *ha.MockClient, mockNtfy *ntfy.MockClient) *Manager {
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	return NewManager(context.Background(), mockHA, stateMgr, logger, false, registry, mockNtfy, &notify.MockNotifier{})
}

// Helper to create a test manager in read-only mode
func createTestManagerReadOnly(mockHA *ha.MockClient, mockNtfy *ntfy.MockClient) *Manager {
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	return NewManager(context.Background(), mockHA, stateMgr, logger, true, registry, mockNtfy, &notify.MockNotifier{})
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

func TestManager_EmergencyShutoffRetry(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Configure the switch turn_off to fail twice, then succeed on the third attempt
	mockHA.SetServiceFailCount("switch", "turn_off", 2, fmt.Errorf("websocket connection lost"))

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overheat detection - should retry and eventually succeed
	mockHA.SimulateStateChange(OverheatSensor, "on")

	// Verify switch was eventually turned off (after retries)
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected switch to be turned off after retries")
	}

	// Verify shutoff was recorded in shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.ShutoffCount != 1 {
		t.Errorf("Expected ShutoffCount=1 after successful retry, got %d", shadowState.Outputs.ShutoffCount)
	}
}

func TestManager_EmergencyShutoffAllRetriesFail(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Configure the switch turn_off to fail more times than the retry limit
	mockHA.SetServiceFailCount("switch", "turn_off", shutoffMaxRetries+1, fmt.Errorf("persistent failure"))

	manager := createTestManager(mockHA, mockNtfy)

	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate overheat detection - all retries should fail
	mockHA.SimulateStateChange(OverheatSensor, "on")

	// Verify no successful service call was recorded
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			t.Error("Expected no successful turn_off call when all retries fail")
		}
	}

	// Verify shutoff was NOT recorded (since it never succeeded)
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.ShutoffCount != 0 {
		t.Errorf("Expected ShutoffCount=0 when all retries fail, got %d", shadowState.Outputs.ShutoffCount)
	}

	// Safety event should still be recorded even though shutoff failed
	if shadowState.Outputs.SafetyEventCount != 1 {
		t.Errorf("Expected SafetyEventCount=1, got %d", shadowState.Outputs.SafetyEventCount)
	}
}

func TestManager_Reset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger a safety event then recovery to set and leave notification cooldown
	mockHA.SimulateStateChange(OverheatSensor, "on")
	assert.Equal(t, 1, manager.GetShadowState().Outputs.SafetyEventCount)
	mockHA.SimulateStateChange(OverheatSensor, "off") // Clear the condition

	// Verify lastNotificationTime is non-zero before reset
	manager.mu.Lock()
	assert.False(t, manager.lastNotificationTime.IsZero(),
		"Expected lastNotificationTime to be non-zero before reset")
	manager.mu.Unlock()

	// Reset should clear the rate limiter and succeed
	err := manager.Reset()
	assert.NoError(t, err)

	// After reset, checkInitialSafetyState runs but all sensors are "off",
	// so lastNotificationTime should have been cleared by Reset
	// (and not re-set since no safety condition is active).
	manager.mu.Lock()
	assert.True(t, manager.lastNotificationTime.IsZero(),
		"Expected lastNotificationTime to be zero after reset with no active conditions")
	manager.mu.Unlock()
}

func TestManager_ResetRechecksSafetyState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	snapshot := mockHA.ServiceCallCount()

	// Set overcurrent sensor to "on" (simulating condition already active)
	mockHA.SetState(OverCurrentSensor, "on", nil)

	// Reset re-checks safety state - should trigger emergency shutoff
	err := manager.Reset()
	assert.NoError(t, err)

	calls := mockHA.GetServiceCallsSince(snapshot)
	found := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			found = true
			break
		}
	}
	assert.True(t, found, "Expected switch turn_off after reset when safety condition is active")
}

func TestManager_HandlePowerChange(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Simulate power reading change
	mockHA.SimulateStateChange(PowerSensor, "1200.5")

	shadowState := manager.GetShadowState()
	assert.Equal(t, "1200.5", shadowState.Outputs.PowerReading)
}

func TestManager_HandlePowerChangeNilState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	// Call handler directly with nil newState - should not panic
	assert.NotPanics(t, func() {
		manager.handlePowerChange(PowerSensor, nil, nil)
	})
}

func TestManager_HandleSwitchChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		stateValue string
		expectOn   bool
	}{
		{name: "switch on", stateValue: "on", expectOn: true},
		{name: "switch off", stateValue: "off", expectOn: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockHA := ha.NewMockClient()
			mockNtfy := ntfy.NewMockClient()

			manager := createTestManager(mockHA, mockNtfy)
			require.NoError(t, manager.Start())
			defer manager.Stop()

			mockHA.SimulateStateChange(SwitchEntity, tc.stateValue)

			shadowState := manager.GetShadowState()
			assert.Equal(t, tc.expectOn, shadowState.Outputs.IsSwitchOn)
		})
	}
}

func TestManager_HandleSwitchChangeNilState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	// Call handler directly with nil newState - should not panic
	assert.NotPanics(t, func() {
		manager.handleSwitchChange(SwitchEntity, nil, nil)
	})
}

func TestManager_OverCurrentRecovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger overcurrent then recovery
	mockHA.SimulateStateChange(OverCurrentSensor, "on")
	mockHA.SimulateStateChange(OverCurrentSensor, "off")

	shadowState := manager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsOverCurrent)
	assert.NotNil(t, shadowState.Outputs.LastRecovery)
	assert.Equal(t, "over-current", shadowState.Outputs.LastRecovery.ConditionType)
}

func TestManager_OverVoltageRecovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger overvoltage then recovery
	mockHA.SimulateStateChange(OverVoltageSensor, "on")
	mockHA.SimulateStateChange(OverVoltageSensor, "off")

	shadowState := manager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsOverVoltage)
	assert.NotNil(t, shadowState.Outputs.LastRecovery)
	assert.Equal(t, "over-voltage", shadowState.Outputs.LastRecovery.ConditionType)
}

func TestManager_NotificationRateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger first safety event - notification should be sent
	mockHA.SimulateStateChange(OverheatSensor, "on")
	mockHA.SimulateStateChange(OverheatSensor, "off")

	firstCalls := len(mockNtfy.GetCalls())
	assert.Equal(t, 1, firstCalls, "Expected 1 notification for first event")

	// Trigger second safety event immediately - within cooldown window
	mockHA.SimulateStateChange(OverCurrentSensor, "on")

	secondCalls := len(mockNtfy.GetCalls())
	assert.Equal(t, 1, secondCalls, "Expected no additional notification within cooldown window")
}

func TestManager_NotificationWithoutNtfyClient(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()

	// Create manager with nil ntfy client
	manager := NewManager(context.Background(), mockHA, stateMgr, logger, false, registry, nil, &notify.MockNotifier{})

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger safety event - should not panic without ntfy client
	assert.NotPanics(t, func() {
		mockHA.SimulateStateChange(OverheatSensor, "on")
	})

	// Safety event should still be recorded
	shadowState := manager.GetShadowState()
	assert.Equal(t, 1, shadowState.Outputs.SafetyEventCount)
}

func TestManager_NtfyNotificationFailure(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	mockNtfy.SetError(fmt.Errorf("ntfy service unavailable"))

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger safety event - ntfy send will fail but shouldn't crash
	assert.NotPanics(t, func() {
		mockHA.SimulateStateChange(OverheatSensor, "on")
	})

	// Safety event and shutoff should still be recorded
	shadowState := manager.GetShadowState()
	assert.Equal(t, 1, shadowState.Outputs.SafetyEventCount)
	assert.Equal(t, 1, shadowState.Outputs.ShutoffCount)
	// Notification should NOT be recorded since send failed
	assert.Nil(t, shadowState.Outputs.LastNotification)
}

func TestManager_TTSAnnouncementFailure(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Make TTS calls fail
	mockHA.SetServiceError("tts", "speak", fmt.Errorf("tts service unavailable"))

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger safety event - TTS will fail but shouldn't crash
	assert.NotPanics(t, func() {
		mockHA.SimulateStateChange(OverheatSensor, "on")
	})

	// Safety event should still be recorded despite TTS failure
	shadowState := manager.GetShadowState()
	assert.Equal(t, 1, shadowState.Outputs.SafetyEventCount)
	assert.Equal(t, 1, shadowState.Outputs.ShutoffCount)
}

func TestManager_TTSReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManagerReadOnly(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger safety event in read-only mode
	mockHA.SimulateStateChange(OverheatSensor, "on")

	// Verify no TTS service calls were made
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "tts" {
			t.Error("Expected no TTS service calls in read-only mode")
		}
	}
}

func TestManager_HandlerNilStateChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler func(m *Manager)
	}{
		{
			name: "handleOverheat nil newState",
			handler: func(m *Manager) {
				m.handleOverheat(OverheatSensor, nil, nil)
			},
		},
		{
			name: "handleOverCurrent nil newState",
			handler: func(m *Manager) {
				m.handleOverCurrent(OverCurrentSensor, nil, nil)
			},
		},
		{
			name: "handleOverVoltage nil newState",
			handler: func(m *Manager) {
				m.handleOverVoltage(OverVoltageSensor, nil, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockHA := ha.NewMockClient()
			mockNtfy := ntfy.NewMockClient()
			manager := createTestManager(mockHA, mockNtfy)

			assert.NotPanics(t, func() {
				tc.handler(manager)
			})
		})
	}
}

func TestManager_InitialSafetyCheckErrorHandling(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Don't set any state for safety sensors - GetState will return nil
	// This exercises the error handling in checkInitialSafetyState

	manager := createTestManager(mockHA, mockNtfy)

	// Start should succeed despite missing sensor states
	assert.NotPanics(t, func() {
		err := manager.Start()
		assert.NoError(t, err)
	})
	defer manager.Stop()
}

func TestManager_MultipleSimultaneousSafetyConditions(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Pre-set all three sensors to "on" before starting
	mockHA.SetState(OverheatSensor, "on", nil)
	mockHA.SetState(OverCurrentSensor, "on", nil)
	mockHA.SetState(OverVoltageSensor, "on", nil)

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Verify multiple shutoff calls were made (idempotent)
	calls := mockHA.GetServiceCalls()
	shutoffCount := 0
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			shutoffCount++
		}
	}
	assert.GreaterOrEqual(t, shutoffCount, 1, "Expected at least one shutoff call")

	// Verify safety events were recorded
	shadowState := manager.GetShadowState()
	assert.GreaterOrEqual(t, shadowState.Outputs.SafetyEventCount, 3,
		"Expected at least 3 safety events for 3 conditions")
}

func TestManager_NotificationCooldownReset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	manager := createTestManager(mockHA, mockNtfy)

	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Trigger first event
	mockHA.SimulateStateChange(OverheatSensor, "on")
	mockHA.SimulateStateChange(OverheatSensor, "off")
	assert.Len(t, mockNtfy.GetCalls(), 1)

	// Set notification time to far in the past to simulate cooldown expiry
	manager.mu.Lock()
	manager.lastNotificationTime = time.Now().Add(-10 * time.Minute)
	manager.mu.Unlock()

	// Trigger another event - should send notification since cooldown expired
	mockHA.SimulateStateChange(OverCurrentSensor, "on")
	assert.Len(t, mockNtfy.GetCalls(), 2, "Expected notification after cooldown expired")
}
