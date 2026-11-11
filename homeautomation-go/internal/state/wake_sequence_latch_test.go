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
	mockClient.SetState("input_boolean.assistant_here", "off", map[string]interface{}{})
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

// Early wake: someone got up while a bedroom sleeps. The latch holds the house awake.
func TestWakeSequenceLatch_ActivatesOnEarlyWakeRisingEdge(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, testlogger.New(), func() {
		atomic.AddInt32(&callbackCount, 1)
	})
	require.NoError(t, latch.Start())
	defer latch.Stop()
	assert.False(t, latch.IsActive())

	mockClient.SimulateStateChange("input_boolean.early_wake_active", "on")

	assert.Eventually(t, latch.IsActive, time.Second, 5*time.Millisecond,
		"early wake rising edge must activate the latch")
	assert.Equal(t, int32(1), atomic.LoadInt32(&callbackCount))
	wakeSeq, err := manager.GetBool("isWakeSequenceActive")
	require.NoError(t, err)
	assert.False(t, wakeSeq, "early wake must never set isWakeSequenceActive")
}

func TestWakeSequenceLatch_ActivatesIfEarlyWakeAlreadyOn(t *testing.T) {
	t.Parallel()
	manager, mockClient := setupTestManagerForLatch(t)
	mockClient.SetState("input_boolean.early_wake_active", "on", map[string]interface{}{})
	mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	require.NoError(t, manager.SyncFromHA())

	var callbackCount int32
	latch := NewWakeSequenceLatch(manager, testlogger.New(), func() {
		atomic.AddInt32(&callbackCount, 1)
	})
	require.NoError(t, latch.Start())
	defer latch.Stop()

	assert.True(t, latch.IsActive(), "an early wake already in progress must hold the latch at start")
	assert.Equal(t, int32(1), atomic.LoadInt32(&callbackCount))
}

// Early wake ends at a normal wake or at bedtime. Its end releases the latch,
// unless the alarm wake sequence is running and owns the latch.
func TestWakeSequenceLatch_EarlyWakeFallingEdge(t *testing.T) {
	tests := []struct {
		name         string
		wakeSequence string
		wantActive   bool
		why          string
	}{
		{"clears the latch", "off", false, "isEarlyWakeActive true->false must clear the latch"},
		{"kept while wake sequence runs", "on", true, "the wake sequence owns the latch while it runs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			manager, mockClient := setupTestManagerForLatch(t)
			mockClient.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
			mockClient.SetState("input_boolean.wake_sequence_active", tc.wakeSequence, map[string]interface{}{})
			require.NoError(t, manager.SyncFromHA())

			var callbackCount int32
			latch := NewWakeSequenceLatch(manager, testlogger.New(), func() {
				atomic.AddInt32(&callbackCount, 1)
			})
			require.NoError(t, latch.Start())
			defer latch.Stop()

			mockClient.SimulateStateChange("input_boolean.early_wake_active", "on")
			assert.Eventually(t, latch.IsActive, time.Second, 5*time.Millisecond)
			countAfterActivate := atomic.LoadInt32(&callbackCount)

			mockClient.SimulateStateChange("input_boolean.early_wake_active", "off")

			if tc.wantActive {
				time.Sleep(10 * time.Millisecond)
				assert.True(t, latch.IsActive(), tc.why)
				assert.Equal(t, countAfterActivate, atomic.LoadInt32(&callbackCount))

				mockClient.SimulateStateChange("input_boolean.anyone_asleep", "off")
				assert.Eventually(t, func() bool { return !latch.IsActive() }, time.Second, 5*time.Millisecond,
					"isAnyoneAsleep true->false must still clear the latch")
				return
			}
			assert.Eventually(t, func() bool { return !latch.IsActive() }, time.Second, 5*time.Millisecond, tc.why)
			assert.Greater(t, atomic.LoadInt32(&callbackCount), countAfterActivate,
				"clearing the latch must trigger a recompute")
		})
	}
}
