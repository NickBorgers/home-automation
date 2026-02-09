package music

import (
	"errors"
	"fmt"
	"time"

	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// selectAppropriateMusicMode determines which music mode should be active (without trigger context)
func (m *Manager) selectAppropriateMusicMode() {
	m.selectAppropriateMusicModeWithContext("", false)
}

// selectAppropriateMusicModeWithContext determines which music mode should be active with trigger context
func (m *Manager) selectAppropriateMusicModeWithContext(triggerKey string, isWakeUpEvent bool) {
	m.logger.Debug("Selecting appropriate music mode",
		zap.String("trigger_key", triggerKey),
		zap.Bool("is_wake_up_event", isWakeUpEvent))

	// Skip if zone manager is actively managing zones
	// This prevents the legacy selection logic from overriding zone manager decisions
	// (e.g., during wake sequences where isAnyoneAsleep=true but zone manager wants morning music)
	if m.zoneManager != nil {
		activeZones := m.zoneManager.GetActiveZones()
		if len(activeZones) > 0 {
			m.logger.Debug("Zone manager is actively managing zones, skipping legacy music mode selection",
				zap.Int("active_zone_count", len(activeZones)))
			return
		}
	}

	// Get current state
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneHome", zap.Error(err))
		return
	}

	// If no one is home, stop music
	if !isAnyoneHome {
		m.logger.Info("No one is home, stopping music")
		if err := m.setMusicPlaybackType(""); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping music playback type update in read-only mode",
					zap.String("music_type", ""))
			} else {
				m.logger.Error("Failed to set empty music playback type", zap.Error(err))
			}
		}
		return
	}

	// Check if anyone is asleep - sleep mode has highest priority
	isAnyoneAsleep, err := m.stateManager.GetBool("isAnyoneAsleep")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneAsleep", zap.Error(err))
		return
	}

	if isAnyoneAsleep {
		m.logger.Info("Someone is asleep, selecting sleep mode")
		if err := m.setMusicPlaybackType("sleep"); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping music playback type update in read-only mode",
					zap.String("music_type", "sleep"))
			} else {
				m.logger.Error("Failed to set sleep music playback type", zap.Error(err))
			}
		}
		return
	}

	// Get current day phase
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	// Get current music playback type to check for persistence
	currentMusicType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil {
		m.logger.Error("Failed to get musicPlaybackType", zap.Error(err))
		return
	}

	// Determine music mode based on day phase and trigger context
	musicMode := m.determineMusicModeFromDayPhase(dayPhase, currentMusicType, triggerKey, isWakeUpEvent)

	m.logger.Info("Selected music mode",
		zap.String("day_phase", dayPhase),
		zap.String("current_music_type", currentMusicType),
		zap.String("trigger_key", triggerKey),
		zap.Bool("is_wake_up_event", isWakeUpEvent),
		zap.String("new_music_mode", musicMode))

	// Set the music playback type
	if err := m.setMusicPlaybackType(musicMode); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping music playback type update in read-only mode",
				zap.String("music_type", musicMode))
		} else {
			m.logger.Error("Failed to set music playback type", zap.Error(err))
		}
	}
}

// determineMusicModeFromDayPhase determines the music mode based on the current day phase
// Matches Node-RED behavior: morning music only plays on wake-up events
func (m *Manager) determineMusicModeFromDayPhase(dayPhase string, currentMusicType string, triggerKey string, isWakeUpEvent bool) string {
	switch dayPhase {
	case "morning":
		// Morning music ONLY plays when someone wakes up (matches Node-RED)
		// Otherwise, fall back to day music during morning phase
		if isWakeUpEvent {
			// Check if it's Sunday (no morning music on Sundays)
			// Use configured timezone to avoid UTC-based weekday check issues
			// (e.g., 6 PM CST Saturday = 00:00 UTC Sunday)
			nowLocal := m.timeProvider.Now().In(m.timezone)
			if nowLocal.Weekday() == time.Sunday {
				m.logger.Debug("Sunday detected, using day mode instead of morning")
				return "day"
			}
			m.logger.Info("Wake-up event during morning phase, playing morning music")
			return "morning"
		}
		// During morning phase but not a wake-up event - use day music
		m.logger.Debug("Morning phase but not a wake-up event, using day music")
		return "day"

	case "day":
		return "day"

	case "sunset", "dusk":
		return "evening"

	case "winddown", "night":
		// Don't override sleep music with winddown
		if currentMusicType == "sleep" {
			m.logger.Debug("Sleep music already playing, not changing to winddown")
			return "sleep"
		}
		return "winddown"

	default:
		m.logger.Warn("Unknown day phase, defaulting to day mode",
			zap.String("day_phase", dayPhase))
		return "day"
	}
}

// setMusicPlaybackType updates the musicPlaybackType state variable
func (m *Manager) setMusicPlaybackType(musicType string) error {
	// Get current type to check if it's actually changing
	currentType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil {
		return fmt.Errorf("failed to get current music playback type: %w", err)
	}

	// Only update if it's different
	if currentType == musicType {
		m.logger.Debug("Music playback type unchanged",
			zap.String("type", musicType))
		return nil
	}

	m.logger.Info("Changing music playback type",
		zap.String("from", currentType),
		zap.String("to", musicType))

	// Update the state variable
	if err := m.stateManager.SetString("musicPlaybackType", musicType); err != nil {
		return fmt.Errorf("failed to set music playback type: %w", err)
	}

	return nil
}
