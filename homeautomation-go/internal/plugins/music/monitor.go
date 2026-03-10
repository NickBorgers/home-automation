package music

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// isPlaybackActive checks if the speaker is currently in a playing state.
// Returns true if state is "playing", false for "paused", "idle", "off", etc.
func (m *Manager) isPlaybackActive(entityID string) (bool, error) {
	state, err := m.haClient.GetState(entityID)
	if err != nil {
		return false, fmt.Errorf("failed to get speaker state: %w", err)
	}
	if state == nil {
		return false, fmt.Errorf("speaker state is nil")
	}

	// Sonos media_player states: playing, paused, idle, off, unavailable
	isPlaying := state.State == "playing"

	m.logger.Debug("Checked playback state",
		zap.String("entity_id", entityID),
		zap.String("state", state.State),
		zap.Bool("is_playing", isPlaying))

	return isPlaying, nil
}

// cancelPlaybackMonitor cancels any active playback health monitor.
// Safe to call even if no monitor is active.
func (m *Manager) cancelPlaybackMonitor() {
	m.playbackMonitorMu.Lock()
	defer m.playbackMonitorMu.Unlock()

	if m.playbackMonitorCancel != nil {
		m.logger.Debug("Cancelling active playback health monitor")
		m.playbackMonitorCancel()
		m.playbackMonitorCancel = nil
	}
}

// startPlaybackMonitor starts a new playback health monitor goroutine.
// It cancels any existing monitor first, then launches a new one.
// The monitor polls the lead speaker state and attempts recovery if auto-pause is detected.
// leadEntityID is used for state reads (via HA), leadSpeakerName for commands (via SoCo/HA).
func (m *Manager) startPlaybackMonitor(leadEntityID, leadSpeakerName, musicType string) {
	m.playbackMonitorMu.Lock()
	defer m.playbackMonitorMu.Unlock()

	// Cancel any existing monitor
	if m.playbackMonitorCancel != nil {
		m.logger.Debug("Cancelling previous playback health monitor for new playback")
		m.playbackMonitorCancel()
	}

	// Create new context for this monitor
	ctx, cancel := context.WithCancel(context.Background())
	m.playbackMonitorCancel = cancel

	// Record monitor start in shadow state
	m.recordPlaybackMonitorStart(leadEntityID, musicType)

	m.logger.Info("Starting playback health monitor",
		zap.String("lead_speaker", leadSpeakerName),
		zap.String("music_type", musicType),
		zap.Duration("duration", playbackMonitorDuration))

	// Launch monitor goroutine
	go m.monitorPlaybackHealth(ctx, leadEntityID, leadSpeakerName, musicType)
}

// monitorPlaybackHealth polls the lead speaker state and detects auto-pause.
// If auto-pause is detected (playing -> paused), it attempts recovery ONCE via media_play.
// The monitor exits after: recovery attempt, timeout, or cancellation.
// leadEntityID is used for state reads, leadSpeakerName for recovery commands.
func (m *Manager) monitorPlaybackHealth(ctx context.Context, leadEntityID, leadSpeakerName, musicType string) {
	// Signal completion for test synchronization (if callback is set)
	defer func() {
		if m.monitorDoneCallback != nil {
			m.monitorDoneCallback()
		}
	}()

	endTime := m.timeProvider.Now().Add(playbackMonitorDuration)
	lastState := "playing" // Assume playing since we just verified it

	for {
		// Check for cancellation before sleeping
		select {
		case <-ctx.Done():
			m.logger.Debug("Playback health monitor cancelled")
			m.recordPlaybackMonitorEnd("cancelled")
			return
		default:
		}

		// Wait for poll interval (uses injectable sleep for testability)
		m.sleepFunc(playbackMonitorPollInterval)

		// Check for cancellation after sleeping
		select {
		case <-ctx.Done():
			m.logger.Debug("Playback health monitor cancelled")
			m.recordPlaybackMonitorEnd("cancelled")
			return
		default:
		}

		// Check if monitor duration has expired
		if m.timeProvider.Now().After(endTime) {
			m.logger.Info("Playback health monitor completed - no auto-pause detected",
				zap.String("lead_speaker", leadSpeakerName))
			m.recordPlaybackMonitorEnd("completed")
			return
		}

		// Get current speaker state (state read via HA)
		playing, err := m.isPlaybackActive(leadEntityID)
		if err != nil {
			m.logger.Warn("Failed to check playback state during health monitor",
				zap.String("lead_speaker", leadSpeakerName),
				zap.Error(err))
			m.updatePlaybackHealthState("unknown")
			continue
		}

		currentState := "paused"
		if playing {
			currentState = "playing"
		}
		m.updatePlaybackHealthState(currentState)

		// Detect unexpected pause (playing -> paused transition)
		if lastState == "playing" && currentState == "paused" {
			m.logger.Warn("Auto-pause detected during health monitoring",
				zap.String("lead_speaker", leadSpeakerName),
				zap.String("music_type", musicType))

			// Wait briefly to confirm it's not transient
			m.sleepFunc(playbackRecoveryDelay)

			// Re-check state after delay
			resumedOnItsOwn, checkErr := m.isPlaybackActive(leadEntityID)
			if checkErr == nil && resumedOnItsOwn {
				// Speaker resumed on its own (transient state)
				m.logger.Debug("Speaker resumed on its own, continuing monitoring",
					zap.String("lead_speaker", leadSpeakerName))
				lastState = "playing"
				continue
			}

			// Still paused (or error checking), attempt recovery
			m.logger.Info("Attempting playback recovery after auto-pause",
				zap.String("lead_speaker", leadSpeakerName))

			success := m.attemptPlaybackRecovery(leadEntityID, leadSpeakerName)
			if success {
				m.logger.Info("Playback recovery successful",
					zap.String("lead_speaker", leadSpeakerName))
				m.recordPlaybackRecoveryResult("success")
			} else {
				m.logger.Warn("Playback recovery failed - stopping monitor to avoid fighting human pause",
					zap.String("lead_speaker", leadSpeakerName))
				m.recordPlaybackRecoveryResult("failed")
			}

			// Exit after single recovery attempt (success or failure)
			m.recordPlaybackMonitorEnd("recovery_attempted")
			return
		}

		lastState = currentState
	}
}

// attemptPlaybackRecovery sends a play command to resume playback.
// Returns true if playback resumed successfully, false otherwise.
// leadEntityID is used for state verification, leadSpeakerName for the play command.
func (m *Manager) attemptPlaybackRecovery(leadEntityID, leadSpeakerName string) bool {
	// Send play command (routed to SoCo or HA)
	if err := m.speakerPlay(leadSpeakerName); err != nil {
		m.logger.Error("Failed to send recovery play command",
			zap.String("speaker", leadSpeakerName),
			zap.Error(err))
		return false
	}

	// Wait for command to take effect
	m.sleepFunc(playbackRecoveryDelay)

	// Verify playback resumed (state read via HA)
	playing, err := m.isPlaybackActive(leadEntityID)
	if err != nil {
		m.logger.Warn("Failed to verify recovery",
			zap.String("speaker", leadSpeakerName),
			zap.Error(err))
		return false
	}

	return playing
}
