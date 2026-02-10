package music

import (
	"fmt"

	"go.uber.org/zap"
)

// =============================================================================
// Phase 2: Zone Playback Methods
// =============================================================================

// orchestrateZonePlayback starts playback for a zone
// This reuses existing orchestration logic but for a specific zone
func (m *Manager) orchestrateZonePlayback(zone *Zone, playbackOption PlaybackOption, trigger string) error {
	m.logger.Info("Orchestrating zone playback",
		zap.String("zone", zone.Name),
		zap.String("lead_speaker", zone.LeadSpeaker),
		zap.Int("participant_count", len(zone.Participants)),
		zap.String("trigger", trigger))

	// Use existing executePlayback logic
	groupResult, verificationAttempts, err := m.executePlayback(
		zone.MusicType,
		playbackOption,
		zone.Participants,
		zone.LeadSpeaker,
	)

	if err != nil {
		return fmt.Errorf("zone playback failed: %w", err)
	}

	// Record shadow state (parameter order: musicType, playbackOption, participants, leadPlayer, trigger, groupResult, verificationAttempts)
	m.recordPlaybackShadowState(
		zone.MusicType,
		playbackOption,
		zone.Participants,
		zone.LeadSpeaker,
		trigger,
		groupResult,
		verificationAttempts,
	)

	return nil
}

// fadeOutZoneSpeakers fades out all speakers in a zone
func (m *Manager) fadeOutZoneSpeakers(zone *Zone, reason string) error {
	m.logger.Info("Fading out zone speakers",
		zap.String("zone", zone.Name),
		zap.Int("speaker_count", len(zone.Participants)),
		zap.String("reason", reason))

	// Fade out each speaker individually
	for _, p := range zone.Participants {
		entityID := m.getSpeakerEntityID(p.PlayerName)
		if entityID != "" {
			m.fadeOutSingleSpeaker(entityID)
		}
	}

	return nil
}

// addSpeakersToZone adds speakers to an active zone
func (m *Manager) addSpeakersToZone(zone *Zone, speakers []string, trigger string) {
	m.logger.Info("Adding speakers to zone",
		zap.String("zone", zone.Name),
		zap.Strings("speakers", speakers),
		zap.String("trigger", trigger))

	// Get participant configs for new speakers
	mode, ok := m.config.Music[zone.Name]
	if !ok {
		m.logger.Error("Music mode not found for zone", zap.String("zone", zone.Name))
		return
	}

	speakerSet := make(map[string]bool)
	for _, s := range speakers {
		speakerSet[s] = true
	}

	// Find participants to add
	for _, p := range mode.Participants {
		if !speakerSet[p.PlayerName] {
			continue
		}

		// Get entity ID
		entityID := m.getSpeakerEntityID(p.PlayerName)
		if entityID == "" {
			m.logger.Warn("Speaker entity ID not found", zap.String("speaker", p.PlayerName))
			continue
		}

		// Join the zone's Sonos group
		leadEntityID := m.getSpeakerEntityID(zone.LeadSpeaker)
		if err := m.callServiceWithRetry("media_player", "join", map[string]interface{}{
			"entity_id":     entityID,
			"group_members": []string{leadEntityID},
		}); err != nil {
			m.logger.Error("Failed to join speaker to zone",
				zap.String("speaker", p.PlayerName),
				zap.String("zone", zone.Name),
				zap.Error(err))
			continue
		}

		// Calculate and set volume
		volume := m.calculateVolume(p.BaseVolume, 1.0) // Use default multiplier

		// Check if speaker should be unmuted before starting fade-in
		participantWithVolume := ParticipantWithVolume{
			PlayerName:   p.PlayerName,
			BaseVolume:   p.BaseVolume,
			Volume:       volume,
			LeaveMutedIf: p.LeaveMutedIf,
		}

		if m.shouldUnmuteSpeaker(participantWithVolume) {
			m.logger.Info("Speaker joined zone, starting fade-in",
				zap.String("speaker", p.PlayerName),
				zap.String("zone", zone.Name),
				zap.Int("target_volume", volume))

			// Use startFadeInWithContext to enable cancellation when new playback starts
			// This prevents false "human override" detection and allows cancelAllFadeIns() to work
			ctx := m.startFadeInWithContext(entityID)
			go m.fadeInSpeaker(ctx, p.PlayerName, volume, zone.MusicType)
		} else {
			// Speaker should stay muted, but set its target volume
			m.logger.Info("Speaker joined zone, keeping muted",
				zap.String("speaker", p.PlayerName),
				zap.String("zone", zone.Name),
				zap.Int("target_volume", volume))

			if err := m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
				"entity_id":    entityID,
				"volume_level": float64(volume) / 100.0,
			}); err != nil {
				m.logger.Error("Failed to set volume for muted speaker",
					zap.String("speaker", p.PlayerName),
					zap.Error(err))
			}

			if err := m.callServiceWithRetry("media_player", "volume_mute", map[string]interface{}{
				"entity_id":       entityID,
				"is_volume_muted": true,
			}); err != nil {
				m.logger.Error("Failed to mute speaker",
					zap.String("speaker", p.PlayerName),
					zap.Error(err))
			}
		}
	}
}

// removeSpeakersFromZone removes speakers from an active zone
func (m *Manager) removeSpeakersFromZone(zone *Zone, speakers []string, trigger string) {
	m.logger.Info("Removing speakers from zone",
		zap.String("zone", zone.Name),
		zap.Strings("speakers", speakers),
		zap.String("trigger", trigger))

	for _, speaker := range speakers {
		entityID := m.getSpeakerEntityID(speaker)
		if entityID == "" {
			continue
		}

		// Fade out the speaker first
		m.fadeOutSingleSpeaker(entityID)

		// Unjoin from group
		if err := m.callServiceWithRetry("media_player", "unjoin", map[string]interface{}{
			"entity_id": entityID,
		}); err != nil {
			m.logger.Error("Failed to unjoin speaker from zone",
				zap.String("speaker", speaker),
				zap.String("zone", zone.Name),
				zap.Error(err))
		}
	}
}

// fadeOutSingleSpeaker fades out a single speaker
func (m *Manager) fadeOutSingleSpeaker(entityID string) {
	m.logger.Debug("Fading out single speaker", zap.String("entity_id", entityID))

	// Set volume to 0 (simple fade out for now)
	if err := m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
		"entity_id":    entityID,
		"volume_level": 0.0,
	}); err != nil {
		m.logger.Error("Failed to set volume for fade out",
			zap.String("entity_id", entityID),
			zap.Error(err))
	}
}
