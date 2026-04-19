package music

import (
	"context"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// TestMusicShadowState_CaptureInputs tests that all subscribed inputs are captured
func TestMusicShadowState_CaptureInputs(t *testing.T) {
	t.Parallel(
	// Create mock state manager
	)

	stateManager := state.NewManager(nil, zap.NewNop(), true)

	// Create music manager
	mockConfig := &MusicConfig{
		Music: make(map[string]MusicMode),
	}
	manager := NewManager(context.Background(), nil, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Capture inputs (should not fail even if state variables don't exist)
	inputs := manager.captureCurrentInputs()

	// Verify function returns a map (even if empty, it shouldn't panic)
	if inputs == nil {
		t.Fatal("Expected non-nil inputs map")
	}

	// The map might be empty if no state variables are set, which is fine
	// The important thing is that captureCurrentInputs doesn't panic
	// and properly checks for errors when getting values
}

// TestMusicShadowState_RecordAction tests that actions update shadow state correctly
func TestMusicShadowState_RecordAction(t *testing.T) {
	t.Parallel(
	// Create mock state manager
	)

	stateManager := state.NewManager(nil, zap.NewNop(), true)
	stateManager.SetString("dayPhase", "evening")
	stateManager.SetBool("isAnyoneHome", true)

	// Create music manager
	mockConfig := &MusicConfig{
		Music: make(map[string]MusicMode),
	}
	fixedTime := time.Date(2025, 11, 28, 12, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(context.Background(), nil, stateManager, mockConfig, zap.NewNop(), true, timeProvider, nil)

	// Record an action
	manager.updateShadowState("start_playback", "Test playback started", "test_trigger")

	// Verify shadow state was updated
	shadowState := manager.GetShadowState()

	if shadowState.Outputs.LastActionType != "start_playback" {
		t.Errorf("Expected LastActionType='start_playback', got '%s'", shadowState.Outputs.LastActionType)
	}

	if shadowState.Outputs.LastActionReason != "Test playback started" {
		t.Errorf("Expected LastActionReason='Test playback started', got '%s'", shadowState.Outputs.LastActionReason)
	}

	if shadowState.Outputs.LastActionTime != fixedTime {
		t.Errorf("Expected LastActionTime=%v, got %v", fixedTime, shadowState.Outputs.LastActionTime)
	}

	// Verify inputs were captured
	if len(shadowState.Inputs.Current) == 0 {
		t.Error("Expected current inputs to be captured, got empty map")
	}

	if len(shadowState.Inputs.AtLastAction) == 0 {
		t.Error("Expected at-last-action inputs to be captured, got empty map")
	}
}

// TestMusicShadowState_UpdateOutputs tests that output updates work correctly
func TestMusicShadowState_UpdateOutputs(t *testing.T) {
	t.Parallel(
	// Create mock state manager and music manager
	)

	stateManager := state.NewManager(nil, zap.NewNop(), true)
	mockConfig := &MusicConfig{
		Music: make(map[string]MusicMode),
	}
	manager := NewManager(context.Background(), nil, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Create test data
	playlistInfo := &shadowstate.PlaylistInfo{
		URI:       "https://tidal.com/browse/playlist/test123",
		Name:      "Test Playlist",
		MediaType: "music",
	}

	speakers := []shadowstate.SpeakerState{
		{
			PlayerName:    "Living Room",
			Volume:        30,
			BaseVolume:    25,
			DefaultVolume: 35,
			IsLeader:      true,
		},
		{
			PlayerName:    "Kitchen",
			Volume:        25,
			BaseVolume:    20,
			DefaultVolume: 30,
			IsLeader:      false,
		},
	}

	// Update outputs
	manager.updateShadowOutputs("evening", playlistInfo, speakers, nil)

	// Verify shadow state
	shadowState := manager.GetShadowState()

	if shadowState.Outputs.CurrentMode != "evening" {
		t.Errorf("Expected CurrentMode='evening', got '%s'", shadowState.Outputs.CurrentMode)
	}

	if shadowState.Outputs.ActivePlaylist.URI != "https://tidal.com/browse/playlist/test123" {
		t.Errorf("Expected playlist URI='https://tidal.com/browse/playlist/test123', got '%s'", shadowState.Outputs.ActivePlaylist.URI)
	}

	if shadowState.Outputs.ActivePlaylist.Name != "Test Playlist" {
		t.Errorf("Expected playlist Name='Test Playlist', got '%s'", shadowState.Outputs.ActivePlaylist.Name)
	}

	if len(shadowState.Outputs.SpeakerGroup) != 2 {
		t.Errorf("Expected 2 speakers, got %d", len(shadowState.Outputs.SpeakerGroup))
	}

	if shadowState.Outputs.SpeakerGroup[0].PlayerName != "Living Room" {
		t.Errorf("Expected first speaker='Living Room', got '%s'", shadowState.Outputs.SpeakerGroup[0].PlayerName)
	}

	if !shadowState.Outputs.SpeakerGroup[0].IsLeader {
		t.Error("Expected first speaker to be leader")
	}

	if shadowState.Outputs.SpeakerGroup[1].IsLeader {
		t.Error("Expected second speaker to NOT be leader")
	}
}

// TestMusicShadowState_GetShadowState tests that GetShadowState returns accurate snapshot
func TestMusicShadowState_GetShadowState(t *testing.T) {
	t.Parallel(
	// Create mock state manager and music manager
	)

	stateManager := state.NewManager(nil, zap.NewNop(), true)
	stateManager.SetString("dayPhase", "morning")

	mockConfig := &MusicConfig{
		Music: make(map[string]MusicMode),
	}
	manager := NewManager(context.Background(), nil, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Record some state
	manager.updateShadowState("test_action", "Test reason", "test_trigger")

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify it's a valid shadow state
	if shadowState == nil {
		t.Fatal("Expected non-nil shadow state")
	}

	if shadowState.Plugin != "music" {
		t.Errorf("Expected plugin='music', got '%s'", shadowState.Plugin)
	}

	if shadowState.Metadata.PluginName != "music" {
		t.Errorf("Expected PluginName='music', got '%s'", shadowState.Metadata.PluginName)
	}

	// Verify maps are initialized
	if shadowState.Inputs.Current == nil {
		t.Error("Expected Current inputs map to be initialized")
	}

	if shadowState.Inputs.AtLastAction == nil {
		t.Error("Expected AtLastAction inputs map to be initialized")
	}

	if shadowState.Outputs.SpeakerGroup == nil {
		t.Error("Expected SpeakerGroup to be initialized")
	}

	if shadowState.Outputs.PlaylistRotation == nil {
		t.Error("Expected PlaylistRotation map to be initialized")
	}
}

// TestMusicShadowState_ConcurrentAccess tests thread safety with concurrent access
func TestMusicShadowState_ConcurrentAccess(t *testing.T) {
	t.Parallel(
	// Create mock state manager and music manager
	)

	stateManager := state.NewManager(nil, zap.NewNop(), true)
	mockConfig := &MusicConfig{
		Music: make(map[string]MusicMode),
	}
	manager := NewManager(context.Background(), nil, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Run concurrent operations
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer goroutine - updates shadow state
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			manager.updateShadowState("test_action", "Concurrent test", "concurrent_test")
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Reader goroutine - reads shadow state
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = manager.GetShadowState()
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Wait for both goroutines to complete with timeout
	waitCh := make(chan struct{})
	go func() { wg.Wait(); close(waitCh) }()

	select {
	case <-waitCh:
		// success
	case <-time.After(10 * time.Second):
		t.Fatal("Timeout waiting for concurrent operations")
	}

	// If we get here without a race condition, test passes
	// (run with -race flag to detect races)
}

// TestMusicShadowState_PlaylistRotation tests that playlist rotation is tracked
func TestMusicShadowState_PlaylistRotation(t *testing.T) {
	t.Parallel(
	// Create mock state manager and music manager
	)

	stateManager := state.NewManager(nil, zap.NewNop(), true)
	mockConfig := &MusicConfig{
		Music: make(map[string]MusicMode),
	}
	manager := NewManager(context.Background(), nil, stateManager, mockConfig, zap.NewNop(), true, nil, nil)

	// Set some playlist rotation state
	manager.playlistNumbers["morning"] = 2
	manager.playlistNumbers["evening"] = 5

	// Update shadow outputs (this should copy the rotation state)
	manager.updateShadowOutputs("", nil, nil, nil)

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify playlist rotation was captured
	if shadowState.Outputs.PlaylistRotation["morning"] != 2 {
		t.Errorf("Expected morning rotation=2, got %d", shadowState.Outputs.PlaylistRotation["morning"])
	}

	if shadowState.Outputs.PlaylistRotation["evening"] != 5 {
		t.Errorf("Expected evening rotation=5, got %d", shadowState.Outputs.PlaylistRotation["evening"])
	}
}

// TestMusicShadowState_InterfaceImplementation tests that MusicShadowState implements PluginShadowState
func TestMusicShadowState_InterfaceImplementation(t *testing.T) {
	t.Parallel()
	shadowState := shadowstate.NewMusicShadowState()

	// Verify interface methods work
	_ = shadowState.GetCurrentInputs()
	_ = shadowState.GetLastActionInputs()
	_ = shadowState.GetOutputs()
	_ = shadowState.GetMetadata()

	// If this compiles, the interface is implemented correctly
}

// TestMusicShadowState_CaptureInputs_IncludesMuteConditionVariables verifies that
// captureCurrentInputs includes variables referenced in leave_muted_if conditions.
//
// Regression test for GitHub issue #998: when isTVPlaying=true, the shadow state
// inputs did not include isTVPlaying, making it impossible to diagnose why speakers
// with leave_muted_if: isTVPlaying=true were not being muted.
func TestMusicShadowState_CaptureInputs_IncludesMuteConditionVariables(t *testing.T) {
	t.Parallel()

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, zap.NewNop(), false)
	_ = stateManager.SetBool("isTVPlaying", true)

	// Config with isTVPlaying as a leave_muted_if condition
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{
						PlayerName:   "Front Room",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{},
					},
					{
						PlayerName: "Kitchen",
						BaseVolume: 9,
						LeaveMutedIf: []MuteCondition{
							{Variable: "isTVPlaying", Value: true},
						},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "https://tidal.com/browse/playlist/test", MediaType: "tidal", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, zap.NewNop(), true, nil, nil)

	inputs := manager.captureCurrentInputs()

	if _, exists := inputs["isTVPlaying"]; !exists {
		t.Error("captureCurrentInputs() must include isTVPlaying when it is referenced in a leave_muted_if condition. " +
			"Without this, shadow state inputs omit isTVPlaying and operators cannot diagnose muting failures (issue #998).")
	}
	if val, exists := inputs["isTVPlaying"]; exists {
		boolVal, ok := val.(bool)
		if !ok || !boolVal {
			t.Errorf("Expected isTVPlaying=true in shadow inputs, got %v (type %T)", val, val)
		}
	}
}
