package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/music"
)

// TestScenario_MusicKitchenVolumes verifies the Kitchen speaker's base volume in each zone.
//
// Story: "The Kitchen's rain sounds are too loud. I want them 25% quieter."
//
// Invariant: Kitchen's sleep-prep base volume (the zone that plays rain sounds) is 25% below its
// earlier value of 11, rounded to the nearest whole step. The shared rain multiplier is unchanged.
// Kitchen is not a participant in the sleep zone. Other zones keep their earlier Kitchen volumes.
func TestScenario_MusicKitchenVolumes(t *testing.T) {
	t.Parallel()
	t.Log("GIVEN: the production music playback zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	expected := map[string]int{
		"morning":    10,
		"day":        11,
		"evening":    10,
		"winddown":   9,
		"sleep-prep": 8,
	}

	for zoneName, mode := range config.Music {
		t.Run(zoneName, func(t *testing.T) {
			t.Logf("WHEN: the %s zone starts playback", zoneName)
			for _, participant := range mode.Participants {
				if participant.PlayerName != "Kitchen" {
					continue
				}
				t.Log("THEN: Kitchen plays at its configured base volume")
				want, ok := expected[zoneName]
				require.True(t, ok, "zone %q needs an expected Kitchen volume in this test", zoneName)
				assert.Equal(t, want, participant.BaseVolume)
				return
			}
			assert.NotContains(t, expected, zoneName, "Kitchen is missing from %s", zoneName)
		})
	}
}
