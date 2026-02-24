package loadshedding

import (
	"context"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ntfy"
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

	env.MockHA.ClearServiceCalls()

	// Set energy state to white
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// Verify thermal battery is active
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should be active at white energy level")

	// Verify thermostat hold was enabled before temperature changes
	calls := env.MockHA.GetServiceCalls()
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

func TestThermalBattery_DeactivatesOnGreenEnergyLevel(t *testing.T) {
	t.Parallel()
	env := setupThermalBatteryEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour) // Avoid rate limiting
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	env.MockHA.ClearServiceCalls()

	// Drop to green
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	// Verify thermal battery is deactivated
	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should be inactive after green")

	// Verify setpoints were reverted and hold was disabled
	calls := env.MockHA.GetServiceCalls()
	revertCalls := 0
	holdOffCalls := 0
	lastRevertIndex := -1
	holdOffIndex := -1
	for i, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			revertCalls++
			lastRevertIndex = i
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 72.0, temp, "House thermostat should be reverted to 72")
			case climateSuite:
				assert.Equal(t, 71.0, temp, "Suite thermostat should be reverted to 71")
			}
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			if entities, ok := call.Data["entity_id"].([]string); ok {
				for _, e := range entities {
					if e == thermostatHoldHouse || e == thermostatHoldSuite {
						holdOffCalls++
						holdOffIndex = i
						break
					}
				}
			}
		}
	}
	assert.Equal(t, 2, revertCalls, "Should have made 2 climate.set_temperature calls to revert")
	assert.Equal(t, 1, holdOffCalls, "Should have made 1 switch.turn_off call to disable thermostat holds")
	assert.Greater(t, holdOffIndex, lastRevertIndex, "Hold should be disabled after setpoints are reverted")

	// Verify shadow state
	shadow := ls.GetShadowState()
	assert.False(t, shadow.Outputs.ThermalBattery.Active)
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

	// Reset rate limit and clear calls
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	env.MockHA.ClearServiceCalls()

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

	env.MockHA.ClearServiceCalls()

	// Try to activate
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should not activate when no one is home")

	// Verify no climate service calls were made (only SetString call)
	calls := env.MockHA.GetServiceCalls()
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

	env.MockHA.ClearServiceCalls()

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

	env.MockHA.ClearServiceCalls()

	// Everyone leaves
	err = env.StateMgr.SetBool("isAnyoneHome", false)
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should deactivate when everyone leaves")

	// Verify setpoints were reverted
	calls := env.MockHA.GetServiceCalls()
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

	env.MockHA.ClearServiceCalls()

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

	env.MockHA.ClearServiceCalls()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// In heating mode, first step should shift UP by 1°F
	calls := env.MockHA.GetServiceCalls()
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

// setupHeatCoolEnv creates a test environment with thermostats in heat_cool mode
// using realistic owner setpoints (69/72, 3°F dead band).
func setupHeatCoolEnv(t *testing.T, outdoorTemp string) *testutil.Env {
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

	// Set outdoor temperature sensor
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

func TestThermalBattery_HeatCoolMode_ColdOutdoor(t *testing.T) {
	t.Parallel()
	// Winter scenario: 35°F outside, 69/72 setpoints → first step shifts UP by 1°F to 70/73
	env := setupHeatCoolEnv(t, "35.0")

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Cold outside → first step shifts band UP by 1°F: 69/72 → 70/73
	calls := env.MockHA.GetServiceCalls()
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

func TestThermalBattery_HeatCoolMode_HotOutdoor(t *testing.T) {
	t.Parallel()
	// Summer scenario: 95°F outside, 69/72 setpoints → first step shifts DOWN by 1°F to 68/71
	env := setupHeatCoolEnv(t, "95.0")

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Hot outside → first step shifts band DOWN by 1°F: 69/72 → 68/71
	calls := env.MockHA.GetServiceCalls()
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

func TestThermalBattery_HeatCoolMode_MildOutdoor_SkipsInSkipZone(t *testing.T) {
	t.Parallel()
	// Spring scenario: 70°F outside, 69/72 setpoints → skip zone is 49-92°F → should skip
	env := setupHeatCoolEnv(t, "70.0")

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should skip when outdoor temp is within skip zone")

	// Verify no climate service calls were made
	calls := env.MockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			t.Error("No climate.set_temperature calls should be made when in skip zone")
		}
	}

	shadow := ls.GetShadowState()
	assert.Contains(t, shadow.Outputs.ThermalBattery.SkipReason, "skip zone")
}

func TestThermalBattery_HeatCoolMode_OutdoorSensorUnavailable(t *testing.T) {
	t.Parallel()
	// No outdoor temp sensor configured → should skip
	env := setupHeatCoolEnv(t, "") // empty = don't set the sensor state

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should skip when outdoor sensor unavailable")

	shadow := ls.GetShadowState()
	assert.Contains(t, shadow.Outputs.ThermalBattery.SkipReason, "outdoor temp sensor unavailable")
}

func TestThermalBattery_HeatCoolMode_DeactivationRestoresOriginal(t *testing.T) {
	t.Parallel()
	// Activate with cold outdoor, then deactivate → verify original 69/72 restored
	env := setupHeatCoolEnv(t, "35.0")

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	env.MockHA.ClearServiceCalls()

	// Deactivate by dropping to green
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive())

	// Verify original setpoints were restored (69/72)
	calls := env.MockHA.GetServiceCalls()
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
	// Verify ntfy notification is sent when thermal battery activates
	env := setupHeatCoolEnv(t, "35.0")

	mockNtfy := ntfy.NewMockClient()
	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, mockNtfy)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Verify ntfy notification was sent
	ntfyCalls := mockNtfy.GetCalls()
	require.Len(t, ntfyCalls, 1, "Should have sent exactly one ntfy notification")
	assert.Equal(t, "Thermal Battery Activated", ntfyCalls[0].Title)
	assert.Contains(t, ntfyCalls[0].Body, "UP (pre-heat)")
	assert.Contains(t, ntfyCalls[0].Body, "outdoor: 35.0")
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

	env.MockHA.ClearServiceCalls()

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

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// In read-only mode, thermal battery should activate (tracks state) but no service calls
	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should track state in read-only mode")

	// Verify no climate service calls were made (just SetString)
	calls := env.MockHA.GetServiceCalls()
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
	env.MockHA.ClearServiceCalls()

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

	env.MockHA.ClearServiceCalls()

	// Try to activate again (should be idempotent)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	// Should not make any climate service calls since already active
	calls := env.MockHA.GetServiceCalls()
	climateCalls := 0
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			climateCalls++
		}
	}
	assert.Equal(t, 0, climateCalls, "No climate calls should be made when thermal battery already active")
}

func TestThermalBattery_YellowMaintainsState(t *testing.T) {
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

	env.MockHA.ClearServiceCalls()

	// Move to yellow (hysteresis) - thermal battery should remain active
	err = env.StateMgr.SetString("currentEnergyLevel", "yellow")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive(), "Thermal battery should remain active during yellow hysteresis")
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

	env.MockHA.ClearServiceCalls()

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
	calls := env.MockHA.GetServiceCalls()
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

	env.MockHA.ClearServiceCalls()

	// Activate: only step 1 applied (poll interval is 1h so step 2 won't happen)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	// Verify only step 1 was applied
	shadow := ls.GetShadowState()
	assert.Equal(t, 1, shadow.Outputs.ThermalBattery.StepsCompleted)
	assert.True(t, shadow.Outputs.ThermalBattery.Stepping)

	env.MockHA.ClearServiceCalls()

	// Deactivate by dropping to green
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	require.NoError(t, err)

	assert.False(t, ls.IsThermalBatteryActive())

	// Verify original setpoints were restored (not step-1 values)
	calls := env.MockHA.GetServiceCalls()
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

	env.MockHA.ClearServiceCalls()

	// Activate
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)
	assert.True(t, ls.IsThermalBatteryActive())

	env.MockHA.ClearServiceCalls()

	// Everyone leaves mid-stepping
	err = env.StateMgr.SetBool("isAnyoneHome", false)
	require.NoError(t, err)

	// Thermal battery should be deactivated
	assert.False(t, ls.IsThermalBatteryActive(), "Thermal battery should deactivate when everyone leaves during stepping")

	// Verify original setpoints were restored
	calls := env.MockHA.GetServiceCalls()
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

	// Simulate stale holds from a previous session (e.g., app restarted while thermal battery was active)
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	ls.SetThermalBatteryHoldRevertDelayForTesting(0)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	// Activate thermal battery
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Verify holds were turned off (revert stale) then turned on (for thermal battery)
	calls := env.MockHA.GetServiceCalls()
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
	// Cold outdoor: 35°F → direction "up" → heat_cool band shifts up
	env := setupHeatCoolEnv(t, "35.0")

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

	env.MockHA.ClearServiceCalls()

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
	calls := env.MockHA.GetServiceCalls()
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

	env.MockHA.ClearServiceCalls()

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
	calls := env.MockHA.GetServiceCalls()
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
