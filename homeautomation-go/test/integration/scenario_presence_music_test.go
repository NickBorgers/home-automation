package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

func TestScenario_OwnerArrivesBeforeBedtime_SleepPrepUsesCurrentPresence(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	logger := testlogger.New()
	require.NoError(t, stateManager.SetupComputedStateV2())
	defer stateManager.StopComputedState()

	stateTracking := statetracking.NewManager(
		context.Background(), client, stateManager, logger, false, nil, "", &alert.MockAlerter{},
	)
	require.NoError(t, stateTracking.Start())
	defer stateTracking.Stop()

	musicManager := music.NewManager(
		context.Background(), client, stateManager, loadTestMusicConfigFromYAML(t), logger, false, nil, nil,
	)
	musicManager.SetSleepFunc(func(time.Duration) {})
	require.NoError(t, musicManager.Start())
	defer musicManager.Stop()

	// GIVEN: Nobody is home shortly before the bedtime automation runs.
	t.Log("GIVEN: nobody is home shortly before bedtime")
	server.SetState("input_boolean.nick_home", "off", nil)
	server.SetState("input_boolean.caroline_home", "off", nil)
	server.SetState("input_boolean.assistant_here", "off", nil)
	server.SetState("input_text.day_phase", "night", nil)
	server.SetState("input_boolean.master_asleep", "off", nil)
	waitForBoolState(t, stateManager, "isAnyoneHome", false)

	// WHEN: An owner arrives, then go_to_bed requests sleep music.
	t.Log("WHEN: an owner arrives immediately before go_to_bed requests sleep music")
	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, stateManager, "isAnyoneHome", true)
	// Setting musicPlaybackType=sleep mirrors what the HA go_to_bed automation does; it does not
	// activate sleep-prep (that depends on dayPhase=night + isAnyoneHome=true + isMasterAsleep=false).
	require.NoError(t, stateManager.SetString("musicPlaybackType", "sleep"))

	// THEN: The authoritative state, State Tracking shadow, and Music zone agree.
	// INVARIANT: derived presence exposed by State Tracking must match the value consumed by Music.
	t.Log("THEN: State Tracking and Music both use current presence without a manual reset")
	require.Eventually(t, func() bool {
		shadow := stateTracking.GetShadowState()
		return shadow.Outputs.DerivedStates.IsAnyOwnerHome && shadow.Outputs.DerivedStates.IsAnyoneHome
	}, stateWaitTimeout, statePollInterval)
	require.Eventually(t, func() bool {
		for _, zone := range musicManager.GetActiveZones() {
			if zone.Name == "sleep-prep" {
				return true
			}
		}
		return false
	}, stateWaitTimeout, statePollInterval, "sleep-prep should start without a manual Music reset")
}
