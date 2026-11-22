package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"homeautomation/internal/alert"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
)

// ============================================================================
// Soundbar + TV hands-off tests
//
// Story: "When I watch TV on the soundbar, the music system must leave the
// soundbar alone. It must not group it, fade it, mute it or change its volume."
//
// INVARIANT: while isTVPlaying=true, the music plugin sends no command to a
// speaker that has a matching hands_off rule.
// INVARIANT: other speakers in the zone still play music.
// ============================================================================

const soundbarTVMusicConfig = `
hands_off:
  - player_name: "Soundbar"
    when:
      - variable: isTVPlaying
        value: true

zones:
  - name: "evening"
    priority: 40
    trigger:
      - variable: dayPhase
        value: evening
      - variable: isAnyoneHome
        value: true
  - name: "winddown"
    priority: 40
    trigger:
      - variable: dayPhase
        value: winddown
      - variable: isAnyoneHome
        value: true

music:
  evening:
    participants:
      - player_name: "Kitchen"
        base_volume: 9
        leave_muted_if: []
      - player_name: "Soundbar"
        base_volume: 10
        leave_muted_if:
          - variable: isGuestAsleep
            value: true
    playback_options:
      - uri: "https://tidal.com/browse/playlist/test_evening_playlist"
        media_type: tidal
        volume_multiplier: 1.0
  winddown:
    participants:
      - player_name: "Kitchen"
        base_volume: 8
        leave_muted_if: []
      - player_name: "Soundbar"
        base_volume: 10
        leave_muted_if:
          - variable: isGuestAsleep
            value: true
    playback_options:
      - uri: "https://tidal.com/browse/playlist/test_winddown_playlist"
        media_type: tidal
        volume_multiplier: 1.0
`

type soundbarTVEnv struct {
	server       *MockHAServer
	stateManager *state.Manager
	music        *music.Manager
}

func setupSoundbarTVTest(t *testing.T) *soundbarTVEnv {
	t.Helper()
	server, client, stateManager, cleanup := setupTest(t)
	t.Cleanup(cleanup)

	logger := testlogger.New()
	require.NoError(t, stateManager.SetupComputedStateV2())
	t.Cleanup(stateManager.StopComputedState)

	stateTracking := statetracking.NewManager(
		context.Background(), client, stateManager, logger, false, nil, "", &alert.MockAlerter{},
	)
	require.NoError(t, stateTracking.Start())
	t.Cleanup(stateTracking.Stop)

	var config music.MusicConfig
	require.NoError(t, yaml.Unmarshal([]byte(soundbarTVMusicConfig), &config))

	for _, name := range []string{"kitchen", "soundbar"} {
		server.SetState("media_player."+name, "idle", map[string]interface{}{"volume_level": 0.1})
	}

	musicManager := music.NewManager(context.Background(), client, stateManager, &config, logger, false, nil, nil)
	musicManager.SetSleepFunc(func(time.Duration) {})
	musicManager.SetDebounceDelay(10 * time.Millisecond)
	require.NoError(t, musicManager.Start())
	t.Cleanup(musicManager.Stop)

	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.master_asleep", "off", nil)
	waitForBoolState(t, stateManager, "isAnyoneHome", true)

	return &soundbarTVEnv{server: server, stateManager: stateManager, music: musicManager}
}

// callsTouching returns the speaker commands that name the entity as a target or a group member.
func callsTouching(calls []ServiceCall, entityID string) []ServiceCall {
	var out []ServiceCall
	for _, call := range calls {
		if call.Domain != "media_player" {
			continue
		}
		if call.ServiceData["entity_id"] == entityID {
			out = append(out, call)
			continue
		}
		if members, ok := call.ServiceData["group_members"].([]interface{}); ok {
			for _, member := range members {
				if member == entityID {
					out = append(out, call)
					break
				}
			}
		}
	}
	return out
}

// TestScenario_TVPlaying_MusicStartLeavesSoundbarAlone covers a zone that starts while the TV plays.
func TestScenario_TVPlaying_MusicStartLeavesSoundbarAlone(t *testing.T) {
	t.Parallel()
	env := setupSoundbarTVTest(t)

	// GIVEN: the TV plays through the soundbar.
	t.Log("GIVEN: someone is home and watching TV")
	env.server.SetState("input_boolean.tv_playing", "on", nil)
	waitForBoolState(t, env.stateManager, "isTVPlaying", true)
	snapshot := env.server.ServiceCallCount()

	// WHEN: the evening zone starts.
	t.Log("WHEN: the day phase changes to evening and music starts")
	env.server.SetState("input_text.day_phase", "evening", nil)

	// THEN: the Kitchen plays, and the Soundbar gets no command at all.
	t.Log("THEN: Kitchen gets music, Soundbar is never touched")
	require.Eventually(t, func() bool {
		return len(callsTouching(env.server.GetServiceCallsSince(snapshot), "media_player.kitchen")) > 0
	}, stateWaitTimeout, statePollInterval, "Kitchen should receive music commands")
	waitForProcessing(t, env.stateManager)
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 100*time.Millisecond)

	assert.Empty(t, callsTouching(env.server.GetServiceCallsSince(snapshot), "media_player.soundbar"),
		"music plugin must send nothing to the Soundbar while the TV plays")
}

// TestScenario_TVStartsDuringMusic_ZoneChangeLeavesSoundbarAlone covers a zone change after the TV starts.
func TestScenario_TVStartsDuringMusic_ZoneChangeLeavesSoundbarAlone(t *testing.T) {
	t.Parallel()
	env := setupSoundbarTVTest(t)

	// GIVEN: evening music plays on Kitchen and Soundbar, TV off.
	t.Log("GIVEN: evening music plays on Kitchen and Soundbar")
	start := env.server.ServiceCallCount()
	env.server.SetState("input_text.day_phase", "evening", nil)
	require.Eventually(t, func() bool {
		return len(callsTouching(env.server.GetServiceCallsSince(start), "media_player.soundbar")) > 0
	}, stateWaitTimeout, statePollInterval, "Soundbar should join music while the TV is off")
	waitForServiceCallsToStabilizeSince(t, env.server, start, 200*time.Millisecond)

	// WHEN: the TV starts, then the zone changes to winddown.
	t.Log("WHEN: the TV starts, then the day phase changes to winddown")
	env.server.SetState("input_boolean.tv_playing", "on", nil)
	waitForBoolState(t, env.stateManager, "isTVPlaying", true)
	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()
	env.server.SetState("input_text.day_phase", "winddown", nil)

	// THEN: the zone change fades, ungroups and regroups Kitchen only.
	t.Log("THEN: the zone change never touches the Soundbar")
	require.Eventually(t, func() bool {
		for _, call := range callsTouching(env.server.GetServiceCallsSince(snapshot), "media_player.kitchen") {
			if call.Service == "volume_set" {
				return true
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, "Kitchen should receive winddown volume commands")
	waitForProcessing(t, env.stateManager)
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 100*time.Millisecond)

	assert.Empty(t, callsTouching(env.server.GetServiceCallsSince(snapshot), "media_player.soundbar"),
		"music plugin must send nothing to the Soundbar while the TV plays")
}
