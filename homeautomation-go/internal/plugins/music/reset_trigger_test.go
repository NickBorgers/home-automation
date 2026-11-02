package music

import (
	"context"
	"testing"

	"homeautomation/pkg/plugin"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMusicResetTestManager builds a started music manager with nobody home,
// so Reset() clears musicPlaybackType.
func newMusicResetTestManager(t *testing.T, readOnly bool) (*Manager, *testutil.Env) {
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

// TestMusicReset_TriggerClearsBooleanAndRunsReset verifies the false → true edge on musicReset
// clears the boolean (loop prevention) and runs Reset, which stops music when nobody is home.
func TestMusicReset_TriggerClearsBooleanAndRunsReset(t *testing.T) {
	t.Parallel()
	manager, env := newMusicResetTestManager(t, false)

	env.StateMgr.SetString("musicPlaybackType", "morning")
	require.NoError(t, env.StateMgr.SetBool("musicReset", true))
	manager.handleMusicResetChange("musicReset", false, true)

	val, err := env.StateMgr.GetBool("musicReset")
	require.NoError(t, err)
	assert.False(t, val, "musicReset must be cleared after the handler runs")

	assert.Eventually(t, func() bool {
		got, _ := env.StateMgr.GetString("musicPlaybackType")
		return got == ""
	}, 3_000_000_000, 20_000_000, "Reset() should clear musicPlaybackType")
}

// TestMusicReset_IgnoresFalseAndNonBool verifies that false and non-bool values do nothing.
func TestMusicReset_IgnoresFalseAndNonBool(t *testing.T) {
	t.Parallel()
	manager, env := newMusicResetTestManager(t, false)

	manager.handleMusicResetChange("musicReset", true, false)
	manager.handleMusicResetChange("musicReset", false, "on")

	assert.Never(t, func() bool {
		got, _ := env.StateMgr.GetString("musicPlaybackType")
		return got != "morning"
	}, 300_000_000, 20_000_000, "musicPlaybackType must stay unchanged")
}

// TestMusicReset_ReadOnlyDoesNotClearBoolean verifies read-only mode writes nothing back.
func TestMusicReset_ReadOnlyDoesNotClearBoolean(t *testing.T) {
	t.Parallel()
	manager, env := newMusicResetTestManager(t, true)

	require.NoError(t, env.StateMgr.SetBool("musicReset", true))
	manager.handleMusicResetChange("musicReset", false, true)

	val, err := env.StateMgr.GetBool("musicReset")
	require.NoError(t, err)
	assert.True(t, val, "read-only mode must not write the boolean")
}
