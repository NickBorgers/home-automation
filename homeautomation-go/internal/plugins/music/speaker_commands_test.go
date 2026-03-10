package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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

// threadSafePaths provides thread-safe recording of HTTP request paths.
// Used by mockSoCoServer to avoid data races when tests run in parallel.
type threadSafePaths struct {
	mu    sync.Mutex
	paths []string
}

func (p *threadSafePaths) Append(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, path)
}

func (p *threadSafePaths) Get(index int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paths[index]
}

func (p *threadSafePaths) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.paths)
}

func (p *threadSafePaths) All() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]string, len(p.paths))
	copy(result, p.paths)
	return result
}

// mockSoCoServer creates a test HTTP server that records SoCo-CLI API calls.
// The returned threadSafePaths is safe for concurrent access from HTTP handler
// goroutines and test goroutines.
func mockSoCoServer(t *testing.T) (*httptest.Server, *threadSafePaths) {
	t.Helper()
	recorded := &threadSafePaths{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.Append(r.URL.Path)
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
	return server, recorded
}

// setupSoCoForTest creates a mock SoCo-CLI server and wires it into the manager.
// Use this for tests with media_type "tidal" to exercise the production code path.
func setupSoCoForTest(t *testing.T, manager *Manager, readOnly bool) *threadSafePaths {
	t.Helper()
	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), readOnly)
	manager.SetSoCoClient(socoClient)
	return paths
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
	require.Equal(t, 1, paths.Len())
	assert.Equal(t, "/Kitchen/volume/75", paths.Get(0))
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
	assert.Equal(t, "/Kitchen/mute", paths.Get(0))

	// Test unmute
	err = manager.speakerSetMute("Kitchen", false)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/unmute", paths.Get(1))
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

	require.Equal(t, 1, paths.Len())
	assert.Equal(t, "/Bedroom/group/Kitchen", paths.Get(0))
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
	require.Equal(t, 2, paths.Len())
	assert.Equal(t, "/Bedroom/group/Kitchen", paths.Get(0))
	// URL path is decoded by Go's HTTP server, so spaces appear as-is
	assert.Equal(t, "/Front Room/group/Kitchen", paths.Get(1))
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

	require.Equal(t, 1, paths.Len())
	assert.Equal(t, "/Kitchen/ungroup", paths.Get(0))
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

	require.Equal(t, 1, paths.Len())
	assert.Equal(t, "/Kitchen/play", paths.Get(0))
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

	require.Equal(t, 1, paths.Len())
	assert.Contains(t, paths.Get(0), "/Kitchen/play_uri/")
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

func TestSpeakerUnjoinBestEffort_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerUnjoinBestEffort("Kitchen", 5*time.Second)
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "unjoin", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
}

func TestSpeakerSetShuffle_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerSetShuffle("Kitchen", true)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/shuffle/on", paths.Get(0))

	err = manager.speakerSetShuffle("Kitchen", false)
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/shuffle/off", paths.Get(1))
}

func TestSpeakerSetShuffle_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerSetShuffle("Kitchen", true)
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "shuffle_set", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
	assert.Equal(t, true, calls[0].Data["shuffle"])
}

func TestSpeakerSetRepeat_RoutesThroughSoCo(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerSetRepeat("Kitchen", "all")
	require.NoError(t, err)
	assert.Equal(t, "/Kitchen/repeat/all", paths.Get(0))
}

func TestSpeakerSetRepeat_FallsBackToHA(t *testing.T) {
	t.Parallel()
	manager, mockClient := createTestManager(t)

	err := manager.speakerSetRepeat("Kitchen", "all")
	require.NoError(t, err)

	calls := mockClient.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "repeat_set", calls[0].Service)
	assert.Equal(t, "media_player.kitchen", calls[0].Data["entity_id"])
	assert.Equal(t, "all", calls[0].Data["repeat"])
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
