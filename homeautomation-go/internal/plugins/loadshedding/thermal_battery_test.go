package loadshedding

import (
	"context"
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

			// In cooling mode, setpoint should be shifted DOWN by 3°F
			switch entityID {
			case climateHouse:
				assert.Equal(t, 69.0, temp, "House thermostat should be shifted from 72 to 69")
			case climateSuite:
				assert.Equal(t, 68.0, temp, "Suite thermostat should be shifted from 71 to 68")
			}

			// Hold must be enabled before any temperature shift
			assert.Greater(t, i, holdCallIndex, "Thermostat hold should be enabled before setting temperature")
		}
	}
	assert.Equal(t, 1, holdCalls, "Should have made 1 switch.turn_on call for thermostat holds")
	assert.Equal(t, 2, climateCalls, "Should have made 2 climate.set_temperature calls")

	// Verify shadow state
	shadow := ls.GetShadowState()
	assert.True(t, shadow.Outputs.ThermalBattery.Active)
	assert.Equal(t, 3.0, shadow.Outputs.ThermalBattery.OffsetApplied)
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

	// In heating mode, setpoint should be shifted UP by 3°F
	calls := env.MockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			temp, _ := call.Data["temperature"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 71.0, temp, "House thermostat should be shifted from 68 to 71 in heat mode")
			case climateSuite:
				assert.Equal(t, 73.0, temp, "Suite thermostat should be shifted from 70 to 73 in heat mode")
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
	// Winter scenario: 35°F outside, 69/72 setpoints → should shift UP to 72/75
	env := setupHeatCoolEnv(t, "35.0")

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Cold outside → shift band UP (pre-heat): 69/72 → 72/75
	calls := env.MockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 72.0, low, "House low should shift from 69 to 72")
				assert.Equal(t, 75.0, high, "House high should shift from 72 to 75")
			case climateSuite:
				assert.Equal(t, 72.0, low, "Suite low should shift from 69 to 72")
				assert.Equal(t, 75.0, high, "Suite high should shift from 72 to 75")
			}
		}
	}
}

func TestThermalBattery_HeatCoolMode_HotOutdoor(t *testing.T) {
	t.Parallel()
	// Summer scenario: 95°F outside, 69/72 setpoints → should shift DOWN to 66/69
	env := setupHeatCoolEnv(t, "95.0")

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	require.NoError(t, err)
	defer ls.Stop()

	env.MockHA.ClearServiceCalls()

	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	require.NoError(t, err)

	assert.True(t, ls.IsThermalBatteryActive())

	// Hot outside → shift band DOWN (pre-cool): 69/72 → 66/69
	calls := env.MockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			entityID, _ := call.Data["entity_id"].(string)
			low, _ := call.Data["target_temp_low"].(float64)
			high, _ := call.Data["target_temp_high"].(float64)

			switch entityID {
			case climateHouse:
				assert.Equal(t, 66.0, low, "House low should shift from 69 to 66")
				assert.Equal(t, 69.0, high, "House high should shift from 72 to 69")
			case climateSuite:
				assert.Equal(t, 66.0, low, "Suite low should shift from 69 to 66")
				assert.Equal(t, 69.0, high, "Suite high should shift from 72 to 69")
			}
		}
	}
}

func TestThermalBattery_HeatCoolMode_MildOutdoor_SkipsInSkipZone(t *testing.T) {
	t.Parallel()
	// Spring scenario: 70°F outside, 69/72 setpoints → skip zone is 59-82°F → should skip
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
