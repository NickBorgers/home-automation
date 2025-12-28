package dayphase

import (
	"fmt"
	"time"

	"homeautomation/internal/config"
	dayphaselib "homeautomation/internal/dayphase"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Manager handles day phase and sun event calculation
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	configLoader *config.Loader
	calculator   *dayphaselib.Calculator
	logger       *zap.Logger
	readOnly     bool
	timezone     *time.Location

	// Control channels
	stopChan    chan struct{}
	stoppedChan chan struct{}
	sunStopChan chan struct{}

	// Lifecycle tracking
	started bool

	// Subscriptions for cleanup
	subscriptions []state.Subscription

	// Shadow state tracking
	shadowTracker *shadowstate.DayPhaseTracker
}

// NewManager creates a new Day Phase manager
// If timezone is nil, it defaults to time.Local
func NewManager(
	haClient ha.HAClient,
	stateManager *state.Manager,
	configLoader *config.Loader,
	calculator *dayphaselib.Calculator,
	logger *zap.Logger,
	readOnly bool,
	timezone *time.Location,
) *Manager {
	if timezone == nil {
		timezone = time.Local
	}
	return &Manager{
		haClient:      haClient,
		stateManager:  stateManager,
		configLoader:  configLoader,
		calculator:    calculator,
		logger:        logger.Named("dayphase"),
		readOnly:      readOnly,
		timezone:      timezone,
		stopChan:      make(chan struct{}),
		stoppedChan:   make(chan struct{}),
		subscriptions: make([]state.Subscription, 0),
		shadowTracker: shadowstate.NewDayPhaseTracker(),
	}
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.DayPhaseShadowState {
	return m.shadowTracker.GetState()
}

// Start begins monitoring and updating day phase variables
func (m *Manager) Start() error {
	m.logger.Info("Starting Day Phase Manager")

	// Start periodic sun time updates (every 6 hours)
	m.sunStopChan = m.calculator.StartPeriodicUpdate()

	// Do initial calculation and update
	if err := m.updateSunEventAndDayPhase(); err != nil {
		return fmt.Errorf("failed to do initial day phase update: %w", err)
	}

	// Start periodic update goroutine (every 5 minutes)
	go m.periodicUpdate()

	// Mark as started
	m.started = true

	m.logger.Info("Day Phase Manager started successfully")
	return nil
}

// Stop stops the Day Phase Manager and cleans up subscriptions
func (m *Manager) Stop() {
	m.logger.Info("Stopping Day Phase Manager")

	// Only stop if started
	if !m.started {
		m.logger.Info("Day Phase Manager was not started, nothing to stop")
		return
	}

	// Stop periodic update
	close(m.stopChan)

	// Stop sun time updates
	if m.sunStopChan != nil {
		close(m.sunStopChan)
	}

	// Wait for goroutine to finish
	<-m.stoppedChan

	// Unsubscribe from all subscriptions
	for _, sub := range m.subscriptions {
		sub.Unsubscribe()
	}
	m.subscriptions = nil

	m.logger.Info("Day Phase Manager stopped")
}

// periodicUpdate runs every 5 minutes to update sun event and day phase
func (m *Manager) periodicUpdate() {
	defer close(m.stoppedChan)

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := m.updateSunEventAndDayPhase(); err != nil {
				m.logger.Error("Failed to update sun event and day phase", zap.Error(err))
			}

		case <-m.stopChan:
			m.logger.Info("Stopping periodic day phase updates")
			return
		}
	}
}

// updateSunEventAndDayPhase calculates and updates sunevent and dayPhase
func (m *Manager) updateSunEventAndDayPhase() error {
	// Update shadow state inputs with current calculation context
	m.updateShadowInputs()

	// Get current sun event
	sunEvent := m.calculator.GetSunEvent()
	sunEventStr := string(sunEvent)

	// Get current sunevent value from state
	currentSunEvent, err := m.stateManager.GetString("sunevent")
	if err != nil {
		m.logger.Warn("Failed to get current sunevent", zap.Error(err))
		currentSunEvent = ""
	}

	// Update sunevent if it changed
	if currentSunEvent != sunEventStr {
		m.logger.Info("Sun event changed",
			zap.String("old", currentSunEvent),
			zap.String("new", sunEventStr))

		if !m.readOnly {
			if err := m.stateManager.SetString("sunevent", sunEventStr); err != nil {
				return fmt.Errorf("failed to update sunevent: %w", err)
			}
		} else {
			m.logger.Info("READ-ONLY mode: Would update sunevent",
				zap.String("value", sunEventStr))
		}

		// Update shadow state
		m.shadowTracker.UpdateSunEvent(sunEventStr)
	}

	// Calculate day phase based on schedule (using configured timezone)
	schedule, err := m.configLoader.GetTodaysScheduleInTimezone(m.timezone)
	if err != nil {
		m.logger.Warn("Failed to get schedule, using defaults", zap.Error(err))
		schedule = nil
	}

	dayPhase := m.calculator.CalculateDayPhase(schedule)
	dayPhaseStr := string(dayPhase)

	// Get current dayPhase value from state
	currentDayPhase, err := m.stateManager.GetString("dayPhase")
	if err != nil {
		m.logger.Warn("Failed to get current dayPhase", zap.Error(err))
		currentDayPhase = ""
	}

	// Update dayPhase if it changed
	if currentDayPhase != dayPhaseStr {
		m.logger.Info("Day phase changed",
			zap.String("old", currentDayPhase),
			zap.String("new", dayPhaseStr),
			zap.String("sun_event", sunEventStr))

		if !m.readOnly {
			if err := m.stateManager.SetString("dayPhase", dayPhaseStr); err != nil {
				return fmt.Errorf("failed to update dayPhase: %w", err)
			}
		} else {
			m.logger.Info("READ-ONLY mode: Would update dayPhase",
				zap.String("value", dayPhaseStr))
		}

		// Update shadow state
		m.shadowTracker.UpdateDayPhase(dayPhaseStr)
	}

	// Always update next transition (time changes even when phase doesn't)
	m.updateNextTransition(dayPhase)

	return nil
}

// Reset re-calculates and updates sun event and day phase
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Day Phase - re-calculating sun event and day phase")

	if err := m.updateSunEventAndDayPhase(); err != nil {
		return fmt.Errorf("failed to reset day phase: %w", err)
	}

	m.logger.Info("Successfully reset Day Phase")
	return nil
}

// updateShadowInputs captures the current input values used for day phase calculation
func (m *Manager) updateShadowInputs() {
	inputs := make(map[string]interface{})

	// Capture current time (the primary input for day phase calculation)
	inputs["currentTime"] = time.Now().Format(time.RFC3339)

	// Capture sun times from calculator if available
	// Note: range over nil map is safe in Go (no iterations)
	for name, t := range m.calculator.GetSunTimes() {
		inputs[name] = t.Format(time.RFC3339)
	}

	// Get current schedule info if available (using configured timezone)
	schedule, err := m.configLoader.GetTodaysScheduleInTimezone(m.timezone)
	if err == nil && schedule != nil {
		inputs["scheduleWake"] = schedule.Wake.Format(time.RFC3339)
		inputs["scheduleBeginWake"] = schedule.BeginWake.Format(time.RFC3339)
		inputs["scheduleWinddown"] = schedule.Winddown.Format(time.RFC3339)
		inputs["scheduleNight"] = schedule.Night.Format(time.RFC3339)
	}

	m.shadowTracker.UpdateCurrentInputs(inputs)
}

// updateNextTransition calculates and updates the next phase transition in shadow state
func (m *Manager) updateNextTransition(currentPhase dayphaselib.DayPhase) {
	now := time.Now()
	sunTimes := m.calculator.GetSunTimes()

	var nextTime time.Time
	var nextPhase string

	// Determine next transition based on current phase
	// Transition order: night → morning → day → sunset → dusk → winddown → night
	switch currentPhase {
	case dayphaselib.DayPhaseNight:
		// Next: morning at dawn
		if t, ok := sunTimes["dawn"]; ok && t.After(now) {
			nextTime = t
			nextPhase = string(dayphaselib.DayPhaseMorning)
		} else {
			// Dawn is tomorrow, use approximate time (in configured timezone)
			tomorrow := now.AddDate(0, 0, 1)
			nextTime = time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 6, 0, 0, 0, m.timezone)
			nextPhase = string(dayphaselib.DayPhaseMorning)
		}

	case dayphaselib.DayPhaseMorning:
		// Next: day at goldenHourEnd
		if t, ok := sunTimes["goldenHourEnd"]; ok && t.After(now) {
			nextTime = t
			nextPhase = string(dayphaselib.DayPhaseDay)
		}

	case dayphaselib.DayPhaseDay:
		// Next: sunset at goldenHour
		if t, ok := sunTimes["goldenHour"]; ok && t.After(now) {
			nextTime = t
			nextPhase = string(dayphaselib.DayPhaseSunset)
		}

	case dayphaselib.DayPhaseSunset:
		// Next: dusk at dusk
		if t, ok := sunTimes["dusk"]; ok && t.After(now) {
			nextTime = t
			nextPhase = string(dayphaselib.DayPhaseDusk)
		}

	case dayphaselib.DayPhaseDusk:
		// Next: winddown (or night) at astronomical night
		if t, ok := sunTimes["night"]; ok && t.After(now) {
			nextTime = t
			nextPhase = string(dayphaselib.DayPhaseWinddown)
		}

	case dayphaselib.DayPhaseWinddown:
		// Next: night at scheduled night time (using configured timezone)
		schedule, err := m.configLoader.GetTodaysScheduleInTimezone(m.timezone)
		if err == nil && schedule != nil && schedule.Night.After(now) {
			nextTime = schedule.Night
			nextPhase = string(dayphaselib.DayPhaseNight)
		} else {
			// Default to 23:00 if no schedule
			nightTime := time.Date(now.Year(), now.Month(), now.Day(), 23, 0, 0, 0, m.timezone)
			if nightTime.After(now) {
				nextTime = nightTime
				nextPhase = string(dayphaselib.DayPhaseNight)
			}
		}
	}

	// Only update if we calculated a valid next transition
	if !nextTime.IsZero() && nextPhase != "" {
		m.shadowTracker.UpdateNextTransition(nextTime, nextPhase)
		m.logger.Debug("Updated next transition",
			zap.Time("next_time", nextTime),
			zap.String("next_phase", nextPhase))
	}
}
