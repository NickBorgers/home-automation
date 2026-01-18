package music

// =============================================================================
// PLAYBACK HEALTH MONITOR TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate that the playback health monitor correctly detects
// auto-pause events and attempts recovery. The monitor is designed to catch
// Sonos speakers that briefly show "playing" then auto-pause.
//
// BACKGROUND:
// Production evidence showed Sonos auto-pausing within 2 minutes of verified
// playback start:
//   09:53:40 - paused -> playing   (verification passed here)
//   09:55:39 - playing -> paused   (auto-paused ~2 min later, undetected)
//
// The health monitor polls speaker state after verification and attempts
// a single recovery via media_play if auto-pause is detected.
//
// =============================================================================

import (
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// =============================================================================
// TEST: Monitor Detects Unexpected Pause and Attempts Recovery
// =============================================================================
//
// SCENARIO:
// - Playback starts and verification passes
// - Health monitor is started
// - Speaker transitions from "playing" to "paused" (auto-pause)
// - Monitor detects the pause and sends media_play recovery command
//
// EXPECTED BEHAVIOR:
// - Monitor logs detection of auto-pause
// - Recovery command (media_play) is sent
// - Monitor exits after single recovery attempt
func TestPlaybackMonitor_DetectsUnexpectedPause(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 15},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	advancingTime := &advancingTimeProvider{currentTime: fixedTime}

	// Create manager without calling Start() to avoid triggering playback
	manager := NewManager(mockClient, stateManager, config, logger, false, advancingTime, nil)

	// Use a channel to coordinate between sleep and test
	sleepDone := make(chan struct{}, 10)
	manager.SetSleepFunc(func(d time.Duration) {
		advancingTime.Advance(d)
		select {
		case sleepDone <- struct{}{}:
		default:
		}
	})

	// Set up state sequence: playing (first poll) -> paused (second poll, triggers detection)
	// Then paused again for confirmation, then paused for recovery check (stays paused = recovery fails)
	mockClient.SetStateSequence("media_player.kitchen", []string{"playing", "paused", "paused", "paused"})
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start the playback monitor directly (no manager.Start() needed)
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for monitor to complete (it should detect pause and attempt recovery)
	// The monitor will sleep multiple times: poll interval, recovery delay, recovery delay again
	for i := 0; i < 5; i++ {
		select {
		case <-sleepDone:
		case <-time.After(500 * time.Millisecond):
		}
	}

	// Give goroutine time to finish
	time.Sleep(100 * time.Millisecond)

	// Verify auto-pause was logged
	autoPauseDetected := false
	recoveryAttempted := false
	for _, entry := range logs.All() {
		if entry.Message == "Auto-pause detected during health monitoring" {
			autoPauseDetected = true
		}
		if entry.Message == "Attempting playback recovery after auto-pause" {
			recoveryAttempted = true
		}
	}

	assert.True(t, autoPauseDetected, "Auto-pause should be detected and logged")
	assert.True(t, recoveryAttempted, "Recovery should be attempted")

	// Verify media_play command was sent
	serviceCalls := mockClient.GetServiceCalls()
	mediaPlayCalled := false
	for _, call := range serviceCalls {
		if call.Domain == "media_player" && call.Service == "media_play" {
			mediaPlayCalled = true
			break
		}
	}
	assert.True(t, mediaPlayCalled, "media_play recovery command should be sent")
}

// =============================================================================
// TEST: Recovery Success - Monitor Records Success and Exits
// =============================================================================
func TestPlaybackMonitor_RecoverySucceeds(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 15},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	advancingTime := &advancingTimeProvider{currentTime: fixedTime}

	// Create manager without calling Start()
	manager := NewManager(mockClient, stateManager, config, logger, false, advancingTime, nil)

	// Use a channel to coordinate between sleep and test
	sleepDone := make(chan struct{}, 10)
	manager.SetSleepFunc(func(d time.Duration) {
		advancingTime.Advance(d)
		select {
		case sleepDone <- struct{}{}:
		default:
		}
	})

	// Set up state sequence: playing (first poll) -> paused (triggers detection)
	// -> paused (confirmation check) -> playing (recovery verification succeeds)
	mockClient.SetStateSequence("media_player.kitchen", []string{"playing", "paused", "paused", "playing"})
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start the monitor directly
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for monitor to complete
	for i := 0; i < 5; i++ {
		select {
		case <-sleepDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond)

	// Verify success was logged
	recoverySuccessful := false
	for _, entry := range logs.All() {
		if entry.Message == "Playback recovery successful" {
			recoverySuccessful = true
			break
		}
	}
	assert.True(t, recoverySuccessful, "Recovery success should be logged")

	// Verify shadow state was updated
	shadow := manager.GetShadowState()
	if shadow.Outputs.PlaybackHealth != nil {
		assert.True(t, shadow.Outputs.PlaybackHealth.RecoveryAttempted, "RecoveryAttempted should be true")
		assert.Equal(t, "success", shadow.Outputs.PlaybackHealth.RecoveryResult, "RecoveryResult should be 'success'")
	}
}

// =============================================================================
// TEST: Recovery Fails - Monitor Stops to Avoid Fighting Human Pause
// =============================================================================
func TestPlaybackMonitor_RecoveryFails_StopsMonitoring(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 15},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	advancingTime := &advancingTimeProvider{currentTime: fixedTime}

	// Create manager without calling Start()
	manager := NewManager(mockClient, stateManager, config, logger, false, advancingTime, nil)

	// Use a channel to coordinate between sleep and test
	sleepDone := make(chan struct{}, 10)
	manager.SetSleepFunc(func(d time.Duration) {
		advancingTime.Advance(d)
		select {
		case sleepDone <- struct{}{}:
		default:
		}
	})

	// Set up state sequence: playing (first poll) -> paused (triggers detection)
	// -> paused (confirmation) -> paused (recovery fails)
	mockClient.SetStateSequence("media_player.kitchen", []string{"playing", "paused", "paused", "paused"})
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start monitor directly
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for monitor to complete
	for i := 0; i < 5; i++ {
		select {
		case <-sleepDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	time.Sleep(100 * time.Millisecond)

	// Verify failure was logged with appropriate message
	recoveryFailed := false
	for _, entry := range logs.All() {
		if entry.Message == "Playback recovery failed - stopping monitor to avoid fighting human pause" {
			recoveryFailed = true
			break
		}
	}
	assert.True(t, recoveryFailed, "Recovery failure should be logged with human pause message")

	// Verify shadow state shows failed recovery
	shadow := manager.GetShadowState()
	if shadow.Outputs.PlaybackHealth != nil {
		assert.True(t, shadow.Outputs.PlaybackHealth.RecoveryAttempted, "RecoveryAttempted should be true")
		assert.Equal(t, "failed", shadow.Outputs.PlaybackHealth.RecoveryResult, "RecoveryResult should be 'failed'")
		assert.False(t, shadow.Outputs.PlaybackHealth.IsMonitoring, "IsMonitoring should be false after failure")
	}
}

// =============================================================================
// TEST: Monitor Cancelled on New Playback
// =============================================================================
func TestPlaybackMonitor_CancelledOnNewPlayback(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 15},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	// Create manager without calling Start()
	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)

	// Use a blocking channel to make the first monitor wait in sleep
	firstMonitorStarted := make(chan struct{})
	manager.SetSleepFunc(func(d time.Duration) {
		select {
		case firstMonitorStarted <- struct{}{}:
		default:
		}
		// Block briefly to allow cancellation to be tested
		time.Sleep(10 * time.Millisecond)
	})

	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start first monitor
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for first monitor to start sleeping
	select {
	case <-firstMonitorStarted:
	case <-time.After(500 * time.Millisecond):
	}

	// Start second monitor (should cancel first)
	manager.startPlaybackMonitor("media_player.kitchen", "evening")

	time.Sleep(100 * time.Millisecond)

	// Verify cancellation was logged
	cancelled := false
	for _, entry := range logs.All() {
		if entry.Message == "Cancelling previous playback health monitor for new playback" {
			cancelled = true
			break
		}
	}
	assert.True(t, cancelled, "Previous monitor should be cancelled when new playback starts")
}

// =============================================================================
// TEST: Shadow State Updated During Monitoring
// =============================================================================
func TestPlaybackMonitor_ShadowStateUpdated(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 15},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	// Create manager without calling Start()
	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider, nil)

	// Use a channel to block the monitor goroutine so we can inspect state
	sleepStarted := make(chan struct{}, 1)
	manager.SetSleepFunc(func(d time.Duration) {
		select {
		case sleepStarted <- struct{}{}:
		default:
		}
		// Block to keep monitor alive for inspection
		time.Sleep(50 * time.Millisecond)
	})

	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start monitor and check shadow state immediately
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for monitor to start its first sleep
	select {
	case <-sleepStarted:
	case <-time.After(500 * time.Millisecond):
	}

	// Verify shadow state is populated
	shadow := manager.GetShadowState()
	require.NotNil(t, shadow.Outputs.PlaybackHealth, "PlaybackHealth should be populated")
	assert.True(t, shadow.Outputs.PlaybackHealth.IsMonitoring, "IsMonitoring should be true")
	assert.Equal(t, "media_player.kitchen", shadow.Outputs.PlaybackHealth.LeadSpeaker)
	assert.Equal(t, "day", shadow.Outputs.PlaybackHealth.MusicType)
	assert.Equal(t, "playing", shadow.Outputs.PlaybackHealth.LastSpeakerState)
	assert.False(t, shadow.Outputs.PlaybackHealth.RecoveryAttempted)

	// Cancel and verify state update
	manager.cancelPlaybackMonitor()
	time.Sleep(100 * time.Millisecond)

	shadow = manager.GetShadowState()
	// After cancellation, the goroutine should have set IsMonitoring to false
	if shadow.Outputs.PlaybackHealth != nil {
		assert.False(t, shadow.Outputs.PlaybackHealth.IsMonitoring, "IsMonitoring should be false after cancellation")
	}
}

// =============================================================================
// HELPER: Advancing time provider for controlled time progression
// =============================================================================

type advancingTimeProvider struct {
	mu          sync.Mutex
	currentTime time.Time
}

func (p *advancingTimeProvider) Now() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentTime
}

func (p *advancingTimeProvider) Advance(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentTime = p.currentTime.Add(d)
}
