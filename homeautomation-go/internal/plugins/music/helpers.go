package music

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// getStateValue gets a state variable value by key
func (m *Manager) getStateValue(key string) (interface{}, error) {
	// Try as boolean first
	if val, err := m.stateManager.GetBool(key); err == nil {
		return val, nil
	}

	// Try as string
	if val, err := m.stateManager.GetString(key); err == nil {
		return val, nil
	}

	// Try as number
	if val, err := m.stateManager.GetNumber(key); err == nil {
		return val, nil
	}

	return nil, fmt.Errorf("failed to get state variable: %s", key)
}

// valuesMatch checks if two values match
func (m *Manager) valuesMatch(a, b interface{}) bool {
	// Simple equality check
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// callService calls a Home Assistant service
func (m *Manager) callService(domain, service string, serviceData map[string]interface{}) error {
	if m.readOnly {
		m.logger.Debug("Read-only mode: would call service",
			zap.String("domain", domain),
			zap.String("service", service),
			zap.Any("service_data", serviceData))
		return nil
	}

	m.logger.Debug("Calling HA service",
		zap.String("domain", domain),
		zap.String("service", service),
		zap.Any("service_data", serviceData))

	// Call the service via HA client
	if err := m.haClient.CallService(domain, service, serviceData); err != nil {
		return fmt.Errorf("service call failed: %w", err)
	}

	return nil
}

// refreshAvailableSpeakers queries Home Assistant for all media_player entities
// and caches which ones are available for use
func (m *Manager) refreshAvailableSpeakers() error {
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get states from Home Assistant: %w", err)
	}

	m.availableSpeakersMu.Lock()
	defer m.availableSpeakersMu.Unlock()

	m.availableSpeakers = make(map[string]bool)
	for _, state := range states {
		if strings.HasPrefix(state.EntityID, "media_player.") {
			m.availableSpeakers[state.EntityID] = true
		}
	}

	m.logger.Info("Refreshed available speakers",
		zap.Int("count", len(m.availableSpeakers)))
	return nil
}

// isSpeakerAvailable checks if a speaker entity exists in Home Assistant
func (m *Manager) isSpeakerAvailable(entityID string) bool {
	m.availableSpeakersMu.RLock()
	defer m.availableSpeakersMu.RUnlock()
	return m.availableSpeakers[entityID]
}

// validateConfiguredSpeakers logs warnings for any configured speakers not found in Home Assistant
func (m *Manager) validateConfiguredSpeakers() {
	for modeName, mode := range m.config.Music {
		for _, participant := range mode.Participants {
			entityID := m.getSpeakerEntityID(participant.PlayerName)
			if !m.isSpeakerAvailable(entityID) {
				m.logger.Warn("Configured speaker not found in Home Assistant",
					zap.String("speaker", participant.PlayerName),
					zap.String("entity_id", entityID),
					zap.String("mode", modeName))
			}
		}
	}
}

// callServiceWithRetry wraps callService with refresh-on-error logic
// If a service call fails, it refreshes the available speakers and retries once
func (m *Manager) callServiceWithRetry(domain, service string, serviceData map[string]interface{}) error {
	// First attempt
	err := m.callService(domain, service, serviceData)
	if err == nil {
		return nil
	}

	// Check if entity might not exist
	entityID, hasEntity := serviceData["entity_id"].(string)
	if !hasEntity {
		return err // No entity_id, can't validate
	}

	// Refresh available speakers
	// For join operations, also log which speakers we're trying to join
	logFields := []zap.Field{
		zap.String("entity_id", entityID),
		zap.String("service", domain+"."+service),
		zap.Error(err),
	}
	if groupMembers, ok := serviceData["group_members"].([]string); ok && len(groupMembers) > 0 {
		logFields = append(logFields, zap.Strings("group_members", groupMembers))
	}
	m.logger.Info("Service call failed, refreshing available speakers", logFields...)

	if refreshErr := m.refreshAvailableSpeakers(); refreshErr != nil {
		m.logger.Warn("Failed to refresh speakers", zap.Error(refreshErr))
		return err // Return original error
	}

	// Check if entity now exists
	if !m.isSpeakerAvailable(entityID) {
		return fmt.Errorf("speaker %s not available in Home Assistant: %w", entityID, err)
	}

	// Retry once
	m.logger.Info("Retrying service call after refresh",
		zap.String("entity_id", entityID),
		zap.String("service", domain+"."+service))
	return m.callService(domain, service, serviceData)
}

// Reset re-evaluates appropriate music mode and triggers playback
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Music - re-selecting appropriate music mode")

	// Check if anyone is home first (matches Node-RED and selectAppropriateMusicModeWithContext)
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneHome", zap.Error(err))
		return err
	}

	// If no one is home, stop music
	if !isAnyoneHome {
		m.logger.Info("No one is home, stopping music on reset")
		m.stopPlayback()
		if err := m.setMusicPlaybackType(""); err != nil {
			if !errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Error("Failed to set empty music playback type", zap.Error(err))
			}
		}
		return nil
	}

	// Check if anyone is asleep - sleep mode has highest priority (matches Node-RED)
	isAnyoneAsleep, err := m.stateManager.GetBool("isAnyoneAsleep")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneAsleep", zap.Error(err))
		return err
	}

	var musicMode string
	if isAnyoneAsleep {
		m.logger.Info("Someone is asleep, selecting sleep mode on reset")
		musicMode = "sleep"
	} else {
		// Get current day phase to determine appropriate mode
		dayPhase, err := m.stateManager.GetString("dayPhase")
		if err != nil {
			m.logger.Error("Failed to get dayPhase", zap.Error(err))
			return err
		}

		// Get current music type
		currentMusicType, err := m.stateManager.GetString("musicPlaybackType")
		if err != nil {
			m.logger.Error("Failed to get musicPlaybackType", zap.Error(err))
			return err
		}

		// Determine music mode (no trigger key or wake-up event for reset)
		musicMode = m.determineMusicModeFromDayPhase(dayPhase, currentMusicType, "", false)
	}

	m.logger.Info("Reset selected music mode",
		zap.Bool("is_anyone_asleep", isAnyoneAsleep),
		zap.String("new_music_mode", musicMode))

	// Check rate limiting (max 1 playback per 10 seconds)
	// If rate-limited, silently drop the reset (matches Node-RED behavior)
	// NOTE: We check but DON'T update lastPlaybackTime here - let the handler do it
	// to avoid double-triggering playback (once from handler, once from direct call)
	m.mu.Lock()
	timeSinceLastPlayback := m.timeProvider.Now().Sub(m.lastPlaybackTime)
	if timeSinceLastPlayback < 10*time.Second && !m.lastPlaybackTime.IsZero() {
		m.mu.Unlock()
		m.logger.Warn("Rate limiting: dropping reset request (too soon after last playback)",
			zap.Duration("time_since_last", timeSinceLastPlayback),
			zap.String("music_mode", musicMode))
		return nil
	}

	// Clear currentlyPlaying to allow restart of same mode
	// This ensures the handler won't skip due to "already playing" check
	m.currentlyPlaying = nil
	m.mu.Unlock()

	// If empty mode, stop playback
	if musicMode == "" {
		m.logger.Info("Stopping music playback on reset")
		m.stopPlayback()

		// Update state variable
		if err := m.setMusicPlaybackType(""); err != nil {
			if !errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Error("Failed to set music playback type", zap.Error(err))
			}
		}

		m.logger.Info("Successfully reset Music")
		return nil
	}

	// Use clear-then-set pattern to force handler to fire even for same-mode resets
	// This leverages the existing pattern used by sleep hygiene (per comment at line 422-424)
	// Step 1: Clear to "" - this triggers handler but stopPlayback() is safe (just fades out)
	// Step 2: Set to target mode - this triggers handler which calls orchestratePlayback()
	if err := m.setMusicPlaybackType(""); err != nil {
		if !errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Error("Failed to clear music playback type", zap.Error(err))
			return err
		}
	}
	if err := m.setMusicPlaybackType(musicMode); err != nil {
		if !errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Error("Failed to set music playback type", zap.Error(err))
			return err
		}
	}

	m.logger.Info("Successfully reset Music")
	return nil
}
