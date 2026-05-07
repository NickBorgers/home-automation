package loadshedding

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupThermalBatteryEnv creates a test environment with thermostat hold switches
// and climate entities initialized in cooling mode.
func setupThermalBatteryEnv(t *testing.T) *testutil.Env {
	t.Helper()
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "off", nil)
	env.MockHA.SetState(thermostatHoldSuite, "off", nil)

	// Set climate entities to cooling mode with realistic setpoints
	env.MockHA.SetState(climateHouse, "cool", map[string]interface{}{
		"temperature":         72.0,
		"current_temperature": 74.0,
		"hvac_action":         "cooling",
	})
	env.MockHA.SetState(climateSuite, "cool", map[string]interface{}{
		"temperature":         71.0,
		"current_temperature": 73.0,
		"hvac_action":         "cooling",
	})

	// Set presence/sleep states: someone is home and awake
	err := env.StateMgr.SetBool("isAnyoneHome", true)
	require.NoError(t, err)
	err = env.StateMgr.SetBool("isEveryoneAsleep", false)
	require.NoError(t, err)

	err = env.StateMgr.SyncFromHA()
	assert.NoError(t, err)
	return env
}

func TestThermalBattery_ActivatesOnWhiteEnergyLevel(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	// Set energy state to white
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// Verify thermal battery is active
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should be active at white energy level")

	// Verify thermostat hold was enabled before temperature changes
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	holdCalls := 0
	climateCalls := 0
	holdCallIndex := -1
	for i, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				for _, e := range entities {
					if e == thermostatHoldHouse || e == thermostatHoldSuite {
						holdCalls++
						holdCallIndex = i
						break
					}
				}
			}
		}
		if call.Domain == "climate" && call.Service == "set_temperature" {
			climateCalls++
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			// First step: only 1°F shift (not full 2°F)
			switch entityID {
			case climateHouse:
				assert.Equal(t, 71.0, temp, "House thermostat should be shifted from 72 to 71 (first step)")
			case climateSuite:
				assert.Equal(t, 70.0, temp, "Suite thermostat should be shifted from 71 to 70 (first step)")
			}

			// Hold must be enabled before any temperature shift
			assert.Greater(t, i, holdCallIndex, "Thermostat hold should be enabled before setting temperature")
		}
	}
	assert.Equal(t, 1, holdCalls, "Should have made 1 switch.turn_on call for thermostat holds")
	assert.Equal(t, 2, climateCalls, "Should have made 2 climate.set_temperature calls")

	// Verify shadow state shows first step
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
	assert.Equal(t, 1.0, shadow.Outputs.ThermalBattery.OffsetApplied)
	assert.Equal(t, 1, shadow.Outputs.ThermalBattery.StepsCompleted)
	assert.Equal(t, 2, shadow.Outputs.ThermalBattery.TotalSteps)
	assert.True(t, shadow.Outputs.ThermalBattery.Stepping, "Should be stepping (more steps remain)")
	assert.Len(t, shadow.Outputs.ThermalBattery.SavedSetpoints, 2)
}

func TestThermalBattery_GreenDipEntersHysteresis_CoolMode(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour) // Avoid rate limiting
	// Long hysteresis so the timer doesn't fire during the test
	ls.SetThermalBatteryHysteresisDurationForTesting(1 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	// Drop to green — should ENTER HYSTERESIS, not deactivate
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	// Thermal battery is still considered active during hysteresis
	assert.True(t, ls.IsThermalBatteryActive(),
		"Thermal battery should remain active in hysteresis after green dip")

	// In cool mode (single-stage), hysteresis reverts the setpoint to the saved value
	// so the AC stops. Hold MUST remain enabled (no turn_off call yet).
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	tempReverts := 0
	holdOffCount := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			tempReverts++
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)
			switch entityID {
			case climateHouse:
				assert.Equal(t, 72.0, temp, "House should revert to saved 72")
			case climateSuite:
				assert.Equal(t, 71.0, temp, "Suite should revert to saved 71")
			}
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				for _, e := range entities {
					if e == thermostatHoldHouse || e == thermostatHoldSuite {
						holdOffCount++
						break // one call counts once, not per-entity
					}
				}
			}
		}
	}
	assert.Equal(t, 2, tempReverts, "Should revert single-stage setpoints to saved values during hysteresis")
	assert.Equal(t, 0, holdOffCount, "Hold MUST remain enabled during hysteresis")

	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active, "Active flag stays true during hysteresis")
	assert.True(t, shadow.Outputs.ThermalBattery.HysteresisActive, "Shadow shows hysteresis active")
	assert.False(t, shadow.Outputs.ThermalBattery.HysteresisExpiresAt.IsZero(), "ExpiresAt is set")
}

func TestThermalBattery_GreenDipEntersHysteresis_HeatCoolMode_WideBand(t *testing.T) {
	t.Parallel()
	// Heat_cool mode is where wide-band hysteresis matters most.
	// Saved 69/72, preheat shifts UP to 70/73. Wide band on green dip: 69/73
	// (low reverts to saved, high stays at shifted) — neither heating nor cooling
	// engages because indoor temp sits inside that wider band.
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryHysteresisDurationForTesting(1 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive(), "Should stay active during hysteresis")

	calls := env.MockHA.GetServiceCallsSince(snapshot)
	wideCalls := 0
	holdOffCount := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			wideCalls++
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)
			assert.Equal(t, 69.0, low, "Wide low = saved low (69) — heat off")
			assert.Equal(t, 73.0, high, "Wide high = shifted high (73) — cool off")
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				for _, e := range entities {
					if e == thermostatHoldHouse || e == thermostatHoldSuite {
						holdOffCount++
						break // one call counts once, not per-entity
					}
				}
			}
		}
	}
	assert.Equal(t, 2, wideCalls, "Should widen both thermostats")
	assert.Equal(t, 0, holdOffCount, "Hold MUST remain enabled during hysteresis")
}

func TestThermalBattery_HysteresisExpiry_ReleasesHoldsWithoutRevert(t *testing.T) {
	t.Parallel()
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	// Very short hysteresis so the timer fires during the test
	ls.SetThermalBatteryHysteresisDurationForTesting(50 * time.Millisecond)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Enter hysteresis
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	snapshot := env.MockHA.ServiceCallCount()

	// Wait for hysteresis to expire
	require.Eventually(t, func() bool {
		return !ls.IsThermalBatteryActive()
	}, 2*time.Second, 10*time.Millisecond, "Thermal battery should deactivate after hysteresis expiry")

	// On expiry: hold MUST be disabled, but no climate.set_temperature calls
	// (we trust the schedule to take over).
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	holdOffCount := 0
	revertCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				for _, e := range entities {
					if e == thermostatHoldHouse || e == thermostatHoldSuite {
						holdOffCount++
						break // one call counts once, not per-entity
					}
				}
			}
		}
	}
	assert.Equal(t, 0, revertCalls, "Expiry must NOT explicitly revert setpoints — schedule resumes via hold off")
	assert.Equal(t, 1, holdOffCount, "Expiry must disable thermostat holds")

	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Active)
	assert.False(t, shadow.Outputs.ThermalBattery.HysteresisActive)
}

func TestThermalBattery_WhiteRecoveryDuringHysteresis_ResumesPreheat(t *testing.T) {
	t.Parallel()
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryHysteresisDurationForTesting(1 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate, then dip to green to enter hysteresis
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	snapshot := env.MockHA.ServiceCallCount()

	// White returns — should resume preheat band (re-apply step 1 → 70/73)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive(), "Should remain active after recovery")

	calls := env.MockHA.GetServiceCallsSince(snapshot)
	resumeCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			resumeCalls++
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)
			assert.Equal(t, 70.0, low, "Should re-apply preheat step (low=70)")
			assert.Equal(t, 73.0, high, "Should re-apply preheat step (high=73)")
		}
	}
	assert.Equal(t, 2, resumeCalls, "Should re-apply preheat band to both thermostats")

	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
	assert.False(t, shadow.Outputs.ThermalBattery.HysteresisActive,
		"Hysteresis should be cleared after recovery")
}

func TestThermalBattery_YellowDropDuringHysteresis_ImmediateRevert(t *testing.T) {
	t.Parallel()
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	ls.SetThermalBatteryHysteresisDurationForTesting(1 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Enter hysteresis
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	// Drop to yellow during hysteresis — should hard-deactivate (cancel timer + revert)
	err = env.StateMgr.SetString("currentEnergyLevel", "yellow")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(),
		"Yellow drop must hard-deactivate even during hysteresis")

	calls := env.MockHA.GetServiceCallsSince(snapshot)
	revertCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)
			assert.Equal(t, 69.0, low, "Should revert to saved low (69)")
			assert.Equal(t, 72.0, high, "Should revert to saved high (72)")
		}
	}
	assert.Equal(t, 2, revertCalls, "Should revert both thermostats on yellow drop")

	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Active)
	assert.False(t, shadow.Outputs.ThermalBattery.HysteresisActive)
}

func TestThermalBattery_StartClearsStaleHolds(t *testing.T) {
	t.Parallel()
	// Simulate restart: holds are ON in HA (from a previous session that crashed
	// during thermal battery or hysteresis), no other thermal-battery activity.
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)
	env.MockHA.SetState(climateHouse, "heat_cool", map[string]interface{}{
		"target_temp_low": 69.0, "target_temp_high": 72.0,
		"current_temperature": 70.0, "hvac_action": "idle",
	})
	env.MockHA.SetState(climateSuite, "heat_cool", map[string]interface{}{
		"target_temp_low": 69.0, "target_temp_high": 72.0,
		"current_temperature": 70.0, "hvac_action": "idle",
	})
	require.NoError(t, env.StateMgr.SyncFromHA())

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryHoldRevertDelayForTesting(0)
	snapshot := env.MockHA.ServiceCallCount()

	require.NoError(t, ls.Start())
	defer ls.Stop()

	// Start() must turn off both holds unconditionally — even though no activation
	// is being attempted (currentEnergyLevel is empty / not white).
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	holdOffCount := 0
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				houseSeen, suiteSeen := false, false
				for _, e := range entities {
					if e == thermostatHoldHouse {
						houseSeen = true
					}
					if e == thermostatHoldSuite {
						suiteSeen = true
					}
				}
				if houseSeen && suiteSeen {
					holdOffCount++
				}
			}
		}
	}
	assert.Equal(t, 1, holdOffCount, "Start() must clear stale holds on both thermostats")
}

func TestThermalBattery_DeactivatesOnRedEnergyLevel(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Reset rate limit
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	_ = env.MockHA.ServiceCallCount()

	// Drop to red (should deactivate thermal battery AND enable load shedding)
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should be deactivated on red")
	assert.True(t, ls.IsLoadSheddingOn(), "Load shedding should be enabled on red")
}

func TestThermalBattery_SkipsWhenNoOneHome(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	// Set no one home
	err := env.StateMgr.SetBool("isAnyoneHome", false)
	require.NoError(t, err)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err = ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	// Try to activate
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should not activate when no one is home")

	// Verify no climate service calls were made (only SetString call)
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "climate" {
			t.Error("No climate service calls should be made when no one is home")
		}
	}

	// Verify shadow state records skip reason
	shadow := ls.GetShadowState()
	assert.Equal(t, "no one is home", shadow.Outputs.ThermalBattery.SkipReason)
}

func TestThermalBattery_SkipsWhenEveryoneAsleep(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	// Set everyone asleep
	err := env.StateMgr.SetBool("isEveryoneAsleep", true)
	require.NoError(t, err)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err = ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	// Try to activate
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should not activate when everyone is asleep")

	// Verify shadow state records skip reason
	shadow := ls.GetShadowState()
	assert.Equal(t, "everyone is asleep", shadow.Outputs.ThermalBattery.SkipReason)
}

func TestThermalBattery_DeactivatesWhenEveryoneLeaves(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	// Everyone leaves
	err = env.StateMgr.SetBool("isAnyoneHome", false)
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should deactivate when everyone leaves")

	// Verify setpoints were reverted
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	revertCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
		}
	}
	assert.Equal(t, 2, revertCalls, "Should revert both thermostats")
}

func TestThermalBattery_DeactivatesWhenEveryoneFallsAsleep(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	_ = env.MockHA.ServiceCallCount()

	// Everyone falls asleep
	err = env.StateMgr.SetBool("isEveryoneAsleep", true)
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should deactivate when everyone sleeps")
}

func TestThermalBattery_HeatingMode(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "off", nil)
	env.MockHA.SetState(thermostatHoldSuite, "off", nil)

	// Set climate entities to heating mode
	env.MockHA.SetState(climateHouse, "heat", map[string]interface{}{
		"temperature":         68.0,
		"current_temperature": 66.0,
		"hvac_action":         "heating",
	})
	env.MockHA.SetState(climateSuite, "heat", map[string]interface{}{
		"temperature":         70.0,
		"current_temperature": 68.0,
		"hvac_action":         "heating",
	})

	err := env.StateMgr.SetBool("isAnyoneHome", true)
	require.NoError(t, err)
	err = env.StateMgr.SetBool("isEveryoneAsleep", false)
	require.NoError(t, err)
	err = env.StateMgr.SyncFromHA()
	require.NoError(t, err)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err = ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// In heating mode, first step should shift UP by 1°F
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 69.0, temp, "House thermostat should be shifted from 68 to 69 (first step)")
			case climateSuite:
				assert.Equal(t, 71.0, temp, "Suite thermostat should be shifted from 70 to 71 (first step)")
			}
		}
	}
}

// makeForecastResponse builds a JSON forecast response for the mock HA client.
// Uses the real HA WebSocket API envelope: entity data is nested under "response".
func makeForecastResponse(high, low float64) json.RawMessage {
	resp := fmt.Sprintf(`{"context":{"id":"test"},"response":{"%s":{"forecast":[{"temperature":%v,"templow":%v}]}}}`, forecastWeatherEntityPrimary, high, low)
	return json.RawMessage(resp)
}

// setupHeatCoolEnv creates a test environment with thermostats in heat_cool mode
// using realistic owner setpoints (69/72, 3°F dead band).
// forecastHigh/forecastLow set the forecast; if both are 0, no forecast is configured.
// outdoorTemp sets the fallback outdoor temp sensor (used when forecast unavailable).
func setupHeatCoolEnv(t *testing.T, outdoorTemp string) *testutil.Env {
	return setupHeatCoolEnvWithForecast(t, outdoorTemp, 0, 0)
}

func setupHeatCoolEnvWithForecast(t *testing.T, outdoorTemp string, forecastHigh, forecastLow float64) *testutil.Env {
	t.Helper()
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "off", nil)
	env.MockHA.SetState(thermostatHoldSuite, "off", nil)

	// Realistic owner setpoints: 69/72 (3°F dead band)
	env.MockHA.SetState(climateHouse, "heat_cool", map[string]interface{}{
		"target_temp_low":     69.0,
		"target_temp_high":    72.0,
		"current_temperature": 70.0,
		"hvac_action":         "idle",
	})
	env.MockHA.SetState(climateSuite, "heat_cool", map[string]interface{}{
		"target_temp_low":     69.0,
		"target_temp_high":    72.0,
		"current_temperature": 71.0,
		"hvac_action":         "idle",
	})

	// Set forecast response if provided
	if forecastHigh != 0 || forecastLow != 0 {
		env.MockHA.SetServiceResponse("weather", "get_forecasts", makeForecastResponse(forecastHigh, forecastLow))
	}

	// Set outdoor temperature sensor (fallback)
	if outdoorTemp != "" {
		env.MockHA.SetState(outdoorTempSensor, outdoorTemp, nil)
	}

	err := env.StateMgr.SetBool("isAnyoneHome", true)
	require.NoError(t, err)
	err = env.StateMgr.SetBool("isEveryoneAsleep", false)
	require.NoError(t, err)
	err = env.StateMgr.SyncFromHA()
	require.NoError(t, err)

	return env
}

func TestThermalBattery_HeatCoolMode_ColdForecast(t *testing.T) {
	t.Parallel()
	// Winter scenario: forecast high 45°F, low 25°F → cold day, shifts UP by 1°F to 70/73
	// Low 25°F < skipLow (69-20=49), so direction = "up" (pre-heat)
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Cold forecast → first step shifts band UP by 1°F: 69/72 → 70/73
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 70.0, low, "House low should shift from 69 to 70 (first step)")
				assert.Equal(t, 73.0, high, "House high should shift from 72 to 73 (first step)")
			case climateSuite:
				assert.Equal(t, 70.0, low, "Suite low should shift from 69 to 70 (first step)")
				assert.Equal(t, 73.0, high, "Suite high should shift from 72 to 73 (first step)")
			}
		}
	}
}

func TestThermalBattery_HeatCoolMode_HotForecast(t *testing.T) {
	t.Parallel()
	// Summer scenario: forecast high 95°F, low 70°F → hot day, shifts DOWN by 1°F to 68/71
	// High 95°F > skipHigh (72+20=92), so direction = "down" (pre-cool)
	env := setupHeatCoolEnvWithForecast(t, "", 95.0, 70.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Hot forecast → first step shifts band DOWN by 1°F: 69/72 → 68/71
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 68.0, low, "House low should shift from 69 to 68 (first step)")
				assert.Equal(t, 71.0, high, "House high should shift from 72 to 71 (first step)")
			case climateSuite:
				assert.Equal(t, 68.0, low, "Suite low should shift from 69 to 68 (first step)")
				assert.Equal(t, 71.0, high, "Suite high should shift from 72 to 71 (first step)")
			}
		}
	}
}

func TestThermalBattery_HeatCoolMode_MildForecast_SkipsInSkipZone(t *testing.T) {
	t.Parallel()
	// Spring scenario: forecast high 75°F, low 55°F, 69/72 setpoints → skip zone is 49-92°F
	// Both high (75) <= 92 and low (55) >= 49 → should skip
	env := setupHeatCoolEnvWithForecast(t, "", 75.0, 55.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should skip when outdoor temp is within skip zone")

	// Verify no climate service calls were made
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			t.Error("No climate.set_temperature calls should be made when in skip zone")
		}
	}

	shadow := ls.GetShadowState()
	assert.Contains(t, shadow.Outputs.ThermalBattery.SkipReason, "skip zone")
}

func TestThermalBattery_HeatCoolMode_ForecastUnavailable_FallsBackToOutdoor(t *testing.T) {
	t.Parallel()
	// No forecast configured, but outdoor temp sensor is available → uses fallback
	env := setupHeatCoolEnv(t, "35.0") // No forecast, outdoor temp = 35°F (cold)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// Should activate using fallback outdoor temp (35°F < 49 skip_low → direction "up")
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should activate using outdoor temp fallback")
}

func TestThermalBattery_HeatCoolMode_BothUnavailable(t *testing.T) {
	t.Parallel()
	// No forecast AND no outdoor temp sensor → should skip
	env := setupHeatCoolEnv(t, "") // No forecast, no outdoor temp

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should skip when both forecast and outdoor sensor unavailable")

	shadow := ls.GetShadowState()
	assert.Contains(t, shadow.Outputs.ThermalBattery.SkipReason, "outdoor temp sensor unavailable")
}

func TestThermalBattery_HeatCoolMode_DeactivationRestoresOriginal(t *testing.T) {
	t.Parallel()
	// Activate with cold forecast, then hard-deactivate via yellow drop
	// → verify original 69/72 restored. (Green now enters hysteresis instead of
	// reverting; yellow/red/black are the hard-deactivation triggers.)
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	// Hard-deactivate via yellow drop
	err = env.StateMgr.SetString("currentEnergyLevel", "yellow")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive())

	// Verify original setpoints were restored (69/72)
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	revertCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)
			assert.Equal(t, 69.0, low, "Should revert to original low of 69")
			assert.Equal(t, 72.0, high, "Should revert to original high of 72")
		}
	}
	assert.Equal(t, 2, revertCalls, "Should revert both thermostats")
}

func TestThermalBattery_NtfyNotificationOnActivation(t *testing.T) {
	t.Parallel()
	// Verify ntfy notification is sent when thermal battery activates with forecast
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	mockAlerter := &alert.MockAlerter{}
	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, mockAlerter)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Verify ntfy notification was sent with forecast info
	alertCalls := mockAlerter.Calls()
	require.Len(t, alertCalls, 1, "Should have sent exactly one ntfy notification")
	assert.Equal(t, "Thermal Battery Activated", alertCalls[0].Title)
	assert.Contains(t, alertCalls[0].Body, "UP (pre-heat)")
	assert.Contains(t, alertCalls[0].Body, "forecast high: 45")
	assert.Contains(t, alertCalls[0].Body, "low: 25")
}

func TestThermalBattery_NtfyNotificationOnActivation_HourlyPath(t *testing.T) {
	t.Parallel()
	// Regression test for issue #1011: hourly-forecast path showed "outdoor: 0.0°F" in notification.
	// Verify that when activated via hourly forecast (solar tail reached), the notification
	// includes stress time and temperature instead of zeroed-out outdoor temp.
	stressTime := time.Now().Add(8 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 37.0, stressTime, 6.0) // 6 kWh < threshold → activates

	mockAlerter := &alert.MockAlerter{}
	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, mockAlerter)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should activate when solar tail is reached")

	alertCalls := mockAlerter.Calls()
	require.Len(t, alertCalls, 1, "Should have sent exactly one ntfy notification")
	assert.Equal(t, "Thermal Battery Activated", alertCalls[0].Title)
	assert.Contains(t, alertCalls[0].Body, "UP (pre-heat)")
	assert.Contains(t, alertCalls[0].Body, "stress at")
	assert.Contains(t, alertCalls[0].Body, stressTime.Local().Format("3:04 PM"))
	assert.Contains(t, alertCalls[0].Body, "37")
	// Must NOT show zeroed-out outdoor temp (the bug)
	assert.NotContains(t, alertCalls[0].Body, "outdoor: 0.0°F")
}

func TestThermalBattery_SkipsWhenThermostatOff(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	// Override: set one thermostat to off
	env.MockHA.SetState(climateHouse, "off", map[string]interface{}{})

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should not activate if thermostat is off")

	shadow := ls.GetShadowState()
	assert.Contains(t, shadow.Outputs.ThermalBattery.SkipReason, "is off")
}

func TestThermalBattery_ReadOnlyMode(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, true, nil, nil) // readOnly=true
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// In read-only mode, thermal battery should activate (tracks state) but no service calls
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should track state in read-only mode")

	// Verify no climate service calls were made (just SetString)
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	for _, call := range calls {
		if call.Domain == "climate" {
			t.Error("No climate service calls should be made in read-only mode")
		}
	}
}

func TestThermalBattery_DoesNotActivateDuringLoadShedding(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	ls.SetThermalBatteryHoldRevertDelayForTesting(0)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Enable load shedding first
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	require.NoError(t, err)
	assert.True(t, ls.IsLoadSheddingOn())

	// Reset rate limit
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	_ = env.MockHA.ServiceCallCount()

	// Now try white - load shedding should be disabled first, then thermal battery should activate
	// But since disableLoadShedding runs first and then activateThermalBattery,
	// the load shedding state should be false by the time thermal battery checks it
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// Load shedding should be disabled (white is in the disable path)
	assert.False(t, ls.IsLoadSheddingOn(), "Load shedding should be disabled at white")
	// Thermal battery should be active
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should activate after load shedding disables")
}

func TestThermalBattery_IdempotentActivation(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	// Try to activate again (should be idempotent)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// Should not make any climate service calls since already active
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	climateCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			climateCalls++
		}
	}
	assert.Equal(t, 0, climateCalls, "No climate calls should be made when thermal battery already active")
}

func TestThermalBattery_YellowDeactivatesThermalBattery(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery at white
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	_ = env.MockHA.ServiceCallCount()

	// Move to yellow - thermal battery should be deactivated (energy is declining)
	err = env.StateMgr.SetString("currentEnergyLevel", "yellow")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should be deactivated at yellow energy level")
}

// =============================================================================
// New stepping tests
// =============================================================================

func TestThermalBattery_SteppingCompletesFullOffset(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryPollIntervalForTesting(10 * time.Millisecond)

	// Use a WaitGroup to synchronize on step 2 completion
	var wg sync.WaitGroup
	wg.Add(1)
	ls.SetThermalBatteryStepDoneCallback(func(stepNumber int) {
		if stepNumber == 2 {
			wg.Done()
		}
	})

	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	// Activate: first step shifts by 1°F (cool mode: 72→71, 71→70)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Simulate thermostats reaching the first step target:
	// In cooling mode, current_temperature should be at or below the stepped setpoint + 1°F
	// House: target after step 1 is 71°F, so current_temp <= 72 means "reached"
	// Suite: target after step 1 is 70°F, so current_temp <= 71 means "reached"
	env.MockHA.SetState(climateHouse, "cool", map[string]interface{}{
		"temperature":         71.0,
		"current_temperature": 71.0,
		"hvac_action":         "idle",
	})
	env.MockHA.SetState(climateSuite, "cool", map[string]interface{}{
		"temperature":         70.0,
		"current_temperature": 70.0,
		"hvac_action":         "idle",
	})

	// Wait for step 2 to complete
	wg.Wait()

	// Verify both steps were applied
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	step1Calls := 0
	step2Calls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			switch entityID {
			case climateHouse:
				if temp == 71.0 {
					step1Calls++
				} else if temp == 70.0 {
					step2Calls++
				}
			case climateSuite:
				if temp == 70.0 {
					step1Calls++
				} else if temp == 69.0 {
					step2Calls++
				}
			}
		}
	}
	assert.Equal(t, 2, step1Calls, "Should have 2 step-1 calls (one per thermostat)")
	assert.Equal(t, 2, step2Calls, "Should have 2 step-2 calls (one per thermostat)")

	// Verify shadow state shows all steps complete
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
	assert.Equal(t, 2.0, shadow.Outputs.ThermalBattery.OffsetApplied)
	assert.Equal(t, 2, shadow.Outputs.ThermalBattery.StepsCompleted)
	assert.Equal(t, 2, shadow.Outputs.ThermalBattery.TotalSteps)
	assert.False(t, shadow.Outputs.ThermalBattery.Stepping, "Should not be stepping (all steps done)")
}

func TestThermalBattery_DeactivationMidStep(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	// Use a very long poll interval so step 2 never fires
	ls.SetThermalBatteryPollIntervalForTesting(1 * time.Hour)
	ls.lastAction = time.Now().Add(-2 * time.Hour)

	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	// Activate: only step 1 applied (poll interval is 1h so step 2 won't happen)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Verify only step 1 was applied
	shadow := ls.GetShadowState()
	assert.Equal(t, 1, shadow.Outputs.ThermalBattery.StepsCompleted)
	assert.True(t, shadow.Outputs.ThermalBattery.Stepping)

	snapshot := env.MockHA.ServiceCallCount()

	// Hard-deactivate by dropping to yellow (green now enters hysteresis instead)
	err = env.StateMgr.SetString("currentEnergyLevel", "yellow")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive())

	// Verify original setpoints were restored (not step-1 values)
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	revertCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 72.0, temp, "House should revert to original 72, not stepped 71")
			case climateSuite:
				assert.Equal(t, 71.0, temp, "Suite should revert to original 71, not stepped 70")
			}
		}
	}
	assert.Equal(t, 2, revertCalls, "Should revert both thermostats")

	// Verify shadow state is clean
	shadow = ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Active)
	assert.False(t, shadow.Outputs.ThermalBattery.Stepping)
}

func TestThermalBattery_SteppingCancelledByPresenceChange(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	// Use long poll so step 2 won't fire on its own
	ls.SetThermalBatteryPollIntervalForTesting(1 * time.Hour)

	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	// Activate
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	snapshot := env.MockHA.ServiceCallCount()

	// Everyone leaves mid-stepping
	err = env.StateMgr.SetBool("isAnyoneHome", false)
	require.NoError(t, err)

	// Thermal battery should be deactivated
	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should deactivate when everyone leaves during stepping")

	// Verify original setpoints were restored
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	revertCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
		}
	}
	assert.Equal(t, 2, revertCalls, "Should revert both thermostats")
}

func TestThermalBattery_RevertsStaleHoldsOnActivation(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryHoldRevertDelayForTesting(0)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Simulate user manually toggling holds ON between Start() and the activation
	// trigger (Start's stale-hold cleanup ran with holds=off; defense-in-depth in
	// activateThermalBattery should still catch this).
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)

	snapshot := env.MockHA.ServiceCallCount()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Verify holds were turned off (revert stale) then turned on (for thermal battery)
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	holdOffIndex := -1
	holdOnIndex := -1
	for i, call := range calls {
		if call.Domain == "switch" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				for _, e := range entities {
					if e == thermostatHoldHouse || e == thermostatHoldSuite {
						if call.Service == "turn_off" && holdOffIndex == -1 {
							holdOffIndex = i
						}
						if call.Service == "turn_on" && holdOnIndex == -1 {
							holdOnIndex = i
						}
						break
					}
				}
			}
		}
	}

	assert.NotEqual(t, -1, holdOffIndex, "Should have turned off stale holds")
	assert.NotEqual(t, -1, holdOnIndex, "Should have turned on holds for thermal battery")
	assert.Less(t, holdOffIndex, holdOnIndex, "Hold off (revert) should happen before hold on (thermal battery)")
}

func TestThermalBattery_HeatCoolSteppingCompletesFullOffset(t *testing.T) {
	t.Parallel()
	// Cold forecast: high 45°F, low 25°F → direction "up" → heat_cool band shifts up
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryPollIntervalForTesting(10 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	ls.SetThermalBatteryStepDoneCallback(func(stepNumber int) {
		if stepNumber == 2 {
			wg.Done()
		}
	})

	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	// Activate: first step shifts band UP by 1°F: 69/72 → 70/73
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Simulate thermostats reaching step-1 target (heating direction: current temp near shifted low)
	// Step 1 target low = 70°F, so current_temp >= 69 (70-1) means ready
	env.MockHA.SetState(climateHouse, "heat_cool", map[string]interface{}{
		"target_temp_low":     70.0,
		"target_temp_high":    73.0,
		"current_temperature": 70.0,
		"hvac_action":         "idle",
	})
	env.MockHA.SetState(climateSuite, "heat_cool", map[string]interface{}{
		"target_temp_low":     70.0,
		"target_temp_high":    73.0,
		"current_temperature": 71.0,
		"hvac_action":         "idle",
	})

	// Wait for step 2 to complete
	wg.Wait()

	// Verify final step applies full 2°F offset: 69/72 → 71/74
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	step2Found := false
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)

			if entityID == climateHouse && low == 71.0 && high == 74.0 {
				step2Found = true
			}
		}
	}
	assert.True(t, step2Found, "Should find step-2 heat_cool call with 71/74 setpoints")

	// Verify shadow state
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
	assert.Equal(t, 2.0, shadow.Outputs.ThermalBattery.OffsetApplied)
	assert.Equal(t, 2, shadow.Outputs.ThermalBattery.StepsCompleted)
	assert.False(t, shadow.Outputs.ThermalBattery.Stepping)
}

func TestThermalBattery_SafetyTimeoutForcesStepAdvancement(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryPollIntervalForTesting(10 * time.Millisecond)
	// Set a very short safety timeout so it triggers quickly in tests
	ls.SetThermalBatteryMaxStepWaitForTesting(20 * time.Millisecond)

	// Wait for step 2 to complete (which should happen via safety timeout, not temperature check)
	var wg sync.WaitGroup
	wg.Add(1)
	ls.SetThermalBatteryStepDoneCallback(func(stepNumber int) {
		if stepNumber == 2 {
			wg.Done()
		}
	})

	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	snapshot := env.MockHA.ServiceCallCount()

	// Activate: first step shifts by 1°F (cool mode: 72→71, 71→70)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Do NOT update current_temperature — simulate a stuck/offline thermostat sensor.
	// The thermostats still report the original current_temperature (74°F, 73°F),
	// which is far from the step-1 targets (71°F, 70°F). Without the safety timeout,
	// the stepping goroutine would poll forever.

	// Wait for step 2 to complete via safety timeout
	wg.Wait()

	// Verify both steps were applied despite thermostat never reaching target
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	step2Calls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			// Step 2 applies full 2°F offset: house 72→70, suite 71→69
			if (entityID == climateHouse && temp == 70.0) ||
				(entityID == climateSuite && temp == 69.0) {
				step2Calls++
			}
		}
	}
	assert.Equal(t, 2, step2Calls, "Should have 2 step-2 calls (forced by safety timeout)")

	// Verify shadow state shows all steps complete
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
	assert.Equal(t, 2.0, shadow.Outputs.ThermalBattery.OffsetApplied)
	assert.Equal(t, 2, shadow.Outputs.ThermalBattery.StepsCompleted)
	assert.Equal(t, 2, shadow.Outputs.ThermalBattery.TotalSteps)
	assert.False(t, shadow.Outputs.ThermalBattery.Stepping, "Should not be stepping (all steps done)")
}

// =============================================================================
// Solar-tail timing tests (issue #1002)
// =============================================================================

// makeHourlyStressForecast creates an hourly forecast with a single stress event
// at stressTime at stressTemp. Used for testing solar-tail deferral.
// The response uses the real HA WebSocket API envelope format.
func makeHourlyStressForecast(stressTemp float64, stressTime time.Time) json.RawMessage {
	resp := fmt.Sprintf(
		`{"context":{"id":"test"},"response":{"%s":{"forecast":[{"datetime":"%s","temperature":%.1f}]}}}`,
		forecastWeatherEntityPrimary,
		stressTime.UTC().Format(time.RFC3339),
		stressTemp,
	)
	return json.RawMessage(resp)
}

// makeHourlyNoStressForecast creates an hourly forecast with mild temperatures
// that stay within the comfort zone (69/72 ±5°F = 64-77°F) for 24 hours.
func makeHourlyNoStressForecast() json.RawMessage {
	now := time.Now().Truncate(time.Hour)
	parts := make([]string, 0, 24)
	for h := 1; h <= 24; h++ {
		t := now.Add(time.Duration(h) * time.Hour)
		parts = append(parts, fmt.Sprintf(`{"datetime":"%s","temperature":70.0}`, t.UTC().Format(time.RFC3339)))
	}
	resp := fmt.Sprintf(`{"context":{"id":"test"},"response":{"%s":{"forecast":[%s]}}}`,
		forecastWeatherEntityPrimary, strings.Join(parts, ","))
	return json.RawMessage(resp)
}

// setupHeatCoolEnvWithHourlyForecast creates a heat_cool test environment with an
// hourly forecast and remainingSolarGeneration state. stressTemp below 64°F triggers
// "up" (pre-heat), above 77°F triggers "down" (pre-cool). remainingSolarKWh is the
// value for remainingSolarGeneration state variable used by the solar-tail gate.
func setupHeatCoolEnvWithHourlyForecast(t *testing.T, stressTemp float64, stressTime time.Time, remainingSolarKWh float64) *testutil.Env {
	t.Helper()
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "off", nil)
	env.MockHA.SetState(thermostatHoldSuite, "off", nil)

	env.MockHA.SetState(climateHouse, "heat_cool", map[string]interface{}{
		"target_temp_low":     69.0,
		"target_temp_high":    72.0,
		"current_temperature": 70.0,
		"hvac_action":         "idle",
	})
	env.MockHA.SetState(climateSuite, "heat_cool", map[string]interface{}{
		"target_temp_low":     69.0,
		"target_temp_high":    72.0,
		"current_temperature": 71.0,
		"hvac_action":         "idle",
	})

	env.MockHA.SetServiceResponse("weather", "get_forecasts", makeHourlyStressForecast(stressTemp, stressTime))

	require.NoError(t, env.StateMgr.SetBool("isAnyoneHome", true))
	require.NoError(t, env.StateMgr.SetBool("isEveryoneAsleep", false))
	require.NoError(t, env.StateMgr.SetNumber("remainingSolarGeneration", remainingSolarKWh))
	require.NoError(t, env.StateMgr.SetBool("isFreeEnergyAvailable", false))
	require.NoError(t, env.StateMgr.SyncFromHA())
	return env
}

func TestThermalBattery_SolarTail_DeferredWhenRemainingAboveThreshold(t *testing.T) {
	t.Parallel()
	// Comfort band: 69/72. Margin: 5°F. Stress threshold: < 64°F (cold).
	// Stress event: cold (37°F) overnight. Remaining solar: 35 kWh. Threshold: 15 kWh.
	// Expected: deferred (35 > 15), shadow state shows deferred=true with solar-tail reason.
	stressTime := time.Now().Add(8 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 37.0, stressTime, 35.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	_ = env.MockHA.ServiceCallCount()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))

	// Should be deferred, not active
	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should be deferred, not active")

	// No climate service calls should be made
	calls := env.MockHA.GetServiceCallsSince(0)
	for _, call := range calls {
		if call.Domain == "climate" {
			t.Error("No climate.set_temperature calls should be made when thermal battery is deferred")
		}
	}

	// Shadow state reflects solar-tail deferral
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Deferred, "Shadow state should show deferred=true")
	assert.Equal(t, "up", shadow.Outputs.ThermalBattery.StressDirection, "Cold stress should be 'up' direction")
	assert.Contains(t, shadow.Outputs.ThermalBattery.DeferReason, "solar tail not yet reached")
	assert.Equal(t, 35.0, shadow.Outputs.ThermalBattery.RemainingSolarKWh)
	assert.Equal(t, 15.0, shadow.Outputs.ThermalBattery.SolarTailThresholdKWh)
	assert.Contains(t, shadow.Outputs.ThermalBattery.ForecastStress, "up")
	assert.Contains(t, shadow.Outputs.ThermalBattery.ForecastStress, "37.0")
}

func TestThermalBattery_SolarTail_ActivatesWhenRemainingBelowThreshold(t *testing.T) {
	t.Parallel()
	// Stress event: cold (50°F) overnight. Remaining solar: 6 kWh. Threshold: 15 kWh.
	// Expected: activates immediately (solar tail reached).
	stressTime := time.Now().Add(8 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 50.0, stressTime, 6.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))

	// Should activate immediately (remaining solar <= threshold)
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should activate when remaining solar is below threshold")

	// Shadow state should NOT be deferred
	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Deferred, "Shadow state should not be deferred")
	assert.True(t, shadow.Outputs.ThermalBattery.Active, "Shadow state should show active=true")
}

func TestThermalBattery_SolarTail_ActivatesImmediatelyDuringFreeEnergy(t *testing.T) {
	t.Parallel()
	// Free-energy hours (overnight utility window) — solar timing does not apply.
	// Even with remaining solar above threshold, should activate immediately.
	stressTime := time.Now().Add(2 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 37.0, stressTime, 35.0)
	require.NoError(t, env.StateMgr.SetBool("isFreeEnergyAvailable", true))

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))

	// Should activate immediately (free energy in effect)
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should activate during free-energy hours regardless of solar tail")

	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Deferred)
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
}

func TestThermalBattery_SolarTail_SkipsWhenNoStress(t *testing.T) {
	t.Parallel()
	// Hourly forecast with mild temps (70°F, within 64-77°F comfort zone).
	// Expected: skip (no thermal stress in window), regardless of solar remaining.
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "off", nil)
	env.MockHA.SetState(thermostatHoldSuite, "off", nil)
	env.MockHA.SetState(climateHouse, "heat_cool", map[string]interface{}{
		"target_temp_low": 69.0, "target_temp_high": 72.0, "current_temperature": 70.0,
	})
	env.MockHA.SetState(climateSuite, "heat_cool", map[string]interface{}{
		"target_temp_low": 69.0, "target_temp_high": 72.0, "current_temperature": 70.0,
	})
	env.MockHA.SetServiceResponse("weather", "get_forecasts", makeHourlyNoStressForecast())
	require.NoError(t, env.StateMgr.SetBool("isAnyoneHome", true))
	require.NoError(t, env.StateMgr.SetBool("isEveryoneAsleep", false))
	require.NoError(t, env.StateMgr.SetNumber("remainingSolarGeneration", 5.0))
	require.NoError(t, env.StateMgr.SyncFromHA())

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should skip when no stress in forecast")

	shadow := ls.GetShadowState()
	assert.Contains(t, shadow.Outputs.ThermalBattery.SkipReason, "no thermal stress")
}

func TestThermalBattery_SolarTail_DeferredClearedOnEnergyDrop(t *testing.T) {
	t.Parallel()
	// Set up deferred state, then drop energy level. Deferred state should clear.
	stressTime := time.Now().Add(8 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 37.0, stressTime, 35.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	ls.lastAction = time.Now().Add(-2 * time.Hour) // avoid rate limiting
	require.NoError(t, ls.Start())
	defer ls.Stop()

	// Activate to white → should defer (solar still high)
	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))
	assert.False(t, ls.IsThermalBatteryActive())
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Deferred, "Should be deferred initially")

	// Drop to yellow → deferred state should clear
	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "yellow"))

	assert.False(t, ls.IsThermalBatteryActive(), "Should not be active after energy drop")
	shadow = ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Deferred, "Deferred state should be cleared after energy drop")
	assert.Empty(t, shadow.Outputs.ThermalBattery.DeferReason, "DeferReason should be cleared")
}

func TestThermalBattery_SolarTail_DeferredClearedWhenNoOneHome(t *testing.T) {
	t.Parallel()
	// Set up deferred state, then mark no one home. Deferred state should clear.
	stressTime := time.Now().Add(8 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 37.0, stressTime, 35.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	// Activate to white → should defer
	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))
	assert.False(t, ls.IsThermalBatteryActive())
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Deferred, "Should be deferred initially")

	// Everyone leaves → deferred state should clear
	require.NoError(t, env.StateMgr.SetBool("isAnyoneHome", false))

	shadow = ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Deferred, "Deferred state should be cleared when no one is home")
	assert.Empty(t, shadow.Outputs.ThermalBattery.DeferReason, "DeferReason should be cleared")
}

func TestThermalBattery_SolarTail_DeferredTimerActivatesWhenSolarTailReached(t *testing.T) {
	t.Parallel()
	// Initial state: remaining solar 35 kWh, threshold 15 kWh → defer.
	// After the first recheck, lower remainingSolarGeneration below threshold.
	// The timer goroutine should re-evaluate and activate.
	recheckInterval := 50 * time.Millisecond

	stressTime := time.Now().Add(4 * time.Hour)
	env := setupHeatCoolEnvWithHourlyForecast(t, 37.0, stressTime, 35.0)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatterySolarTailThresholdForTesting(15.0)
	ls.SetThermalBatteryDeferredRecheckIntervalForTesting(recheckInterval)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))

	// Initially deferred
	assert.False(t, ls.IsThermalBatteryActive(), "Should be deferred initially")
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Deferred, "Should show deferred in shadow state")

	// Drop remaining solar below threshold — mimics solar curve tailing off.
	require.NoError(t, env.StateMgr.SetNumber("remainingSolarGeneration", 6.0))

	// Timer should re-evaluate and activate.
	require.Eventually(t, ls.IsThermalBatteryActive, 4*time.Second, recheckInterval,
		"Thermal battery should activate after remaining solar drops below threshold")
	shadow = ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Deferred, "Shadow state should not be deferred after activation")
	assert.True(t, shadow.Outputs.ThermalBattery.Active, "Shadow state should show active=true")
}

func TestThermalBattery_SolarTail_FallsBackToDailyWhenHourlyUnavailable(t *testing.T) {
	t.Parallel()
	// No hourly forecast configured (daily format returned = no valid datetimes).
	// Fall back to daily forecast logic. Cold daily forecast → activates.
	// Solar-tail gate only applies on the hourly path.
	env := setupHeatCoolEnvWithForecast(t, "", 45.0, 25.0) // daily forecast only

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	require.NoError(t, ls.Start())
	defer ls.Stop()

	require.NoError(t, env.StateMgr.SetString("currentEnergyLevel", "white"))

	// Should activate using daily forecast fallback (not deferred)
	assert.True(t, ls.IsThermalBatteryActive(), "Should activate using daily forecast when hourly unavailable")
	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Deferred, "Should not be deferred when using daily fallback")
}
