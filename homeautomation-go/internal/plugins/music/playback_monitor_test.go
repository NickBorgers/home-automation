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
// TEST SYNCHRONIZATION:
// These tests use deterministic synchronization via:
// - sleepDone channel: signals when the mock sleep function is called
// - monitorDone channel: signals when the monitor goroutine exits (via callback)
// This avoids time-based waits (time.Sleep, time.After) that can cause flakiness.
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

	// Channel to track sleep calls for coordinating test with monitor goroutine
	sleepDone := make(chan struct{}, 10)
	manager.SetSleepFunc(func(d time.Duration) {
		advancingTime.Advance(d)
		select {
		case sleepDone <- struct{}{}:
		default:
		}
	})

	// Channel to signal when monitor goroutine exits (deterministic completion)
	monitorDone := make(chan struct{})
	manager.SetMonitorDoneCallback(func() {
		close(monitorDone)
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

	// Wait for monitor to complete - uses deterministic callback instead of time-based wait
	<-monitorDone

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

	// Channel to track sleep calls
	sleepDone := make(chan struct{}, 10)
	manager.SetSleepFunc(func(d time.Duration) {
		advancingTime.Advance(d)
		select {
		case sleepDone <- struct{}{}:
		default:
		}
	})

	// Channel to signal when monitor goroutine exits (deterministic completion)
	monitorDone := make(chan struct{})
	manager.SetMonitorDoneCallback(func() {
		close(monitorDone)
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

	// Wait for monitor to complete - deterministic callback
	<-monitorDone

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

	// Channel to track sleep calls
	sleepDone := make(chan struct{}, 10)
	manager.SetSleepFunc(func(d time.Duration) {
		advancingTime.Advance(d)
		select {
		case sleepDone <- struct{}{}:
		default:
		}
	})

	// Channel to signal when monitor goroutine exits (deterministic completion)
	monitorDone := make(chan struct{})
	manager.SetMonitorDoneCallback(func() {
		close(monitorDone)
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

	// Wait for monitor to complete - deterministic callback
	<-monitorDone

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

	// Channel to signal when first monitor starts sleeping (so we know it's active)
	firstMonitorStarted := make(chan struct{}, 1)
	// Channel to control when sleep completes (allows us to orchestrate the test)
	sleepGate := make(chan struct{})
	manager.SetSleepFunc(func(d time.Duration) {
		select {
		case firstMonitorStarted <- struct{}{}:
		default:
		}
		// Block until test releases the gate
		<-sleepGate
	})

	// Track monitor completions - we expect 2 monitors (first cancelled, second runs to completion)
	var monitorCompletions int
	var completionMu sync.Mutex
	firstMonitorDone := make(chan struct{})
	manager.SetMonitorDoneCallback(func() {
		completionMu.Lock()
		monitorCompletions++
		if monitorCompletions == 1 {
			close(firstMonitorDone)
		}
		completionMu.Unlock()
	})

	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start first monitor
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for first monitor to start sleeping
	<-firstMonitorStarted

	// Start second monitor (should cancel first)
	manager.startPlaybackMonitor("media_player.kitchen", "evening")

	// Release the sleep gate so monitors can proceed
	close(sleepGate)

	// Wait for first monitor to exit (via cancellation)
	<-firstMonitorDone

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

	// Channel to signal when monitor starts sleeping (so we know shadow state is set)
	sleepStarted := make(chan struct{}, 1)
	// Channel to control when sleep completes (allows state inspection while monitor is active)
	sleepGate := make(chan struct{})
	manager.SetSleepFunc(func(d time.Duration) {
		select {
		case sleepStarted <- struct{}{}:
		default:
		}
		// Block until test releases the gate
		<-sleepGate
	})

	// Channel to signal when monitor goroutine exits
	monitorDone := make(chan struct{})
	manager.SetMonitorDoneCallback(func() {
		close(monitorDone)
	})

	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "playing",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen"},
	})

	// Start monitor and check shadow state immediately
	manager.startPlaybackMonitor("media_player.kitchen", "day")

	// Wait for monitor to start its first sleep - this ensures shadow state is populated
	<-sleepStarted

	// Verify shadow state is populated while monitor is active
	shadow := manager.GetShadowState()
	require.NotNil(t, shadow.Outputs.PlaybackHealth, "PlaybackHealth should be populated")
	assert.True(t, shadow.Outputs.PlaybackHealth.IsMonitoring, "IsMonitoring should be true")
	assert.Equal(t, "media_player.kitchen", shadow.Outputs.PlaybackHealth.LeadSpeaker)
	assert.Equal(t, "day", shadow.Outputs.PlaybackHealth.MusicType)
	assert.Equal(t, "playing", shadow.Outputs.PlaybackHealth.LastSpeakerState)
	assert.False(t, shadow.Outputs.PlaybackHealth.RecoveryAttempted)

	// Cancel and verify state update
	manager.cancelPlaybackMonitor()

	// Release the sleep gate so monitor can exit
	close(sleepGate)

	// Wait for monitor to exit - deterministic callback
	<-monitorDone

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
