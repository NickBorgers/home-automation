package music

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// fadeOutSpeakers performs a quick fade-out on all currently playing speakers.
// This prevents jarring audio when changing playback modes or stopping music.
// The fade-out is intentionally quick (500ms total) to avoid delaying transitions.
func (m *Manager) fadeOutSpeakers() {
	m.mu.RLock()
	currentlyPlaying := m.currentlyPlaying
	m.mu.RUnlock()

	if currentlyPlaying == nil || len(currentlyPlaying.Participants) == 0 {
		m.logger.Debug("No active playback to fade out")
		return
	}

	m.logger.Info("Starting quick fade-out before playback change",
		zap.Int("speaker_count", len(currentlyPlaying.Participants)))

	// Get current volumes for each speaker to fade from
	// Key is speaker name for SoCo routing; HA state reads use entity IDs
	speakerVolumes := make(map[string]int)
	for _, p := range currentlyPlaying.Participants {
		entityID := m.getSpeakerEntityID(p.PlayerName)
		currentVolume := m.getSpeakerVolume(entityID)
		if currentVolume > 0 {
			speakerVolumes[p.PlayerName] = currentVolume
		}
	}

	if len(speakerVolumes) == 0 {
		m.logger.Debug("All speakers already at volume 0, skipping fade-out")
		return
	}

	// Fade out in steps
	for step := 1; step <= fadeOutSteps; step++ {
		// Calculate progress (1.0 at start, 0.0 at end)
		progress := float64(fadeOutSteps-step) / float64(fadeOutSteps)

		for speakerName, startVolume := range speakerVolumes {
			targetVolume := int(float64(startVolume) * progress)

			if err := m.speakerSetVolume(speakerName, targetVolume); err != nil {
				m.logger.Debug("Failed to set volume during fade-out",
					zap.String("speaker", speakerName),
					zap.Int("step", step),
					zap.Error(err))
				// Continue with other speakers even if one fails
			}
		}

		// Don't sleep after the last step
		if step < fadeOutSteps {
			m.sleepFunc(fadeOutStepDelay)
		}
	}

	m.logger.Info("Fade-out complete")
}

// breakSpeakerGroups unjoins all participants from their existing groups.
// This must be called before building a new speaker group to ensure speakers
// aren't already grouped together in unpredictable ways.
// Matches Node-RED behavior: "Break group for player" -> player.become.standalone
//
// Unjoin calls run concurrently with a short timeout (speakerUnjoinTimeout) to
// prevent unresponsive speakers from blocking startup or playback transitions.
// When speakers are systematically unreachable (e.g., HA Sonos integration issues),
// sequential unjoin with full retries could block for 18+ minutes (6 speakers ×
// 3 min retry budget each), causing HA to close the websocket and crash the app.
// See: https://github.com/NickBorgers/home-automation/issues/787
func (m *Manager) breakSpeakerGroups(participants []ParticipantWithVolume) {
	m.logger.Info("Breaking existing speaker groups before building new group",
		zap.Int("participant_count", len(participants)))

	// Unjoin all speakers concurrently with a short timeout per speaker.
	// Each unjoin is independent and best-effort — if a speaker is unreachable,
	// we log a warning and continue rather than blocking for minutes of retries.
	var wg sync.WaitGroup
	for _, p := range participants {
		wg.Add(1)
		go func(p ParticipantWithVolume) {
			defer wg.Done()

			m.logger.Debug("Unjoining speaker from existing group",
				zap.String("speaker", p.PlayerName))

			// Use best-effort call with short timeout instead of full retry.
			// This limits each unjoin to ~15s instead of ~3 minutes of retries.
			if err := m.speakerUnjoinBestEffort(p.PlayerName, speakerUnjoinTimeout); err != nil {
				// Log warning but continue - speaker might not be in a group
				// or might be temporarily unreachable
				m.logger.Warn("Failed to unjoin speaker (best-effort, continuing)",
					zap.String("speaker", p.PlayerName),
					zap.Error(err))
			}
		}(p)
	}
	wg.Wait()

	// Allow time for Sonos to process the unjoin commands before building new group
	m.sleepFunc(speakerUnjoinSettleDelay)

	m.logger.Info("Finished breaking existing speaker groups")
}

// buildSpeakerGroup creates a Sonos speaker group with retry logic.
// Returns a SpeakerGroupResult indicating which speakers successfully joined.
// Continues with partial group if some speakers are unavailable.
// Only fails entirely if lead speaker is unavailable or all speakers fail.
func (m *Manager) buildSpeakerGroup(participants []ParticipantWithVolume, leadSpeakerName string) (*SpeakerGroupResult, error) {
	m.logger.Info("Building speaker group", zap.Int("count", len(participants)))

	result := &SpeakerGroupResult{
		Results:     make([]SpeakerResult, len(participants)),
		LeadActive:  true,
		ActiveCount: 0,
		FailedCount: 0,
	}

	// Initialize all participants as active (will mark failed ones as we go)
	for i, p := range participants {
		result.Results[i] = SpeakerResult{
			Participant: p,
			Active:      true,
		}
	}

	// If only one participant (lead only), no grouping needed
	if len(participants) <= 1 {
		if len(participants) == 1 {
			result.ActiveCount = 1
		}
		return result, nil
	}

	// Build list of follower names for batch join
	var followerNames []string
	for i, p := range participants {
		if i == 0 {
			continue // Skip lead player
		}
		followerNames = append(followerNames, p.PlayerName)
	}

	// First attempt: try to add all speakers at once (most efficient)
	allSucceeded := false
	var lastErr error

	for attempt := 1; attempt <= maxSpeakerGroupRetries; attempt++ {
		err := m.speakerJoinGroupBatch(leadSpeakerName, followerNames)

		if err == nil {
			allSucceeded = true
			if attempt > 1 {
				m.logger.Info("Speaker group created after retry",
					zap.String("lead", leadSpeakerName),
					zap.Strings("members", followerNames),
					zap.Int("attempt", attempt))
			} else {
				m.logger.Info("Speaker group created",
					zap.String("lead", leadSpeakerName),
					zap.Strings("members", followerNames))
			}
			break
		}

		lastErr = err

		// Check for permanent errors - no point retrying batch, try individual speakers
		if isPermanentSpeakerError(err) {
			m.logger.Warn("Batch group creation has permanent error, trying individual joins",
				zap.Error(err))
			break
		}

		if attempt < maxSpeakerGroupRetries {
			retryDelay := speakerGroupRetryBaseDelay * time.Duration(1<<(attempt-1))
			if retryDelay > speakerGroupRetryMaxDelay {
				retryDelay = speakerGroupRetryMaxDelay
			}
			m.logger.Warn("Failed to create speaker group, retrying",
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", maxSpeakerGroupRetries),
				zap.Duration("retry_delay", retryDelay),
				zap.Error(err))
			m.sleepFunc(retryDelay)
		}
	}

	if allSucceeded {
		// All speakers joined successfully
		result.ActiveCount = len(participants)
		m.sleepFunc(speakerGroupSettleDelay)
		return result, nil
	}

	// Group creation failed - try adding speakers individually to form partial group
	m.logger.Warn("Full group creation failed, attempting to build partial group",
		zap.Error(lastErr))

	// Track how many followers we successfully add
	// Lead is only considered active if at least one follower joins
	// (which proves the lead is responsive)
	followersJoined := 0

	// Try each follower individually
	for i := 1; i < len(participants); i++ {
		p := participants[i]
		speakerJoined := false

		for attempt := 1; attempt <= maxSpeakerGroupRetries; attempt++ {
			err := m.speakerJoinGroup(p.PlayerName, leadSpeakerName)

			if err == nil {
				speakerJoined = true
				m.logger.Info("Speaker joined group individually",
					zap.String("speaker", p.PlayerName),
					zap.Int("attempt", attempt))
				break
			}

			// Check for permanent errors that won't resolve with retries
			if isPermanentSpeakerError(err) {
				result.Results[i].Active = false
				result.Results[i].FailureReason = err.Error()
				result.FailedCount++
				m.logger.Warn("Speaker has permanent error, skipping retries",
					zap.String("speaker", p.PlayerName),
					zap.Error(err))
				break
			}

			if attempt < maxSpeakerGroupRetries {
				retryDelay := speakerGroupRetryBaseDelay * time.Duration(1<<(attempt-1))
				if retryDelay > speakerGroupRetryMaxDelay {
					retryDelay = speakerGroupRetryMaxDelay
				}
				m.logger.Warn("Failed to add speaker to group, retrying",
					zap.String("speaker", p.PlayerName),
					zap.Int("attempt", attempt),
					zap.Int("max_attempts", maxSpeakerGroupRetries),
					zap.Duration("retry_delay", retryDelay),
					zap.Error(err))
				m.sleepFunc(retryDelay)
			} else {
				// Mark this speaker as failed
				result.Results[i].Active = false
				result.Results[i].FailureReason = err.Error()
				result.FailedCount++
				m.logger.Warn("Speaker unavailable, continuing without it",
					zap.String("speaker", p.PlayerName),
					zap.Error(err))
			}
		}

		if speakerJoined {
			followersJoined++
		}
	}

	// Check if any followers joined - this also verifies the lead is responsive
	// If all followers failed, we can't verify the lead is working, so fail entirely
	if followersJoined == 0 {
		result.LeadActive = false
		result.ActiveCount = 0
		// Mark lead as failed too since we couldn't verify it works
		result.Results[0].Active = false
		result.Results[0].FailureReason = "could not verify lead speaker - all join attempts failed"
		result.FailedCount = len(participants)
		return result, fmt.Errorf("failed to create speaker group: all speakers unavailable (batch and individual joins failed)")
	}

	// At least one follower joined, so lead is verified working
	result.ActiveCount = 1 + followersJoined // lead + successful followers

	if result.FailedCount > 0 {
		m.logger.Warn("Proceeding with partial speaker group",
			zap.Int("active_speakers", result.ActiveCount),
			zap.Int("failed_speakers", result.FailedCount))
	}

	// Wait for group to stabilize
	m.sleepFunc(speakerGroupSettleDelay)

	return result, nil
}

// isPermanentSpeakerError returns true if the error indicates a permanent failure
// that won't be resolved by retrying (e.g., speaker not found, not a Sonos speaker).
func isPermanentSpeakerError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// These errors indicate the speaker is not available and retrying won't help
	permanentErrors := []string{
		"not a known Sonos speaker",
		"entity not found",
		"unknown entity",
		"does not exist",
	}
	for _, pe := range permanentErrors {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(pe)) {
			return true
		}
	}
	return false
}

// randomJitter returns a random duration between 0 and asyncJoinJitterMax.
// Used to prevent speakers from aligning their join attempts.
func (m *Manager) randomJitter() time.Duration {
	return time.Duration(rand.Int64N(int64(asyncJoinJitterMax)))
}

// asyncJoinConcurrencyLimit limits concurrent speaker joins to reduce IGMP congestion.
// Sonos speakers use IGMP for group formation, and too many concurrent joins can overwhelm
// the network, causing failures or delays.
const asyncJoinConcurrencyLimit = 3

// buildSpeakerGroupAsync adds follower speakers to the lead speaker's group asynchronously.
// Each speaker runs in its own goroutine with staggered start times to reduce IGMP congestion.
// This runs in a goroutine and does not block playback on the lead speaker.
// The musicType parameter is used for fade-in duration selection.
//
// Uses errgroup for structured concurrency with bounded parallelism. Individual speaker
// join failures are logged but don't stop other speakers from joining (partial success).
func (m *Manager) buildSpeakerGroupAsync(participants []ParticipantWithVolume, leadSpeakerName string, musicType string) {
	followers := len(participants) - 1
	m.logger.Info("Starting async speaker group building",
		zap.String("lead", leadSpeakerName),
		zap.Int("followers", followers))

	// Use errgroup for structured concurrency with bounded parallelism
	g := new(errgroup.Group)
	g.SetLimit(asyncJoinConcurrencyLimit) // Limit concurrent speaker joins to reduce IGMP congestion

	// Launch a goroutine for each follower with staggered start times + jitter
	for i := 1; i < len(participants); i++ {
		p := participants[i]
		// Base stagger + random jitter to prevent alignment
		staggerDelay := time.Duration(i-1)*asyncJoinStaggerDelay + m.randomJitter()

		g.Go(func() error {
			// Wait for staggered start
			if staggerDelay > 0 {
				m.logger.Info("Waiting before join attempt",
					zap.String("speaker", p.PlayerName),
					zap.Duration("delay", staggerDelay))
				m.sleepFunc(staggerDelay)
			}

			return m.joinSpeakerWithRetry(p, leadSpeakerName, musicType)
		})
	}

	// Wait for all goroutines to complete
	// Note: We don't check the error here because individual speaker failures
	// are logged by joinSpeakerWithRetry and we want partial success
	if err := g.Wait(); err != nil {
		m.logger.Debug("Some speakers failed to join (partial success is expected)",
			zap.Error(err))
	}

	m.logger.Info("Async speaker group building complete")
}

// joinSpeakerWithRetry attempts to join a single speaker to the group with retries.
// Called from goroutines in buildSpeakerGroupAsync.
// Returns an error if the speaker fails to join after all retries.
func (m *Manager) joinSpeakerWithRetry(p ParticipantWithVolume, leadSpeakerName string, musicType string) error {
	entityID := m.getSpeakerEntityID(p.PlayerName)

	var joinErr error
	for attempt := 1; attempt <= maxAsyncSpeakerRetries; attempt++ {
		joinErr = m.speakerJoinGroup(p.PlayerName, leadSpeakerName)

		if joinErr == nil {
			m.logger.Info("Speaker joined group (async)",
				zap.String("speaker", p.PlayerName),
				zap.Int("attempt", attempt))
			break
		}

		// Check for permanent errors that won't resolve with retries
		if isPermanentSpeakerError(joinErr) {
			m.logger.Warn("Speaker has permanent error, skipping retries",
				zap.String("speaker", p.PlayerName),
				zap.Error(joinErr))
			break
		}

		if attempt < maxAsyncSpeakerRetries {
			// Exponential backoff with jitter to prevent alignment
			retryDelay := asyncJoinRetryBaseDelay * time.Duration(1<<(attempt-1))
			if retryDelay > asyncJoinRetryMaxDelay {
				retryDelay = asyncJoinRetryMaxDelay
			}
			retryDelay += m.randomJitter() // Add jitter to prevent alignment
			m.logger.Warn("Failed to add speaker to group (async), retrying",
				zap.String("speaker", p.PlayerName),
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", maxAsyncSpeakerRetries),
				zap.Duration("retry_delay", retryDelay),
				zap.Error(joinErr))
			m.sleepFunc(retryDelay)
		}
	}

	if joinErr != nil {
		m.logger.Warn("Speaker unavailable (async), skipping",
			zap.String("speaker", p.PlayerName),
			zap.Error(joinErr))
		return fmt.Errorf("speaker %s failed to join: %w", p.PlayerName, joinErr)
	}

	// Speaker joined successfully - check if it should be unmuted and start fade-in
	if m.shouldUnmuteSpeaker(p) {
		m.logger.Info("Speaker joined (async), starting fade-in",
			zap.String("speaker", p.PlayerName),
			zap.Int("target_volume", p.Volume))

		ctx := m.startFadeInWithContext(entityID)
		go m.fadeInSpeaker(ctx, p.PlayerName, p.Volume, musicType)
	} else {
		// Speaker should stay muted, but set its target volume
		m.logger.Info("Speaker joined (async), keeping muted",
			zap.String("speaker", p.PlayerName),
			zap.Int("target_volume", p.Volume))

		if err := m.speakerSetVolume(p.PlayerName, p.Volume); err != nil {
			m.logger.Error("Failed to set volume for muted speaker (async)",
				zap.String("speaker", p.PlayerName),
				zap.Error(err))
		}

		if err := m.speakerSetMute(p.PlayerName, true); err != nil {
			m.logger.Error("Failed to mute speaker (async)",
				zap.String("speaker", p.PlayerName),
				zap.Error(err))
		}
	}

	return nil
}

// shouldIncludeInZone determines if a speaker should be included in the zone at all.
// This evaluates exclude_if conditions - if any condition matches, the speaker is excluded
// from the zone entirely and will not join the Sonos group.
// This is different from shouldUnmuteSpeaker which controls muting within a zone.
func (m *Manager) shouldIncludeInZone(participant Participant) bool {
	// If no exclude conditions, always include
	if len(participant.ExcludeIf) == 0 {
		return true
	}

	// Check each exclude condition
	for _, condition := range participant.ExcludeIf {
		// Get the state variable value
		value, err := m.getStateValue(condition.Variable)
		if err != nil {
			m.logger.Error("Failed to get state variable for exclude condition",
				zap.String("variable", condition.Variable),
				zap.String("speaker", participant.PlayerName),
				zap.Error(err))
			continue
		}

		// Check if condition matches (should be excluded)
		if m.valuesMatch(value, condition.Value) {
			m.logger.Debug("Exclude condition matched, speaker excluded from zone",
				zap.String("speaker", participant.PlayerName),
				zap.String("variable", condition.Variable),
				zap.Any("value", value),
				zap.Any("condition", condition.Value))
			return false // Exclude from zone
		}
	}

	// No conditions matched, include in zone
	return true
}

// shouldUnmuteSpeaker determines if a speaker should be unmuted based on conditions
func (m *Manager) shouldUnmuteSpeaker(participant ParticipantWithVolume) bool {
	// If no mute conditions, always unmute
	if len(participant.LeaveMutedIf) == 0 {
		return true
	}

	// Check each mute condition
	for _, condition := range participant.LeaveMutedIf {
		// Get the state variable value
		value, err := m.getStateValue(condition.Variable)
		if err != nil {
			m.logger.Error("Failed to get state variable for mute condition",
				zap.String("variable", condition.Variable),
				zap.Error(err))
			continue
		}

		// Check if condition matches (should stay muted)
		if m.valuesMatch(value, condition.Value) {
			m.logger.Debug("Mute condition matched",
				zap.String("variable", condition.Variable),
				zap.Any("value", value),
				zap.Any("condition", condition.Value))
			return false // Stay muted
		}
	}

	// No conditions matched, unmute
	return true
}

// humanOverrideThreshold is the volume difference (in percentage points) that indicates
// a human is manually adjusting the speaker volume during an automated fade operation.
// Using 1% because Sonos physical controls change volume by exactly 1% per input,
// so a single button press should be enough to signal the user is fighting the fade.
const humanOverrideThreshold = 1

// cancelAllFadeIns cancels all active fade-in goroutines.
// This should be called before starting new fade-ins to prevent:
// 1. Concurrent fade-ins on the same speaker causing volume jumping
// 2. Old fade-ins detecting "human override" when new ones set volume to 0
func (m *Manager) cancelAllFadeIns() {
	m.fadeInContextsMu.Lock()
	defer m.fadeInContextsMu.Unlock()

	if len(m.fadeInContexts) == 0 {
		return
	}

	m.logger.Info("Cancelling active fade-ins before new playback",
		zap.Int("count", len(m.fadeInContexts)))

	for entityID, cancel := range m.fadeInContexts {
		m.logger.Debug("Cancelling fade-in for speaker",
			zap.String("entity_id", entityID))
		cancel()
	}

	// Clear the map - new fade-ins will add themselves
	m.fadeInContexts = make(map[string]context.CancelFunc)
}

// startFadeInWithContext creates a context for a fade-in goroutine and registers it.
// Returns the context to pass to fadeInSpeaker.
// The fade-in should be cancelled when a new playback sequence starts.
func (m *Manager) startFadeInWithContext(entityID string) context.Context {
	m.fadeInContextsMu.Lock()
	defer m.fadeInContextsMu.Unlock()

	// Cancel any existing fade-in for this speaker
	if cancel, exists := m.fadeInContexts[entityID]; exists {
		m.logger.Debug("Cancelling existing fade-in for speaker",
			zap.String("entity_id", entityID))
		cancel()
	}

	// Create new context
	ctx, cancel := context.WithCancel(context.Background())
	m.fadeInContexts[entityID] = cancel

	return ctx
}

// unregisterFadeIn removes the fade-in context registration when fade-in completes.
// This should be called when a fade-in finishes (successfully or due to abort).
func (m *Manager) unregisterFadeIn(entityID string) {
	m.fadeInContextsMu.Lock()
	defer m.fadeInContextsMu.Unlock()

	delete(m.fadeInContexts, entityID)
}

// fadeInSpeaker gradually increases speaker volume.
// The ctx parameter allows cancellation when a new playback sequence starts.
// When cancelled, the function exits gracefully without logging "human override".
func (m *Manager) fadeInSpeaker(ctx context.Context, speakerName string, targetVolume int, startingMusicType string) {
	m.logger.Debug("Starting fade-in",
		zap.String("speaker", speakerName),
		zap.Int("target_volume", targetVolume))

	entityID := m.getSpeakerEntityID(speakerName)

	// Ensure we unregister the fade-in when done (unless already cancelled/unregistered)
	defer m.unregisterFadeIn(entityID)

	// Check if already cancelled before starting
	select {
	case <-ctx.Done():
		m.logger.Debug("Fade-in cancelled before start",
			zap.String("speaker", speakerName))
		return
	default:
	}

	// Record fade-in start in shadow state
	m.recordFadeInStart(speakerName, entityID, targetVolume)

	// SAFETY: Set volume to 0 BEFORE unmuting to prevent sudden loud noise.
	// If the speaker was previously at high volume and muted, unmuting without
	// lowering volume first would cause an immediate loud playback.
	if err := m.speakerSetVolume(speakerName, 0); err != nil {
		m.logger.Error("Failed to set initial volume before unmute",
			zap.String("speaker", speakerName),
			zap.Error(err))
		m.clearFadeInProgress(entityID)
		return
	}

	// Now safe to unmute - Sonos maintains mute state independently of volume
	if err := m.speakerSetMute(speakerName, false); err != nil {
		m.logger.Error("Failed to unmute speaker before fade-in",
			zap.String("speaker", speakerName),
			zap.Error(err))
		m.clearFadeInProgress(entityID)
		return
	}

	// Track failures for better error reporting
	var consecutiveFailures int
	var totalFailures int
	var lastSuccessfulVolume int = -1
	const maxConsecutiveFailures = 3

	// Gradual fade-in: 1 → targetVolume (volume 0 already set above)
	// Starting at 1 means the first audible volume bump happens immediately after
	// joining, making it easier for humans to override by lowering volume to 0.
	for currentVolume := 1; currentVolume <= targetVolume; currentVolume++ {
		// Check for context cancellation (new playback started)
		select {
		case <-ctx.Done():
			m.logger.Info("Fade-in cancelled due to new playback sequence",
				zap.String("speaker", speakerName),
				zap.Int("stopped_at_volume", currentVolume))
			m.clearFadeInProgress(entityID)
			return
		default:
		}

		// Check if this speaker's zone is still active.
		// musicPlaybackType is a single global variable that can only represent one
		// zone. When multiple zones start simultaneously, the last zone to call
		// setMusicPlaybackType wins, causing all other zones' fade-ins to see a
		// mismatch and abort (issue #772, #777). We ONLY check via the zone manager's
		// IsZoneActive(), which correctly tracks all concurrent zones independently.
		//
		// When zoneManager is nil (unit tests that don't set up zones), we skip this
		// check entirely — context cancellation (cancelAllFadeIns) already provides
		// adequate protection against stale fade-ins when new playback starts.
		if m.zoneManager != nil && !m.zoneManager.IsZoneActive(startingMusicType) {
			m.logger.Info("Zone no longer active during fade-in, stopping",
				zap.String("speaker", speakerName),
				zap.String("zone", startingMusicType))
			m.clearFadeInProgress(entityID)
			return
		}

		// Set volume (routed to SoCo or HA)
		if err := m.speakerSetVolume(speakerName, currentVolume); err != nil {
			consecutiveFailures++
			totalFailures++
			m.logger.Error("Failed to set volume during fade-in",
				zap.String("speaker", speakerName),
				zap.Int("volume", currentVolume),
				zap.Int("consecutive_failures", consecutiveFailures),
				zap.Error(err))

			// If too many consecutive failures, abort the fade-in
			if consecutiveFailures >= maxConsecutiveFailures {
				m.logger.Error("Aborting fade-in due to repeated failures",
					zap.String("speaker", speakerName),
					zap.Int("target_volume", targetVolume),
					zap.Int("last_successful_volume", lastSuccessfulVolume),
					zap.Int("total_failures", totalFailures))
				m.clearFadeInProgress(entityID)
				return
			}
		} else {
			consecutiveFailures = 0
			lastSuccessfulVolume = currentVolume
		}

		// Update shadow state with current progress
		m.updateFadeInProgress(entityID, currentVolume)

		// Adaptive delay: slower at start, faster as volume increases
		// Matches Node-RED behavior: msg.delay = (100 - current_volume) * 250
		// At volume 0%: 25s delay, at 50%: 12.5s, at 90%: 2.5s, at 99%: 250ms
		delayMs := (100 - currentVolume) * 250 // 250ms per percentage point remaining
		if delayMs < 250 {
			delayMs = 250 // Minimum 250ms between steps
		}
		m.sleepFunc(time.Duration(delayMs) * time.Millisecond)

		// Check for context cancellation after sleep (catches cancellation during delay)
		select {
		case <-ctx.Done():
			m.logger.Info("Fade-in cancelled due to new playback sequence",
				zap.String("speaker", speakerName),
				zap.Int("stopped_at_volume", currentVolume))
			m.clearFadeInProgress(entityID)
			return
		default:
		}

		// Human override detection: check if someone manually lowered volume during fade-in
		// Only check if we successfully set volume and not at the start (volume 0)
		// IMPORTANT: Skip this check if context is cancelled - the volume change was from
		// the new playback sequence, not a human
		if lastSuccessfulVolume > 0 && currentVolume < targetVolume {
			actualVolume := m.getSpeakerVolume(entityID)
			// Skip check if we couldn't get the volume (returns -1)
			// For fade-in: if actual volume is significantly LOWER than what we set,
			// someone is fighting the fade-in (turning it down)
			// Note: Using difference check (currentVolume - actualVolume) > threshold
			// instead of actualVolume < (currentVolume - threshold) for clarity,
			// though they are mathematically equivalent for non-negative values.
			if actualVolume >= 0 && (currentVolume-actualVolume) > humanOverrideThreshold {
				// Double-check context hasn't been cancelled - if it was, this is NOT a human override
				select {
				case <-ctx.Done():
					m.logger.Info("Fade-in cancelled due to new playback sequence (volume change was from new playback)",
						zap.String("speaker", speakerName),
						zap.Int("stopped_at_volume", currentVolume))
					m.clearFadeInProgress(entityID)
					return
				default:
				}

				m.logger.Info("Human override detected during fade-in, aborting",
					zap.String("speaker", speakerName),
					zap.Int("expected_volume", currentVolume),
					zap.Int("actual_volume", actualVolume))
				m.recordFadeInHumanOverride(entityID, currentVolume, actualVolume)
				return
			}
		}
	}

	// Log completion with failure summary if any failures occurred
	if totalFailures > 0 {
		m.logger.Warn("Fade-in completed with some failures",
			zap.String("speaker", speakerName),
			zap.Int("final_volume", targetVolume),
			zap.Int("total_failures", totalFailures))
	} else {
		m.logger.Info("Fade-in completed",
			zap.String("speaker", speakerName),
			zap.Int("final_volume", targetVolume))
	}

	// Mark fade-in as complete
	m.clearFadeInProgress(entityID)
}

// getSpeakerVolume queries the current volume from Home Assistant
// Returns volume as percentage (0-100)
func (m *Manager) getSpeakerVolume(speakerEntityID string) int {
	state, err := m.haClient.GetState(speakerEntityID)
	if err != nil {
		m.logger.Warn("Failed to get speaker state for override detection",
			zap.String("speaker", speakerEntityID),
			zap.Error(err))
		return -1 // Return -1 to indicate failure, caller should handle
	}

	// Get volume_level attribute (0.0-1.0)
	volumeLevel, ok := state.Attributes["volume_level"].(float64)
	if !ok {
		m.logger.Warn("Speaker has no volume_level attribute",
			zap.String("speaker", speakerEntityID))
		return -1
	}

	// Convert to percentage (0-100)
	volume := int(volumeLevel * 100)
	return volume
}

// getSpeakerEntityID converts speaker name to Home Assistant entity ID
func (m *Manager) getSpeakerEntityID(speakerName string) string {
	// Convert "Kitchen" to "media_player.kitchen"
	// Simple conversion - assumes lowercase, spaces to underscores
	entityName := ""
	for _, char := range speakerName {
		if char == ' ' {
			entityName += "_"
		} else {
			entityName += string(char)
		}
	}
	// Convert to lowercase
	entityName = toLower(entityName)
	return "media_player." + entityName
}

// toLower converts a string to lowercase
func toLower(s string) string {
	result := ""
	for _, char := range s {
		if char >= 'A' && char <= 'Z' {
			result += string(char + 32)
		} else {
			result += string(char)
		}
	}
	return result
}
