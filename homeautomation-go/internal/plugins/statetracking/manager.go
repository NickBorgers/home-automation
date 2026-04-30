package statetracking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
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

	// ArrivalDebounceDuration is how long after a departure to suppress
	// re-arrival triggers. When a presence sensor bounces (GPS/WiFi flicker),
	// the person briefly drops out then re-appears. Without this debounce,
	// each re-appearance triggers a fresh TTS announcement and garage open.
	ArrivalDebounceDuration = 5 * time.Minute
)

// Manager handles automatic computation of derived state variables.
// This plugin implements the logic from Node-RED's "State Tracking" flow.
//
// Derived states computed:
//   - isAnyOwnerHome = isNickHome OR isCarolineHome
//   - isAnyoneHome = isAnyOwnerHome OR isAssistantHere
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
	notifier     notify.Notifier
	clock        clock.Clock

	// Timers for sleep/wake detection
	masterSleepTimer clock.Timer
	masterWakeTimer  clock.Timer

	// Timer for owner return home auto-reset
	ownerReturnHomeTimer clock.Timer

	timerMutex sync.Mutex

	// Departure timestamps to suppress near_home false positives (issue #918)
	// and arrival bounce debounce (issue #922)
	nickDepartureTime      time.Time
	carolineDepartureTime  time.Time
	assistantDepartureTime time.Time

	// SoCo-CLI base URL for querying Sonos speaker groups (optional)
	socoCliURL string

	// Shadow state tracking
	shadowTracker *shadowstate.StateTrackingTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper
}

// NewManager creates a new State Tracking manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, socoCliURL string, notifier notify.Notifier) *Manager {
	shadowTracker := shadowstate.NewStateTrackingTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("statetracking"),
		readOnly:      readOnly,
		clock:         clock.NewRealClock(),
		socoCliURL:    socoCliURL,
		notifier:      notifier,
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

	// Subscribe to Assistant's presence for arrival announcements
	if err := m.subHelper.SubscribeToEntity("input_boolean.assistant_here", m.handleAssistantHereChange); err != nil {
		return fmt.Errorf("failed to subscribe to input_boolean.assistant_here: %w", err)
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
			"input_boolean.assistant_here (arrival → TTS)",
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

	// Re-validate trigger condition: confirm lights are still off before marking asleep.
	// During the 1-minute delay the scene may have been activated (e.g. owner arrived home),
	// so we must re-read the current entity state rather than assuming it hasn't changed. (#1017)
	lightState, err := m.haClient.GetState("light.primary_suite")
	if err != nil {
		m.logger.Warn("Failed to re-read light.primary_suite, aborting sleep detection", zap.Error(err))
		return
	}
	if lightState.State != "off" {
		m.logger.Debug("Primary suite lights no longer off, aborting sleep detection",
			zap.String("light_state", lightState.State))
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
	// Re-validate trigger condition: confirm the bedroom door is still open before marking awake.
	// The door may have been closed again during the 20-second delay. (#1017)
	doorState, err := m.haClient.GetState("input_boolean.primary_bedroom_door_open")
	if err != nil {
		m.logger.Warn("Failed to re-read primary_bedroom_door_open, aborting wake detection", zap.Error(err))
		return
	}
	if doorState.State != "on" {
		m.logger.Debug("Primary bedroom door no longer open, aborting wake detection",
			zap.String("door_state", doorState.State))
		return
	}

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

		// Cancel any pending sleep detection timer when an owner arrives home (issue #991).
		// The timer may have been started when lights went off during away/lockdown behavior
		// for the departing owner, and must not fire now that someone has returned.
		m.timerMutex.Lock()
		if m.masterSleepTimer != nil {
			m.logger.Info("Owner arrived home - cancelling pending sleep detection timer")
			m.masterSleepTimer.Stop()
			m.masterSleepTimer = nil
			m.shadowTracker.UpdateSleepDetectionTimer(false)
		}
		m.timerMutex.Unlock()

		// Check arrival debounce to suppress presence sensor bounce (issue #922)
		m.timerMutex.Lock()
		timeSinceDeparture := m.clock.Now().Sub(m.nickDepartureTime)
		recentlyDeparted := !m.nickDepartureTime.IsZero() && timeSinceDeparture < ArrivalDebounceDuration
		m.timerMutex.Unlock()

		if recentlyDeparted {
			m.logger.Info("Nick arrival suppressed - presence sensor bounce (arrival debounce)",
				zap.Duration("timeSinceDeparture", timeSinceDeparture),
				zap.Duration("debounceDuration", ArrivalDebounceDuration))
			return
		}

		// Set didOwnerJustReturnHome for garage automation
		m.setOwnerJustReturnedHome()

		// Check if anyone else was already home (Caroline or Assistant)
		// We check the OLD value of isAnyoneHome before Nick arrived
		wasAnyoneHome := false
		if isCarolineHome, err := m.stateManager.GetBool("isCarolineHome"); err == nil && isCarolineHome {
			wasAnyoneHome = true
		}
		if isAssistantHere, err := m.stateManager.GetBool("isAssistantHere"); err == nil && isAssistantHere {
			wasAnyoneHome = true
		}

		if wasAnyoneHome {
			// Run announcement asynchronously to avoid deadlocks
			go m.announceArrivalDirect("Nick", "Nick is home", []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
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

		// Cancel any pending sleep detection timer when an owner arrives home (issue #991).
		// The timer may have been started when lights went off during away/lockdown behavior
		// for the departing owner, and must not fire now that someone has returned.
		m.timerMutex.Lock()
		if m.masterSleepTimer != nil {
			m.logger.Info("Owner arrived home - cancelling pending sleep detection timer")
			m.masterSleepTimer.Stop()
			m.masterSleepTimer = nil
			m.shadowTracker.UpdateSleepDetectionTimer(false)
		}
		m.timerMutex.Unlock()

		// Check arrival debounce to suppress presence sensor bounce (issue #922)
		m.timerMutex.Lock()
		timeSinceDeparture := m.clock.Now().Sub(m.carolineDepartureTime)
		recentlyDeparted := !m.carolineDepartureTime.IsZero() && timeSinceDeparture < ArrivalDebounceDuration
		m.timerMutex.Unlock()

		if recentlyDeparted {
			m.logger.Info("Caroline arrival suppressed - presence sensor bounce (arrival debounce)",
				zap.Duration("timeSinceDeparture", timeSinceDeparture),
				zap.Duration("debounceDuration", ArrivalDebounceDuration))
			return
		}

		// Set didOwnerJustReturnHome for garage automation
		m.setOwnerJustReturnedHome()

		// Check if anyone else was already home (Nick or Assistant)
		wasAnyoneHome := false
		if isNickHome, err := m.stateManager.GetBool("isNickHome"); err == nil && isNickHome {
			wasAnyoneHome = true
		}
		if isAssistantHere, err := m.stateManager.GetBool("isAssistantHere"); err == nil && isAssistantHere {
			wasAnyoneHome = true
		}

		if wasAnyoneHome {
			// Run announcement asynchronously to avoid deadlocks
			go m.announceArrivalDirect("Caroline", "Caroline is home", []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
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

// handleAssistantHereChange processes Assistant's presence state changes for TTS announcements
func (m *Manager) handleAssistantHereChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || oldState == nil {
		return
	}

	// Update shadow state inputs
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Check if Assistant just arrived (state changed to "on" from something else)
	if newState.State == "on" && oldState.State != "on" {
		m.logger.Debug("Assistant arrived, checking if should announce",
			zap.String("entity_id", entityID),
			zap.String("old_state", oldState.State),
			zap.String("new_state", newState.State))

		// Check arrival debounce to suppress presence sensor bounce (issue #922)
		m.timerMutex.Lock()
		timeSinceDeparture := m.clock.Now().Sub(m.assistantDepartureTime)
		recentlyDeparted := !m.assistantDepartureTime.IsZero() && timeSinceDeparture < ArrivalDebounceDuration
		m.timerMutex.Unlock()

		if recentlyDeparted {
			m.logger.Info("Assistant arrival suppressed - presence sensor bounce (arrival debounce)",
				zap.Duration("timeSinceDeparture", timeSinceDeparture),
				zap.Duration("debounceDuration", ArrivalDebounceDuration))
			return
		}

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
			go m.announceArrivalDirect("Assistant", "Assistant is here", []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
				"media_player.kids_bathroom",
				"media_player.soundbar",
				"media_player.office",
			})
		} else {
			m.logger.Debug("Nobody else was home, not announcing Assistant's arrival")
		}
	} else if newState.State != "on" && oldState.State == "on" {
		// Assistant left - record departure time for arrival debounce (issue #922)
		m.timerMutex.Lock()
		m.assistantDepartureTime = m.clock.Now()
		m.timerMutex.Unlock()
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

// entityIDToSpeakerName converts "media_player.front_room" to "Front Room".
func entityIDToSpeakerName(entityID string) string {
	name := strings.TrimPrefix(entityID, "media_player.")
	words := strings.Split(name, "_")
	for i, w := range words {
		if len(w) > 0 {
			runes := []rune(w)
			runes[0] = unicode.ToUpper(runes[0])
			words[i] = string(runes)
		}
	}
	return strings.Join(words, " ")
}

// speakerNameToEntityID converts "Front Room" to "media_player.front_room".
func speakerNameToEntityID(name string) string {
	return "media_player." + strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

// socoGroupsResponse matches the JSON returned by SoCo-CLI's /groups endpoint.
type socoGroupsResponse struct {
	ExitCode int    `json:"exit_code"`
	Result   string `json:"result"`
	ErrorMsg string `json:"error_msg"`
}

// getGroupCoordinator queries SoCo-CLI to find the group coordinator for a speaker.
// Returns the coordinator's entity ID if the speaker is in a group, or empty string if
// ungrouped or SoCo-CLI is unavailable. Errors are logged and result in graceful fallback.
func (m *Manager) getGroupCoordinator(speakerEntityID string) string {
	if m.socoCliURL == "" {
		return ""
	}

	speakerName := entityIDToSpeakerName(speakerEntityID)
	endpoint := fmt.Sprintf("%s/%s/groups", m.socoCliURL, url.PathEscape(speakerName))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		m.logger.Warn("Failed to query SoCo-CLI for speaker groups, using default speakers",
			zap.String("speaker", speakerName), zap.Error(err))
		return ""
	}
	defer resp.Body.Close()

	var socoResp socoGroupsResponse
	if err := json.NewDecoder(resp.Body).Decode(&socoResp); err != nil {
		m.logger.Warn("Failed to decode SoCo-CLI groups response", zap.Error(err))
		return ""
	}

	if socoResp.ExitCode != 0 {
		m.logger.Warn("SoCo-CLI groups query failed",
			zap.Int("exit_code", socoResp.ExitCode), zap.String("error", socoResp.ErrorMsg))
		return ""
	}

	// Parse result lines like "Front Room: Kitchen, Bedroom\nSoundbar:\n"
	// Each line is "Coordinator: member1, member2, ..."
	// A coordinator with no members is standalone.
	for _, line := range strings.Split(socoResp.Result, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		coordinator := strings.TrimSpace(parts[0])
		members := strings.TrimSpace(parts[1])
		if members == "" {
			// Standalone speaker, no group
			continue
		}

		// Check if our speaker is the coordinator or a member of this group
		if strings.EqualFold(coordinator, speakerName) {
			return speakerNameToEntityID(coordinator)
		}
		for _, member := range strings.Split(members, ",") {
			if strings.EqualFold(strings.TrimSpace(member), speakerName) {
				return speakerNameToEntityID(coordinator)
			}
		}
	}

	return ""
}

// announceArrivalDirect makes an arrival announcement via the shared notifier
// (caller has already checked if someone is home).
func (m *Manager) announceArrivalDirect(person, message string, mediaPlayers []string) {
	// Check if any target speaker is in a Sonos group; if so, send TTS to the
	// group coordinator only (sending to individual group members breaks playback).
	if coordinator := m.getGroupCoordinator(mediaPlayers[0]); coordinator != "" {
		m.logger.Info("TTS targeting Sonos group coordinator instead of default speakers",
			zap.String("coordinator", coordinator),
			zap.Strings("original_speakers", mediaPlayers))
		mediaPlayers = []string{coordinator}
	}

	m.logger.Info("Announcing arrival via notifier",
		zap.String("person", person),
		zap.String("message", message),
		zap.Strings("media_players", mediaPlayers))

	if err := m.notifier.Announce(m.ctx, message, notify.WithSpeakers(mediaPlayers)); err != nil {
		m.logger.Error("Failed to announce arrival",
			zap.String("person", person),
			zap.Error(err))
	}

	// Record in shadow state regardless (the notifier itself records read-only intent).
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
