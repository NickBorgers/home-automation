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

// ============================================================================
// TV Tracker Additional Tests (for uncovered methods)
// ============================================================================

func TestTVTrackerUpdateSyncBoxAvailable(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	// Initially sync box should be available (per default in NewTVShadowState)
	state := tvt.GetState()
	if !state.Outputs.SyncBoxAvailable {
		t.Error("Expected SyncBoxAvailable to be true initially")
	}

	// Mark as unavailable
	tvt.UpdateSyncBoxAvailable(false)

	state = tvt.GetState()
	if state.Outputs.SyncBoxAvailable {
		t.Error("Expected SyncBoxAvailable to be false")
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}

	// Mark as available again
	tvt.UpdateSyncBoxAvailable(true)

	state = tvt.GetState()
	if !state.Outputs.SyncBoxAvailable {
		t.Error("Expected SyncBoxAvailable to be true")
	}
}

func TestTVTrackerUpdateLastRecovery(t *testing.T) {
	t.Parallel()
	tvt := NewTVTracker()

	rebootTime := time.Now().Add(-5 * time.Minute)
	dailyCount := 3

	tvt.UpdateLastRecovery(rebootTime, dailyCount)

	state := tvt.GetState()
	if !state.Outputs.LastSyncBoxReboot.Equal(rebootTime) {
		t.Errorf("Expected LastSyncBoxReboot to be %v, got %v", rebootTime, state.Outputs.LastSyncBoxReboot)
	}
	if state.Outputs.DailyRebootCount != dailyCount {
		t.Errorf("Expected DailyRebootCount to be %d, got %d", dailyCount, state.Outputs.DailyRebootCount)
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

// ============================================================================
// SleepHygiene Tracker Additional Tests (for uncovered methods)
// ============================================================================

func TestSleepHygieneTrackerUpdateEightSleepAvailability(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	checkTime := time.Now()
	st.UpdateEightSleepAvailability(true, checkTime)

	state := st.GetState()
	if !state.Outputs.EightSleepAvailable {
		t.Error("Expected EightSleepAvailable to be true")
	}
	if state.Outputs.BackupWakeEnabled {
		t.Error("Expected BackupWakeEnabled to be false when Eight Sleep is available")
	}
	if !state.Outputs.LastAvailabilityCheck.Equal(checkTime) {
		t.Errorf("Expected LastAvailabilityCheck to be %v, got %v", checkTime, state.Outputs.LastAvailabilityCheck)
	}

	// Test unavailable state
	checkTime2 := time.Now()
	st.UpdateEightSleepAvailability(false, checkTime2)

	state = st.GetState()
	if state.Outputs.EightSleepAvailable {
		t.Error("Expected EightSleepAvailable to be false")
	}
	if !state.Outputs.BackupWakeEnabled {
		t.Error("Expected BackupWakeEnabled to be true when Eight Sleep is unavailable")
	}
}

func TestSleepHygieneTrackerRecordHumanOverride(t *testing.T) {
	t.Parallel()
	st := NewSleepHygieneTracker()

	// First record a fade out start
	st.RecordFadeOutStart("media_player.bedroom", 60)

	// Then record a human override
	st.RecordHumanOverride("media_player.bedroom", 50, 80)

	state := st.GetState()
	fadeOut, exists := state.Outputs.FadeOutProgress["media_player.bedroom"]
	if !exists {
		t.Fatal("Expected fade out progress for media_player.bedroom")
	}
	if !fadeOut.HumanOverrideDetected {
		t.Error("Expected HumanOverrideDetected to be true")
	}
	if fadeOut.ExpectedVolume != 50 {
		t.Errorf("Expected ExpectedVolume 50, got %d", fadeOut.ExpectedVolume)
	}
	if fadeOut.ActualVolume != 80 {
		t.Errorf("Expected ActualVolume 80, got %d", fadeOut.ActualVolume)
	}
	if fadeOut.IsActive {
		t.Error("Expected IsActive to be false after human override")
	}
}

// ============================================================================
// SexMode Tracker Tests
// ============================================================================

func TestNewSexModeTracker(t *testing.T) {
	t.Parallel()
	smt := NewSexModeTracker()
	if smt == nil {
		t.Fatal("NewSexModeTracker returned nil")
	}
	if smt.state == nil {
		t.Error("state not initialized")
	}
}

func TestSexModeTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	smt := NewSexModeTracker()

	inputs := map[string]interface{}{
		"musicPlaybackType": "romance",
		"isNickHome":        true,
		"isCarolineHome":    true,
	}

	smt.UpdateCurrentInputs(inputs)

	state := smt.GetState()
	if state.Inputs.Current["musicPlaybackType"] != "romance" {
		t.Errorf("Expected musicPlaybackType to be 'romance', got %v", state.Inputs.Current["musicPlaybackType"])
	}
	if state.Inputs.Current["isNickHome"] != true {
		t.Errorf("Expected isNickHome to be true, got %v", state.Inputs.Current["isNickHome"])
	}
}

func TestSexModeTrackerSnapshotInputsForAction(t *testing.T) {
	t.Parallel()
	smt := NewSexModeTracker()

	// Set initial inputs
	inputs := map[string]interface{}{
		"musicPlaybackType": "none",
	}
	smt.UpdateCurrentInputs(inputs)

	// Snapshot
	smt.SnapshotInputsForAction()

	// Change current inputs
	newInputs := map[string]interface{}{
		"musicPlaybackType": "romance",
	}
	smt.UpdateCurrentInputs(newInputs)

	state := smt.GetState()

	// Current should be romance
	if state.Inputs.Current["musicPlaybackType"] != "romance" {
		t.Errorf("Expected current musicPlaybackType to be 'romance', got %v", state.Inputs.Current["musicPlaybackType"])
	}

	// At last action should be none
	if state.Inputs.AtLastAction["musicPlaybackType"] != "none" {
		t.Errorf("Expected atLastAction musicPlaybackType to be 'none', got %v", state.Inputs.AtLastAction["musicPlaybackType"])
	}
}

func TestSexModeTrackerRecordAction(t *testing.T) {
	t.Parallel()
	smt := NewSexModeTracker()

	activatedAt := time.Now()
	smt.RecordAction("activate", "Button pressed", true, "relaxing", activatedAt)

	state := smt.GetState()

	if !state.Outputs.IsActive {
		t.Error("Expected IsActive to be true")
	}
	if state.Outputs.PreSexMusicType != "relaxing" {
		t.Errorf("Expected PreSexMusicType 'relaxing', got %s", state.Outputs.PreSexMusicType)
	}
	if !state.Outputs.ActivatedAt.Equal(activatedAt) {
		t.Errorf("Expected ActivatedAt to match, got %v", state.Outputs.ActivatedAt)
	}
	if state.Outputs.LastActionType != "activate" {
		t.Errorf("Expected LastActionType 'activate', got %s", state.Outputs.LastActionType)
	}
	if state.Outputs.LastActionReason != "Button pressed" {
		t.Errorf("Expected LastActionReason 'Button pressed', got %s", state.Outputs.LastActionReason)
	}
	if state.Outputs.LastActionTime.IsZero() {
		t.Error("Expected LastActionTime to be set")
	}

	// Test deactivation
	smt.RecordAction("deactivate", "Auto timeout", false, "", time.Time{})

	state = smt.GetState()
	if state.Outputs.IsActive {
		t.Error("Expected IsActive to be false after deactivation")
	}
}

func TestSexModeTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	smt := NewSexModeTracker()

	inputs := map[string]interface{}{
		"testKey": "testValue",
	}
	smt.UpdateCurrentInputs(inputs)

	state1 := smt.GetState()
	state1.Inputs.Current["testKey"] = "modified"

	state2 := smt.GetState()
	if state2.Inputs.Current["testKey"] != "testValue" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestSexModeTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	smt := NewSexModeTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				smt.UpdateCurrentInputs(map[string]interface{}{"count": i*20 + j})
				smt.SnapshotInputsForAction()
				smt.RecordAction("test", "test reason", i%2 == 0, "test", time.Now())
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = smt.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestSexModeShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*SexModeShadowState)(nil)
}

// ============================================================================
// Christmas Tracker Tests
// ============================================================================

func TestNewChristmasTracker(t *testing.T) {
	t.Parallel()
	ct := NewChristmasTracker()
	if ct == nil {
		t.Fatal("NewChristmasTracker returned nil")
	}
	if ct.state == nil {
		t.Error("state not initialized")
	}
}

func TestChristmasTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	ct := NewChristmasTracker()

	inputs := map[string]interface{}{
		"dayPhase":     "evening",
		"isAnyoneHome": true,
	}

	ct.UpdateCurrentInputs(inputs)

	state := ct.GetState()
	if state.Inputs.Current["dayPhase"] != "evening" {
		t.Errorf("Expected dayPhase to be 'evening', got %v", state.Inputs.Current["dayPhase"])
	}
	if state.Inputs.Current["isAnyoneHome"] != true {
		t.Errorf("Expected isAnyoneHome to be true, got %v", state.Inputs.Current["isAnyoneHome"])
	}
}

func TestChristmasTrackerSnapshotInputsForAction(t *testing.T) {
	t.Parallel()
	ct := NewChristmasTracker()

	// Set initial inputs
	inputs := map[string]interface{}{
		"dayPhase": "morning",
	}
	ct.UpdateCurrentInputs(inputs)

	// Snapshot
	ct.SnapshotInputsForAction()

	// Change current inputs
	newInputs := map[string]interface{}{
		"dayPhase": "evening",
	}
	ct.UpdateCurrentInputs(newInputs)

	state := ct.GetState()

	// Current should be evening
	if state.Inputs.Current["dayPhase"] != "evening" {
		t.Errorf("Expected current dayPhase to be 'evening', got %v", state.Inputs.Current["dayPhase"])
	}

	// At last action should be morning
	if state.Inputs.AtLastAction["dayPhase"] != "morning" {
		t.Errorf("Expected atLastAction dayPhase to be 'morning', got %v", state.Inputs.AtLastAction["dayPhase"])
	}
}

func TestChristmasTrackerRecordActivation(t *testing.T) {
	t.Parallel()
	ct := NewChristmasTracker()

	ct.RecordActivation(5, "Evening time, someone is home")

	state := ct.GetState()

	if state.Outputs.LightsActivated != 5 {
		t.Errorf("Expected LightsActivated 5, got %d", state.Outputs.LightsActivated)
	}
	if state.Outputs.LastActionReason != "Evening time, someone is home" {
		t.Errorf("Expected LastActionReason 'Evening time, someone is home', got %s", state.Outputs.LastActionReason)
	}
	if state.Outputs.LastActivationTime.IsZero() {
		t.Error("Expected LastActivationTime to be set")
	}
}

func TestChristmasTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	ct := NewChristmasTracker()

	inputs := map[string]interface{}{
		"testKey": "testValue",
	}
	ct.UpdateCurrentInputs(inputs)

	state1 := ct.GetState()
	state1.Inputs.Current["testKey"] = "modified"

	state2 := ct.GetState()
	if state2.Inputs.Current["testKey"] != "testValue" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestChristmasTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	ct := NewChristmasTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				ct.UpdateCurrentInputs(map[string]interface{}{"count": i*20 + j})
				ct.SnapshotInputsForAction()
				ct.RecordActivation(i+1, "test reason")
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = ct.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestChristmasShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*ChristmasShadowState)(nil)
}

// ============================================================================
// Environmental Tracker Tests
// ============================================================================

func TestNewEnvironmentalTracker(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()
	if et == nil {
		t.Fatal("NewEnvironmentalTracker returned nil")
	}
	if et.state == nil {
		t.Error("state not initialized")
	}
}

func TestEnvironmentalTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	inputs := map[string]interface{}{
		"humidity":    65.5,
		"temperature": 72.0,
	}

	et.UpdateCurrentInputs(inputs)

	state := et.GetState()
	if state.Inputs.Current["humidity"] != 65.5 {
		t.Errorf("Expected humidity to be 65.5, got %v", state.Inputs.Current["humidity"])
	}
}

func TestEnvironmentalTrackerUpdateHumiditySensors(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	sensors := []HumiditySensorData{
		{EntityID: "sensor.bathroom_humidity", FriendlyName: "Bathroom", IsIndoor: true, Value: 75.0, Valid: true},
		{EntityID: "sensor.kitchen_humidity", FriendlyName: "Kitchen", IsIndoor: true, Value: 55.0, Valid: true},
	}

	et.UpdateHumiditySensors(sensors)

	state := et.GetState()
	if len(state.Outputs.HumiditySensors) != 2 {
		t.Errorf("Expected 2 humidity sensors, got %d", len(state.Outputs.HumiditySensors))
	}
	if state.Outputs.HumiditySensors[0].FriendlyName != "Bathroom" {
		t.Errorf("Expected first sensor to be 'Bathroom', got %s", state.Outputs.HumiditySensors[0].FriendlyName)
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestEnvironmentalTrackerUpdateAlertLevel(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	conditionStart := time.Now().Add(-35 * time.Minute)
	et.UpdateAlertLevel("warning", conditionStart, true)

	state := et.GetState()
	if state.Outputs.AlertLevel != "warning" {
		t.Errorf("Expected AlertLevel 'warning', got %s", state.Outputs.AlertLevel)
	}
	if !state.Outputs.ConditionStartTime.Equal(conditionStart) {
		t.Error("Expected ConditionStartTime to match")
	}
	if !state.Outputs.IsSustained {
		t.Error("Expected IsSustained to be true")
	}
}

func TestEnvironmentalTrackerRecordNotification(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	locations := []string{"Bathroom", "Kitchen"}
	et.RecordNotification("warning", "High humidity detected", locations)

	state := et.GetState()
	if state.Outputs.LastNotification == nil {
		t.Fatal("Expected LastNotification to be set")
	}
	if state.Outputs.LastNotification.Level != "warning" {
		t.Errorf("Expected level 'warning', got %s", state.Outputs.LastNotification.Level)
	}
	if state.Outputs.LastNotification.Message != "High humidity detected" {
		t.Errorf("Expected message 'High humidity detected', got %s", state.Outputs.LastNotification.Message)
	}
	if len(state.Outputs.LastNotification.SensorLocations) != 2 {
		t.Errorf("Expected 2 sensor locations, got %d", len(state.Outputs.LastNotification.SensorLocations))
	}
}

func TestEnvironmentalTrackerRecordResolutionNotice(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	et.RecordResolutionNotice("Humidity levels have returned to normal")

	state := et.GetState()
	if state.Outputs.LastResolutionNotice == nil {
		t.Fatal("Expected LastResolutionNotice to be set")
	}
	if state.Outputs.LastResolutionNotice.Level != "resolved" {
		t.Errorf("Expected level 'resolved', got %s", state.Outputs.LastResolutionNotice.Level)
	}
	if state.Outputs.LastResolutionNotice.Message != "Humidity levels have returned to normal" {
		t.Errorf("Unexpected message: %s", state.Outputs.LastResolutionNotice.Message)
	}
}

func TestEnvironmentalTrackerUpdateWaterLeakSensors(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	sensors := []WaterLeakSensorData{
		{EntityID: "binary_sensor.water_leak_1", FriendlyName: "Under Sink", State: "off"},
		{EntityID: "binary_sensor.water_leak_2", FriendlyName: "Near Washer", State: "on"},
	}

	et.UpdateWaterLeakSensors(sensors)

	state := et.GetState()
	if len(state.Outputs.WaterLeakSensors) != 2 {
		t.Errorf("Expected 2 water leak sensors, got %d", len(state.Outputs.WaterLeakSensors))
	}
}

func TestEnvironmentalTrackerUpdateActiveWaterLeaks(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	alerts := []WaterLeakAlert{
		{EntityID: "binary_sensor.water_leak_2", FriendlyName: "Near Washer", DetectedAt: time.Now(), NotificationSent: true},
	}

	et.UpdateActiveWaterLeaks(alerts)

	state := et.GetState()
	if len(state.Outputs.ActiveWaterLeaks) != 1 {
		t.Errorf("Expected 1 active water leak, got %d", len(state.Outputs.ActiveWaterLeaks))
	}
}

func TestEnvironmentalTrackerRecordWaterLeakNotification(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	et.RecordWaterLeakNotification("binary_sensor.water_leak_1", "Under Sink", "Water leak detected under the sink!")

	state := et.GetState()
	if state.Outputs.LastWaterLeakNotice == nil {
		t.Fatal("Expected LastWaterLeakNotice to be set")
	}
	if state.Outputs.LastWaterLeakNotice.EntityID != "binary_sensor.water_leak_1" {
		t.Errorf("Expected EntityID 'binary_sensor.water_leak_1', got %s", state.Outputs.LastWaterLeakNotice.EntityID)
	}
	if state.Outputs.LastWaterLeakNotice.FriendlyName != "Under Sink" {
		t.Errorf("Expected FriendlyName 'Under Sink', got %s", state.Outputs.LastWaterLeakNotice.FriendlyName)
	}
}

func TestEnvironmentalTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	inputs := map[string]interface{}{
		"testKey": "testValue",
	}
	et.UpdateCurrentInputs(inputs)

	state1 := et.GetState()
	state1.Inputs.Current["testKey"] = "modified"

	state2 := et.GetState()
	if state2.Inputs.Current["testKey"] != "testValue" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestEnvironmentalTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	et := NewEnvironmentalTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				et.UpdateCurrentInputs(map[string]interface{}{"count": i*20 + j})
				et.UpdateAlertLevel("warning", time.Now(), true)
				et.RecordNotification("warning", "test", []string{"test"})
			}
		}(i)
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

func TestEnvironmentalShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*EnvironmentalShadowState)(nil)
}

// ============================================================================
// SensorHealth Tracker Tests
// ============================================================================

func TestNewSensorHealthTracker(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()
	if st == nil {
		t.Fatal("NewSensorHealthTracker returned nil")
	}
	if st.state == nil {
		t.Error("state not initialized")
	}
}

func TestSensorHealthTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	inputs := map[string]interface{}{
		"sensorCount": 10,
	}

	st.UpdateCurrentInputs(inputs)

	state := st.GetState()
	if state.Inputs.Current["sensorCount"] != 10 {
		t.Errorf("Expected sensorCount to be 10, got %v", state.Inputs.Current["sensorCount"])
	}
}

func TestSensorHealthTrackerUpdateBatterySensors(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	sensors := []BatterySensorData{
		{EntityID: "sensor.battery_1", FriendlyName: "Motion Sensor", BatteryLevel: 85, IsLow: false},
		{EntityID: "sensor.battery_2", FriendlyName: "Door Sensor", BatteryLevel: 15, IsLow: true},
	}

	st.UpdateBatterySensors(sensors)

	state := st.GetState()
	if len(state.Outputs.BatterySensors) != 2 {
		t.Errorf("Expected 2 battery sensors, got %d", len(state.Outputs.BatterySensors))
	}
	if state.Outputs.BatterySensors[1].IsLow != true {
		t.Error("Expected second sensor to be marked as low")
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestSensorHealthTrackerUpdateTemperatureSensors(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	sensors := []TemperatureSensorData{
		{EntityID: "sensor.temp_1", FriendlyName: "Garage Temp", Value: 72.0, Valid: true, IsLockedUp: false},
		{EntityID: "sensor.temp_2", FriendlyName: "Outdoor Temp", Value: 55.0, Valid: true, IsLockedUp: true},
	}

	st.UpdateTemperatureSensors(sensors)

	state := st.GetState()
	if len(state.Outputs.TemperatureSensors) != 2 {
		t.Errorf("Expected 2 temperature sensors, got %d", len(state.Outputs.TemperatureSensors))
	}
	if state.Outputs.TemperatureSensors[1].IsLockedUp != true {
		t.Error("Expected second sensor to be marked as locked up")
	}
}

func TestSensorHealthTrackerUpdateLowBatteryAlerts(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	alerts := []LowBatteryAlert{
		{EntityID: "sensor.battery_2", FriendlyName: "Door Sensor", BatteryLevel: 15, DetectedAt: time.Now(), NotificationSent: true},
	}

	st.UpdateLowBatteryAlerts(alerts)

	state := st.GetState()
	if len(state.Outputs.LowBatteryAlerts) != 1 {
		t.Errorf("Expected 1 low battery alert, got %d", len(state.Outputs.LowBatteryAlerts))
	}
}

func TestSensorHealthTrackerRecordNotification(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	st.RecordNotification("low_battery", "sensor.battery_2", "Low battery detected on Door Sensor")

	state := st.GetState()
	if state.Outputs.LastNotification == nil {
		t.Fatal("Expected LastNotification to be set")
	}
	if state.Outputs.LastNotification.AlertType != "low_battery" {
		t.Errorf("Expected AlertType 'low_battery', got %s", state.Outputs.LastNotification.AlertType)
	}
	if state.Outputs.LastNotification.EntityID != "sensor.battery_2" {
		t.Errorf("Expected EntityID 'sensor.battery_2', got %s", state.Outputs.LastNotification.EntityID)
	}
}

func TestSensorHealthTrackerRecordTemperatureLockupNotification(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	st.RecordTemperatureLockupNotification("sensor.temp_garage", "Garage Temperature", "Temperature sensor appears locked up")

	state := st.GetState()
	if state.Outputs.LastTemperatureLockupNotice == nil {
		t.Fatal("Expected LastTemperatureLockupNotice to be set")
	}
	if state.Outputs.LastTemperatureLockupNotice.EntityID != "sensor.temp_garage" {
		t.Errorf("Expected EntityID 'sensor.temp_garage', got %s", state.Outputs.LastTemperatureLockupNotice.EntityID)
	}
	if state.Outputs.LastTemperatureLockupNotice.FriendlyName != "Garage Temperature" {
		t.Errorf("Expected FriendlyName 'Garage Temperature', got %s", state.Outputs.LastTemperatureLockupNotice.FriendlyName)
	}
}

func TestSensorHealthTrackerRecordTemperatureRecoveryNotification(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	st.RecordTemperatureRecoveryNotification("sensor.temp_garage", "Garage Temperature", "Temperature sensor recovered")

	state := st.GetState()
	if state.Outputs.LastTemperatureRecoveryNotice == nil {
		t.Fatal("Expected LastTemperatureRecoveryNotice to be set")
	}
	if state.Outputs.LastTemperatureRecoveryNotice.EntityID != "sensor.temp_garage" {
		t.Errorf("Expected EntityID 'sensor.temp_garage', got %s", state.Outputs.LastTemperatureRecoveryNotice.EntityID)
	}
}

func TestSensorHealthTrackerSetLastDiscoveryRefresh(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	refreshTime := time.Now()
	st.SetLastDiscoveryRefresh(refreshTime)

	state := st.GetState()
	if !state.Outputs.LastDiscoveryRefresh.Equal(refreshTime) {
		t.Errorf("Expected LastDiscoveryRefresh to be %v, got %v", refreshTime, state.Outputs.LastDiscoveryRefresh)
	}
}

func TestSensorHealthTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	inputs := map[string]interface{}{
		"testKey": "testValue",
	}
	st.UpdateCurrentInputs(inputs)

	state1 := st.GetState()
	state1.Inputs.Current["testKey"] = "modified"

	state2 := st.GetState()
	if state2.Inputs.Current["testKey"] != "testValue" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestSensorHealthTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				st.UpdateCurrentInputs(map[string]interface{}{"count": i*20 + j})
				st.RecordNotification("low_battery", "sensor.test", "test message")
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

	wg.Wait()
}

func TestSensorHealthShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*SensorHealthShadowState)(nil)
}

// ============================================================================
// SensorConfig Tracker Tests
// ============================================================================

func TestNewSensorConfigTracker(t *testing.T) {
	t.Parallel()
	sct := NewSensorConfigTracker()
	if sct == nil {
		t.Fatal("NewSensorConfigTracker returned nil")
	}
	if sct.state == nil {
		t.Error("state not initialized")
	}
}

func TestSensorConfigTrackerRecordConfiguration(t *testing.T) {
	t.Parallel()
	sct := NewSensorConfigTracker()

	configuredEntities := []string{"sensor.motion_1", "sensor.motion_2"}
	failedEntities := []string{"sensor.motion_3"}

	sct.RecordConfiguration("motion_sensitivity", "Set motion sensitivity to high", 3.0, configuredEntities, failedEntities)

	state := sct.GetState()
	if len(state.Outputs.Configurations) != 1 {
		t.Errorf("Expected 1 configuration, got %d", len(state.Outputs.Configurations))
	}

	config := state.Outputs.Configurations[0]
	if config.ConfigType != "motion_sensitivity" {
		t.Errorf("Expected ConfigType 'motion_sensitivity', got %s", config.ConfigType)
	}
	if config.Description != "Set motion sensitivity to high" {
		t.Errorf("Expected Description 'Set motion sensitivity to high', got %s", config.Description)
	}
	if config.Value != 3.0 {
		t.Errorf("Expected Value 3.0, got %f", config.Value)
	}
	if len(config.ConfiguredEntities) != 2 {
		t.Errorf("Expected 2 configured entities, got %d", len(config.ConfiguredEntities))
	}
	if len(config.FailedEntities) != 1 {
		t.Errorf("Expected 1 failed entity, got %d", len(config.FailedEntities))
	}
	if config.ConfiguredAt.IsZero() {
		t.Error("Expected ConfiguredAt to be set")
	}
	if state.Outputs.ConfiguredAt.IsZero() {
		t.Error("Expected ConfiguredAt to be set")
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestSensorConfigTrackerClear(t *testing.T) {
	t.Parallel()
	sct := NewSensorConfigTracker()

	// Add some configurations
	sct.RecordConfiguration("test1", "Test 1", 1.0, []string{"entity1"}, nil)
	sct.RecordConfiguration("test2", "Test 2", 2.0, []string{"entity2"}, nil)

	// Verify configurations exist
	state := sct.GetState()
	if len(state.Outputs.Configurations) != 2 {
		t.Fatalf("Expected 2 configurations before clear, got %d", len(state.Outputs.Configurations))
	}

	// Clear
	sct.Clear()

	// Verify cleared
	state = sct.GetState()
	if len(state.Outputs.Configurations) != 0 {
		t.Errorf("Expected 0 configurations after clear, got %d", len(state.Outputs.Configurations))
	}
	if !state.Outputs.ConfiguredAt.IsZero() {
		t.Error("Expected ConfiguredAt to be cleared")
	}
}

func TestSensorConfigTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	sct := NewSensorConfigTracker()

	sct.RecordConfiguration("test", "Test", 1.0, []string{"entity1"}, nil)

	state1 := sct.GetState()

	// Modify the returned state
	state1.Outputs.Configurations[0].ConfigType = "modified"

	state2 := sct.GetState()
	if state2.Outputs.Configurations[0].ConfigType != "test" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestSensorConfigTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	sct := NewSensorConfigTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				sct.RecordConfiguration(fmt.Sprintf("config_%d_%d", i, j), "test", float64(i*20+j), []string{"entity"}, nil)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = sct.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestSensorConfigShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*SensorConfigShadowState)(nil)
}

// ============================================================================
// Infrastructure Tracker Tests
// ============================================================================

func TestNewInfrastructureTracker(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()
	if it == nil {
		t.Fatal("NewInfrastructureTracker returned nil")
	}
	if it.state == nil {
		t.Error("state not initialized")
	}
}

func TestInfrastructureTrackerUpdateCurrentInputs(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	inputs := map[string]interface{}{
		"septicPower": 85.0,
	}

	it.UpdateCurrentInputs(inputs)

	state := it.GetState()
	if state.Inputs.Current["septicPower"] != 85.0 {
		t.Errorf("Expected septicPower to be 85.0, got %v", state.Inputs.Current["septicPower"])
	}
}

func TestInfrastructureTrackerUpdateSepticPower(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	it.UpdateSepticPower(87.5)

	state := it.GetState()
	if state.Outputs.SepticSystemStatus.CurrentPowerW != 87.5 {
		t.Errorf("Expected CurrentPowerW 87.5, got %f", state.Outputs.SepticSystemStatus.CurrentPowerW)
	}
	if state.Outputs.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestInfrastructureTrackerUpdateSystemState(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	it.UpdateSystemState("normal")

	state := it.GetState()
	if state.Outputs.SepticSystemStatus.SystemState != "normal" {
		t.Errorf("Expected SystemState 'normal', got %s", state.Outputs.SepticSystemStatus.SystemState)
	}
}

func TestInfrastructureTrackerUpdateAeratorFailureStart(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	startTime := time.Now()
	it.UpdateAeratorFailureStart(startTime)

	state := it.GetState()
	if !state.Outputs.SepticSystemStatus.AeratorFailureStart.Equal(startTime) {
		t.Error("Expected AeratorFailureStart to match")
	}
}

func TestInfrastructureTrackerUpdatePumpRunningStart(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	startTime := time.Now()
	it.UpdatePumpRunningStart(startTime)

	state := it.GetState()
	if !state.Outputs.SepticSystemStatus.PumpRunningStart.Equal(startTime) {
		t.Error("Expected PumpRunningStart to match")
	}
}

func TestInfrastructureTrackerUpdateLastNormalPowerTime(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	normalTime := time.Now()
	it.UpdateLastNormalPowerTime(normalTime)

	state := it.GetState()
	if !state.Outputs.SepticSystemStatus.LastNormalPowerTime.Equal(normalTime) {
		t.Error("Expected LastNormalPowerTime to match")
	}
}

func TestInfrastructureTrackerUpdateIsAlerting(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	it.UpdateIsAlerting(true)

	state := it.GetState()
	if !state.Outputs.SepticSystemStatus.IsAlerting {
		t.Error("Expected IsAlerting to be true")
	}

	it.UpdateIsAlerting(false)

	state = it.GetState()
	if state.Outputs.SepticSystemStatus.IsAlerting {
		t.Error("Expected IsAlerting to be false")
	}
}

func TestInfrastructureTrackerUpdateActiveAlerts(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	alerts := []InfrastructureAlert{
		{AlertType: "aerator_failure", Message: "Aerator may have failed", DetectedAt: time.Now()},
	}

	it.UpdateActiveAlerts(alerts)

	state := it.GetState()
	if len(state.Outputs.ActiveAlerts) != 1 {
		t.Errorf("Expected 1 active alert, got %d", len(state.Outputs.ActiveAlerts))
	}
	if state.Outputs.ActiveAlerts[0].AlertType != "aerator_failure" {
		t.Errorf("Expected AlertType 'aerator_failure', got %s", state.Outputs.ActiveAlerts[0].AlertType)
	}
}

func TestInfrastructureTrackerRecordNotification(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	it.RecordNotification("aerator_failure", "Septic aerator may have failed", "high")

	state := it.GetState()
	if state.Outputs.LastNotification == nil {
		t.Fatal("Expected LastNotification to be set")
	}
	if state.Outputs.LastNotification.AlertType != "aerator_failure" {
		t.Errorf("Expected AlertType 'aerator_failure', got %s", state.Outputs.LastNotification.AlertType)
	}
	if state.Outputs.LastNotification.Priority != "high" {
		t.Errorf("Expected Priority 'high', got %s", state.Outputs.LastNotification.Priority)
	}
}

func TestInfrastructureTrackerRecordTTSAnnouncement(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	it.RecordTTSAnnouncement("Warning: Septic system issue detected")

	state := it.GetState()
	if state.Outputs.LastTTSAnnouncement == nil {
		t.Fatal("Expected LastTTSAnnouncement to be set")
	}
	if state.Outputs.LastTTSAnnouncement.Message != "Warning: Septic system issue detected" {
		t.Errorf("Expected message 'Warning: Septic system issue detected', got %s", state.Outputs.LastTTSAnnouncement.Message)
	}
	if state.Outputs.LastTTSAnnouncement.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

func TestInfrastructureTrackerClearAlerts(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	// Set up some alerts
	alerts := []InfrastructureAlert{
		{AlertType: "aerator_failure", Message: "Test", DetectedAt: time.Now()},
	}
	it.UpdateActiveAlerts(alerts)
	it.UpdateIsAlerting(true)
	it.UpdateAeratorFailureStart(time.Now())
	it.UpdatePumpRunningStart(time.Now())

	// Clear alerts
	it.ClearAlerts()

	state := it.GetState()
	if len(state.Outputs.ActiveAlerts) != 0 {
		t.Errorf("Expected 0 active alerts after clear, got %d", len(state.Outputs.ActiveAlerts))
	}
	if state.Outputs.SepticSystemStatus.IsAlerting {
		t.Error("Expected IsAlerting to be false after clear")
	}
	if !state.Outputs.SepticSystemStatus.AeratorFailureStart.IsZero() {
		t.Error("Expected AeratorFailureStart to be cleared")
	}
	if !state.Outputs.SepticSystemStatus.PumpRunningStart.IsZero() {
		t.Error("Expected PumpRunningStart to be cleared")
	}
}

func TestInfrastructureTrackerGetStateReturnsDeepCopy(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	inputs := map[string]interface{}{
		"testKey": "testValue",
	}
	it.UpdateCurrentInputs(inputs)

	state1 := it.GetState()
	state1.Inputs.Current["testKey"] = "modified"

	state2 := it.GetState()
	if state2.Inputs.Current["testKey"] != "testValue" {
		t.Error("Modifying returned state affected the internal state")
	}
}

func TestInfrastructureTrackerConcurrentAccess(t *testing.T) {
	t.Parallel()
	it := NewInfrastructureTracker()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				it.UpdateCurrentInputs(map[string]interface{}{"count": i*20 + j})
				it.UpdateSepticPower(float64(i*20 + j))
				it.UpdateIsAlerting(i%2 == 0)
				it.RecordNotification("test", "test message", "low")
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = it.GetState()
			}
		}()
	}

	wg.Wait()
}

func TestInfrastructureShadowStateImplementsInterface(t *testing.T) {
	t.Parallel()
	var _ PluginShadowState = (*InfrastructureShadowState)(nil)
}

// ============================================================================
// Energy Tracker Additional Tests (for uncovered methods)
// ============================================================================

func TestEnergyTrackerUpdateBatteryPercentage(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateBatteryPercentage(75.5)

	state := et.GetState()
	if state.Outputs.SensorReadings.BatteryPercentage != 75.5 {
		t.Errorf("Expected BatteryPercentage 75.5, got %f", state.Outputs.SensorReadings.BatteryPercentage)
	}
	if state.Outputs.SensorReadings.LastUpdate.IsZero() {
		t.Error("Expected LastUpdate to be set")
	}
}

func TestEnergyTrackerUpdateThisHourSolarKW(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateThisHourSolarKW(5.25)

	state := et.GetState()
	if state.Outputs.SensorReadings.ThisHourSolarGenerationKW != 5.25 {
		t.Errorf("Expected ThisHourSolarGenerationKW 5.25, got %f", state.Outputs.SensorReadings.ThisHourSolarGenerationKW)
	}
}

func TestEnergyTrackerUpdateRemainingSolarKWH(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateRemainingSolarKWH(15.75)

	state := et.GetState()
	if state.Outputs.SensorReadings.RemainingSolarGenerationKWH != 15.75 {
		t.Errorf("Expected RemainingSolarGenerationKWH 15.75, got %f", state.Outputs.SensorReadings.RemainingSolarGenerationKWH)
	}
}

func TestEnergyTrackerUpdateGridAvailable(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateGridAvailable(false)

	state := et.GetState()
	if state.Outputs.SensorReadings.IsGridAvailable {
		t.Error("Expected IsGridAvailable to be false")
	}

	et.UpdateGridAvailable(true)

	state = et.GetState()
	if !state.Outputs.SensorReadings.IsGridAvailable {
		t.Error("Expected IsGridAvailable to be true")
	}
}

func TestEnergyTrackerUpdateDiscoveredIndicatorLights(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	entities := []string{"light.indicator_1", "light.indicator_2", "light.indicator_3"}
	et.UpdateDiscoveredIndicatorLights(entities)

	state := et.GetState()
	if len(state.Outputs.DiscoveredIndicatorLights) != 3 {
		t.Errorf("Expected 3 indicator lights, got %d", len(state.Outputs.DiscoveredIndicatorLights))
	}
}

func TestEnergyTrackerUpdateIndicatorLightsAction(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	rgbColor := []int{0, 255, 0}
	entityIDs := []string{"light.indicator_1", "light.indicator_2"}
	et.UpdateIndicatorLightsAction("high", rgbColor, 75, entityIDs)

	state := et.GetState()
	if state.Outputs.IndicatorLightsAction == nil {
		t.Fatal("Expected IndicatorLightsAction to be set")
	}
	if state.Outputs.IndicatorLightsAction.EnergyLevel != "high" {
		t.Errorf("Expected EnergyLevel 'high', got %s", state.Outputs.IndicatorLightsAction.EnergyLevel)
	}
	if len(state.Outputs.IndicatorLightsAction.RGBColor) != 3 {
		t.Errorf("Expected 3 RGB values, got %d", len(state.Outputs.IndicatorLightsAction.RGBColor))
	}
	if state.Outputs.IndicatorLightsAction.BrightnessPct != 75 {
		t.Errorf("Expected BrightnessPct 75, got %d", state.Outputs.IndicatorLightsAction.BrightnessPct)
	}
	if len(state.Outputs.IndicatorLightsAction.EntityIDs) != 2 {
		t.Errorf("Expected 2 entity IDs, got %d", len(state.Outputs.IndicatorLightsAction.EntityIDs))
	}
}

func TestEnergyTrackerUpdateLuxReading(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateLuxReading("sensor.office_lux", 450.0)

	state := et.GetState()
	if state.Outputs.LuxSensorReadings == nil {
		t.Fatal("Expected LuxSensorReadings to be initialized")
	}
	reading, exists := state.Outputs.LuxSensorReadings["sensor.office_lux"]
	if !exists {
		t.Fatal("Expected lux reading for sensor.office_lux")
	}
	if reading.Lux != 450.0 {
		t.Errorf("Expected Lux 450.0, got %f", reading.Lux)
	}
	if reading.EntityID != "sensor.office_lux" {
		t.Errorf("Expected EntityID 'sensor.office_lux', got %s", reading.EntityID)
	}
}

func TestEnergyTrackerUpdateLightToLuxMapping(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	mapping := map[string]string{
		"light.office":  "sensor.office_lux",
		"light.kitchen": "sensor.kitchen_lux",
	}
	et.UpdateLightToLuxMapping(mapping)

	state := et.GetState()
	if state.Outputs.LightToLuxSensorMapping == nil {
		t.Fatal("Expected LightToLuxSensorMapping to be initialized")
	}
	if len(state.Outputs.LightToLuxSensorMapping) != 2 {
		t.Errorf("Expected 2 mappings, got %d", len(state.Outputs.LightToLuxSensorMapping))
	}
	if state.Outputs.LightToLuxSensorMapping["light.office"] != "sensor.office_lux" {
		t.Error("Expected light.office to map to sensor.office_lux")
	}
}

func TestEnergyTrackerUpdatePerDeviceBrightness(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdatePerDeviceBrightness("light.office", "sensor.office_lux", 450.0, 75, true)

	state := et.GetState()
	if state.Outputs.PerDeviceBrightness == nil {
		t.Fatal("Expected PerDeviceBrightness to be initialized")
	}
	brightness, exists := state.Outputs.PerDeviceBrightness["light.office"]
	if !exists {
		t.Fatal("Expected brightness for light.office")
	}
	if brightness.LightEntity != "light.office" {
		t.Errorf("Expected LightEntity 'light.office', got %s", brightness.LightEntity)
	}
	if brightness.LuxSensorEntity != "sensor.office_lux" {
		t.Errorf("Expected LuxSensorEntity 'sensor.office_lux', got %s", brightness.LuxSensorEntity)
	}
	if brightness.CurrentLux != 450.0 {
		t.Errorf("Expected CurrentLux 450.0, got %f", brightness.CurrentLux)
	}
	if brightness.BrightnessPct != 75 {
		t.Errorf("Expected BrightnessPct 75, got %d", brightness.BrightnessPct)
	}
	if !brightness.IsAdaptive {
		t.Error("Expected IsAdaptive to be true")
	}
}

func TestEnergyTrackerUpdateBaselineLux(t *testing.T) {
	t.Parallel()
	et := NewEnergyTracker()

	et.UpdateBaselineLux("light.office", 25.0)

	state := et.GetState()
	if state.Outputs.BaselineCalibrations == nil {
		t.Fatal("Expected BaselineCalibrations to be initialized")
	}
	calibration, exists := state.Outputs.BaselineCalibrations["light.office"]
	if !exists {
		t.Fatal("Expected calibration for light.office")
	}
	if calibration.LightEntity != "light.office" {
		t.Errorf("Expected LightEntity 'light.office', got %s", calibration.LightEntity)
	}
	if calibration.BaselineLux != 25.0 {
		t.Errorf("Expected BaselineLux 25.0, got %f", calibration.BaselineLux)
	}
	if calibration.LastCalibrationTime.IsZero() {
		t.Error("Expected LastCalibrationTime to be set")
	}
}

// ============================================================================
// Node Status Tracker Tests
// ============================================================================

func TestSensorHealthTrackerUpdateNodeStatuses(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	statuses := []NodeStatusData{
		{EntityID: "sensor.device_1_node_status", DeviceName: "Front Door Lock", Status: "alive"},
		{EntityID: "sensor.device_2_node_status", DeviceName: "Motion Sensor", Status: "asleep"},
	}

	st.UpdateNodeStatuses(statuses)

	state := st.GetState()
	if len(state.Outputs.NodeStatuses) != 2 {
		t.Errorf("Expected 2 node statuses, got %d", len(state.Outputs.NodeStatuses))
	}
}

func TestSensorHealthTrackerUpdateDeadDeviceAlerts(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	alerts := []DeadDeviceAlert{
		{EntityID: "sensor.device_1_node_status", DeviceName: "Front Door Lock", DetectedAt: time.Now()},
	}

	st.UpdateDeadDeviceAlerts(alerts)

	state := st.GetState()
	if len(state.Outputs.DeadDeviceAlerts) != 1 {
		t.Errorf("Expected 1 dead device alert, got %d", len(state.Outputs.DeadDeviceAlerts))
	}
}

func TestSensorHealthTrackerRecordDeadDeviceNotification(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	st.RecordDeadDeviceNotification("sensor.device_1_node_status", "Front Door Lock", "Device is not responding")

	state := st.GetState()
	if state.Outputs.LastDeadDeviceNotification == nil {
		t.Fatal("Expected LastDeadDeviceNotification to be set")
	}
	if state.Outputs.LastDeadDeviceNotification.EntityID != "sensor.device_1_node_status" {
		t.Errorf("Expected EntityID 'sensor.device_1_node_status', got %s", state.Outputs.LastDeadDeviceNotification.EntityID)
	}
	if state.Outputs.LastDeadDeviceNotification.DeviceName != "Front Door Lock" {
		t.Errorf("Expected DeviceName 'Front Door Lock', got %s", state.Outputs.LastDeadDeviceNotification.DeviceName)
	}
}

func TestSensorHealthTrackerRecordDeviceRecoveryNotification(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	st.RecordDeviceRecoveryNotification("sensor.device_1_node_status", "Front Door Lock", "Device is back online")

	state := st.GetState()
	if state.Outputs.LastDeviceRecoveryNotification == nil {
		t.Fatal("Expected LastDeviceRecoveryNotification to be set")
	}
	if state.Outputs.LastDeviceRecoveryNotification.EntityID != "sensor.device_1_node_status" {
		t.Errorf("Expected EntityID 'sensor.device_1_node_status', got %s", state.Outputs.LastDeviceRecoveryNotification.EntityID)
	}
	if state.Outputs.LastDeviceRecoveryNotification.DeviceName != "Front Door Lock" {
		t.Errorf("Expected DeviceName 'Front Door Lock', got %s", state.Outputs.LastDeviceRecoveryNotification.DeviceName)
	}
}

func TestSensorHealthTrackerGetStateDeepCopiesNodeStatuses(t *testing.T) {
	t.Parallel()
	st := NewSensorHealthTracker()

	statuses := []NodeStatusData{
		{EntityID: "sensor.device_1_node_status", DeviceName: "Front Door Lock", Status: "alive"},
	}
	st.UpdateNodeStatuses(statuses)

	state1 := st.GetState()
	state2 := st.GetState()

	// Modifying state1 should not affect state2
	if len(state1.Outputs.NodeStatuses) > 0 {
		state1.Outputs.NodeStatuses[0].Status = "modified"
	}

	if state2.Outputs.NodeStatuses[0].Status == "modified" {
		t.Error("Expected GetState to return a deep copy")
	}
}
