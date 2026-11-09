package music

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsBedroomSleepPlaybackActive_UsesActiveZoneMembership(t *testing.T) {
	manager := &Manager{}
	manager.zoneManager = &ZoneManager{activeZones: map[string]*Zone{
		"sleep-prep": {
			Name:         "sleep-prep",
			Participants: []ParticipantWithVolume{{PlayerName: "Primary Bed"}},
		},
	}}

	assert.True(t, manager.IsBedroomSleepPlaybackActive())

	manager.zoneManager.mu.Lock()
	manager.zoneManager.activeZones = map[string]*Zone{
		"morning": {
			Name:         "morning",
			Participants: []ParticipantWithVolume{{PlayerName: "Primary Bed"}},
		},
	}
	manager.zoneManager.mu.Unlock()
	assert.False(t, manager.IsBedroomSleepPlaybackActive(), "normal bedroom playback must not suppress TTS")
}
