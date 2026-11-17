package music

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProductionConfig_SleepProfilesPrimarySuite checks the shipped sleep and sleep-prep profiles.
//
// User story: "The rain sounds in the primary bedroom are too quiet, and the primary bathroom
// should play them too, both before bed and while we sleep."
//
// INVARIANTS:
//   - Primary Bed plays at 15 in both profiles (12 raised by 25%).
//   - Primary Bath is a participant in both profiles.
func TestProductionConfig_SleepProfilesPrimarySuite(t *testing.T) {
	t.Parallel()

	config, err := LoadConfig(filepath.Join(findRepoRoot(t), "configs", "music_config.yaml"))
	require.NoError(t, err)

	for _, mode := range []string{"sleep-prep", "sleep"} {
		volumes := map[string]int{}
		for _, p := range config.Music[mode].Participants {
			volumes[p.PlayerName] = p.BaseVolume
		}
		assert.Equal(t, 15, volumes["Primary Bed"], "%s: Primary Bed volume", mode)
		assert.Equal(t, 9, volumes["Primary Bath"], "%s: Primary Bath volume", mode)
	}
}
