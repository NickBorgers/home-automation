package music

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
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

// Manager handles music mode selection and playback coordination
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	config       *MusicConfig
	logger       *zap.Logger
	readOnly     bool
	timeProvider plugin.TimeProvider
	sleepFunc    SleepFunc // Injectable sleep function for testing

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
}

// NewManager creates a new Music manager
// If timeProvider is nil, it defaults to plugin.RealTimeProvider
func NewManager(haClient ha.HAClient, stateManager *state.Manager, config *MusicConfig, logger *zap.Logger, readOnly bool, timeProvider plugin.TimeProvider) *Manager {
	if timeProvider == nil {
		timeProvider = plugin.RealTimeProvider{}
	}
	return &Manager{
		haClient:           haClient,
		stateManager:       stateManager,
		config:             config,
		logger:             logger.Named("music"),
		readOnly:           readOnly,
		timeProvider:       timeProvider,
		sleepFunc:          time.Sleep,
		playlistNumbers:    make(map[string]int),
		shadowState:        shadowstate.NewMusicShadowState(),
		subscriptions:      make([]state.Subscription, 0),
		playbackInProgress: false,
		availableSpeakers:  make(map[string]bool),
	}
}

// SetSleepFunc allows overriding the sleep function for testing
func (m *Manager) SetSleepFunc(fn SleepFunc) {
	m.sleepFunc = fn
}

// Start begins monitoring state changes and managing music playback
func (m *Manager) Start() error {
	m.logger.Info("Starting Music Manager")

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

// selectAppropriateMusicMode determines which music mode should be active (without trigger context)
func (m *Manager) selectAppropriateMusicMode() {
	m.selectAppropriateMusicModeWithContext("", false)
}

// selectAppropriateMusicModeWithContext determines which music mode should be active with trigger context
func (m *Manager) selectAppropriateMusicModeWithContext(triggerKey string, isWakeUpEvent bool) {
	m.logger.Debug("Selecting appropriate music mode",
		zap.String("trigger_key", triggerKey),
		zap.Bool("is_wake_up_event", isWakeUpEvent))

	// Get current state
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneHome", zap.Error(err))
		return
	}

	// If no one is home, stop music
	if !isAnyoneHome {
		m.logger.Info("No one is home, stopping music")
		if err := m.setMusicPlaybackType(""); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping music playback type update in read-only mode",
					zap.String("music_type", ""))
			} else {
				m.logger.Error("Failed to set empty music playback type", zap.Error(err))
			}
		}
		return
	}

	// Check if anyone is asleep - sleep mode has highest priority
	isAnyoneAsleep, err := m.stateManager.GetBool("isAnyoneAsleep")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneAsleep", zap.Error(err))
		return
	}

	if isAnyoneAsleep {
		m.logger.Info("Someone is asleep, selecting sleep mode")
		if err := m.setMusicPlaybackType("sleep"); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping music playback type update in read-only mode",
					zap.String("music_type", "sleep"))
			} else {
				m.logger.Error("Failed to set sleep music playback type", zap.Error(err))
			}
		}
		return
	}

	// Get current day phase
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return
	}

	// Get current music playback type to check for persistence
	currentMusicType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil {
		m.logger.Error("Failed to get musicPlaybackType", zap.Error(err))
		return
	}

	// Determine music mode based on day phase and trigger context
	musicMode := m.determineMusicModeFromDayPhase(dayPhase, currentMusicType, triggerKey, isWakeUpEvent)

	m.logger.Info("Selected music mode",
		zap.String("day_phase", dayPhase),
		zap.String("current_music_type", currentMusicType),
		zap.String("trigger_key", triggerKey),
		zap.Bool("is_wake_up_event", isWakeUpEvent),
		zap.String("new_music_mode", musicMode))

	// Set the music playback type
	if err := m.setMusicPlaybackType(musicMode); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping music playback type update in read-only mode",
				zap.String("music_type", musicMode))
		} else {
			m.logger.Error("Failed to set music playback type", zap.Error(err))
		}
	}
}

// determineMusicModeFromDayPhase determines the music mode based on the current day phase
// Matches Node-RED behavior: morning music only plays on wake-up events
func (m *Manager) determineMusicModeFromDayPhase(dayPhase string, currentMusicType string, triggerKey string, isWakeUpEvent bool) string {
	switch dayPhase {
	case "morning":
		// Morning music ONLY plays when someone wakes up (matches Node-RED)
		// Otherwise, fall back to day music during morning phase
		if isWakeUpEvent {
			// Check if it's Sunday (no morning music on Sundays)
			if m.timeProvider.Now().Weekday() == time.Sunday {
				m.logger.Debug("Sunday detected, using day mode instead of morning")
				return "day"
			}
			m.logger.Info("Wake-up event during morning phase, playing morning music")
			return "morning"
		}
		// During morning phase but not a wake-up event - use day music
		m.logger.Debug("Morning phase but not a wake-up event, using day music")
		return "day"

	case "day":
		return "day"

	case "sunset", "dusk":
		return "evening"

	case "winddown", "night":
		// Don't override sleep music with winddown
		if currentMusicType == "sleep" {
			m.logger.Debug("Sleep music already playing, not changing to winddown")
			return "sleep"
		}
		return "winddown"

	default:
		m.logger.Warn("Unknown day phase, defaulting to day mode",
			zap.String("day_phase", dayPhase))
		return "day"
	}
}

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

	// Check rate limiting (max 1 playback per 10 seconds)
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
	if m.currentlyPlaying != nil && m.currentlyPlaying.Type == newType && newType != "" {
		m.mu.RUnlock()
		m.logger.Debug("Double activation of already-playing musicType, ignoring",
			zap.String("type", newType))
		return
	}
	m.mu.RUnlock()

	// If empty string, stop playback
	if newType == "" {
		m.logger.Info("Stopping music playback")
		m.stopPlayback()
		return
	}

	// Start playback orchestration with musicPlaybackType as trigger
	if err := m.orchestratePlayback(newType, "musicPlaybackType"); err != nil {
		m.logger.Error("Failed to orchestrate playback",
			zap.String("type", newType),
			zap.Error(err))
	}
}

// collectMuteConditionVariables collects all unique variables from participant mute conditions
// These are variables like isNickOfficeOccupied that need subscriptions for dynamic speaker unmuting
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
			for _, condition := range participant.LeaveMutedIf {
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

// handleMuteConditionChange processes changes to variables used in speaker mute conditions
// This re-evaluates speaker states during active playback
func (m *Manager) handleMuteConditionChange(key string, oldValue, newValue interface{}) {
	m.logger.Debug("Mute condition variable changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Check if music is currently playing
	m.mu.RLock()
	currentlyPlaying := m.currentlyPlaying
	m.mu.RUnlock()

	if currentlyPlaying == nil || currentlyPlaying.Type == "" {
		m.logger.Debug("No music currently playing, ignoring mute condition change",
			zap.String("key", key))
		return
	}

	m.logger.Info("Re-evaluating speaker mute conditions during active playback",
		zap.String("key", key),
		zap.String("music_type", currentlyPlaying.Type))

	// Re-evaluate each participant's mute conditions
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
// Volume was already set during initial playback, so we just need to unmute.
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
	m.mu.Lock()
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

	// Set all speakers to volume 0
	for _, mode := range m.config.Music {
		for _, participant := range mode.Participants {
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
	}

	m.logger.Info("Music playback stopped")
}

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

	// Build participants with calculated volumes
	participants := make([]ParticipantWithVolume, 0, len(mode.Participants))
	for _, p := range mode.Participants {
		volume := m.calculateVolume(p.BaseVolume, playbackOption.VolumeMultiplier)
		participants = append(participants, ParticipantWithVolume{
			PlayerName:    p.PlayerName,
			BaseVolume:    p.BaseVolume,
			Volume:        volume,
			DefaultVolume: volume,
			LeaveMutedIf:  p.LeaveMutedIf,
		})
	}

	// Get lead player (first participant)
	if len(participants) == 0 {
		return fmt.Errorf("no participants for music type: %s", musicType)
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

// getNextPlaylistIndex returns the next playlist index with rotation
func (m *Manager) getNextPlaylistIndex(musicType string, optionsCount int) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get current index or initialize to 0
	currentIndex, exists := m.playlistNumbers[musicType]
	if !exists {
		currentIndex = 0
	}

	// Save the index to use
	indexToUse := currentIndex

	// Increment for next time (with wraparound)
	nextIndex := currentIndex + 1
	if nextIndex >= optionsCount {
		nextIndex = 0
	}
	m.playlistNumbers[musicType] = nextIndex

	// Sync to Home Assistant (fire and forget, don't block on errors)
	m.syncWg.Add(1)
	go m.syncPlaylistRotationToHA()

	return indexToUse
}

// loadPlaylistRotationFromHA loads playlist rotation state from Home Assistant on startup.
// It validates that stored indices are within bounds for each music type's playlist count.
func (m *Manager) loadPlaylistRotationFromHA() {
	rotationJSON, err := m.stateManager.GetString("musicPlaylistRotation")
	if err != nil {
		m.logger.Warn("Failed to get playlist rotation from HA", zap.Error(err))
		return
	}

	if rotationJSON == "" || rotationJSON == "{}" {
		m.logger.Debug("No playlist rotation state in HA, starting fresh")
		return
	}

	var loadedRotation map[string]int
	if err := json.Unmarshal([]byte(rotationJSON), &loadedRotation); err != nil {
		m.logger.Warn("Failed to parse playlist rotation JSON from HA, starting fresh",
			zap.String("json", rotationJSON),
			zap.Error(err))
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate and apply each loaded index
	for musicType, index := range loadedRotation {
		// Check if this music type exists in config
		mode, exists := m.config.Music[musicType]
		if !exists {
			// Keep the value anyway - music type might be added back later
			m.playlistNumbers[musicType] = index
			m.logger.Debug("Loaded rotation for unconfigured music type",
				zap.String("musicType", musicType),
				zap.Int("index", index))
			continue
		}

		optionsCount := len(mode.PlaybackOptions)
		if optionsCount == 0 {
			m.logger.Warn("Music type has no playback options, skipping",
				zap.String("musicType", musicType))
			continue
		}

		// Wrap index if it exceeds available playlists (e.g., playlist was removed)
		validIndex := index
		if index >= optionsCount {
			validIndex = index % optionsCount
			m.logger.Info("Playlist rotation index exceeded options count, wrapping",
				zap.String("musicType", musicType),
				zap.Int("storedIndex", index),
				zap.Int("optionsCount", optionsCount),
				zap.Int("wrappedIndex", validIndex))
		}

		m.playlistNumbers[musicType] = validIndex
	}

	m.logger.Info("Loaded playlist rotation from HA", zap.Any("rotation", m.playlistNumbers))
}

// syncPlaylistRotationToHA persists playlist rotation state to Home Assistant.
// This should be called after updating playlistNumbers.
func (m *Manager) syncPlaylistRotationToHA() {
	defer m.syncWg.Done()

	m.mu.RLock()
	rotationCopy := make(map[string]int, len(m.playlistNumbers))
	for k, v := range m.playlistNumbers {
		rotationCopy[k] = v
	}
	m.mu.RUnlock()

	rotationJSON, err := json.Marshal(rotationCopy)
	if err != nil {
		m.logger.Error("Failed to marshal playlist rotation to JSON", zap.Error(err))
		return
	}

	if err := m.stateManager.SetString("musicPlaylistRotation", string(rotationJSON)); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping playlist rotation sync in read-only mode")
		} else {
			m.logger.Error("Failed to sync playlist rotation to HA", zap.Error(err))
		}
	}
}

// WaitForSync waits for any pending playlist rotation syncs to complete.
// This is primarily used for testing to avoid sleep-based synchronization.
func (m *Manager) WaitForSync() {
	m.syncWg.Wait()
}

// calculateVolume calculates final volume from base and multiplier
func (m *Manager) calculateVolume(baseVolume int, multiplier float64) int {
	volume := math.Round(float64(baseVolume) * multiplier)
	// Cap at 15 (Sonos max for Spotify playback scale)
	if volume > 15 {
		volume = 15
	}
	if volume < 0 {
		volume = 0
	}
	return int(volume)
}

// executePlayback executes the actual playback sequence.
// Returns SpeakerGroupResult indicating which speakers are active, and the number of
// verification attempts needed (1 = first try succeeded).
// Sequence matches Node-RED: break existing groups → build new group → mute → play → fade in
func (m *Manager) executePlayback(musicType string, option PlaybackOption, participants []ParticipantWithVolume, leadPlayer string) (*SpeakerGroupResult, int, error) {
	m.logger.Info("Executing playback sequence",
		zap.String("type", musicType),
		zap.String("lead_player", leadPlayer),
		zap.Int("participant_count", len(participants)))

	leadEntityID := m.getSpeakerEntityID(leadPlayer)

	// Step 1: Break speakers from existing groups before building new group
	// This matches Node-RED behavior where stopMsg routes through "Break group for player"
	m.breakSpeakerGroups(participants)

	// Step 2: Build speaker group if multiple participants
	var groupResult *SpeakerGroupResult
	if len(participants) > 1 {
		var err error
		groupResult, err = m.buildSpeakerGroup(participants, leadEntityID)
		if err != nil {
			return groupResult, 0, fmt.Errorf("failed to build speaker group: %w", err)
		}
	} else {
		// Single speaker - create result with just the lead
		groupResult = &SpeakerGroupResult{
			Results: []SpeakerResult{{
				Participant: participants[0],
				Active:      true,
			}},
			ActiveCount: 1,
			FailedCount: 0,
			LeadActive:  true,
		}
	}

	// Step 3: Mute all ACTIVE speakers initially
	for _, sr := range groupResult.Results {
		if !sr.Active {
			continue // Skip failed speakers
		}
		entityID := m.getSpeakerEntityID(sr.Participant.PlayerName)
		if err := m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
			"entity_id":    entityID,
			"volume_level": 0,
		}); err != nil {
			m.logger.Error("Failed to mute speaker",
				zap.String("speaker", sr.Participant.PlayerName),
				zap.Error(err))
		}
	}

	// Step 4: Start playback on lead player with verification
	// This verifies playback actually starts, not just that the command was accepted
	attempts, err := m.startPlaybackWithVerification(leadEntityID, option)
	if err != nil {
		return groupResult, attempts, fmt.Errorf("failed to start playback: %w", err)
	}
	if attempts > 1 {
		m.logger.Info("Playback required multiple attempts",
			zap.Int("attempts", attempts),
			zap.String("speaker", leadPlayer))
	}

	// Step 5: Enable shuffle for Spotify playlists
	if option.MediaType == "playlist" {
		if err := m.callServiceWithRetry("media_player", "shuffle_set", map[string]interface{}{
			"entity_id": leadEntityID,
			"shuffle":   true,
		}); err != nil {
			m.logger.Warn("Failed to enable shuffle",
				zap.String("speaker", leadPlayer),
				zap.Error(err))
		}
	}

	// Step 6: Evaluate mute conditions and unmute eligible ACTIVE speakers
	for _, sr := range groupResult.Results {
		if !sr.Active {
			continue // Skip failed speakers
		}
		if m.shouldUnmuteSpeaker(sr.Participant) {
			m.logger.Info("Unmuting speaker",
				zap.String("speaker", sr.Participant.PlayerName),
				zap.Int("target_volume", sr.Participant.Volume))

			// Start fade-in in goroutine
			go m.fadeInSpeaker(sr.Participant.PlayerName, sr.Participant.Volume, musicType)
		} else {
			m.logger.Info("Keeping speaker muted due to conditions",
				zap.String("speaker", sr.Participant.PlayerName))
		}
	}

	m.logger.Info("Playback sequence completed successfully",
		zap.String("type", musicType),
		zap.Int("active_speakers", groupResult.ActiveCount),
		zap.Int("failed_speakers", groupResult.FailedCount),
		zap.Int("verification_attempts", attempts))

	return groupResult, attempts, nil
}

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
)

// startPlaybackWithVerification sends the play_media command and verifies playback actually starts.
// It returns the number of attempts needed (1 = first try succeeded) and any error.
// This handles the failure mode where HA accepts play_media but the speaker doesn't actually play.
func (m *Manager) startPlaybackWithVerification(leadEntityID string, option PlaybackOption) (attempts int, err error) {
	for attempt := 1; attempt <= playbackVerificationRetries; attempt++ {
		// Send play_media command
		if err := m.callServiceWithRetry("media_player", "play_media", map[string]interface{}{
			"entity_id":          leadEntityID,
			"media_content_id":   option.URI,
			"media_content_type": option.MediaType,
		}); err != nil {
			return attempt, fmt.Errorf("failed to send play_media: %w", err)
		}

		// Wait for speaker to start playing
		m.sleepFunc(playbackVerificationDelay)

		// Check if playback actually started
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

		// Try media_player.play as a nudge in case the speaker is paused
		if nudgeErr := m.callServiceWithRetry("media_player", "media_play", map[string]interface{}{
			"entity_id": leadEntityID,
		}); nudgeErr != nil {
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

// isPlaybackActive checks if the speaker is currently in a playing state.
// Returns true if state is "playing", false for "paused", "idle", "off", etc.
func (m *Manager) isPlaybackActive(entityID string) (bool, error) {
	state, err := m.haClient.GetState(entityID)
	if err != nil {
		return false, fmt.Errorf("failed to get speaker state: %w", err)
	}
	if state == nil {
		return false, fmt.Errorf("speaker state is nil")
	}

	// Sonos media_player states: playing, paused, idle, off, unavailable
	isPlaying := state.State == "playing"

	m.logger.Debug("Checked playback state",
		zap.String("entity_id", entityID),
		zap.String("state", state.State),
		zap.Bool("is_playing", isPlaying))

	return isPlaying, nil
}

// breakSpeakerGroups unjoins all participants from their existing groups.
// This must be called before building a new speaker group to ensure speakers
// aren't already grouped together in unpredictable ways.
// Matches Node-RED behavior: "Break group for player" -> player.become.standalone
func (m *Manager) breakSpeakerGroups(participants []ParticipantWithVolume) {
	m.logger.Info("Breaking existing speaker groups before building new group",
		zap.Int("participant_count", len(participants)))

	// Unjoin each speaker from any existing group
	for _, p := range participants {
		entityID := m.getSpeakerEntityID(p.PlayerName)

		m.logger.Debug("Unjoining speaker from existing group",
			zap.String("speaker", p.PlayerName),
			zap.String("entity_id", entityID))

		// Use media_player.unjoin to break the speaker out of any existing group
		// This is equivalent to Sonos "player.become.standalone"
		if err := m.callServiceWithRetry("media_player", "unjoin", map[string]interface{}{
			"entity_id": entityID,
		}); err != nil {
			// Log warning but continue - speaker might not be in a group
			m.logger.Warn("Failed to unjoin speaker (may not be in a group)",
				zap.String("speaker", p.PlayerName),
				zap.Error(err))
		}
	}

	// Allow time for Sonos to process the unjoin commands before building new group
	m.sleepFunc(speakerUnjoinSettleDelay)

	m.logger.Info("Finished breaking existing speaker groups")
}

// buildSpeakerGroup creates a Sonos speaker group with retry logic.
// Returns a SpeakerGroupResult indicating which speakers successfully joined.
// Continues with partial group if some speakers are unavailable.
// Only fails entirely if lead speaker is unavailable or all speakers fail.
func (m *Manager) buildSpeakerGroup(participants []ParticipantWithVolume, leadEntityID string) (*SpeakerGroupResult, error) {
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

	// Build list of follower entity IDs
	var groupMembers []string
	for i, p := range participants {
		if i == 0 {
			// Skip lead player
			continue
		}
		groupMembers = append(groupMembers, m.getSpeakerEntityID(p.PlayerName))
	}

	// First attempt: try to add all speakers at once (most efficient)
	allSucceeded := false
	var lastErr error

	for attempt := 1; attempt <= maxSpeakerGroupRetries; attempt++ {
		err := m.callServiceWithRetry("media_player", "join", map[string]interface{}{
			"entity_id":     leadEntityID,
			"group_members": groupMembers,
		})

		if err == nil {
			allSucceeded = true
			if attempt > 1 {
				m.logger.Info("Speaker group created after retry",
					zap.String("lead", leadEntityID),
					zap.Strings("members", groupMembers),
					zap.Int("attempt", attempt))
			} else {
				m.logger.Info("Speaker group created",
					zap.String("lead", leadEntityID),
					zap.Strings("members", groupMembers))
			}
			break
		}

		lastErr = err
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
		entityID := m.getSpeakerEntityID(p.PlayerName)
		speakerJoined := false

		for attempt := 1; attempt <= maxSpeakerGroupRetries; attempt++ {
			err := m.callServiceWithRetry("media_player", "join", map[string]interface{}{
				"entity_id":     leadEntityID,
				"group_members": []string{entityID},
			})

			if err == nil {
				speakerJoined = true
				m.logger.Info("Speaker joined group individually",
					zap.String("speaker", p.PlayerName),
					zap.Int("attempt", attempt))
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

// fadeInSpeaker gradually increases speaker volume
func (m *Manager) fadeInSpeaker(speakerName string, targetVolume int, startingMusicType string) {
	m.logger.Debug("Starting fade-in",
		zap.String("speaker", speakerName),
		zap.Int("target_volume", targetVolume))

	entityID := m.getSpeakerEntityID(speakerName)

	// SAFETY: Set volume to 0 BEFORE unmuting to prevent sudden loud noise.
	// If the speaker was previously at high volume and muted, unmuting without
	// lowering volume first would cause an immediate loud playback.
	if err := m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
		"entity_id":    entityID,
		"volume_level": 0.0,
	}); err != nil {
		m.logger.Error("Failed to set initial volume before unmute",
			zap.String("speaker", speakerName),
			zap.Error(err))
		return
	}

	// Now safe to unmute - Sonos maintains mute state independently of volume
	if err := m.callServiceWithRetry("media_player", "volume_mute", map[string]interface{}{
		"entity_id":       entityID,
		"is_volume_muted": false,
	}); err != nil {
		m.logger.Error("Failed to unmute speaker before fade-in",
			zap.String("speaker", speakerName),
			zap.Error(err))
		return
	}

	// Track failures for better error reporting
	var consecutiveFailures int
	var totalFailures int
	var lastSuccessfulVolume int = -1
	const maxConsecutiveFailures = 3

	// Gradual fade-in: 0 → targetVolume
	for currentVolume := 0; currentVolume <= targetVolume; currentVolume++ {
		// Check if music type changed (stop fade if switched)
		musicType, err := m.stateManager.GetString("musicPlaybackType")
		if err == nil && musicType != startingMusicType {
			m.logger.Info("Music type changed during fade-in, stopping",
				zap.String("speaker", speakerName),
				zap.String("starting_type", startingMusicType),
				zap.String("current_type", musicType))
			return
		}

		// Set volume
		if err := m.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
			"entity_id":    entityID,
			"volume_level": float64(currentVolume) / 100.0, // Normalize percentage (0-100) to 0.0-1.0
		}); err != nil {
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
				return
			}
		} else {
			consecutiveFailures = 0
			lastSuccessfulVolume = currentVolume
		}

		// Adaptive delay: slower at start, faster as volume increases
		// Matches Node-RED behavior: msg.delay = (100 - current_volume) * 250
		// At volume 0%: 25s delay, at 50%: 12.5s, at 90%: 2.5s, at 99%: 250ms
		delayMs := (100 - currentVolume) * 250 // 250ms per percentage point remaining
		if delayMs < 250 {
			delayMs = 250 // Minimum 250ms between steps
		}
		m.sleepFunc(time.Duration(delayMs) * time.Millisecond)
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

// getStateValue gets a state variable value by key
func (m *Manager) getStateValue(key string) (interface{}, error) {
	// Try as boolean first
	if val, err := m.stateManager.GetBool(key); err == nil {
		return val, nil
	}

	// Try as string
	if val, err := m.stateManager.GetString(key); err == nil {
		return val, nil
	}

	// Try as number
	if val, err := m.stateManager.GetNumber(key); err == nil {
		return val, nil
	}

	return nil, fmt.Errorf("failed to get state variable: %s", key)
}

// valuesMatch checks if two values match
func (m *Manager) valuesMatch(a, b interface{}) bool {
	// Simple equality check
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// callService calls a Home Assistant service
func (m *Manager) callService(domain, service string, serviceData map[string]interface{}) error {
	if m.readOnly {
		m.logger.Debug("Read-only mode: would call service",
			zap.String("domain", domain),
			zap.String("service", service),
			zap.Any("service_data", serviceData))
		return nil
	}

	m.logger.Debug("Calling HA service",
		zap.String("domain", domain),
		zap.String("service", service),
		zap.Any("service_data", serviceData))

	// Call the service via HA client
	if err := m.haClient.CallService(domain, service, serviceData); err != nil {
		return fmt.Errorf("service call failed: %w", err)
	}

	return nil
}

// refreshAvailableSpeakers queries Home Assistant for all media_player entities
// and caches which ones are available for use
func (m *Manager) refreshAvailableSpeakers() error {
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get states from Home Assistant: %w", err)
	}

	m.availableSpeakersMu.Lock()
	defer m.availableSpeakersMu.Unlock()

	m.availableSpeakers = make(map[string]bool)
	for _, state := range states {
		if strings.HasPrefix(state.EntityID, "media_player.") {
			m.availableSpeakers[state.EntityID] = true
		}
	}

	m.logger.Info("Refreshed available speakers",
		zap.Int("count", len(m.availableSpeakers)))
	return nil
}

// isSpeakerAvailable checks if a speaker entity exists in Home Assistant
func (m *Manager) isSpeakerAvailable(entityID string) bool {
	m.availableSpeakersMu.RLock()
	defer m.availableSpeakersMu.RUnlock()
	return m.availableSpeakers[entityID]
}

// validateConfiguredSpeakers logs warnings for any configured speakers not found in Home Assistant
func (m *Manager) validateConfiguredSpeakers() {
	for modeName, mode := range m.config.Music {
		for _, participant := range mode.Participants {
			entityID := m.getSpeakerEntityID(participant.PlayerName)
			if !m.isSpeakerAvailable(entityID) {
				m.logger.Warn("Configured speaker not found in Home Assistant",
					zap.String("speaker", participant.PlayerName),
					zap.String("entity_id", entityID),
					zap.String("mode", modeName))
			}
		}
	}
}

// callServiceWithRetry wraps callService with refresh-on-error logic
// If a service call fails, it refreshes the available speakers and retries once
func (m *Manager) callServiceWithRetry(domain, service string, serviceData map[string]interface{}) error {
	// First attempt
	err := m.callService(domain, service, serviceData)
	if err == nil {
		return nil
	}

	// Check if entity might not exist
	entityID, hasEntity := serviceData["entity_id"].(string)
	if !hasEntity {
		return err // No entity_id, can't validate
	}

	// Refresh available speakers
	m.logger.Info("Service call failed, refreshing available speakers",
		zap.String("entity_id", entityID),
		zap.Error(err))

	if refreshErr := m.refreshAvailableSpeakers(); refreshErr != nil {
		m.logger.Warn("Failed to refresh speakers", zap.Error(refreshErr))
		return err // Return original error
	}

	// Check if entity now exists
	if !m.isSpeakerAvailable(entityID) {
		return fmt.Errorf("speaker %s not available in Home Assistant: %w", entityID, err)
	}

	// Retry once
	m.logger.Info("Retrying service call after refresh", zap.String("entity_id", entityID))
	return m.callService(domain, service, serviceData)
}

// Reset re-evaluates appropriate music mode and triggers playback
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Music - re-selecting appropriate music mode")

	// Get current day phase to determine appropriate mode
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase", zap.Error(err))
		return err
	}

	// Get current music type
	currentMusicType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil {
		m.logger.Error("Failed to get musicPlaybackType", zap.Error(err))
		return err
	}

	// Determine music mode (no trigger key or wake-up event for reset)
	musicMode := m.determineMusicModeFromDayPhase(dayPhase, currentMusicType, "", false)

	m.logger.Info("Reset selected music mode",
		zap.String("day_phase", dayPhase),
		zap.String("current_music_type", currentMusicType),
		zap.String("new_music_mode", musicMode))

	// Check rate limiting (max 1 playback per 10 seconds)
	// If rate-limited, silently drop the reset (matches Node-RED behavior)
	m.mu.Lock()
	timeSinceLastPlayback := m.timeProvider.Now().Sub(m.lastPlaybackTime)
	if timeSinceLastPlayback < 10*time.Second && !m.lastPlaybackTime.IsZero() {
		m.mu.Unlock()
		m.logger.Warn("Rate limiting: dropping reset request (too soon after last playback)",
			zap.Duration("time_since_last", timeSinceLastPlayback),
			zap.String("music_mode", musicMode))
		return nil
	}
	m.lastPlaybackTime = m.timeProvider.Now()

	// Clear currentlyPlaying to allow restart of same mode
	m.currentlyPlaying = nil
	m.mu.Unlock()

	// If empty mode, stop playback
	if musicMode == "" {
		m.logger.Info("Stopping music playback on reset")
		m.stopPlayback()

		// Update state variable
		if err := m.setMusicPlaybackType(""); err != nil {
			if !errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Error("Failed to set music playback type", zap.Error(err))
			}
		}

		m.logger.Info("Successfully reset Music")
		return nil
	}

	// Update the music playback type state variable
	if err := m.setMusicPlaybackType(musicMode); err != nil {
		if !errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Error("Failed to set music playback type", zap.Error(err))
		}
	}

	// Directly trigger playback (even if same mode - that's what reset means)
	if err := m.orchestratePlayback(musicMode, "reset"); err != nil {
		m.logger.Error("Failed to orchestrate playback on reset",
			zap.String("type", musicMode),
			zap.Error(err))
		return err
	}

	m.logger.Info("Successfully reset Music")
	return nil
}

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

	return &shadowCopy
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
