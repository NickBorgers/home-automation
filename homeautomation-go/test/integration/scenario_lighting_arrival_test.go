package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Lighting Arrival Bug Regression (2026-05-25 incident)
//
// Reproduces the production incident where, on arrival, the lighting plugin
// fired three back-to-back scene recalls within 474ms on a dynamic-palette
// scene. The Hue Bridge could not reliably start the palette on cold bulbs,
// each bulb reverted to off, and the statetracking plugin then read those
// off events as "occupants are asleep."
//
// After the fix:
//   1. evaluateAndActivateRoom debounces per-room evaluations so an arrival
//      burst collapses to a single coalesced evaluation.
//   2. activateScene does a two-step recall for HasDynamics rooms: first
//      with dynamic=false (static settle), then with dynamic=true (palette).
//
// This integration test verifies both mechanisms together: an arrival burst
// against a HasDynamics=true room yields exactly TWO scene.turn_on calls
// (one static + one dynamic), not six.
// ============================================================================

// setupLightingArrivalTest creates a lighting plugin wired to a mock HA, with
// the Primary Suite configured for two-step dynamic recall, the production
// debounce delay enabled, and a mock clock for deterministic timer control.
func setupLightingArrivalTest(t *testing.T) (*MockHAServer, *lighting.Manager, *state.Manager, *clock.MockClock, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	transition := 180
	cfg := &lighting.HueConfig{
		Rooms: []lighting.RoomConfig{
			{
				HueGroup:   "Primary Suite",
				HASSAreaID: "master_bedroom",
				Conditions: []lighting.LightingCondition{
					// Mirrors production: skip if wake or sleep-prep active,
					// off if no one home and awake, on otherwise.
					{Action: "skip", Variable: "isWakeSequenceActive", Value: true},
					{Action: "skip", Variable: "isSleepPrepActive", Value: true},
					{Action: "skip", Variable: "isMasterAsleep", Value: true},
					{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
					{Action: "on", Variable: "isMasterAsleep", Value: false},
				},
				TransitionSeconds: &transition,
				HasDynamics:       true,
			},
		},
	}

	logger := testlogger.New()
	mockClock := clock.NewMockClock(time.Now())

	lightingMgr := lighting.NewManager(context.Background(), client, manager, cfg, logger, false, nil)
	lightingMgr.SetClock(mockClock)

	require.NoError(t, lightingMgr.Start())
	// Enable production debouncing (matches what the plugin adapter does in
	// real deployment). Without this, evaluateAndActivateRoom fires
	// synchronously and the regression isn't exercised.
	lightingMgr.SetDebounceDelay(300 * time.Millisecond)

	cleanup := func() {
		lightingMgr.Stop()
		baseCleanup()
	}

	return server, lightingMgr, manager, mockClock, cleanup
}

// TestScenario_LightingArrival_DebouncedTwoStepRecall reproduces the arrival
// burst on a HasDynamics=true room and asserts that the lighting plugin
// issues exactly two scene.turn_on calls (one static, one dynamic) — not
// the six (3 evaluations × 2 calls each) that would happen without
// debouncing.
func TestScenario_LightingArrival_DebouncedTwoStepRecall(t *testing.T) {
	t.Parallel()
	server, _, manager, mockClock, cleanup := setupLightingArrivalTest(t)
	defer cleanup()

	t.Log("GIVEN: dayPhase=day, no one home, primary suite settled (lockdown-off state)")
	server.SetState("input_text.day_phase", "day", nil)
	server.SetState("input_boolean.anyone_home", "off", nil)
	server.SetState("input_boolean.anyone_home_and_awake", "off", nil)
	server.SetState("input_boolean.master_asleep", "off", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	server.SetState("input_boolean.sleep_prep_active", "off", nil)
	server.SetState("light.primary_suite", "off", nil)

	waitForBoolState(t, manager, "isAnyoneHome", false, "isAnyoneHome should be false initially")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", false, "isAnyoneHomeAndAwake should be false initially")
	waitForProcessing(t, manager)
	// Flush any pending debounced evaluations from setup (room may have been
	// scheduled for `off` due to the initial nobody-home state).
	mockClock.AdvanceAndProcess(400 * time.Millisecond)
	waitForProcessing(t, manager)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Arrival burst — multiple presence variables flip near-simultaneously")
	// Mirrors the production cascade: isAnyoneHome flips, then
	// isAnyoneHomeAndAwake (computed). Each fires a room evaluation. The
	// debounce window (300ms) coalesces them.
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.anyone_home_and_awake", "on", nil)
	waitForBoolState(t, manager, "isAnyoneHome", true, "isAnyoneHome should flip to true")
	waitForBoolState(t, manager, "isAnyoneHomeAndAwake", true, "isAnyoneHomeAndAwake should flip to true")
	waitForProcessing(t, manager)

	t.Log("AND: Before the debounce delay elapses, no scene calls should have fired")
	sceneCallsBeforeDebounce := filterServiceCalls(server.GetServiceCallsSince(snapshot), "scene", "turn_on")
	assert.Equal(t, 0, len(sceneCallsBeforeDebounce),
		"scene activations must be deferred until the debounce window elapses")

	t.Log("WHEN: The debounce window elapses — coalesced evaluation runs and fires the static phase")
	mockClock.AdvanceAndProcess(310 * time.Millisecond)
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "static phase scene call should fire")

	sceneCallsAfterDebounce := filterServiceCalls(server.GetServiceCallsSince(snapshot), "scene", "turn_on")
	require.Equal(t, 1, len(sceneCallsAfterDebounce),
		"exactly one static-phase scene call after the debounce coalesces the burst")
	assert.Equal(t, "scene.primary_suite_day", sceneCallsAfterDebounce[0].ServiceData["entity_id"])
	assert.Equal(t, false, sceneCallsAfterDebounce[0].ServiceData["dynamic"],
		"first call is the static phase (dynamic=false) so bulbs settle at the scene's base colors")

	t.Log("WHEN: The two-step gap elapses — dynamic phase fires")
	mockClock.AdvanceAndProcess(600 * time.Millisecond) // > twoStepRecallGap (500ms)

	// Poll for the dynamic-phase call to land (timer callback is fired
	// synchronously during AdvanceAndProcess, but the mock server's call
	// list update happens through normal service-call recording).
	require.Eventually(t, func() bool {
		return len(filterServiceCalls(server.GetServiceCallsSince(snapshot), "scene", "turn_on")) >= 2
	}, 3*time.Second, 10*time.Millisecond, "dynamic phase should land")

	sceneCallsAfterDynamic := filterServiceCalls(server.GetServiceCallsSince(snapshot), "scene", "turn_on")
	require.Equal(t, 2, len(sceneCallsAfterDynamic),
		"exactly TWO scene calls total: static + dynamic; never six")
	assert.Equal(t, true, sceneCallsAfterDynamic[1].ServiceData["dynamic"],
		"second call is the dynamic phase (dynamic=true) so the palette starts from a stable state")

	t.Log("✓ Arrival burst coalesced to one logical activation; two-step recall preserved")
}
