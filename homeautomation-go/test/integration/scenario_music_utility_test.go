package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/music"
)

// TestScenario_MusicUtilityFollowsPantry verifies that the Utility speaker plays wherever Pantry plays.
//
// Story: "When music plays in the pantry, I want to hear it in the utility room too, a little quieter."
//
// Invariants:
//   - Utility is in every zone that has Pantry, and in no other zone.
//   - Utility's base volume is one step below Pantry's in each zone.
//   - Utility has the same mute and exclude rules as Pantry, so the two act as one.
//   - Utility never leads a zone.
func TestScenario_MusicUtilityFollowsPantry(t *testing.T) {
	t.Parallel()
	t.Log("GIVEN: the production music playback zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	for zoneName, mode := range config.Music {
		t.Run(zoneName, func(t *testing.T) {
			t.Logf("WHEN: the %s zone starts playback", zoneName)
			var pantry, utility *music.Participant
			for i := range mode.Participants {
				switch mode.Participants[i].PlayerName {
				case "Pantry":
					pantry = &mode.Participants[i]
				case "Utility":
					utility = &mode.Participants[i]
				}
			}

			if pantry == nil {
				t.Log("THEN: Utility stays out of a zone without Pantry")
				assert.Nil(t, utility, "Utility must play only where Pantry plays")
				return
			}

			t.Log("THEN: Utility plays with Pantry, slightly quieter, and does not lead")
			require.NotNil(t, utility, "Utility must play wherever Pantry plays")
			assert.Equal(t, pantry.BaseVolume-1, utility.BaseVolume, "Utility is one step quieter than Pantry")
			assert.ElementsMatch(t, pantry.LeaveMutedIf, utility.LeaveMutedIf)
			assert.ElementsMatch(t, pantry.ExcludeIf, utility.ExcludeIf)
			assert.NotEqual(t, "Utility", mode.Participants[0].PlayerName, "Utility must not lead")
		})
	}
}
