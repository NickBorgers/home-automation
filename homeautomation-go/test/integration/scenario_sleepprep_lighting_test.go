package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/config"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Sleep Prep + Lighting Integration Tests
//
// These tests validate the fix for the chicken-and-egg bug where the lighting
// plugin re-activated bedroom lights after manual turn-off during sleep prep,
// preventing isMasterAsleep from ever triggering.
//
// Root cause: Between go_to_bed firing and isMasterAsleep becoming true,
// any state change caused lighting to re-activate Primary Suite lights,
// cancelling the sleep detection timer.
//
// Fix: isSleepPrepActive bridges the gap — set by handleGoToBed, cleared
// when person wakes up or on midnight crossing.
// ============================================================================

// setupSleepPrepLightingTest creates a test environment with both lighting and
// sleep hygiene plugins for cross-plugin testing.
func setupSleepPrepLightingTest(t *testing.T) (*MockHAServer, *lighting.Manager, *sleephygiene.Manager, *state.Manager, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load test lighting config (includes isSleepPrepActive skip condition)
	configPath := filepath.Join("testdata", "hue_config_test.yaml")
	lightingConfig, err := lighting.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test lighting config")

	// Create lighting plugin
	lightingMgr := lighting.NewManager(context.Background(), client, stateManager, lightingConfig, logger, false, nil)
	err = lightingMgr.Start()
	require.NoError(t, err, "Failed to start lighting manager")

	// Create sleep hygiene plugin
	configLoader := config.NewLoader("../../configs", logger)
	sleepMgr := sleephygiene.NewManager(context.Background(), client, stateManager, configLoader, logger, false, nil, nil)
	err = sleepMgr.Start()
	require.NoError(t, err, "Failed to start sleep hygiene manager")

	cleanup := func() {
		sleepMgr.Stop()
		lightingMgr.Stop()
		baseCleanup()
	}

	return server, lightingMgr, sleepMgr, stateManager, cleanup
}

// TestScenario_GoToBed_LightingDoesNotReactivatePrimarySuite validates that
// after go_to_bed fires (isSleepPrepActive=true), a state change does NOT
// cause the lighting plugin to re-activate the Master Bedroom lights.
func TestScenario_GoToBed_LightingDoesNotReactivatePrimarySuite(t *testing.T) {
	t.Parallel()
	server, _, _, stateManager, cleanup := setupSleepPrepLightingTest(t)
	defer cleanup()

	// GIVEN: Night phase, someone home, go_to_bed has fired (isSleepPrepActive=true)
	t.Log("GIVEN: Night phase, someone home, isSleepPrepActive=true (go_to_bed has fired)")
	server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	server.SetState("input_text.sun_event", "night", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.sleep_prep_active", "on", map[string]interface{}{})
	server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	// Take snapshot of service calls
	snapshot := server.ServiceCallCount()

	// WHEN: A state change triggers lighting re-evaluation (e.g., sunevent changes)
	t.Log("WHEN: sunevent changes, triggering lighting re-evaluation")
	server.SetState("input_text.sun_event", "astronomical_twilight", map[string]interface{}{})

	// Wait briefly for any lighting reactions to propagate
	waitForServiceCallsToStabilizeSince(t, server, snapshot, 300*time.Millisecond)

	// THEN: Lighting should NOT have activated a scene for Master Bedroom
	t.Log("THEN: Lighting should skip Master Bedroom (isSleepPrepActive=true)")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	for _, call := range sceneActivations {
		if entityID, ok := call.ServiceData["entity_id"].(string); ok {
			// Master Bedroom scenes should NOT be activated
			assert.NotContains(t, entityID, "master_bedroom",
				"Lighting should NOT activate master_bedroom scene when isSleepPrepActive=true, but activated: %s", entityID)
		}
	}
}

// TestScenario_SleepPrepActive_ClearedOnWakeUp validates that when a person
// wakes up (isMasterAsleep → false), isSleepPrepActive is cleared so that
// the lighting plugin resumes normal control of the Primary Suite.
func TestScenario_SleepPrepActive_ClearedOnWakeUp(t *testing.T) {
	t.Parallel()
	server, _, _, stateManager, cleanup := setupSleepPrepLightingTest(t)
	defer cleanup()

	// GIVEN: isSleepPrepActive=true, isMasterAsleep=true (person is asleep)
	t.Log("GIVEN: Person is asleep, isSleepPrepActive=true, isMasterAsleep=true")
	server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	server.SetState("input_boolean.sleep_prep_active", "on", map[string]interface{}{})
	server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	// WHEN: Person wakes up (isMasterAsleep → false)
	t.Log("WHEN: Person wakes up (isMasterAsleep changes to false)")
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})

	// THEN: isSleepPrepActive should be cleared
	t.Log("THEN: isSleepPrepActive should be cleared to false")
	waitForBoolState(t, stateManager, "isSleepPrepActive", false,
		"isSleepPrepActive should be cleared when person wakes up")
}

// TestScenario_SleepPrepNotActive_LightingControlsBedroom validates that
// when isSleepPrepActive=false (normal operation), the lighting plugin
// still controls the Master Bedroom normally.
func TestScenario_SleepPrepNotActive_LightingControlsBedroom(t *testing.T) {
	t.Parallel()
	server, _, _, stateManager, cleanup := setupSleepPrepLightingTest(t)
	defer cleanup()

	// GIVEN: Evening, someone home, isSleepPrepActive=false (normal operation)
	t.Log("GIVEN: Evening, someone home and awake, isSleepPrepActive=false")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_text.sun_event", "sunset", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.sleep_prep_active", "off", map[string]interface{}{})
	server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	// WHEN: Day phase changes (triggers lighting re-evaluation)
	t.Log("WHEN: Day phase changes to winddown")
	server.SetState("input_text.day_phase", "winddown", map[string]interface{}{})

	// Wait for scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "scenes should be activated")

	// THEN: Lighting should activate scenes including Master Bedroom
	t.Log("THEN: Lighting should activate scenes normally (including Master Bedroom)")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	assert.Greater(t, len(sceneActivations), 0,
		"Should activate scenes when isSleepPrepActive=false")
}

// TestScenario_GoToBedDeferredUntilArrival_BedroomWelcomeNotSuppressed validates
// the production invariant from the empty-house bedtime incident:
//
// GIVEN no one is home at go_to_bed time, sleep prep must defer and leave
// isSleepPrepActive=false.
// WHEN someone arrives inside the 1-hour go_to_bed window, the lighting plugin
// must run the bedroom welcome scene while isSleepPrepActive is still false.
// THEN the next scheduled sleep hygiene tick may fire go_to_bed and set
// isSleepPrepActive=true without having suppressed the arrival lighting.
func TestScenario_GoToBedDeferredUntilArrival_BedroomWelcomeNotSuppressed(t *testing.T) {
	t.Parallel()

	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	logger := testlogger.New()
	goToBedWindow := time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC)

	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	require.NoError(t, stateManager.SetString("sunevent", "night"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", "winddown"))
	require.NoError(t, stateManager.SetBool("isAnyoneHome", false))
	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", false))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	require.NoError(t, stateManager.SetBool("isEveryoneAsleep", false))
	require.NoError(t, stateManager.SetBool("isSleepPrepActive", false))
	require.NoError(t, stateManager.SetBool("isWakeSequenceActive", false))
	waitForProcessing(t, stateManager)

	configPath := filepath.Join("testdata", "hue_config_test.yaml")
	lightingConfig, err := lighting.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test lighting config")

	lightingMgr := lighting.NewManager(context.Background(), client, stateManager, lightingConfig, logger, false, nil)
	require.NoError(t, lightingMgr.Start(), "Failed to start lighting manager")
	defer lightingMgr.Stop()

	// ../../../configs: integration tests live two levels deep (test/integration),
	// so reaching the top-level configs dir requires three parent hops.
	configLoader := config.NewLoader("../../../configs", logger)
	configLoader.SetClock(clock.NewMockClock(goToBedWindow))
	require.NoError(t, configLoader.LoadScheduleConfig(), "Failed to load schedule config")
	sleepMgr := sleephygiene.NewManager(
		context.Background(),
		client,
		stateManager,
		configLoader,
		logger,
		false,
		plugin.FixedTimeProvider{FixedTime: goToBedWindow},
		time.UTC,
	)
	require.NoError(t, sleepMgr.Start(), "Failed to start sleep hygiene manager")
	defer sleepMgr.Stop()

	t.Log("GIVEN: No one home at go_to_bed time; sleep prep defers")
	// Explicitly run a scheduled check while no one is home to prove the defer
	// branch fires and leaves isSleepPrepActive=false. Without this call the
	// assertions below would only confirm the initial setup values.
	sleepMgr.TriggerScheduledCheckForTest()
	isSleepPrepGiven, err := stateManager.GetBool("isSleepPrepActive")
	require.NoError(t, err)
	require.False(t, isSleepPrepGiven,
		"isSleepPrepActive must remain false when the defer branch ran with no one home")
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "winddown", musicType, "go_to_bed should not start sleep music while no one is home")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Someone arrives home within the 1-hour go_to_bed window")
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", true))
	waitForProcessing(t, stateManager)

	t.Log("THEN: Bedroom welcome lighting runs before sleep prep becomes active")
	waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", "scene.master_bedroom_night",
		"arrival should activate the bedroom scene before deferred go_to_bed sets isSleepPrepActive")
	isSleepPrep, err := stateManager.GetBool("isSleepPrepActive")
	require.NoError(t, err)
	// The 1-minute ticker can't fire within a single waitForProcessing cycle at
	// test speed, so this snapshot assert is safe — no racy true→false flip.
	assert.False(t, isSleepPrep,
		"isSleepPrepActive must still be false immediately after arrival lighting runs")

	t.Log("WHEN: The next scheduled sleep hygiene tick runs")
	sleepMgr.TriggerScheduledCheckForTest()

	t.Log("THEN: Deferred go_to_bed fires after arrival without suppressing the bedroom scene")
	waitForBoolState(t, stateManager, "isSleepPrepActive", true,
		"isSleepPrepActive should become true after the deferred go_to_bed trigger fires")
	waitForStringState(t, stateManager, "musicPlaybackType", "sleep",
		"deferred go_to_bed should start sleep music after arrival")
}
