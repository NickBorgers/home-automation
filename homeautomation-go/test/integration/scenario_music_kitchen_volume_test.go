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
// Story: "Music is too loud in every profile. I want it 30% quieter."
//
// Invariant: Kitchen's base volume in each zone is 70% of its earlier value, rounded to the nearest
// whole step. The shared rain multiplier is unchanged. Kitchen is not a participant in the sleep zone.
// scenario_music_volume_reduction_test.go checks every speaker; this test keeps the Kitchen focus.
func TestScenario_MusicKitchenVolumes(t *testing.T) {
	t.Parallel()
	t.Log("GIVEN: the production music playback zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	expected := map[string]int{
		"morning":    7,
		"day":        8,
		"evening":    7,
		"winddown":   6,
		"sleep-prep": 6,
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
