package integration

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"homeautomation/internal/plugins/music"
)

// TestScenario_MusicSoundbarFollowsKitchen verifies that the Soundbar is always
// grouped with Kitchen while retaining its independent mute policy.
func TestScenario_MusicSoundbarFollowsKitchen(t *testing.T) {
	t.Log("GIVEN: the production music playback zones")
	config, err := music.LoadConfig(filepath.Join("..", "..", "..", "configs", "music_config.yaml"))
	require.NoError(t, err)

	kitchenZones := make(map[string]bool)
	soundbarZones := make(map[string]bool)
	for zoneName, mode := range config.Music {
		for _, participant := range mode.Participants {
			switch participant.PlayerName {
			case "Kitchen":
				kitchenZones[zoneName] = true
			case "Soundbar":
				soundbarZones[zoneName] = true
				assert.Equal(t, 10, participant.BaseVolume,
					"Soundbar should retain its independently configured target volume in %s", zoneName)
				assert.Equal(t, []music.MuteCondition{{Variable: "isGuestAsleep", Value: true}},
					participant.LeaveMutedIf,
					"Soundbar should use its own mute condition in %s", zoneName)
				assert.Empty(t, participant.ExcludeIf,
					"Soundbar must remain grouped when muted in %s", zoneName)
			}
		}
	}

	t.Log("THEN: the music plugin leaves the Soundbar alone while the TV plays")
	assert.Contains(t, config.HandsOff, music.HandsOffRule{
		PlayerName: "Soundbar",
		When:       []music.MuteCondition{{Variable: "isTVPlaying", Value: true}},
	})

	t.Log("WHEN: Kitchen-containing zones and Soundbar-containing zones are compared")
	t.Log("THEN: Soundbar is grouped in every Kitchen zone and nowhere else")
	assert.Equal(t, kitchenZones, soundbarZones)
	assert.False(t, soundbarZones["wakeup"])
	assert.False(t, soundbarZones["sex"])
}
