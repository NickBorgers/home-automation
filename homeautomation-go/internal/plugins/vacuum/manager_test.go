package vacuum

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
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

func newTestManager(t *testing.T, readOnly bool, fixedTime time.Time) (*Manager, *ha.MockClient, *state.Manager, *notify.MockNotifier) {
	t.Helper()
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	tp := plugin.FixedTimeProvider{FixedTime: fixedTime}
	mockNotifier := notify.NewMockNotifier()

	mgr := NewManager(context.Background(), mockHA, stateMgr, newTestConfig(), logger, readOnly, tp, registry, mockNotifier)
	// Disable the background goroutine to keep tests fully synchronous;
	// tests drive repeats via TickRepeatForTest.
	mgr.SetRepeatCheckIntervalForTest(time.Hour)
	return mgr, mockHA, stateMgr, mockNotifier
}

func setSensor(t *testing.T, mockHA *ha.MockClient, value string) {
	t.Helper()
	mockHA.SimulateStateChange(testErrorSensor, value)
}

func TestVacuum_NoErrorAtStartup_NoAnnouncement(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, time.Now())

	// Pre-seed the sensor in its healthy state before Start.
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	assert.Equal(t, 0, mockNotifier.CallCount(), "no TTS call expected when sensor starts at No error")
	assert.Empty(t, mgr.GetShadowState().Outputs.CurrentError)
}

func TestVacuum_TransitionToError_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := mockNotifier.GetCalls()
	require.Len(t, calls, 1, "expected one TTS announcement")
	assert.Equal(t,
		"Robot vacuum needs attention: Mop Dock Clean Water Tank empty",
		calls[0].Message)
	assert.Equal(t, notify.Routine, calls[0].Urgency)

	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError)
	assert.Equal(t, 1, st.Outputs.AnnouncementsSinceErrorBegan)
}

func TestVacuum_SameErrorReemitted_NoExtraAnnouncement(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Equal(t, 1, mockNotifier.CallCount())

	setSensor(t, mockHA, "Dustbin missing")
	assert.Equal(t, 1, mockNotifier.CallCount(), "same error string must not re-announce")
}

func TestVacuum_DifferentError_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := mockNotifier.GetCalls()
	require.Len(t, calls, 2)
	assert.Contains(t, calls[1].Message, "Mop Dock Clean Water Tank empty")
}

func TestVacuum_SuppressedWhileMasterAsleep(t *testing.T) {
	t.Parallel()
	mgr, mockHA, stateMgr, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, stateMgr.SetBool("isMasterAsleep", true))
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	assert.Equal(t, 0, mockNotifier.CallCount(), "TTS must not fire while master is asleep")

	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError,
		"shadow state still tracks the error so the dashboard sees it")
	assert.Equal(t, 1, st.Outputs.SuppressedWhileAsleepCount)
}

func TestVacuum_RepeatAfterInterval(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, t0)
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Equal(t, 1, mockNotifier.CallCount())

	// Tick before the repeat interval expires — no extra announcement.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(1 * time.Hour)}
	mgr.TickRepeatForTest()
	assert.Equal(t, 1, mockNotifier.CallCount())

	// Tick after the 2h repeat interval — re-announces.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(2*time.Hour + time.Second)}
	mgr.TickRepeatForTest()
	assert.Equal(t, 2, mockNotifier.CallCount(), "expected re-announcement after repeat interval")
}

func TestVacuum_ErrorClears_StopsRepeating(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, t0)
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Equal(t, 1, mockNotifier.CallCount())

	// Error clears.
	setSensor(t, mockHA, "No error")
	assert.Empty(t, mgr.GetShadowState().Outputs.CurrentError)

	// Tick after a long time — must NOT re-announce, since the error is gone.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(10 * time.Hour)}
	mgr.TickRepeatForTest()
	assert.Equal(t, 1, mockNotifier.CallCount(), "no re-announcement once error has cleared")
}

func TestVacuum_ReadOnly_SkipsTTSCall(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockNotifier := newTestManager(t, true, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	assert.Equal(t, 0, mockNotifier.CallCount(), "READ_ONLY mode must not call TTS notifier")
	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError)
	assert.Equal(t, 1, st.Outputs.AnnouncementsSinceErrorBegan,
		"shadow state still records what would have been announced")
}

func TestVacuum_InitialErrorActiveAtStartup_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockNotifier := newTestManager(t, false, time.Now())
	// Sensor is already reporting an error before the plugin starts.
	mockHA.SimulateStateChange(testErrorSensor, "Mop Dock Clean Water Tank empty")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	calls := mockNotifier.GetCalls()
	require.Len(t, calls, 1, "an active error at startup should be announced once")
	assert.Contains(t, calls[0].Message, "Mop Dock Clean Water Tank empty")
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
