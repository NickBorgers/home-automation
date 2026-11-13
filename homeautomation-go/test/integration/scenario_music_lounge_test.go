package integration

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScenario_LoungeSpeaker_JoinsEveryAutomaticMusicMode(t *testing.T) {
	t.Parallel()

	// GIVEN: The automatic music configuration is loaded.
	t.Log("GIVEN: the automatic music configuration is loaded")
	config := loadTestMusicConfigFromYAML(t)

	tests := []struct {
		mode       string
		baseVolume int
		leader     string
	}{
		{mode: "morning", baseVolume: 8, leader: "Kitchen"},
		{mode: "day", baseVolume: 9, leader: "Kitchen"},
		{mode: "evening", baseVolume: 8, leader: "Kitchen"},
		{mode: "winddown", baseVolume: 7, leader: "Kitchen"},
		{mode: "sleep-prep", baseVolume: 9, leader: "Front Room"},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			// WHEN: The household enters an automatic music mode.
			t.Logf("WHEN: the household enters %s mode", tt.mode)
			mode, ok := config.Music[tt.mode]
			require.True(t, ok, "automatic music mode %q must exist", tt.mode)
			require.NotEmpty(t, mode.Participants)

			// THEN: Lounge participates without becoming the zone leader.
			t.Log("THEN: Lounge joins at its established volume and the existing leader remains first")
			require.Equal(t, tt.leader, mode.Participants[0].PlayerName,
				"the first participant is always the zone leader")

			for _, participant := range mode.Participants {
				if participant.PlayerName == "Lounge" {
					require.Equal(t, tt.baseVolume, participant.BaseVolume)
					require.Empty(t, participant.LeaveMutedIf)
					return
				}
			}
			t.Fatalf("Lounge is missing from %s mode", tt.mode)
		})
	}
}
