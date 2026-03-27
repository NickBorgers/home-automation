package music

import (
	"errors"
	"fmt"
	"time"

	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Speaker group retry configuration
const (
	// maxSpeakerGroupRetries is the maximum number of attempts to create a speaker group.
	// Sonos speaker grouping can fail due to network issues or speaker unavailability.
	// Home Assistant has a 9.5s timeout for Sonos operations, so retries help recover
	// from transient failures.
	// With exponential backoff (2s, 4s, 8s, 15s, 15s, 15s), this provides approximately
	// 59 seconds of retry coverage to handle network outages lasting up to 1 minute.
	maxSpeakerGroupRetries = 6

	// speakerGroupRetryBaseDelay is the base delay between retry attempts.
	// Uses exponential backoff: 2s, 4s, 8s, then capped at speakerGroupRetryMaxDelay.
	speakerGroupRetryBaseDelay = 2 * time.Second

	// speakerGroupRetryMaxDelay caps the exponential backoff for speaker group operations.
	speakerGroupRetryMaxDelay = 15 * time.Second

	// speakerUnjoinSettleDelay is the delay after unjoining all speakers
	// to allow the Sonos system to stabilize before forming new groups.
	speakerUnjoinSettleDelay = 500 * time.Millisecond

	// speakerUnjoinTimeout is the maximum time to wait for a single unjoin call.
	// Unjoin is best-effort cleanup — if a speaker is unresponsive, we skip it
	// rather than blocking startup or playback transitions for minutes.
	// With serial unjoin, each call is a single UPnP request (no retries),
	// so 3s is sufficient. Worst case = 6 × (3s + 500ms) + 500ms settle ≈ 21.5s.
	speakerUnjoinTimeout = 3 * time.Second

	// speakerUnjoinInterDelay is the delay between sequential unjoin calls.
	// Serializing with a short gap avoids flooding the Sonos mesh with
	// simultaneous UPnP BecomeCoordinatorOfStandaloneGroup SOAP requests,
	// which caused ~63% timeout failures when all 6 speakers were unjoined in parallel.
	speakerUnjoinInterDelay = 500 * time.Millisecond

	// speakerGroupSettleDelay is the delay after building a speaker group
	// to allow the Sonos system to stabilize before starting playback.
	speakerGroupSettleDelay = 500 * time.Millisecond

	// playbackVerificationDelay is how long to wait after sending play_media
	// before checking if playback actually started. Sonos needs time to
	// receive the command and begin playback.
	playbackVerificationDelay = 2 * time.Second

	// playbackVerificationRetries is how many times to retry play_media if
	// the speaker doesn't enter "playing" state. This handles transient failures
	// where the command is accepted but playback doesn't start.
	playbackVerificationRetries = 3

	// playbackVerificationRetryDelay is the delay between retry attempts.
	playbackVerificationRetryDelay = 3 * time.Second

	// fadeOutSteps is the number of volume steps for fade-out.
	// A quick fade-out (5 steps) provides a smooth transition without significant delay.
	fadeOutSteps = 5

	// fadeOutStepDelay is the delay between each fade-out volume step.
	// 100ms per step * 5 steps = 500ms total fade-out time.
	fadeOutStepDelay = 100 * time.Millisecond

	// asyncJoinStaggerDelay is the base delay between launching each speaker's join goroutine.
	// Staggers join attempts to reduce IGMP/multicast congestion.
	asyncJoinStaggerDelay = 15 * time.Second

	// asyncJoinJitterMax is the maximum random jitter added to stagger and retry delays.
	// Prevents multiple speakers from aligning their join attempts.
	asyncJoinJitterMax = 15 * time.Second

	// maxAsyncSpeakerRetries is the number of retry attempts for async speaker joins.
	// Each speaker retries independently in its own goroutine.
	maxAsyncSpeakerRetries = 6

	// asyncJoinRetryBaseDelay is the starting delay for join operation retries.
	// Join failures indicate congestion, so start with a substantial delay.
	asyncJoinRetryBaseDelay = 30 * time.Second

	// asyncJoinRetryMaxDelay caps the exponential backoff at 60 seconds.
	asyncJoinRetryMaxDelay = 60 * time.Second

	// playbackMonitorDuration is how long to monitor playback health after starting.
	// 3 minutes covers the observed Sonos auto-pause window (typically < 2 min).
	playbackMonitorDuration = 3 * time.Minute

	// playbackMonitorPollInterval is how often to check speaker state during monitoring.
	playbackMonitorPollInterval = 10 * time.Second

	// playbackRecoveryDelay is how long to wait before attempting recovery.
	// Prevents reacting to transient state changes.
	playbackRecoveryDelay = 2 * time.Second
)

// orchestratePlayback coordinates the complete playback flow
func (m *Manager) orchestratePlayback(musicType string, trigger string) error {
	m.logger.Info("Orchestrating playback", zap.String("type", musicType), zap.String("trigger", trigger))

	// Get the music mode configuration
	mode, ok := m.config.Music[musicType]
	if !ok {
		return fmt.Errorf("unknown music type: %s", musicType)
	}

	// Select playlist with rotation
	playlistIndex := m.getNextPlaylistIndex(musicType, len(mode.PlaybackOptions))
	playbackOption := mode.PlaybackOptions[playlistIndex]

	m.logger.Info("Selected playlist",
		zap.String("type", musicType),
		zap.Int("playlist_index", playlistIndex),
		zap.String("uri", playbackOption.URI),
		zap.Float64("volume_multiplier", playbackOption.VolumeMultiplier))

	// Set the currently playing music URI in Home Assistant
	if err := m.stateManager.SetString("currentlyPlayingMusicUri", playbackOption.URI); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping URI update in read-only mode",
				zap.String("uri", playbackOption.URI))
		} else {
			m.logger.Error("Failed to set currently playing music URI",
				zap.String("uri", playbackOption.URI),
				zap.Error(err))
		}
	}

	// Phase 1: Filter participants by exclude_if conditions first, then build with calculated volumes
	// This implements the zone assignment policy - excluded speakers won't join the Sonos group
	participants := make([]ParticipantWithVolume, 0, len(mode.Participants))
	excludedCount := 0
	for _, p := range mode.Participants {
		// Check if speaker should be included in this zone
		if !m.shouldIncludeInZone(p) {
			m.logger.Info("Speaker excluded from zone",
				zap.String("speaker", p.PlayerName),
				zap.String("music_type", musicType))
			excludedCount++
			continue // Skip this speaker - don't add to group
		}

		volume := m.calculateVolume(p.BaseVolume, playbackOption.VolumeMultiplier)
		participants = append(participants, ParticipantWithVolume{
			PlayerName:    p.PlayerName,
			BaseVolume:    p.BaseVolume,
			Volume:        volume,
			DefaultVolume: volume,
			LeaveMutedIf:  p.LeaveMutedIf,
			ExcludeIf:     p.ExcludeIf,
		})
	}

	if excludedCount > 0 {
		m.logger.Info("Zone participant filtering complete",
			zap.String("music_type", musicType),
			zap.Int("total_configured", len(mode.Participants)),
			zap.Int("included", len(participants)),
			zap.Int("excluded", excludedCount))
	}

	// Get lead player (first included participant)
	if len(participants) == 0 {
		return fmt.Errorf("no participants for music type: %s (all %d speakers excluded by exclude_if conditions)", musicType, len(mode.Participants))
	}
	leadPlayer := participants[0].PlayerName

	// Update currently playing state
	m.mu.Lock()
	m.currentlyPlaying = &CurrentlyPlayingMusic{
		Type:         musicType,
		URI:          playbackOption.URI,
		MediaType:    playbackOption.MediaType,
		LeadPlayer:   leadPlayer,
		Participants: participants,
	}
	m.mu.Unlock()

	if m.readOnly {
		m.logger.Info("Read-only mode: would start playback",
			zap.String("type", musicType),
			zap.String("lead_player", leadPlayer),
			zap.Int("participant_count", len(participants)))
		// Record shadow state even in read-only mode (nil groupResult = all active, 0 = no verification in read-only)
		m.recordPlaybackShadowState(musicType, playbackOption, participants, leadPlayer, trigger, nil, 0)
		return nil
	}

	// Execute playback sequence
	groupResult, verificationAttempts, err := m.executePlayback(musicType, playbackOption, participants, leadPlayer)
	if err != nil {
		return fmt.Errorf("failed to execute playback: %w", err)
	}

	// Record shadow state after successful playback with speaker status and verification info
	m.recordPlaybackShadowState(musicType, playbackOption, participants, leadPlayer, trigger, groupResult, verificationAttempts)

	return nil
}

// executePlayback executes the actual playback sequence.
// Returns SpeakerGroupResult indicating which speakers are active, and the number of
// verification attempts needed (1 = first try succeeded).
// Sequence matches Node-RED: break existing groups → build new group → mute → play → fade in
func (m *Manager) executePlayback(musicType string, option PlaybackOption, participants []ParticipantWithVolume, leadPlayer string) (*SpeakerGroupResult, int, error) {
	m.logger.Info("Executing playback sequence (async speaker grouping)",
		zap.String("type", musicType),
		zap.String("lead_player", leadPlayer),
		zap.Int("participant_count", len(participants)))

	// Cancel any active playback health monitor before starting new playback
	m.cancelPlaybackMonitor()

	// Cancel any active fade-ins before starting new playback
	// This prevents:
	// 1. Concurrent fade-ins on the same speaker causing volume jumping
	// 2. Old fade-ins detecting "human override" when new ones set volume to 0
	m.cancelAllFadeIns()

	// Step 0: Fade out current playback before making any changes
	// This prevents jarring audio when speakers are ungrouped/regrouped
	m.fadeOutSpeakers()

	leadEntityID := m.getSpeakerEntityID(leadPlayer)

	// Step 1: Break speakers from existing groups before starting new playback
	// This matches Node-RED behavior where stopMsg routes through "Break group for player"
	m.breakSpeakerGroups(participants)

	// Step 2: Mute LEAD speaker only before starting playback
	// Followers will be muted when they join asynchronously
	if err := m.speakerSetVolume(leadPlayer, 0); err != nil {
		m.logger.Error("Failed to mute lead speaker",
			zap.String("speaker", leadPlayer),
			zap.Error(err))
	}

	// Step 3: Start playback on lead player IMMEDIATELY (before building group)
	// This is the key change - playback starts without waiting for followers
	// IMPORTANT: Even if verification fails, we continue with fade-in. Sonos speakers
	// at volume 0 may not report "playing" state, but the play_media command was sent.
	attempts, verifyErr := m.startPlaybackWithVerification(leadEntityID, leadPlayer, option)
	playbackVerificationFailed := verifyErr != nil
	if playbackVerificationFailed {
		m.logger.Warn("Playback verification failed, continuing with fade-in anyway",
			zap.String("speaker", leadPlayer),
			zap.Int("attempts", attempts),
			zap.Error(verifyErr))
	} else if attempts > 1 {
		m.logger.Info("Playback required multiple attempts",
			zap.Int("attempts", attempts),
			zap.String("speaker", leadPlayer))
	}

	// Start post-playback health monitor to detect auto-pause
	// Only in non-read-only mode since we may need to send recovery commands
	if !m.readOnly && verifyErr == nil {
		m.startPlaybackMonitor(leadEntityID, leadPlayer, musicType)
	}

	// Step 4: Enable shuffle for playlists (Spotify and Tidal)
	if option.MediaType == "playlist" || option.MediaType == "tidal" {
		if err := m.speakerSetShuffle(leadPlayer, true); err != nil {
			m.logger.Warn("Failed to enable shuffle",
				zap.String("speaker", leadPlayer),
				zap.Error(err))
		}
	}

	// Step 5: Enable repeat for all playback types
	// Repeat ensures continuous playback, especially important for single-file
	// media like rain sounds that would otherwise stop after playing once
	if err := m.speakerSetRepeat(leadPlayer, "all"); err != nil {
		m.logger.Warn("Failed to enable repeat",
			zap.String("speaker", leadPlayer),
			zap.Error(err))
	}

	// Step 6: Start fade-in on LEAD speaker immediately
	// The lead is already playing - start bringing up its volume right away
	leadParticipant := participants[0]
	if m.shouldUnmuteSpeaker(leadParticipant) {
		m.logger.Info("Starting lead speaker fade-in",
			zap.String("speaker", leadPlayer),
			zap.Int("target_volume", leadParticipant.Volume))

		leadCtx := m.startFadeInWithContext(leadEntityID)
		go m.fadeInSpeaker(leadCtx, leadPlayer, leadParticipant.Volume, musicType)
	} else {
		// Lead speaker should stay muted, but set its target volume
		m.logger.Info("Keeping lead speaker muted, setting target volume",
			zap.String("speaker", leadPlayer),
			zap.Int("target_volume", leadParticipant.Volume))

		if err := m.speakerSetVolume(leadPlayer, leadParticipant.Volume); err != nil {
			m.logger.Error("Failed to set volume for muted lead speaker",
				zap.String("speaker", leadPlayer),
				zap.Error(err))
		}

		if err := m.speakerSetMute(leadPlayer, true); err != nil {
			m.logger.Error("Failed to mute lead speaker",
				zap.String("speaker", leadPlayer),
				zap.Error(err))
		}
	}

	// Step 7: Build speaker group ASYNCHRONOUSLY for followers
	// Each speaker joins and starts its own fade-in without blocking the main flow
	if len(participants) > 1 {
		m.logger.Info("Launching async speaker group building",
			zap.Int("followers", len(participants)-1))
		go m.buildSpeakerGroupAsync(participants, leadPlayer, musicType)
	}

	// Create result - for async mode, we only report the lead as definitely active
	// Followers are handled asynchronously and may or may not join
	groupResult := &SpeakerGroupResult{
		Results: []SpeakerResult{{
			Participant: leadParticipant,
			Active:      true,
		}},
		ActiveCount: 1,
		FailedCount: 0, // Followers handled async - failures logged but not counted here
		LeadActive:  true,
	}

	// Add pending results for followers (they'll be processed async)
	for i := 1; i < len(participants); i++ {
		groupResult.Results = append(groupResult.Results, SpeakerResult{
			Participant:   participants[i],
			Active:        false, // Will be updated async, but we report false initially
			FailureReason: "pending async join",
		})
	}

	if playbackVerificationFailed {
		m.logger.Info("Playback started with verification failure (lead fade-in attempted, followers joining async)",
			zap.String("type", musicType),
			zap.Int("verification_attempts", attempts))
	} else {
		m.logger.Info("Playback started on lead (followers joining async)",
			zap.String("type", musicType),
			zap.Int("verification_attempts", attempts))
	}

	return groupResult, attempts, nil
}

// startPlaybackWithVerification sends the play_media command and verifies playback actually starts.
// It returns the number of attempts needed (1 = first try succeeded) and any error.
// This handles the failure mode where HA accepts play_media but the speaker doesn't actually play.
// leadPlayerName is the human-readable speaker name (e.g., "Kitchen") used for SoCo-CLI commands.
func (m *Manager) startPlaybackWithVerification(leadEntityID string, leadPlayerName string, option PlaybackOption) (attempts int, err error) {
	// Tidal playlists are played via SoCo-CLI instead of HA play_media
	if option.MediaType == "tidal" {
		return m.startTidalPlayback(leadEntityID, leadPlayerName, option)
	}

	for attempt := 1; attempt <= playbackVerificationRetries; attempt++ {
		// Send play_media command (routed to SoCo play_uri or HA play_media)
		if err := m.speakerPlayMedia(leadPlayerName, option.URI, option.MediaType); err != nil {
			return attempt, fmt.Errorf("failed to send play_media: %w", err)
		}

		// Wait for speaker to start playing
		m.sleepFunc(playbackVerificationDelay)

		// Check if playback actually started (state read still goes through HA)
		playing, checkErr := m.isPlaybackActive(leadEntityID)
		if checkErr != nil {
			m.logger.Warn("Failed to verify playback state",
				zap.String("entity_id", leadEntityID),
				zap.Int("attempt", attempt),
				zap.Error(checkErr))
			// Can't verify, assume it worked (fail-open)
			return attempt, nil
		}

		if playing {
			if attempt > 1 {
				m.logger.Info("Playback started after retry",
					zap.String("entity_id", leadEntityID),
					zap.Int("attempts", attempt))
			}
			return attempt, nil
		}

		// Not playing - try sending play command as a nudge
		m.logger.Warn("Playback not started, attempting recovery",
			zap.String("entity_id", leadEntityID),
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", playbackVerificationRetries))

		// Try play as a nudge in case the speaker is paused
		if nudgeErr := m.speakerPlay(leadPlayerName); nudgeErr != nil {
			m.logger.Debug("Play nudge failed", zap.Error(nudgeErr))
		}

		// Brief wait and re-check before retry
		m.sleepFunc(1 * time.Second)
		playing, _ = m.isPlaybackActive(leadEntityID)
		if playing {
			m.logger.Info("Playback started after play nudge",
				zap.String("entity_id", leadEntityID),
				zap.Int("attempts", attempt))
			return attempt, nil
		}

		if attempt < playbackVerificationRetries {
			m.logger.Info("Waiting before retry",
				zap.Duration("delay", playbackVerificationRetryDelay))
			m.sleepFunc(playbackVerificationRetryDelay)
		}
	}

	return playbackVerificationRetries, fmt.Errorf("playback failed to start after %d attempts - speaker grouped but not playing", playbackVerificationRetries)
}

// startTidalPlayback plays a Tidal playlist via SoCo-CLI and verifies playback started.
// Uses the same verification loop as HA-based playback: wait, check, retry.
func (m *Manager) startTidalPlayback(leadEntityID string, leadPlayerName string, option PlaybackOption) (attempts int, err error) {
	if m.socoClient == nil {
		return 1, fmt.Errorf("tidal playback requested but SoCo-CLI client is not configured (set SOCO_CLI_URL)")
	}

	for attempt := 1; attempt <= playbackVerificationRetries; attempt++ {
		if err := m.socoClient.PlayShareLink(leadPlayerName, option.URI); err != nil {
			return attempt, fmt.Errorf("failed to start Tidal playback via SoCo-CLI: %w", err)
		}

		// Wait for speaker to start playing
		m.sleepFunc(playbackVerificationDelay)

		// Check if playback actually started
		playing, checkErr := m.isPlaybackActive(leadEntityID)
		if checkErr != nil {
			m.logger.Warn("Failed to verify Tidal playback state",
				zap.String("entity_id", leadEntityID),
				zap.Int("attempt", attempt),
				zap.Error(checkErr))
			// Can't verify, assume it worked (fail-open)
			return attempt, nil
		}

		if playing {
			if attempt > 1 {
				m.logger.Info("Tidal playback started after retry",
					zap.String("entity_id", leadEntityID),
					zap.Int("attempts", attempt))
			}
			return attempt, nil
		}

		m.logger.Warn("Tidal playback not started, retrying",
			zap.String("entity_id", leadEntityID),
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", playbackVerificationRetries))

		if attempt < playbackVerificationRetries {
			m.sleepFunc(playbackVerificationRetryDelay)
		}
	}

	return playbackVerificationRetries, fmt.Errorf("tidal playback failed to start after %d attempts", playbackVerificationRetries)
}
