package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/music"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Music when the app restarts
//
// Production deploys restart the app many times a day. A restart must not be
// audible when the music on the speakers is already right.
//
// INVARIANTS:
// - Music this app started for the current mode keeps playing. No restart.
// - Music for the current mode that someone paused stays paused. The running
//   app does not resume a pause, so a restart must not either.
// - Music that does not belong to the current mode is replaced.
// ============================================================================

// tidalTrackOnSonos is what Sonos reports for a track from a Tidal playlist.
const tidalTrackOnSonos = "x-sonos-http:track%2f219523260.flac?sid=174&flags=24616&sn=9"

// setMusicRestartHome puts the house in a calm state with someone home and awake.
func setMusicRestartHome(server *MockHAServer, dayPhase string) {
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", dayPhase, map[string]interface{}{})
	for _, entityID := range []string{"media_player.primary_bed", "media_player.sitting_room"} {
		server.SetState(entityID, "idle", map[string]interface{}{"volume_level": 0.1})
	}
}

// setLeadSpeaker sets the lead speaker's state and the track it reports.
func setLeadSpeaker(server *MockHAServer, entityID, playbackState string) {
	server.SetState(entityID, playbackState, map[string]interface{}{
		"media_content_id":   tidalTrackOnSonos,
		"media_content_type": "music",
		"group_members":      []interface{}{entityID},
		"volume_level":       0.1,
	})
}

// speakerCommandsSince returns the media_player service calls made since snapshot.
func speakerCommandsSince(server *MockHAServer, snapshot int) []ServiceCall {
	var commands []ServiceCall
	for _, call := range server.GetServiceCallsSince(snapshot) {
		if call.Domain == "media_player" {
			commands = append(commands, call)
		}
	}
	return commands
}

// TestScenario_UserStory_RestartKeepsOurMusicPlaying verifies that a restart leaves
// music alone when the app started it for the current mode.
//
// User story: "Evening music is playing. The app restarts for a deploy. The music
// keeps playing. I do not hear it stop or fade in again."
func TestScenario_UserStory_RestartKeepsOurMusicPlaying(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("GIVEN: It is evening and evening music is playing on the Kitchen group")
	setMusicRestartHome(server, "evening")
	setLeadSpeaker(server, "media_player.kitchen", "playing")

	t.Log("WHEN: The app restarts")
	require.NoError(t, stateManager.SyncFromHA())
	snapshot := server.ServiceCallCount()
	musicManager := music.NewManager(context.Background(), client, stateManager,
		loadTestMusicConfigFromYAML(t), testlogger.New(), false, nil, nil)
	require.NoError(t, musicManager.Start())
	defer musicManager.Stop()

	t.Log("THEN: No speaker commands are sent")
	time.Sleep(500 * time.Millisecond)
	assert.Empty(t, speakerCommandsSince(server, snapshot), "a restart must not restart music that is already right")
}

// TestScenario_UserStory_RestartDoesNotResumePausedMusic verifies that a restart does
// not resume music that someone paused.
//
// User story: "I paused the music for a phone call. The app restarts for a deploy.
// The music stays paused."
//
// Regression: on 2026-11-11 all speakers were paused by hand at 16:18Z. A deploy at
// 17:32Z started morning music again.
func TestScenario_UserStory_RestartDoesNotResumePausedMusic(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("GIVEN: It is evening and someone paused the evening music on the Kitchen group")
	setMusicRestartHome(server, "evening")
	setLeadSpeaker(server, "media_player.kitchen", "paused")

	t.Log("WHEN: The app restarts")
	require.NoError(t, stateManager.SyncFromHA())
	snapshot := server.ServiceCallCount()
	musicManager := music.NewManager(context.Background(), client, stateManager,
		loadTestMusicConfigFromYAML(t), testlogger.New(), false, nil, nil)
	require.NoError(t, musicManager.Start())
	defer musicManager.Stop()

	t.Log("THEN: No speaker commands are sent, so the music stays paused")
	time.Sleep(500 * time.Millisecond)
	assert.Empty(t, speakerCommandsSince(server, snapshot), "a restart must not resume music someone paused")
}

// TestScenario_UserStory_RestartReplacesMusicFromAnotherMode verifies that a restart
// replaces paused music that does not belong to the current mode.
//
// User story: "Evening music was paused. While the app was down, night began. When
// the app comes back, it starts the sleep-prep music."
func TestScenario_UserStory_RestartReplacesMusicFromAnotherMode(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("GIVEN: It is night, and the sleep-prep lead speaker holds paused Tidal music from the evening")
	setMusicRestartHome(server, "night")
	setLeadSpeaker(server, "media_player.front_room", "paused")

	t.Log("WHEN: The app restarts")
	require.NoError(t, stateManager.SyncFromHA())
	snapshot := server.ServiceCallCount()
	musicManager := music.NewManager(context.Background(), client, stateManager,
		loadTestMusicConfigFromYAML(t), testlogger.New(), false, nil, nil)
	require.NoError(t, musicManager.Start())
	defer musicManager.Stop()

	t.Log("THEN: Sleep-prep music starts")
	waitForCondition(t, func() bool {
		return len(filterServiceCalls(server.GetServiceCallsSince(snapshot), "media_player", "play_media")) >= 1
	}, "music from another mode should be replaced at startup")
}
