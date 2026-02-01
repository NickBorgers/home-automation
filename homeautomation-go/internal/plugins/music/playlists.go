package music

import (
	"encoding/json"
	"errors"
	"math"

	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// getNextPlaylistIndex returns the next playlist index with rotation
func (m *Manager) getNextPlaylistIndex(musicType string, optionsCount int) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get current index or initialize to 0
	currentIndex, exists := m.playlistNumbers[musicType]
	if !exists {
		currentIndex = 0
	}

	// Save the index to use
	indexToUse := currentIndex

	// Increment for next time (with wraparound)
	nextIndex := currentIndex + 1
	if nextIndex >= optionsCount {
		nextIndex = 0
	}
	m.playlistNumbers[musicType] = nextIndex

	// Sync to Home Assistant (fire and forget, don't block on errors)
	m.syncWg.Add(1)
	go m.syncPlaylistRotationToHA()

	return indexToUse
}

// loadPlaylistRotationFromHA loads playlist rotation state from Home Assistant on startup.
// It validates that stored indices are within bounds for each music type's playlist count.
func (m *Manager) loadPlaylistRotationFromHA() {
	rotationJSON, err := m.stateManager.GetString("musicPlaylistRotation")
	if err != nil {
		m.logger.Warn("Failed to get playlist rotation from HA", zap.Error(err))
		return
	}

	if rotationJSON == "" || rotationJSON == "{}" {
		m.logger.Debug("No playlist rotation state in HA, starting fresh")
		return
	}

	var loadedRotation map[string]int
	if err := json.Unmarshal([]byte(rotationJSON), &loadedRotation); err != nil {
		m.logger.Warn("Failed to parse playlist rotation JSON from HA, starting fresh",
			zap.String("json", rotationJSON),
			zap.Error(err))
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate and apply each loaded index
	for musicType, index := range loadedRotation {
		// Check if this music type exists in config
		mode, exists := m.config.Music[musicType]
		if !exists {
			// Keep the value anyway - music type might be added back later
			m.playlistNumbers[musicType] = index
			m.logger.Debug("Loaded rotation for unconfigured music type",
				zap.String("musicType", musicType),
				zap.Int("index", index))
			continue
		}

		optionsCount := len(mode.PlaybackOptions)
		if optionsCount == 0 {
			m.logger.Warn("Music type has no playback options, skipping",
				zap.String("musicType", musicType))
			continue
		}

		// Wrap index if it exceeds available playlists (e.g., playlist was removed)
		validIndex := index
		if index >= optionsCount {
			validIndex = index % optionsCount
			m.logger.Info("Playlist rotation index exceeded options count, wrapping",
				zap.String("musicType", musicType),
				zap.Int("storedIndex", index),
				zap.Int("optionsCount", optionsCount),
				zap.Int("wrappedIndex", validIndex))
		}

		m.playlistNumbers[musicType] = validIndex
	}

	m.logger.Info("Loaded playlist rotation from HA", zap.Any("rotation", m.playlistNumbers))
}

// syncPlaylistRotationToHA persists playlist rotation state to Home Assistant.
// This should be called after updating playlistNumbers.
func (m *Manager) syncPlaylistRotationToHA() {
	defer m.syncWg.Done()

	m.mu.RLock()
	rotationCopy := make(map[string]int, len(m.playlistNumbers))
	for k, v := range m.playlistNumbers {
		rotationCopy[k] = v
	}
	m.mu.RUnlock()

	rotationJSON, err := json.Marshal(rotationCopy)
	if err != nil {
		m.logger.Error("Failed to marshal playlist rotation to JSON", zap.Error(err))
		return
	}

	if err := m.stateManager.SetString("musicPlaylistRotation", string(rotationJSON)); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping playlist rotation sync in read-only mode")
		} else {
			m.logger.Error("Failed to sync playlist rotation to HA", zap.Error(err))
		}
	}
}

// WaitForSync waits for any pending playlist rotation syncs to complete.
// This is primarily used for testing to avoid sleep-based synchronization.
func (m *Manager) WaitForSync() {
	m.syncWg.Wait()
}

// calculateVolume calculates final volume from base and multiplier
func (m *Manager) calculateVolume(baseVolume int, multiplier float64) int {
	volume := math.Round(float64(baseVolume) * multiplier)
	// Cap at 15 (Sonos max for Spotify playback scale)
	if volume > 15 {
		volume = 15
	}
	if volume < 0 {
		volume = 0
	}
	return int(volume)
}
