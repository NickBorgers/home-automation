package security

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Timer duration constants
const (
	// LockdownClearDelay is how long to wait after turning off lockdown before turning it back on
	// This ensures a clear activation signal even if lockdown was already on
	LockdownClearDelay = 30 * time.Second

	// LockdownResetDelay is how long to wait before auto-resetting lockdown
	LockdownResetDelay = 5 * time.Second

	// DoorbellRateLimit is the minimum time between doorbell notifications
	DoorbellRateLimit = 20 * time.Second

	// DoorbellFlashDelay is the delay between light flashes for doorbell
	DoorbellFlashDelay = 2 * time.Second

	// VehicleArrivalRateLimit is the minimum time between vehicle arrival notifications
	VehicleArrivalRateLimit = 20 * time.Second

	// ArrivalLockdownGracePeriod is how long after an owner arrives home to suppress
	// "no one home" lockdown activations, guarding against brief presence flaps (issue #991).
	ArrivalLockdownGracePeriod = 5 * time.Minute
)

// Manager handles security-related automation
type Manager struct {
	ctx           context.Context
	haClient      ha.HAClient
	stateManager  *state.Manager
	notifier      notify.Notifier
	logger        *zap.Logger
	readOnly      bool
	clock         clock.Clock
	shadowTracker *shadowstate.SecurityTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Rate limiting for notifications
	lastDoorbellNotification       time.Time
	lastVehicleArrivalNotification time.Time

	// lastOwnerArrivalTime records when didOwnerJustReturnHome last became true.
	// Used to suppress lockdown during brief presence flaps after an owner arrives (issue #991).
	lastOwnerArrivalTime time.Time

	mu sync.Mutex
}

// NewManager creates a new Security manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, notifier notify.Notifier, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewSecurityTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		notifier:      notifier,
		logger:        logger.Named("security"),
		readOnly:      readOnly,
		clock:         clock.NewRealClock(),
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "security", logger.Named("security")),
	}
}

// SetClock sets the clock implementation (useful for testing)
func (m *Manager) SetClock(c clock.Clock) {
	m.clock = c
}

// Start begins monitoring security-related events
func (m *Manager) Start() error {
	m.logger.Info("Starting Security Manager")

	// 1. Subscribe to sleep/home states for lockdown activation (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToState("isEveryoneAsleep", m.handleEveryoneAsleepChange); err != nil {
		return fmt.Errorf("failed to subscribe to isEveryoneAsleep: %w", err)
	}

	if err := m.subHelper.SubscribeToState("isAnyoneHome", m.handleAnyoneHomeChange); err != nil {
		return fmt.Errorf("failed to subscribe to isAnyoneHome: %w", err)
	}

	// 2. Subscribe to didOwnerJustReturnHome for garage auto-open
	if err := m.subHelper.SubscribeToState("didOwnerJustReturnHome", m.handleOwnerReturnHome); err != nil {
		return fmt.Errorf("failed to subscribe to didOwnerJustReturnHome: %w", err)
	}

	// Also register isExpectingSomeone for input capture (used in handlers but not subscribed)
	// Note: SubscriptionHelper auto-registers any subscribed keys for input capture

	// 3. Subscribe to doorbell button
	if err := m.subHelper.SubscribeToEntity("input_button.doorbell", m.handleDoorbellPressed); err != nil {
		return fmt.Errorf("failed to subscribe to doorbell: %w", err)
	}

	// 4. Subscribe to vehicle arriving button
	if err := m.subHelper.SubscribeToEntity("input_button.vehicle_arriving", m.handleVehicleArriving); err != nil {
		return fmt.Errorf("failed to subscribe to vehicle_arriving: %w", err)
	}

	// 5. Subscribe to lockdown activation for auto-reset
	if err := m.subHelper.SubscribeToEntity("input_boolean.lockdown", m.handleLockdownActivated); err != nil {
		return fmt.Errorf("failed to subscribe to lockdown: %w", err)
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("Security Manager started successfully")
	return nil
}

// Stop stops the Security Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Security Manager")

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Security Manager stopped")
}

// handleEveryoneAsleepChange activates lockdown when everyone is asleep
func (m *Manager) handleEveryoneAsleepChange(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	asleep, ok := newValue.(bool)
	if !ok {
		m.logger.Error("Invalid type for isEveryoneAsleep", zap.Any("value", newValue))
		return
	}

	if asleep {
		m.logger.Info("Everyone is asleep, activating lockdown")
		m.activateLockdown("Everyone is asleep", key)
	}
}

// handleAnyoneHomeChange activates lockdown when no one is home
func (m *Manager) handleAnyoneHomeChange(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	anyoneHome, ok := newValue.(bool)
	if !ok {
		m.logger.Error("Invalid type for isAnyoneHome", zap.Any("value", newValue))
		return
	}

	if !anyoneHome {
		// Suppress lockdown if an owner arrived recently — guards against brief presence
		// flaps that cause isAnyoneHome to momentarily go false (issue #991).
		m.mu.Lock()
		timeSinceArrival := m.clock.Since(m.lastOwnerArrivalTime)
		withinGrace := !m.lastOwnerArrivalTime.IsZero() && timeSinceArrival < ArrivalLockdownGracePeriod
		m.mu.Unlock()

		if withinGrace {
			m.logger.Info("No one home but owner arrived recently — suppressing lockdown (arrival grace period)",
				zap.Duration("timeSinceArrival", timeSinceArrival),
				zap.Duration("gracePeriod", ArrivalLockdownGracePeriod))
			return
		}

		m.logger.Info("No one is home, activating lockdown")
		m.activateLockdown("No one is home", key)
	}
}

// activateLockdown turns on the lockdown input_boolean
// It first turns lockdown off, waits 30 seconds, then turns it on to ensure
// a clear activation signal even if lockdown was already on
func (m *Manager) activateLockdown(reason string, trigger string) {
	// Record action in shadow state before executing
	m.recordLockdownAction(true, reason, trigger)

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would activate lockdown (turn off, wait 30s, turn on)", zap.String("reason", reason))
		return
	}

	// Run the off-wait-on sequence in a goroutine to avoid blocking
	go func() {
		// Step 1: Turn lockdown off first
		if err := m.haClient.CallService(m.ctx, "input_boolean", "turn_off", map[string]interface{}{
			"entity_id": "input_boolean.lockdown",
		}); err != nil {
			m.logger.Error("Failed to turn off lockdown before activation", zap.Error(err))
			return
		}
		m.logger.Info("Lockdown turned off, waiting 30 seconds before activation", zap.String("reason", reason))

		// Step 2: Wait 30 seconds
		m.clock.Sleep(LockdownClearDelay)

		// Step 3: Turn lockdown on
		if err := m.haClient.CallService(m.ctx, "input_boolean", "turn_on", map[string]interface{}{
			"entity_id": "input_boolean.lockdown",
		}); err != nil {
			m.logger.Error("Failed to activate lockdown", zap.Error(err))
		} else {
			m.logger.Info("Lockdown activated", zap.String("reason", reason))
		}
	}()
}

// handleLockdownActivated auto-resets lockdown after 5 seconds
func (m *Manager) handleLockdownActivated(entity string, oldState, newState *ha.State) {
	if newState.State == "on" {
		m.logger.Info("Lockdown activated, will auto-reset in 5 seconds")

		// Wait 5 seconds, then reset
		go func() {
			m.clock.Sleep(LockdownResetDelay)

			// Record deactivation in shadow state
			m.recordLockdownAction(false, "Auto-reset after 5 seconds", "lockdown_timer")

			if m.readOnly {
				m.logger.Info("READ-ONLY: Would reset lockdown")
				return
			}

			if err := m.haClient.CallService(m.ctx, "input_boolean", "turn_off", map[string]interface{}{
				"entity_id": "input_boolean.lockdown",
			}); err != nil {
				m.logger.Error("Failed to reset lockdown", zap.Error(err))
			} else {
				m.logger.Info("Lockdown reset")
			}
		}()
	}
}

// handleOwnerReturnHome opens garage door if owner just returned home
func (m *Manager) handleOwnerReturnHome(key string, oldValue, newValue interface{}) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	returned, ok := newValue.(bool)
	if !ok {
		m.logger.Error("Invalid type for didOwnerJustReturnHome", zap.Any("value", newValue))
		return
	}

	if !returned {
		return
	}

	// Record arrival time for lockdown grace period (issue #991).
	// This timestamp persists even after didOwnerJustReturnHome is cleared (e.g. on
	// departure or flap), so we can suppress lockdown for ArrivalLockdownGracePeriod
	// minutes after the owner physically arrived.
	m.mu.Lock()
	m.lastOwnerArrivalTime = m.clock.Now()
	m.mu.Unlock()

	m.logger.Info("Owner just returned home, checking garage status")

	// Check if garage is empty (no vehicle detected)
	currentState, err := m.haClient.GetState("binary_sensor.garage_door_vehicle_detected")
	if err != nil {
		m.logger.Error("Failed to get garage sensor state", zap.Error(err))
		return
	}

	// If sensor is "off", garage is empty
	if currentState.State == "off" {
		m.logger.Info("Garage is empty, opening door")
		m.openGarageDoor(true)
	} else {
		m.logger.Info("Garage is occupied, not opening door")
	}
}

// openGarageDoor opens the garage door
func (m *Manager) openGarageDoor(garageWasEmpty bool) {
	// Record action in shadow state
	m.recordGarageOpenAction("Owner returned home", garageWasEmpty, "didOwnerJustReturnHome")

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would open garage door")
		return
	}

	if err := m.haClient.CallService(m.ctx, "cover", "open_cover", map[string]interface{}{
		"entity_id": "cover.garage_door_door",
	}); err != nil {
		m.logger.Error("Failed to open garage door", zap.Error(err))
	} else {
		m.logger.Info("Garage door opened")
	}
}

// handleDoorbellPressed sends notifications when doorbell is pressed
func (m *Manager) handleDoorbellPressed(entity string, oldState, newState *ha.State) {
	// Rate limit: max 1 notification per 20 seconds
	m.mu.Lock()
	rateLimited := m.clock.Since(m.lastDoorbellNotification) < DoorbellRateLimit
	if rateLimited {
		m.logger.Info("Doorbell notification rate limited")
		m.mu.Unlock()
		// Record the rate-limited event
		m.recordDoorbellEvent(true, false, false, "doorbell")
		return
	}
	m.lastDoorbellNotification = m.clock.Now()
	m.mu.Unlock()

	m.logger.Info("Doorbell pressed, sending notifications")

	// Send TTS notification
	m.sendTTSNotification("Doorbell ringing")

	// Flash lights twice
	go m.flashLightsForDoorbell()

	// Record the successful event
	m.recordDoorbellEvent(false, true, true, "doorbell")
}

// flashLightsForDoorbell flashes lights twice with 2-second delay
func (m *Manager) flashLightsForDoorbell() {
	lights := []string{
		"light.primary_suite",
		"light.living_room",
		"light.independent",
	}

	// First flash
	m.flashLights(lights)

	// Wait 2 seconds
	m.clock.Sleep(DoorbellFlashDelay)

	// Second flash
	m.flashLights(lights)
}

// flashLights flashes the specified lights
func (m *Manager) flashLights(lights []string) {
	if m.readOnly {
		m.logger.Info("READ-ONLY: Would flash lights", zap.Strings("lights", lights))
		return
	}

	if err := m.haClient.CallService(m.ctx, "light", "turn_on", map[string]interface{}{
		"entity_id": lights,
		"flash":     "short",
	}); err != nil {
		m.logger.Error("Failed to flash lights", zap.Error(err))
	}
}

// handleVehicleArriving announces when expected vehicle arrives
func (m *Manager) handleVehicleArriving(entity string, oldState, newState *ha.State) {
	// Shadow state inputs are automatically captured by SubscriptionHelper before this handler runs

	// Check if we're expecting someone
	expectingSomeone, err := m.stateManager.GetBool("isExpectingSomeone")
	if err != nil {
		m.logger.Error("Failed to get isExpectingSomeone state", zap.Error(err))
		return
	}

	if !expectingSomeone {
		m.logger.Info("Vehicle arriving but not expecting anyone")
		// Record the event even if not expecting
		m.recordVehicleArrivalEvent(false, false, false, "vehicle_arriving")
		return
	}

	// Rate limit: max 1 notification per 20 seconds
	m.mu.Lock()
	rateLimited := m.clock.Since(m.lastVehicleArrivalNotification) < VehicleArrivalRateLimit
	if rateLimited {
		m.logger.Info("Vehicle arrival notification rate limited")
		m.mu.Unlock()
		// Record the rate-limited event
		m.recordVehicleArrivalEvent(true, false, true, "vehicle_arriving")
		return
	}
	m.lastVehicleArrivalNotification = m.clock.Now()
	m.mu.Unlock()

	m.logger.Info("Expected vehicle has arrived, sending notification")

	// Send TTS notification
	m.sendTTSNotification("They have arrived")

	// Record the successful event
	m.recordVehicleArrivalEvent(false, true, true, "vehicle_arriving")

	// Reset expecting someone flag
	if !m.readOnly {
		if err := m.haClient.CallService(m.ctx, "input_boolean", "turn_off", map[string]interface{}{
			"entity_id": "input_boolean.expecting_someone",
		}); err != nil {
			m.logger.Error("Failed to reset expecting_someone", zap.Error(err))
		}
	}
}

// sendTTSNotification sends an urgent verbal announcement via the shared
// notifier. Urgent announcements ignore the asleep volume reduction so they
// remain audible at night (doorbell, vehicle arrival, intruder alerts).
func (m *Manager) sendTTSNotification(message string) {
	speakers := []string{
		"media_player.bedroom",
		"media_player.kitchen",
		"media_player.dining_room",
		"media_player.kids_bathroom",
	}

	if err := m.notifier.Announce(m.ctx, message,
		notify.WithSpeakers(speakers),
		notify.WithUrgency(notify.UrgencyUrgent),
	); err != nil {
		m.logger.Error("Failed to send security announcement", zap.Error(err), zap.String("message", message))
		return
	}
	m.logger.Info("Security announcement sent", zap.String("message", message))
}

// addTriggerToInputs adds the trigger field to the current shadow state inputs
// Note: Other inputs are automatically captured by SubscriptionHelper before handlers run
func (m *Manager) addTriggerToInputs(trigger string) {
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{
		"trigger": trigger,
	})
}

// recordLockdownAction captures the current inputs and records a lockdown action in shadow state
func (m *Manager) recordLockdownAction(active bool, reason string, trigger string) {
	// First, update current inputs (includes trigger field)
	m.addTriggerToInputs(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the action
	m.shadowTracker.RecordLockdownAction(active, reason)
}

// recordDoorbellEvent captures the current inputs and records a doorbell event in shadow state
func (m *Manager) recordDoorbellEvent(rateLimited bool, ttsSent bool, lightsFlashed bool, trigger string) {
	// First, update current inputs (includes trigger field)
	m.addTriggerToInputs(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the event
	m.shadowTracker.RecordDoorbellEvent(rateLimited, ttsSent, lightsFlashed)
}

// recordVehicleArrivalEvent captures the current inputs and records a vehicle arrival event in shadow state
func (m *Manager) recordVehicleArrivalEvent(rateLimited bool, ttsSent bool, wasExpecting bool, trigger string) {
	// First, update current inputs (includes trigger field)
	m.addTriggerToInputs(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the event
	m.shadowTracker.RecordVehicleArrivalEvent(rateLimited, ttsSent, wasExpecting)
}

// recordGarageOpenAction captures the current inputs and records a garage open action in shadow state
func (m *Manager) recordGarageOpenAction(reason string, garageWasEmpty bool, trigger string) {
	// First, update current inputs (includes trigger field)
	m.addTriggerToInputs(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the action
	m.shadowTracker.RecordGarageOpenEvent(reason, garageWasEmpty)
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.SecurityShadowState {
	return m.shadowTracker.GetState()
}

// Reset re-evaluates security conditions and resets rate limiters
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Security - re-evaluating lockdown conditions and clearing rate limiters")

	// Clear rate limiters to allow immediate notifications
	m.mu.Lock()
	m.lastDoorbellNotification = time.Time{}
	m.lastVehicleArrivalNotification = time.Time{}
	m.mu.Unlock()

	// Re-evaluate lockdown conditions
	isEveryoneAsleep, err := m.stateManager.GetBool("isEveryoneAsleep")
	if err != nil {
		m.logger.Error("Failed to get isEveryoneAsleep", zap.Error(err))
	} else if isEveryoneAsleep {
		m.logger.Info("Everyone is asleep, re-activating lockdown")
		m.activateLockdown("Everyone is asleep (reset)", "reset")
	}

	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneHome", zap.Error(err))
	} else if !isAnyoneHome {
		m.logger.Info("No one is home, re-activating lockdown")
		m.activateLockdown("No one is home (reset)", "reset")
	}

	m.logger.Info("Successfully reset Security")
	return nil
}
