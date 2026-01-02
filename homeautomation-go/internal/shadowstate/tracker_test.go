package shadowstate

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestNewTracker(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()
	if tracker == nil {
		t.Fatal("NewTracker returned nil")
	}
	if tracker.pluginStates == nil {
		t.Error("pluginStates map not initialized")
	}
	if tracker.stateProviders == nil {
		t.Error("stateProviders map not initialized")
	}
}

func TestTrackerRegisterPlugin(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()
	state := NewLightingShadowState()

	tracker.RegisterPlugin("lighting", state)

	retrieved, ok := tracker.GetPluginState("lighting")
	if !ok {
		t.Fatal("Failed to retrieve registered plugin state")
	}
	if retrieved == nil {
		t.Error("Retrieved state is nil")
	}
}

func TestTrackerRegisterPluginProvider(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()
	callCount := 0

	provider := func() PluginShadowState {
		callCount++
		return NewLightingShadowState()
	}

	tracker.RegisterPluginProvider("lighting", provider)

	// First call
	state1, ok := tracker.GetPluginState("lighting")
	if !ok {
		t.Fatal("Failed to retrieve state from provider")
	}
	if state1 == nil {
		t.Error("Retrieved state is nil")
	}
	if callCount != 1 {
		t.Errorf("Expected provider to be called once, was called %d times", callCount)
	}

	// Second call should call provider again
	state2, ok := tracker.GetPluginState("lighting")
	if !ok {
		t.Fatal("Failed to retrieve state from provider on second call")
	}
	if state2 == nil {
		t.Error("Retrieved state is nil on second call")
	}
	if callCount != 2 {
		t.Errorf("Expected provider to be called twice, was called %d times", callCount)
	}
}

func TestTrackerProviderTakesPrecedence(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()

	// Register static state
	staticState := NewLightingShadowState()
	staticState.Inputs.Current["test"] = "static"
	tracker.RegisterPlugin("lighting", staticState)

	// Register provider for same plugin
	tracker.RegisterPluginProvider("lighting", func() PluginShadowState {
		providerState := NewLightingShadowState()
		providerState.Inputs.Current["test"] = "provider"
		return providerState
	})

	// Provider should take precedence
	state, ok := tracker.GetPluginState("lighting")
	if !ok {
		t.Fatal("Failed to retrieve state")
	}

	inputs := state.GetCurrentInputs()
	if inputs["test"] != "provider" {
		t.Errorf("Expected provider state, got %v", inputs["test"])
	}
}

func TestTrackerGetPluginStateNotFound(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()

	_, ok := tracker.GetPluginState("nonexistent")
	if ok {
		t.Error("Expected GetPluginState to return false for nonexistent plugin")
	}
}

func TestTrackerGetAllPluginStates(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()

	// Register multiple plugins
	tracker.RegisterPlugin("plugin1", NewLightingShadowState())
	tracker.RegisterPlugin("plugin2", NewLightingShadowState())
	tracker.RegisterPluginProvider("plugin3", func() PluginShadowState {
		return NewLightingShadowState()
	})

	states := tracker.GetAllPluginStates()

	if len(states) != 3 {
		t.Errorf("Expected 3 plugin states, got %d", len(states))
	}

	for _, name := range []string{"plugin1", "plugin2", "plugin3"} {
		if _, ok := states[name]; !ok {
			t.Errorf("Expected to find %s in all states", name)
		}
	}
}

func TestTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	tracker := NewTracker()

	// Register a provider
	tracker.RegisterPluginProvider("lighting", func() PluginShadowState {
		return NewLightingShadowState()
	})

	// Concurrent reads
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, ok := tracker.GetPluginState("lighting")
				if !ok {
					t.Error("Failed to get state during concurrent access")
				}
			}
		}()
	}

	wg.Wait()
}

func TestNewLightingTracker(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()
	if lt == nil {
		t.Fatal("NewLightingTracker returned nil")
	}
	if lt.state == nil {
		t.Error("state not initialized")
	}
}

func TestLightingTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	inputs := map[string]interface{}{
		"dayPhase":     "evening",
		"sunevent":     "sunset",
		"isAnyoneHome": true,
	}

	lt.UpdateCurrentInputs(inputs)

	state := lt.GetState()
	if state.Inputs.Current["dayPhase"] != "evening" {
		t.Errorf("Expected dayPhase to be 'evening', got %v", state.Inputs.Current["dayPhase"])
	}
	if state.Inputs.Current["sunevent"] != "sunset" {
		t.Errorf("Expected sunevent to be 'sunset', got %v", state.Inputs.Current["sunevent"])
	}
	if state.Inputs.Current["isAnyoneHome"] != true {
		t.Errorf("Expected isAnyoneHome to be true, got %v", state.Inputs.Current["isAnyoneHome"])
	}
}

func TestLightingTrackerSnapshotInputsForAction(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	// Set initial inputs
	inputs := map[string]interface{}{
		"dayPhase": "afternoon",
	}
	lt.UpdateCurrentInputs(inputs)

	// Snapshot
	lt.SnapshotInputsForAction()

	// Change current inputs
	newInputs := map[string]interface{}{
		"dayPhase": "evening",
	}
	lt.UpdateCurrentInputs(newInputs)

	state := lt.GetState()

	// Current should be evening
	if state.Inputs.Current["dayPhase"] != "evening" {
		t.Errorf("Expected current dayPhase to be 'evening', got %v", state.Inputs.Current["dayPhase"])
	}

	// At last action should be afternoon
	if state.Inputs.AtLastAction["dayPhase"] != "afternoon" {
		t.Errorf("Expected atLastAction dayPhase to be 'afternoon', got %v", state.Inputs.AtLastAction["dayPhase"])
	}
}

func TestLightingTrackerRecordRoomAction(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	lt.RecordRoomAction("Living Room", "activate_scene", "dayPhase changed", "evening", false)

	state := lt.GetState()

	room, ok := state.Outputs.Rooms["Living Room"]
	if !ok {
		t.Fatal("Room 'Living Room' not found in outputs")
	}

	if room.ActionType != "activate_scene" {
		t.Errorf("Expected action type 'activate_scene', got %s", room.ActionType)
	}
	if room.Reason != "dayPhase changed" {
		t.Errorf("Expected reason 'dayPhase changed', got %s", room.Reason)
	}
	if room.ActiveScene != "evening" {
		t.Errorf("Expected active scene 'evening', got %s", room.ActiveScene)
	}
	if room.TurnedOff {
		t.Error("Expected TurnedOff to be false")
	}
}

func TestLightingTrackerRecordTurnOff(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	lt.RecordRoomAction("Kitchen", "turn_off", "No one home", "", true)

	state := lt.GetState()

	room, ok := state.Outputs.Rooms["Kitchen"]
	if !ok {
		t.Fatal("Room 'Kitchen' not found in outputs")
	}

	if room.ActionType != "turn_off" {
		t.Errorf("Expected action type 'turn_off', got %s", room.ActionType)
	}
	if !room.TurnedOff {
		t.Error("Expected TurnedOff to be true")
	}
	if room.ActiveScene != "" {
		t.Errorf("Expected active scene to be empty, got %s", room.ActiveScene)
	}
}

func TestLightingTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	// Set initial state
	inputs := map[string]interface{}{
		"dayPhase": "morning",
	}
	lt.UpdateCurrentInputs(inputs)

	// Get state
	state1 := lt.GetState()

	// Modify the returned state
	state1.Inputs.Current["dayPhase"] = "modified"

	// Get state again
	state2 := lt.GetState()

	// Original should be unchanged
	if state2.Inputs.Current["dayPhase"] != "morning" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestLightingTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				inputs := map[string]interface{}{
					"dayPhase": "test",
					"count":    i*20 + j,
				}
				lt.UpdateCurrentInputs(inputs)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = lt.GetState()
			}
		}()
	}

	// Concurrent snapshots
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				lt.SnapshotInputsForAction()
			}
		}()
	}

	// Concurrent room actions
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				roomName := fmt.Sprintf("Room%d", i)
				lt.RecordRoomAction(roomName, "activate_scene", "test", "evening", false)
			}
		}(i)
	}

	wg.Wait()
}

func TestLightingTrackerMetadataUpdates(t *testing.T) {
	t.Parallel()
	lt := NewLightingTracker()

	initialMetadata := lt.GetState().Metadata

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Update inputs
	lt.UpdateCurrentInputs(map[string]interface{}{"test": "value"})

	updatedMetadata := lt.GetState().Metadata

	if !updatedMetadata.LastUpdated.After(initialMetadata.LastUpdated) {
		t.Error("Expected LastUpdated to be updated after UpdateCurrentInputs")
	}

	// Wait a bit more
	time.Sleep(10 * time.Millisecond)

	// Record action
	lt.RecordRoomAction("Test Room", "test", "test", "test", false)

	actionMetadata := lt.GetState().Metadata

	if !actionMetadata.LastUpdated.After(updatedMetadata.LastUpdated) {
		t.Error("Expected LastUpdated to be updated after RecordRoomAction")
	}
}

func TestLightingShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*LightingShadowState)(nil)
}

// Security Tracker Tests

func TestNewSecurityTracker(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()
	if st == nil {
		t.Fatal("NewSecurityTracker returned nil")
	}
	if st.state == nil {
		t.Error("state not initialized")
	}
}

func TestSecurityTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	inputs := map[string]interface{}{
		"isEveryoneAsleep":       true,
		"isAnyoneHome":           false,
		"isExpectingSomeone":     false,
		"didOwnerJustReturnHome": false,
	}

	st.UpdateCurrentInputs(inputs)

	state := st.GetState()
	if state.Inputs.Current["isEveryoneAsleep"] != true {
		t.Errorf("Expected isEveryoneAsleep to be true, got %v", state.Inputs.Current["isEveryoneAsleep"])
	}
	if state.Inputs.Current["isAnyoneHome"] != false {
		t.Errorf("Expected isAnyoneHome to be false, got %v", state.Inputs.Current["isAnyoneHome"])
	}
}

func TestSecurityTrackerSnapshotInputsForAction(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	// Set initial inputs
	inputs := map[string]interface{}{
		"isEveryoneAsleep": false,
	}
	st.UpdateCurrentInputs(inputs)

	// Snapshot
	st.SnapshotInputsForAction()

	// Change current inputs
	newInputs := map[string]interface{}{
		"isEveryoneAsleep": true,
	}
	st.UpdateCurrentInputs(newInputs)

	state := st.GetState()

	// Current should be true
	if state.Inputs.Current["isEveryoneAsleep"] != true {
		t.Errorf("Expected current isEveryoneAsleep to be true, got %v", state.Inputs.Current["isEveryoneAsleep"])
	}

	// At last action should be false
	if state.Inputs.AtLastAction["isEveryoneAsleep"] != false {
		t.Errorf("Expected atLastAction isEveryoneAsleep to be false, got %v", state.Inputs.AtLastAction["isEveryoneAsleep"])
	}
}

func TestSecurityTrackerRecordLockdownAction(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	st.RecordLockdownAction(true, "Everyone is asleep")

	state := st.GetState()

	if !state.Outputs.Lockdown.Active {
		t.Error("Expected lockdown to be active")
	}
	if state.Outputs.Lockdown.Reason != "Everyone is asleep" {
		t.Errorf("Expected reason 'Everyone is asleep', got %s", state.Outputs.Lockdown.Reason)
	}
	if state.Outputs.Lockdown.ActivatedAt.IsZero() {
		t.Error("Expected ActivatedAt to be set")
	}
	if state.Outputs.Lockdown.WillResetAt.IsZero() {
		t.Error("Expected WillResetAt to be set")
	}

	// Test deactivation
	st.RecordLockdownAction(false, "Auto-reset")

	state = st.GetState()
	if state.Outputs.Lockdown.Active {
		t.Error("Expected lockdown to be inactive after reset")
	}
	if state.Outputs.Lockdown.ActivatedAt != (time.Time{}) {
		t.Error("Expected ActivatedAt to be cleared")
	}
}

func TestSecurityTrackerRecordDoorbellEvent(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	st.RecordDoorbellEvent(false, true, true)

	state := st.GetState()

	if state.Outputs.LastDoorbell == nil {
		t.Fatal("Expected LastDoorbell to be set")
	}
	if state.Outputs.LastDoorbell.RateLimited {
		t.Error("Expected RateLimited to be false")
	}
	if !state.Outputs.LastDoorbell.TTSSent {
		t.Error("Expected TTSSent to be true")
	}
	if !state.Outputs.LastDoorbell.LightsFlashed {
		t.Error("Expected LightsFlashed to be true")
	}
	if state.Outputs.LastDoorbell.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}

	// Test rate-limited event
	st.RecordDoorbellEvent(true, false, false)

	state = st.GetState()
	if !state.Outputs.LastDoorbell.RateLimited {
		t.Error("Expected RateLimited to be true")
	}
	if state.Outputs.LastDoorbell.TTSSent {
		t.Error("Expected TTSSent to be false for rate-limited event")
	}
}

func TestSecurityTrackerRecordVehicleArrivalEvent(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	st.RecordVehicleArrivalEvent(false, true, true)

	state := st.GetState()

	if state.Outputs.LastVehicle == nil {
		t.Fatal("Expected LastVehicle to be set")
	}
	if state.Outputs.LastVehicle.RateLimited {
		t.Error("Expected RateLimited to be false")
	}
	if !state.Outputs.LastVehicle.TTSSent {
		t.Error("Expected TTSSent to be true")
	}
	if !state.Outputs.LastVehicle.WasExpecting {
		t.Error("Expected WasExpecting to be true")
	}
	if state.Outputs.LastVehicle.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

func TestSecurityTrackerRecordGarageOpenEvent(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	st.RecordGarageOpenEvent("Owner returned home", true)

	state := st.GetState()

	if state.Outputs.LastGarageOpen == nil {
		t.Fatal("Expected LastGarageOpen to be set")
	}
	if state.Outputs.LastGarageOpen.Reason != "Owner returned home" {
		t.Errorf("Expected reason 'Owner returned home', got %s", state.Outputs.LastGarageOpen.Reason)
	}
	if !state.Outputs.LastGarageOpen.GarageWasEmpty {
		t.Error("Expected GarageWasEmpty to be true")
	}
	if state.Outputs.LastGarageOpen.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

func TestSecurityTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	// Set initial state
	inputs := map[string]interface{}{
		"isEveryoneAsleep": false,
	}
	st.UpdateCurrentInputs(inputs)

	// Get state
	state1 := st.GetState()

	// Modify the returned state
	state1.Inputs.Current["isEveryoneAsleep"] = true

	// Get state again
	state2 := st.GetState()

	// Original should be unchanged
	if state2.Inputs.Current["isEveryoneAsleep"] != false {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestSecurityTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				inputs := map[string]interface{}{
					"isEveryoneAsleep": i%2 == 0,
					"count":            i*20 + j,
				}
				st.UpdateCurrentInputs(inputs)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = st.GetState()
			}
		}()
	}

	// Concurrent snapshots
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				st.SnapshotInputsForAction()
			}
		}()
	}

	// Concurrent lockdown actions
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				st.RecordLockdownAction(i%2 == 0, "test")
			}
		}(i)
	}

	// Concurrent doorbell events
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				st.RecordDoorbellEvent(false, true, true)
			}
		}()
	}

	wg.Wait()
}

func TestSecurityTrackerMetadataUpdates(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	initialMetadata := st.GetState().Metadata

	// Wait a bit
	time.Sleep(10 * time.Millisecond)

	// Update inputs
	st.UpdateCurrentInputs(map[string]interface{}{"test": "value"})

	updatedMetadata := st.GetState().Metadata

	if !updatedMetadata.LastUpdated.After(initialMetadata.LastUpdated) {
		t.Error("Expected LastUpdated to be updated after UpdateCurrentInputs")
	}

	// Wait a bit more
	time.Sleep(10 * time.Millisecond)

	// Record action
	st.RecordLockdownAction(true, "test")

	actionMetadata := st.GetState().Metadata

	if !actionMetadata.LastUpdated.After(updatedMetadata.LastUpdated) {
		t.Error("Expected LastUpdated to be updated after RecordLockdownAction")
	}
}

func TestSecurityShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*SecurityShadowState)(nil)
}

func TestSecurityTrackerLastActionTime(t *testing.T) {
	t.Parallel()
	st := NewSecurityTracker()

	// Initially should be zero
	state := st.GetState()
	if !state.Outputs.LastActionTime.IsZero() {
		t.Error("Expected LastActionTime to be zero initially")
	}

	// Record an action
	st.RecordLockdownAction(true, "test")

	state = st.GetState()
	if state.Outputs.LastActionTime.IsZero() {
		t.Error("Expected LastActionTime to be set after action")
	}

	firstTime := state.Outputs.LastActionTime

	// Wait and record another action
	time.Sleep(10 * time.Millisecond)
	st.RecordDoorbellEvent(false, true, true)

	state = st.GetState()
	if !state.Outputs.LastActionTime.After(firstTime) {
		t.Error("Expected LastActionTime to be updated after second action")
	}
}

// SleepHygieneTracker tests

func TestNewSleepHygieneTracker(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()
	if st == nil {
		t.Fatal("NewSleepHygieneTracker returned nil")
	}
	if st.state == nil {
		t.Error("state not initialized")
	}
}

func TestSleepHygieneTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	inputs := map[string]interface{}{
		"isMasterAsleep":    true,
		"musicPlaybackType": "sleep",
		"alarmTime":         float64(1234567890000),
	}

	st.UpdateCurrentInputs(inputs)

	state := st.GetState()
	if state.Inputs.Current["isMasterAsleep"] != true {
		t.Errorf("Expected isMasterAsleep to be true, got %v", state.Inputs.Current["isMasterAsleep"])
	}
	if state.Inputs.Current["musicPlaybackType"] != "sleep" {
		t.Errorf("Expected musicPlaybackType to be 'sleep', got %v", state.Inputs.Current["musicPlaybackType"])
	}
}

func TestSleepHygieneTrackerSnapshotInputsForAction(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	// Set initial inputs
	inputs := map[string]interface{}{
		"isMasterAsleep": true,
	}
	st.UpdateCurrentInputs(inputs)

	// Snapshot
	st.SnapshotInputsForAction()

	// Change current inputs
	newInputs := map[string]interface{}{
		"isMasterAsleep": false,
	}
	st.UpdateCurrentInputs(newInputs)

	state := st.GetState()

	// Current should be false
	if state.Inputs.Current["isMasterAsleep"] != false {
		t.Errorf("Expected current isMasterAsleep to be false, got %v", state.Inputs.Current["isMasterAsleep"])
	}

	// At last action should be true
	if state.Inputs.AtLastAction["isMasterAsleep"] != true {
		t.Errorf("Expected atLastAction isMasterAsleep to be true, got %v", state.Inputs.AtLastAction["isMasterAsleep"])
	}
}

func TestSleepHygieneTrackerRecordAction(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	st.RecordAction("begin_wake", "Starting wake sequence")

	state := st.GetState()

	if state.Outputs.LastActionType != "begin_wake" {
		t.Errorf("Expected action type 'begin_wake', got %s", state.Outputs.LastActionType)
	}
	if state.Outputs.LastActionReason != "Starting wake sequence" {
		t.Errorf("Expected reason 'Starting wake sequence', got %s", state.Outputs.LastActionReason)
	}
	if state.Outputs.LastActionTime.IsZero() {
		t.Error("Expected LastActionTime to be set")
	}
}

func TestSleepHygieneTrackerUpdateWakeSequenceStatus(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	// Initial status should be inactive
	state := st.GetState()
	if state.Outputs.WakeSequenceStatus != "inactive" {
		t.Errorf("Expected initial status to be 'inactive', got %s", state.Outputs.WakeSequenceStatus)
	}

	// Update to begin_wake
	st.UpdateWakeSequenceStatus("begin_wake")
	state = st.GetState()
	if state.Outputs.WakeSequenceStatus != "begin_wake" {
		t.Errorf("Expected status to be 'begin_wake', got %s", state.Outputs.WakeSequenceStatus)
	}
}

func TestSleepHygieneTrackerFadeOutProgress(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	// Record fade out start
	st.RecordFadeOutStart("media_player.bedroom", 60)

	state := st.GetState()
	fadeOut, exists := state.Outputs.FadeOutProgress["media_player.bedroom"]
	if !exists {
		t.Fatal("Expected fade out progress for media_player.bedroom")
	}
	if fadeOut.StartVolume != 60 {
		t.Errorf("Expected start volume 60, got %d", fadeOut.StartVolume)
	}
	if fadeOut.CurrentVolume != 60 {
		t.Errorf("Expected current volume 60, got %d", fadeOut.CurrentVolume)
	}
	if !fadeOut.IsActive {
		t.Error("Expected IsActive to be true")
	}

	// Update progress
	st.UpdateFadeOutProgress("media_player.bedroom", 30)
	state = st.GetState()
	fadeOut = state.Outputs.FadeOutProgress["media_player.bedroom"]
	if fadeOut.CurrentVolume != 30 {
		t.Errorf("Expected current volume 30, got %d", fadeOut.CurrentVolume)
	}

	// Complete fade out
	st.UpdateFadeOutProgress("media_player.bedroom", 0)
	state = st.GetState()
	fadeOut = state.Outputs.FadeOutProgress["media_player.bedroom"]
	if fadeOut.IsActive {
		t.Error("Expected IsActive to be false when volume reaches 0")
	}

	// Clear fade out progress
	st.ClearFadeOutProgress()
	state = st.GetState()
	if len(state.Outputs.FadeOutProgress) != 0 {
		t.Error("Expected fade out progress to be cleared")
	}
}

func TestSleepHygieneTrackerRecordTTSAnnouncement(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	st.RecordTTSAnnouncement("Time to cuddle", "media_player.bedroom")

	state := st.GetState()
	if state.Outputs.LastTTSAnnouncement == nil {
		t.Fatal("Expected TTS announcement to be set")
	}
	if state.Outputs.LastTTSAnnouncement.Message != "Time to cuddle" {
		t.Errorf("Expected message 'Time to cuddle', got %s", state.Outputs.LastTTSAnnouncement.Message)
	}
	if state.Outputs.LastTTSAnnouncement.Speaker != "media_player.bedroom" {
		t.Errorf("Expected speaker 'media_player.bedroom', got %s", state.Outputs.LastTTSAnnouncement.Speaker)
	}
}

func TestSleepHygieneTrackerRecordReminders(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	// Record stop screens reminder
	st.RecordStopScreensReminder()
	state := st.GetState()
	if state.Outputs.StopScreensReminder == nil {
		t.Fatal("Expected StopScreensReminder to be set")
	}
	if !state.Outputs.StopScreensReminder.Triggered {
		t.Error("Expected StopScreensReminder.Triggered to be true")
	}

	// Record go to bed reminder
	st.RecordGoToBedReminder()
	state = st.GetState()
	if state.Outputs.GoToBedReminder == nil {
		t.Fatal("Expected GoToBedReminder to be set")
	}
	if !state.Outputs.GoToBedReminder.Triggered {
		t.Error("Expected GoToBedReminder.Triggered to be true")
	}
}

func TestSleepHygieneTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	// Set initial state
	inputs := map[string]interface{}{
		"isMasterAsleep": true,
	}
	st.UpdateCurrentInputs(inputs)

	// Get state
	state1 := st.GetState()

	// Modify the returned state
	state1.Inputs.Current["isMasterAsleep"] = false

	// Get state again
	state2 := st.GetState()

	// Original should be unchanged
	if state2.Inputs.Current["isMasterAsleep"] != true {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestSleepHygieneTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				st.UpdateWakeSequenceStatus("test")
				st.RecordFadeOutStart("media_player.test", 50)
				st.UpdateFadeOutProgress("media_player.test", 25)
			}
		}()
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = st.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestSleepHygieneShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*SleepHygieneShadowState)(nil)
}

// ============================================================================
// Phase 6: Read-Heavy Plugin Tracker Tests
// ============================================================================

// LoadSheddingTracker tests

func TestNewLoadSheddingTracker(t *testing.T) {
	t.Parallel()
	lst := NewLoadSheddingTracker()
	if lst == nil {
		t.Fatal("NewLoadSheddingTracker returned nil")
	}
	if lst.state == nil {
		t.Error("state not initialized")
	}
}

func TestLoadSheddingTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	lst := NewLoadSheddingTracker()

	inputs := map[string]interface{}{
		"currentEnergyLevel": "high",
		"outsideTemperature": 85.0,
	}

	lst.UpdateCurrentInputs(inputs)

	state := lst.GetState()
	if state.Inputs.Current["currentEnergyLevel"] != "high" {
		t.Errorf("Expected currentEnergyLevel to be 'high', got %v", state.Inputs.Current["currentEnergyLevel"])
	}
	if state.Inputs.Current["outsideTemperature"] != 85.0 {
		t.Errorf("Expected outsideTemperature to be 85.0, got %v", state.Inputs.Current["outsideTemperature"])
	}
}

func TestLoadSheddingTrackerSnapshotInputsForAction(t *testing.T) {
	t.Parallel()
	lst := NewLoadSheddingTracker()

	// Set initial inputs
	inputs := map[string]interface{}{
		"currentEnergyLevel": "low",
	}
	lst.UpdateCurrentInputs(inputs)

	// Snapshot
	lst.SnapshotInputsForAction()

	// Change current inputs
	newInputs := map[string]interface{}{
		"currentEnergyLevel": "high",
	}
	lst.UpdateCurrentInputs(newInputs)

	state := lst.GetState()

	// Current should be high
	if state.Inputs.Current["currentEnergyLevel"] != "high" {
		t.Errorf("Expected current currentEnergyLevel to be 'high', got %v", state.Inputs.Current["currentEnergyLevel"])
	}

	// At last action should be low
	if state.Inputs.AtLastAction["currentEnergyLevel"] != "low" {
		t.Errorf("Expected atLastAction currentEnergyLevel to be 'low', got %v", state.Inputs.AtLastAction["currentEnergyLevel"])
	}
}

func TestLoadSheddingTrackerRecordAction(t *testing.T) {
	t.Parallel()
	lst := NewLoadSheddingTracker()

	settings := ThermostatSettings{
		HoldMode: true,
		TempLow:  68.0,
		TempHigh: 78.0,
	}

	lst.RecordLoadSheddingAction(true, "increase_temp", "Low energy level", settings)

	state := lst.GetState()

	if !state.Outputs.Active {
		t.Error("Expected load shedding to be active")
	}
	if state.Outputs.LastActionType != "increase_temp" {
		t.Errorf("Expected action type 'increase_temp', got %s", state.Outputs.LastActionType)
	}
	if state.Outputs.LastActionReason != "Low energy level" {
		t.Errorf("Expected reason 'Low energy level', got %s", state.Outputs.LastActionReason)
	}
	if state.Outputs.ThermostatSettings.TempHigh != 78.0 {
		t.Errorf("Expected temp high 78.0, got %f", state.Outputs.ThermostatSettings.TempHigh)
	}
	if !state.Outputs.ThermostatSettings.HoldMode {
		t.Error("Expected HoldMode to be true")
	}
	if state.Outputs.LastActionTime.IsZero() {
		t.Error("Expected LastActionTime to be set")
	}
}

func TestLoadSheddingTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	lst := NewLoadSheddingTracker()

	// Set initial state
	inputs := map[string]interface{}{
		"currentEnergyLevel": "medium",
	}
	lst.UpdateCurrentInputs(inputs)

	// Get state
	state1 := lst.GetState()

	// Modify the returned state
	state1.Inputs.Current["currentEnergyLevel"] = "modified"

	// Get state again
	state2 := lst.GetState()

	// Original should be unchanged
	if state2.Inputs.Current["currentEnergyLevel"] != "medium" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestLoadSheddingShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*LoadSheddingShadowState)(nil)
}

// EnergyTracker tests

func TestNewEnergyTracker(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()
	if et == nil {
		t.Fatal("NewEnergyTracker returned nil")
	}
	if et.state == nil {
		t.Error("state not initialized")
	}
}

func TestEnergyTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	inputs := map[string]interface{}{
		"batteryPercentage": 75.0,
		"solarGenerationKW": 5.2,
		"gridImportWatts":   1200.0,
	}

	et.UpdateCurrentInputs(inputs)

	state := et.GetState()
	if state.Inputs.Current["batteryPercentage"] != 75.0 {
		t.Errorf("Expected batteryPercentage to be 75.0, got %v", state.Inputs.Current["batteryPercentage"])
	}
	if state.Inputs.Current["solarGenerationKW"] != 5.2 {
		t.Errorf("Expected solarGenerationKW to be 5.2, got %v", state.Inputs.Current["solarGenerationKW"])
	}
}

func TestEnergyTrackerUpdateSensorReadings(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateSensorReadings(80.0, 4.5, 12.3, true)

	state := et.GetState()
	if state.Outputs.SensorReadings.BatteryPercentage != 80.0 {
		t.Errorf("Expected BatteryPercentage 80.0, got %f", state.Outputs.SensorReadings.BatteryPercentage)
	}
	if state.Outputs.SensorReadings.ThisHourSolarGenerationKW != 4.5 {
		t.Errorf("Expected ThisHourSolarGenerationKW 4.5, got %f", state.Outputs.SensorReadings.ThisHourSolarGenerationKW)
	}
	if state.Outputs.SensorReadings.RemainingSolarGenerationKWH != 12.3 {
		t.Errorf("Expected RemainingSolarGenerationKWH 12.3, got %f", state.Outputs.SensorReadings.RemainingSolarGenerationKWH)
	}
	if !state.Outputs.SensorReadings.IsGridAvailable {
		t.Error("Expected IsGridAvailable to be true")
	}
	if state.Outputs.SensorReadings.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestEnergyTrackerUpdateBatteryLevel(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateBatteryLevel("high")

	state := et.GetState()
	if state.Outputs.BatteryEnergyLevel != "high" {
		t.Errorf("Expected BatteryEnergyLevel 'high', got %s", state.Outputs.BatteryEnergyLevel)
	}
	if state.Outputs.LastComputations.LastBatteryLevelCalc.IsZero() {
		t.Error("Expected LastBatteryLevelCalc to be set")
	}
}

func TestEnergyTrackerUpdateSolarLevel(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateSolarLevel("medium")

	state := et.GetState()
	if state.Outputs.SolarProductionEnergyLevel != "medium" {
		t.Errorf("Expected SolarProductionEnergyLevel 'medium', got %s", state.Outputs.SolarProductionEnergyLevel)
	}
	if state.Outputs.LastComputations.LastSolarLevelCalc.IsZero() {
		t.Error("Expected LastSolarLevelCalc to be set")
	}
}

func TestEnergyTrackerUpdateOverallLevel(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateOverallLevel("low")

	state := et.GetState()
	if state.Outputs.CurrentEnergyLevel != "low" {
		t.Errorf("Expected CurrentEnergyLevel 'low', got %s", state.Outputs.CurrentEnergyLevel)
	}
	if state.Outputs.LastComputations.LastOverallLevelCalc.IsZero() {
		t.Error("Expected LastOverallLevelCalc to be set")
	}
}

func TestEnergyTrackerUpdateFreeEnergyAvailable(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateFreeEnergyAvailable(true)

	state := et.GetState()
	if !state.Outputs.IsFreeEnergyAvailable {
		t.Error("Expected IsFreeEnergyAvailable to be true")
	}
	if state.Outputs.LastComputations.LastFreeEnergyCheck.IsZero() {
		t.Error("Expected LastFreeEnergyCheck to be set")
	}

	// Test setting to false
	et.UpdateFreeEnergyAvailable(false)
	state = et.GetState()
	if state.Outputs.IsFreeEnergyAvailable {
		t.Error("Expected IsFreeEnergyAvailable to be false")
	}
}

func TestEnergyTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	inputs := map[string]interface{}{
		"batteryPercentage": 50.0,
	}
	et.UpdateCurrentInputs(inputs)

	state1 := et.GetState()
	state1.Inputs.Current["batteryPercentage"] = 99.0

	state2 := et.GetState()
	if state2.Inputs.Current["batteryPercentage"] != 50.0 {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestEnergyTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				et.UpdateBatteryLevel("high")
				et.UpdateSolarLevel("medium")
				et.UpdateOverallLevel("low")
				et.UpdateFreeEnergyAvailable(true)
				et.UpdateSensorReadings(80.0, 4.5, 12.3, true)
			}
		}()
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = et.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestEnergyShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*EnergyShadowState)(nil)
}

// StateTrackingTracker tests

func TestNewStateTrackingTracker(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()
	if stt == nil {
		t.Fatal("NewStateTrackingTracker returned nil")
	}
	if stt.state == nil {
		t.Error("state not initialized")
	}
}

func TestStateTrackingTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	inputs := map[string]interface{}{
		"isNickHome":     true,
		"isCarolineHome": false,
		"isMasterAsleep": true,
	}

	stt.UpdateCurrentInputs(inputs)

	state := stt.GetState()
	if state.Inputs.Current["isNickHome"] != true {
		t.Errorf("Expected isNickHome to be true, got %v", state.Inputs.Current["isNickHome"])
	}
	if state.Inputs.Current["isCarolineHome"] != false {
		t.Errorf("Expected isCarolineHome to be false, got %v", state.Inputs.Current["isCarolineHome"])
	}
	if state.Inputs.Current["isMasterAsleep"] != true {
		t.Errorf("Expected isMasterAsleep to be true, got %v", state.Inputs.Current["isMasterAsleep"])
	}
}

func TestStateTrackingTrackerUpdateDerivedStates(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	stt.UpdateDerivedStates(true, true, true, false)

	state := stt.GetState()
	if !state.Outputs.DerivedStates.IsAnyOwnerHome {
		t.Error("Expected IsAnyOwnerHome to be true")
	}
	if !state.Outputs.DerivedStates.IsAnyoneHome {
		t.Error("Expected IsAnyoneHome to be true")
	}
	if !state.Outputs.DerivedStates.IsAnyoneAsleep {
		t.Error("Expected IsAnyoneAsleep to be true")
	}
	if state.Outputs.DerivedStates.IsEveryoneAsleep {
		t.Error("Expected IsEveryoneAsleep to be false")
	}
	if state.Outputs.LastComputation.IsZero() {
		t.Error("Expected LastComputation to be set")
	}
}

func TestStateTrackingTrackerUpdateSleepDetectionTimer(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	// Activate timer
	stt.UpdateSleepDetectionTimer(true)

	state := stt.GetState()
	if !state.Outputs.TimerStates.SleepDetectionActive {
		t.Error("Expected SleepDetectionActive to be true")
	}
	if state.Outputs.TimerStates.SleepDetectionStarted.IsZero() {
		t.Error("Expected SleepDetectionStarted to be set")
	}

	// Deactivate timer
	stt.UpdateSleepDetectionTimer(false)

	state = stt.GetState()
	if state.Outputs.TimerStates.SleepDetectionActive {
		t.Error("Expected SleepDetectionActive to be false")
	}
	if !state.Outputs.TimerStates.SleepDetectionStarted.IsZero() {
		t.Error("Expected SleepDetectionStarted to be cleared")
	}
}

func TestStateTrackingTrackerUpdateWakeDetectionTimer(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	// Activate timer
	stt.UpdateWakeDetectionTimer(true)

	state := stt.GetState()
	if !state.Outputs.TimerStates.WakeDetectionActive {
		t.Error("Expected WakeDetectionActive to be true")
	}
	if state.Outputs.TimerStates.WakeDetectionStarted.IsZero() {
		t.Error("Expected WakeDetectionStarted to be set")
	}

	// Deactivate timer
	stt.UpdateWakeDetectionTimer(false)

	state = stt.GetState()
	if state.Outputs.TimerStates.WakeDetectionActive {
		t.Error("Expected WakeDetectionActive to be false")
	}
	if !state.Outputs.TimerStates.WakeDetectionStarted.IsZero() {
		t.Error("Expected WakeDetectionStarted to be cleared")
	}
}

func TestStateTrackingTrackerUpdateOwnerReturnTimer(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	// Activate timer
	stt.UpdateOwnerReturnTimer(true)

	state := stt.GetState()
	if !state.Outputs.TimerStates.OwnerReturnResetActive {
		t.Error("Expected OwnerReturnResetActive to be true")
	}
	if state.Outputs.TimerStates.OwnerReturnResetStarted.IsZero() {
		t.Error("Expected OwnerReturnResetStarted to be set")
	}

	// Deactivate timer
	stt.UpdateOwnerReturnTimer(false)

	state = stt.GetState()
	if state.Outputs.TimerStates.OwnerReturnResetActive {
		t.Error("Expected OwnerReturnResetActive to be false")
	}
	if !state.Outputs.TimerStates.OwnerReturnResetStarted.IsZero() {
		t.Error("Expected OwnerReturnResetStarted to be cleared")
	}
}

func TestStateTrackingTrackerRecordArrivalAnnouncement(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	stt.RecordArrivalAnnouncement("Nick", "Nick is home!")

	state := stt.GetState()
	if state.Outputs.LastAnnouncement == nil {
		t.Fatal("Expected LastAnnouncement to be set")
	}
	if state.Outputs.LastAnnouncement.Person != "Nick" {
		t.Errorf("Expected person 'Nick', got %s", state.Outputs.LastAnnouncement.Person)
	}
	if state.Outputs.LastAnnouncement.Message != "Nick is home!" {
		t.Errorf("Expected message 'Nick is home!', got %s", state.Outputs.LastAnnouncement.Message)
	}
	if state.Outputs.LastAnnouncement.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

func TestStateTrackingTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	inputs := map[string]interface{}{
		"isNickHome": true,
	}
	stt.UpdateCurrentInputs(inputs)

	state1 := stt.GetState()
	state1.Inputs.Current["isNickHome"] = false

	state2 := stt.GetState()
	if state2.Inputs.Current["isNickHome"] != true {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestStateTrackingTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	stt := NewStateTrackingTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				stt.UpdateDerivedStates(i%2 == 0, true, false, false)
				stt.UpdateSleepDetectionTimer(i%2 == 0)
				stt.UpdateWakeDetectionTimer(i%2 == 1)
				stt.UpdateOwnerReturnTimer(i%2 == 0)
				stt.RecordArrivalAnnouncement("Test", "Test message")
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = stt.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestStateTrackingShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*StateTrackingShadowState)(nil)
}

// DayPhaseTracker tests

func TestNewDayPhaseTracker(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()
	if dpt == nil {
		t.Fatal("NewDayPhaseTracker returned nil")
	}
	if dpt.state == nil {
		t.Error("state not initialized")
	}
}

func TestDayPhaseTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()

	inputs := map[string]interface{}{
		"sunElevation":    45.5,
		"sunAzimuth":      180.0,
		"nextSunriseTime": "2024-01-15T06:30:00Z",
	}

	dpt.UpdateCurrentInputs(inputs)

	state := dpt.GetState()
	if state.Inputs.Current["sunElevation"] != 45.5 {
		t.Errorf("Expected sunElevation to be 45.5, got %v", state.Inputs.Current["sunElevation"])
	}
	if state.Inputs.Current["sunAzimuth"] != 180.0 {
		t.Errorf("Expected sunAzimuth to be 180.0, got %v", state.Inputs.Current["sunAzimuth"])
	}
}

func TestDayPhaseTrackerUpdateSunEvent(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()

	dpt.UpdateSunEvent("sunset")

	state := dpt.GetState()
	if state.Outputs.SunEvent != "sunset" {
		t.Errorf("Expected SunEvent 'sunset', got %s", state.Outputs.SunEvent)
	}
	if state.Outputs.LastSunEventCalc.IsZero() {
		t.Error("Expected LastSunEventCalc to be set")
	}
}

func TestDayPhaseTrackerUpdateDayPhase(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()

	dpt.UpdateDayPhase("evening")

	state := dpt.GetState()
	if state.Outputs.DayPhase != "evening" {
		t.Errorf("Expected DayPhase 'evening', got %s", state.Outputs.DayPhase)
	}
	if state.Outputs.LastDayPhaseCalc.IsZero() {
		t.Error("Expected LastDayPhaseCalc to be set")
	}
}

func TestDayPhaseTrackerUpdateNextTransition(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()

	transitionTime := time.Now().Add(2 * time.Hour)
	dpt.UpdateNextTransition(transitionTime, "night")

	state := dpt.GetState()
	if !state.Outputs.NextTransitionTime.Equal(transitionTime) {
		t.Errorf("Expected NextTransitionTime to match, got %v", state.Outputs.NextTransitionTime)
	}
	if state.Outputs.NextTransitionPhase != "night" {
		t.Errorf("Expected NextTransitionPhase 'night', got %s", state.Outputs.NextTransitionPhase)
	}
}

func TestDayPhaseTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()

	inputs := map[string]interface{}{
		"sunElevation": 30.0,
	}
	dpt.UpdateCurrentInputs(inputs)

	state1 := dpt.GetState()
	state1.Inputs.Current["sunElevation"] = 90.0

	state2 := dpt.GetState()
	if state2.Inputs.Current["sunElevation"] != 30.0 {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestDayPhaseTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	dpt := NewDayPhaseTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				dpt.UpdateSunEvent("sunset")
				dpt.UpdateDayPhase("evening")
				dpt.UpdateNextTransition(time.Now().Add(time.Hour), "night")
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = dpt.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestDayPhaseShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*DayPhaseShadowState)(nil)
}

// TVTracker tests

func TestNewTVTracker(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()
	if tvt == nil {
		t.Fatal("NewTVTracker returned nil")
	}
	if tvt.state == nil {
		t.Error("state not initialized")
	}
}

func TestTVTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	inputs := map[string]interface{}{
		"appleTVState": "playing",
		"tvPowerState": "on",
		"currentInput": "HDMI1",
	}

	tvt.UpdateCurrentInputs(inputs)

	state := tvt.GetState()
	if state.Inputs.Current["appleTVState"] != "playing" {
		t.Errorf("Expected appleTVState to be 'playing', got %v", state.Inputs.Current["appleTVState"])
	}
	if state.Inputs.Current["tvPowerState"] != "on" {
		t.Errorf("Expected tvPowerState to be 'on', got %v", state.Inputs.Current["tvPowerState"])
	}
	if state.Inputs.Current["currentInput"] != "HDMI1" {
		t.Errorf("Expected currentInput to be 'HDMI1', got %v", state.Inputs.Current["currentInput"])
	}
}

func TestTVTrackerUpdateAppleTVState(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	tvt.UpdateAppleTVState(true, "playing")

	state := tvt.GetState()
	if !state.Outputs.IsAppleTVPlaying {
		t.Error("Expected IsAppleTVPlaying to be true")
	}
	if state.Outputs.AppleTVState != "playing" {
		t.Errorf("Expected AppleTVState 'playing', got %s", state.Outputs.AppleTVState)
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}

	// Test paused state
	tvt.UpdateAppleTVState(false, "paused")
	state = tvt.GetState()
	if state.Outputs.IsAppleTVPlaying {
		t.Error("Expected IsAppleTVPlaying to be false")
	}
	if state.Outputs.AppleTVState != "paused" {
		t.Errorf("Expected AppleTVState 'paused', got %s", state.Outputs.AppleTVState)
	}
}

func TestTVTrackerUpdateTVPower(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	tvt.UpdateTVPower(true)

	state := tvt.GetState()
	if !state.Outputs.IsTVOn {
		t.Error("Expected IsTVOn to be true")
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}

	// Test power off
	tvt.UpdateTVPower(false)
	state = tvt.GetState()
	if state.Outputs.IsTVOn {
		t.Error("Expected IsTVOn to be false")
	}
}

func TestTVTrackerUpdateHDMIInput(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	tvt.UpdateHDMIInput("HDMI2")

	state := tvt.GetState()
	if state.Outputs.CurrentHDMIInput != "HDMI2" {
		t.Errorf("Expected CurrentHDMIInput 'HDMI2', got %s", state.Outputs.CurrentHDMIInput)
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestTVTrackerUpdateTVPlaying(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	tvt.UpdateTVPlaying(true)

	state := tvt.GetState()
	if !state.Outputs.IsTVPlaying {
		t.Error("Expected IsTVPlaying to be true")
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}

	// Test setting to false
	tvt.UpdateTVPlaying(false)
	state = tvt.GetState()
	if state.Outputs.IsTVPlaying {
		t.Error("Expected IsTVPlaying to be false")
	}
}

func TestTVTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	inputs := map[string]interface{}{
		"appleTVState": "playing",
	}
	tvt.UpdateCurrentInputs(inputs)

	state1 := tvt.GetState()
	state1.Inputs.Current["appleTVState"] = "modified"

	state2 := tvt.GetState()
	if state2.Inputs.Current["appleTVState"] != "playing" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestTVTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tvt.UpdateAppleTVState(i%2 == 0, "playing")
				tvt.UpdateTVPower(i%2 == 0)
				tvt.UpdateHDMIInput(fmt.Sprintf("HDMI%d", i%4))
				tvt.UpdateTVPlaying(i%2 == 0)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = tvt.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestTVShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*TVShadowState)(nil)
}
