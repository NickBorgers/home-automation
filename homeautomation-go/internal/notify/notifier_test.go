package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func testConfig(restoreDelay int) *Config {
	return &Config{
		Notification: NotificationSettings{
			DefaultSpeakers:     []string{"media_player.kitchen", "media_player.bedroom"},
			AwakeVolumePercent:  60,
			AsleepVolumePercent: 30,
			TTSEntityID:         "tts.google_translate_en_com",
			SnapshotRestore:     true,
			RestoreDelaySeconds: restoreDelay,
		},
	}
}

func testNotifier(mockHA *ha.MockClient, stateMgr *state.Manager, readOnly bool, restoreDelay int) *TTSNotifier {
	return NewTTSNotifier(mockHA, stateMgr, testConfig(restoreDelay), zap.NewNop(), readOnly)
}

func volumeSetCalls(t *testing.T, mockHA *ha.MockClient) []ha.ServiceCall {
	t.Helper()
	var out []ha.ServiceCall
	for _, c := range mockHA.GetServiceCalls() {
		if c.Domain == "media_player" && c.Service == "volume_set" {
			out = append(out, c)
		}
	}
	return out
}

func ttsCalls(t *testing.T, mockHA *ha.MockClient) []ha.ServiceCall {
	t.Helper()
	var out []ha.ServiceCall
	for _, c := range mockHA.GetServiceCalls() {
		if c.Domain == "tts" && c.Service == "speak" {
			out = append(out, c)
		}
	}
	return out
}

func TestTTSNotifier_SpeakSetsVolumeBeforeTTS(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.2})
	mockHA.SetState("media_player.bedroom", "playing", map[string]interface{}{"volume_level": 0.3})

	n := testNotifier(mockHA, nil, false, 0)
	require.NoError(t, n.Speak(context.Background(), "hello", Routine, nil))

	calls := mockHA.GetServiceCalls()
	require.GreaterOrEqual(t, len(calls), 2, "expected at least volume_set and tts.speak calls")

	// volume_set must appear before tts.speak
	var volIdx, ttsIdx = -1, -1
	for i, c := range calls {
		if c.Domain == "media_player" && c.Service == "volume_set" && volIdx == -1 {
			volIdx = i
		}
		if c.Domain == "tts" && c.Service == "speak" {
			ttsIdx = i
		}
	}
	assert.NotEqual(t, -1, volIdx, "volume_set call expected")
	assert.NotEqual(t, -1, ttsIdx, "tts.speak call expected")
	assert.Less(t, volIdx, ttsIdx, "volume_set must precede tts.speak")

	// volume_set should use the awake volume
	ttsVol, _ := calls[volIdx].Data["volume_level"].(float64)
	assert.InDelta(t, 0.60, ttsVol, 0.001, "awake volume should be 60%%")
}

func TestTTSNotifier_RoutineUsesAsleepVolumeWhenAsleep(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.5})
	mockHA.SetState("media_player.bedroom", "playing", map[string]interface{}{"volume_level": 0.5})

	stateMgr := state.NewManager(mockHA, zap.NewNop(), false)
	require.NoError(t, stateMgr.SetBool("isMasterAsleep", true))

	n := testNotifier(mockHA, stateMgr, false, 0)
	require.NoError(t, n.Speak(context.Background(), "quiet announcement", Routine, nil))

	volCalls := volumeSetCalls(t, mockHA)
	require.NotEmpty(t, volCalls)
	vol, _ := volCalls[0].Data["volume_level"].(float64)
	assert.InDelta(t, 0.30, vol, 0.001, "routine announcement while asleep should use asleep volume (30%%)")
}

func TestTTSNotifier_UrgentAlwaysUsesAwakeVolume(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.5})
	mockHA.SetState("media_player.bedroom", "playing", map[string]interface{}{"volume_level": 0.5})

	stateMgr := state.NewManager(mockHA, zap.NewNop(), false)
	require.NoError(t, stateMgr.SetBool("isMasterAsleep", true))

	n := testNotifier(mockHA, stateMgr, false, 0)
	require.NoError(t, n.Speak(context.Background(), "urgent!", Urgent, nil))

	volCalls := volumeSetCalls(t, mockHA)
	require.NotEmpty(t, volCalls)
	vol, _ := volCalls[0].Data["volume_level"].(float64)
	assert.InDelta(t, 0.60, vol, 0.001, "urgent announcement should always use awake volume (60%%)")
}

func TestTTSNotifier_ReadOnlySkipsAllCalls(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.5})

	n := testNotifier(mockHA, nil, true, 0)
	require.NoError(t, n.Speak(context.Background(), "test", Routine, nil))

	assert.Empty(t, mockHA.GetServiceCalls(), "no HA calls expected in read-only mode")
}

func TestTTSNotifier_RestoresVolumesAfterDelay(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.2})
	mockHA.SetState("media_player.bedroom", "playing", map[string]interface{}{"volume_level": 0.4})

	// Use a very short restore delay for test speed.
	n := testNotifier(mockHA, nil, false, 0) // 0 seconds delay

	require.NoError(t, n.Speak(context.Background(), "hello", Routine, nil))

	// Wait for the restore goroutine to complete.
	time.Sleep(50 * time.Millisecond)

	volCalls := volumeSetCalls(t, mockHA)
	// Expect: 1 batch set (announcement volume) + 2 individual restores = 3 total
	assert.GreaterOrEqual(t, len(volCalls), 3, "expected batch volume_set plus restore calls")

	// Find the restore calls (individual, not batch).
	var kitchenRestored, bedroomRestored bool
	for _, c := range volCalls {
		if entityID, ok := c.Data["entity_id"].(string); ok {
			vol, _ := c.Data["volume_level"].(float64)
			if entityID == "media_player.kitchen" {
				assert.InDelta(t, 0.2, vol, 0.001, "kitchen should be restored to 0.2")
				kitchenRestored = true
			}
			if entityID == "media_player.bedroom" {
				assert.InDelta(t, 0.4, vol, 0.001, "bedroom should be restored to 0.4")
				bedroomRestored = true
			}
		}
	}
	assert.True(t, kitchenRestored, "kitchen volume should have been restored")
	assert.True(t, bedroomRestored, "bedroom volume should have been restored")
}

func TestTTSNotifier_TTSFailureRestoresImmediately(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{"volume_level": 0.2})
	mockHA.SetState("media_player.bedroom", "playing", map[string]interface{}{"volume_level": 0.4})
	mockHA.SetServiceError("tts", "speak", errors.New("tts unavailable"))

	n := testNotifier(mockHA, nil, false, 60) // long delay — restore should fire before it
	err := n.Speak(context.Background(), "hello", Routine, nil)
	assert.Error(t, err)

	// No goroutine delay — restore should have happened synchronously.
	volCalls := volumeSetCalls(t, mockHA)
	var kitchenRestored, bedroomRestored bool
	for _, c := range volCalls {
		if entityID, ok := c.Data["entity_id"].(string); ok {
			vol, _ := c.Data["volume_level"].(float64)
			if entityID == "media_player.kitchen" && vol == 0.2 {
				kitchenRestored = true
			}
			if entityID == "media_player.bedroom" && vol == 0.4 {
				bedroomRestored = true
			}
		}
	}
	assert.True(t, kitchenRestored, "kitchen should be restored immediately on TTS failure")
	assert.True(t, bedroomRestored, "bedroom should be restored immediately on TTS failure")
}

func TestTTSNotifier_CustomSpeakersOverrideDefaults(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	mockHA.SetState("media_player.office", "playing", map[string]interface{}{"volume_level": 0.5})

	n := testNotifier(mockHA, nil, false, 0)
	custom := []string{"media_player.office"}
	require.NoError(t, n.Speak(context.Background(), "office only", Routine, custom))

	tts := ttsCalls(t, mockHA)
	require.Len(t, tts, 1)
	speakers, _ := tts[0].Data["media_player_entity_id"].([]string)
	assert.Equal(t, custom, speakers)
}

func TestTTSNotifier_DefaultSpeakersFromConfig(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()

	n := testNotifier(mockHA, nil, false, 0)
	require.NoError(t, n.Speak(context.Background(), "everywhere", Routine, nil))

	tts := ttsCalls(t, mockHA)
	require.Len(t, tts, 1)
	speakers, _ := tts[0].Data["media_player_entity_id"].([]string)
	assert.Equal(t, testConfig(0).Notification.DefaultSpeakers, speakers)
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Parallel()
	cfg, err := LoadConfig("/nonexistent/notification_config.yaml")
	require.NoError(t, err)
	assert.Equal(t, 60, cfg.Notification.AwakeVolumePercent)
	assert.Equal(t, 30, cfg.Notification.AsleepVolumePercent)
	assert.Equal(t, "tts.google_translate_en_com", cfg.Notification.TTSEntityID)
	assert.True(t, cfg.Notification.SnapshotRestore)
	assert.NotEmpty(t, cfg.Notification.DefaultSpeakers)
}

func TestMockNotifier_RecordsCalls(t *testing.T) {
	t.Parallel()
	m := NewMockNotifier()
	assert.Equal(t, 0, m.CallCount())

	require.NoError(t, m.Speak(context.Background(), "hello", Routine, nil))
	require.NoError(t, m.Speak(context.Background(), "world", Urgent, []string{"media_player.kitchen"}))

	calls := m.GetCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, "hello", calls[0].Message)
	assert.Equal(t, Routine, calls[0].Urgency)
	assert.Nil(t, calls[0].Speakers)
	assert.Equal(t, "world", calls[1].Message)
	assert.Equal(t, Urgent, calls[1].Urgency)
	assert.Equal(t, []string{"media_player.kitchen"}, calls[1].Speakers)
}

func TestMockNotifier_SetError(t *testing.T) {
	t.Parallel()
	m := NewMockNotifier()
	m.SetError(errors.New("mock tts failure"))
	err := m.Speak(context.Background(), "test", Routine, nil)
	assert.Error(t, err)
	assert.Equal(t, 0, m.CallCount(), "failed call should not be recorded")
}
