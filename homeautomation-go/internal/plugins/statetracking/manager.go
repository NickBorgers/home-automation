package statetracking

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Timer duration constants (can be overridden in tests via MockClock)
const (
	// SleepDetectionDelay is how long lights must be off before marking master asleep
	SleepDetectionDelay = 1 * time.Minute

	// WakeDetectionDelay is how long bedroom door must be open before marking master awake
	WakeDetectionDelay = 20 * time.Second

	// OwnerReturnHomeResetDelay is how long before didOwnerJustReturnHome auto-resets
	OwnerReturnHomeResetDelay = 10 * time.Minute

	// DepartureCooldown is how long after leaving the home zone to suppress
	// near_home triggers. On departure, the home zone (smaller) clears before
	// the near_home zone (larger) fires. Without this cooldown, the near_home
	// trigger is indistinguishable from a genuine arrival.
	DepartureCooldown = 2 * time.Minute
)

// Manager handles automatic computation of derived state variables.
// This plugin implements the logic from Node-RED's "State Tracking" flow.
//
// Derived states computed:
//   - isAnyOwnerHome = isNickHome OR isCarolineHome
//   - isAnyoneHome = isAnyOwnerHome OR isToriHere
//   - isAnyoneAsleep = isMasterAsleep OR isGuestAsleep
//   - isEveryoneAsleep = isMasterAsleep AND isGuestAsleep
//
// Additional features:
//   - Automatic master sleep detection when primary suite lights off for 1 minute
//   - Automatic master wake detection when bedroom door open for 20 seconds
//   - Automatic guest sleep detection when guest bedroom door closes
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	helper       *state.DerivedStateHelper
	clock        clock.Clock

	// Timers for sleep/wake detection
	masterSleepTimer clock.Timer
	masterWakeTimer  clock.Timer

	// Timer for owner return home auto-reset
	ownerReturnHomeTimer clock.Timer

	timerMutex sync.Mutex

	// Departure timestamps to suppress near_home false positives (issue #918)
	nickDepartureTime     time.Time
	carolineDepartureTime time.Time

	// Shadow state tracking
	shadowTracker *shadowstate.StateTrackingTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper
}

// NewManager creates a new State Tracking manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewStateTrackingTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("statetracking"),
		readOnly:      readOnly,
		clock:         clock.NewRealClock(),
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "statetracking", logger.Named("statetracking")),
	}
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.StateTrackingShadowState {
	return m.shadowTracker.GetState()
}

// SetClock sets the clock implementation (useful for testing)
func (m *Manager) SetClock(c clock.Clock) {
	m.clock = c
}

// Start begins computing and maintaining derived states.
// This must be called before other plugins that depend on derived states (Music, Security).
func (m *Manager) Start() error {
	m.logger.Info("Starting State Tracking Manager")

	// Create and start the derived state helper
	m.helper = state.NewDerivedStateHelper(m.stateManager, m.logger)

	// Wire up callback to update shadow state when derived states are computed
	m.helper.SetUpdateCallback(func(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep bool) {
		m.shadowTracker.UpdateDerivedStates(anyOwnerHome, anyoneHome, anyoneAsleep, everyoneAsleep)
	})

	if err := m.helper.Start(); err != nil {
		return fmt.Errorf("failed to start derived state helper: %w", err)
	}

	// Subscribe to primary suite lights for master sleep detection (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToEntity("light.primary_suite", m.handlePrimarySuiteLightsChange); err != nil {
		return fmt.Errorf("failed to subscribe to light.primary_suite: %w", err)
	}

	// Subscribe to primary bedroom door for master wake detection
	if err := m.subHelper.SubscribeToEntity("input_boolean.primary_bedroom_door_open", m.handlePrimaryBedroomDoorChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.primary_bedroom_door_open: %w", err)
	}

	// Subscribe to Nick's presence for arrival announcements
	if err := m.subHelper.SubscribeToEntity("input_boolean.nick_home", m.handleNickHomeChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.nick_home: %w", err)
	}

	// Subscribe to Caroline's presence for arrival announcements
	if err := m.subHelper.SubscribeToEntity("input_boolean.caroline_home", m.handleCarolineHomeChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.caroline_home: %w", err)
	}

	// Subscribe to Tori's presence for arrival announcements
	if err := m.subHelper.SubscribeToEntity("input_boolean.tori_here", m.handleToriHereChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.tori_here: %w", err)
	}

	// Subscribe to near_home presence for owner return detection
	// These HomeKit automations are more reliable arrival indicators than zone-based helpers
	if err := m.subHelper.SubscribeToEntity("input_boolean.nick_near_home", m.handleNickNearHomeChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.nick_near_home: %w", err)
	}
	if err := m.subHelper.SubscribeToEntity("input_boolean.caroline_near_home", m.handleCarolineNearHomeChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.caroline_near_home: %w", err)
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("State Tracking Manager started successfully",
		zap.Strings("derivedStates", []string{
			"isAnyOwnerHome",
			"isAnyoneHome",
			"isAnyoneAsleep",
			"isEveryoneAsleep",
		}),
		zap.Strings("sleepDetection", []string{
			"light.primary_suite (1min off → asleep)",
			"input_boolean.primary_bedroom_door_open (20sec open → awake)",
		}),
		zap.Strings("presenceAnnouncements", []string{
			"input_boolean.nick_home (arrival → TTS)",
			"input_boolean.caroline_home (arrival → TTS)",
			"input_boolean.tori_here (arrival → TTS)",
		}),
		zap.Strings("ownerReturnHome", []string{
			"isNickHome/isCarolineHome (arrival → didOwnerJustReturnHome=true, 10min auto-reset)",
			"nick_near_home (on when NOT home → didOwnerJustReturnHome=true)",
			"caroline_near_home (on when NOT home → didOwnerJustReturnHome=true)",
		}))
	return nil
}

// Stop stops the State Tracking Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping State Tracking Manager")

	// Stop any active timers
	m.timerMutex.Lock()
	if m.masterSleepTimer != nil {
		m.masterSleepTimer.Stop()
		m.masterSleepTimer = nil
	}
	if m.masterWakeTimer != nil {
		m.masterWakeTimer.Stop()
		m.masterWakeTimer = nil
	}
	if m.ownerReturnHomeTimer != nil {
		m.ownerReturnHomeTimer.Stop()
		m.ownerReturnHomeTimer = nil
	}
	m.timerMutex.Unlock()

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	if m.helper != nil {
		m.helper.Stop()
	}
	m.logger.Info("State Tracking Manager stopped")
}

// handlePrimarySuiteLightsChange processes primary suite lights state changes
func (m *Manager) handlePrimarySuiteLightsChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	lightsOff := newState.State == "off"

	m.logger.Debug("Primary suite lights changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.Bool("lights_off", lightsOff))

	m.timerMutex.Lock()
	defer m.timerMutex.Unlock()

	// Cancel existing sleep timer if any
	if m.masterSleepTimer != nil {
		m.masterSleepTimer.Stop()
		m.masterSleepTimer = nil
	}

	if lightsOff {
		// Start 1-minute timer for sleep detection
		m.logger.Debug("Primary suite lights turned off, starting 1-minute sleep detection timer")
		m.masterSleepTimer = m.clock.AfterFunc(SleepDetectionDelay, func() {
			m.detectMasterAsleep()
		})
		// Update shadow state
		m.shadowTracker.UpdateSleepDetectionTimer(true)
	} else {
		m.logger.Debug("Primary suite lights turned on, canceling sleep detection")
		// Update shadow state
		m.shadowTracker.UpdateSleepDetectionTimer(false)
	}
}

// detectMasterAsleep runs after lights have been off for 1 minute
func (m *Manager) detectMasterAsleep() {
	m.logger.Debug("1-minute timer expired, checking if should mark master asleep")

	// Check if anyone is home
	isAnyoneHome, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		m.logger.Error("Failed to get isAnyoneHome", zap.Error(err))
		return
	}

	if !isAnyoneHome {
		m.logger.Debug("Nobody home, not marking master asleep")
		return
	}

	// Check if master is already asleep
	isMasterAsleep, err := m.stateManager.GetBool("isMasterAsleep")
	if err != nil {
		m.logger.Error("Failed to get isMasterAsleep", zap.Error(err))
		return
	}

	if isMasterAsleep {
		m.logger.Debug("Master already marked asleep, nothing to do")
		return
	}

	// All checks passed, mark master as asleep
	m.logger.Info("Marking master as asleep (lights off for 1 minute)")
	if err := m.stateManager.SetBool("isMasterAsleep", true); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping isMasterAsleep update in read-only mode")
		} else {
			m.logger.Error("Failed to set isMasterAsleep", zap.Error(err))
		}
	}
}

// handlePrimaryBedroomDoorChange processes primary bedroom door state changes
func (m *Manager) handlePrimaryBedroomDoorChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	doorOpen := newState.State == "on"

	m.logger.Debug("Primary bedroom door changed",
		zap.String("entity_id", entityID),
		zap.String("new_state", newState.State),
		zap.Bool("door_open", doorOpen))

	m.timerMutex.Lock()
	defer m.timerMutex.Unlock()

	// Cancel existing wake timer if any
	if m.masterWakeTimer != nil {
		m.masterWakeTimer.Stop()
		m.masterWakeTimer = nil
	}

	if doorOpen {
		// Start 20-second timer for wake detection
		m.logger.Debug("Primary bedroom door opened, starting 20-second wake detection timer")
		m.masterWakeTimer = m.clock.AfterFunc(WakeDetectionDelay, func() {
			m.detectMasterAwake()
		})
		// Update shadow state
		m.shadowTracker.UpdateWakeDetectionTimer(true)
	} else {
		m.logger.Debug("Primary bedroom door closed, canceling wake detection")
		// Update shadow state
		m.shadowTracker.UpdateWakeDetectionTimer(false)
	}
}

// detectMasterAwake runs after door has been open for 20 seconds
func (m *Manager) detectMasterAwake() {
	m.logger.Info("Marking master as awake (bedroom door open for 20 seconds)")

	if err := m.stateManager.SetBool("isMasterAsleep", false); err != nil {
		if errors.Is(err, state.ErrReadOnlyMode) {
			m.logger.Debug("Skipping isMasterAsleep update in read-only mode")
		} else {
			m.logger.Error("Failed to set isMasterAsleep to false", zap.Error(err))
		}
	}
}

// handleNickHomeChange processes Nick's presence state changes for TTS announcements
func (m *Manager) handleNickHomeChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || oldState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Check if Nick just arrived (state changed to "on" from something else)
	if newState.State == "on" && oldState.State != "on" {
		m.logger.Debug("Nick arrived home, checking if should announce",
			zap.String("entity_id", entityID),
			zap.String("old_state", oldState.State),
			zap.String("new_state", newState.State))

		// Set didOwnerJustReturnHome for garage automation
		m.setOwnerJustReturnedHome()

		// Check if anyone else was already home (Caroline or Tori)
		// We check the OLD value of isAnyoneHome before Nick arrived
		wasAnyoneHome := false
		if isCarolineHome, err := m.stateManager.GetBool("isCarolineHome"); err == nil && isCarolineHome {
			wasAnyoneHome = true
		}
		if isToriHere, err := m.stateManager.GetBool("isToriHere"); err == nil && isToriHere {
			wasAnyoneHome = true
		}

		if wasAnyoneHome {
			// Run announcement asynchronously to avoid deadlocks
			go m.announceArrivalDirect("Nick", "Nick is home", []string{
				"media_player.kitchen",
				"media_player.dining_room",
				"media_player.soundbar",
				"media_player.kids_bathroom",
			})
		} else {
			m.logger.Debug("Nobody else was home, not announcing Nick's arrival")
		}
	} else if newState.State != "on" && oldState.State == "on" {
		// Nick left home - record departure time and clear didOwnerJustReturnHome
		m.timerMutex.Lock()
		m.nickDepartureTime = m.clock.Now()
		m.timerMutex.Unlock()
		m.clearOwnerJustReturnedHome()
	}
}

// handleCarolineHomeChange processes Caroline's presence state changes for TTS announcements
func (m *Manager) handleCarolineHomeChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || oldState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Check if Caroline just arrived (state changed to "on" from something else)
	if newState.State == "on" && oldState.State != "on" {
		m.logger.Debug("Caroline arrived home, checking if should announce",
			zap.String("entity_id", entityID),
			zap.String("old_state", oldState.State),
			zap.String("new_state", newState.State))

		// Set didOwnerJustReturnHome for garage automation
		m.setOwnerJustReturnedHome()

		// Check if anyone else was already home (Nick or Tori)
		wasAnyoneHome := false
		if isNickHome, err := m.stateManager.GetBool("isNickHome"); err == nil && isNickHome {
			wasAnyoneHome = true
		}
		if isToriHere, err := m.stateManager.GetBool("isToriHere"); err == nil && isToriHere {
			wasAnyoneHome = true
		}

		if wasAnyoneHome {
			// Run announcement asynchronously to avoid deadlocks
			go m.announceArrivalDirect("Caroline", "Caroline is home", []string{
				"media_player.kitchen",
				"media_player.dining_room",
				"media_player.kids_bathroom",
				"media_player.soundbar",
				"media_player.office",
			})
		} else {
			m.logger.Debug("Nobody else was home, not announcing Caroline's arrival")
		}
	} else if newState.State != "on" && oldState.State == "on" {
		// Caroline left home - record departure time and clear didOwnerJustReturnHome
		m.timerMutex.Lock()
		m.carolineDepartureTime = m.clock.Now()
		m.timerMutex.Unlock()
		m.clearOwnerJustReturnedHome()
	}
}

// handleToriHereChange processes Tori's presence state changes for TTS announcements
func (m *Manager) handleToriHereChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || oldState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Check if Tori just arrived (state changed to "on" from something else)
	if newState.State == "on" && oldState.State != "on" {
		m.logger.Debug("Tori arrived, checking if should announce",
			zap.String("entity_id", entityID),
			zap.String("old_state", oldState.State),
			zap.String("new_state", newState.State))

		// Check if anyone else was already home (Nick or Caroline)
		wasAnyoneHome := false
		if isNickHome, err := m.stateManager.GetBool("isNickHome"); err == nil && isNickHome {
			wasAnyoneHome = true
		}
		if isCarolineHome, err := m.stateManager.GetBool("isCarolineHome"); err == nil && isCarolineHome {
			wasAnyoneHome = true
		}

		if wasAnyoneHome {
			// Run announcement asynchronously to avoid deadlocks
			go m.announceArrivalDirect("Tori", "Tori is here", []string{
				"media_player.kitchen",
				"media_player.dining_room",
				"media_player.kids_bathroom",
				"media_player.soundbar",
				"media_player.office",
			})
		} else {
			m.logger.Debug("Nobody else was home, not announcing Tori's arrival")
		}
	}
}

// handleNickNearHomeChange processes Nick's near_home state changes for arrival detection
// The near_home geofence is triggered when both arriving AND leaving. We only want to
// set didOwnerJustReturnHome when arriving (i.e., when Nick is NOT currently marked as home).
func (m *Manager) handleNickNearHomeChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || oldState == nil {
		return
	}

	// Check if near_home just turned on (state changed to "on" from something else)
	if newState.State == "on" && oldState.State != "on" {
		m.logger.Debug("Nick near_home triggered, checking if arriving or leaving",
			zap.String("entity_id", entityID),
			zap.String("old_state", oldState.State),
			zap.String("new_state", newState.State))

		// Check if Nick is currently NOT home (indicating arrival, not departure)
		isNickHome, err := m.stateManager.GetBool("isNickHome")
		if err != nil {
			m.logger.Error("Failed to get isNickHome for near_home check", zap.Error(err))
			return
		}

		if !isNickHome {
			// Check departure cooldown to distinguish arrival from departure (issue #918)
			m.timerMutex.Lock()
			timeSinceDeparture := m.clock.Now().Sub(m.nickDepartureTime)
			recentlyDeparted := !m.nickDepartureTime.IsZero() && timeSinceDeparture < DepartureCooldown
			m.timerMutex.Unlock()

			if recentlyDeparted {
				m.logger.Info("Nick near_home triggered but recently departed - suppressing (departure cooldown)",
					zap.Duration("timeSinceDeparture", timeSinceDeparture),
					zap.Duration("cooldown", DepartureCooldown))
				return
			}

			// Nick is NOT home and just triggered near_home → he is ARRIVING
			m.logger.Info("Nick near_home triggered while NOT home - setting didOwnerJustReturnHome",
				zap.Bool("isNickHome", isNickHome))
			m.setOwnerJustReturnedHome()
		} else {
			// Nick IS home and just triggered near_home → he is LEAVING, ignore
			m.logger.Debug("Nick near_home triggered while already home - ignoring (leaving)",
				zap.Bool("isNickHome", isNickHome))
		}
	}
}

// handleCarolineNearHomeChange processes Caroline's near_home state changes for arrival detection
// The near_home geofence is triggered when both arriving AND leaving. We only want to
// set didOwnerJustReturnHome when arriving (i.e., when Caroline is NOT currently marked as home).
func (m *Manager) handleCarolineNearHomeChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || oldState == nil {
		return
	}

	// Check if near_home just turned on (state changed to "on" from something else)
	if newState.State == "on" && oldState.State != "on" {
		m.logger.Debug("Caroline near_home triggered, checking if arriving or leaving",
			zap.String("entity_id", entityID),
			zap.String("old_state", oldState.State),
			zap.String("new_state", newState.State))

		// Check if Caroline is currently NOT home (indicating arrival, not departure)
		isCarolineHome, err := m.stateManager.GetBool("isCarolineHome")
		if err != nil {
			m.logger.Error("Failed to get isCarolineHome for near_home check", zap.Error(err))
			return
		}

		if !isCarolineHome {
			// Check departure cooldown to distinguish arrival from departure (issue #918)
			m.timerMutex.Lock()
			timeSinceDeparture := m.clock.Now().Sub(m.carolineDepartureTime)
			recentlyDeparted := !m.carolineDepartureTime.IsZero() && timeSinceDeparture < DepartureCooldown
			m.timerMutex.Unlock()

			if recentlyDeparted {
				m.logger.Info("Caroline near_home triggered but recently departed - suppressing (departure cooldown)",
					zap.Duration("timeSinceDeparture", timeSinceDeparture),
					zap.Duration("cooldown", DepartureCooldown))
				return
			}

			// Caroline is NOT home and just triggered near_home → she is ARRIVING
			m.logger.Info("Caroline near_home triggered while NOT home - setting didOwnerJustReturnHome",
				zap.Bool("isCarolineHome", isCarolineHome))
			m.setOwnerJustReturnedHome()
		} else {
			// Caroline IS home and just triggered near_home → she is LEAVING, ignore
			m.logger.Debug("Caroline near_home triggered while already home - ignoring (leaving)",
				zap.Bool("isCarolineHome", isCarolineHome))
		}
	}
}

// announceArrivalDirect makes a TTS announcement (caller has already checked if someone is home)
func (m *Manager) announceArrivalDirect(person, message string, mediaPlayers []string) {
	// Skip TTS in read-only mode
	if m.readOnly {
		m.logger.Info("Would announce arrival (read-only mode)",
			zap.String("person", person),
			zap.String("message", message),
			zap.Strings("media_players", mediaPlayers))
		// Still record in shadow state even in read-only mode
		m.shadowTracker.RecordArrivalAnnouncement(person, message)
		return
	}

	// Make the TTS announcement
	m.logger.Info("Announcing arrival via TTS",
		zap.String("person", person),
		zap.String("message", message),
		zap.Strings("media_players", mediaPlayers))

	err := m.haClient.CallService(m.ctx, "tts", "speak", map[string]interface{}{
		"entity_id":              "tts.google_translate_en_com",
		"message":                message,
		"cache":                  true,
		"media_player_entity_id": mediaPlayers,
	})

	if err != nil {
		m.logger.Error("Failed to announce arrival via TTS",
			zap.String("person", person),
			zap.Error(err))
	}

	// Record in shadow state
	m.shadowTracker.RecordArrivalAnnouncement(person, message)
}

// setOwnerJustReturnedHome sets didOwnerJustReturnHome to true and starts/restarts the 10-minute auto-reset timer
func (m *Manager) setOwnerJustReturnedHome() {
	m.logger.Info("Owner just returned home, setting didOwnerJustReturnHome=true")

	// Set the state variable
	if err := m.stateManager.SetBool("didOwnerJustReturnHome", true); err != nil {
		m.logger.Error("Failed to set didOwnerJustReturnHome", zap.Error(err))
		return
	}

	// Start/restart the 10-minute auto-reset timer
	m.timerMutex.Lock()
	defer m.timerMutex.Unlock()

	// Cancel existing timer if any (extends timer if second owner arrives)
	if m.ownerReturnHomeTimer != nil {
		m.ownerReturnHomeTimer.Stop()
	}

	// Start 10-minute timer for auto-reset
	m.logger.Debug("Starting 10-minute auto-reset timer for didOwnerJustReturnHome")
	m.ownerReturnHomeTimer = m.clock.AfterFunc(OwnerReturnHomeResetDelay, func() {
		m.resetOwnerJustReturnedHome()
	})

	// Update shadow state
	m.shadowTracker.UpdateOwnerReturnTimer(true)
}

// clearOwnerJustReturnedHome immediately sets didOwnerJustReturnHome to false (when owner leaves)
func (m *Manager) clearOwnerJustReturnedHome() {
	m.logger.Debug("Owner left home, clearing didOwnerJustReturnHome")

	// Cancel any pending auto-reset timer
	m.timerMutex.Lock()
	if m.ownerReturnHomeTimer != nil {
		m.ownerReturnHomeTimer.Stop()
		m.ownerReturnHomeTimer = nil
	}
	m.timerMutex.Unlock()

	// Clear the state variable
	if err := m.stateManager.SetBool("didOwnerJustReturnHome", false); err != nil {
		m.logger.Error("Failed to clear didOwnerJustReturnHome", zap.Error(err))
	}

	// Update shadow state
	m.shadowTracker.UpdateOwnerReturnTimer(false)
}

// resetOwnerJustReturnedHome is called by the auto-reset timer after 10 minutes
func (m *Manager) resetOwnerJustReturnedHome() {
	m.logger.Info("Auto-resetting didOwnerJustReturnHome to false (10 minutes elapsed)")

	// Clear the timer reference
	m.timerMutex.Lock()
	m.ownerReturnHomeTimer = nil
	m.timerMutex.Unlock()

	// Reset the state variable
	if err := m.stateManager.SetBool("didOwnerJustReturnHome", false); err != nil {
		m.logger.Error("Failed to reset didOwnerJustReturnHome", zap.Error(err))
	}
}

// Reset re-computes all derived states
func (m *Manager) Reset() error {
	m.logger.Info("Resetting State Tracking - re-computing all derived states")

	if m.helper != nil {
		// The helper automatically re-computes all derived states on initialization
		// and whenever source states change, so we just need to trigger a recalculation
		if err := m.helper.Recalculate(); err != nil {
			return fmt.Errorf("failed to recalculate derived states: %w", err)
		}
		m.logger.Info("Successfully re-computed all derived states")
	}

	return nil
}
