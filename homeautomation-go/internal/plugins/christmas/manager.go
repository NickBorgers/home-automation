package christmas

import (
	"sync"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// HolidayLightLabelID is the Home Assistant label ID (slug) for holiday lights
const HolidayLightLabelID = "holiday_light"

// Manager handles Christmas/holiday light activation
type Manager struct {
	haClient      ha.HAClient
	logger        *zap.Logger
	readOnly      bool
	shadowTracker *shadowstate.ChristmasTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// State tracking
	mu sync.Mutex
}

// NewManager creates a new Christmas lights manager
func NewManager(haClient ha.HAClient, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewChristmasTracker()

	return &Manager{
		haClient:      haClient,
		logger:        logger.Named("christmas"),
		readOnly:      readOnly,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, nil, registry, shadowTracker, "christmas", logger.Named("christmas")),
	}
}

// Start begins monitoring for christmas mode activation
func (m *Manager) Start() error {
	m.logger.Info("Starting Christmas Lights Manager")

	// Subscribe to input_boolean.christmas state changes (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToEntity("input_boolean.christmas", m.handleChristmasChange); err != nil {
		return err
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("Christmas Lights Manager started successfully")
	return nil
}

// Stop stops the Christmas Lights Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Christmas Lights Manager")

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Christmas Lights Manager stopped")
}

// handleChristmasChange processes input_boolean.christmas state changes
func (m *Manager) handleChristmasChange(entity string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	oldStateStr := "nil"
	if oldState != nil {
		oldStateStr = oldState.State
	}
	m.logger.Info("Christmas mode state changed",
		zap.String("old", oldStateStr),
		zap.String("new", newState.State))

	if newState.State == "on" {
		m.handleChristmasOn()
	}
	// No action needed when turned off - lights will be turned off by their respective rooms
}

// handleChristmasOn activates holiday lights and resets the toggle
func (m *Manager) handleChristmasOn() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Info("Activating holiday lights")

	// Snapshot inputs for the action
	m.shadowTracker.SnapshotInputsForAction()

	// 1. Turn on all lights with the "Holiday Light" label
	lightsActivated := m.activateHolidayLights()

	// 2. Immediately toggle the input_boolean back to off
	m.resetChristmasToggle()

	// Record action in shadow state
	m.shadowTracker.RecordActivation(lightsActivated, "Holiday lights activated via input_boolean.christmas")
}

// activateHolidayLights turns on all lights with the "holiday_light" label
func (m *Manager) activateHolidayLights() int {
	m.logger.Info("Turning on lights with holiday_light label")

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would turn on lights with label",
			zap.String("label_id", HolidayLightLabelID))
		return 0
	}

	// Call light.turn_on service with target containing label_id
	target := &ha.ServiceTarget{
		LabelID: []string{HolidayLightLabelID},
	}
	err := m.haClient.CallServiceWithTarget("light", "turn_on", target, nil)
	if err != nil {
		m.logger.Error("Failed to turn on holiday lights",
			zap.String("label_id", HolidayLightLabelID),
			zap.Error(err))
		return 0
	}

	m.logger.Info("Holiday lights turned on",
		zap.String("label_id", HolidayLightLabelID))

	// We don't have an easy way to count how many lights were activated
	// Just return 1 to indicate success
	return 1
}

// resetChristmasToggle turns off the input_boolean.christmas
func (m *Manager) resetChristmasToggle() {
	m.logger.Info("Resetting input_boolean.christmas to off")

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would turn off input_boolean.christmas")
		return
	}

	err := m.haClient.CallService("input_boolean", "turn_off", map[string]interface{}{
		"entity_id": "input_boolean.christmas",
	})
	if err != nil {
		m.logger.Error("Failed to reset input_boolean.christmas", zap.Error(err))
	} else {
		m.logger.Info("input_boolean.christmas reset to off")
	}
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.ChristmasShadowState {
	return m.shadowTracker.GetState()
}

// Reset re-evaluates christmas state (checks if it should be active)
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Christmas - checking current state")

	// Get current christmas state from HA
	state, err := m.haClient.GetState("input_boolean.christmas")
	if err != nil {
		m.logger.Error("Failed to get input_boolean.christmas state", zap.Error(err))
		return err
	}

	if state.State == "on" {
		m.logger.Info("Christmas is on, activating holiday lights")
		m.handleChristmasOn()
	} else {
		m.logger.Info("Christmas is off, no action needed")
	}

	m.logger.Info("Successfully reset Christmas")
	return nil
}
