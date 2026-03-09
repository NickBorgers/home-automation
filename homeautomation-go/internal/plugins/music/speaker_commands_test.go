package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// =============================================================================
// Speaker Command Routing Tests
// =============================================================================
//
// These tests verify that speaker commands are correctly routed to either
// SoCo-CLI (when configured) or Home Assistant (fallback).
//
// Key behaviors tested:
// - When socoClient is nil, all commands go through HA service calls
// - When socoClient is set, all commands go through SoCo-CLI HTTP API
// - Parameters are correctly translated between the two paths

// mockSoCoServer creates a test HTTP server that records SoCo-CLI API calls
func mockSoCoServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		resp := SoCoResponse{
			Result:   "success",
			ExitCode: 0,
			Speaker:  "test",
			Action:   "test",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)
	return server, &paths
}

// createTestManager creates a Manager wired up to a mock HA client for testing.
func createTestManager(t *testing.T) (*Manager, *ha.MockClient) {
	t.Helper()
	logger := zaptest.NewLogger(t)
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 15},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://example.com/music.mp3", MediaType: "music", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, nil, nil)
	return manager, mockClient
}

func TestSpeakerSetVolume_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	// No SoCo client configured - should use HA
	err := manager.speakerSetVolume("Kitchen", 50)
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "media_player", calls[0].Domain)
	assert.Equal(t, "volume_set", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
	assert.Equal(t, 0.5, calls[0].Data["volume_level"])
}

func TestSpeakerSetVolume_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerSetVolume("Kitchen", 75)
	require.NoError(t, err)

	// Should NOT have called HA
	assert.Empty(t, mockClient.GetServiceCalls(), "HA should not be called when SoCo is configured")

	// Should have called SoCo
	require.Len(t, *paths, 1)
	assert.Equal(t, "/Kitchen/volume/75", (*paths)[0])
}

func TestSpeakerSetMute_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerSetMute("Front Room", true)
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "volume_mute", calls[0].Service)
	assert.Equal(t, "media_player.front_room", calls[0].Data["entity_id"])
	assert.Equal(t, true, calls[0].Data["is_volume_muted"])
}

func TestSpeakerSetMute_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	// Test mute
	err := manager.speakerSetMute("Kitchen", true)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/mute", (*paths)[0])

	// Test unmute
	err = manager.speakerSetMute("Kitchen", false)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/unmute", (*paths)[1])
}

func TestSpeakerJoinGroup_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerJoinGroup("Bedroom", "Kitchen")
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "join", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
	groupMembers, ok := calls[0].Data["group_members"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"media_player.bedroom"}, groupMembers)
}

func TestSpeakerJoinGroup_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerJoinGroup("Bedroom", "Kitchen")
	require.NoError(t, err)

	require.Len(t, *paths, 1)
	assert.Equal(t, "/Bedroom/group/Kitchen", (*paths)[0])
}

func TestSpeakerJoinGroupBatch_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerJoinGroupBatch("Kitchen", []string{"Bedroom", "Front Room"})
	require.NoError(t, err)

	// SoCo joins each follower individually
	require.Len(t, *paths, 2)
	assert.Equal(t, "/Bedroom/group/Kitchen", (*paths)[0])
	// URL path is decoded by Go's HTTP server, so spaces appear as-is
	assert.Equal(t, "/Front Room/group/Kitchen", (*paths)[1])
}

func TestSpeakerJoinGroupBatch_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerJoinGroupBatch("Kitchen", []string{"Bedroom", "Front Room"})
	require.NoError(t, err)

	// HA uses a single batch call
	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "join", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
	groupMembers, ok := calls[0].Data["group_members"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"media_player.bedroom", "media_player.front_room"}, groupMembers)
}

func TestSpeakerUnjoin_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerUnjoin("Kitchen")
	require.NoError(t, err)

	require.Len(t, *paths, 1)
	assert.Equal(t, "/Kitchen/ungroup", (*paths)[0])
}

func TestSpeakerUnjoin_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerUnjoin("Kitchen")
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "unjoin", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
}

func TestSpeakerPlay_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerPlay("Kitchen")
	require.NoError(t, err)

	require.Len(t, *paths, 1)
	assert.Equal(t, "/Kitchen/play", (*paths)[0])
}

func TestSpeakerPlay_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerPlay("Kitchen")
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "media_play", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
}

func TestSpeakerPlayMedia_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerPlayMedia("Kitchen", "http://example.com/stream.mp3", "music")
	require.NoError(t, err)

	require.Len(t, *paths, 1)
	assert.Contains(t, (*paths)[0], "/Kitchen/play_uri/")
}

func TestSpeakerPlayMedia_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerPlayMedia("Kitchen", "http://example.com/stream.mp3", "music")
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "play_media", calls[0].Service)
	assert.Equal(t, "http://example.com/stream.mp3", calls[0].Data["media_content_id"])
	assert.Equal(t, "music", calls[0].Data["media_content_type"])
}

func TestSpeakerSetShuffle_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerSetShuffle("Kitchen", true)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/shuffle/on", (*paths)[0])

	err = manager.speakerSetShuffle("Kitchen", false)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/shuffle/off", (*paths)[1])
}

func TestSpeakerSetRepeat_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerSetRepeat("Kitchen", "all")
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/repeat/all", (*paths)[0])
}

func TestSpeakerCommandPath_ReportsCorrectly(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	// Without SoCo client
	assert.Equal(t, "home-assistant", manager.speakerCommandPath())

	// With SoCo client
	server, _ := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)
	assert.Contains(t, manager.speakerCommandPath(), "soco-cli")
}
