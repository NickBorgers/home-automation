package music

import (
	"fmt"

	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// captureCurrentInputs snapshots all subscribed state variables
func (m *Manager) captureCurrentInputs() map[string]interface{} {
	inputs := make(map[string]interface{})

	// Capture all subscribed variables
	if val, err := m.stateManager.GetString("dayPhase"); err == nil && val != "" {
		inputs["dayPhase"] = val
	}
	if val, err := m.stateManager.GetBool("isAnyoneAsleep"); err == nil {
		inputs["isAnyoneAsleep"] = val
	}
	if val, err := m.stateManager.GetBool("isAnyoneHome"); err == nil {
		inputs["isAnyoneHome"] = val
	}
	if val, err := m.stateManager.GetBool("isMasterAsleep"); err == nil {
		inputs["isMasterAsleep"] = val
	}
	if val, err := m.stateManager.GetBool("isEveryoneAsleep"); err == nil {
		inputs["isEveryoneAsleep"] = val
	}
	if val, err := m.stateManager.GetBool("isWakeSequenceActive"); err == nil {
		inputs["isWakeSequenceActive"] = val
	}

	// Capture variables referenced in leave_muted_if / exclude_if conditions across all
	// music modes. These variables (e.g. isTVPlaying, isNickOfficeOccupied) control
	// per-speaker muting and must appear in shadow inputs so operators can diagnose
	// why speakers are or aren't muted (issue #998).
	for _, varName := range m.collectMuteConditionVariables() {
		if _, already := inputs[varName]; already {
			continue // Already captured above
		}
		if val, err := m.getStateValue(varName); err == nil {
			inputs[varName] = val
		}
	}

	return inputs
}

// updateShadowState records an action in the shadow state
func (m *Manager) updateShadowState(actionType, reason, trigger string) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	// Capture current inputs and add trigger
	currentInputs := m.captureCurrentInputs()
	currentInputs["trigger"] = trigger

	// If this is the first action or inputs changed, snapshot at-last-action
	if len(m.shadowState.Inputs.AtLastAction) == 0 {
		m.shadowState.Inputs.AtLastAction = currentInputs
	} else {
		// Copy current inputs to at-last-action
		m.shadowState.Inputs.AtLastAction = make(map[string]interface{})
		for k, v := range currentInputs {
			m.shadowState.Inputs.AtLastAction[k] = v
		}
	}

	// Always update current inputs
	m.shadowState.Inputs.Current = currentInputs

	// Update outputs
	m.shadowState.Outputs.LastActionTime = m.timeProvider.Now()
	m.shadowState.Outputs.LastActionType = actionType
	m.shadowState.Outputs.LastActionReason = reason

	// Update metadata
	m.shadowState.Metadata.LastUpdated = m.timeProvider.Now()
}

// updateShadowOutputs updates the output portion of shadow state
func (m *Manager) updateShadowOutputs(mode string, playlist *shadowstate.PlaylistInfo, speakers []shadowstate.SpeakerState, verification *shadowstate.PlaybackVerificationStatus) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	if mode != "" {
		m.shadowState.Outputs.CurrentMode = mode
	}
	if playlist != nil {
		m.shadowState.Outputs.ActivePlaylist = *playlist
	}
	if speakers != nil {
		m.shadowState.Outputs.SpeakerGroup = speakers
	}
	if verification != nil {
		m.shadowState.Outputs.PlaybackVerification = verification
	}

	// Copy playlist rotation state
	m.mu.RLock()
	for k, v := range m.playlistNumbers {
		m.shadowState.Outputs.PlaylistRotation[k] = v
	}
	m.mu.RUnlock()

	// Update active zones from ZoneManager (per design review feedback)
	if m.zoneManager != nil {
		activeZones := m.zoneManager.GetActiveZones()
		zoneShadowStates := make([]shadowstate.ZoneShadowState, 0, len(activeZones))
		for _, zone := range activeZones {
			participants := make([]shadowstate.SpeakerState, 0, len(zone.Participants))
			for _, p := range zone.Participants {
				participants = append(participants, shadowstate.SpeakerState{
					PlayerName:    p.PlayerName,
					Volume:        p.Volume,
					BaseVolume:    p.BaseVolume,
					DefaultVolume: p.DefaultVolume,
					IsLeader:      p.PlayerName == zone.LeadSpeaker,
					Active:        true,
				})
			}
			zoneShadowStates = append(zoneShadowStates, shadowstate.ZoneShadowState{
				Name:         zone.Name,
				MusicType:    zone.MusicType,
				Priority:     zone.Priority,
				LeadSpeaker:  zone.LeadSpeaker,
				Participants: participants,
				PlaylistURI:  zone.PlaylistURI,
				StartedAt:    zone.StartedAt,
			})
		}
		m.shadowState.Outputs.ActiveZones = zoneShadowStates
	}

	m.shadowState.Metadata.LastUpdated = m.timeProvider.Now()
}

// GetShadowState returns the current shadow state (implements ShadowStateProvider)
func (m *Manager) GetShadowState() *shadowstate.MusicShadowState {
	m.shadowMu.RLock()
	defer m.shadowMu.RUnlock()

	// Return a deep copy to avoid race conditions
	shadowCopy := *m.shadowState

	// Deep copy maps and slices
	shadowCopy.Inputs.Current = make(map[string]interface{})
	for k, v := range m.shadowState.Inputs.Current {
		shadowCopy.Inputs.Current[k] = v
	}

	shadowCopy.Inputs.AtLastAction = make(map[string]interface{})
	for k, v := range m.shadowState.Inputs.AtLastAction {
		shadowCopy.Inputs.AtLastAction[k] = v
	}

	shadowCopy.Outputs.SpeakerGroup = make([]shadowstate.SpeakerState, len(m.shadowState.Outputs.SpeakerGroup))
	copy(shadowCopy.Outputs.SpeakerGroup, m.shadowState.Outputs.SpeakerGroup)

	shadowCopy.Outputs.PlaylistRotation = make(map[string]int)
	for k, v := range m.shadowState.Outputs.PlaylistRotation {
		shadowCopy.Outputs.PlaylistRotation[k] = v
	}

	// Deep copy fade-in progress
	shadowCopy.Outputs.FadeInProgress = make(map[string]shadowstate.SpeakerFadeIn)
	for k, v := range m.shadowState.Outputs.FadeInProgress {
		shadowCopy.Outputs.FadeInProgress[k] = v
	}

	// Deep copy playback health status
	if m.shadowState.Outputs.PlaybackHealth != nil {
		healthCopy := *m.shadowState.Outputs.PlaybackHealth
		shadowCopy.Outputs.PlaybackHealth = &healthCopy
	}

	// Deep copy active zones
	if len(m.shadowState.Outputs.ActiveZones) > 0 {
		shadowCopy.Outputs.ActiveZones = make([]shadowstate.ZoneShadowState, len(m.shadowState.Outputs.ActiveZones))
		for i, zone := range m.shadowState.Outputs.ActiveZones {
			zoneCopy := zone
			zoneCopy.Participants = make([]shadowstate.SpeakerState, len(zone.Participants))
			copy(zoneCopy.Participants, zone.Participants)
			shadowCopy.Outputs.ActiveZones[i] = zoneCopy
		}
	}

	return &shadowCopy
}

// recordPlaybackMonitorStart records the start of playback health monitoring
func (m *Manager) recordPlaybackMonitorStart(leadEntityID, musicType string) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	now := m.timeProvider.Now()
	m.shadowState.Outputs.PlaybackHealth = &shadowstate.PlaybackHealthStatus{
		IsMonitoring:      true,
		MonitorStartTime:  now,
		MonitorEndTime:    now.Add(playbackMonitorDuration),
		RecoveryAttempted: false,
		LastSpeakerState:  "playing", // Assume playing since verification just passed
		LeadSpeaker:       leadEntityID,
		MusicType:         musicType,
	}
	m.shadowState.Metadata.LastUpdated = now
}

// updatePlaybackHealthState updates the last observed speaker state during monitoring
func (m *Manager) updatePlaybackHealthState(state string) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	if m.shadowState.Outputs.PlaybackHealth != nil {
		m.shadowState.Outputs.PlaybackHealth.LastSpeakerState = state
	}
}

// recordPlaybackRecoveryResult records the outcome of a recovery attempt
func (m *Manager) recordPlaybackRecoveryResult(result string) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	now := m.timeProvider.Now()
	if m.shadowState.Outputs.PlaybackHealth != nil {
		m.shadowState.Outputs.PlaybackHealth.RecoveryAttempted = true
		m.shadowState.Outputs.PlaybackHealth.RecoveryTime = now
		m.shadowState.Outputs.PlaybackHealth.RecoveryResult = result
	}
	m.shadowState.Metadata.LastUpdated = now
}

// recordPlaybackMonitorEnd records the end of playback health monitoring
func (m *Manager) recordPlaybackMonitorEnd(reason string) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	now := m.timeProvider.Now()
	if m.shadowState.Outputs.PlaybackHealth != nil {
		m.shadowState.Outputs.PlaybackHealth.IsMonitoring = false
		m.shadowState.Outputs.PlaybackHealth.MonitorEndTime = now
	}
	m.shadowState.Metadata.LastUpdated = now

	m.logger.Debug("Playback health monitor ended",
		zap.String("reason", reason))
}

// recordFadeInStart records the start of a speaker fade-in
func (m *Manager) recordFadeInStart(speakerName, entityID string, targetVolume int) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	now := m.timeProvider.Now()
	m.shadowState.Outputs.FadeInProgress[entityID] = shadowstate.SpeakerFadeIn{
		SpeakerName:     speakerName,
		SpeakerEntityID: entityID,
		CurrentVolume:   0,
		TargetVolume:    targetVolume,
		IsActive:        true,
		StartTime:       now,
		LastUpdate:      now,
	}
	m.shadowState.Outputs.FadeState = "fading_in"
	m.shadowState.Metadata.LastUpdated = now
}

// updateFadeInProgress updates the fade-in progress for a speaker
func (m *Manager) updateFadeInProgress(entityID string, currentVolume int) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	if fadeIn, exists := m.shadowState.Outputs.FadeInProgress[entityID]; exists {
		fadeIn.CurrentVolume = currentVolume
		fadeIn.LastUpdate = m.timeProvider.Now()
		if currentVolume >= fadeIn.TargetVolume {
			fadeIn.IsActive = false
		}
		m.shadowState.Outputs.FadeInProgress[entityID] = fadeIn
	}
	m.shadowState.Metadata.LastUpdated = m.timeProvider.Now()
}

// recordFadeInHumanOverride records that a human override was detected during fade-in
// Only sets global FadeState to "idle" when ALL fade-ins are inactive
func (m *Manager) recordFadeInHumanOverride(entityID string, expectedVolume, actualVolume int) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	if fadeIn, exists := m.shadowState.Outputs.FadeInProgress[entityID]; exists {
		fadeIn.HumanOverrideDetected = true
		fadeIn.ExpectedVolume = expectedVolume
		fadeIn.ActualVolume = actualVolume
		fadeIn.IsActive = false
		fadeIn.LastUpdate = m.timeProvider.Now()
		m.shadowState.Outputs.FadeInProgress[entityID] = fadeIn
	}

	// Only set global state to idle if NO fade-ins are still active
	anyActive := false
	for _, fadeIn := range m.shadowState.Outputs.FadeInProgress {
		if fadeIn.IsActive {
			anyActive = true
			break
		}
	}
	if !anyActive {
		m.shadowState.Outputs.FadeState = "idle"
	}

	m.shadowState.Metadata.LastUpdated = m.timeProvider.Now()
}

// clearFadeInProgress marks a speaker's fade-in as complete
// Only sets global FadeState to "idle" when ALL fade-ins are inactive
func (m *Manager) clearFadeInProgress(entityID string) {
	m.shadowMu.Lock()
	defer m.shadowMu.Unlock()

	if fadeIn, exists := m.shadowState.Outputs.FadeInProgress[entityID]; exists {
		fadeIn.IsActive = false
		fadeIn.LastUpdate = m.timeProvider.Now()
		m.shadowState.Outputs.FadeInProgress[entityID] = fadeIn
	}

	// Only set global state to idle if NO fade-ins are still active
	anyActive := false
	for _, fadeIn := range m.shadowState.Outputs.FadeInProgress {
		if fadeIn.IsActive {
			anyActive = true
			break
		}
	}
	if !anyActive {
		m.shadowState.Outputs.FadeState = "idle"
	}

	m.shadowState.Metadata.LastUpdated = m.timeProvider.Now()
}

// recordPlaybackShadowState records shadow state after playback orchestration.
// groupResult can be nil (for read-only mode or single speaker), in which case
// all participants are assumed to be active.
// verificationAttempts indicates how many attempts were needed to verify playback started (0 = not verified/read-only).
func (m *Manager) recordPlaybackShadowState(musicType string, playbackOption PlaybackOption, participants []ParticipantWithVolume, leadPlayer string, trigger string, groupResult *SpeakerGroupResult, verificationAttempts int) {
	// Convert participants to shadow state speaker format
	speakers := make([]shadowstate.SpeakerState, 0, len(participants))

	if groupResult != nil {
		// Use the group result to populate active status
		for _, sr := range groupResult.Results {
			speakers = append(speakers, shadowstate.SpeakerState{
				PlayerName:    sr.Participant.PlayerName,
				Volume:        sr.Participant.Volume,
				BaseVolume:    sr.Participant.BaseVolume,
				DefaultVolume: sr.Participant.DefaultVolume,
				IsLeader:      sr.Participant.PlayerName == leadPlayer,
				Active:        sr.Active,
				FailureReason: sr.FailureReason,
			})
		}
	} else {
		// No group result - assume all participants are active
		for _, p := range participants {
			speakers = append(speakers, shadowstate.SpeakerState{
				PlayerName:    p.PlayerName,
				Volume:        p.Volume,
				BaseVolume:    p.BaseVolume,
				DefaultVolume: p.DefaultVolume,
				IsLeader:      p.PlayerName == leadPlayer,
				Active:        true,
			})
		}
	}

	// Create playlist info
	playlistInfo := &shadowstate.PlaylistInfo{
		URI:       playbackOption.URI,
		Name:      "", // Name is not available in PlaybackOption
		MediaType: playbackOption.MediaType,
	}

	// Build reason message with partial group info if applicable
	var reason string
	if groupResult != nil && groupResult.FailedCount > 0 {
		reason = fmt.Sprintf("Started playback of '%s' in mode '%s' (partial group: %d/%d speakers active)",
			playbackOption.URI, musicType, groupResult.ActiveCount, len(participants))
	} else {
		reason = fmt.Sprintf("Started playback of '%s' in mode '%s'", playbackOption.URI, musicType)
	}

	// Build verification status (nil for read-only mode where verificationAttempts is 0)
	var verification *shadowstate.PlaybackVerificationStatus
	if verificationAttempts > 0 {
		verification = &shadowstate.PlaybackVerificationStatus{
			Verified:       true,
			AttemptsNeeded: verificationAttempts,
			FinalState:     "playing",
			VerifiedAt:     m.timeProvider.Now(),
			LeadSpeaker:    m.getSpeakerEntityID(leadPlayer),
		}
	}

	m.updateShadowState("start_playback", reason, trigger)
	m.updateShadowOutputs(musicType, playlistInfo, speakers, verification)
}
