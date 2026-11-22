package integration

import (
	"path/filepath"
	"testing"

	"homeautomation/internal/plugins/music"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScenario_RainZonesDoNotPlaySleepRain2 verifies the shipped rain playlists.
//
// User story: "The sleep-rain-2 track fades in and out too much overnight. It must not play in the rain zones."
//
// History: sleep-rain-2.m3u was first removed in #1207; it was accidentally re-added in #1412
// and removed again here. This test guards the specific filename so the same track cannot
// re-enter via a copy-paste error.
//
// INVARIANT: No playback option in sleep-prep or sleep uses sleep-rain-2.m3u.
func TestScenario_RainZonesDoNotPlaySleepRain2(t *testing.T) {
	t.Log("GIVEN: the production music config")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	for _, modeName := range []string{"sleep-prep", "sleep"} {
		t.Logf("WHEN: the %s playback options are listed", modeName)
		mode, ok := config.Music[modeName]
		require.True(t, ok, "%s zone exists", modeName)
		require.NotEmpty(t, mode.PlaybackOptions)

		for _, option := range mode.PlaybackOptions {
			assert.NotContains(t, option.URI, "sleep-rain-2.m3u",
				"THEN: %s must not offer the sleep-rain-2 track", modeName)
		}
	}
}
