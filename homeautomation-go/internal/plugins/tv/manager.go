package tv

import (
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

	// SyncBoxMaxDailyReboots is the maximum number of reboots allowed per day
	SyncBoxMaxDailyReboots = 10

	// Entity IDs for sync box recovery
	SyncBoxSoftwarePowerEntity = "switch.sync_box_power"
	SyncBoxPhysicalPowerEntity = "switch.hue_sync_box_power"
	SyncBoxHDMIInputEntity     = "select.sync_box_hdmi_input"

	// Entity ID for sync box light sync control
	SyncBoxLightSyncEntity = "switch.sync_box_light_sync"
)

// Manager handles TV monitoring and manipulation
type Manager struct {
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

	// For testing: injectable time and sleep functions
	timeNow   func() time.Time
	sleepFunc func(time.Duration)
}

// NewManager creates a new TV manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewTVTracker()

	return &Manager{
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("tv"),
		readOnly:      readOnly,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "tv", logger.Named("tv")),
		// Initialize time functions with defaults
		timeNow:   time.Now,
		sleepFunc: time.Sleep,
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

		// Trigger recovery check asynchronously
		go m.checkAndRecoverSyncBox()
		return
	}

	// Sync box is available
	m.shadowTracker.UpdateSyncBoxAvailable(true)

	// Check if sync box is on
	isTVOn := newState.State == "on"

	m.logger.Debug("Sync box power state changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.Bool("is_tv_on", isTVOn))

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

	// If TV is off, then it's definitely not playing
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

	// If TV is off, it's definitely not playing
	if !isTVOn {
		m.logger.Debug("TV is off, setting isTVPlaying to false",
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

// controlSyncBoxLightSync turns the sync box light sync on or off based on TV playing state
func (m *Manager) controlSyncBoxLightSync(isTVPlaying bool) {
	if m.readOnly {
		m.logger.Debug("Skipping sync box light sync control in read-only mode",
			zap.Bool("would_sync", isTVPlaying))
		return
	}

	action := "turn_off"
	if isTVPlaying {
		action = "turn_on"
	}

	m.logger.Info("Controlling sync box light sync",
		zap.String("action", action),
		zap.String("entity_id", SyncBoxLightSyncEntity))

	err := m.haClient.CallService("switch", action, map[string]interface{}{
		"entity_id": SyncBoxLightSyncEntity,
	})
	if err != nil {
		m.logger.Error("Failed to control sync box light sync",
			zap.String("action", action),
			zap.Error(err))
		return
	}

	m.logger.Debug("Sync box light sync controlled successfully",
		zap.String("action", action))
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
		m.logger.Info("Physical power is off, sync box intentionally unpowered - no recovery needed",
			zap.String("physical_power_state", physicalPowerState.State))
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
	err := m.haClient.CallService("switch", "turn_off", map[string]interface{}{
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

	// Step 3: Turn on physical power
	m.logger.Info("Turning on sync box physical power")
	err = m.haClient.CallService("switch", "turn_on", map[string]interface{}{
		"entity_id": SyncBoxPhysicalPowerEntity,
	})
	if err != nil {
		m.logger.Error("Failed to turn on sync box physical power", zap.Error(err))
		return
	}

	m.logger.Info("Sync box power cycle recovery completed",
		zap.Int("daily_reboot_count", rebootCount))
}

// GetRecoveryState returns the current recovery state for testing
func (m *Manager) GetRecoveryState() (lastReboot time.Time, dailyCount int, inProgress bool) {
	m.recoveryMu.Lock()
	defer m.recoveryMu.Unlock()
	return m.lastSyncBoxReboot, m.dailyRebootCount, m.recoveryInProgress
}
