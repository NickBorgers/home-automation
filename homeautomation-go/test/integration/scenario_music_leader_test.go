package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/music"
)

// TestScenario_MusicZoneLeaders verifies which speaker leads each production music zone.
//
// Story: "When music plays through the house, I want it to keep playing without dropouts."
//
// The leader (the first participant) streams the music and feeds every follower. If its link
// is weak, the whole group stutters or drops. Invariants:
//   - Whole-house zones are led by Pantry, the speaker with the strongest Wi-Fi link.
//   - The leader is never excluded, so the zone keeps the same leader in every state.
//   - Soundbar never leads. It switches its output to the TV when TV playback starts.
//   - Sleep sounds stay led by Primary Bed. The night rain setup depends on it.
func TestScenario_MusicZoneLeaders(t *testing.T) {
	t.Parallel()
	t.Log("GIVEN: the production music playback zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	expectedLeaders := map[string]string{
		"morning":    "Pantry",
		"day":        "Pantry",
		"evening":    "Pantry",
		"winddown":   "Pantry",
		"sleep-prep": "Pantry",
		"sleep":      "Primary Bed",
		"sex":        "Primary Bed",
		"wakeup":     "Primary Bed",
	}

	for zoneName, mode := range config.Music {
		t.Run(zoneName, func(t *testing.T) {
			t.Logf("WHEN: the %s zone starts playback", zoneName)
			require.NotEmpty(t, mode.Participants)
			leader := mode.Participants[0]

			t.Log("THEN: the expected speaker leads, and it cannot drop out of its own zone")
			want, ok := expectedLeaders[zoneName]
			require.True(t, ok, "zone %q needs an expected leader in this test", zoneName)
			assert.Equal(t, want, leader.PlayerName, "the first participant is the zone leader")
			assert.NotEqual(t, "Soundbar", leader.PlayerName,
				"Soundbar switches to TV audio and must never lead")
			assert.Empty(t, leader.ExcludeIf, "an excluded leader would hand the group to another speaker")
		})
	}

	for zone := range expectedLeaders {
		assert.Contains(t, config.Music, zone, "expected zone %q is missing from the config", zone)
	}
}
