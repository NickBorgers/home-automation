package sleephygiene

import (
	"testing"
	"time"

	"homeautomation/internal/config"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// TestSleepHygieneShadowState_CaptureInputs tests input capture
func TestSleepHygieneShadowState_CaptureInputs(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)

	// Set test state values
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isEveryoneAsleep", false)
	stateManager.SetBool("isFadeOutInProgress", false)
	stateManager.SetBool("isNickHome", true)
	stateManager.SetBool("isCarolineHome", true)

	mockConfig := &config.Loader{}
	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Capture inputs
	inputs := manager.captureCurrentInputs()

	// Verify inputs captured
	if inputs["isMasterAsleep"] != true {
		t.Errorf("Expected isMasterAsleep to be true, got %v", inputs["isMasterAsleep"])
	}
	if inputs["musicPlaybackType"] != "sleep" {
		t.Errorf("Expected musicPlaybackType to be 'sleep', got %v", inputs["musicPlaybackType"])
	}
	if inputs["isAnyoneHome"] != true {
		t.Errorf("Expected isAnyoneHome to be true, got %v", inputs["isAnyoneHome"])
	}
}

// TestSleepHygieneShadowState_RecordAction tests action recording
func TestSleepHygieneShadowState_RecordAction(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Set some state
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Record an action
	manager.recordAction("begin_wake", "Starting wake sequence", "test_trigger")

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify action recorded
	if shadowState.Outputs.LastActionType != "begin_wake" {
		t.Errorf("Expected LastActionType to be 'begin_wake', got %s", shadowState.Outputs.LastActionType)
	}
	if shadowState.Outputs.LastActionReason != "Starting wake sequence" {
		t.Errorf("Expected LastActionReason to be 'Starting wake sequence', got %s", shadowState.Outputs.LastActionReason)
	}

	// Verify inputs at last action were captured
	if shadowState.Inputs.AtLastAction["isMasterAsleep"] != true {
		t.Errorf("Expected AtLastAction.isMasterAsleep to be true, got %v", shadowState.Inputs.AtLastAction["isMasterAsleep"])
	}
}

// TestSleepHygieneShadowState_WakeSequenceStatus tests wake sequence status tracking
func TestSleepHygieneShadowState_WakeSequenceStatus(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Initial state should be inactive
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.WakeSequenceStatus != "inactive" {
		t.Errorf("Expected initial WakeSequenceStatus to be 'inactive', got %s", shadowState.Outputs.WakeSequenceStatus)
	}

	// Update to begin_wake
	manager.shadowTracker.UpdateWakeSequenceStatus("begin_wake")
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.WakeSequenceStatus != "begin_wake" {
		t.Errorf("Expected WakeSequenceStatus to be 'begin_wake', got %s", shadowState.Outputs.WakeSequenceStatus)
	}

	// Update to wake_in_progress
	manager.shadowTracker.UpdateWakeSequenceStatus("wake_in_progress")
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.WakeSequenceStatus != "wake_in_progress" {
		t.Errorf("Expected WakeSequenceStatus to be 'wake_in_progress', got %s", shadowState.Outputs.WakeSequenceStatus)
	}

	// Update to complete
	manager.shadowTracker.UpdateWakeSequenceStatus("complete")
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.WakeSequenceStatus != "complete" {
		t.Errorf("Expected WakeSequenceStatus to be 'complete', got %s", shadowState.Outputs.WakeSequenceStatus)
	}
}

// TestSleepHygieneShadowState_FadeOutProgress tests fade-out progress tracking
func TestSleepHygieneShadowState_FadeOutProgress(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Record fade out start
	manager.shadowTracker.RecordFadeOutStart("media_player.bedroom", 60)

	shadowState := manager.GetShadowState()

	// Verify fade out recorded
	fadeOut, exists := shadowState.Outputs.FadeOutProgress["media_player.bedroom"]
	if !exists {
		t.Fatal("Expected fade out progress for media_player.bedroom to exist")
	}
	if fadeOut.SpeakerEntityID != "media_player.bedroom" {
		t.Errorf("Expected SpeakerEntityID to be 'media_player.bedroom', got %s", fadeOut.SpeakerEntityID)
	}
	if fadeOut.StartVolume != 60 {
		t.Errorf("Expected StartVolume to be 60, got %d", fadeOut.StartVolume)
	}
	if fadeOut.CurrentVolume != 60 {
		t.Errorf("Expected CurrentVolume to be 60, got %d", fadeOut.CurrentVolume)
	}
	if !fadeOut.IsActive {
		t.Error("Expected IsActive to be true")
	}

	// Update progress
	manager.shadowTracker.UpdateFadeOutProgress("media_player.bedroom", 30)
	shadowState = manager.GetShadowState()

	fadeOut = shadowState.Outputs.FadeOutProgress["media_player.bedroom"]
	if fadeOut.CurrentVolume != 30 {
		t.Errorf("Expected CurrentVolume to be 30, got %d", fadeOut.CurrentVolume)
	}
	if !fadeOut.IsActive {
		t.Error("Expected IsActive to still be true")
	}

	// Complete fade out (volume 0)
	manager.shadowTracker.UpdateFadeOutProgress("media_player.bedroom", 0)
	shadowState = manager.GetShadowState()

	fadeOut = shadowState.Outputs.FadeOutProgress["media_player.bedroom"]
	if fadeOut.CurrentVolume != 0 {
		t.Errorf("Expected CurrentVolume to be 0, got %d", fadeOut.CurrentVolume)
	}
	if fadeOut.IsActive {
		t.Error("Expected IsActive to be false when volume reaches 0")
	}

	// Clear fade out progress
	manager.shadowTracker.ClearFadeOutProgress()
	shadowState = manager.GetShadowState()

	if len(shadowState.Outputs.FadeOutProgress) != 0 {
		t.Errorf("Expected FadeOutProgress to be empty, got %d entries", len(shadowState.Outputs.FadeOutProgress))
	}
}

// TestSleepHygieneShadowState_TTSAnnouncement tests TTS announcement recording
func TestSleepHygieneShadowState_TTSAnnouncement(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Initially no announcement
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.LastTTSAnnouncement != nil {
		t.Error("Expected LastTTSAnnouncement to be nil initially")
	}

	// Record TTS announcement
	manager.shadowTracker.RecordTTSAnnouncement("Time to cuddle", "media_player.bedroom")

	shadowState = manager.GetShadowState()

	// Verify announcement recorded
	if shadowState.Outputs.LastTTSAnnouncement == nil {
		t.Fatal("Expected LastTTSAnnouncement to not be nil")
	}
	if shadowState.Outputs.LastTTSAnnouncement.Message != "Time to cuddle" {
		t.Errorf("Expected Message to be 'Time to cuddle', got %s", shadowState.Outputs.LastTTSAnnouncement.Message)
	}
	if shadowState.Outputs.LastTTSAnnouncement.Speaker != "media_player.bedroom" {
		t.Errorf("Expected Speaker to be 'media_player.bedroom', got %s", shadowState.Outputs.LastTTSAnnouncement.Speaker)
	}
	if shadowState.Outputs.LastTTSAnnouncement.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

// TestSleepHygieneShadowState_Reminders tests reminder recording
func TestSleepHygieneShadowState_Reminders(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Initially no reminders
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.StopScreensReminder != nil {
		t.Error("Expected StopScreensReminder to be nil initially")
	}
	if shadowState.Outputs.GoToBedReminder != nil {
		t.Error("Expected GoToBedReminder to be nil initially")
	}

	// Record stop screens reminder
	manager.shadowTracker.RecordStopScreensReminder()
	shadowState = manager.GetShadowState()

	if shadowState.Outputs.StopScreensReminder == nil {
		t.Fatal("Expected StopScreensReminder to not be nil")
	}
	if !shadowState.Outputs.StopScreensReminder.Triggered {
		t.Error("Expected StopScreensReminder.Triggered to be true")
	}
	if shadowState.Outputs.StopScreensReminder.Timestamp.IsZero() {
		t.Error("Expected StopScreensReminder.Timestamp to be set")
	}

	// Record go to bed reminder
	manager.shadowTracker.RecordGoToBedReminder()
	shadowState = manager.GetShadowState()

	if shadowState.Outputs.GoToBedReminder == nil {
		t.Fatal("Expected GoToBedReminder to not be nil")
	}
	if !shadowState.Outputs.GoToBedReminder.Triggered {
		t.Error("Expected GoToBedReminder.Triggered to be true")
	}
	if shadowState.Outputs.GoToBedReminder.Timestamp.IsZero() {
		t.Error("Expected GoToBedReminder.Timestamp to be set")
	}
}

// TestSleepHygieneShadowState_GetShadowState tests getting shadow state
func TestSleepHygieneShadowState_GetShadowState(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Set some state
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetString("musicPlaybackType", "sleep")

	// Update shadow state
	manager.updateShadowInputs()

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify metadata
	if shadowState.Plugin != "sleephygiene" {
		t.Errorf("Expected Plugin to be 'sleephygiene', got %s", shadowState.Plugin)
	}
	if shadowState.Metadata.PluginName != "sleephygiene" {
		t.Errorf("Expected PluginName to be 'sleephygiene', got %s", shadowState.Metadata.PluginName)
	}

	// Verify inputs
	if shadowState.Inputs.Current["isMasterAsleep"] != true {
		t.Errorf("Expected Current.isMasterAsleep to be true, got %v", shadowState.Inputs.Current["isMasterAsleep"])
	}
	if shadowState.Inputs.Current["musicPlaybackType"] != "sleep" {
		t.Errorf("Expected Current.musicPlaybackType to be 'sleep', got %v", shadowState.Inputs.Current["musicPlaybackType"])
	}

	// Verify outputs
	if shadowState.Outputs.WakeSequenceStatus != "inactive" {
		t.Errorf("Expected WakeSequenceStatus to be 'inactive', got %s", shadowState.Outputs.WakeSequenceStatus)
	}
	if shadowState.Outputs.FadeOutProgress == nil {
		t.Error("Expected FadeOutProgress to not be nil")
	}
}

// TestSleepHygieneShadowState_ConcurrentAccess tests thread-safe concurrent access
func TestSleepHygieneShadowState_ConcurrentAccess(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Run concurrent operations
	done := make(chan bool)
	numGoroutines := 10

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = manager.GetShadowState()
			}
			done <- true
		}()
	}

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				manager.shadowTracker.UpdateWakeSequenceStatus("test")
				manager.shadowTracker.RecordFadeOutStart("media_player.test", 50)
				manager.shadowTracker.UpdateFadeOutProgress("media_player.test", 25)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines*2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent operations to complete")
		}
	}
}

// TestSleepHygieneShadowState_InterfaceImplementation tests that shadow state implements PluginShadowState
func TestSleepHygieneShadowState_InterfaceImplementation(t *testing.T) {
	shadowState := shadowstate.NewSleepHygieneShadowState()

	// Verify it implements the interface
	var _ shadowstate.PluginShadowState = shadowState

	// Test interface methods
	currentInputs := shadowState.GetCurrentInputs()
	if currentInputs == nil {
		t.Error("Expected GetCurrentInputs to return non-nil map")
	}

	lastActionInputs := shadowState.GetLastActionInputs()
	if lastActionInputs == nil {
		t.Error("Expected GetLastActionInputs to return non-nil map")
	}

	outputs := shadowState.GetOutputs()
	if outputs == nil {
		t.Error("Expected GetOutputs to return non-nil value")
	}

	metadata := shadowState.GetMetadata()
	if metadata.PluginName != "sleephygiene" {
		t.Errorf("Expected PluginName to be 'sleephygiene', got %s", metadata.PluginName)
	}
}

// TestSleepHygieneShadowState_CancelWake tests cancel wake shadow state tracking
func TestSleepHygieneShadowState_CancelWake(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Set up wake sequence state
	stateManager.SetString("musicPlaybackType", "wakeup")
	manager.shadowTracker.UpdateWakeSequenceStatus("wake_in_progress")
	manager.shadowTracker.RecordFadeOutStart("media_player.bedroom", 50)

	// Simulate bedroom lights turning off during wake sequence
	manager.handleBedroomLightsOff("off")

	shadowState := manager.GetShadowState()

	// Verify cancel wake action recorded
	if shadowState.Outputs.LastActionType != "cancel_wake" {
		t.Errorf("Expected LastActionType to be 'cancel_wake', got %s", shadowState.Outputs.LastActionType)
	}

	// Verify wake sequence status reset to inactive
	if shadowState.Outputs.WakeSequenceStatus != "inactive" {
		t.Errorf("Expected WakeSequenceStatus to be 'inactive', got %s", shadowState.Outputs.WakeSequenceStatus)
	}

	// Verify fade-out progress cleared
	if len(shadowState.Outputs.FadeOutProgress) != 0 {
		t.Errorf("Expected FadeOutProgress to be empty after cancel, got %d entries", len(shadowState.Outputs.FadeOutProgress))
	}
}

// TestSleepHygieneShadowState_BedroomLightsChange tests bedroom lights change handler
func TestSleepHygieneShadowState_BedroomLightsChange(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Set up wake sequence state
	stateManager.SetString("musicPlaybackType", "wakeup")
	manager.shadowTracker.UpdateWakeSequenceStatus("wake_in_progress")

	// Simulate bedroom lights state change
	newState := &ha.State{
		EntityID: "light.master_bedroom",
		State:    "off",
	}

	manager.handleBedroomLightsChange("light.master_bedroom", nil, newState)

	shadowState := manager.GetShadowState()

	// Verify cancel wake was triggered
	if shadowState.Outputs.LastActionType != "cancel_wake" {
		t.Errorf("Expected LastActionType to be 'cancel_wake', got %s", shadowState.Outputs.LastActionType)
	}
}

// TestSleepHygieneShadowState_BedroomLightsNoCancel tests that cancel wake doesn't trigger inappropriately
func TestSleepHygieneShadowState_BedroomLightsNoCancel(t *testing.T) {
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	mockConfig := &config.Loader{}

	manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Set up non-wakeup music
	stateManager.SetString("musicPlaybackType", "sleep")

	// Record an initial action
	manager.recordAction("test_action", "test reason", "test_trigger")

	// Simulate bedroom lights turning off (should not cancel wake)
	manager.handleBedroomLightsOff("off")

	shadowState := manager.GetShadowState()

	// Verify cancel wake was NOT triggered
	if shadowState.Outputs.LastActionType == "cancel_wake" {
		t.Error("Expected cancel_wake to not be triggered when not in wake sequence")
	}
}

// TestSleepHygieneShadowState_HandleGoToBed tests go to bed handler with various conditions
// Shadow state is ALWAYS recorded because sleep music is always started.
// Lights are only flashed when someone is home and not everyone is asleep.
func TestSleepHygieneShadowState_HandleGoToBed(t *testing.T) {
	testCases := []struct {
		name              string
		isAnyoneHome      bool
		isEveryoneAsleep  bool
		shouldFlashLights bool
		setupStateManager func(*state.Manager)
	}{
		{
			name:              "No one home - music starts, no lights",
			isAnyoneHome:      false,
			isEveryoneAsleep:  false,
			shouldFlashLights: false,
			setupStateManager: func(sm *state.Manager) {
				sm.SetBool("isAnyoneHome", false)
				sm.SetBool("isEveryoneAsleep", false)
			},
		},
		{
			name:              "Everyone asleep - music starts, no lights",
			isAnyoneHome:      true,
			isEveryoneAsleep:  true,
			shouldFlashLights: false,
			setupStateManager: func(sm *state.Manager) {
				sm.SetBool("isAnyoneHome", true)
				sm.SetBool("isEveryoneAsleep", true)
			},
		},
		{
			name:              "Someone home and awake - music starts, lights flash",
			isAnyoneHome:      true,
			isEveryoneAsleep:  false,
			shouldFlashLights: true,
			setupStateManager: func(sm *state.Manager) {
				sm.SetBool("isAnyoneHome", true)
				sm.SetBool("isEveryoneAsleep", false)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, zap.NewNop(), false)
			mockConfig := &config.Loader{}

			manager := NewManager(mockClient, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

			// Set up state
			tc.setupStateManager(stateManager)

			// Handle go to bed
			manager.handleGoToBed()

			shadowState := manager.GetShadowState()

			// Shadow state should ALWAYS be recorded (music is always started)
			if shadowState.Outputs.GoToBedReminder == nil {
				t.Error("Expected GoToBedReminder to be set (music always starts)")
			}
			if shadowState.Outputs.LastActionType != "go_to_bed" {
				t.Errorf("Expected LastActionType to be 'go_to_bed', got %s", shadowState.Outputs.LastActionType)
			}

			// Verify action reason reflects whether lights were flashed
			if tc.shouldFlashLights {
				if shadowState.Outputs.LastActionReason != "Starting sleep music and flashing common area lights" {
					t.Errorf("Expected action reason for lights+music, got '%s'", shadowState.Outputs.LastActionReason)
				}
			} else {
				if shadowState.Outputs.LastActionReason != "Starting sleep music (no light flash: conditions not met)" {
					t.Errorf("Expected action reason for music only, got '%s'", shadowState.Outputs.LastActionReason)
				}
			}
		})
	}
}
