package tv

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Sync box recovery constants
const (
	// SyncBoxDebounceDelay is the time to wait after detecting unavailable state
	// before initiating recovery (prevents false positives during HA restarts)
	SyncBoxDebounceDelay = 30 * time.Second

	// SyncBoxRebootCooldown is the minimum time between power cycle attempts
	SyncBoxRebootCooldown = 10 * time.Minute

	// SyncBoxPowerCycleDelay is the time to wait between turning off and on
	SyncBoxPowerCycleDelay = 5 * time.Second

	// SyncBoxPowerOnMaxRetries is the number of attempts to turn on physical power
	SyncBoxPowerOnMaxRetries = 3

	// SyncBoxPowerOnRetryBaseDelay is the base delay between power-on retries (exponential backoff: 5s, 10s, 20s)
	SyncBoxPowerOnRetryBaseDelay = 5 * time.Second

	// SyncBoxMaxDailyReboots is the maximum number of reboots allowed per day
	SyncBoxMaxDailyReboots = 10

	// Entity IDs for sync box recovery
	SyncBoxSoftwarePowerEntity = "switch.sync_box_power"
	SyncBoxPhysicalPowerEntity = "switch.hue_sync_box_power"
	SyncBoxHDMIInputEntity     = "select.sync_box_hdmi_input"

	// Entity ID for sync box light sync control
	SyncBoxLightSyncEntity = "switch.sync_box_light_sync"

	// Entity ID for the TV remote (actual TV power state)
	TVRemoteEntity = "media_player.sony_xr_65a80k"

	// LightSyncOffDebounce is how long to wait before turning off light sync
	// after Apple TV reports "paused". Prevents flapping due to Apple TV
	// integration briefly reporting "paused" state during active playback.
	LightSyncOffDebounce = 60 * time.Second
)

// Manager handles TV monitoring and manipulation
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool

	// Shadow state tracking
	shadowTracker *shadowstate.TVTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Sync box recovery state
	recoveryMu         sync.Mutex
	lastSyncBoxReboot  time.Time
	dailyRebootCount   int
	rebootCountResetAt time.Time
	recoveryInProgress bool

	// TV remote (panel power) state — tracked locally to avoid HA GetState races
	tvRemoteMu       sync.RWMutex
	tvRemoteOn       bool   // last-known boolean state of media_player.sony_xr_65a80k
	tvRemoteRawState string // last-known raw state ("on"/"off"/"standby"/"unavailable"/...)

	// Light sync debounce state
	lightSyncOffMu       sync.Mutex
	lightSyncOffPending  bool          // true if a turn_off is pending
	lightSyncOffCancel   chan struct{} // signal to cancel pending turn_off
	lightSyncOffDebounce time.Duration // configurable debounce duration

	// For testing: injectable time and sleep functions
	timeNow   func() time.Time
	sleepFunc func(time.Duration)
}

// NewManager creates a new TV manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewTVTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("tv"),
		readOnly:      readOnly,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "tv", logger.Named("tv")),
		// Initialize time functions with defaults
		timeNow:              time.Now,
		sleepFunc:            time.Sleep,
		lightSyncOffDebounce: LightSyncOffDebounce,
	}
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.TVShadowState {
	return m.shadowTracker.GetState()
}

// Start begins monitoring TV-related entities
func (m *Manager) Start() error {
	m.logger.Info("Starting TV Manager")

	// Subscribe to Apple TV media player state changes (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToEntity("media_player.big_beautiful_oled", m.handleAppleTVStateChange); err != nil {
		return fmt.Errorf("failed to subscribe to media_player.big_beautiful_oled: %w", err)
	}

	// Subscribe to TV remote entity (actual TV power state)
	if err := m.subHelper.SubscribeToEntity(TVRemoteEntity, m.handleTVRemoteChange); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", TVRemoteEntity, err)
	}

	// Subscribe to sync box software power state changes (detects crash/unavailable)
	if err := m.subHelper.SubscribeToEntity(SyncBoxSoftwarePowerEntity, m.handleSyncBoxPowerChange); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", SyncBoxSoftwarePowerEntity, err)
	}

	// Subscribe to sync box physical power switch (Z-Wave smart plug)
	if err := m.subHelper.SubscribeToEntity(SyncBoxPhysicalPowerEntity, m.handlePhysicalPowerChange); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", SyncBoxPhysicalPowerEntity, err)
	}

	// Subscribe to HDMI input selector changes
	if err := m.subHelper.SubscribeToEntity(SyncBoxHDMIInputEntity, m.handleHDMIInputChange); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", SyncBoxHDMIInputEntity, err)
	}

	// Subscribe to isAppleTVPlaying state changes to recalculate isTVPlaying
	if err := m.subHelper.SubscribeToState("isAppleTVPlaying", m.handleAppleTVPlayingChange); err != nil {
		return fmt.Errorf("failed to subscribe to isAppleTVPlaying: %w", err)
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	// Initialize current states
	m.logger.Info("Initializing TV states from current HA entities")
	if err := m.initializeStates(); err != nil {
		m.logger.Warn("Failed to initialize some TV states", zap.Error(err))
	}

	m.logger.Info("TV Manager started successfully")
	return nil
}

// Stop stops the TV Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping TV Manager")

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("TV Manager stopped")
}

// initializeStates fetches current HA entity states and initializes state variables
func (m *Manager) initializeStates() error {
	// Get Apple TV state
	appleTVState, err := m.haClient.GetState("media_player.big_beautiful_oled")
	if err == nil && appleTVState != nil {
		m.handleAppleTVStateChange("media_player.big_beautiful_oled", nil, appleTVState)
	} else if err != nil {
		m.logger.Warn("Failed to get initial Apple TV state", zap.Error(err))
	}

	// Get TV remote state (actual TV power)
	tvRemoteState, err := m.haClient.GetState(TVRemoteEntity)
	if err == nil && tvRemoteState != nil {
		m.handleTVRemoteChange(TVRemoteEntity, nil, tvRemoteState)
	} else if err != nil {
		m.logger.Warn("Failed to get initial TV remote state", zap.Error(err))
	}

	// Get sync box power state
	syncBoxState, err := m.haClient.GetState("switch.sync_box_power")
	if err == nil && syncBoxState != nil {
		m.handleSyncBoxPowerChange("switch.sync_box_power", nil, syncBoxState)
	} else if err != nil {
		m.logger.Warn("Failed to get initial sync box state", zap.Error(err))
	}

	// Get HDMI input state
	hdmiInputState, err := m.haClient.GetState("select.sync_box_hdmi_input")
	if err == nil && hdmiInputState != nil {
		m.handleHDMIInputChange("select.sync_box_hdmi_input", nil, hdmiInputState)
	} else if err != nil {
		m.logger.Warn("Failed to get initial HDMI input state", zap.Error(err))
	}

	return nil
}

// handleAppleTVStateChange processes media_player.big_beautiful_oled state changes
func (m *Manager) handleAppleTVStateChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Update shadow state inputs with raw HA entity value
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Check if Apple TV is playing
	isPlaying := newState.State == "playing"

	m.logger.Debug("Apple TV state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.Bool("is_playing", isPlaying))

	// Update isAppleTVPlaying state variable
	if err := m.stateManager.SetBool("isAppleTVPlaying", isPlaying); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping isAppleTVPlaying update in read-only mode",
				zap.Bool("is_playing", isPlaying))
		} else {
			m.logger.Error("Failed to set isAppleTVPlaying", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateAppleTVState(isPlaying, newState.State)
}

// handleTVRemoteChange processes media_player.sony_xr_65a80k state changes (TV panel power).
// The TV media_player acts as a kill switch: when the TV panel turns off, light sync is forced off.
// TV turning on does NOT trigger light sync — that's driven by sync box state changes.
func (m *Manager) handleTVRemoteChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// media_player entities use states like "on", "playing", "paused", "idle", "standby", "off", "unavailable"
	// The TV is considered on when state is anything other than "off", "standby", "unavailable", or "unknown"
	// For Sony Bravia, "standby" means the TV screen is off (low power mode)
	isOn := newState.State != "off" && newState.State != "standby" && newState.State != "unavailable" && newState.State != "unknown"
	m.tvRemoteMu.Lock()
	m.tvRemoteOn = isOn
	m.tvRemoteRawState = newState.State
	m.tvRemoteMu.Unlock()

	m.logger.Debug("TV media_player state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.Bool("is_on", isOn))

	// Only act when TV turns off — force isTVPlaying=false and light sync off.
	// TV turning on is intentionally ignored: the sync box state drives light sync enablement,
	// not the TV panel (which may just be showing a screensaver).
	if !isOn {
		m.logger.Info("TV panel turned off, forcing isTVPlaying=false and light sync off",
			zap.String("tv_state", newState.State))

		if err := m.stateManager.SetBool("isTVPlaying", false); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping isTVPlaying update in read-only mode",
					zap.Bool("is_playing", false))
			} else {
				m.logger.Error("Failed to set isTVPlaying to false", zap.Error(err))
			}
		}
		m.shadowTracker.UpdateTVPlaying(false)
		m.controlSyncBoxLightSync(false)
	}
}

// handleSyncBoxPowerChange processes switch.sync_box_power state changes
func (m *Manager) handleSyncBoxPowerChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Update shadow state inputs with raw HA entity value
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Check for unavailable state (indicates sync box crash)
	if newState.State == "unavailable" {
		m.logger.Warn("Sync box software power is unavailable - may indicate crash",
			zap.String("entity_id", entityID))
		m.shadowTracker.UpdateSyncBoxAvailable(false)

		// Clear TV states immediately — the sync box is down, so nothing is playing
		if err := m.stateManager.SetBool("isTVon", false); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping isTVon update in read-only mode")
			} else {
				m.logger.Error("Failed to set isTVon to false", zap.Error(err))
			}
		}
		m.shadowTracker.UpdateTVPower(false)

		if err := m.stateManager.SetBool("isTVPlaying", false); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping isTVPlaying update in read-only mode")
			} else {
				m.logger.Error("Failed to set isTVPlaying to false", zap.Error(err))
			}
		}
		m.shadowTracker.UpdateTVPlaying(false)
		m.controlSyncBoxLightSync(false)

		// Trigger recovery check asynchronously
		go m.checkAndRecoverSyncBox()
		return
	}

	// Detect recovery from unavailable — the entertainment area may have overridden light states
	recoveredFromUnavailable := oldState != nil && oldState.State == "unavailable"

	// Sync box is available
	m.shadowTracker.UpdateSyncBoxAvailable(true)

	// Check if sync box is on
	isTVOn := newState.State == "on"

	m.logger.Debug("Sync box power state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.Bool("is_tv_on", isTVOn),
		zap.Bool("recovered_from_unavailable", recoveredFromUnavailable))

	// Update isTVon state variable
	if err := m.stateManager.SetBool("isTVon", isTVOn); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping isTVon update in read-only mode",
				zap.Bool("is_tv_on", isTVOn))
		} else {
			m.logger.Error("Failed to set isTVon", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateTVPower(isTVOn)

	// If sync box is off, then it's definitely not playing
	if !isTVOn {
		if err := m.stateManager.SetBool("isTVPlaying", false); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping isTVPlaying update in read-only mode",
					zap.Bool("is_playing", false))
			} else {
				m.logger.Error("Failed to set isTVPlaying to false", zap.Error(err))
			}
		}
		// Update shadow state
		m.shadowTracker.UpdateTVPlaying(false)
	} else {
		// Sync box turned on — recalculate isTVPlaying based on current HDMI input.
		// This handles the race where HDMI input change arrives before power change
		// (both at the same second), so the HDMI handler saw isTVon=false.
		hdmiInputState, err := m.haClient.GetState("select.sync_box_hdmi_input")
		if err != nil {
			m.logger.Warn("Failed to get HDMI input state for recalculation", zap.Error(err))
			return
		}
		if hdmiInputState != nil {
			m.calculateTVPlaying(hdmiInputState.State)
		}
	}

	// After sync box recovery, the Hue entertainment area reconnects and can override
	// light states. Force-notify isTVPlaying so the lighting plugin re-applies the
	// correct scene, even if the value hasn't changed.
	if recoveredFromUnavailable {
		m.logger.Info("Sync box recovered from unavailable, force-notifying isTVPlaying to restore lighting",
			zap.Bool("is_tv_on", isTVOn))
		if err := m.stateManager.ForceNotifyBool("isTVPlaying"); err != nil {
			m.logger.Error("Failed to force-notify isTVPlaying after recovery", zap.Error(err))
		}
	}
}

// handlePhysicalPowerChange processes switch.hue_sync_box_power (Z-Wave plug) state changes
func (m *Manager) handlePhysicalPowerChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	m.logger.Debug("Sync box physical power state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State))
}

// handleHDMIInputChange processes select.sync_box_hdmi_input state changes
func (m *Manager) handleHDMIInputChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Update shadow state inputs with raw HA entity value
	// Shadow state inputs are automatically captured by SubscriptionHelper

	hdmiInput := newState.State

	m.logger.Debug("HDMI input changed",
		zap.String("entity_id", entityID),
		zap.String("new_input", hdmiInput))

	// Update shadow state
	m.shadowTracker.UpdateHDMIInput(hdmiInput)

	// Calculate isTVPlaying based on HDMI input
	m.calculateTVPlaying(hdmiInput)
}

// handleAppleTVPlayingChange is called when isAppleTVPlaying state variable changes
func (m *Manager) handleAppleTVPlayingChange(key string, oldValue, newValue interface{}) {
	m.logger.Debug("isAppleTVPlaying state changed",
		zap.String("key", key),
		zap.Any("old", oldValue),
		zap.Any("new", newValue))

	// Get current HDMI input to recalculate isTVPlaying
	hdmiInputState, err := m.haClient.GetState("select.sync_box_hdmi_input")
	if err != nil {
		m.logger.Warn("Failed to get HDMI input state", zap.Error(err))
		return
	}

	if hdmiInputState != nil {
		m.calculateTVPlaying(hdmiInputState.State)
	}
}

// calculateTVPlaying determines isTVPlaying based on HDMI input and Apple TV state
func (m *Manager) calculateTVPlaying(hdmiInput string) {
	// First check if TV is on - if not, nothing is playing
	isTVOn, err := m.stateManager.GetBool("isTVon")
	if err != nil {
		m.logger.Error("Failed to get isTVon", zap.Error(err))
		return
	}

	// If sync box is off, it's definitely not playing
	if !isTVOn {
		m.logger.Debug("Sync box is off, setting isTVPlaying to false",
			zap.String("hdmi_input", hdmiInput))

		if err := m.stateManager.SetBool("isTVPlaying", false); err != nil {
			if errors.Is(err, state.ErrReadOnlyMode) {
				m.logger.Debug("Skipping isTVPlaying update in read-only mode",
					zap.Bool("is_playing", false))
			} else {
				m.logger.Error("Failed to set isTVPlaying", zap.Error(err))
			}
		}
		m.shadowTracker.UpdateTVPlaying(false)
		m.controlSyncBoxLightSync(false)
		return
	}

	// Also check if TV panel is actually on (tracked locally from remote entity).
	// Prevents enabling light sync when sync box is on but TV is off
	// (e.g., sync box powers on overnight while TV is off).
	m.tvRemoteMu.RLock()
	tvPanelOn := m.tvRemoteOn
	tvRemoteRaw := m.tvRemoteRawState
	m.tvRemoteMu.RUnlock()

	if !tvPanelOn {
		// When the Bravia integration is unavailable we can't trust its panel-off
		// signal — the integrationwatchdog plugin will reload it shortly. In the
		// meantime fall through and trust the sync box's state.
		if tvRemoteRaw == "unavailable" || tvRemoteRaw == "unknown" {
			m.logger.Warn("TV panel reports unavailable/unknown — trusting sync box state",
				zap.String("hdmi_input", hdmiInput),
				zap.String("tv_remote_state", tvRemoteRaw))
			// Fall through to normal HDMI input / Apple TV calculation below
		} else {
			m.logger.Debug("TV panel is off, setting isTVPlaying to false",
				zap.String("hdmi_input", hdmiInput))

			if err := m.stateManager.SetBool("isTVPlaying", false); err != nil {
				if errors.Is(err, state.ErrReadOnlyMode) {
					m.logger.Debug("Skipping isTVPlaying update in read-only mode",
						zap.Bool("is_playing", false))
				} else {
					m.logger.Error("Failed to set isTVPlaying", zap.Error(err))
				}
			}
			m.shadowTracker.UpdateTVPlaying(false)
			m.controlSyncBoxLightSync(false)
			return
		}
	}

	// Check if Apple TV is the current input
	isAppleTVInput := strings.Contains(hdmiInput, "AppleTV")

	var isTVPlaying bool

	if isAppleTVInput {
		// If Apple TV is selected, isTVPlaying = isAppleTVPlaying
		isAppleTVPlaying, err := m.stateManager.GetBool("isAppleTVPlaying")
		if err != nil {
			m.logger.Error("Failed to get isAppleTVPlaying", zap.Error(err))
			return
		}
		isTVPlaying = isAppleTVPlaying
	} else {
		// If other input (e.g., console, cable), assume TV is playing
		isTVPlaying = true
	}

	m.logger.Debug("Calculated isTVPlaying",
		zap.String("hdmi_input", hdmiInput),
		zap.Bool("is_appletv_input", isAppleTVInput),
		zap.Bool("is_tv_playing", isTVPlaying))

	// Update isTVPlaying state variable
	if err := m.stateManager.SetBool("isTVPlaying", isTVPlaying); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping isTVPlaying update in read-only mode",
				zap.Bool("is_playing", isTVPlaying))
		} else {
			m.logger.Error("Failed to set isTVPlaying", zap.Error(err))
		}
	}

	// Update shadow state
	m.shadowTracker.UpdateTVPlaying(isTVPlaying)

	// Control sync box light sync based on TV playing state
	m.controlSyncBoxLightSync(isTVPlaying)
}

// controlSyncBoxLightSync turns the sync box light sync on or off based on TV playing state.
// Turn ON happens immediately; turn OFF is debounced to prevent flapping.
func (m *Manager) controlSyncBoxLightSync(isTVPlaying bool) {
	if m.readOnly {
		m.logger.Debug("Skipping sync box light sync control in read-only mode",
			zap.Bool("would_sync", isTVPlaying))
		return
	}

	if isTVPlaying {
		// Turn ON immediately, and cancel any pending turn-off
		m.cancelPendingLightSyncOff()

		m.logger.Info("Controlling sync box light sync",
			zap.String("action", "turn_on"),
			zap.String("entity_id", SyncBoxLightSyncEntity))

		err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
			"entity_id": SyncBoxLightSyncEntity,
		})
		if err != nil {
			m.logger.Error("Failed to turn on sync box light sync", zap.Error(err))
		}
	} else {
		// Turn OFF with debounce to prevent flapping
		m.scheduleLightSyncOff()
	}
}

// scheduleLightSyncOff schedules a debounced turn-off for the sync box light sync.
// If a turn-off is already pending, this is a no-op.
func (m *Manager) scheduleLightSyncOff() {
	m.lightSyncOffMu.Lock()

	// If already pending, do nothing
	if m.lightSyncOffPending {
		m.logger.Debug("Light sync turn-off already pending, skipping duplicate")
		m.lightSyncOffMu.Unlock()
		return
	}

	m.lightSyncOffPending = true
	m.lightSyncOffCancel = make(chan struct{})
	cancelCh := m.lightSyncOffCancel
	m.lightSyncOffMu.Unlock()

	debounce := m.lightSyncOffDebounce
	m.logger.Debug("Scheduling light sync turn-off with debounce",
		zap.Duration("debounce", debounce))

	go func() {
		// Use a timer so we can cancel it
		timer := time.NewTimer(debounce)
		defer timer.Stop()

		select {
		case <-cancelCh:
			m.logger.Debug("Light sync turn-off cancelled")
			return
		case <-timer.C:
			// Debounce period elapsed, proceed with turn-off
		}

		m.lightSyncOffMu.Lock()
		m.lightSyncOffPending = false
		m.lightSyncOffMu.Unlock()

		m.logger.Info("Controlling sync box light sync",
			zap.String("action", "turn_off"),
			zap.String("entity_id", SyncBoxLightSyncEntity),
			zap.String("reason", "debounce elapsed"))

		err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
			"entity_id": SyncBoxLightSyncEntity,
		})
		if err != nil {
			m.logger.Error("Failed to turn off sync box light sync", zap.Error(err))
		}
	}()
}

// cancelPendingLightSyncOff cancels any pending light sync turn-off.
func (m *Manager) cancelPendingLightSyncOff() {
	m.lightSyncOffMu.Lock()
	defer m.lightSyncOffMu.Unlock()

	if m.lightSyncOffPending && m.lightSyncOffCancel != nil {
		close(m.lightSyncOffCancel)
		m.lightSyncOffPending = false
		m.lightSyncOffCancel = nil
		m.logger.Debug("Cancelled pending light sync turn-off")
	}
}

// IsLightSyncOffPending returns whether a light sync turn-off is pending (for testing)
func (m *Manager) IsLightSyncOffPending() bool {
	m.lightSyncOffMu.Lock()
	defer m.lightSyncOffMu.Unlock()
	return m.lightSyncOffPending
}

// checkAndRecoverSyncBox checks if sync box recovery is needed and performs power cycle
func (m *Manager) checkAndRecoverSyncBox() {
	m.recoveryMu.Lock()

	// Check if recovery is already in progress
	if m.recoveryInProgress {
		m.logger.Debug("Recovery already in progress, skipping")
		m.recoveryMu.Unlock()
		return
	}

	m.recoveryInProgress = true
	m.recoveryMu.Unlock()

	defer func() {
		m.recoveryMu.Lock()
		m.recoveryInProgress = false
		m.recoveryMu.Unlock()
	}()

	// Debounce: wait before checking again (prevents false positives during HA restart)
	m.logger.Info("Sync box unavailable detected, waiting for debounce period",
		zap.Duration("delay", SyncBoxDebounceDelay))
	m.sleepFunc(SyncBoxDebounceDelay)

	// Re-check if sync box is still unavailable
	syncBoxState, err := m.haClient.GetState(SyncBoxSoftwarePowerEntity)
	if err != nil {
		m.logger.Error("Failed to get sync box state during recovery check", zap.Error(err))
		return
	}
	if syncBoxState == nil {
		m.logger.Error("Sync box state is nil during recovery check")
		return
	}
	if syncBoxState.State != "unavailable" {
		m.logger.Info("Sync box recovered on its own, no action needed",
			zap.String("current_state", syncBoxState.State))
		m.shadowTracker.UpdateSyncBoxAvailable(true)
		return
	}

	// Check if physical power is on - only attempt recovery if power is on
	physicalPowerState, err := m.haClient.GetState(SyncBoxPhysicalPowerEntity)
	if err != nil {
		m.logger.Error("Failed to get physical power state", zap.Error(err))
		return
	}
	if physicalPowerState == nil {
		m.logger.Error("Physical power state is nil during recovery check")
		return
	}
	if physicalPowerState.State != "on" {
		m.logger.Warn("Physical power is off - turning it back on",
			zap.String("physical_power_state", physicalPowerState.State))
		m.ensurePhysicalPowerOn()
		return
	}

	// Check recovery safeguards
	if !m.canAttemptRecovery() {
		return
	}

	// Perform the power cycle recovery
	m.performPowerCycleRecovery()
}

// canAttemptRecovery checks if we're allowed to attempt recovery based on safeguards
func (m *Manager) canAttemptRecovery() bool {
	now := m.timeNow()

	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()

	// Reset daily counter if it's a new day
	if now.Sub(m.rebootCountResetAt) > 24*time.Hour {
		m.dailyRebootCount = 0
		m.rebootCountResetAt = now
	}

	// Check daily limit
	if m.dailyRebootCount >= SyncBoxMaxDailyReboots {
		m.logger.Error("Maximum daily reboots reached, skipping recovery",
			zap.Int("daily_count", m.dailyRebootCount),
			zap.Int("max_allowed", SyncBoxMaxDailyReboots))
		return false
	}

	// Check cooldown
	if !m.lastSyncBoxReboot.IsZero() && now.Sub(m.lastSyncBoxReboot) < SyncBoxRebootCooldown {
		m.logger.Warn("Sync box still unavailable but in cooldown period",
			zap.Duration("time_since_last_reboot", now.Sub(m.lastSyncBoxReboot)),
			zap.Duration("cooldown_required", SyncBoxRebootCooldown))
		return false
	}

	return true
}

// performPowerCycleRecovery performs the actual power cycle recovery sequence
func (m *Manager) performPowerCycleRecovery() {
	// Skip in read-only mode
	if m.readOnly {
		m.logger.Info("Skipping sync box power cycle recovery in read-only mode")
		return
	}

	m.logger.Warn("Initiating Hue Sync Box power cycle recovery")

	// Update tracking state
	m.recoveryMu.Lock()
	m.lastSyncBoxReboot = m.timeNow()
	m.dailyRebootCount++
	rebootCount := m.dailyRebootCount
	m.recoveryMu.Unlock()

	// Update shadow state with recovery info
	m.shadowTracker.UpdateLastRecovery(m.lastSyncBoxReboot, rebootCount)

	// Step 1: Turn off physical power
	m.logger.Info("Turning off sync box physical power")
	err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": SyncBoxPhysicalPowerEntity,
	})
	if err != nil {
		m.logger.Error("Failed to turn off sync box physical power", zap.Error(err))
		return
	}

	// Step 2: Wait for power cycle delay
	m.logger.Info("Waiting for power cycle delay",
		zap.Duration("delay", SyncBoxPowerCycleDelay))
	m.sleepFunc(SyncBoxPowerCycleDelay)

	// Step 3: Turn on physical power with retries
	for attempt := 1; attempt <= SyncBoxPowerOnMaxRetries; attempt++ {
		m.logger.Info("Turning on sync box physical power",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", SyncBoxPowerOnMaxRetries))

		err = m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
			"entity_id": SyncBoxPhysicalPowerEntity,
		})
		if err == nil {
			m.logger.Info("Sync box power cycle recovery completed",
				zap.Int("daily_reboot_count", rebootCount),
				zap.Int("attempt", attempt))
			return
		}

		m.logger.Error("Failed to turn on sync box physical power (will retry)",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", SyncBoxPowerOnMaxRetries),
			zap.Error(err))

		if attempt < SyncBoxPowerOnMaxRetries {
			retryDelay := SyncBoxPowerOnRetryBaseDelay * time.Duration(1<<(attempt-1))
			m.logger.Info("Waiting before retry",
				zap.Duration("delay", retryDelay))
			m.sleepFunc(retryDelay)
		}
	}

	m.logger.Error("CRITICAL: All attempts to turn on sync box physical power failed during power cycle",
		zap.Int("attempts", SyncBoxPowerOnMaxRetries),
		zap.Int("daily_reboot_count", rebootCount))
}

// ensurePhysicalPowerOn turns on the Z-Wave switch with retries (no turn-off step).
// Used when physical power is found off during recovery — just needs to be turned back on.
func (m *Manager) ensurePhysicalPowerOn() {
	if m.readOnly {
		m.logger.Info("Skipping physical power-on in read-only mode")
		return
	}

	for attempt := 1; attempt <= SyncBoxPowerOnMaxRetries; attempt++ {
		m.logger.Info("Turning on sync box physical power",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", SyncBoxPowerOnMaxRetries))

		err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
			"entity_id": SyncBoxPhysicalPowerEntity,
		})
		if err == nil {
			m.logger.Info("Sync box physical power turned on successfully",
				zap.Int("attempt", attempt))
			return
		}

		m.logger.Error("Failed to turn on sync box physical power (will retry)",
			zap.Int("attempt", attempt),
			zap.Int("max_attempts", SyncBoxPowerOnMaxRetries),
			zap.Error(err))

		if attempt < SyncBoxPowerOnMaxRetries {
			retryDelay := SyncBoxPowerOnRetryBaseDelay * time.Duration(1<<(attempt-1))
			m.logger.Info("Waiting before retry",
				zap.Duration("delay", retryDelay))
			m.sleepFunc(retryDelay)
		}
	}

	m.logger.Error("CRITICAL: All attempts to turn on sync box physical power failed",
		zap.Int("attempts", SyncBoxPowerOnMaxRetries))
}

// GetRecoveryState returns the current recovery state for testing
func (m *Manager) GetRecoveryState() (lastReboot time.Time, dailyCount int, inProgress bool) {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	return m.lastSyncBoxReboot, m.dailyRebootCount, m.recoveryInProgress
}
