package integration

import (
	"context"
	"testing"

	"homeautomation/internal/alert"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// ============================================================================
// Guest Asleep Scenarios
//
// User story:
//   "When a guest is asleep, the Soundbar stays muted so music does not wake
//    them. When we have no guest, the Soundbar plays with the rest of the
//    house, even while I am still in bed."
//
// The Soundbar's mute rule is leave_muted_if isGuestAsleep=true
// (TestScenario_MusicSoundbarFollowsKitchen pins that rule). So the Soundbar
// is audible exactly when isGuestAsleep is false.
//
// INVARIANTS:
//   - With no guests (isHaveGuests=false), isGuestAsleep is always false.
//   - With guests, isGuestAsleep is set by the guest bedroom door (or by hand).
//   - When the guests leave, isGuestAsleep clears.
//   - isEveryoneAsleep with no guests equals isMasterAsleep.
//   - isEveryoneAsleep with guests needs both the master and the guest asleep.
// ============================================================================

// setupGuestAsleepTest runs computed state and statetracking the way production does.
func setupGuestAsleepTest(t *testing.T) (*MockHAServer, *state.Manager, func()) {
	t.Helper()
	server, client, manager, baseCleanup := setupTest(t)
	require.NoError(t, manager.SetupComputedStateV2())

	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, manager, "isNickHome", true)

	tracking := statetracking.NewManager(context.Background(), client, manager, testlogger.New(),
		false, nil, "", &alert.MockAlerter{})
	require.NoError(t, tracking.Start())
	waitForProcessing(t, manager)

	return server, manager, func() {
		tracking.Stop()
		baseCleanup()
	}
}

func TestScenario_GuestAsleep_NoGuestsMasterAsleepLeavesSoundbarAudible(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupGuestAsleepTest(t)
	defer cleanup()

	t.Log("GIVEN: no guests, and the master is awake")
	server.SetState("input_boolean.have_guests", "off", nil)
	server.SetState("input_boolean.master_asleep", "off", nil)
	waitForBoolState(t, manager, "isMasterAsleep", false)

	t.Log("WHEN: the master goes to sleep")
	server.SetState("input_boolean.master_asleep", "on", nil)
	waitForBoolState(t, manager, "isMasterAsleep", true)

	t.Log("THEN: everyone counts as asleep, because the master is the only sleeper")
	waitForBoolState(t, manager, "isEveryoneAsleep", true,
		"with no guests, isEveryoneAsleep must follow isMasterAsleep")
	waitForServerState(t, server, "input_boolean.everyone_asleep", "on")

	t.Log("AND: no guest counts as asleep, so the Soundbar mute rule does not fire")
	requireStaysFalse(t, manager, "isGuestAsleep",
		"with no guests, isGuestAsleep must stay false so the Soundbar plays")
	waitForServerState(t, server, "input_boolean.guest_asleep", "off")
}

func TestScenario_GuestAsleep_GuestDoorClosesMutesThenGuestsLeaveUnmutes(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupGuestAsleepTest(t)
	defer cleanup()

	t.Log("GIVEN: guests are here, the master is awake, the guest door is open")
	server.SetState("input_boolean.master_asleep", "off", nil)
	server.SetState("input_boolean.have_guests", "on", nil)
	server.SetState("input_boolean.guest_bedroom_door_open", "on", nil)
	waitForBoolState(t, manager, "isHaveGuests", true)
	waitForBoolState(t, manager, "isGuestBedroomDoorOpen", true)

	t.Log("WHEN: the master goes to sleep, and the guest is still up")
	server.SetState("input_boolean.master_asleep", "on", nil)
	waitForBoolState(t, manager, "isMasterAsleep", true)

	t.Log("THEN: the guest does not count as asleep, and not everyone is asleep")
	requireStaysFalse(t, manager, "isGuestAsleep", "the guest is still up")
	requireStaysFalse(t, manager, "isEveryoneAsleep", "the guest is still up")

	t.Log("WHEN: the guest closes the bedroom door")
	server.SetState("input_boolean.guest_bedroom_door_open", "off", nil)

	t.Log("THEN: the guest counts as asleep (Soundbar mutes), and everyone is asleep")
	waitForBoolState(t, manager, "isGuestAsleep", true)
	waitForBoolState(t, manager, "isEveryoneAsleep", true)

	t.Log("WHEN: the guests leave")
	server.SetState("input_boolean.have_guests", "off", nil)

	t.Log("THEN: no guest counts as asleep (Soundbar unmutes), and the master still sleeps")
	waitForBoolState(t, manager, "isGuestAsleep", false,
		"when the guests leave, isGuestAsleep must clear")
	waitForServerState(t, server, "input_boolean.guest_asleep", "off")
	waitForBoolState(t, manager, "isEveryoneAsleep", true,
		"with no guests, isEveryoneAsleep must follow isMasterAsleep")
}
