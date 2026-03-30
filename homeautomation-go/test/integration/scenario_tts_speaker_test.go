package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ============================================================================
// TTS Speaker Selection Tests
//
// These tests validate that TTS arrival announcements target the Sonos group
// leader when music is playing, rather than individual speakers that may be
// group members. Sending TTS to individual group members breaks the entire
// group's playback.
//
// INVARIANT: When music is playing, TTS must target ONLY the group leader.
// ============================================================================

// ttsSpeakerEnv holds plugins for TTS speaker selection tests
type ttsSpeakerEnv struct {
	server        *MockHAServer
	client        *ha.Client
	stateManager  *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	music         *music.Manager
}

func setupTTSSpeakerTest(t *testing.T) (*ttsSpeakerEnv, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load music config
	data, err := os.ReadFile("testdata/music_config_test.yaml")
	require.NoError(t, err)
	var musicConfig music.MusicConfig
	require.NoError(t, yaml.Unmarshal(data, &musicConfig))

	// Create plugins
	env := &ttsSpeakerEnv{
		server:        server,
		client:        client,
		stateManager:  manager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, manager, logger, false, nil),
		music:         music.NewManager(context.Background(), client, manager, &musicConfig, logger, false, nil, nil),
	}

	// Set up media player entities
	for _, name := range []string{"kitchen", "bedroom", "sitting_room", "front_room", "kids_bathroom", "primary_bathroom"} {
		server.SetState("media_player."+name, "idle", map[string]interface{}{
			"volume_level": 0.1,
		})
	}

	// Make music plugin skip sleeps during tests
	env.music.SetSleepFunc(func(d time.Duration) {})
	// Use immediate debounce for deterministic tests
	env.music.SetDebounceDelay(0)

	// Start plugins
	require.NoError(t, env.stateTracking.Start())
	require.NoError(t, env.music.Start())

	waitForProcessing(t, manager)

	cleanup := func() {
		env.music.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// TestScenario_TTSSpeaker_MusicPlaying_TargetsGroupLeader validates that when
// music is playing on a speaker group, TTS announcements target only the group
// leader instead of individual speakers.
//
// User story: "When Caroline arrives while morning music is playing on
// Kitchen (lead) + Sitting Room + Bedroom, the TTS should go to Kitchen only
// so it doesn't break the Sonos group."
func TestScenario_TTSSpeaker_MusicPlaying_TargetsGroupLeader(t *testing.T) {
	t.Parallel()
	env, cleanup := setupTTSSpeakerTest(t)
	defer cleanup()

	t.Log("========== TEST: TTS targets group leader when music is playing ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Morning music is playing with Kitchen as lead speaker")

	// Set up conditions for morning music
	server := env.server
	server.SetState("input_boolean.nick_home", "on", nil)
	waitForProcessing(t, env.stateManager)

	server.SetState("input_text.day_phase", "morning", nil)
	waitForProcessing(t, env.stateManager)

	// Wait for music to start (zone resolution)
	waitForCondition(t, func() bool {
		var cpMusic map[string]interface{}
		if err := env.stateManager.GetJSON("currentlyPlayingMusic", &cpMusic); err != nil {
			return false
		}
		lead, _ := cpMusic["leadPlayer"].(string)
		return lead != ""
	}, "Music should start playing with a lead player")

	// Verify the lead player is Kitchen (first participant in morning config)
	var cpMusic map[string]interface{}
	err := env.stateManager.GetJSON("currentlyPlayingMusic", &cpMusic)
	require.NoError(t, err)
	assert.Equal(t, "Kitchen", cpMusic["leadPlayer"], "Kitchen should be the morning music lead player")

	// ========== WHEN ==========
	t.Log("WHEN: Caroline arrives home (while Nick is already home)")

	// Take snapshot to capture the TTS service call
	snapshot := server.ServiceCallCount()

	// Trigger Caroline's arrival
	server.SetState("input_boolean.caroline_home", "on", nil)
	waitForProcessing(t, env.stateManager)

	// Wait for the TTS service call
	waitForServiceCallSince(t, server, snapshot, "tts", "speak", "TTS announcement should be made")

	// ========== THEN ==========
	t.Log("THEN: TTS should target only media_player.kitchen (the group leader)")

	calls := server.GetServiceCallsSince(snapshot)
	ttsCalls := filterServiceCalls(calls, "tts", "speak")
	require.NotEmpty(t, ttsCalls, "Should have at least one TTS call")

	// Check that the TTS targets the group leader only
	lastTTS := ttsCalls[len(ttsCalls)-1]
	mediaPlayers := lastTTS.ServiceData["media_player_entity_id"]

	// The media_player_entity_id should be a list containing only the group leader
	playerList, ok := mediaPlayers.([]interface{})
	if ok {
		assert.Len(t, playerList, 1, "TTS should target exactly 1 speaker (the group leader)")
		assert.Equal(t, "media_player.kitchen", playerList[0], "TTS should target the group leader")
	} else {
		// May come as []string
		playerStrList, ok := mediaPlayers.([]string)
		require.True(t, ok, "media_player_entity_id should be a list, got %T", mediaPlayers)
		assert.Len(t, playerStrList, 1, "TTS should target exactly 1 speaker (the group leader)")
		assert.Equal(t, "media_player.kitchen", playerStrList[0], "TTS should target the group leader")
	}
}

// TestScenario_TTSSpeaker_NoMusic_UsesDefaults validates that when no music is
// playing, TTS announcements use the default speaker list.
// This test uses only the statetracking plugin (no music plugin) to guarantee
// currentlyPlayingMusic remains empty.
func TestScenario_TTSSpeaker_NoMusic_UsesDefaults(t *testing.T) {
	t.Parallel()

	// Set up base test infrastructure (no music plugin)
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	logger := testlogger.New()
	st := statetracking.NewManager(context.Background(), client, manager, logger, false, nil)
	require.NoError(t, st.Start())
	defer st.Stop()

	waitForProcessing(t, manager)

	t.Log("========== TEST: TTS uses default speakers when no music ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Nick is home, no music plugin running (currentlyPlayingMusic is empty)")

	server.SetState("input_boolean.nick_home", "on", nil)
	waitForProcessing(t, manager)

	// ========== WHEN ==========
	t.Log("WHEN: Caroline arrives home")

	snapshot := server.ServiceCallCount()

	server.SetState("input_boolean.caroline_home", "on", nil)
	waitForProcessing(t, manager)

	// Wait for TTS
	waitForServiceCallSince(t, server, snapshot, "tts", "speak", "TTS announcement should be made")

	// ========== THEN ==========
	t.Log("THEN: TTS should target the default speaker list (multiple speakers)")

	calls := server.GetServiceCallsSince(snapshot)
	ttsCalls := filterServiceCalls(calls, "tts", "speak")
	require.NotEmpty(t, ttsCalls)

	lastTTS := ttsCalls[len(ttsCalls)-1]
	mediaPlayers := lastTTS.ServiceData["media_player_entity_id"]

	// Should have multiple default speakers
	switch pl := mediaPlayers.(type) {
	case []interface{}:
		assert.Greater(t, len(pl), 1, "Default TTS should target multiple speakers")
	case []string:
		assert.Greater(t, len(pl), 1, "Default TTS should target multiple speakers")
	default:
		t.Fatalf("Unexpected media_player_entity_id type: %T", mediaPlayers)
	}
}
