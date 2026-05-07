package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/loadshedding"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Thermal Battery Hysteresis User-Story Integration Tests
//
// Validates the wide-band hysteresis behavior end-to-end through real state
// subscriptions: white→green dip widens setpoints (no HVAC counter-run),
// white recovery resumes the preheat band, yellow drop hard-deactivates.
//
// Reference incident: 2026-05-06 evening saw white→green→white oscillation
// trigger a 16-min AC counter-run on the primary suite zone immediately upon
// the original deactivation. With hysteresis, the green dip should now coast.
// ============================================================================

// setupThermalBatteryHysteresisTest creates a load-shedding manager wired into
// a real mock HA server with thermostats in heat_cool mode and presence flags set.
// Uses the outdoor-temp fallback path for thermal-battery direction (35°F → UP shift)
// because the integration MockHAServer does not support custom forecast responses.
func setupThermalBatteryHysteresisTest(t *testing.T) (*MockHAServer, *loadshedding.Manager, *state.Manager, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	// Seed HA state for thermal battery preconditions
	server.SetState("switch.most_of_house_thermostat_hold", "off", nil)
	server.SetState("switch.primary_suite_thermostat_hold", "off", nil)
	// Heat_cool mode with realistic owner setpoints (69/72, 3°F dead band)
	houseAttrs := map[string]interface{}{
		"target_temp_low":     69.0,
		"target_temp_high":    72.0,
		"current_temperature": 70.0,
		"hvac_action":         "idle",
	}
	suiteAttrs := map[string]interface{}{
		"target_temp_low":     69.0,
		"target_temp_high":    72.0,
		"current_temperature": 71.0,
		"hvac_action":         "idle",
	}
	server.SetState("climate.most_of_house_thermostat", "heat_cool", houseAttrs)
	server.SetState("climate.primary_suite_thermostat", "heat_cool", suiteAttrs)
	// Outdoor temp 35°F triggers UP direction via fallback path (no forecast configured)
	server.SetState("sensor.weather_station_temperature", "35.0", nil)

	require.NoError(t, manager.SyncFromHA())
	require.NoError(t, manager.SetBool("isAnyoneHome", true))
	require.NoError(t, manager.SetBool("isEveryoneAsleep", false))

	logger := testlogger.New()
	registry := shadowstate.NewSubscriptionRegistry()
	ls := loadshedding.NewManager(context.Background(), client, manager, logger, false, registry, nil)
	// Long enough to outlast the test, but bounded so we never hang.
	ls.SetThermalBatteryHysteresisDurationForTesting(30 * time.Second)
	require.NoError(t, ls.Start())

	cleanup := func() {
		ls.Stop()
		baseCleanup()
	}
	return server, ls, manager, cleanup
}

// climateSetpointForEntity returns the most recent climate.set_temperature call
// for the given entity, or nil if none. Used to read the current shifted setpoints.
func climateSetpointForEntity(calls []testutil.ServiceCall, entityID string) *testutil.ServiceCall {
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		if call.Domain != "climate" || call.Service != "set_temperature" {
			continue
		}
		if id, ok := call.ServiceData["entity_id"].(string); ok && id == entityID {
			return &call
		}
	}
	return nil
}

// TestScenario_ThermalBatteryHysteresis_GreenDipCoastsThenResumesOnWhite
// validates the user story: a transient white→green→white oscillation should
// NOT cause HVAC counter-runs, and preheat should resume cleanly on recovery.
func TestScenario_ThermalBatteryHysteresis_GreenDipCoastsThenResumesOnWhite(t *testing.T) {
	t.Parallel()
	server, ls, manager, cleanup := setupThermalBatteryHysteresisTest(t)
	defer cleanup()

	// GIVEN: Energy reaches white, thermal battery preheats to 70/73
	t.Log("GIVEN: Energy at white, thermal battery activates with UP shift")
	require.NoError(t, manager.SetString("currentEnergyLevel", "white"))

	require.Eventually(t, func() bool { return ls.IsThermalBatteryActive() },
		5*time.Second, 10*time.Millisecond, "Thermal battery should activate at white")

	// Verify preheat band applied (69/72 → 70/73 after first step)
	t.Log("Verifying preheat band 70/73 applied")
	require.Eventually(t, func() bool {
		call := climateSetpointForEntity(server.GetServiceCalls(), "climate.most_of_house_thermostat")
		if call == nil {
			return false
		}
		low, _ := call.ServiceData["target_temp_low"].(float64)
		high, _ := call.ServiceData["target_temp_high"].(float64)
		return low == 70.0 && high == 73.0
	}, 5*time.Second, 10*time.Millisecond, "Should apply preheat band 70/73")

	snapshot := server.ServiceCallCount()

	// WHEN: Energy dips white→green (transient)
	t.Log("WHEN: Energy drops to green (transient dip)")
	require.NoError(t, manager.SetString("currentEnergyLevel", "green"))

	// THEN: Thermal battery enters hysteresis — wide band 69/73, holds remain on
	t.Log("THEN: Wide band 69/73 set, thermal battery still considered active")
	require.Eventually(t, func() bool {
		call := climateSetpointForEntity(server.GetServiceCallsSince(snapshot), "climate.most_of_house_thermostat")
		if call == nil {
			return false
		}
		low, _ := call.ServiceData["target_temp_low"].(float64)
		high, _ := call.ServiceData["target_temp_high"].(float64)
		return low == 69.0 && high == 73.0
	}, 5*time.Second, 10*time.Millisecond, "Should widen band to 69/73 (saved low, shifted high)")

	assert.True(t, ls.IsThermalBatteryActive(),
		"INVARIANT: Thermal battery must remain ACTIVE during hysteresis")

	// Holds must NOT be disabled during hysteresis
	for _, call := range server.GetServiceCallsSince(snapshot) {
		if call.Domain == "switch" && call.Service == "turn_off" {
			if eids, ok := call.ServiceData["entity_id"].([]string); ok {
				for _, e := range eids {
					if e == "switch.most_of_house_thermostat_hold" || e == "switch.primary_suite_thermostat_hold" {
						t.Errorf("INVARIANT VIOLATED: Hold must remain enabled during hysteresis (got turn_off for %s)", e)
					}
				}
			}
		}
	}

	resumeSnapshot := server.ServiceCallCount()

	// WHEN: Energy returns to white during hysteresis
	t.Log("WHEN: Energy returns to white during hysteresis")
	require.NoError(t, manager.SetString("currentEnergyLevel", "white"))

	// THEN: Preheat band 70/73 is re-applied (no fresh capture, no notification re-fire)
	t.Log("THEN: Preheat band 70/73 resumes")
	require.Eventually(t, func() bool {
		call := climateSetpointForEntity(server.GetServiceCallsSince(resumeSnapshot), "climate.most_of_house_thermostat")
		if call == nil {
			return false
		}
		low, _ := call.ServiceData["target_temp_low"].(float64)
		high, _ := call.ServiceData["target_temp_high"].(float64)
		return low == 70.0 && high == 73.0
	}, 5*time.Second, 10*time.Millisecond, "Should resume preheat band 70/73 on white recovery")

	assert.True(t, ls.IsThermalBatteryActive(), "Should remain active after recovery")
	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.HysteresisActive,
		"Hysteresis should be cleared after white recovery")
}

// TestScenario_ThermalBatteryHysteresis_YellowDropHardDeactivates
// validates that a real load-shedding signal (yellow) during hysteresis still
// hard-deactivates the thermal battery and reverts setpoints.
func TestScenario_ThermalBatteryHysteresis_YellowDropHardDeactivates(t *testing.T) {
	t.Parallel()
	server, ls, manager, cleanup := setupThermalBatteryHysteresisTest(t)
	defer cleanup()

	// GIVEN: thermal battery active and in hysteresis after a green dip
	require.NoError(t, manager.SetString("currentEnergyLevel", "white"))
	require.Eventually(t, func() bool { return ls.IsThermalBatteryActive() },
		5*time.Second, 10*time.Millisecond)

	require.NoError(t, manager.SetString("currentEnergyLevel", "green"))
	require.Eventually(t, func() bool {
		return ls.GetShadowState().Outputs.ThermalBattery.HysteresisActive
	}, 5*time.Second, 10*time.Millisecond, "Hysteresis should engage on green")

	snapshot := server.ServiceCallCount()

	// WHEN: Energy drops to yellow (real load-shedding signal)
	t.Log("WHEN: Energy drops to yellow during hysteresis")
	require.NoError(t, manager.SetString("currentEnergyLevel", "yellow"))

	// THEN: Hard deactivation — setpoints reverted to original 69/72, hysteresis cleared
	t.Log("THEN: Setpoints reverted to original 69/72")
	require.Eventually(t, func() bool {
		call := climateSetpointForEntity(server.GetServiceCallsSince(snapshot), "climate.most_of_house_thermostat")
		if call == nil {
			return false
		}
		low, _ := call.ServiceData["target_temp_low"].(float64)
		high, _ := call.ServiceData["target_temp_high"].(float64)
		return low == 69.0 && high == 72.0
	}, 5*time.Second, 10*time.Millisecond, "Yellow drop should revert to original 69/72")

	require.Eventually(t, func() bool { return !ls.IsThermalBatteryActive() },
		5*time.Second, 10*time.Millisecond, "Yellow drop must hard-deactivate")

	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.HysteresisActive)
}
