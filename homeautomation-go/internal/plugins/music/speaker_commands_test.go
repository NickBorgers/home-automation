package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
// Uses zap.NewNop() to avoid panics when goroutines spawned by executePlayback
// (fade-in, speaker group building) outlive the test and attempt to log.
func setupSoCoForTest(t *testing.T, manager *Manager, readOnly bool) *threadSafePaths {
	t.Helper()
	server, paths := mockSoCoServer(t)
	socoClient := NewSoCoClient(server.URL, zap.NewNop(), readOnly)
	manager.SetSoCoClient(socoClient)
	return paths
}

// setupFailingGroupJoinSoCo creates a mock SoCo-CLI server where group join operations
// always fail (exit_code=1) while all other operations succeed. This is used to test
// that pre-muting happens before the async join attempt (issue #998): if a follower
// speaker fails to join the group, it should still have been pre-muted so it doesn't
// play audio unmuted as a standalone speaker.
func setupFailingGroupJoinSoCo(t *testing.T, manager *Manager) *threadSafePaths {
	t.Helper()
	recorded := &threadSafePaths{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded.Append(r.URL.Path)

		resp := SoCoResponse{
			Speaker: "test",
			Action:  "test",
		}

		// Return error for group join operations: /{follower}/group/{lead}
		if containsGroupJoin(r.URL.Path) {
			resp.ExitCode = 1
			resp.ErrorMsg = "forced group join failure (test)"
			resp.Result = "error"
		} else {
			resp.ExitCode = 0
			resp.Result = "success"
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "encode failed", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	socoClient := NewSoCoClient(server.URL, zap.NewNop(), false)
	manager.SetSoCoClient(socoClient)
	return recorded
}

// containsGroupJoin returns true for SoCo paths that represent a group join operation:
// /{follower}/group/{lead} — e.g., /Kitchen/group/Front%20Room
func containsGroupJoin(path string) bool {
	return strings.Contains(path, "/group/")
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
	assert.Equal(t, "/Kitchen/mute/off", paths.Get(1))
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

func TestSpeakerJoinGroupBatch_SoCoContinuesOnError(t *testing.T) {
	t.Parallel()
	manager, _ := createTestManager(t)

	var callCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&callCount, 1)
		if attempt == 1 {
			// First speaker fails with non-retryable SoCo error
			resp := SoCoResponse{ExitCode: 1, ErrorMsg: "speaker not found"}
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp := SoCoResponse{Result: "success", ExitCode: 0}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	socoClient := NewSoCoClient(server.URL, zaptest.NewLogger(t), false)
	manager.SetSoCoClient(socoClient)

	err := manager.speakerJoinGroupBatch("Kitchen", []string{"Bedroom", "Front Room"})
	// Should return an error (first speaker failed)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "speaker not found")
	// But should have attempted both speakers (not stopped at first error)
	assert.Equal(t, int32(2), atomic.LoadInt32(&callCount),
		"should attempt all speakers even when one fails")
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

	// Queue-based playback: clear_queue → add_uri_to_queue → play_from_queue
	allPaths := paths.All()
	require.Len(t, allPaths, 3)
	assert.Equal(t, "/Kitchen/clear_queue", allPaths[0])
	assert.Contains(t, allPaths[1], "/Kitchen/add_uri_to_queue/")
	assert.Equal(t, "/Kitchen/play_from_queue", allPaths[2])
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
