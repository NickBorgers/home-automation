package music

import (
	"context"
	"testing"
	"time"

	"homeautomation/pkg/plugin"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAudioResetTestManager builds a started music manager with nobody home,
// so Reset() clears musicPlaybackType.
func newAudioResetTestManager(t *testing.T, readOnly bool) (*Manager, *testutil.Env) {
	t.Helper()
	env := testutil.NewEnv(t)
	participant := []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}
	playback := []PlaybackOption{{URI: "https://tidal.com/browse/playlist/test", MediaType: "tidal", VolumeMultiplier: 1.0}}
	cfg := &MusicConfig{Music: map[string]MusicMode{"morning": {Participants: participant, PlaybackOptions: playback}}}

	env.StateMgr.SetString("dayPhase", "morning")
	env.StateMgr.SetBool("isAnyoneHome", false)
	env.StateMgr.SetString("musicPlaybackType", "morning")
	env.MockHA.SetState("media_player.kitchen", "idle", nil)

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, readOnly, &plugin.RealTimeProvider{}, nil)
	require.NoError(t, manager.Start())
	t.Cleanup(manager.Stop)
	return manager, env
}

// TestAudioReset_TriggerClearsBooleanAndRunsReset verifies the false → true edge on audioReset
// clears the boolean (loop prevention) and runs Reset, which stops music when nobody is home.
func TestAudioReset_TriggerClearsBooleanAndRunsReset(t *testing.T) {
	t.Parallel()
	_, env := newAudioResetTestManager(t, false)

	env.StateMgr.SetString("musicPlaybackType", "morning")
	require.NoError(t, env.StateMgr.SetBool("audioReset", true))

	val, err := env.StateMgr.GetBool("audioReset")
	require.NoError(t, err)
	assert.False(t, val, "audioReset must be cleared after the handler runs")

	assert.Eventually(t, func() bool {
		got, _ := env.StateMgr.GetString("musicPlaybackType")
		return got == ""
	}, 3*time.Second, 20*time.Millisecond, "Reset() should clear musicPlaybackType")
}

// TestAudioReset_IgnoresFalseAndNonBool verifies that false and non-bool values do nothing.
func TestAudioReset_IgnoresFalseAndNonBool(t *testing.T) {
	t.Parallel()
	manager, env := newAudioResetTestManager(t, false)

	manager.handleAudioResetChange("audioReset", true, false)
	manager.handleAudioResetChange("audioReset", false, "on")

	assert.Never(t, func() bool {
		got, _ := env.StateMgr.GetString("musicPlaybackType")
		return got != "morning"
	}, 300*time.Millisecond, 20*time.Millisecond, "musicPlaybackType must stay unchanged")
}

// TestAudioReset_ReadOnlyDoesNotClearBoolean verifies read-only mode writes nothing back.
func TestAudioReset_ReadOnlyDoesNotClearBoolean(t *testing.T) {
	t.Parallel()
	_, env := newAudioResetTestManager(t, true)

	require.NoError(t, env.StateMgr.SetBool("audioReset", true))

	val, err := env.StateMgr.GetBool("audioReset")
	require.NoError(t, err)
	assert.True(t, val, "read-only mode must not write the boolean")
}
