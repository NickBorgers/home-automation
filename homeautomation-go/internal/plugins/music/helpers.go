package music

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	if err := m.haClient.CallService(m.ctx, domain, service, serviceData); err != nil {
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

// callServiceBestEffort calls a Home Assistant service with a short timeout and
// no retries. Used for cleanup operations (like unjoin) where blocking on
// unresponsive speakers would be worse than skipping them.
// The timeout bounds the total time including the HA client's internal retries —
// context cancellation prevents retry attempts after the deadline.
func (m *Manager) callServiceBestEffort(domain, service string, serviceData map[string]interface{}, timeout time.Duration) error {
	if m.readOnly {
		m.logger.Debug("Read-only mode: would call service (best-effort)",
			zap.String("domain", domain),
			zap.String("service", service),
			zap.Any("service_data", serviceData))
		return nil
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	m.logger.Debug("Calling HA service (best-effort)",
		zap.String("domain", domain),
		zap.String("service", service),
		zap.Duration("timeout", timeout),
		zap.Any("service_data", serviceData))

	if err := m.haClient.CallService(ctx, domain, service, serviceData); err != nil {
		return fmt.Errorf("best-effort service call failed: %w", err)
	}

	return nil
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

// Reset stops all active zones and re-evaluates zone resolution from scratch.
// This provides a clean restart without requiring the legacy selectAppropriateMusicMode path.
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Music - stopping all zones and re-resolving")

	// Stop all active zones and current playback
	if m.zoneManager != nil {
		m.zoneManager.StopAllZones("reset")
	}
	m.stopPlayback()

	// Clear currentlyPlaying to allow restart of same mode
	m.mu.Lock()
	m.currentlyPlaying = nil
	m.mu.Unlock()

	// Clear musicPlaybackType so zone resolution starts clean
	if err := m.setMusicPlaybackType(""); err != nil {
		m.logger.Warn("Failed to clear musicPlaybackType during reset", zap.Error(err))
	}

	// Re-resolve zones from current state
	if m.zoneManager != nil {
		if err := m.zoneManager.ResolveZones("reset"); err != nil {
			m.logger.Error("Failed to resolve zones after reset", zap.Error(err))
			return err
		}
	}

	m.logger.Info("Successfully reset Music")
	return nil
}
