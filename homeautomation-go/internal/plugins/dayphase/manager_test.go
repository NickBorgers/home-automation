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
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	assert.NotNil(t, manager)
	assert.Equal(t, mockClient, manager.haClient)
	assert.Equal(t, stateManager, manager.stateManager)
	assert.Equal(t, configLoader, manager.configLoader)
	assert.Equal(t, calculator, manager.calculator)
	assert.False(t, manager.readOnly)
}

func TestManagerStartStop(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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

	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, true, nil)

	// Update should succeed even in read-only mode (just won't write to HA)
	err = manager.updateSunEventAndDayPhase()
	assert.NoError(t, err)
}

func TestManagerPeriodicUpdate(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)

	// Test with different coordinates (San Francisco)
	calculator := dayphaselib.NewCalculator(37.7749, -122.4194, logger)

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
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

	// Should not panic if Stop is called before Start
	assert.NotPanics(t, func() {
		manager.Stop()
	})
}

func TestManagerReset(t *testing.T) {
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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
	// Test that shadow state outputs.NextTransitionTime and NextTransitionPhase are populated
	// This test catches the bug where UpdateNextTransition() was never called
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

	// Use a fixed reference time: January 15, 2024 at 10:00 AM
	// This makes the test deterministic regardless of when it runs
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.Local)
	mockClock := clock.NewMockClock(fixedTime)
	calculator.SetClock(mockClock)
	configLoader.SetClock(mockClock)

	manager := NewManager(mockClient, stateManager, configLoader, calculator, logger, false, nil)

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
	// Test that shadow state outputs for sun event and day phase are populated
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	configLoader := config.NewLoader("../../../configs", logger)
	calculator := dayphaselib.NewCalculator(32.85486, -97.50515, logger)

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
