package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// =============================================================================
// SoCo-CLI state reads (volume, playback)
// =============================================================================

func TestSoCoClient_StateReads(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	serve := func(t *testing.T, result string, exitCode int, requestPath *string) *SoCoClient {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*requestPath = r.URL.Path
			json.NewEncoder(w).Encode(SoCoResponse{Result: result, ExitCode: exitCode, ErrorMsg: "boom"})
		}))
		t.Cleanup(server.Close)
		return NewSoCoClient(server.URL, logger, false)
	}

	t.Run("GetVolume parses percentage", func(t *testing.T) {
		var path string
		client := serve(t, "13", 0, &path)

		volume, err := client.GetVolume("Sitting Room")
		require.NoError(t, err)
		assert.Equal(t, 13, volume)
		assert.Equal(t, "/Sitting Room/volume", path)
	})

	t.Run("GetVolume rejects non-numeric result", func(t *testing.T) {
		var path string
		client := serve(t, "success", 0, &path)

		_, err := client.GetVolume("Pantry")
		assert.Error(t, err)
	})

	t.Run("GetVolume returns SoCo application error", func(t *testing.T) {
		var path string
		client := serve(t, "", 1, &path)

		_, err := client.GetVolume("Pantry")
		assert.ErrorContains(t, err, "exit_code=1")
	})

	t.Run("GetPlaybackState returns transport state", func(t *testing.T) {
		var path string
		client := serve(t, "PLAYING\n", 0, &path)

		transport, err := client.GetPlaybackState("Pantry")
		require.NoError(t, err)
		assert.Equal(t, "PLAYING", transport)
		assert.Equal(t, "/Pantry/playback", path)
	})

	t.Run("reads still run in read-only mode", func(t *testing.T) {
		var path string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.Path
			json.NewEncoder(w).Encode(SoCoResponse{Result: "8", ExitCode: 0})
		}))
		defer server.Close()

		client := NewSoCoClient(server.URL, logger, true)
		volume, err := client.GetVolume("Pantry")
		require.NoError(t, err)
		assert.Equal(t, 8, volume)
		assert.Equal(t, "/Pantry/volume", path)
	})
}

// =============================================================================
// USER STORY: a speaker with no Home Assistant entity
// =============================================================================
//
// After the move-in, Home Assistant has no media_player.pantry or
// media_player.lounge. The Sonos "Bedroom" is media_player.front_room there.
// SoCo-CLI addresses speakers by name, so everything must work without an entity.
//
// INVARIANT: Human override detection works for every speaker that SoCo-CLI can
// reach, whether or not Home Assistant has an entity for it.
func TestScenario_HumanOverrideDetection_SpeakerWithoutHAEntity(t *testing.T) {
	t.Parallel()

	// GIVEN: SoCo-CLI knows "Pantry". Home Assistant does not.
	t.Log("GIVEN: Pantry exists in SoCo-CLI but has no Home Assistant entity")
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	var mu sync.Mutex
	currentVolume := 0
	humanOverride := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		resp := SoCoResponse{Result: "success"}
		switch r.URL.Path {
		case "/Pantry/volume":
			// A person turned the speaker down by hand
			if humanOverride {
				resp.Result = "0"
			} else {
				resp.Result = strconv.Itoa(currentVolume)
			}
		default:
			var level int
			if _, err := parseVolumePath(r.URL.Path, &level); err == nil {
				currentVolume = level
			}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{{PlayerName: "Pantry", BaseVolume: 8}},
				PlaybackOptions: []PlaybackOption{
					{URI: "https://tidal.com/browse/playlist/test", MediaType: "tidal", VolumeMultiplier: 1.0},
				},
			},
		},
	}
	timeProvider := plugin.FixedTimeProvider{FixedTime: time.Date(2026, 1, 12, 17, 0, 0, 0, time.UTC)}
	manager := NewManager(context.Background(), mockHA, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSoCoClient(NewSoCoClient(server.URL, logger, false))

	require.NoError(t, stateManager.SetString("musicPlaybackType", "day"))

	step := 0
	manager.SetSleepFunc(func(d time.Duration) {
		step++
		// WHEN: At step 3 a person turns the volume down
		if step == 3 {
			t.Log("WHEN: Someone turns Pantry down during the fade-in")
			mu.Lock()
			humanOverride = true
			mu.Unlock()
		}
	})

	manager.fadeInSpeaker(context.Background(), "Pantry", 8, "day")

	// THEN: The fade-in notices and stops
	t.Log("THEN: The fade-in detects the override and stops before the target volume")
	fadeIn, exists := manager.GetShadowState().Outputs.FadeInProgress["media_player.pantry"]
	require.True(t, exists, "fade-in progress should be recorded for Pantry")
	assert.True(t, fadeIn.HumanOverrideDetected)
	assert.Less(t, fadeIn.CurrentVolume, 8)
}

// parseVolumePath extracts N from a "/{speaker}/volume/N" path.
func parseVolumePath(path string, level *int) (string, error) {
	idx := len(path) - 1
	for idx >= 0 && path[idx] != '/' {
		idx--
	}
	n, err := strconv.Atoi(path[idx+1:])
	if err != nil {
		return "", err
	}
	*level = n
	return path[:idx], nil
}
