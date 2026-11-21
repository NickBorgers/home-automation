package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/plugins/vacuum"
	"homeautomation/internal/testlogger"
	"homeautomation/internal/tts"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Announcement Speaker List
//
// User story:
//   "Every spoken announcement plays on the same speakers, wherever I am in
//    the house. When I add a speaker, I add it in one place."
//
// INVARIANTS:
//   - Announcements that do not need a special speaker set play on
//     notification.default_speakers from configs/notification_config.yaml.
//   - No plugin carries its own copy of that list.
//   - The vacuum "You have satisfied Casper" confirmation stays on the Sitting
//     speaker only.
//   - During an early wake, a deferable announcement skips both speakers of
//     the primary suite (Primary Bed and Primary Bath).
//
// The test reads the real config files, so a change to either list shows here.
// ============================================================================

// repoConfigDir is the repository's configs directory, relative to this package.
const repoConfigDir = "../../../configs"

// announcementSpeakers is the one shared list in configs/notification_config.yaml.
var announcementSpeakers = []string{
	"media_player.lounge",
	"media_player.sitting",
	"media_player.primary_bed",
	"media_player.kids_bathroom",
	"media_player.primary_bath",
	"media_player.utility",
	"media_player.kitchen",
	"media_player.pantry",
}

func TestScenario_AnnouncementSpeakers_ConfigListsTheWholeHouse(t *testing.T) {
	t.Parallel()
	cfg, err := notify.LoadConfig(filepath.Join(repoConfigDir, "notification_config.yaml"))
	require.NoError(t, err)
	assert.ElementsMatch(t, announcementSpeakers, cfg.DefaultSpeakers,
		"default_speakers must be the one whole-house announcement list")
	assert.ElementsMatch(t, []string{"media_player.primary_bed", "media_player.primary_bath"}, cfg.BedroomSpeakers)
}

func TestScenario_AnnouncementSpeakers_EarlyWakeSkipsPrimarySuite(t *testing.T) {
	t.Parallel()
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t.Log("GIVEN: the real notification config, the bedroom asleep, someone up early")
	notifyCfg, err := notify.LoadConfig(filepath.Join(repoConfigDir, "notification_config.yaml"))
	require.NoError(t, err)
	synth := &tts.MockSynthesizer{URL: "http://test/audio/early.mp3"}
	notifier := notify.NewManager(client, manager, synth, *notifyCfg, testlogger.New(), false)
	require.NoError(t, manager.SetBool("isMasterAsleep", true))
	require.NoError(t, manager.SetBool("isEarlyWakeActive", true))
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: a deferable announcement arrives")
	require.NoError(t, notifier.Announce(context.Background(), "Casper needs attention"))

	t.Log("THEN: it plays on the shared list minus Primary Bed and Primary Bath")
	waitForServiceCallSince(t, server, snapshot, "media_player", "play_media")
	calls := FilterServiceCalls(server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	require.Len(t, calls, 1)
	assert.ElementsMatch(t, []string{
		"media_player.lounge",
		"media_player.sitting",
		"media_player.kids_bathroom",
		"media_player.utility",
		"media_player.kitchen",
		"media_player.pantry",
	}, toStringSlice(calls[0].ServiceData["entity_id"]),
		"neither primary suite speaker may play during an early wake")
}

func TestScenario_AnnouncementSpeakers_VacuumErrorUsesSharedList(t *testing.T) {
	t.Parallel()
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t.Log("GIVEN: the real vacuum and notification configs, master awake, someone home")
	cfg, err := vacuum.LoadConfig(filepath.Join(repoConfigDir, "vacuum_config.yaml"))
	require.NoError(t, err)
	cfg.Vacuum.ErrorSensorID = vacuumErrorEntity
	server.SetState(vacuumErrorEntity, "No error", nil)
	require.NoError(t, manager.SetBool("isMasterAsleep", false))
	require.NoError(t, manager.SetBool("isAnyoneHome", true))

	notifyCfg, err := notify.LoadConfig(filepath.Join(repoConfigDir, "notification_config.yaml"))
	require.NoError(t, err)
	synth := &tts.MockSynthesizer{URL: "http://test/audio/vacuum.mp3"}
	logger := testlogger.New()
	notifier := notify.NewManager(client, manager, synth, *notifyCfg, logger, false)
	alerter := alert.NewManager(ntfy.NewMockClient(), notifier, logger)
	tp := plugin.FixedTimeProvider{FixedTime: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)}
	mgr := vacuum.NewManager(context.Background(), client, manager, alerter, notifier, cfg, logger, false, tp, nil)
	mgr.SetRepeatCheckIntervalForTest(time.Hour)
	require.NoError(t, mgr.Start())
	defer mgr.Stop()
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Casper reports an error")
	server.SetState(vacuumErrorEntity, "Mop Dock Clean Water Tank empty", nil)

	t.Log("THEN: the error is announced on the shared speaker list")
	waitForServiceCallSince(t, server, snapshot, "media_player", "play_media")
	calls := FilterServiceCalls(server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	require.Len(t, calls, 1)
	assert.ElementsMatch(t, announcementSpeakers, toStringSlice(calls[0].ServiceData["entity_id"]))
	snapshot = server.ServiceCallCount()

	t.Log("WHEN: the error clears")
	server.SetState(vacuumErrorEntity, "No error", nil)

	t.Log("THEN: the clear confirmation plays on the Sitting speaker only")
	waitForServiceCallSince(t, server, snapshot, "media_player", "play_media")
	calls = FilterServiceCalls(server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"media_player.sitting"}, toStringSlice(calls[0].ServiceData["entity_id"]))
}

func TestScenario_AnnouncementSpeakers_ArrivalUsesSharedList(t *testing.T) {
	t.Parallel()
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t.Log("GIVEN: Nick is home and awake, Caroline is away")
	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.caroline_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", true)
	require.NoError(t, manager.SetBool("isMasterAsleep", false))

	notifyCfg, err := notify.LoadConfig(filepath.Join(repoConfigDir, "notification_config.yaml"))
	require.NoError(t, err)
	logger := testlogger.New()
	synth := &tts.MockSynthesizer{URL: "http://test/audio/arrival.mp3"}
	notifier := notify.NewManager(client, manager, synth, *notifyCfg, logger, false)
	alerter := alert.NewManager(ntfy.NewMockClient(), notifier, logger)
	tracking := statetracking.NewManager(context.Background(), client, manager, logger, false, nil, "", alerter)
	require.NoError(t, tracking.Start())
	defer tracking.Stop()
	waitForProcessing(t, manager)
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Caroline arrives home")
	server.SetState("input_boolean.caroline_home", "on", nil)

	t.Log("THEN: her arrival is announced on the shared speaker list")
	waitForServiceCallSince(t, server, snapshot, "media_player", "play_media")
	calls := FilterServiceCalls(server.GetServiceCallsSince(snapshot), "media_player", "play_media")
	require.Len(t, calls, 1)
	assert.ElementsMatch(t, announcementSpeakers, toStringSlice(calls[0].ServiceData["entity_id"]))
	assert.Equal(t, []string{"Caroline is home"}, synth.Messages())
}
