package music

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// CurrentlyPlayingMusic represents the currently active music playback
type CurrentlyPlayingMusic struct {
	Type         string                  `json:"type"`
	URI          string                  `json:"uri"`
	MediaType    string                  `json:"media_type"`
	LeadPlayer   string                  `json:"leadPlayer"`
	Participants []ParticipantWithVolume `json:"participants"`
}

// ParticipantWithVolume represents a speaker with calculated volume
type ParticipantWithVolume struct {
	PlayerName    string          `json:"player_name"`
	BaseVolume    int             `json:"base_volume"`
	Volume        int             `json:"volume"`
	DefaultVolume int             `json:"default_volume"`
	LeaveMutedIf  []MuteCondition `json:"leave_muted_if"`
	ExcludeIf     []MuteCondition `json:"exclude_if"` // Phase 1: Zone exclusion conditions
}

// SpeakerResult tracks whether a speaker successfully joined the group
type SpeakerResult struct {
	Participant   ParticipantWithVolume
	Active        bool
	FailureReason string
}

// SpeakerGroupResult holds the results of building a speaker group
type SpeakerGroupResult struct {
	Results     []SpeakerResult // All speakers with their join status
	ActiveCount int             // Number of speakers that successfully joined
	FailedCount int             // Number of speakers that failed to join
	LeadActive  bool            // Whether the lead speaker is available
}

// SleepFunc is a function type for sleeping (allows test injection)
type SleepFunc func(time.Duration)

// MonitorDoneCallback is called when the playback health monitor exits (for test synchronization)
type MonitorDoneCallback func()

// Manager handles music mode selection and playback coordination
type Manager struct {
	ctx          context.Context // Shutdown context for graceful cancellation
	haClient     ha.HAClient
	stateManager *state.Manager
	config       *MusicConfig
	logger       *zap.Logger
	readOnly     bool
	timeProvider plugin.TimeProvider
	timezone     *time.Location // Configured timezone for day-of-week checks
	sleepFunc    SleepFunc      // Injectable sleep function for testing

	// Playback state
	playlistNumbers    map[string]int // Tracks playlist rotation per music type
	currentlyPlaying   *CurrentlyPlayingMusic
	lastPlaybackTime   time.Time
	playbackInProgress bool
	mu                 sync.RWMutex // Protects playback state

	// Shadow state tracking
	shadowState *shadowstate.MusicShadowState
	shadowMu    sync.RWMutex // Protects shadow state

	// Subscriptions for cleanup
	subscriptions []state.Subscription

	// Available media_player entities from Home Assistant
	availableSpeakers   map[string]bool // entity_id -> exists
	availableSpeakersMu sync.RWMutex

	// Sync tracking for tests
	syncWg sync.WaitGroup // Tracks pending rotation syncs

	// Fade-in goroutine tracking for cancellation
	// Prevents concurrent fade-ins on the same speaker and false human-override detection
	fadeInContexts   map[string]context.CancelFunc // entity_id -> cancel func
	fadeInContextsMu sync.Mutex

	// Playback health monitoring for auto-pause detection
	playbackMonitorCancel context.CancelFunc
	playbackMonitorMu     sync.Mutex

	// Test hook for deterministic synchronization (called when monitor goroutine exits)
	monitorDoneCallback MonitorDoneCallback

	// Phase 2: Multi-zone support
	zoneManager *ZoneManager
}

// NewManager creates a new Music manager
// If timeProvider is nil, it defaults to plugin.RealTimeProvider
// If timezone is nil, it defaults to time.Local
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, config *MusicConfig, logger *zap.Logger, readOnly bool, timeProvider plugin.TimeProvider, timezone *time.Location) *Manager {
	if timeProvider == nil {
		timeProvider = plugin.RealTimeProvider{}
	}
	if timezone == nil {
		timezone = time.Local
	}
	return &Manager{
		ctx:                ctx,
		haClient:           haClient,
		stateManager:       stateManager,
		config:             config,
		logger:             logger.Named("music"),
		readOnly:           readOnly,
		timeProvider:       timeProvider,
		timezone:           timezone,
		sleepFunc:          time.Sleep,
		playlistNumbers:    make(map[string]int),
		shadowState:        shadowstate.NewMusicShadowState(),
		subscriptions:      make([]state.Subscription, 0),
		playbackInProgress: false,
		availableSpeakers:  make(map[string]bool),
		fadeInContexts:     make(map[string]context.CancelFunc),
	}
}

// SetSleepFunc allows overriding the sleep function for testing
func (m *Manager) SetSleepFunc(fn SleepFunc) {
	m.sleepFunc = fn
}

// SetMonitorDoneCallback sets a callback to be invoked when the playback monitor exits.
// This allows tests to wait deterministically for the monitor goroutine to complete.
func (m *Manager) SetMonitorDoneCallback(fn MonitorDoneCallback) {
	m.monitorDoneCallback = fn
}

// Start begins monitoring state changes and managing music playback
func (m *Manager) Start() error {
	m.logger.Info("Starting Music Manager")

	// Phase 2: Initialize zone manager
	m.zoneManager = NewZoneManager(m, m.config, m.logger)

	// Load playlist rotation state from Home Assistant (before any playback)
	m.loadPlaylistRotationFromHA()

	// Subscribe to dayPhase changes
	sub, err := m.stateManager.Subscribe("dayPhase", m.handleStateChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to dayPhase: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)

	// Subscribe to isAnyoneAsleep changes
	sub, err = m.stateManager.Subscribe("isAnyoneAsleep", m.handleStateChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isAnyoneAsleep: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)

	// Subscribe to isAnyoneHome changes
	sub, err = m.stateManager.Subscribe("isAnyoneHome", m.handleStateChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to isAnyoneHome: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)

	// Subscribe to musicPlaybackType changes to trigger actual playback
	sub, err = m.stateManager.Subscribe("musicPlaybackType", m.handleMusicPlaybackTypeChange)
	if err != nil {
		return fmt.Errorf("failed to subscribe to musicPlaybackType: %w", err)
	}
	m.subscriptions = append(m.subscriptions, sub)

	// Subscribe to all mute condition variables from participant configs
	muteConditionVars := m.collectMuteConditionVariables()
	for _, varName := range muteConditionVars {
		varNameCopy := varName // Capture loop variable
		sub, err = m.stateManager.Subscribe(varNameCopy, m.handleMuteConditionChange)
		if err != nil {
			// Log warning but don't fail - variable might not exist yet
			m.logger.Warn("Failed to subscribe to mute condition variable",
				zap.String("variable", varNameCopy),
				zap.Error(err))
			continue
		}
		m.subscriptions = append(m.subscriptions, sub)
		m.logger.Debug("Subscribed to mute condition variable",
			zap.String("variable", varNameCopy))
	}

	// Phase 2: Subscribe to zone trigger variables if explicit zones are defined
	// Use SubscribeWithContext to receive correlation IDs for cross-plugin event tracking
	if m.config.HasZones() {
		zoneTriggerVars := m.collectZoneTriggerVariables()
		for _, varName := range zoneTriggerVars {
			varNameCopy := varName
			sub, err = m.stateManager.SubscribeWithContext(varNameCopy, m.handleZoneTriggerChangeWithContext)
			if err != nil {
				m.logger.Warn("Failed to subscribe to zone trigger variable",
					zap.String("variable", varNameCopy),
					zap.Error(err))
				continue
			}
			m.subscriptions = append(m.subscriptions, sub)
			m.logger.Debug("Subscribed to zone trigger variable with context",
				zap.String("variable", varNameCopy))
		}
	}

	// Refresh available speakers from Home Assistant
	if err := m.refreshAvailableSpeakers(); err != nil {
		m.logger.Warn("Failed to refresh available speakers on startup", zap.Error(err))
	}

	// Validate configured speakers exist in Home Assistant
	m.validateConfiguredSpeakers()

	// Perform initial music mode selection
	m.selectAppropriateMusicMode()

	m.logger.Info("Music Manager started successfully")
	return nil
}

// Stop stops the Music Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Music Manager")

	// Unsubscribe from all subscriptions
	for _, sub := range m.subscriptions {
		sub.Unsubscribe()
	}
	m.subscriptions = nil

	m.logger.Info("Music Manager stopped")
}

// handleStateChange processes state changes that should trigger music mode re-evaluation
func (m *Manager) handleStateChange(key string, oldValue, newValue interface{}) {
	m.logger.Debug("State change detected",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Detect wake-up event: isAnyoneAsleep changed from true to false
	// This matches Node-RED behavior where msg.topic and msg.payload are checked:
	//   if (msg.topic == "isAnyoneAsleep" && msg.payload == false) { ... }
	isWakeUpEvent := false
	if key == "isAnyoneAsleep" {
		oldBool, oldOk := oldValue.(bool)
		newBool, newOk := newValue.(bool)
		if oldOk && newOk && oldBool && !newBool {
			isWakeUpEvent = true
			m.logger.Info("Wake-up event detected: isAnyoneAsleep changed from true to false")
		}
	}

	// Re-evaluate music mode with context
	m.selectAppropriateMusicModeWithContext(key, isWakeUpEvent)
}

// handleMusicPlaybackTypeChange is called when musicPlaybackType changes
// This triggers actual music playback orchestration
func (m *Manager) handleMusicPlaybackTypeChange(key string, oldValue, newValue interface{}) {
	m.logger.Info("Music playback type changed, initiating playback",
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	newType, ok := newValue.(string)
	if !ok {
		m.logger.Error("Invalid musicPlaybackType value type")
		return
	}

	// If empty string, stop playback (no rate limiting for stop operations)
	// IMPORTANT: This check must come BEFORE rate limiting to allow the clear-then-set
	// pattern used by sleep hygiene to force a music restart. If we rate-limited stop
	// operations, the subsequent set would be blocked.
	if newType == "" {
		m.logger.Info("Stopping music playback")
		m.stopPlayback()
		return
	}

	// Check rate limiting (max 1 playback per 10 seconds)
	// Only applies to starting playback, not stopping
	m.mu.Lock()
	timeSinceLastPlayback := m.timeProvider.Now().Sub(m.lastPlaybackTime)
	if timeSinceLastPlayback < 10*time.Second && !m.lastPlaybackTime.IsZero() {
		m.mu.Unlock()
		m.logger.Warn("Rate limiting: playback too soon after last playback",
			zap.Duration("time_since_last", timeSinceLastPlayback))
		return
	}
	m.lastPlaybackTime = m.timeProvider.Now()
	m.mu.Unlock()

	// Prevent re-activation of already playing music
	m.mu.RLock()
	if m.currentlyPlaying != nil && m.currentlyPlaying.Type == newType {
		m.mu.RUnlock()
		m.logger.Debug("Double activation of already-playing musicType, ignoring",
			zap.String("type", newType))
		return
	}
	m.mu.RUnlock()

	// Start playback orchestration with musicPlaybackType as trigger
	if err := m.orchestratePlayback(newType, "musicPlaybackType"); err != nil {
		m.logger.Error("Failed to orchestrate playback",
			zap.String("type", newType),
			zap.Error(err))
	}
}

// collectMuteConditionVariables collects all unique variables from participant mute and exclude conditions.
// These are variables like isNickOfficeOccupied that need subscriptions for dynamic speaker unmuting,
// and variables like isMasterAsleep that control zone exclusion (Phase 1).
func (m *Manager) collectMuteConditionVariables() []string {
	// Use a map to collect unique variables
	varMap := make(map[string]bool)

	// Standard variables that are already subscribed to via explicit handlers
	alreadySubscribed := map[string]bool{
		"dayPhase":          true,
		"isAnyoneAsleep":    true,
		"isAnyoneHome":      true,
		"musicPlaybackType": true,
	}

	for _, mode := range m.config.Music {
		for _, participant := range mode.Participants {
			// Collect leave_muted_if variables
			for _, condition := range participant.LeaveMutedIf {
				if condition.Variable != "" && !alreadySubscribed[condition.Variable] {
					varMap[condition.Variable] = true
				}
			}
			// Phase 1: Also collect exclude_if variables for zone exclusion
			for _, condition := range participant.ExcludeIf {
				if condition.Variable != "" && !alreadySubscribed[condition.Variable] {
					varMap[condition.Variable] = true
				}
			}
		}
	}

	// Convert map to slice
	result := make([]string, 0, len(varMap))
	for varName := range varMap {
		result = append(result, varName)
	}

	return result
}

// collectZoneTriggerVariables collects all unique variables from zone trigger conditions.
// These are variables like isAnyoneAsleep that control zone activation (Phase 2).
// Supports both legacy Triggers and new TriggerGroups.
func (m *Manager) collectZoneTriggerVariables() []string {
	varMap := make(map[string]bool)

	// Variables already subscribed to via explicit handlers
	alreadySubscribed := map[string]bool{
		"dayPhase":          true,
		"isAnyoneAsleep":    true,
		"isAnyoneHome":      true,
		"musicPlaybackType": true,
	}

	for _, zone := range m.config.Zones {
		// Legacy: zone.Triggers
		for _, trigger := range zone.Triggers {
			if trigger.Variable != "" && !alreadySubscribed[trigger.Variable] {
				varMap[trigger.Variable] = true
			}
		}

		// New: zone.TriggerGroups
		for _, group := range zone.TriggerGroups {
			for _, trigger := range group.Triggers {
				if trigger.Variable != "" && !alreadySubscribed[trigger.Variable] {
					varMap[trigger.Variable] = true
				}
			}
		}
	}

	result := make([]string, 0, len(varMap))
	for varName := range varMap {
		result = append(result, varName)
	}
	return result
}

// handleZoneTriggerChangeWithContext processes changes to zone trigger variables (Phase 2)
// with event correlation context for cross-plugin tracking.
// This re-evaluates which zones should be active.
func (m *Manager) handleZoneTriggerChangeWithContext(ctx *state.EventContext, key string, oldValue, newValue interface{}) {
	m.logger.Info("Zone trigger variable changed",
		zap.String("correlation_id", ctx.CorrelationID),
		zap.String("variable", key),
		zap.Any("old_value", oldValue),
		zap.Any("new_value", newValue))

	// Delegate to zone manager to resolve zones with context
	if m.zoneManager != nil {
		if err := m.zoneManager.ResolveZonesWithContext(ctx, "trigger:"+key); err != nil {
			m.logger.Error("Failed to resolve zones after trigger change",
				zap.String("correlation_id", ctx.CorrelationID),
				zap.String("variable", key),
				zap.Error(err))
		}
	}
}

// handleMuteConditionChange processes changes to variables used in speaker mute and exclude conditions.
// This re-evaluates speaker states during active playback.
// Phase 1: For exclude_if changes, logs that zone composition changed but doesn't migrate speakers (Phase 3).
// For leave_muted_if changes, dynamically mutes/unmutes speakers within the current zone.
func (m *Manager) handleMuteConditionChange(key string, oldValue, newValue interface{}) {
	m.logger.Debug("Mute/exclude condition variable changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Check if music is currently playing
	m.mu.RLock()
	currentlyPlaying := m.currentlyPlaying
	musicType := ""
	if currentlyPlaying != nil {
		musicType = currentlyPlaying.Type
	}
	m.mu.RUnlock()

	if currentlyPlaying == nil || musicType == "" {
		m.logger.Debug("No music currently playing, ignoring condition change",
			zap.String("key", key))
		return
	}

	// Phase 1: Check if this variable affects zone exclusion for any configured speaker
	// When an exclude_if variable changes, the zone composition may have changed
	// (speakers that were excluded may now be eligible, or vice versa)
	// Dynamic speaker migration is Phase 3 - for now, log and require manual reset
	mode, ok := m.config.Music[musicType]
	if ok {
		for _, participant := range mode.Participants {
			for _, condition := range participant.ExcludeIf {
				if condition.Variable == key {
					m.logger.Info("Zone exclusion condition changed - zone composition may have changed",
						zap.String("speaker", participant.PlayerName),
						zap.String("variable", key),
						zap.Any("new_value", newValue),
						zap.String("music_type", musicType),
						zap.String("note", "Dynamic speaker migration is Phase 3 - use /api/plugins/music/reset to re-orchestrate"))
				}
			}
		}
	}

	m.logger.Info("Re-evaluating speaker mute conditions during active playback",
		zap.String("key", key),
		zap.String("music_type", musicType))

	// Re-evaluate each participant's mute conditions (leave_muted_if only)
	// Speakers in currentlyPlaying.Participants already passed exclude_if at orchestration time
	for _, participant := range currentlyPlaying.Participants {
		// Check if this participant uses the changed variable in their mute conditions
		usesVariable := false
		for _, condition := range participant.LeaveMutedIf {
			if condition.Variable == key {
				usesVariable = true
				break
			}
		}

		if !usesVariable {
			continue
		}

		// Re-evaluate whether this speaker should be unmuted
		shouldUnmute := m.shouldUnmuteSpeaker(participant)

		m.logger.Info("Re-evaluated speaker mute condition",
			zap.String("speaker", participant.PlayerName),
			zap.String("changed_variable", key),
			zap.Bool("should_unmute", shouldUnmute))

		if shouldUnmute {
			// Unmute the speaker (volume was already set during initial playback)
			m.unmuteSpeaker(participant)
		} else {
			// Mute the speaker
			m.muteSpeaker(participant)
		}
	}
}

// unmuteSpeaker unmutes a Sonos speaker using the volume_mute service.
// This is used during active playback when a room becomes occupied.
// Volume was already set to the target during initial playback (even for muted speakers),
// so unmuting will immediately play at the correct volume level.
func (m *Manager) unmuteSpeaker(participant ParticipantWithVolume) {
	if m.readOnly {
		m.logger.Debug("Read-only mode: would unmute speaker",
			zap.String("speaker", participant.PlayerName))
		return
	}

	entityID := m.getSpeakerEntityID(participant.PlayerName)

	m.logger.Info("Unmuting speaker",
		zap.String("speaker", participant.PlayerName))

	if err := m.callServiceWithRetry("media_player", "volume_mute", map[string]interface{}{
		"entity_id":       entityID,
		"is_volume_muted": false,
	}); err != nil {
		m.logger.Error("Failed to unmute speaker",
			zap.String("speaker", participant.PlayerName),
			zap.Error(err))
	}
}

// muteSpeaker mutes a Sonos speaker using the volume_mute service.
// This is used during active playback when a room becomes unoccupied.
func (m *Manager) muteSpeaker(participant ParticipantWithVolume) {
	if m.readOnly {
		m.logger.Debug("Read-only mode: would mute speaker",
			zap.String("speaker", participant.PlayerName))
		return
	}

	entityID := m.getSpeakerEntityID(participant.PlayerName)

	m.logger.Info("Muting speaker",
		zap.String("speaker", participant.PlayerName))

	if err := m.callServiceWithRetry("media_player", "volume_mute", map[string]interface{}{
		"entity_id":       entityID,
		"is_volume_muted": true,
	}); err != nil {
		m.logger.Error("Failed to mute speaker",
			zap.String("speaker", participant.PlayerName),
			zap.Error(err))
	}
}

// stopPlayback stops all music playback
func (m *Manager) stopPlayback() {
	// Cancel any active playback health monitor
	m.cancelPlaybackMonitor()

	// Fade out before stopping to prevent jarring audio cutoff
	m.fadeOutSpeakers()

	m.mu.Lock()
	lastPlaying := m.currentlyPlaying // Save before clearing
	m.currentlyPlaying = nil
	m.mu.Unlock()

	// Clear the currently playing music URI in Home Assistant
	if err := m.stateManager.SetString("currentlyPlayingMusicUri", ""); err != nil {
		if !errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Error("Failed to clear currently playing music URI", zap.Error(err))
		}
	}

	if m.readOnly {
		m.logger.Debug("Skipping playback stop in read-only mode")
		return
	}

	// Only set volume to 0 for speakers that were actually playing
	if lastPlaying == nil {
		m.logger.Debug("No active playback to stop")
		return
	}

	for _, participant := range lastPlaying.Participants {
		entityID := m.getSpeakerEntityID(participant.PlayerName)
		if err := m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
			"entity_id":    entityID,
			"volume_level": 0,
		}); err != nil {
			m.logger.Error("Failed to set speaker volume to 0",
				zap.String("speaker", participant.PlayerName),
				zap.Error(err))
		}
	}

	m.logger.Info("Music playback stopped",
		zap.String("type", lastPlaying.Type),
		zap.Int("speaker_count", len(lastPlaying.Participants)))
}
