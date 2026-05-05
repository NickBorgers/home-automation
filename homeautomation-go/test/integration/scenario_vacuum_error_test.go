package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/notify"
	"homeautomation/internal/plugins/vacuum"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/internal/tts"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// Vacuum Error Announcement Tests
//
// User story:
//   "When my robot vacuum reports an actionable error (e.g. 'Mop Dock Clean
//    Water Tank empty'), I want a spoken TTS announcement on common-area
//    speakers so I can address it. While I'm asleep, suppress the
//    announcement. Re-announce every 2 hours until the error clears."
//
// INVARIANTS:
//   - "No error" must NEVER trigger a TTS call.
//   - When master is asleep (vacuum uses UrgencyDeferable), the
//     error must be tracked but NOT spoken.
//   - The same error string re-emitted by HA must NOT cause an immediate
//     duplicate announcement (the repeat timer governs cadence).
//   - After the error clears, no further announcements occur.
// ============================================================================

const vacuumErrorEntity = "sensor.valetudo_test_error"

type vacuumEnv struct {
	server  *MockHAServer
	manager *state.Manager
	logger  *zap.Logger
	vacuum  *vacuum.Manager
	synth   *tts.MockSynthesizer
}

func setupVacuumTest(t *testing.T, fixedTime time.Time) (*vacuumEnv, func()) {
	t.Helper()
	server, client, manager, baseCleanup := setupTest(t)
	logger := testlogger.New()

	// Pre-seed the sensor with the healthy "No error" state so the plugin's
	// initial GetState call sees it.
	server.SetState(vacuumErrorEntity, "No error", nil)

	cfg := &vacuum.Config{}
	cfg.Vacuum.ErrorSensorID = vacuumErrorEntity
	cfg.Vacuum.NoErrorValue = "No error"
	cfg.Vacuum.Announcement.MessagePrefix = "Robot vacuum needs attention"
	cfg.Vacuum.Announcement.RepeatInterval = 2 * time.Hour
	cfg.Vacuum.Announcement.Speakers = []string{
		"media_player.kitchen",
		"media_player.sitting_room",
		"media_player.front_room",
		"media_player.kids_bathroom",
	}

	tp := plugin.FixedTimeProvider{FixedTime: fixedTime}
	// Use a real notifier with a fake TTS synthesizer so the resulting
	// media_player.play_media service call flows through the mock HA server
	// (where the test asserts it). The synth records the spoken text so the
	// test can verify the message content without parsing the HA call.
	notifyCfg := notify.DefaultConfig()
	synth := &tts.MockSynthesizer{URL: "http://test/audio/vacuum.mp3"}
	notifier := notify.NewManager(client, manager, synth, notifyCfg, logger, false)
	alerter := alert.NewManager(nil, notifier, logger)
	mgr := vacuum.NewManager(context.Background(), client, manager, alerter, cfg, logger, false, tp, nil)
	mgr.SetRepeatCheckIntervalForTest(time.Hour) // park the background ticker; tests drive ticks explicitly
	require.NoError(t, mgr.Start(), "vacuum plugin should start")

	env := &vacuumEnv{server: server, manager: manager, logger: logger, vacuum: mgr, synth: synth}

	cleanup := func() {
		mgr.Stop()
		baseCleanup()
	}
	return env, cleanup
}

func vacuumTTSCalls(server *MockHAServer) []ServiceCall {
	return FilterServiceCalls(server.GetServiceCalls(), "media_player", "play_media")
}

// TestScenario_VacuumError_AnnouncesWhenErrorAppears verifies that a transition
// from "No error" to a real error produces a TTS announcement on the configured
// speakers with a message that includes the error description.
func TestScenario_VacuumError_AnnouncesWhenErrorAppears(t *testing.T) {
	t.Parallel()
	env, cleanup := setupVacuumTest(t, time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC))
	defer cleanup()

	t.Log("GIVEN: Vacuum sensor reports 'No error', master is awake")
	require.NoError(t, env.manager.SetBool("isMasterAsleep", false))

	t.Log("WHEN: Sensor flips to 'Mop Dock Clean Water Tank empty'")
	env.server.SetState(vacuumErrorEntity, "Mop Dock Clean Water Tank empty", nil)

	t.Log("THEN: A TTS announcement is sent within stateWaitTimeout")
	require.Eventually(t, func() bool {
		return len(vacuumTTSCalls(env.server)) >= 1
	}, stateWaitTimeout, statePollInterval, "expected at least one media_player.play_media call")

	calls := vacuumTTSCalls(env.server)
	require.NotEmpty(t, calls)
	assert.Equal(t, "music", calls[0].ServiceData["media_content_type"])
	assert.Equal(t, true, calls[0].ServiceData["announce"])
	assert.Equal(t, "http://test/audio/vacuum.mp3", calls[0].ServiceData["media_content_id"])

	msgs := env.synth.Messages()
	require.NotEmpty(t, msgs, "synthesizer should have been called")
	assert.Contains(t, msgs[0], "Mop Dock Clean Water Tank empty",
		"synthesized text must include the error description")
}

// TestScenario_VacuumError_SuppressedWhileMasterAsleep verifies that an error
// fired while master is asleep does NOT speak, but is recorded in shadow state
// so dashboards and morning announcements can pick it up.
func TestScenario_VacuumError_SuppressedWhileMasterAsleep(t *testing.T) {
	t.Parallel()
	env, cleanup := setupVacuumTest(t, time.Date(2026, 4, 27, 3, 0, 0, 0, time.UTC))
	defer cleanup()

	t.Log("GIVEN: Master is asleep, sensor at 'No error'")
	require.NoError(t, env.manager.SetBool("isMasterAsleep", true))
	waitForBoolState(t, env.manager, "isMasterAsleep", true,
		"isMasterAsleep should be true before sensor change")

	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: Sensor flips to a real error")
	env.server.SetState(vacuumErrorEntity, "Dustbin missing", nil)

	t.Log("THEN: Plugin recognizes the error in shadow state...")
	require.Eventually(t, func() bool {
		return env.vacuum.GetShadowState().Outputs.CurrentError == "Dustbin missing"
	}, stateWaitTimeout, statePollInterval,
		"shadow state should reflect the active error")

	t.Log("...but no media_player.play_media service call is sent.")
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 200*time.Millisecond)
	calls := FilterServiceCalls(env.server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	assert.Empty(t, calls, "TTS must not fire while master is asleep")

	st := env.vacuum.GetShadowState()
	assert.GreaterOrEqual(t, st.Outputs.SuppressedWhileAsleepCount, 1,
		"shadow state should record the suppression")
}

// TestScenario_VacuumError_StopsRepeatingAfterClear verifies the repeat timer
// stops once the error clears: ticking the repeat-check after the error has
// returned to "No error" must not produce additional announcements.
func TestScenario_VacuumError_StopsRepeatingAfterClear(t *testing.T) {
	t.Parallel()
	env, cleanup := setupVacuumTest(t, time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC))
	defer cleanup()

	require.NoError(t, env.manager.SetBool("isMasterAsleep", false))

	t.Log("GIVEN: An active error was announced")
	env.server.SetState(vacuumErrorEntity, "Dustbin missing", nil)
	require.Eventually(t, func() bool {
		return len(vacuumTTSCalls(env.server)) >= 1
	}, stateWaitTimeout, statePollInterval)

	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: The error clears")
	env.server.SetState(vacuumErrorEntity, "No error", nil)
	require.Eventually(t, func() bool {
		return env.vacuum.GetShadowState().Outputs.CurrentError == ""
	}, stateWaitTimeout, statePollInterval, "shadow state should clear the error")

	t.Log("THEN: Subsequent repeat ticks must not produce a new announcement")
	env.vacuum.TickRepeatForTest()
	newCalls := FilterServiceCalls(env.server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	assert.Empty(t, newCalls, "no announcements should fire after the error has cleared")
}
