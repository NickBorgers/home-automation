package vacuum

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/alert"
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
	cfg.Vacuum.Announcement.MessagePrefix = "Casper needs attention"
	cfg.Vacuum.Announcement.RepeatInterval = 2 * time.Hour
	cfg.Vacuum.Announcement.Speakers = []string{
		"media_player.kitchen",
		"media_player.sitting_room",
		"media_player.front_room",
		"media_player.kids_bathroom",
	}
	cfg.Vacuum.ClearAnnouncement.Message = "You have satisfied Casper"
	cfg.Vacuum.ClearAnnouncement.Speakers = []string{"media_player.sitting_room"}
	return cfg
}

func newTestManager(
	t *testing.T,
	readOnly bool,
	fixedTime time.Time,
) (*Manager, *ha.MockClient, *state.Manager, *alert.MockAlerter, *notify.MockNotifier) {
	t.Helper()
	logger := zap.NewNop()
	mockHA := ha.NewMockClient()
	stateMgr := state.NewManager(mockHA, logger, false)
	registry := shadowstate.NewSubscriptionRegistry()
	tp := plugin.FixedTimeProvider{FixedTime: fixedTime}
	mockAlerter := &alert.MockAlerter{}
	mockNotifier := &notify.MockNotifier{}

	mgr := NewManager(
		context.Background(),
		mockHA,
		stateMgr,
		mockAlerter,
		mockNotifier,
		newTestConfig(),
		logger,
		readOnly,
		tp,
		registry,
	)
	// Disable the background goroutine to keep tests fully synchronous;
	// tests drive repeats via TickRepeatForTest.
	mgr.SetRepeatCheckIntervalForTest(time.Hour)
	return mgr, mockHA, stateMgr, mockAlerter, mockNotifier
}

func setSensor(t *testing.T, mockHA *ha.MockClient, value string) {
	t.Helper()
	mockHA.SimulateStateChange(testErrorSensor, value)
}

func TestVacuum_NoErrorAtStartup_NoAnnouncement(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, time.Now())

	// Pre-seed the sensor in its healthy state before Start.
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	assert.Empty(t, mockAlerter.Calls(), "no announcement expected when sensor starts at No error")
	assert.Empty(t, mgr.GetShadowState().Outputs.CurrentError)
}

func TestVacuum_TransitionToError_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := mockAlerter.Calls()
	require.Len(t, calls, 1, "expected one announcement")
	assert.Equal(t,
		"Casper needs attention: Mop Dock Clean Water Tank empty",
		calls[0].Body)
	assert.Equal(t,
		[]string{
			"media_player.kitchen",
			"media_player.sitting_room",
			"media_player.front_room",
			"media_player.kids_bathroom",
		},
		calls[0].Speakers)
	assert.Equal(t, notify.UrgencyDeferable, calls[0].Urgency)

	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError)
	assert.Equal(t, 1, st.Outputs.AnnouncementsSinceErrorBegan)
}

func TestVacuum_SameErrorReemitted_NoExtraAnnouncement(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Len(t, mockAlerter.Calls(), 1)

	setSensor(t, mockHA, "Dustbin missing")
	assert.Len(t, mockAlerter.Calls(), 1, "same error string must not re-announce")
}

func TestVacuum_DifferentError_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := mockAlerter.Calls()
	require.Len(t, calls, 2)
	assert.Contains(t, calls[1].Body, "Mop Dock Clean Water Tank empty")
}

func TestVacuum_NotifierSuppressedAsleep_RecordsAndPreservesCadence(t *testing.T) {
	t.Parallel()
	// Asleep-suppression now lives in the shared notifier, not vacuum. Vacuum
	// passes UrgencyDeferable; the notifier returns ErrSuppressedAsleep when
	// master is asleep. Vacuum reacts by incrementing the suppress counter and
	// updating lastAnnouncedAt so the 2h repeat cadence still applies.
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, time.Now())
	mockAlerter.Err = notify.ErrSuppressedAsleep
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	calls := mockAlerter.Calls()
	require.Len(t, calls, 1, "vacuum still calls notifier; suppression is the notifier's decision")
	assert.Equal(t, notify.UrgencyDeferable, calls[0].Urgency,
		"vacuum errors must be Deferable so the notifier suppresses them while asleep")

	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError,
		"shadow state still tracks the error so the dashboard sees it")
	assert.Equal(t, 1, st.Outputs.SuppressedWhileAsleepCount)
}

func TestVacuum_RepeatAfterInterval(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, t0)
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Len(t, mockAlerter.Calls(), 1)

	// Tick before the repeat interval expires — no extra announcement.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(1 * time.Hour)}
	mgr.TickRepeatForTest()
	assert.Len(t, mockAlerter.Calls(), 1)

	// Tick after the 2h repeat interval — re-announces.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(2*time.Hour + time.Second)}
	mgr.TickRepeatForTest()
	assert.Len(t, mockAlerter.Calls(), 2, "expected re-announcement after repeat interval")
}

func TestVacuum_ErrorClears_StopsRepeating(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, t0)
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	require.Len(t, mockAlerter.Calls(), 1)

	// Error clears.
	setSensor(t, mockHA, "No error")
	assert.Empty(t, mgr.GetShadowState().Outputs.CurrentError)

	// Tick after a long time — must NOT re-announce, since the error is gone.
	mgr.timeProvider = plugin.FixedTimeProvider{FixedTime: t0.Add(10 * time.Hour)}
	mgr.TickRepeatForTest()
	assert.Len(t, mockAlerter.Calls(), 1, "no re-announcement once error has cleared")
}

func TestVacuum_ErrorClearsWhileSomeoneHome_AnnouncesClearTTS(t *testing.T) {
	t.Parallel()
	mgr, mockHA, stateMgr, _, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, stateMgr.SetBool("isAnyoneHome", true))
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	setSensor(t, mockHA, "No error")

	calls := mockNotifier.Calls()
	require.Len(t, calls, 1, "expected clear confirmation TTS")
	assert.Equal(t, "You have satisfied Casper", calls[0].Message)
	assert.Equal(t, []string{"media_player.sitting_room"}, calls[0].Speakers)
	assert.Equal(t, notify.UrgencyDeferable, calls[0].Urgency)
}

func TestVacuum_ErrorClearsWhileNobodyHome_NoClearTTS(t *testing.T) {
	t.Parallel()
	mgr, mockHA, stateMgr, _, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, stateMgr.SetBool("isAnyoneHome", false))
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	setSensor(t, mockHA, "No error")

	assert.Empty(t, mockNotifier.Calls(), "clear confirmation should not speak to an empty house")
}

func TestVacuum_NoErrorToNoError_NoClearTTS(t *testing.T) {
	t.Parallel()
	mgr, mockHA, stateMgr, _, mockNotifier := newTestManager(t, false, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, stateMgr.SetBool("isAnyoneHome", true))
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "No error")

	assert.Empty(t, mockNotifier.Calls(), "healthy-to-healthy updates should not announce")
}

func TestVacuum_ErrorClears_UsesClearAnnouncementConfigOverride(t *testing.T) {
	t.Parallel()
	mgr, mockHA, stateMgr, _, mockNotifier := newTestManager(t, false, time.Now())
	mgr.cfg.Vacuum.ClearAnnouncement.Message = "Robot status nominal"
	mgr.cfg.Vacuum.ClearAnnouncement.Speakers = []string{
		"media_player.sitting_room",
		"media_player.kitchen",
	}
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, stateMgr.SetBool("isAnyoneHome", true))
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Dustbin missing")
	setSensor(t, mockHA, "No error")

	calls := mockNotifier.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "Robot status nominal", calls[0].Message)
	assert.Equal(t, []string{"media_player.sitting_room", "media_player.kitchen"}, calls[0].Speakers)
}

func TestVacuum_ReadOnly_StillRecordsAndCallsNotifier(t *testing.T) {
	t.Parallel()
	// Vacuum no longer special-cases readOnly for TTS — that responsibility
	// moved to the notifier. The plugin still records to shadow state and
	// invokes the notifier; the notifier itself decides whether to actually
	// call Home Assistant.
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, true, time.Now())
	mockHA.SimulateStateChange(testErrorSensor, "No error")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	setSensor(t, mockHA, "Mop Dock Clean Water Tank empty")

	assert.Len(t, mockAlerter.Calls(), 1, "vacuum delegates to notifier in all modes")
	st := mgr.GetShadowState()
	assert.Equal(t, "Mop Dock Clean Water Tank empty", st.Outputs.CurrentError)
	assert.Equal(t, 1, st.Outputs.AnnouncementsSinceErrorBegan)
}

func TestVacuum_InitialErrorActiveAtStartup_Announces(t *testing.T) {
	t.Parallel()
	mgr, mockHA, _, mockAlerter, _ := newTestManager(t, false, time.Now())
	// Sensor is already reporting an error before the plugin starts.
	mockHA.SimulateStateChange(testErrorSensor, "Mop Dock Clean Water Tank empty")
	require.NoError(t, mgr.Start())
	defer mgr.Stop()

	calls := mockAlerter.Calls()
	require.Len(t, calls, 1, "an active error at startup should be announced once")
	assert.Contains(t, calls[0].Body, "Mop Dock Clean Water Tank empty")
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
	assert.Equal(t, defaultClearMessage, cfg.Vacuum.ClearAnnouncement.Message)
	assert.Equal(t, []string{defaultClearSpeakerEntityID}, cfg.Vacuum.ClearAnnouncement.Speakers)
}

func TestVacuum_LoadConfigInvalidDuration(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	cfg.Vacuum.Announcement.RepeatIntervalRaw = "not-a-duration"
	err := cfg.applyDefaultsAndValidate()
	assert.Error(t, err)
}
