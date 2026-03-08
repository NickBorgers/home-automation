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

	// SoCo-CLI client for Tidal playback (nil if not configured)
	socoClient *SoCoClient

	// Phase 2: Multi-zone support
	zoneManager      *ZoneManager
	debounceDelay    time.Duration // debounce delay for zone trigger changes
	debounceDelaySet bool          // true if SetDebounceDelay was called explicitly
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

// validateTidalAvailability checks whether Tidal playlists are configured but
// SoCo-CLI is not available, and logs warnings about affected music modes.
// Tidal playlists will be skipped at playback time; modes with only Tidal
// playlists will have no playable options.
func (m *Manager) validateTidalAvailability() {
	if m.socoClient != nil {
		return // SoCo-CLI configured, all playlists available
	}

	var degradedModes []string
	var brokenModes []string
	totalTidal := 0

	for modeName, mode := range m.config.Music {
		tidalCount := 0
		for _, opt := range mode.PlaybackOptions {
			if opt.MediaType == "tidal" {
				tidalCount++
			}
		}
		if tidalCount == 0 {
			continue
		}
		totalTidal += tidalCount

		nonTidalCount := len(mode.PlaybackOptions) - tidalCount
		if nonTidalCount == 0 {
			brokenModes = append(brokenModes, modeName)
		} else {
			degradedModes = append(degradedModes, modeName)
		}
	}

	if totalTidal == 0 {
		return
	}

	m.logger.Warn("SoCo-CLI not configured: Tidal playlists will be skipped (set SOCO_CLI_URL to enable)",
		zap.Int("tidal_playlists_skipped", totalTidal))

	if len(degradedModes) > 0 {
		m.logger.Warn("Music modes with reduced playlist options (some Tidal playlists skipped)",
			zap.Strings("modes", degradedModes))
	}
	if len(brokenModes) > 0 {
		m.logger.Error("Music modes with NO playable options (all playlists are Tidal)",
			zap.Strings("modes", brokenModes))
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

// SetSoCoClient sets the SoCo-CLI client for Tidal playback.
func (m *Manager) SetSoCoClient(client *SoCoClient) {
	m.socoClient = client
}

// SetDebounceDelay overrides the zone trigger debounce delay.
// Can be called before or after Start(). Use 0 for immediate (synchronous)
// resolution in tests. In production, Start() sets the default of 500ms.
func (m *Manager) SetDebounceDelay(d time.Duration) {
	m.debounceDelay = d
	m.debounceDelaySet = true
	if m.zoneManager != nil {
		m.zoneManager.SetDebounceDelay(d)
	}
}

// GetActiveZones returns a copy of all currently active music zones.
// Returns nil if the zone manager hasn't been initialized yet.
// This is primarily used for testing and debugging.
func (m *Manager) GetActiveZones() []*Zone {
	if m.zoneManager == nil {
		return nil
	}
	return m.zoneManager.GetActiveZones()
}

// Start begins monitoring state changes and managing music playback
func (m *Manager) Start() error {
	m.logger.Info("Starting Music Manager")

	// Warn about unavailable Tidal playlists when SoCo-CLI is not configured
	m.validateTidalAvailability()

	// Ensure zones are always populated. LoadConfig calls ensureZones at load time,
	// but programmatically-constructed configs (e.g., in tests) may not have zones.
	// This guarantees the zone-based code path is always used (#639).
	m.config.ensureZones()

	// Initialize zone manager
	m.zoneManager = NewZoneManager(m, m.config, m.logger)
	// Apply debounce delay if explicitly set via SetDebounceDelay().
	// If not set, the zone manager uses its default (0 = immediate resolution).
	// Production debouncing is configured by the plugin adapter after Start().
	if m.debounceDelaySet {
		m.zoneManager.SetDebounceDelay(m.debounceDelay)
	}

	// Load playlist rotation state from Home Assistant (before any playback)
	m.loadPlaylistRotationFromHA()

	// Subscribe to musicPlaybackType changes (for stop handling and manual zone triggers).
	// dayPhase, isAnyoneAsleep, isAnyoneHome are handled by zone trigger subscriptions below.
	sub, err := m.stateManager.Subscribe("musicPlaybackType", m.handleMusicPlaybackTypeChange)
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

	// Subscribe to zone trigger variables for zone resolution.
	// Uses SubscribeWithContext to receive correlation IDs for cross-plugin event tracking.
	// Zones are always present (ensureZones populates them at config load time).
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

	// Refresh available speakers from Home Assistant
	if err := m.refreshAvailableSpeakers(); err != nil {
		m.logger.Warn("Failed to refresh available speakers on startup", zap.Error(err))
	}

	// Validate configured speakers exist in Home Assistant
	m.validateConfiguredSpeakers()

	// Perform initial zone resolution to start appropriate zones
	if m.zoneManager != nil {
		if err := m.zoneManager.ResolveZones("startup"); err != nil {
			m.logger.Error("Failed initial zone resolution", zap.Error(err))
		}
	}

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

// handleMusicPlaybackTypeChange is called when musicPlaybackType changes.
// The zone manager orchestrates playback directly via orchestrateZonePlayback
// when zones start. musicPlaybackType is set by startZone for fade-in safety
// check consistency.
//
// When musicPlaybackType is cleared to "", this triggers zone resolution to
// re-evaluate which zones should be active. Auto-triggered zones (morning,
// evening) continue playing; manually-triggered zones (wakeup, sex) stop
// because their musicPlaybackType fallback no longer matches.
//
// For manually-triggered zones (sex, wakeup) that are activated by setting
// musicPlaybackType directly, this handler triggers zone resolution so the
// zone manager can evaluate and start the appropriate zone.
func (m *Manager) handleMusicPlaybackTypeChange(key string, oldValue, newValue interface{}) {
	m.logger.Info("Music playback type changed",
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	newType, ok := newValue.(string)
	if !ok {
		m.logger.Error("Invalid musicPlaybackType value type")
		return
	}

	// If empty string, re-evaluate zones rather than stopping everything.
	// This ensures auto-triggered zones (morning, evening, etc.) continue playing
	// while manually-triggered zones (wakeup, sex) stop because their
	// musicPlaybackType fallback no longer matches (zone_manager.go getActiveZoneConfigs).
	//
	// Fix for issue #755: Previously this called StopAllZones which killed all zones
	// including the morning zone that was already playing. The morning zone then had
	// to restart from scratch (regroup speakers, fade in over ~6 minutes).
	if newType == "" {
		m.logger.Info("musicPlaybackType cleared, resolving zones to stop manually-triggered zones")
		if m.zoneManager != nil && !m.zoneManager.IsResolving() {
			if err := m.zoneManager.ResolveZones("musicPlaybackType cleared"); err != nil {
				m.logger.Error("Failed to resolve zones after musicPlaybackType cleared",
					zap.Error(err))
			}
		} else if m.zoneManager == nil {
			// Fallback when zone manager is not initialized (startup, tests)
			m.stopPlayback()
		}
		return
	}

	// Trigger zone resolution so the zone manager can evaluate which zones
	// should be active. This handles both manually-triggered zones (sex, wakeup)
	// and explicit musicPlaybackType changes from external sources (HA, API).
	//
	// Skip resolution if it would be re-entrant: startZone calls setMusicPlaybackType
	// which triggers this handler again. The zone is already being started, so
	// re-resolving would be redundant. We detect this via the resolvingZones flag.
	if m.zoneManager != nil && !m.zoneManager.IsResolving() {
		if err := m.zoneManager.ResolveZones("musicPlaybackType:" + newType); err != nil {
			m.logger.Error("Failed to resolve zones after musicPlaybackType change",
				zap.String("type", newType),
				zap.Error(err))
		}
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
//
// Note: dayPhase, isAnyoneAsleep, and isAnyoneHome are NOT filtered out here even though
// they also have subscriptions via handleStateChange. When zones are configured,
// handleStateChange returns early (skipping legacy selectAppropriateMusicMode), so these
// variables MUST be subscribed to handleZoneTriggerChangeWithContext to trigger zone
// resolution. musicPlaybackType is excluded because it has its own dedicated handler
// (handleMusicPlaybackTypeChange) that should not trigger zone resolution.
func (m *Manager) collectZoneTriggerVariables() []string {
	varMap := make(map[string]bool)

	// Only musicPlaybackType is excluded — it has a dedicated handler
	// (handleMusicPlaybackTypeChange) and should not trigger zone resolution.
	// dayPhase, isAnyoneAsleep, and isAnyoneHome are intentionally NOT filtered
	// because when zones are configured, handleStateChange returns early, and
	// these variables must reach handleZoneTriggerChangeWithContext to trigger
	// zone resolution for routine daily transitions.
	alreadySubscribed := map[string]bool{
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
// This schedules a debounced zone resolution to coalesce rapid state changes from a
// single logical event (e.g., wake-up sets isAnyoneAsleep, isMasterAsleep, isWakeSequenceActive
// within ~250ms) into one resolution, avoiding duplicate Sonos commands.
func (m *Manager) handleZoneTriggerChangeWithContext(ctx *state.EventContext, key string, oldValue, newValue interface{}) {
	m.logger.Info("Zone trigger variable changed",
		zap.String("correlation_id", ctx.CorrelationID),
		zap.String("variable", key),
		zap.Any("old_value", oldValue),
		zap.Any("new_value", newValue))

	// Schedule debounced zone resolution instead of resolving immediately.
	// This coalesces rapid triggers from a single logical event into one resolution.
	if m.zoneManager != nil {
		m.zoneManager.ScheduleResolve(ctx, "trigger:"+key)
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
