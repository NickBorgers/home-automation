package integration

import (
	"math"
	"path/filepath"
	"strings"
	"testing"

	"homeautomation/internal/plugins/music"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScenario_RainPlaybackUsesRaisedVolumeTargets verifies the shipped rain profiles.
//
// User story: "Rain sounds should keep the louder levels I set manually, whichever rain playlist plays."
//
// INVARIANTS:
//   - Every configured rain playlist uses the same effective targets in sleep-prep and sleep.
//   - Primary Bed targets 24; the other targets preserve their configured room balance.
//   - Effective volumes use the production rounding rule: round(base volume * playlist multiplier).
func TestScenario_RainPlaybackUsesRaisedVolumeTargets(t *testing.T) {
	t.Parallel()
	t.Log("GIVEN: the production sleep-prep and sleep rain profiles")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	expected := map[string]map[string]int{
		"sleep-prep": {
			"Pantry": 14, "Utility": 13, "Lounge": 14, "Kitchen": 10, "Soundbar": 16,
			"Primary Bed": 24, "Primary Bath": 14, "Sitting": 14,
		},
		"sleep": {"Primary Bed": 24, "Primary Bath": 14},
	}

	for _, modeName := range []string{"sleep-prep", "sleep"} {
		mode := config.Music[modeName]
		t.Logf("WHEN: each configured %s rain playlist is selected", modeName)
		require.Len(t, mode.PlaybackOptions, 2)
		for _, option := range mode.PlaybackOptions {
			require.Contains(t, option.URI, "sleep-rain-", "%s contains a non-rain option", modeName)
			for _, participant := range mode.Participants {
				effectiveVolume := int(math.Round(float64(participant.BaseVolume) * option.VolumeMultiplier))
				assert.Equal(t, expected[modeName][participant.PlayerName], effectiveVolume,
					"THEN: %s targets the raised volume for %s with %s",
					participant.PlayerName, modeName, filepath.Base(strings.TrimSuffix(option.URI, ".m3u")))
			}
		}
	}
}
