package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/music"
)

// TestScenario_MusicVolumesReducedThirtyPercent verifies the shipped base volume of every speaker in every zone.
//
// Story: "Music is too loud in every playback profile. Bring all of it down by 30%."
//
// Invariants:
//   - Every base volume is 70% of its earlier value, rounded to the nearest whole Sonos step.
//   - Two exceptions keep the room balance that other tests protect:
//     Utility stays one step below Pantry, so it is Pantry minus 1 where plain rounding would tie them.
//     Primary Bed in the rain zones is 10, not 11, so its raised rain volume (10 x 1.6 = 16) lands near 70% of 24.
//   - Playlist multipliers are unchanged, so rain sounds fall with the base volumes.
func TestScenario_MusicVolumesReducedThirtyPercent(t *testing.T) {
	t.Parallel()
	t.Log("GIVEN: the production music playback zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	expected := map[string]map[string]int{
		"morning": {
			"Pantry": 6, "Utility": 5, "Lounge": 6, "Kitchen": 7, "Soundbar": 7,
			"Kids Bathroom": 5, "Primary Bed": 5, "Primary Bath": 5, "Sitting": 6,
		},
		"day": {
			"Pantry": 6, "Utility": 5, "Lounge": 6, "Kitchen": 8, "Soundbar": 7,
			"Kids Bathroom": 5, "Primary Bed": 5, "Primary Bath": 5, "Sitting": 6,
		},
		"evening": {
			"Pantry": 6, "Utility": 5, "Lounge": 6, "Kitchen": 7, "Soundbar": 7,
			"Kids Bathroom": 6, "Primary Bed": 4, "Primary Bath": 4, "Sitting": 6,
		},
		"winddown": {
			"Pantry": 5, "Utility": 4, "Lounge": 5, "Kitchen": 6, "Soundbar": 7,
			"Kids Bathroom": 6, "Primary Bed": 4, "Primary Bath": 4, "Sitting": 5,
		},
		"sleep-prep": {
			"Pantry": 6, "Utility": 5, "Lounge": 6, "Kitchen": 6, "Soundbar": 7,
			"Primary Bed": 10, "Primary Bath": 6, "Sitting": 6,
		},
		"sleep":  {"Primary Bed": 10, "Primary Bath": 6},
		"sex":    {"Primary Bed": 6},
		"wakeup": {"Primary Bed": 4},
	}

	require.Len(t, config.Music, len(expected), "every zone needs an expected volume table in this test")
	for zoneName, want := range expected {
		t.Run(zoneName, func(t *testing.T) {
			mode, ok := config.Music[zoneName]
			require.True(t, ok, "zone %s must exist", zoneName)

			t.Logf("WHEN: the %s zone starts playback", zoneName)
			got := map[string]int{}
			for _, participant := range mode.Participants {
				got[participant.PlayerName] = participant.BaseVolume
			}

			t.Log("THEN: each speaker plays at its reduced base volume")
			assert.Equal(t, want, got)
		})
	}
}
