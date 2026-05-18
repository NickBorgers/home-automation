package music

import (
	"fmt"
	"math"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

func (m *Manager) adoptStartupZonePlayback(zone *Zone, selected PlaybackOption, trigger string) bool {
	if trigger != "startup" {
		return false
	}

	leadEntityID := m.getSpeakerEntityID(zone.LeadSpeaker)
	leadState, err := m.haClient.GetState(leadEntityID)
	if err != nil {
		m.logger.Debug("Startup reconciliation skipped: failed to read lead speaker state",
			zap.String("speaker", zone.LeadSpeaker),
			zap.Error(err))
		return false
	}
	if leadState.State != "playing" {
		m.logger.Debug("Startup reconciliation skipped: lead speaker is not playing",
			zap.String("speaker", zone.LeadSpeaker),
			zap.String("state", leadState.State))
		return false
	}

	currentURI, ok := stringAttribute(leadState, "media_content_id")
	if !ok || currentURI == "" {
		m.logger.Debug("Startup reconciliation skipped: lead speaker has no current media URI",
			zap.String("speaker", zone.LeadSpeaker))
		return false
	}

	playbackOption, ok := m.playbackOptionForURI(zone.MusicType, currentURI)
	if !ok {
		m.logger.Info("Startup reconciliation mismatch: current URI is not in mode playlist pool",
			zap.String("mode", zone.MusicType),
			zap.String("current_uri", currentURI),
			zap.String("selected_uri", selected.URI))
		return false
	}

	actualGroup := m.actualGroupMembers(leadState, leadEntityID)
	if !m.groupRoughlyMatches(zone, actualGroup) {
		m.logger.Info("Startup reconciliation mismatch: speaker group does not match zone",
			zap.String("zone", zone.Name),
			zap.Strings("actual_group", mapKeys(actualGroup)))
		return false
	}

	participants := m.participantsWithCurrentVolumes(zone.Participants)
	m.mu.Lock()
	m.currentlyPlaying = &CurrentlyPlayingMusic{
		Type:         zone.MusicType,
		URI:          currentURI,
		MediaType:    playbackOption.MediaType,
		LeadPlayer:   zone.LeadSpeaker,
		Participants: participants,
	}
	m.mu.Unlock()

	m.zoneManager.mu.Lock()
	zone.PlaylistURI = currentURI
	zone.MediaType = playbackOption.MediaType
	zone.Participants = participants
	m.zoneManager.mu.Unlock()

	m.recordAdoptedStartupShadowState(zone.MusicType, playbackOption, participants, zone.LeadSpeaker, actualGroup, trigger)
	m.logger.Info("Adopted existing startup playback",
		zap.String("zone", zone.Name),
		zap.String("lead_speaker", zone.LeadSpeaker),
		zap.String("uri", currentURI))
	if !m.readOnly {
		m.startPlaybackMonitor(leadEntityID, zone.LeadSpeaker, zone.MusicType)
	}
	return true
}

func (m *Manager) playbackOptionForURI(musicType, uri string) (PlaybackOption, bool) {
	mode, ok := m.config.Music[musicType]
	if !ok {
		return PlaybackOption{}, false
	}
	for _, option := range mode.PlaybackOptions {
		if option.URI == uri {
			return option, true
		}
	}
	return PlaybackOption{}, false
}

func (m *Manager) actualGroupMembers(state *ha.State, leadEntityID string) map[string]bool {
	group := make(map[string]bool)
	if members, ok := stringSliceAttribute(state, "group_members"); ok {
		for _, member := range members {
			if member != "" {
				group[member] = true
			}
		}
	}
	group[leadEntityID] = true
	return group
}

func (m *Manager) groupRoughlyMatches(zone *Zone, actualGroup map[string]bool) bool {
	desired := make(map[string]bool, len(zone.Participants))
	for _, p := range zone.Participants {
		desired[m.getSpeakerEntityID(p.PlayerName)] = true
	}
	for entityID := range actualGroup {
		if !desired[entityID] {
			return false
		}
	}
	return true
}

func (m *Manager) participantsWithCurrentVolumes(participants []ParticipantWithVolume) []ParticipantWithVolume {
	result := make([]ParticipantWithVolume, 0, len(participants))
	for _, p := range participants {
		current := p
		if state, err := m.haClient.GetState(m.getSpeakerEntityID(p.PlayerName)); err == nil {
			if volumeLevel, ok := floatAttribute(state, "volume_level"); ok {
				current.Volume = int(math.Round(volumeLevel * 100))
			}
		}
		result = append(result, current)
	}
	return result
}

func (m *Manager) recordAdoptedStartupShadowState(musicType string, playbackOption PlaybackOption, participants []ParticipantWithVolume, leadPlayer string, actualGroup map[string]bool, trigger string) {
	speakers := make([]shadowstate.SpeakerState, 0, len(participants))
	for _, p := range participants {
		speakers = append(speakers, shadowstate.SpeakerState{
			PlayerName:    p.PlayerName,
			Volume:        p.Volume,
			BaseVolume:    p.BaseVolume,
			DefaultVolume: p.DefaultVolume,
			IsLeader:      p.PlayerName == leadPlayer,
			Active:        actualGroup[m.getSpeakerEntityID(p.PlayerName)],
		})
	}

	playlistInfo := &shadowstate.PlaylistInfo{
		URI:       playbackOption.URI,
		Name:      "",
		MediaType: playbackOption.MediaType,
	}
	reason := fmt.Sprintf("Adopted existing playback of '%s' in mode '%s'", playbackOption.URI, musicType)
	m.updateShadowState("adopt_playback", reason, trigger)
	m.updateShadowOutputs(musicType, playlistInfo, speakers, nil)
}

func stringAttribute(state *ha.State, key string) (string, bool) {
	value, ok := state.Attributes[key]
	if !ok {
		return "", false
	}
	str, ok := value.(string)
	return str, ok
}

func floatAttribute(state *ha.State, key string) (float64, bool) {
	value, ok := state.Attributes[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

func stringSliceAttribute(state *ha.State, key string) ([]string, bool) {
	value, ok := state.Attributes[key]
	if !ok {
		return nil, false
	}
	switch v := value.(type) {
	case []string:
		return v, true
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func mapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
