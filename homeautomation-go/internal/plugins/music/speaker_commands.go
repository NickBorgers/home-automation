package music

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// =============================================================================
// Speaker Command Routing
//
// These methods route speaker commands to either SoCo-CLI (direct UPnP) or
// Home Assistant service calls, depending on whether SoCo-CLI is configured.
//
// When SoCo-CLI is configured (SOCO_CLI_URL set):
//   Go service → SoCo-CLI HTTP API → UPnP → Speaker (direct, low latency)
//
// When SoCo-CLI is not configured (fallback):
//   Go service → HA WebSocket → HA Sonos Integration → UPnP → Speaker
//
// State reads (current volume, playback status) always go through HA regardless
// of whether SoCo-CLI is configured.
//
// All methods take speaker names ("Kitchen", "Front Room") not entity IDs.
// =============================================================================

// speakerSetVolume sets a speaker's volume (0-100 percentage).
// Routes to SoCo-CLI for direct UPnP control when available.
func (m *Manager) speakerSetVolume(speakerName string, volumePercent int) error {
	if m.socoClient != nil {
		return m.socoClient.SetVolume(speakerName, volumePercent)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
		"entity_id":    entityID,
		"volume_level": float64(volumePercent) / 100.0,
	})
}

// speakerSetMute sets the mute state of a speaker.
// Routes to SoCo-CLI for direct UPnP control when available.
func (m *Manager) speakerSetMute(speakerName string, muted bool) error {
	if m.socoClient != nil {
		if muted {
			return m.socoClient.Mute(speakerName)
		}
		return m.socoClient.Unmute(speakerName)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "volume_mute", map[string]interface{}{
		"entity_id":       entityID,
		"is_volume_muted": muted,
	})
}

// speakerJoinGroup adds a single follower speaker to the lead speaker's group.
// In SoCo/UPnP, the follower joins the lead. In HA, the lead adds the follower.
func (m *Manager) speakerJoinGroup(followerName, leadName string) error {
	if m.socoClient != nil {
		return m.socoClient.GroupSpeaker(followerName, leadName)
	}
	leadEntityID := m.getSpeakerEntityID(leadName)
	followerEntityID := m.getSpeakerEntityID(followerName)
	return m.callServiceWithRetry("media_player", "join", map[string]interface{}{
		"entity_id":     leadEntityID,
		"group_members": []string{followerEntityID},
	})
}

// speakerJoinGroupBatch adds multiple follower speakers to the lead speaker's group.
// For SoCo-CLI, each follower joins individually; errors are logged but do not
// prevent remaining speakers from being attempted. For HA, uses a single batch call.
func (m *Manager) speakerJoinGroupBatch(leadName string, followerNames []string) error {
	if m.socoClient != nil {
		var firstErr error
		for _, follower := range followerNames {
			if err := m.socoClient.GroupSpeaker(follower, leadName); err != nil {
				m.logger.Warn("Failed to join speaker to group, continuing with remaining speakers",
					zap.String("follower", follower),
					zap.String("lead", leadName),
					zap.Error(err))
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	}
	leadEntityID := m.getSpeakerEntityID(leadName)
	var groupMembers []string
	for _, f := range followerNames {
		groupMembers = append(groupMembers, m.getSpeakerEntityID(f))
	}
	return m.callServiceWithRetry("media_player", "join", map[string]interface{}{
		"entity_id":     leadEntityID,
		"group_members": groupMembers,
	})
}

// speakerUnjoin removes a speaker from its current group.
// Routes to SoCo-CLI for direct UPnP control when available.
func (m *Manager) speakerUnjoin(speakerName string) error {
	if m.socoClient != nil {
		return m.socoClient.UngroupSpeaker(speakerName)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "unjoin", map[string]interface{}{
		"entity_id": entityID,
	})
}

// speakerUnjoinBestEffort removes a speaker from its current group with a timeout.
// Used for cleanup operations where blocking on unresponsive speakers would be
// worse than skipping them. The timeout bounds the operation instead of using
// extended retries.
func (m *Manager) speakerUnjoinBestEffort(speakerName string, timeout time.Duration) error {
	if m.socoClient != nil {
		ctx, cancel := context.WithTimeout(m.ctx, timeout)
		defer cancel()
		return m.socoClient.UngroupSpeakerCtx(ctx, speakerName)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceBestEffort("media_player", "unjoin", map[string]interface{}{
		"entity_id": entityID,
	}, timeout)
}

// speakerPlay resumes playback on a speaker.
// Routes to SoCo-CLI for direct UPnP control when available.
func (m *Manager) speakerPlay(speakerName string) error {
	if m.socoClient != nil {
		return m.socoClient.Play(speakerName)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "media_play", map[string]interface{}{
		"entity_id": entityID,
	})
}

// speakerPlayMedia starts playback of specific media content on a speaker.
// Routes to SoCo-CLI (queue-based: clear_queue → add_uri_to_queue → play_from_queue)
// when available. Queue-based playback ensures Sonos repeat mode works correctly.
// For Tidal playback, use socoClient.PlayShareLink directly instead.
func (m *Manager) speakerPlayMedia(speakerName, mediaContentID, mediaContentType string) error {
	if m.socoClient != nil {
		if mediaContentType != "" {
			m.logger.Debug("SoCo-CLI queue-based playback does not use mediaContentType; parameter ignored",
				zap.String("speaker", speakerName),
				zap.String("mediaContentType", mediaContentType))
		}
		return m.socoClient.PlayURIFromQueue(speakerName, mediaContentID)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "play_media", map[string]interface{}{
		"entity_id":          entityID,
		"media_content_id":   mediaContentID,
		"media_content_type": mediaContentType,
	})
}

// speakerSetShuffle enables or disables shuffle mode on a speaker.
// Routes to SoCo-CLI for direct UPnP control when available.
func (m *Manager) speakerSetShuffle(speakerName string, enabled bool) error {
	if m.socoClient != nil {
		return m.socoClient.SetShuffle(speakerName, enabled)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "shuffle_set", map[string]interface{}{
		"entity_id": entityID,
		"shuffle":   enabled,
	})
}

// speakerSetRepeat sets the repeat mode on a speaker ("all", "one", "off").
// Routes to SoCo-CLI for direct UPnP control when available.
func (m *Manager) speakerSetRepeat(speakerName string, mode string) error {
	if m.socoClient != nil {
		return m.socoClient.SetRepeat(speakerName, mode)
	}
	entityID := m.getSpeakerEntityID(speakerName)
	return m.callServiceWithRetry("media_player", "repeat_set", map[string]interface{}{
		"entity_id": entityID,
		"repeat":    mode,
	})
}

// logSpeakerCommandPath logs which path (SoCo vs HA) speaker commands are using.
// Called once during startup for observability.
func (m *Manager) logSpeakerCommandPath() {
	if m.socoClient != nil {
		m.logger.Info("Speaker commands routed via SoCo-CLI (direct UPnP)",
			zap.String("soco_url", m.socoClient.baseURL))
	} else {
		m.logger.Info("Speaker commands routed via Home Assistant (WebSocket → Sonos integration)")
	}
}

// speakerCommandPath returns a string describing the current command routing.
// Used for shadow state reporting.
func (m *Manager) speakerCommandPath() string {
	if m.socoClient != nil {
		return fmt.Sprintf("soco-cli (%s)", m.socoClient.baseURL)
	}
	return "home-assistant"
}
