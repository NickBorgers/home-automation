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
// Story: "Music in the kitchen is too quiet. I want it about 25% louder everywhere."
//
// Invariant: Kitchen base volumes are 25% above their earlier values (8, 9, 8, 7, 9),
// rounded to the nearest whole step.
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
		"sleep-prep": 11,
	}

	for zoneName, mode := range config.Music {
		t.Run(zoneName, func(t *testing.T) {
			t.Logf("WHEN: the %s zone starts playback", zoneName)
			for _, participant := range mode.Participants {
				if participant.PlayerName != "Kitchen" {
					continue
				}
				t.Log("THEN: Kitchen plays at its raised base volume")
				want, ok := expected[zoneName]
				require.True(t, ok, "zone %q needs an expected Kitchen volume in this test", zoneName)
				assert.Equal(t, want, participant.BaseVolume)
				return
			}
			assert.NotContains(t, expected, zoneName, "Kitchen is missing from %s", zoneName)
		})
	}
}
