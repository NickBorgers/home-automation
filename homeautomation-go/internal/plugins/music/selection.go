package music

import (
	"fmt"

	"go.uber.org/zap"
)

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
