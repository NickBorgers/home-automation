package integration

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"homeautomation/internal/plugins/music"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const startupSoCoMusicConfig = `
music:
  day:
    participants:
      - player_name: "Pantry"
        base_volume: 9
        leave_muted_if: []
      - player_name: "Lounge"
        base_volume: 9
        leave_muted_if: []
      - player_name: "Kitchen"
        base_volume: 9
        leave_muted_if: []
    playback_options:
      - uri: "https://tidal.com/browse/playlist/day-current"
        media_type: tidal
        volume_multiplier: 1.0
`

// TestScenario_MusicRestart_AdoptsPlaybackFromSoCo checks that a restart keeps music that is
// already playing, using only SoCo-CLI.
//
// Story: "When the automation restarts, the music that is playing should keep playing."
//
// SoCo-CLI addresses speakers by name, so a speaker needs no Home Assistant entity.
// Kitchen and Primary Bath have none today.
//
// INVARIANT: With SoCo-CLI configured, startup reads the lead speaker's state, current track,
// group and volumes from SoCo-CLI. A zone that already plays the right music is adopted, not
// restarted.
func TestScenario_MusicRestart_AdoptsPlaybackFromSoCo(t *testing.T) {
	t.Parallel()

	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	var musicConfig music.MusicConfig
	require.NoError(t, yaml.Unmarshal([]byte(startupSoCoMusicConfig), &musicConfig))

	// GIVEN: Pantry leads a day group with Lounge and plays a Tidal track.
	// Home Assistant has no media_player entity for any of these speakers.
	t.Log("GIVEN: SoCo reports Pantry playing Tidal, grouped with Lounge; Home Assistant has no speaker entities")
	soco := newFakeSoCoServer()
	socoServer := httptest.NewServer(soco)
	defer socoServer.Close()
	const track = "x-sonos-http:track%2f312527385.flac?sid=174&flags=24616&sn=9"
	soco.setPlaying("Pantry", track, 11)
	soco.setPlaying("Lounge", track, 12)
	soco.setGroups("\nPantry: Lounge\nKitchen: \nBarn: \n")

	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "day", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	// WHEN: The automation starts.
	t.Log("WHEN: The music plugin starts")
	logger := testlogger.New()
	musicMgr := music.NewManager(context.Background(), client, stateManager, &musicConfig, logger, false, nil, nil)
	musicMgr.SetSleepFunc(func(time.Duration) {})
	musicMgr.SetSoCoClient(music.NewSoCoClient(socoServer.URL, logger, false))
	require.NoError(t, musicMgr.Start())
	defer musicMgr.Stop()

	// THEN: The playing music is adopted. Pantry's queue is not cleared or reloaded.
	t.Log("THEN: The plugin adopts the playing music and does not restart it")
	waitForCondition(t, func() bool {
		return musicMgr.GetShadowState().Outputs.LastActionType == "adopt_playback"
	}, "startup should adopt the music Pantry is playing")
	assert.Zero(t, soco.callCount("Pantry", "clear_queue"), "adopted music must not be reloaded")
	assert.Zero(t, soco.callCount("Pantry", "sharelink"), "adopted music must not be reloaded")

	shadow := musicMgr.GetShadowState()
	assert.Equal(t, "day", shadow.Outputs.CurrentMode)
	active := make(map[string]bool)
	volumes := make(map[string]int)
	for _, s := range shadow.Outputs.SpeakerGroup {
		active[s.PlayerName] = s.Active
		volumes[s.PlayerName] = s.Volume
	}
	assert.Equal(t, map[string]bool{"Pantry": true, "Lounge": true, "Kitchen": false}, active,
		"group membership comes from SoCo")
	assert.Equal(t, 11, volumes["Pantry"], "volume comes from SoCo")
	assert.Equal(t, 12, volumes["Lounge"], "volume comes from SoCo")
}
