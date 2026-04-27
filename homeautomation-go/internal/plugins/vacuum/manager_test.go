package vacuum

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const testErrorSensor = "sensor.valetudo_test_error"

func newTestConfig() *Config {
	cfg := &Config{}
	cfg.Vacuum.ErrorSensorID = testErrorSensor
	cfg.Vacuum.NoErrorValue = "No error"
	cfg.Vacuum.Announcement.MessagePrefix = "Robot vacuum needs attention"
	cfg.Vacuum.Announcement.RepeatInterval = 2 * time.Hour
	cfg.Vacuum.Announcement.SuppressWhileMasterAsleep = true
	cfg.Vacuum.Announcement.Speakers = []string{
		"media_player.kitchen",
		"media_player.sitting_room",
		"media_player.front_room",
		"media_player.soundbar",
		"media_player.kids_bathroom",
	}
	return cfg
}

func newTestManager(t *testing.T, readOnly bool, fixedTime time.Time) (*Manager, *ha.MockClient, *state.Manager) {
	t.Helper()
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	tp := plugin.FixedTimeProvider{FixedTime: fixedTime}

	mgr := NewManager(context.Background(), mockHA, stateMgr, newTestConfig(), logger, readOnly, tp, registry)
	// Disable the background goroutine to keep tests fully synchronous;
	// tests drive repeats via TickRepeatForTest.
	mgr.SetRepeatCheckIntervalForTest(time.Hour)
	return mgr, mockHA, stateMgr
}

func ttsCalls(t *testing.T, m *ha.MockClient) []ha.ServiceCall {
	t.Helper()
	calls := m.GetServiceCalls()
	out := make([]ha.ServiceCall, 0, len(calls))
	for _, c := range calls {
		if c.Domain == "tts" && c.Service == "speak" {
			out = append(out, c)
		}
	}
	return out
}

func setSensor(t *testing.T, mockHA *ha.MockClient, value string) {
	t.Helper()
	mockHA.SimulateStateChange(testErrorSensor, value)
}

func TestVacuum_NoErrorAtStartup_NoAnnouncement(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _ := newTestManager(t, false, time.Now())

	// Pre-seed the sensor in its healthy state before Start.
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	assert.Empty(t, ttsCalls(t, mockHA), "no TTS call expected when sensor starts at No error")
	assert.Empty(t, mgr.GetShadowState().Outputs.CurrentError)
}

func TestVacuum_TransitionToError_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _ := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := ttsCalls(t, mockHA)
	require.Len(t, calls, 1, "expected one TTS announcement")
	assert.Equal(t, "tts.google_translate_en_com", calls[0].Data["entity_id"])
	assert.Equal(t,
		"Robot vacuum needs attention: Mop Dock Clean Water Tank empty",
		calls[0].Data["message"])
	assert.Equal(t,
		[]string{
			"media_player.kitchen",
			"media_player.sitting_room",
			"media_player.front_room",
			"media_player.soundbar",
			"media_player.kids_bathroom",
		},
		calls[0].Data["media_player_entity_id"])
	assert.Equal(t, true, calls[0].Data["cache"])

	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError)
	assert.Equal(t, 1, st.Outputs.AnnouncementsSinceErrorBegan)
}

func TestVacuum_SameErrorReemitted_NoExtraAnnouncement(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _ := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Len(t, ttsCalls(t, mockHA), 1)

	setSensor(t, mockHA, "Dustbin missing")
	assert.Len(t, ttsCalls(t, mockHA), 1, "same error string must not re-announce")
}

func TestVacuum_DifferentError_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _ := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := ttsCalls(t, mockHA)
	require.Len(t, calls, 2)
	assert.Contains(t, calls[1].Data["message"], "Mop Dock Clean Water Tank empty")
}

func TestVacuum_SuppressedWhileMasterAsleep(t *testing.T) {
	t.Parallel()
	mgr, mockHA, stateMgr := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, stateMgr.SetBool("isMasterAsleep", true))
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	assert.Empty(t, ttsCalls(t, mockHA), "TTS must not fire while master is asleep")

	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError,
		"shadow state still tracks the error so the dashboard sees it")
	assert.Equal(t, 1, st.Outputs.SuppressedWhileAsleepCount)
}

func TestVacuum_RepeatAfterInterval(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	mgr, mockHA, _ := newTestManager(t, false, t0)
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Len(t, ttsCalls(t, mockHA), 1)

	// Tick before the repeat interval expires — no extra announcement.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(1 * time.Hour)}
	mgr.TickRepeatForTest()
	assert.Len(t, ttsCalls(t, mockHA), 1)

	// Tick after the 2h repeat interval — re-announces.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(2*time.Hour + time.Second)}
	mgr.TickRepeatForTest()
	assert.Len(t, ttsCalls(t, mockHA), 2, "expected re-announcement after repeat interval")
}

func TestVacuum_ErrorClears_StopsRepeating(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	mgr, mockHA, _ := newTestManager(t, false, t0)
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Len(t, ttsCalls(t, mockHA), 1)

	// Error clears.
	setSensor(t, mockHA, "No error")
	assert.Empty(t, mgr.GetShadowState().Outputs.CurrentError)

	// Tick after a long time — must NOT re-announce, since the error is gone.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(10 * time.Hour)}
	mgr.TickRepeatForTest()
	assert.Len(t, ttsCalls(t, mockHA), 1, "no re-announcement once error has cleared")
}

func TestVacuum_ReadOnly_SkipsTTSCall(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _ := newTestManager(t, true, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	assert.Empty(t, ttsCalls(t, mockHA), "READ_ONLY mode must not call HA tts.speak")
	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError)
	assert.Equal(t, 1, st.Outputs.AnnouncementsSinceErrorBegan,
		"shadow state still records what would have been announced")
}

func TestVacuum_InitialErrorActiveAtStartup_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _ := newTestManager(t, false, time.Now())
	// Sensor is already reporting an error before the plugin starts.
	mockHA.SimulateStateChange(testErrorSensor, "Mop Dock Clean Water Tank empty")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	calls := ttsCalls(t, mockHA)
	require.Len(t, calls, 1, "an active error at startup should be announced once")
	assert.Contains(t, calls[0].Data["message"], "Mop Dock Clean Water Tank empty")
}

func TestVacuum_LoadConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	require.NoError(t, cfg.applyDefaultsAndValidate())
	assert.Equal(t, defaultErrorSensorID, cfg.Vacuum.ErrorSensorID)
	assert.Equal(t, defaultNoErrorValue, cfg.Vacuum.NoErrorValue)
	assert.Equal(t, defaultMessagePrefix, cfg.Vacuum.Announcement.MessagePrefix)
	assert.Equal(t, defaultRepeatInterval, cfg.Vacuum.Announcement.RepeatInterval)
	assert.Equal(t, defaultSpeakers, cfg.Vacuum.Announcement.Speakers)
}

func TestVacuum_LoadConfigInvalidDuration(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	cfg.Vacuum.Announcement.RepeatIntervalRaw = "not-a-duration"
	err := cfg.applyDefaultsAndValidate()
	assert.Error(t, err)
}
