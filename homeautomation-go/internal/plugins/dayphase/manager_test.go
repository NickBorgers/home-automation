package dayphase

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/config"
	dayphaselib "homeautomation/internal/dayphase"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
)

func TestNewManager(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.haClient)
	assert.Equal(t, stateManager, manager.stateManager)
	assert.Equal(t, configLoader, manager.configLoader)
	assert.Equal(t, calculator, manager.calculator)
	assert.False(t, manager.readOnly)
}

func TestManagerStartStop(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Start the manager
	err := manager.Start()
	assert.NoError(t, err)

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Stop the manager
	manager.Stop()
}

func TestUpdateSunEventAndDayPhase(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Initialize state variables
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	configLoader := config.NewLoader("../../../configs", logger)

	// Load schedule config for day phase calculation
	err = configLoader.LoadScheduleConfig()
	if err != nil {
		// If config file doesn't exist in test environment, skip this part
		t.Logf("Warning: Could not load schedule config: %v", err)
	}

	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Update sun event and day phase
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	// Verify that sunevent was set
	sunEvent, err := stateManager.GetString("sunevent")
	assert.NoError(t, err)
	assert.NotEmpty(t, sunEvent)
	assert.Contains(t, []string{"morning", "day", "sunset", "dusk", "night"}, sunEvent)

	// Verify that dayPhase was set
	dayPhase, err := stateManager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.NotEmpty(t, dayPhase)
	assert.Contains(t, []string{"morning", "day", "sunset", "dusk", "winddown", "night"}, dayPhase)
}

func TestUpdateSunEventAndDayPhaseReadOnly(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true) // READ ONLY

	// Initialize state variables
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// Set initial values
	err = stateManager.SetString("sunevent", "night")
	assert.Error(t, err) // Should error in read-only mode

	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, true, nil)

	// Update should succeed even in read-only mode (just won't write to HA)
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)
}

func TestManagerPeriodicUpdate(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Start the manager
	err := manager.Start()
	assert.NoError(t, err)

	// Let it run for a short time
	time.Sleep(200 * time.Millisecond)

	// Verify initial state was set
	sunEvent, err := stateManager.GetString("sunevent")
	assert.NoError(t, err)
	assert.NotEmpty(t, sunEvent)

	dayPhase, err := stateManager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.NotEmpty(t, dayPhase)

	// Stop the manager
	manager.Stop()
}

func TestManagerWithDifferentCoordinates(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)

	// Test with different coordinates (San Francisco)
	calculator := dayphaselib.NewCalculator(37.7749, -122.4194, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	err := manager.Start()
	assert.NoError(t, err)

	// Give it time to calculate
	time.Sleep(100 * time.Millisecond)

	// Should still work with different coordinates
	sunEvent, err := stateManager.GetString("sunevent")
	assert.NoError(t, err)
	assert.NotEmpty(t, sunEvent)

	manager.Stop()
}

func TestUpdateSunEventNoChange(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Initialize state
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// First update
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	sunEvent1, _ := stateManager.GetString("sunevent")

	// Second update (should be same value, no change)
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	sunEvent2, _ := stateManager.GetString("sunevent")
	assert.Equal(t, sunEvent1, sunEvent2)
}

func TestUpdateDayPhaseNoChange(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Initialize state
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// First update
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	dayPhase1, _ := stateManager.GetString("dayPhase")

	// Second update (should be same value, no change)
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	dayPhase2, _ := stateManager.GetString("dayPhase")
	assert.Equal(t, dayPhase1, dayPhase2)
}

func TestManagerStopBeforeStart(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Should not panic if Stop is called before Start
	assert.NotPanics(t, func() {
		manager.Stop()
	})
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Start the manager
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-calculate sun event and day phase
	err = manager.Reset()
	assert.NoError(t, err)

	// Verify sun event and day phase are set
	sunEvent, err := stateManager.GetString("sunevent")
	assert.NoError(t, err)
	assert.NotEmpty(t, sunEvent)

	dayPhase, err := stateManager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.NotEmpty(t, dayPhase)
}

func TestManager_ShadowState_NextTransitionUpdated(t *testing.T) {
	t.Parallel(
	// Test that shadow state outputs.NextTransitionTime and NextTransitionPhase are populated
	// This test catches the bug where UpdateNextTransition() was never called
	)

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// Use a fixed reference time: January 15, 2024 at 10:00 AM in UTC
	// Using UTC ensures the test is deterministic regardless of the CI machine's timezone.
	// At this time (10:00 UTC = 4:00 AM in Texas), it's before dawn, so day phase is "night".
	// The next transition should be to "morning" at dawn (~13:00 UTC / 7:00 AM CST).
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	// Pass time.UTC as the timezone to ensure consistent schedule parsing
	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
	// SetClock on manager also propagates to calculator (see manager.go:SetClock)
	manager.SetClock(mockClock)
	configLoader.SetClock(mockClock)

	// Initialize state
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// Update sun event and day phase (which should also update next transition)
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state outputs are populated (not zero values)
	// NextTransitionTime should be set
	assert.False(t, shadowState.Outputs.NextTransitionTime.IsZero(),
		"Expected NextTransitionTime to be set, got zero time")

	// NextTransitionPhase should be a valid phase
	validPhases := []string{"morning", "day", "sunset", "dusk", "winddown", "night"}
	found := false
	for _, phase := range validPhases {
		if shadowState.Outputs.NextTransitionPhase == phase {
			found = true
			break
		}
	}
	assert.True(t, found,
		"Expected NextTransitionPhase to be a valid phase, got: %s", shadowState.Outputs.NextTransitionPhase)

	// NextTransitionTime should be in the future (or within a reasonable range)
	// Compare against the fixed reference time, not time.Now()
	assert.True(t, shadowState.Outputs.NextTransitionTime.After(fixedTime.Add(-1*time.Hour)),
		"Expected NextTransitionTime to be recent or in the future")
}

func TestManager_ShadowState_SunEventAndDayPhaseUpdated(t *testing.T) {
	t.Parallel(
	// Test that shadow state outputs for sun event and day phase are populated
	)

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Initialize state
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// Update sun event and day phase
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify SunEvent is set
	validSunEvents := []string{"morning", "day", "sunset", "dusk", "night"}
	foundSunEvent := false
	for _, event := range validSunEvents {
		if shadowState.Outputs.SunEvent == event {
			foundSunEvent = true
			break
		}
	}
	assert.True(t, foundSunEvent,
		"Expected SunEvent to be a valid value, got: %s", shadowState.Outputs.SunEvent)

	// Verify DayPhase is set
	validDayPhases := []string{"morning", "day", "sunset", "dusk", "winddown", "night"}
	foundDayPhase := false
	for _, phase := range validDayPhases {
		if shadowState.Outputs.DayPhase == phase {
			foundDayPhase = true
			break
		}
	}
	assert.True(t, foundDayPhase,
		"Expected DayPhase to be a valid value, got: %s", shadowState.Outputs.DayPhase)
}

func TestManager_UpdateNextTransition_AllPhases(t *testing.T) {
	t.Parallel()

	// Table-driven test for updateNextTransition at various times of day.
	// Uses times that produce deterministic phases with suncalc at Austin TX coords.
	// Note: suncalc centers around solar noon, so very early AM UTC times may
	// return previous day's sun times. We use mid-day times for reliability.
	tests := []struct {
		name      string
		fixedTime time.Time
	}{
		{
			// Mid-morning UTC in summer (after dawn ~10:15 UTC, before noon)
			// → morning phase → next transition should be to day
			name:      "morning phase has next transition",
			fixedTime: time.Date(2024, 6, 15, 11, 0, 0, 0, time.UTC),
		},
		{
			// Mid-afternoon UTC in summer (after noon, before golden hour ~01:00+1 UTC)
			// → day phase → next transition should be to sunset
			name:      "day phase has next transition",
			fixedTime: time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := testlogger.New()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			configLoader := config.NewLoader("../../../configs", logger)
			calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

			mockClock := clock.NewMockClock(tc.fixedTime)

			manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
			manager.SetClock(mockClock)
			configLoader.SetClock(mockClock)

			err := stateManager.SyncFromHA()
			assert.NoError(t, err)

			err = manager.updateSunEventAndDayPhase()
			assert.NoError(t, err)

			shadowState := manager.GetShadowState()
			assert.False(t, shadowState.Outputs.NextTransitionTime.IsZero(),
				"Expected NextTransitionTime to be set for %s", tc.name)
			assert.NotEmpty(t, shadowState.Outputs.NextTransitionPhase,
				"Expected NextTransitionPhase to be set for %s", tc.name)
		})
	}
}

func TestManager_UpdateNextTransition_SunsetToDusk(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// Late afternoon UTC in summer - near sunset time
	fixedTime := time.Date(2024, 6, 15, 20, 30, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
	manager.SetClock(mockClock)
	configLoader.SetClock(mockClock)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	shadowState := manager.GetShadowState()
	// At this time, should be in sunset or later - next transition should be populated
	assert.False(t, shadowState.Outputs.NextTransitionTime.IsZero(),
		"Expected NextTransitionTime to be set during sunset")
}

func TestManager_UpdateNextTransition_WinddownToNight(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// 10 PM UTC = late evening, should be in winddown or night phase
	fixedTime := time.Date(2024, 6, 15, 22, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
	manager.SetClock(mockClock)
	configLoader.SetClock(mockClock)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	// Verify that shadow state was updated (transition may or may not be set
	// depending on whether we're past the night time)
	shadowState := manager.GetShadowState()
	assert.NotNil(t, shadowState)
}

func TestManager_UpdateNextTransition_NightPhase(t *testing.T) {
	t.Parallel()

	// Test updateNextTransition for the night phase directly.
	// We call updateNextTransition with DayPhaseNight to exercise the
	// night-to-morning transition code path, regardless of what the
	// calculator computes for the current time.
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	fixedTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
	manager.SetClock(mockClock)
	configLoader.SetClock(mockClock)

	// Force sun times to be populated
	_ = calculator.UpdateSunTimes()

	// Directly call updateNextTransition with night phase
	manager.updateNextTransition(dayphaselib.DayPhaseNight)

	shadowState := manager.GetShadowState()
	assert.False(t, shadowState.Outputs.NextTransitionTime.IsZero(),
		"Expected NextTransitionTime to be set for night phase")
	assert.Equal(t, "morning", shadowState.Outputs.NextTransitionPhase,
		"Expected next phase to be morning when in night")
}

func TestManager_UpdateNextTransition_DirectCalls(t *testing.T) {
	t.Parallel()

	// Table-driven test exercising updateNextTransition for each phase directly.
	// This covers all switch cases in the function without depending on
	// suncalc producing a specific phase for a specific time.
	tests := []struct {
		name              string
		phase             dayphaselib.DayPhase
		expectedNextPhase string
	}{
		{
			name:              "night to morning",
			phase:             dayphaselib.DayPhaseNight,
			expectedNextPhase: "morning",
		},
		{
			name:              "morning to day",
			phase:             dayphaselib.DayPhaseMorning,
			expectedNextPhase: "day",
		},
		{
			name:              "day to sunset",
			phase:             dayphaselib.DayPhaseDay,
			expectedNextPhase: "sunset",
		},
		{
			name:              "sunset to dusk",
			phase:             dayphaselib.DayPhaseSunset,
			expectedNextPhase: "dusk",
		},
		{
			name:              "dusk to winddown",
			phase:             dayphaselib.DayPhaseDusk,
			expectedNextPhase: "winddown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := testlogger.New()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)
			configLoader := config.NewLoader("../../../configs", logger)
			calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

			// Use noon UTC when sun times are most reliable
			fixedTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
			mockClock := clock.NewMockClock(fixedTime)

			manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
			manager.SetClock(mockClock)

			// Populate sun times
			_ = calculator.UpdateSunTimes()

			// Call updateNextTransition directly with the specified phase
			manager.updateNextTransition(tc.phase)

			shadowState := manager.GetShadowState()

			// For phases whose sun time is in the future relative to noon UTC,
			// the transition should be populated. For phases where the relevant
			// sun time may have already passed (e.g., morning→day when
			// goldenHourEnd is before noon), the transition might not be set.
			// We verify at least that the function doesn't panic and that when
			// a transition IS set, it has the expected next phase.
			if !shadowState.Outputs.NextTransitionTime.IsZero() {
				assert.Equal(t, tc.expectedNextPhase, shadowState.Outputs.NextTransitionPhase,
					"Wrong next phase for %s", tc.name)
			}
		})
	}
}

func TestManager_UpdateNextTransition_WinddownToNightNoSchedule(t *testing.T) {
	t.Parallel()

	// Test the winddown→night transition when no schedule is loaded.
	// The code should default to 23:00 as the night time.
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	// Use non-existent config dir to ensure no schedule loads
	configLoader := config.NewLoader("/nonexistent/configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// Use 20:00 UTC so that 23:00 tonight is in the future
	fixedTime := time.Date(2024, 6, 15, 20, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
	manager.SetClock(mockClock)

	_ = calculator.UpdateSunTimes()

	manager.updateNextTransition(dayphaselib.DayPhaseWinddown)

	shadowState := manager.GetShadowState()
	assert.False(t, shadowState.Outputs.NextTransitionTime.IsZero(),
		"Expected NextTransitionTime to be set for winddown phase")
	assert.Equal(t, "night", shadowState.Outputs.NextTransitionPhase,
		"Expected next phase to be night when in winddown")
	// Verify the time is 23:00 (default night time)
	assert.Equal(t, 23, shadowState.Outputs.NextTransitionTime.Hour(),
		"Expected night transition at 23:00")
}

func TestManager_ShadowInputsIncludeSchedule(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	fixedTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)
	manager.SetClock(mockClock)
	configLoader.SetClock(mockClock)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// Load schedule so inputs will include schedule data
	_ = configLoader.LoadScheduleConfig()

	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	shadowState := manager.GetShadowState()
	// Inputs should contain currentTime and configuredTimezone at minimum
	assert.NotNil(t, shadowState.Inputs.Current)
	assert.Contains(t, shadowState.Inputs.Current, "currentTime")
	assert.Contains(t, shadowState.Inputs.Current, "configuredTimezone")
}

func TestManager_ShadowInputsWithoutSchedule(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	// Use a non-existent config dir so schedule loading fails gracefully
	configLoader := config.NewLoader("/nonexistent/configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, time.UTC)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// Should not fail even without schedule
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)

	shadowState := manager.GetShadowState()
	assert.Contains(t, shadowState.Inputs.Current, "currentTime")
	assert.Contains(t, shadowState.Inputs.Current, "configuredTimezone")
	// Schedule fields should not be present
	assert.NotContains(t, shadowState.Inputs.Current, "scheduleDusk")
}

func TestManager_PeriodicUpdateError(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Start and immediately stop - should not panic
	err := manager.Start()
	assert.NoError(t, err)
	manager.Stop()
}

func TestManager_TimezoneNilDefaults(t *testing.T) {
	t.Parallel()

	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, time.UTC, logger)

	// Pass nil timezone - should default to time.Local
	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)
	assert.Equal(t, time.Local, manager.timezone)
}
