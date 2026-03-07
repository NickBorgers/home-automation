package lighting

import (
	"context"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// immediateSleep is a sleep function that returns immediately (for tests).
func immediateSleep(_ context.Context, _ time.Duration) error {
	return nil
}

func TestBridgeMonitor_VerifySceneActivation_BrightnessChanged(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	room := &RoomConfig{
		HueGroup:      "Living Room",
		HASSAreaID:    "living_room_2",
		LightEntityID: "light.living_room",
	}

	// Set initial brightness (before scene activation)
	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	// Use a state sequence: first call returns 30 (before), second returns 200 (after)
	// Since SetState already set it, we need to update it to simulate the change
	// We change the state inside the sleep func to simulate the bridge responding.
	bm.sleepFunc = func(_ context.Context, _ time.Duration) error {
		// After "sleeping", update the mock state to simulate bridge response
		mock.SetState("light.living_room", "on", map[string]interface{}{
			"brightness": float64(200),
		})
		return nil
	}

	bm.VerifySceneActivation(context.Background(), room, "day")

	// Should NOT have recorded a failure
	state := bm.GetState()
	assert.False(t, state.BridgeStale, "Bridge should not be marked stale")
	assert.Equal(t, 0, state.ConsecutiveFailures, "No failures expected")
}

func TestBridgeMonitor_VerifySceneActivation_BrightnessUnchanged(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	room := &RoomConfig{
		HueGroup:      "Living Room",
		HASSAreaID:    "living_room_2",
		LightEntityID: "light.living_room",
	}

	// Brightness stays the same (simulating stale bridge)
	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	bm.VerifySceneActivation(context.Background(), room, "day")

	state := bm.GetState()
	assert.Equal(t, 1, state.ConsecutiveFailures, "Should have 1 failure")
	assert.False(t, state.BridgeStale, "Single room failure should not mark bridge stale")
}

func TestBridgeMonitor_MultipleRoomFailures_MarksBridgeStale(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	// Set up two rooms with unchanging brightness
	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})
	mock.SetState("light.kitchen", "on", map[string]interface{}{
		"brightness": float64(50),
	})

	room1 := &RoomConfig{
		HueGroup:      "Living Room",
		HASSAreaID:    "living_room_2",
		LightEntityID: "light.living_room",
	}
	room2 := &RoomConfig{
		HueGroup:      "Kitchen",
		HASSAreaID:    "kitchen",
		LightEntityID: "light.kitchen",
	}

	bm.VerifySceneActivation(context.Background(), room1, "day")
	bm.VerifySceneActivation(context.Background(), room2, "day")

	state := bm.GetState()
	assert.True(t, state.BridgeStale, "Bridge should be stale after 2 room failures")
	assert.Equal(t, 2, state.ConsecutiveFailures)
	assert.Len(t, state.RecentFailures, 2)
}

func TestBridgeMonitor_SendsNotification_WhenBridgeStale(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})
	mock.SetState("light.kitchen", "on", map[string]interface{}{
		"brightness": float64(50),
	})

	snapshot := mock.ServiceCallCount()

	room1 := &RoomConfig{
		HueGroup:      "Living Room",
		HASSAreaID:    "living_room_2",
		LightEntityID: "light.living_room",
	}
	room2 := &RoomConfig{
		HueGroup:      "Kitchen",
		HASSAreaID:    "kitchen",
		LightEntityID: "light.kitchen",
	}

	bm.VerifySceneActivation(context.Background(), room1, "day")
	bm.VerifySceneActivation(context.Background(), room2, "day")

	// Check that a notification was sent
	calls := mock.GetServiceCallsSince(snapshot)
	notifyCalls := 0
	for _, call := range calls {
		if call.Domain == "notify" {
			notifyCalls++
			assert.Contains(t, call.Data["message"], "Hue bridge")
			assert.Contains(t, call.Data["title"], "Hue Bridge Stale")
		}
	}
	assert.Equal(t, 1, notifyCalls, "Should have sent exactly 1 notification")
}

func TestBridgeMonitor_NotificationCooldown(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})
	mock.SetState("light.kitchen", "on", map[string]interface{}{
		"brightness": float64(50),
	})
	mock.SetState("light.sitting_room", "on", map[string]interface{}{
		"brightness": float64(40),
	})

	room1 := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}
	room2 := &RoomConfig{HueGroup: "Kitchen", LightEntityID: "light.kitchen"}
	room3 := &RoomConfig{HueGroup: "Sitting Room", LightEntityID: "light.sitting_room"}

	// First two failures trigger notification
	bm.VerifySceneActivation(context.Background(), room1, "day")
	bm.VerifySceneActivation(context.Background(), room2, "day")

	snapshot := mock.ServiceCallCount()

	// Third failure should NOT trigger another notification (cooldown)
	bm.VerifySceneActivation(context.Background(), room3, "day")

	calls := mock.GetServiceCallsSince(snapshot)
	notifyCalls := 0
	for _, call := range calls {
		if call.Domain == "notify" {
			notifyCalls++
		}
	}
	assert.Equal(t, 0, notifyCalls, "No notification expected during cooldown")
}

func TestBridgeMonitor_FailureWindowExpiry(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	// Use a controllable time function
	currentTime := time.Now()
	mu := sync.Mutex{}
	bm.nowFunc = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return currentTime
	}

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	room := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}

	// Record a failure
	bm.VerifySceneActivation(context.Background(), room, "day")
	assert.Equal(t, 1, bm.GetState().ConsecutiveFailures)

	// Advance time past the failure window
	mu.Lock()
	currentTime = currentTime.Add(BridgeFailureWindow + 1*time.Minute)
	mu.Unlock()

	// Old failure should be pruned
	state := bm.GetState()
	assert.Equal(t, 0, state.ConsecutiveFailures, "Old failures should be pruned")
	assert.False(t, state.BridgeStale)
}

func TestBridgeMonitor_SkipsVerification_NoLightEntityID(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	// Room without light_entity_id configured
	room := &RoomConfig{
		HueGroup:   "C Office",
		HASSAreaID: "c_office",
		// No LightEntityID
	}

	bm.VerifySceneActivation(context.Background(), room, "day")

	state := bm.GetState()
	assert.Equal(t, 0, state.ConsecutiveFailures, "No verification should occur without light entity")
}

func TestBridgeMonitor_SkipsVerification_EntityNotFound(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	// Don't set state for the entity - it should gracefully skip
	room := &RoomConfig{
		HueGroup:      "Living Room",
		LightEntityID: "light.nonexistent",
	}

	bm.VerifySceneActivation(context.Background(), room, "day")

	state := bm.GetState()
	assert.Equal(t, 0, state.ConsecutiveFailures, "Entity not found should not count as failure")
}

func TestBridgeMonitor_ContextCancellation(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)

	// Sleep function that respects cancellation
	bm.sleepFunc = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
	bm.verifyDelay = 5 * time.Second

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	room := &RoomConfig{
		HueGroup:      "Living Room",
		LightEntityID: "light.living_room",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	bm.VerifySceneActivation(ctx, room, "day")

	state := bm.GetState()
	assert.Equal(t, 0, state.ConsecutiveFailures, "Cancelled verification should not record failure")
}

func TestBridgeMonitor_ReadOnly_NoNotification(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, true, tracker) // readOnly = true
	bm.sleepFunc = immediateSleep

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})
	mock.SetState("light.kitchen", "on", map[string]interface{}{
		"brightness": float64(50),
	})

	snapshot := mock.ServiceCallCount()

	room1 := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}
	room2 := &RoomConfig{HueGroup: "Kitchen", LightEntityID: "light.kitchen"}

	bm.VerifySceneActivation(context.Background(), room1, "day")
	bm.VerifySceneActivation(context.Background(), room2, "day")

	calls := mock.GetServiceCallsSince(snapshot)
	notifyCalls := 0
	for _, call := range calls {
		if call.Domain == "notify" {
			notifyCalls++
		}
	}
	assert.Equal(t, 0, notifyCalls, "No notifications in read-only mode")
}

func TestBridgeMonitor_ClearFailures(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	room := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}
	bm.VerifySceneActivation(context.Background(), room, "day")

	assert.Equal(t, 1, bm.GetState().ConsecutiveFailures)

	bm.ClearFailures()

	assert.Equal(t, 0, bm.GetState().ConsecutiveFailures, "Failures should be cleared")
}

func TestBridgeMonitor_LightOff_ReadsBrightnessAsZero(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	// Light is off both before and after
	mock.SetState("light.living_room", "off", map[string]interface{}{})

	room := &RoomConfig{
		HueGroup:      "Living Room",
		LightEntityID: "light.living_room",
	}

	bm.VerifySceneActivation(context.Background(), room, "day")

	// Brightness 0 → 0 = no change = failure
	state := bm.GetState()
	assert.Equal(t, 1, state.ConsecutiveFailures,
		"Light staying off after scene activation should count as failure")
}

func TestBridgeMonitor_ShadowStateUpdated(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})
	mock.SetState("light.kitchen", "on", map[string]interface{}{
		"brightness": float64(50),
	})

	room1 := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}
	room2 := &RoomConfig{HueGroup: "Kitchen", LightEntityID: "light.kitchen"}

	bm.VerifySceneActivation(context.Background(), room1, "day")
	bm.VerifySceneActivation(context.Background(), room2, "day")

	// Check shadow state was updated
	shadowState := tracker.GetState()
	require.NotNil(t, shadowState.Outputs.BridgeMonitor, "Bridge monitor should be in shadow state")
	assert.True(t, shadowState.Outputs.BridgeMonitor.BridgeStale)
	assert.Equal(t, 2, shadowState.Outputs.BridgeMonitor.ConsecutiveFailures)
	assert.Len(t, shadowState.Outputs.BridgeMonitor.RecentFailures, 2)
}

func TestBridgeMonitor_BrightnessWithinTolerance(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)

	// The brightness changes but by less than the tolerance
	bm.sleepFunc = func(_ context.Context, _ time.Duration) error {
		mock.SetState("light.living_room", "on", map[string]interface{}{
			"brightness": float64(35), // delta of 5, less than tolerance of 10
		})
		return nil
	}

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	room := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}
	bm.VerifySceneActivation(context.Background(), room, "day")

	state := bm.GetState()
	assert.Equal(t, 1, state.ConsecutiveFailures,
		"Small brightness change within tolerance should count as failure")
}

func TestBridgeMonitor_BrightnessOutsideTolerance(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)

	// The brightness changes by more than the tolerance
	bm.sleepFunc = func(_ context.Context, _ time.Duration) error {
		mock.SetState("light.living_room", "on", map[string]interface{}{
			"brightness": float64(200), // delta of 170, more than tolerance of 10
		})
		return nil
	}

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	room := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}
	bm.VerifySceneActivation(context.Background(), room, "day")

	state := bm.GetState()
	assert.Equal(t, 0, state.ConsecutiveFailures,
		"Large brightness change should not be a failure")
}

func TestBridgeMonitor_SameRoomMultipleFailures_NotStale(t *testing.T) {
	t.Parallel()
	mock := ha.NewMockClient()
	mock.Connect()
	tracker := shadowstate.NewLightingTracker()
	env := testutil.NewEnv(t)
	bm := NewBridgeMonitor(mock, env.Logger, false, tracker)
	bm.sleepFunc = immediateSleep

	mock.SetState("light.living_room", "on", map[string]interface{}{
		"brightness": float64(30),
	})

	room := &RoomConfig{HueGroup: "Living Room", LightEntityID: "light.living_room"}

	// Multiple failures for the same room
	bm.VerifySceneActivation(context.Background(), room, "day")
	bm.VerifySceneActivation(context.Background(), room, "evening")
	bm.VerifySceneActivation(context.Background(), room, "night")

	state := bm.GetState()
	assert.False(t, state.BridgeStale,
		"Multiple failures in same room should not mark bridge stale (could be bulb issue)")
	assert.Equal(t, 3, state.ConsecutiveFailures)
}

func TestAbs(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 5, abs(5))
	assert.Equal(t, 5, abs(-5))
	assert.Equal(t, 0, abs(0))
}
