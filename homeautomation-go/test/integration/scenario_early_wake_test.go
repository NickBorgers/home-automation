package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
	"homeautomation/internal/plugins/earlywake"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/internal/tts"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Early Wake Scenarios
//
// User story:
//   "I wake up early and get up. My partner (or a guest) is still asleep.
//    When I turn on a light in the main living space, the house wakes up with
//    me: common-area lights follow their normal awake rules, morning music
//    plays on the common speakers, and announcements can be heard. The
//    bedroom stays sleep-compatible: its lights are untouched and its speaker
//    keeps the rain sounds."
//
// The wake signal is explicit: a person turns on a main-living-space light.
// A night trip (a feeding, the bathroom) that uses only path lights, or
// lights the app turned on itself, must never wake the house.
//
// How "a person turned it on" is detected: Home Assistant attaches a context
// to each state change. A change the app made carries the app's own user ID
// (from auth/current_user). A change an HA automation made carries a
// parent_id. A change from the Hue app, a wall switch, HomeKit or the HA
// dashboard has neither.
//
// INVARIANTS:
//   - isEarlyWakeActive turns on only for a manual off->on of a wake light
//     while someone is asleep.
//   - The app's own light commands never turn it on.
//   - HA automation light changes never turn it on.
//   - Lights outside the wake list (the pantry night path) never turn it on.
//   - Early wake makes isAnyoneHomeAndAwake true while isMasterAsleep stays
//     true, and never sets isWakeSequenceActive.
//   - A normal wake (isAnyoneAsleep true->false) clears isEarlyWakeActive.
// ============================================================================

const (
	appUserID    = "app-user-0001"
	personUserID = "nick-user-0002"
)

var earlyWakeLights = []string{
	"light.living",
	"light.living_accent",
	"light.sitting",
	"light.sitting_chandelier",
	"light.kitchen_cans",
	"light.kitchen_main_lights",
	"light.breakfast_nook_island_can",
	"light.breakfast_nook_pantry_can",
}

type earlyWakeEnv struct {
	server       *MockHAServer
	client       *ha.Client
	stateManager *state.Manager
	earlyWake    *earlywake.Manager
}

// setupEarlyWakeTest starts the computed state (with the wake latch) and the
// early wake plugin against a mock HA whose app user is appUserID. The house
// starts at 5 a.m.: Nick home, the bedroom asleep, every wake light off.
func setupEarlyWakeTest(t *testing.T) (*earlyWakeEnv, func()) {
	t.Helper()
	logger := testlogger.New()

	server := NewMockHAServer(testAddr, testToken)
	server.SetCurrentUserID(appUserID)
	server.InitializeStates()
	require.NoError(t, server.Start())

	for _, light := range append([]string{"light.pantry_area", "light.primary_suite"}, earlyWakeLights...) {
		server.SetState(light, "off", map[string]interface{}{})
	}
	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_text.day_phase", "night", nil)

	client := ha.NewClient(fmt.Sprintf("ws://%s/api/websocket", server.Addr()), testToken, logger)
	require.NoError(t, client.Connect())

	manager := state.NewManager(client, logger, false)
	require.NoError(t, manager.SyncFromHA())
	require.NoError(t, manager.SetupComputedStateV2())

	cfg := &earlywake.Config{WakeLights: earlyWakeLights}
	ew := earlywake.NewManager(context.Background(), client, manager, cfg, logger, false)
	require.NoError(t, ew.Start())
	waitForProcessing(t, manager)

	waitForBoolState(t, manager, "isAnyoneAsleep", true, "bedroom should start asleep")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "house should start asleep")

	env := &earlyWakeEnv{server: server, client: client, stateManager: manager, earlyWake: ew}
	cleanup := func() {
		ew.Stop()
		client.Disconnect()
		server.Stop()
	}
	return env, cleanup
}

// requireStaysFalse checks that a bool state is still false after the system had time to react.
func requireStaysFalse(t *testing.T, manager *state.Manager, key, msg string) {
	t.Helper()
	waitForProcessing(t, manager)
	time.Sleep(200 * time.Millisecond)
	got, err := manager.GetBool(key)
	require.NoError(t, err)
	assert.False(t, got, msg)
}

func TestScenario_EarlyWake_ManualLivingLightWakesHouse(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("GIVEN: 5 a.m., the bedroom is asleep, the house is dark")
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: Nick turns on the living room lights from the Hue app (no user, no parent)")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{"brightness": 128.0},
		&StateContext{ID: "ctx-hue-app"})

	t.Log("THEN: early wake is on and the house counts as awake")
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", true,
		"a manual living light turn-on while asleep must start early wake")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", true,
		"early wake must hold isAnyoneHomeAndAwake true")
	waitForServerState(t, env.server, "input_boolean.early_wake_active", "on",
		"early wake must be visible in Home Assistant")

	t.Log("AND: the bedroom is still asleep and the wake sequence never started")
	masterAsleep, err := env.stateManager.GetBool("isMasterAsleep")
	require.NoError(t, err)
	assert.True(t, masterAsleep, "early wake must not wake the bedroom")
	wakeSeq, err := env.stateManager.GetBool("isWakeSequenceActive")
	require.NoError(t, err)
	assert.False(t, wakeSeq, "early wake must not start the bedroom wake sequence")

	t.Log("AND: the early wake plugin sends no command to the primary suite lights")
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 200*time.Millisecond)
	for _, call := range env.server.GetServiceCallsSince(snapshot) {
		if call.Domain == "light" {
			assert.NotEqual(t, "light.primary_suite", call.ServiceData["entity_id"],
				"the bedroom lights must stay untouched")
		}
	}
}

func TestScenario_EarlyWake_DashboardToggleWakesHouse(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("WHEN: someone turns on input_boolean.early_wake_active by hand")
	env.server.SetState("input_boolean.early_wake_active", "on", nil)

	t.Log("THEN: the latch holds the house awake")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", true,
		"turning on early wake by hand must wake the house")
}

func TestScenario_EarlyWake_KitchenLightFromDashboardWakesHouse(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("WHEN: Nick turns on the kitchen cans from the HA dashboard (his own user ID)")
	env.server.SetStateWithContext("light.kitchen_cans", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-dashboard", UserID: personUserID})

	t.Log("THEN: early wake is on")
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", true,
		"a person's turn-on from the dashboard must start early wake")
}

func TestScenario_EarlyWake_AppCommandDoesNotWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("WHEN: the app itself turns on the living lights (a path light, lux control, a Pico)")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-app", UserID: appUserID})

	t.Log("THEN: early wake stays off")
	requireStaysFalse(t, env.stateManager, "isEarlyWakeActive", "the app's own commands must never wake the house")
	requireStaysFalse(t, env.stateManager, "isAnyoneHomeAndAwake", "the house must stay asleep")
}

func TestScenario_EarlyWake_HAAutomationDoesNotWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("WHEN: a Home Assistant automation turns on the sitting lights")
	env.server.SetStateWithContext("light.sitting", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-auto", ParentID: "ctx-automation-trigger"})

	t.Log("THEN: early wake stays off")
	requireStaysFalse(t, env.stateManager, "isEarlyWakeActive", "HA automations must never wake the house")
}

func TestScenario_EarlyWake_PantryNightPathDoesNotWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("WHEN: at 3 a.m. someone turns on the pantry lights by hand on the way to the nursery")
	env.server.SetStateWithContext("light.pantry_area", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-hue-app"})

	t.Log("THEN: early wake stays off: the pantry is the night path, not the main living space")
	requireStaysFalse(t, env.stateManager, "isEarlyWakeActive", "the pantry must not wake the house")
}

func TestScenario_EarlyWake_NotWhileEveryoneAwake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("GIVEN: the bedroom is awake")
	env.server.SetState("input_boolean.master_asleep", "off", nil)
	waitForBoolState(t, env.stateManager, "isAnyoneAsleep", false)

	t.Log("WHEN: someone turns on the living lights by hand")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-hue-app"})

	t.Log("THEN: early wake stays off: there is nobody asleep to work around")
	requireStaysFalse(t, env.stateManager, "isEarlyWakeActive", "early wake only applies while someone sleeps")
}

func TestScenario_EarlyWake_LightAlreadyOnDoesNotWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("GIVEN: the living lights are already on")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{"brightness": 10.0},
		&StateContext{ID: "ctx-app", UserID: appUserID})
	waitForProcessing(t, env.stateManager)

	t.Log("WHEN: a person changes their brightness (on -> on)")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{"brightness": 200.0},
		&StateContext{ID: "ctx-hue-app"})

	t.Log("THEN: early wake stays off: only an off -> on turn-on is the signal")
	requireStaysFalse(t, env.stateManager, "isEarlyWakeActive", "a brightness change is not a wake signal")
}

func TestScenario_EarlyWake_NormalWakeClearsIt(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("GIVEN: early wake is on")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-hue-app"})
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", true)

	t.Log("WHEN: the bedroom wakes up normally")
	env.server.SetState("input_boolean.master_asleep", "off", nil)

	t.Log("THEN: early wake clears, and the house stays awake the normal way")
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", false,
		"a normal wake must clear early wake so it cannot carry into the next night")
	waitForServerState(t, env.server, "input_boolean.early_wake_active", "off")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", true)

	t.Log("AND: the next night the house goes to sleep as usual")
	env.server.SetState("input_boolean.master_asleep", "on", nil)
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", false,
		"with early wake cleared, bedtime must put the house to sleep")
}

// A guest can count as asleep all day, so isAnyoneAsleep never falls and the
// normal-wake clear never runs. Bedtime (master_asleep turning on) must clear it.
func TestScenario_EarlyWake_BedtimeClearsItWhileGuestSleeps(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("GIVEN: a guest is asleep, and Nick starts an early wake")
	env.server.SetState("input_boolean.guest_asleep", "on", nil)
	waitForBoolState(t, env.stateManager, "isGuestAsleep", true)
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-hue-app"})
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", true)

	t.Log("AND: the primary bedroom wakes later, while the guest still sleeps")
	env.server.SetState("input_boolean.master_asleep", "off", nil)
	waitForBoolState(t, env.stateManager, "isMasterAsleep", false)
	waitForProcessing(t, env.stateManager)
	assert.True(t, mustGetBool(t, env.stateManager, "isEarlyWakeActive"),
		"while the guest sleeps, early wake keeps the common areas awake")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", true)

	t.Log("WHEN: the primary bedroom goes to bed that night")
	env.server.SetState("input_boolean.master_asleep", "on", nil)

	t.Log("THEN: early wake clears and the house goes to sleep")
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", false,
		"bedtime must clear early wake so it cannot carry into the night")
	waitForServerState(t, env.server, "input_boolean.early_wake_active", "off")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", false,
		"with early wake cleared, bedtime must put the house to sleep")
}

// Early wake turned on by hand while everyone is awake has no normal wake to clear it.
func TestScenario_EarlyWake_BedtimeClearsDashboardToggle(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()

	t.Log("GIVEN: everyone is awake, and someone turns on early wake from the dashboard")
	env.server.SetState("input_boolean.master_asleep", "off", nil)
	waitForBoolState(t, env.stateManager, "isAnyoneAsleep", false)
	// Let the "everyone woke up" handlers finish before the toggle, or they clear it.
	waitForProcessing(t, env.stateManager)
	env.server.SetState("input_boolean.early_wake_active", "on", nil)
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", true)

	t.Log("WHEN: the primary bedroom goes to bed")
	env.server.SetState("input_boolean.master_asleep", "on", nil)

	t.Log("THEN: early wake clears and the house goes to sleep")
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", false,
		"bedtime must clear early wake")
	waitForServerState(t, env.server, "input_boolean.early_wake_active", "off")
	waitForBoolState(t, env.stateManager, "isAnyoneHomeAndAwake", false,
		"with early wake cleared, bedtime must put the house to sleep")
}

func mustGetBool(t *testing.T, manager *state.Manager, key string) bool {
	t.Helper()
	v, err := manager.GetBool(key)
	require.NoError(t, err)
	return v
}

// ============================================================================
// Announcements during early wake
//
// User story: "If the vacuum needs attention while I'm up early, tell me in
// the kitchen, but don't wake my partner in the bedroom."
// ============================================================================

func setupEarlyWakeNotifier(t *testing.T, env *earlyWakeEnv) *notify.Manager {
	t.Helper()
	cfg := notify.DefaultConfig()
	cfg.DefaultSpeakers = []string{"media_player.pantry", "media_player.primary_bed", "media_player.sitting"}
	cfg.BedroomSpeakers = []string{"media_player.primary_bed"}
	synth := &tts.MockSynthesizer{URL: "http://test/audio/early.mp3"}
	notifier := notify.NewManager(env.client, env.stateManager, synth, cfg, testlogger.New(), false)
	// The bedroom speaker plays the sleep zone the whole time.
	sleepZone := &sleepPlaybackState{}
	sleepZone.active.Store(true)
	notifier.SetSleepPlaybackProvider(sleepZone)
	return notifier
}

func TestScenario_EarlyWake_DeferableAnnouncementSkipsBedroom(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()
	notifier := setupEarlyWakeNotifier(t, env)

	t.Log("GIVEN: early wake is on, the bedroom is asleep with rain sounds")
	env.server.SetStateWithContext("light.living", "on", map[string]interface{}{},
		&StateContext{ID: "ctx-hue-app"})
	waitForBoolState(t, env.stateManager, "isEarlyWakeActive", true)
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: a deferable announcement arrives")
	err := notifier.Announce(context.Background(), "Casper needs attention")

	t.Log("THEN: it plays on the common speakers and not in the bedroom")
	require.NoError(t, err, "a deferable announcement must play during early wake")
	waitForServiceCallSince(t, env.server, snapshot, "media_player", "play_media")
	calls := FilterServiceCalls(env.server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	require.Len(t, calls, 1)
	speakers := toStringSlice(calls[0].ServiceData["entity_id"])
	assert.ElementsMatch(t, []string{"media_player.pantry", "media_player.sitting"}, speakers,
		"the bedroom speaker must be left out")
}

func TestScenario_EarlyWake_DeferableAnnouncementStillSuppressedWithoutIt(t *testing.T) {
	t.Parallel()
	env, cleanup := setupEarlyWakeTest(t)
	defer cleanup()
	notifier := setupEarlyWakeNotifier(t, env)

	t.Log("GIVEN: the whole house is asleep (no early wake)")
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: a deferable announcement arrives")
	err := notifier.Announce(context.Background(), "Casper needs attention")

	t.Log("THEN: it is suppressed as before")
	assert.True(t, errors.Is(err, notify.ErrSuppressedAsleep), "deferable speech must stay suppressed, got %v", err)
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 200*time.Millisecond)
	assert.Empty(t, FilterServiceCalls(env.server.GetServiceCallsSince(snapshot), "media_player", "play_media"))
}

// toStringSlice reads an entity_id field that JSON decoding turned into []interface{}.
func toStringSlice(v interface{}) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []interface{}:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	case string:
		return []string{s}
	}
	return nil
}
