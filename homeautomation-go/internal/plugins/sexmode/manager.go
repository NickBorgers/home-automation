package sexmode

import (
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// EightSleep climate control constants
const (
	// EightSleepMinTemp is the minimum temperature setting for Eight Sleep (-100 to 100 range)
	EightSleepMinTemp = -100 // Coldest setting

	// EightSleepNickEntity is Nick's Eight Sleep climate entity
	EightSleepNickEntity = "climate.nick_s_eight_sleep_side_climate"

	// EightSleepCarolineEntity is Caroline's Eight Sleep climate entity
	EightSleepCarolineEntity = "climate.caroline_s_eight_sleep_side_climate"
)

// Manager handles sex mode coordination across music, lighting, and climate
type Manager struct {
	haClient      ha.HAClient
	stateManager  *state.Manager
	logger        *zap.Logger
	readOnly      bool
	shadowTracker *shadowstate.SexModeTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// State tracking
	mu              sync.Mutex
	isActive        bool
	preSexMusicType string
	activatedAt     time.Time
}

// NewManager creates a new Sex Mode manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewSexModeTracker()

	return &Manager{
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("sexmode"),
		readOnly:      readOnly,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "sexmode", logger.Named("sexmode")),
	}
}

// Start begins monitoring for sex mode activation
func (m *Manager) Start() error {
	m.logger.Info("Starting Sex Mode Manager")

	// Subscribe to input_boolean.sex state changes (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToEntity("input_boolean.sex", m.handleSexModeChange); err != nil {
		return err
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("Sex Mode Manager started successfully")
	return nil
}

// Stop stops the Sex Mode Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Sex Mode Manager")

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Sex Mode Manager stopped")
}

// handleSexModeChange processes input_boolean.sex state changes
func (m *Manager) handleSexModeChange(entity string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	oldStateStr := "nil"
	if oldState != nil {
		oldStateStr = oldState.State
	}
	m.logger.Info("Sex mode state changed",
		zap.String("old", oldStateStr),
		zap.String("new", newState.State))

	if newState.State == "on" {
		m.handleSexModeOn()
	} else if newState.State == "off" {
		m.handleSexModeOff()
	}
}

// handleSexModeOn activates sex mode: music, lighting, and climate control
func (m *Manager) handleSexModeOn() {
	m.mu.Lock()
	if m.isActive {
		m.mu.Unlock()
		m.logger.Debug("Sex mode already active, ignoring duplicate activation")
		return
	}
	m.mu.Unlock()

	m.logger.Info("Activating sex mode")

	// 1. Save current music playback type and set to "sex"
	m.saveMusicStateAndActivate()

	// 2. Activate night lighting scene for Primary Suite only
	m.activatePrimarySuiteNightScene()

	// 3. Set Eight Sleep to coldest setting
	m.setEightSleepToColdest()

	// Update state tracking
	m.mu.Lock()
	m.isActive = true
	m.activatedAt = time.Now()
	m.mu.Unlock()

	// Record action in shadow state
	m.recordAction("activate", "Sex mode activated", "input_boolean.sex")
}

// handleSexModeOff deactivates sex mode and restores previous state
func (m *Manager) handleSexModeOff() {
	m.mu.Lock()
	if !m.isActive {
		m.mu.Unlock()
		m.logger.Debug("Sex mode not active, ignoring deactivation")
		return
	}
	m.mu.Unlock()

	m.logger.Info("Deactivating sex mode")

	// 1. Restore previous music playback type
	m.restoreMusicState()

	// 2. Re-evaluate Primary Suite lighting based on current conditions
	m.reevaluatePrimarySuiteLighting()

	// 3. Let Eight Sleep return to its schedule (no action needed)
	m.logger.Info("Eight Sleep will return to normal schedule automatically")

	// Update state tracking
	m.mu.Lock()
	m.isActive = false
	m.preSexMusicType = ""
	m.activatedAt = time.Time{}
	m.mu.Unlock()

	// Record action in shadow state
	m.recordAction("deactivate", "Sex mode deactivated", "input_boolean.sex")
}

// saveMusicStateAndActivate saves the current music type and activates sex music
func (m *Manager) saveMusicStateAndActivate() {
	// Get current music playback type
	currentType, err := m.stateManager.GetString("musicPlaybackType")
	if err != nil {
		m.logger.Warn("Failed to get current musicPlaybackType, using empty string", zap.Error(err))
		currentType = ""
	}

	m.mu.Lock()
	m.preSexMusicType = currentType
	m.mu.Unlock()

	m.logger.Info("Saving current music type and activating sex music",
		zap.String("previous_type", currentType))

	// Set music playback type to "sex"
	if m.readOnly {
		m.logger.Info("READ-ONLY: Would set musicPlaybackType to 'sex'")
		return
	}

	if err := m.stateManager.SetString("musicPlaybackType", "sex"); err != nil {
		m.logger.Error("Failed to set musicPlaybackType to sex", zap.Error(err))
	}
}

// restoreMusicState restores the previous music playback type
func (m *Manager) restoreMusicState() {
	m.mu.Lock()
	previousType := m.preSexMusicType
	m.mu.Unlock()

	m.logger.Info("Restoring previous music type",
		zap.String("type", previousType))

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would restore musicPlaybackType",
			zap.String("type", previousType))
		return
	}

	if err := m.stateManager.SetString("musicPlaybackType", previousType); err != nil {
		m.logger.Error("Failed to restore musicPlaybackType", zap.Error(err))
	}
}

// activatePrimarySuiteNightScene activates the night scene for Primary Suite only
func (m *Manager) activatePrimarySuiteNightScene() {
	m.logger.Info("Activating night scene for Primary Suite")

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would activate scene.primary_suite_night")
		return
	}

	// Call scene.turn_on service
	err := m.haClient.CallService("scene", "turn_on", map[string]interface{}{
		"entity_id": "scene.primary_suite_night",
	})
	if err != nil {
		m.logger.Error("Failed to activate Primary Suite night scene", zap.Error(err))
	} else {
		m.logger.Info("Primary Suite night scene activated")
	}
}

// reevaluatePrimarySuiteLighting re-evaluates lighting for Primary Suite based on current conditions
func (m *Manager) reevaluatePrimarySuiteLighting() {
	// Get current day phase to determine appropriate scene
	dayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Error("Failed to get dayPhase for lighting restoration", zap.Error(err))
		dayPhase = "night" // Default to night if unknown
	}

	// Check if master is asleep - if so, turn off lights
	isMasterAsleep, err := m.stateManager.GetBool("isMasterAsleep")
	if err != nil {
		m.logger.Warn("Failed to get isMasterAsleep", zap.Error(err))
		isMasterAsleep = false
	}

	m.logger.Info("Re-evaluating Primary Suite lighting",
		zap.String("day_phase", dayPhase),
		zap.Bool("is_master_asleep", isMasterAsleep))

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would re-evaluate Primary Suite lighting",
			zap.String("day_phase", dayPhase),
			zap.Bool("is_master_asleep", isMasterAsleep))
		return
	}

	if isMasterAsleep {
		// Turn off Primary Suite lights
		err := m.haClient.CallService("light", "turn_off", map[string]interface{}{
			"area_id": "master_bedroom",
		})
		if err != nil {
			m.logger.Error("Failed to turn off Primary Suite lights", zap.Error(err))
		} else {
			m.logger.Info("Primary Suite lights turned off (master is asleep)")
		}
	} else {
		// Activate scene based on current day phase
		sceneEntityID := fmt.Sprintf("scene.primary_suite_%s", dayPhase)
		err := m.haClient.CallService("scene", "turn_on", map[string]interface{}{
			"entity_id":  sceneEntityID,
			"transition": 30, // Smooth transition
		})
		if err != nil {
			m.logger.Error("Failed to activate Primary Suite scene",
				zap.String("scene", sceneEntityID),
				zap.Error(err))
		} else {
			m.logger.Info("Primary Suite scene activated",
				zap.String("scene", sceneEntityID))
		}
	}
}

// setEightSleepToColdest sets both Eight Sleep sides to the coldest setting
func (m *Manager) setEightSleepToColdest() {
	m.logger.Info("Setting Eight Sleep to coldest",
		zap.Int("temperature", EightSleepMinTemp))

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would set Eight Sleep to coldest",
			zap.String("nick_entity", EightSleepNickEntity),
			zap.String("caroline_entity", EightSleepCarolineEntity),
			zap.Int("temperature", EightSleepMinTemp))
		return
	}

	// Set Nick's side using climate.set_temperature
	err := m.haClient.CallService("climate", "set_temperature", map[string]interface{}{
		"entity_id":   EightSleepNickEntity,
		"temperature": EightSleepMinTemp,
	})
	if err != nil {
		m.logger.Error("Failed to set Nick's Eight Sleep to coldest", zap.Error(err))
	} else {
		m.logger.Info("Nick's Eight Sleep set to coldest")
	}

	// Set Caroline's side using climate.set_temperature
	err = m.haClient.CallService("climate", "set_temperature", map[string]interface{}{
		"entity_id":   EightSleepCarolineEntity,
		"temperature": EightSleepMinTemp,
	})
	if err != nil {
		m.logger.Error("Failed to set Caroline's Eight Sleep to coldest", zap.Error(err))
	} else {
		m.logger.Info("Caroline's Eight Sleep set to coldest")
	}
}

// addTriggerToInputs adds the trigger field to the current shadow state inputs
// Note: Other inputs are automatically captured by SubscriptionHelper before handlers run
func (m *Manager) addTriggerToInputs(trigger string) {
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{
		"trigger": trigger,
	})
}

// recordAction captures inputs and records an action in shadow state
func (m *Manager) recordAction(actionType, reason, trigger string) {
	m.addTriggerToInputs(trigger)
	m.shadowTracker.SnapshotInputsForAction()

	m.mu.Lock()
	preSexMusicType := m.preSexMusicType
	isActive := m.isActive
	activatedAt := m.activatedAt
	m.mu.Unlock()

	m.shadowTracker.RecordAction(actionType, reason, isActive, preSexMusicType, activatedAt)
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.SexModeShadowState {
	return m.shadowTracker.GetState()
}

// Reset re-evaluates sex mode state (checks if it should be active)
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Sex Mode - checking current state")

	// Get current sex mode state from HA
	state, err := m.haClient.GetState("input_boolean.sex")
	if err != nil {
		m.logger.Error("Failed to get input_boolean.sex state", zap.Error(err))
		return err
	}

	m.mu.Lock()
	wasActive := m.isActive
	m.mu.Unlock()

	if state.State == "on" && !wasActive {
		m.logger.Info("Sex mode should be active, activating")
		m.handleSexModeOn()
	} else if state.State == "off" && wasActive {
		m.logger.Info("Sex mode should be inactive, deactivating")
		m.handleSexModeOff()
	} else {
		m.logger.Info("Sex mode state is correct, no action needed",
			zap.String("ha_state", state.State),
			zap.Bool("is_active", wasActive))
	}

	m.logger.Info("Successfully reset Sex Mode")
	return nil
}
