package state

import (
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestManagerForLatch(t *testing.T) (*Manager, *ha.MockClient) {
	t.Helper()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Set up required entities
	mockClient.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.tori_here", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.any_owner_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	mockClient.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	return manager, mockClient
}

func TestWakeSequenceLatch_ActivatesOnRisingEdge(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)
	logger := testlogger.New()

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, logger, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	err := latch.Start()
	require.NoError(t, err)
	defer latch.Stop()

	// Initially inactive
	assert.False(t, latch.IsActive())

	// Rising edge should activate latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")

	// Give callback time to run
	time.Sleep(10 * time.Millisecond)

	assert.True(t, latch.IsActive(), "Latch should be active after rising edge")
	assert.Equal(t, int32(1), atomic.LoadInt32(&callbackCount), "Callback should have been called once")
}

func TestWakeSequenceLatch_DoesNotActivateIfAlreadyOn(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Wake sequence already on at startup
	mockClient.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, logger, false)
	err := manager.SyncFromHA()
	require.NoError(t, err)

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, logger, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	err = latch.Start()
	require.NoError(t, err)
	defer latch.Stop()

	// Should not be active at startup (no edge detected)
	assert.False(t, latch.IsActive())
	assert.Equal(t, int32(0), atomic.LoadInt32(&callbackCount))

	// Simulating a state change from on to on should not activate
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")

	time.Sleep(10 * time.Millisecond)

	assert.False(t, latch.IsActive(), "Latch should not activate without edge transition")
}

func TestWakeSequenceLatch_ClearsOnAnyoneAsleepFallingEdge(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)
	logger := testlogger.New()

	// Set up initial state: someone is asleep
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	manager.SyncFromHA()

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, logger, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	err := latch.Start()
	require.NoError(t, err)
	defer latch.Stop()

	// Activate latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	time.Sleep(10 * time.Millisecond)

	assert.True(t, latch.IsActive())
	initialCount := atomic.LoadInt32(&callbackCount)

	// Falling edge of isAnyoneAsleep should clear latch
	mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
	time.Sleep(10 * time.Millisecond)

	assert.False(t, latch.IsActive(), "Latch should clear on isAnyoneAsleep falling edge")
	assert.Greater(t, atomic.LoadInt32(&callbackCount), initialCount, "Callback should have been called")
}

func TestWakeSequenceLatch_DoesNotClearOnWakeSequenceOff(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)
	logger := testlogger.New()

	// Set up initial state: someone is asleep
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	manager.SyncFromHA()

	latch := NewWakeSequenceLatch(manager, logger, nil)

	err := latch.Start()
	require.NoError(t, err)
	defer latch.Stop()

	// Activate latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	time.Sleep(10 * time.Millisecond)

	assert.True(t, latch.IsActive())

	// Wake sequence turning off should NOT clear latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "off")
	time.Sleep(10 * time.Millisecond)

	assert.True(t, latch.IsActive(), "Latch should NOT clear when wake sequence turns off")
}

func TestWakeSequenceLatch_Reset(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)
	logger := testlogger.New()

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, logger, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	err := latch.Start()
	require.NoError(t, err)
	defer latch.Stop()

	// Activate latch
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	time.Sleep(10 * time.Millisecond)

	assert.True(t, latch.IsActive())
	initialCount := atomic.LoadInt32(&callbackCount)

	// Manual reset
	latch.Reset()

	assert.False(t, latch.IsActive(), "Latch should be cleared after Reset()")
	assert.Greater(t, atomic.LoadInt32(&callbackCount), initialCount, "Callback should have been called on reset")
}

func TestWakeSequenceLatch_ResetWhenNotActive(t *testing.T) {
	t.Parallel()
	manager, _ := setupTestManagerForLatch(t)
	logger := testlogger.New()

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, logger, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	err := latch.Start()
	require.NoError(t, err)
	defer latch.Stop()

	// Reset when not active should be a no-op
	latch.Reset()

	assert.False(t, latch.IsActive())
	assert.Equal(t, int32(0), atomic.LoadInt32(&callbackCount), "Callback should not be called when resetting inactive latch")
}

func TestWakeSequenceLatch_Stop(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)
	logger := testlogger.New()

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, logger, func() {
		atomic.AddInt32(&callbackCount, 1)
	})

	err := latch.Start()
	require.NoError(t, err)

	// Stop the latch
	latch.Stop()

	// State changes should no longer affect the latch
	initialCount := atomic.LoadInt32(&callbackCount)
	mockClient.SimulateStateChange("input_boolean.wake_sequence_active", "on")
	time.Sleep(10 * time.Millisecond)

	assert.Equal(t, initialCount, atomic.LoadInt32(&callbackCount), "Callback should not be called after Stop()")
}
