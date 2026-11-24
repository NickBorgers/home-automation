package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScenario_TVPowerOnSelectsAudioSystemOncePerUse(t *testing.T) {
	server, _, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: the TV panel and playback are off")
	server.SetState("media_player.sony_xr_65a80k", "off", nil)
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: the TV panel turns on and playback state subsequently churns")
	server.SetState("media_player.sony_xr_65a80k", "on", nil)
	server.SetState("switch.sync_box_power", "on", nil)
	server.SetState("select.sync_box_hdmi_input", "AppleTV", nil)
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, FilterServiceCalls(server.GetServiceCallsSince(snapshot), "remote", "send_command"),
		"TV power without playback must not select audio")
	server.SetState("media_player.big_beautiful_oled", "playing", nil)
	server.SetState("media_player.big_beautiful_oled", "paused", nil)
	server.SetState("media_player.big_beautiful_oled", "playing", nil)

	t.Log("THEN: the audio system command is sent exactly twice, about five seconds apart")
	require.Eventually(t, func() bool {
		return len(FilterServiceCalls(server.GetServiceCallsSince(snapshot), "remote", "send_command")) == 2
	}, 7*time.Second, 50*time.Millisecond)

	calls := FilterServiceCalls(server.GetServiceCallsSince(snapshot), "remote", "send_command")
	require.Len(t, calls, 2)
	for _, call := range calls {
		require.Equal(t, "remote.sony_xr_65a80k", call.ServiceData["entity_id"])
		require.Equal(t, "AudioOutput_AudioSystem", call.ServiceData["command"])
	}
	require.GreaterOrEqual(t, calls[1].Timestamp.Sub(calls[0].Timestamp), 5*time.Second)

	t.Log("AND: unrelated playback transitions do not start another attempt pair")
	time.Sleep(200 * time.Millisecond)
	require.Len(t, FilterServiceCalls(server.GetServiceCallsSince(snapshot), "remote", "send_command"), 2)
}
