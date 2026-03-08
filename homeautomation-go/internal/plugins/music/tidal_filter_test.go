package music

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

func TestFilterPlaybackOptions(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	mixedOptions := []PlaybackOption{
		{URI: "spotify:playlist:abc", MediaType: "playlist", VolumeMultiplier: 1.0},
		{URI: "https://tidal.com/browse/playlist/123", MediaType: "tidal", VolumeMultiplier: 1.0},
		{URI: "http://rain-sounds.example.com/rain.m3u", MediaType: "music", VolumeMultiplier: 1.3},
		{URI: "https://tidal.com/browse/playlist/456", MediaType: "tidal", VolumeMultiplier: 1.0},
	}

	allTidalOptions := []PlaybackOption{
		{URI: "https://tidal.com/browse/playlist/123", MediaType: "tidal", VolumeMultiplier: 1.0},
		{URI: "https://tidal.com/browse/playlist/456", MediaType: "tidal", VolumeMultiplier: 1.0},
	}

	noTidalOptions := []PlaybackOption{
		{URI: "spotify:playlist:abc", MediaType: "playlist", VolumeMultiplier: 1.0},
		{URI: "http://rain-sounds.example.com/rain.m3u", MediaType: "music", VolumeMultiplier: 1.3},
	}

	t.Run("SoCo-CLI configured: returns all options unfiltered", func(t *testing.T) {
		t.Parallel()
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		config := &MusicConfig{Music: map[string]MusicMode{}}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
		manager.SetSoCoClient(NewSoCoClient("http://127.0.0.1:8000", logger, false))

		result := manager.filterPlaybackOptions(mixedOptions)
		assert.Equal(t, mixedOptions, result)
	})

	t.Run("SoCo-CLI not configured: filters out Tidal options", func(t *testing.T) {
		t.Parallel()
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		config := &MusicConfig{Music: map[string]MusicMode{}}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
		// socoClient is nil by default

		result := manager.filterPlaybackOptions(mixedOptions)
		assert.Len(t, result, 2)
		assert.Equal(t, "playlist", result[0].MediaType)
		assert.Equal(t, "music", result[1].MediaType)
	})

	t.Run("SoCo-CLI not configured: all Tidal returns empty", func(t *testing.T) {
		t.Parallel()
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		config := &MusicConfig{Music: map[string]MusicMode{}}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)

		result := manager.filterPlaybackOptions(allTidalOptions)
		assert.Empty(t, result)
	})

	t.Run("SoCo-CLI not configured: no Tidal options unchanged", func(t *testing.T) {
		t.Parallel()
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		config := &MusicConfig{Music: map[string]MusicMode{}}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)

		result := manager.filterPlaybackOptions(noTidalOptions)
		assert.Equal(t, noTidalOptions, result)
	})
}

func TestOrchestratePlayback_TidalFiltering(t *testing.T) {
	t.Parallel()

	t.Run("skips Tidal playlists when SoCo-CLI not configured", func(t *testing.T) {
		t.Parallel()
		core, _ := observer.New(zap.InfoLevel)
		logger := zap.New(core)

		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning": {
					Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}},
					PlaybackOptions: []PlaybackOption{
						{URI: "spotify:playlist:abc", MediaType: "playlist", VolumeMultiplier: 1.0},
						{URI: "https://tidal.com/browse/playlist/123", MediaType: "tidal", VolumeMultiplier: 1.0},
					},
				},
				"day":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"evening":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"winddown": {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"sleep":    {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"sex":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"wakeup":   {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
			},
		}

		fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
		timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, timeProvider, nil)
		// socoClient is nil - SoCo-CLI not configured
		manager.SetSleepFunc(func(d time.Duration) {})

		_ = stateManager.SetString("dayPhase", "morning")
		_ = stateManager.SetBool("isAnyoneHome", true)
		_ = stateManager.SetBool("isAnyoneAsleep", false)

		// Call orchestratePlayback - should pick the Spotify playlist, not the Tidal one
		err := manager.orchestratePlayback("morning", "test")
		require.NoError(t, err)

		// Verify the selected URI is the Spotify one
		manager.mu.RLock()
		currentlyPlaying := manager.currentlyPlaying
		manager.mu.RUnlock()

		require.NotNil(t, currentlyPlaying)
		assert.Equal(t, "spotify:playlist:abc", currentlyPlaying.URI)
		assert.Equal(t, "playlist", currentlyPlaying.MediaType)
	})

	t.Run("returns error when all options are Tidal and SoCo-CLI not configured", func(t *testing.T) {
		t.Parallel()
		core, _ := observer.New(zap.InfoLevel)
		logger := zap.New(core)

		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"wakeup": {
					Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 6}},
					PlaybackOptions: []PlaybackOption{
						{URI: "https://tidal.com/browse/playlist/abc", MediaType: "tidal", VolumeMultiplier: 1.0},
					},
				},
				"morning":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"day":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"evening":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"winddown": {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"sleep":    {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"sex":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
			},
		}

		fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
		timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, timeProvider, nil)
		manager.SetSleepFunc(func(d time.Duration) {})

		err := manager.orchestratePlayback("wakeup", "test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no playable options")
		assert.Contains(t, err.Error(), "SOCO_CLI_URL")
	})

	t.Run("Tidal playlists work when SoCo-CLI is configured", func(t *testing.T) {
		t.Parallel()
		core, _ := observer.New(zap.InfoLevel)
		logger := zap.New(core)

		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"wakeup": {
					Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 6}},
					PlaybackOptions: []PlaybackOption{
						{URI: "https://tidal.com/browse/playlist/abc", MediaType: "tidal", VolumeMultiplier: 1.0},
					},
				},
				"morning":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"day":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"evening":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"winddown": {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"sleep":    {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
				"sex":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}},
			},
		}

		fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
		timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, timeProvider, nil)
		// Configure SoCo-CLI client (read-only mode since we don't have a real server)
		manager.SetSoCoClient(NewSoCoClient("http://127.0.0.1:8000", logger, true))
		manager.SetSleepFunc(func(d time.Duration) {})

		// Call orchestratePlayback - should use the Tidal playlist since SoCo-CLI is configured
		err := manager.orchestratePlayback("wakeup", "test")
		require.NoError(t, err)

		manager.mu.RLock()
		currentlyPlaying := manager.currentlyPlaying
		manager.mu.RUnlock()

		require.NotNil(t, currentlyPlaying)
		assert.Equal(t, "https://tidal.com/browse/playlist/abc", currentlyPlaying.URI)
		assert.Equal(t, "tidal", currentlyPlaying.MediaType)
	})
}

func TestValidateTidalAvailability(t *testing.T) {
	t.Parallel()

	t.Run("logs warning for degraded modes", func(t *testing.T) {
		t.Parallel()
		core, logs := observer.New(zap.WarnLevel)
		logger := zap.New(core)

		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning": {
					PlaybackOptions: []PlaybackOption{
						{URI: "spotify:playlist:abc", MediaType: "playlist"},
						{URI: "https://tidal.com/browse/playlist/123", MediaType: "tidal"},
					},
				},
				"wakeup": {
					PlaybackOptions: []PlaybackOption{
						{URI: "https://tidal.com/browse/playlist/456", MediaType: "tidal"},
					},
				},
				"sleep": {
					PlaybackOptions: []PlaybackOption{
						{URI: "http://rain.example.com/rain.m3u", MediaType: "music"},
					},
				},
			},
		}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
		// socoClient is nil

		manager.validateTidalAvailability()

		// Should have logged a general warning and specific mode warnings
		allLogs := logs.All()
		assert.GreaterOrEqual(t, len(allLogs), 2, "expected at least 2 log entries (general + modes)")

		// Check for the general warning
		foundGeneral := false
		for _, entry := range allLogs {
			if entry.Message == "SoCo-CLI not configured: Tidal playlists will be skipped (set SOCO_CLI_URL to enable)" {
				foundGeneral = true
			}
		}
		assert.True(t, foundGeneral, "expected general SoCo-CLI warning")

		// Check for broken modes warning (wakeup has only Tidal)
		foundBroken := false
		for _, entry := range allLogs {
			if entry.Message == "Music modes with NO playable options (all playlists are Tidal)" {
				foundBroken = true
			}
		}
		assert.True(t, foundBroken, "expected broken modes warning for wakeup")
	})

	t.Run("no warnings when SoCo-CLI configured", func(t *testing.T) {
		t.Parallel()
		core, logs := observer.New(zap.WarnLevel)
		logger := zap.New(core)

		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning": {
					PlaybackOptions: []PlaybackOption{
						{URI: "https://tidal.com/browse/playlist/123", MediaType: "tidal"},
					},
				},
			},
		}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
		manager.SetSoCoClient(NewSoCoClient("http://127.0.0.1:8000", logger, false))

		manager.validateTidalAvailability()

		assert.Empty(t, logs.All(), "no warnings expected when SoCo-CLI is configured")
	})

	t.Run("no warnings when no Tidal playlists configured", func(t *testing.T) {
		t.Parallel()
		core, logs := observer.New(zap.WarnLevel)
		logger := zap.New(core)

		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning": {
					PlaybackOptions: []PlaybackOption{
						{URI: "spotify:playlist:abc", MediaType: "playlist"},
					},
				},
			},
		}

		manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)

		manager.validateTidalAvailability()

		assert.Empty(t, logs.All(), "no warnings expected when no Tidal playlists configured")
	})
}

func TestGetNextPlaylistIndex_BoundsCheck(t *testing.T) {
	t.Parallel()
	logger := zaptest.NewLogger(t)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)

	t.Run("index wraps when options count decreases", func(t *testing.T) {
		// Simulate having advanced to index 5 with 6 options
		manager.mu.Lock()
		manager.playlistNumbers["test_mode"] = 5
		manager.mu.Unlock()

		// Now call with only 2 options (e.g., after Tidal filtering)
		index := manager.getNextPlaylistIndex("test_mode", 2)
		manager.WaitForSync()

		// Index 5 % 2 = 1, which should be within bounds
		assert.Less(t, index, 2, "index should be within bounds of reduced options count")
		assert.Equal(t, 1, index)
	})

	t.Run("index 0 stays 0 with any count", func(t *testing.T) {
		manager.mu.Lock()
		manager.playlistNumbers["test_mode2"] = 0
		manager.mu.Unlock()

		index := manager.getNextPlaylistIndex("test_mode2", 3)
		manager.WaitForSync()

		assert.Equal(t, 0, index)
	})
}
