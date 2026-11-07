package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/plugins/roompresence"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Room Presence Scenario Tests
//
// Frigate person events reach the plugin as MQTT messages through Home Assistant's mqtt/subscribe
// command, as in production. The house is the made-up example from docs/architecture/ROOM_PRESENCE.md.
// ============================================================================

func setupRoomPresenceScenario(t *testing.T, cfgFn ...func(*roompresence.Config)) (*MockHAServer, *roompresence.Manager, func()) {
	t.Helper()
	server, client, stateMgr, baseCleanup := setupTest(t)

	cfg, err := roompresence.LoadConfig(filepath.Join("..", "..", "internal", "plugins", "roompresence",
		"testdata", "example_config.yaml"))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	cfg.Mode = roompresence.ModeActive
	for _, fn := range cfgFn {
		fn(cfg)
	}

	mgr := roompresence.NewManager(context.Background(), client, stateMgr, cfg, "", testlogger.New(), false, nil)
	require.NoError(t, mgr.Start())
	require.Eventually(t, func() bool { return server.MQTTSubscriptionCount() == 2 }, 5*time.Second,
		10*time.Millisecond, "plugin subscribes to frigate/events and the camera heartbeats")

	return server, mgr, func() {
		mgr.Stop()
		baseCleanup()
	}
}

func publishFrigate(t *testing.T, server *MockHAServer, typ, id, cam string, entered []string, current ...string) {
	t.Helper()
	payload, err := json.Marshal(map[string]interface{}{
		"type": typ,
		"after": map[string]interface{}{
			"id": id, "camera": cam, "label": "person",
			"entered_zones": entered, "current_zones": current, "false_positive": false,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, server.PublishMQTT("frigate/events", string(payload)))
}

// GIVEN nobody is in the kitchen
// WHEN I walk in from the hall and stand still at the counter until Frigate loses me
// THEN isKitchenOccupied turns on and stays on
func TestScenario_RoomPresence_WalkInAndSitStill(t *testing.T) {
	server, mgr, cleanup := setupRoomPresenceScenario(t)
	defer cleanup()

	t.Log("GIVEN: the kitchen is empty")
	server.SetState("input_boolean.kitchen_occupied", "off", nil)

	t.Log("WHEN: a person comes out of the hall doorway and into the kitchen")
	publishFrigate(t, server, "new", "a", "cam_kitchen", []string{})
	publishFrigate(t, server, "update", "a", "cam_kitchen", []string{"kitchen_to_hall"}, "kitchen_to_hall")
	publishFrigate(t, server, "update", "a", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen")
	publishFrigate(t, server, "end", "a", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen")

	t.Log("THEN: isKitchenOccupied is written on")
	waitForServerState(t, server, "input_boolean.kitchen_occupied", "on")
	assert.True(t, mgr.Occupied("kitchen"), "the token stays when the track ends in a room zone")
}

// GIVEN one person is home and in the study
// WHEN Frigate reports a person at a fixed spot in the empty kitchen
// THEN isKitchenOccupied stays off
func TestScenario_RoomPresence_FalsePersonDoesNotOccupyKitchen(t *testing.T) {
	server, mgr, cleanup := setupRoomPresenceScenario(t, func(cfg *roompresence.Config) {
		cfg.HandoffWindow = 100 * time.Millisecond
	})
	defer cleanup()

	t.Log("GIVEN: one person is home, and walked into the study")
	server.SetState("input_boolean.kitchen_occupied", "off", nil)
	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.caroline_home", "off", nil)
	server.SetState("input_boolean.assistant_here", "off", nil)
	server.SetState("input_boolean.have_guests", "off", nil)
	require.Eventually(t, func() bool {
		s := mgr.GetShadowState()
		return s.Inputs.Current["isCarolineHome"] == false && s.Inputs.Current["isNickHome"] == true
	}, 5*time.Second, 10*time.Millisecond)
	publishFrigate(t, server, "update", "p", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	publishFrigate(t, server, "end", "p", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	require.Eventually(t, func() bool { return mgr.Occupied("study") }, 5*time.Second, 10*time.Millisecond)

	t.Log("WHEN: a person appears from nowhere in the kitchen, after the handoff window")
	time.Sleep(200 * time.Millisecond) // outside the 100 ms hand-off window
	for i := 0; i < 5; i++ {
		publishFrigate(t, server, "update", "ghost", "cam_kitchen", []string{"kitchen"}, "kitchen")
	}

	t.Log("THEN: the kitchen is not occupied")
	require.Eventually(t, func() bool { return !mgr.Occupied("kitchen") }, time.Second, 10*time.Millisecond)
	assert.False(t, mgr.Occupied("kitchen"))
	assert.Equal(t, "off", server.GetState("input_boolean.kitchen_occupied").State)
	s := mgr.GetShadowState()
	require.Len(t, s.Outputs.ActiveTracks, 1)
	assert.False(t, s.Outputs.ActiveTracks[0].Confirmed)
}
