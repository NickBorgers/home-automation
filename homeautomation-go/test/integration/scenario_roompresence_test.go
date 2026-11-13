package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homeautomation/internal/plugins/roompresence"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/occupancy"

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

// GIVEN a person walked into the study, which no camera sees, and the plugin keeps tokens in a helper
// WHEN the plugin restarts, as it does after an image update
// THEN the study is still occupied, and a seen area's token is not brought back
func TestScenario_RoomPresence_RestartKeepsUnseenTokens(t *testing.T) {
	server, client, stateMgr, baseCleanup := setupTest(t)
	defer baseCleanup()
	server.SetState("input_text.room_presence_tokens", "", map[string]interface{}{})

	cfg, err := roompresence.LoadConfig(filepath.Join("..", "..", "internal", "plugins", "roompresence",
		"testdata", "example_config.yaml"))
	require.NoError(t, err)
	cfg.TokenStore = "input_text.room_presence_tokens"
	start := func() *roompresence.Manager {
		mgr := roompresence.NewManager(context.Background(), client, stateMgr, cfg, "", testlogger.New(), false, nil)
		require.NoError(t, mgr.Start())
		return mgr
	}

	t.Log("GIVEN: one person walks from the lounge into the study, another stands in the kitchen")
	mgr := start()
	require.Eventually(t, func() bool { return server.MQTTSubscriptionCount() == 2 }, 5*time.Second, 10*time.Millisecond)
	publishFrigate(t, server, "update", "p", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	publishFrigate(t, server, "end", "p", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	publishFrigate(t, server, "update", "k", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen")
	require.Eventually(t, func() bool { return mgr.Occupied("study") && mgr.Occupied("kitchen") },
		5*time.Second, 10*time.Millisecond)

	t.Log("WHEN: the plugin stops and starts again")
	mgr.Stop()
	mgr = start()
	defer mgr.Stop()

	t.Log("THEN: the study token is back; the kitchen waits for its camera")
	require.Eventually(t, func() bool { return mgr.Occupied("study") }, 5*time.Second, 10*time.Millisecond,
		"the person in the unseen study is not forgotten")
	assert.False(t, mgr.Occupied("kitchen"), "seen areas are not kept: the camera reports the person again")
	toks := mgr.GetShadowState().Outputs.Tokens
	require.Len(t, toks, 1)
	assert.Equal(t, "study", toks[0].Area)
}

// GIVEN a person has been in the study for a while, so the token store has not changed lately
// WHEN the plugin is killed without a clean stop, and starts again
// THEN the study is still occupied, because the plugin kept the store's save time fresh
func TestScenario_RoomPresence_KilledProcessKeepsUnseenTokens(t *testing.T) {
	server, client, stateMgr, baseCleanup := setupTest(t)
	defer baseCleanup()
	const store = "input_text.room_presence_tokens"
	server.SetState(store, "", map[string]interface{}{})

	cfg, err := roompresence.LoadConfig(filepath.Join("..", "..", "internal", "plugins", "roompresence",
		"testdata", "example_config.yaml"))
	require.NoError(t, err)
	cfg.TokenStore = store
	cfg.TokenStoreMaxAge = 3 * time.Second // stands in for 15 min; the save time refreshes every 1 s
	start := func() *roompresence.Manager {
		mgr := roompresence.NewManager(context.Background(), client, stateMgr, cfg, "", testlogger.New(), false, nil)
		require.NoError(t, mgr.Start())
		return mgr
	}

	t.Log("GIVEN: a person walked into the study, and nothing changed for longer than token_store_max_age")
	mgr := start()
	require.Eventually(t, func() bool { return server.MQTTSubscriptionCount() == 2 }, 5*time.Second, 10*time.Millisecond)
	publishFrigate(t, server, "update", "p", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	publishFrigate(t, server, "end", "p", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	require.Eventually(t, func() bool { return mgr.Occupied("study") }, 5*time.Second, 10*time.Millisecond)
	// Wait for the initial settled save (storeSettle = 10 s), then for one periodic refresh
	// (every max_age/3 = 1 s). Polling is used because the store state is directly observable.
	// The plugin writes an empty list at startup, so wait for a save that holds the study token.
	var firstSave string
	require.Eventually(t, func() bool {
		s := server.GetState(store).State
		if strings.Contains(s, "study") {
			firstSave = s
			return true
		}
		return false
	}, 15*time.Second, 100*time.Millisecond, "waiting for token to settle and be saved")
	require.Eventually(t, func() bool {
		return server.GetState(store).State != firstSave
	}, 5*time.Second, 100*time.Millisecond, "waiting for periodic store refresh")

	t.Log("WHEN: the process is killed, so it never writes the store on stop, and starts again")
	beforeKill := server.GetState(store).State
	mgr.Stop()
	server.SetState(store, beforeKill, map[string]interface{}{}) // undo the stop save: a kill makes none
	require.Eventually(t, func() bool {
		s, err := client.GetState(store)
		return err == nil && s.State == beforeKill
	}, 5*time.Second, 10*time.Millisecond)
	mgr = start()
	defer mgr.Stop()

	t.Log("THEN: the study token is back")
	require.Eventually(t, func() bool { return mgr.Occupied("study") }, 5*time.Second, 10*time.Millisecond,
		"a killed process must not forget the person in the study")
}

// setupRoomPresenceWithHub starts the plugin with an occupancy hub, as production does. The vacuum
// scheduler shares the robot's area through the same hub.
func setupRoomPresenceWithHub(t *testing.T) (*MockHAServer, *roompresence.Manager, *occupancy.Hub, func()) {
	t.Helper()
	server, client, stateMgr, baseCleanup := setupTest(t)
	cfg, err := roompresence.LoadConfig(filepath.Join("..", "..", "internal", "plugins", "roompresence",
		"testdata", "example_config.yaml"))
	require.NoError(t, err)
	hub := occupancy.NewHub()
	mgr := roompresence.NewManager(context.Background(), client, stateMgr, cfg, "", testlogger.New(), false, nil)
	mgr.SetOccupancy(hub)
	require.NoError(t, mgr.Start())
	require.Eventually(t, func() bool { return server.MQTTSubscriptionCount() == 2 }, 5*time.Second, 10*time.Millisecond)
	return server, mgr, hub, func() {
		mgr.Stop()
		baseCleanup()
	}
}

// GIVEN the robot vacuum is cleaning the lounge, and nobody is in the lounge or the study
// WHEN Frigate takes the robot for a person: a track appears from nowhere in the lounge and ends in the
// study doorway
// THEN nobody is put in the lounge or the study
func TestScenario_RoomPresence_VacuumIsNotAPerson(t *testing.T) {
	server, mgr, hub, cleanup := setupRoomPresenceWithHub(t)
	defer cleanup()

	t.Log("GIVEN: the robot vacuum is in the lounge")
	hub.SetVacuum("lounge")

	t.Log("WHEN: a track appears from nowhere in the lounge, drives around, and ends in the study doorway")
	publishFrigate(t, server, "new", "robot", "cam_lounge", []string{}, "lounge")
	publishFrigate(t, server, "update", "robot", "cam_lounge", []string{"lounge"}, "lounge")
	publishFrigate(t, server, "update", "robot", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	publishFrigate(t, server, "end", "robot", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")

	t.Log("THEN: the track is dropped, and no area holds a token")
	require.Eventually(t, func() bool {
		r := mgr.GetShadowState().Outputs.RecentTracks
		return len(r) == 1 && r[0].ID == "robot"
	}, 5*time.Second, 10*time.Millisecond)
	s := mgr.GetShadowState().Outputs
	assert.Empty(t, s.Tokens, "the robot is not a person")
	assert.False(t, s.RecentTracks[0].Confirmed)
	assert.Contains(t, s.RecentTracks[0].Outcome, "robot vacuum")
	occ, known := hub.Occupied("study")
	assert.True(t, known)
	assert.False(t, occ)
}

// GIVEN the robot vacuum is cleaning the lounge, and a person stands in the kitchen
// WHEN the person walks from the kitchen into the lounge
// THEN the lounge is occupied, so the vacuum scheduler sees the person and stops the run
func TestScenario_RoomPresence_PersonWalksInWhereVacuumIs(t *testing.T) {
	server, mgr, hub, cleanup := setupRoomPresenceWithHub(t)
	defer cleanup()

	t.Log("GIVEN: a person came into the kitchen from the hall, and the robot vacuum is in the lounge")
	publishFrigate(t, server, "update", "k", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen")
	require.Eventually(t, func() bool { return mgr.Occupied("kitchen") }, 5*time.Second, 10*time.Millisecond)
	hub.SetVacuum("lounge")

	t.Log("WHEN: the lounge camera picks the person up while the kitchen camera still follows them")
	publishFrigate(t, server, "update", "l", "cam_lounge", []string{"lounge"}, "lounge")

	t.Log("THEN: the person's token moves into the lounge")
	require.Eventually(t, func() bool {
		occ, known := hub.Occupied("lounge")
		return occ && known
	}, 5*time.Second, 10*time.Millisecond, "a person who walks in where the robot is must still be seen")
	assert.False(t, mgr.Occupied("kitchen"))
}
