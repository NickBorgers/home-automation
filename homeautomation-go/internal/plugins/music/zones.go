package music

import (
	"fmt"
	"time"

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

	// Update currently playing state to reflect the zone's music type.
	// This is critical for consistency with the non-zone orchestratePlayback path
	// and for any code that checks currentlyPlaying.Type.
	m.mu.Lock()
	m.currentlyPlaying = &CurrentlyPlayingMusic{
		Type:         zone.MusicType,
		URI:          playbackOption.URI,
		MediaType:    playbackOption.MediaType,
		LeadPlayer:   zone.LeadSpeaker,
		Participants: zone.Participants,
	}
	m.mu.Unlock()
	// Publish playback state for cross-plugin discovery (e.g., TTS speaker selection)
	m.publishCurrentlyPlayingMusic()

	if m.readOnly {
		m.logger.Info("Read-only mode: would start zone playback",
			zap.String("zone", zone.Name),
			zap.String("lead_speaker", zone.LeadSpeaker),
			zap.Int("participant_count", len(zone.Participants)))
		// Record shadow state even in read-only mode
		m.recordPlaybackShadowState(zone.MusicType, playbackOption, zone.Participants, zone.LeadSpeaker, trigger, nil, 0)
		return nil
	}

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
		m.fadeOutSingleSpeaker(p.PlayerName)
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

		// Check exclude_if conditions (consistent with assignSpeakersToZones)
		if !m.shouldIncludeInZone(p) {
			m.logger.Debug("Speaker excluded from zone during dynamic add",
				zap.String("speaker", p.PlayerName),
				zap.String("zone", zone.Name))
			continue
		}

		// Zero volume before joining to prevent audio pop when speaker starts
		// playing at its current volume before fadeInSpeaker can zero it.
		if err := m.speakerSetVolume(p.PlayerName, 0); err != nil {
			m.logger.Warn("Failed to zero volume before group join",
				zap.String("speaker", p.PlayerName),
				zap.Error(err))
		}

		// Join the zone's Sonos group
		if err := m.speakerJoinGroup(p.PlayerName, zone.LeadSpeaker); err != nil {
			m.logger.Error("Failed to join speaker to zone",
				zap.String("speaker", p.PlayerName),
				zap.String("zone", zone.Name),
				zap.Error(err))
			continue
		}

		// Calculate and set volume using the zone's active playback option multiplier
		volume := m.calculateVolume(p.BaseVolume, zone.VolumeMultiplier)

		// Create ParticipantWithVolume to check mute conditions
		participant := ParticipantWithVolume{
			PlayerName:    p.PlayerName,
			BaseVolume:    p.BaseVolume,
			Volume:        volume,
			DefaultVolume: volume,
			LeaveMutedIf:  p.LeaveMutedIf,
			ExcludeIf:     p.ExcludeIf,
		}

		// Check if speaker should be unmuted before starting fade-in
		if m.shouldUnmuteSpeaker(participant) {
			// Use startFadeInWithContext to enable cancellation when new playback starts
			// This prevents false "human override" detection and allows cancelAllFadeIns() to work
			entityID := m.getSpeakerEntityID(p.PlayerName)
			ctx := m.startFadeInWithContext(entityID)
			go m.fadeInSpeaker(ctx, p.PlayerName, volume, zone.MusicType)
		} else {
			// Speaker should stay muted, but set its target volume
			m.logger.Info("Speaker joined zone, keeping muted",
				zap.String("speaker", p.PlayerName),
				zap.String("zone", zone.Name),
				zap.Int("target_volume", volume))

			if err := m.speakerSetVolume(p.PlayerName, volume); err != nil {
				m.logger.Error("Failed to set volume for muted speaker",
					zap.String("speaker", p.PlayerName),
					zap.Error(err))
			}

			if err := m.speakerSetMute(p.PlayerName, true); err != nil {
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
		// Fade out the speaker first
		m.fadeOutSingleSpeaker(speaker)

		// Unjoin from group
		if err := m.speakerUnjoin(speaker); err != nil {
			m.logger.Error("Failed to unjoin speaker from zone",
				zap.String("speaker", speaker),
				zap.String("zone", zone.Name),
				zap.Error(err))
		}
	}
}

// smoothVolumeAdjust gradually adjusts a speaker's volume to the target.
// Used during seamless zone transitions where speakers continue playing
// but need a volume change (e.g., sleep-prep multiplier 1.3 → sleep multiplier 1.45).
func (m *Manager) smoothVolumeAdjust(speakerName string, targetVolume int) {
	entityID := m.getSpeakerEntityID(speakerName)
	if entityID == "" {
		return
	}

	// State read via HA (getSpeakerVolume queries HA entity state)
	currentVolume := m.getSpeakerVolume(entityID)
	if currentVolume < 0 {
		// Can't read current volume, set target directly
		if err := m.speakerSetVolume(speakerName, targetVolume); err != nil {
			m.logger.Error("Failed to set volume during seamless transition",
				zap.String("speaker", speakerName),
				zap.Error(err))
		}
		return
	}

	if currentVolume == targetVolume {
		return // Already at target
	}

	m.logger.Info("Smoothly adjusting volume for seamless transition",
		zap.String("speaker", speakerName),
		zap.Int("from", currentVolume),
		zap.Int("to", targetVolume))

	// Adjust volume in steps of 1
	step := 1
	if currentVolume > targetVolume {
		step = -1
	}

	for vol := currentVolume + step; ; vol += step {
		if (step > 0 && vol > targetVolume) || (step < 0 && vol < targetVolume) {
			break
		}
		if err := m.speakerSetVolume(speakerName, vol); err != nil {
			m.logger.Error("Failed to adjust volume during seamless transition",
				zap.String("speaker", speakerName),
				zap.Int("volume", vol),
				zap.Error(err))
		}
		m.sleepFunc(250 * time.Millisecond)
	}
}

// fadeOutSingleSpeaker fades out a single speaker by setting volume to 0.
func (m *Manager) fadeOutSingleSpeaker(speakerName string) {
	m.logger.Debug("Fading out single speaker", zap.String("speaker", speakerName))

	if err := m.speakerSetVolume(speakerName, 0); err != nil {
		m.logger.Error("Failed to set volume for fade out",
			zap.String("speaker", speakerName),
			zap.Error(err))
	}
}
