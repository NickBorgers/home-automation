package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Wake By Master Bedroom Door Scenarios
//
// User story:
//   "When I open the bedroom door in the morning, the whole house should wake
//    up with me. I should not have to tell the house I am up."
//
// A contact sensor on the master bedroom door (a binary_sensor, on = open) is
// named in state_tracking_config.yaml. Opening it clears isMasterAsleep, which
// lets isAnyoneAsleep and the awake latch follow. Other plugins react to those.
//
// INVARIANTS:
//   - The door wakes the house only in the morning day phase.
//   - The door wakes the house only on a real closed -> open change. A sensor
//     coming back from unavailable (restart, radio gap) never wakes it.
//   - The door never puts anyone to sleep.
//   - With no sensor configured, the plugin does nothing with doors.
// ============================================================================

const wakeDoorSensor = "binary_sensor.example_master_bedroom_door"

type wakeDoorEnv struct {
	server       *MockHAServer
	client       *ha.Client
	stateManager *state.Manager
	tracking     *statetracking.Manager
}

// setupWakeDoorTest starts state tracking with the door sensor configured (or not).
// The house starts with the owner home, the master bedroom asleep and the door closed.
func setupWakeDoorTest(t *testing.T, dayPhase, sensor string) (*wakeDoorEnv, func()) {
	t.Helper()
	logger := testlogger.New()

	server := NewMockHAServer(testAddr, testToken)
	server.InitializeStates()
	require.NoError(t, server.Start())

	server.SetState("light.primary_suite", "off", map[string]interface{}{})
	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_text.day_phase", dayPhase, nil)
	server.SetState(wakeDoorSensor, "off", map[string]interface{}{})

	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", server.Addr()), testToken, logger)
	require.NoError(t, client.Connect())

	manager := state.NewManager(client, logger, false)
	require.NoError(t, manager.SyncFromHA())
	require.NoError(t, manager.SetupComputedStateV2())

	tracking := statetracking.NewManager(context.Background(), client, manager, logger, false, nil, "", &alert.MockAlerter{})
	tracking.SetMasterBedroomDoorSensor(sensor)
	require.NoError(t, tracking.Start())
	waitForProcessing(t, manager)

	waitForBoolState(t, manager, "isAnyoneAsleep", true, "house should start asleep")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "house should start asleep")

	cleanup := func() {
		tracking.Stop()
		client.Disconnect()
		server.Stop()
	}
	return &wakeDoorEnv{server: server, client: client, stateManager: manager, tracking: tracking}, cleanup
}

func requireStillAsleep(t *testing.T, env *wakeDoorEnv, msg string) {
	t.Helper()
	waitForProcessing(t, env.stateManager)
	// The mock server's event may still be in transit when the handlers drain.
	time.Sleep(200 * time.Millisecond)
	asleep, err := env.stateManager.GetBool("isMasterAsleep")
	require.NoError(t, err)
	assert.True(t, asleep, msg)
}

func TestScenario_WakeByBedroomDoor_MorningOpenWakesWholeHouse(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeDoorTest(t, "morning", wakeDoorSensor)
	defer cleanup()

	t.Log("GIVEN: it is morning, the master bedroom is asleep, the door is closed")

	t.Log("WHEN: the master bedroom door opens")
	env.server.SetState(wakeDoorSensor, "on", map[string]interface{}{})

	t.Log("THEN: the master bedroom is awake and the whole house follows")
	waitForBoolState(t, env.stateManager, "isMasterAsleep", false, "opening the door in the morning must clear isMasterAsleep")
	waitForServerState(t, env.server, "input_boolean.master_asleep", "off", "the cleared state must reach Home Assistant")
	waitForBoolState(t, env.stateManager, "isAnyoneAsleep", false, "the house is no longer asleep")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", true, "the house counts as awake")
}

func TestScenario_WakeByBedroomDoor_OutsideMorningDoesNotWake(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"night", "winddown", "day"} {
		phase := phase
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			env, cleanup := setupWakeDoorTest(t, phase, wakeDoorSensor)
			defer cleanup()

			t.Log("WHEN: the door opens outside the morning phase (a night trip, a feeding)")
			env.server.SetState(wakeDoorSensor, "on", map[string]interface{}{})

			t.Log("THEN: the master bedroom stays asleep")
			requireStillAsleep(t, env, "a door opening outside the morning phase must not wake the house")
		})
	}
}

func TestScenario_WakeByBedroomDoor_ReappearingSensorDoesNotWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeDoorTest(t, "morning", wakeDoorSensor)
	defer cleanup()

	t.Log("GIVEN: the sensor drops off the network, then comes back reporting open")
	env.server.SetState(wakeDoorSensor, "unavailable", map[string]interface{}{})
	waitForProcessing(t, env.stateManager)
	env.server.SetState(wakeDoorSensor, "on", map[string]interface{}{})

	t.Log("THEN: no one opened a door, so the bedroom stays asleep")
	requireStillAsleep(t, env, "unavailable -> on is not a door opening")
}

func TestScenario_WakeByBedroomDoor_ClosingDoorDoesNotWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeDoorTest(t, "morning", wakeDoorSensor)
	defer cleanup()

	t.Log("GIVEN: the door is open and the bedroom is asleep again")
	env.server.SetState(wakeDoorSensor, "on", map[string]interface{}{})
	waitForBoolState(t, env.stateManager, "isMasterAsleep", false, "setup: the open wakes the bedroom")
	env.server.SetState("input_boolean.master_asleep", "on", nil)
	waitForBoolState(t, env.stateManager, "isMasterAsleep", true, "setup: bedroom asleep again")

	t.Log("WHEN: the door closes")
	env.server.SetState(wakeDoorSensor, "off", map[string]interface{}{})

	t.Log("THEN: closing the door does not wake anyone")
	requireStillAsleep(t, env, "closing the door must not wake the house")
}

func TestScenario_WakeByBedroomDoor_NoSensorConfiguredIsInert(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeDoorTest(t, "morning", "")
	defer cleanup()

	t.Log("WHEN: a door sensor changes but no sensor is configured")
	env.server.SetState(wakeDoorSensor, "on", map[string]interface{}{})

	t.Log("THEN: nothing wakes")
	requireStillAsleep(t, env, "without a configured sensor the door must not wake the house")
}
