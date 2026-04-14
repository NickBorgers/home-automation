package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Owner Handoff Scenario Tests (issue #991)
//
// These tests cover the bug where lockdown fires spuriously during a normal
// owner handoff (one leaving as another arrives). Two root causes:
//
//  1. Sleep detection false positive: the 1-minute sleep timer started when
//     lights went off (lockdown/away behavior) was not cancelled when another
//     owner arrived. Timer would fire and mark master as asleep even though
//     someone had just walked in.
//
//  2. No lockdown grace period after arrival: a brief presence flap (8 seconds)
//     immediately triggered full lockdown even though an owner had just arrived.
// ============================================================================

// findLockdownCall returns the first lockdown service call (either turn_on or turn_off)
// found in calls since the given snapshot, or nil if none found.
func findLockdownCall(server *MockHAServer, since int) *ServiceCall {
	calls := server.GetServiceCallsSince(since)
	for i := range calls {
		call := &calls[i]
		if call.Domain == "input_boolean" {
			if entityID, ok := call.ServiceData["entity_id"].(string); ok && entityID == "input_boolean.lockdown" {
				return call
			}
		}
	}
	return nil
}

// TestScenario_OwnerHandoff_SleepTimerCancelledOnArrival verifies that the sleep
// detection timer (started when primary suite lights go off during away/lockdown
// behavior) is cancelled when an owner arrives home, preventing a false
// isMasterAsleep=true detection (issue #991 fix 1).
//
// Invariant: arriving home MUST cancel any pending sleep detection timer.
func TestScenario_OwnerHandoff_SleepTimerCancelledOnArrival(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: No one is home and master is not marked asleep")
	server.SetState("input_boolean.nick_home", "off", nil)
	server.SetState("input_boolean.caroline_home", "off", nil)
	require.NoError(t, manager.SetBool("isMasterAsleep", false))
	waitForBoolState(t, manager, "isAnyoneHome", false, "isAnyoneHome should be false initially")
	waitForProcessing(t, manager)

	t.Log("WHEN: Primary suite lights turn off (simulating lockdown/away behavior)")
	server.SetState("light.primary_suite", "off", nil)
	waitForProcessing(t, manager)
	// The 1-minute sleep detection timer has now started.

	t.Log("AND: Nick arrives 54 seconds later (before the 1-minute sleep timer fires)")
	mockClock.AdvanceAndProcess(54 * time.Second)

	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, manager, "isNickHome", true, "isNickHome should be true after arrival")
	waitForProcessing(t, manager)
	// Fix 1: arrival should have cancelled the sleep detection timer.

	t.Log("WHEN: The 1-minute sleep detection window expires (timer should have been cancelled)")
	mockClock.AdvanceAndProcess(10 * time.Second) // total ~64s from lights off, past the 1-minute mark

	t.Log("THEN: isMasterAsleep must remain false (sleep timer was cancelled by Nick's arrival)")
	isMasterAsleep, err := manager.GetBool("isMasterAsleep")
	require.NoError(t, err)
	assert.False(t, isMasterAsleep,
		"isMasterAsleep should be false — sleep timer must be cancelled when owner arrives home")

	t.Log("✓ Sleep timer correctly cancelled on owner arrival during handoff")
}

// TestScenario_OwnerHandoff_LockdownSuppressedDuringGracePeriod verifies that a
// brief presence sensor flap does NOT trigger lockdown when an owner recently
// arrived home (issue #991 fix 2).
//
// Invariant: "no one home" lockdown must NOT activate if an owner arrived within
// the last ArrivalLockdownGracePeriod minutes.
func TestScenario_OwnerHandoff_LockdownSuppressedDuringGracePeriod(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: Nick is not home initially")
	server.SetState("input_boolean.nick_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false initially")
	waitForProcessing(t, manager)
	// Advance clock so there is no lingering state from startup.
	mockClock.AdvanceAndProcess(10 * time.Minute)

	t.Log("WHEN: Nick arrives home (sets didOwnerJustReturnHome=true)")
	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, manager, "isNickHome", true, "isNickHome should be true after arrival")
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true")
	waitForProcessing(t, manager)

	snapshot := server.ServiceCallCount()

	t.Log("AND: Nick's presence briefly flaps false (simulating 8-second sensor glitch)")
	server.SetState("input_boolean.nick_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false during flap")
	waitForServiceCallQuiescenceSince(t, server, snapshot, 200*time.Millisecond)

	t.Log("THEN: Lockdown must NOT be activated (owner just arrived — within grace period)")
	lockdownCall := findLockdownCall(server, snapshot)
	assert.Nil(t, lockdownCall,
		"Lockdown must not be triggered during brief presence flap within grace period after owner arrival")

	t.Log("✓ Lockdown correctly suppressed during presence flap within arrival grace period")
}

// TestScenario_OwnerHandoff_LockdownAllowedAfterGracePeriod verifies that a genuine
// departure (after the grace period expires) still triggers lockdown normally.
func TestScenario_OwnerHandoff_LockdownAllowedAfterGracePeriod(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: Nick is not home initially")
	server.SetState("input_boolean.nick_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false initially")
	waitForProcessing(t, manager)
	mockClock.AdvanceAndProcess(10 * time.Minute)

	t.Log("AND: Nick arrives home")
	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, manager, "isNickHome", true, "isNickHome should be true after arrival")
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true")
	waitForProcessing(t, manager)

	t.Log("WHEN: The arrival grace period expires (6 minutes pass)")
	mockClock.AdvanceAndProcess(6 * time.Minute)

	snapshot := server.ServiceCallCount()

	t.Log("AND: Nick genuinely departs")
	server.SetState("input_boolean.nick_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false after departure")

	t.Log("THEN: Lockdown MUST be activated (grace period has expired)")
	waitForServiceCallWithEntitySince(t, server, snapshot, "input_boolean", "turn_off", "input_boolean.lockdown",
		"Lockdown should be activated when owner genuinely departs after grace period")

	t.Log("✓ Lockdown correctly activated after grace period on genuine departure")
}
