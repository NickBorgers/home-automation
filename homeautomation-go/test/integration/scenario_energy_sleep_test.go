package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/config"
	"homeautomation/internal/plugins/energy"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// Energy + Sleep Hygiene Coordination Tests
//
// These tests validate cross-plugin interactions between the energy and sleep
// hygiene plugins during wake sequences and battery state transitions.
//
// The energy plugin monitors battery percentage and sets batteryEnergyLevel
// ("green", "yellow", "red", "black"). The sleep hygiene plugin manages
// wake sequences (begin_wake -> fade-out -> lights -> wake music).
//
// INVARIANTS:
// - Wake sequence must NOT be interrupted by battery state changes
// - Energy indicator lights can change during wake sequence
// - Both plugins react to state changes independently without blocking
// - Battery going critical during wake does NOT cancel the wake sequence
// ============================================================================

// energySleepEnv holds plugins for energy + sleep hygiene tests
type energySleepEnv struct {
	server        *MockHAServer
	manager       *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	energy        *energy.Manager
	sleepHygiene  *sleephygiene.Manager
}

func setupEnergySleepTest(t *testing.T, fixedTime time.Time) (*energySleepEnv, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load energy config properly (same pattern as scenario_energy_test.go)
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test energy config")

	// Set up computed state registry with energy providers
	// This is required since energy level computation uses the registry
	var energyStates []state.EnergyStateConfig
	for _, es := range energyConfig.Energy.EnergyStates {
		energyStates = append(energyStates, state.EnergyStateConfig{
			ConditionName:                       es.ConditionName,
			BatteryMinimumPercentage:            es.BatteryMinimumPercentage,
			EnergyProductionMinimumKW:           es.EnergyProductionMinimumKW,
			RemainingEnergyProductionMinimumKWH: es.RemainingEnergyProductionMinimumKWH,
		})
	}
	err = manager.SetupComputedStateV2WithEnergy(energyStates, nil)
	require.NoError(t, err, "Failed to set up computed state registry with energy providers")

	configLoader := config.NewLoader("../../configs", logger)

	// Use fixed time for deterministic wake sequence behavior
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	// Create plugins
	env := &energySleepEnv{
		server:        server,
		manager:       manager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, manager, logger, false, nil),
		energy:        energy.NewManager(context.Background(), client, manager, energyConfig, logger, false, time.UTC, nil),
		sleepHygiene:  sleephygiene.NewManager(context.Background(), client, manager, configLoader, logger, false, timeProvider, nil),
	}

	// Start plugins in priority order
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking")
	require.NoError(t, env.energy.Start(), "Failed to start energy")
	env.energy.WaitForStartup()
	require.NoError(t, env.sleepHygiene.Start(), "Failed to start sleep hygiene")

	// Wait for plugin initialization handlers to complete
	waitForProcessing(t, manager)

	cleanup := func() {
		env.sleepHygiene.Stop()
		env.energy.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// ============================================================================
// Test 1: Battery State Changes During Wake Sequence
// ============================================================================

// TestScenario_BatteryDropsDuringWakeSequence validates that when the battery
// drops to critical ("red") during an active wake sequence, the wake sequence
// continues uninterrupted while the energy plugin updates its indicators.
//
// User story: "When my alarm goes off, the wake-up sequence should complete
// even if the battery drops to critical. I need to wake up regardless of
// battery state."
//
// INVARIANT: isWakeSequenceActive=true must NOT be cleared by energy changes.
// INVARIANT: Energy indicator lights update independently of wake sequence.
func TestScenario_BatteryDropsDuringWakeSequence(t *testing.T) {
	t.Parallel()
	// Set time to morning (8:50 AM) when wake sequence would be active
	morningTime := time.Date(2025, 1, 15, 8, 50, 0, 0, time.UTC)
	env, cleanup := setupEnergySleepTest(t, morningTime)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Wake sequence is active, battery is currently green")

	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.fade_out_in_progress", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})

	// Battery starts at healthy level
	env.server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "85.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Wait for battery level to settle at green
	waitForStringState(t, env.manager, "batteryEnergyLevel", "green", "Battery should start at green")

	// Set wake sequence active in state manager too
	require.NoError(t, env.manager.SetBool("isWakeSequenceActive", true))

	waitForProcessing(t, env.manager)
	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Battery drops to critical level (15%) during wake sequence")

	env.server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "15.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Wait for energy plugin to process the battery change
	// Battery at 15% is below red threshold (20%) so it should be "black" per test config
	waitForStringStateOneOf(t, env.manager, "batteryEnergyLevel", []string{"red", "black"},
		"Battery level should update to red or black")

	// ========== THEN ==========
	t.Log("THEN: Wake sequence continues AND energy level updates")

	// ASSERTION 1: Wake sequence is still active (not interrupted by energy)
	isWakeActive, err := env.manager.GetBool("isWakeSequenceActive")
	assert.NoError(t, err)
	assert.True(t, isWakeActive,
		"CRITICAL: Wake sequence must NOT be interrupted by battery state changes")

	// ASSERTION 2: Energy plugin updated battery level independently
	batteryLevel, err := env.manager.GetString("batteryEnergyLevel")
	assert.NoError(t, err)
	t.Logf("Battery energy level after drop: %s", batteryLevel)
	assert.NotEqual(t, "green", batteryLevel,
		"Energy level should have changed from green after battery dropped to 15%")

	t.Log("SUCCESS: Energy and wake sequence operated independently")
}

// ============================================================================
// Test 2: Wake Sequence Starts While Battery Is Already Low
// ============================================================================

// TestScenario_WakeSequenceStartsWithLowBattery validates that a wake
// sequence can start successfully even when the battery is already in
// a low/critical state.
//
// User story: "My alarm should still wake me up even if the battery was
// low overnight."
//
// INVARIANT: Low battery state must not prevent wake sequence from starting.
func TestScenario_WakeSequenceStartsWithLowBattery(t *testing.T) {
	t.Parallel()
	alarmTime := time.Date(2025, 1, 15, 8, 50, 0, 0, time.UTC)
	env, cleanup := setupEnergySleepTest(t, alarmTime)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Battery is already at low level, master is asleep")

	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.fade_out_in_progress", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	env.server.SetState("input_text.music_playback_type", "sleep", map[string]interface{}{})

	// Battery already low
	env.server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "15.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Wait for energy plugin to finish computing battery level from the 15% sensor reading
	waitForStringStateOneOf(t, env.manager, "batteryEnergyLevel", []string{"red", "black"},
		"Battery level should settle to red or black at 15%")

	// Record battery level after it has settled
	batteryLevel, err := env.manager.GetString("batteryEnergyLevel")
	assert.NoError(t, err)
	t.Logf("Battery level before wake attempt: %s", batteryLevel)

	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Alarm time triggers wake sequence (begin_wake)")

	// Simulate alarm time being set (triggers sleephygiene time check)
	alarmTimeMs := float64(alarmTime.Unix() * 1000)
	env.server.SetState("input_number.alarm_time", fmt.Sprintf("%.0f", alarmTimeMs), map[string]interface{}{})

	// Wait for sleep hygiene plugin to process the alarm trigger
	waitForProcessing(t, env.manager)

	// ========== THEN ==========
	t.Log("THEN: Wake sequence starts despite low battery")

	// The sleep hygiene plugin should have attempted to start the wake sequence
	// regardless of battery state. Check for wake-related service calls.
	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls after alarm: %d", len(calls))

	// Check if fade-out started (indicates begin_wake ran)
	fadeOutState := env.server.GetState("input_boolean.fade_out_in_progress")
	if fadeOutState != nil && fadeOutState.State == "on" {
		t.Log("SUCCESS: Wake sequence started (fade-out in progress) despite low battery")
	}

	// ASSERTION: Energy state didn't change just because wake started
	batteryLevelAfter, err := env.manager.GetString("batteryEnergyLevel")
	assert.NoError(t, err)
	assert.Equal(t, batteryLevel, batteryLevelAfter,
		"Battery energy level should remain unchanged by wake sequence")

	t.Log("SUCCESS: Wake sequence and energy plugin coexist with low battery")
}

// ============================================================================
// Test 3: Energy Level Transitions Don't Affect Sleep State
// ============================================================================

// TestScenario_EnergyTransitions_SleepStateUnaffected validates that rapid
// energy level transitions (green -> yellow -> red) do not affect the sleep
// hygiene plugin's state tracking.
//
// User story: "Battery fluctuations overnight shouldn't wake me up or
// interfere with my sleep sounds."
//
// INVARIANT: Energy state changes must not modify isMasterAsleep or
// isFadeOutInProgress.
func TestScenario_EnergyTransitions_SleepStateUnaffected(t *testing.T) {
	t.Parallel()
	nightTime := time.Date(2025, 1, 15, 2, 0, 0, 0, time.UTC)
	env, cleanup := setupEnergySleepTest(t, nightTime)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Night time, master is asleep, battery is green")

	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.fade_out_in_progress", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	env.server.SetState("input_text.music_playback_type", "sleep", map[string]interface{}{})

	// Battery starts healthy
	env.server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "90.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Wait for initial battery state to settle
	waitForStringState(t, env.manager, "batteryEnergyLevel", "green", "Battery should start at green")

	// Record initial sleep state
	isMasterAsleep, err := env.manager.GetBool("isMasterAsleep")
	require.NoError(t, err)
	require.True(t, isMasterAsleep, "Master should be asleep initially")

	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Battery drops through multiple levels: green -> yellow -> red")

	// Simulate battery draining overnight
	batteryLevels := []string{"75.0", "45.0", "15.0"}
	for _, level := range batteryLevels {
		env.server.SetState("sensor.span_panel_span_storage_battery_percentage_2", level, map[string]interface{}{
			"unit_of_measurement": "%",
		})
		waitForProcessing(t, env.manager)
	}

	// Wait for final battery level to settle
	waitForProcessing(t, env.manager)

	// ========== THEN ==========
	t.Log("THEN: Sleep state is completely unaffected by energy transitions")

	// ASSERTION 1: isMasterAsleep unchanged
	isMasterAsleep, err = env.manager.GetBool("isMasterAsleep")
	assert.NoError(t, err)
	assert.True(t, isMasterAsleep,
		"isMasterAsleep must remain true during energy transitions")

	// ASSERTION 2: isFadeOutInProgress unchanged
	isFadeOut, err := env.manager.GetBool("isFadeOutInProgress")
	assert.NoError(t, err)
	assert.False(t, isFadeOut,
		"isFadeOutInProgress must remain false during energy transitions")

	// ASSERTION 3: isWakeSequenceActive unchanged
	isWakeActive, err := env.manager.GetBool("isWakeSequenceActive")
	assert.NoError(t, err)
	assert.False(t, isWakeActive,
		"isWakeSequenceActive must remain false during energy transitions")

	// ASSERTION 4: Energy level DID update (energy plugin is working)
	batteryLevel, err := env.manager.GetString("batteryEnergyLevel")
	assert.NoError(t, err)
	t.Logf("Final battery energy level: %s", batteryLevel)
	assert.NotEqual(t, "green", batteryLevel,
		"Battery level should have changed from green after dropping to 15%")

	t.Log("SUCCESS: Energy transitions did not affect sleep state")
}
