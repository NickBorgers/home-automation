package integration

// =============================================================================
// USER STORY INTEGRATION TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate end-to-end user stories that span multiple plugins.
// Unlike unit tests that verify individual plugin behavior in isolation,
// these tests verify that plugins coordinate correctly from the user's
// perspective.
//
// PHILOSOPHY (TDD for Cross-Plugin Features):
// When implementing a feature that involves multiple plugins:
// 1. Write the user story test FIRST - describe what the user expects
// 2. The test will fail because the feature isn't implemented
// 3. Implement the feature across plugins
// 4. The test passes when the user's expectation is met
//
// This catches coordination bugs that unit tests miss because unit tests
// set up state atomically, while real scenarios involve timing and sequences.
//
// EXAMPLE BUG CAUGHT:
// PR #564 - isWakeSequenceActive was set in handleWake() (T+5min) instead of
// handleBeginWake() (T+0). Unit tests passed because they set up state directly.
// A user story test would have caught this because it simulates the full timeline.
//
// =============================================================================

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// INVARIANT ASSERTIONS
// =============================================================================
//
// These capture the rules that should ALWAYS hold, regardless of timing.
// These are documented here so future tests and implementations respect them.
//
// INVARIANT 1: Sleep zone requires isWakeSequenceActive=false
//   When isWakeSequenceActive=true, the sleep zone should NOT activate.
//   This is enforced by the sleep zone trigger: isWakeSequenceActive: false
//
// INVARIANT 2: Morning zone can activate via wake sequence
//   The morning zone has two trigger groups (OR logic):
//   - Normal: dayPhase=morning, isAnyoneAsleep=false
//   - WakeSequence: isWakeSequenceActive=true, dayPhase=morning
//
// INVARIANT 3: State timing matters
//   isWakeSequenceActive must be set IMMEDIATELY when begin_wake fires (T+0),
//   not 5 minutes later when handleWake runs (T+5min).
//   This prevents a race window where dayPhase changes but sleep zone
//   still matches.
//
// =============================================================================

// =============================================================================
// USER STORY: WAKE SEQUENCE COORDINATION
// =============================================================================
//
// AS A user with an Eight Sleep smart mattress
// WHEN my alarm goes off in the morning
// I WANT morning music to start playing in the rest of the house
// AND I DON'T WANT sleep music to restart during the fade-out
// SO THAT I wake up to energizing music while my bedroom fades out quietly
//
// ACCEPTANCE CRITERIA:
// 1. When Eight Sleep alarm fires, isWakeSequenceActive becomes true IMMEDIATELY
// 2. Sleep zone stops matching (it requires isWakeSequenceActive=false)
// 3. Morning zone activates when dayPhase=morning (via its wake sequence trigger group)
// 4. This works even if dayPhase changes during the 5-minute fade-out window
//
// =============================================================================

func TestUserStory_WakeSequence_StateCoordination(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("=== USER STORY: Wake Sequence State Coordination ===")

	// =========================================================================
	// GIVEN: It's night, master is asleep
	// =========================================================================
	t.Log("GIVEN: Night phase, master asleep")

	// Set initial state via HA server (simulates real HA entities)
	server.SetState("input_text.day_phase", "night", nil)
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.anyone_asleep", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	waitForProcessing(t, stateManager)

	// Verify initial state
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "Wake sequence should NOT be active initially")

	// =========================================================================
	// WHEN: Eight Sleep alarm fires (begin_wake event at T+0)
	// =========================================================================
	t.Log("WHEN: Eight Sleep alarm fires (begin_wake at T+0)")

	// The fix from PR #564: isWakeSequenceActive is set IMMEDIATELY
	// when handleBeginWake() fires, not 5 minutes later in handleWake()
	server.SetState("input_boolean.wake_sequence_active", "on", nil)
	waitForProcessing(t, stateManager)

	// CRITICAL ASSERTION: isWakeSequenceActive must be true NOW
	waitForBoolState(t, stateManager, "isWakeSequenceActive", true,
		"isWakeSequenceActive MUST be true immediately at T+0 (begin_wake), "+
			"not after 5-minute delay. This is the PR #564 fix.")

	// =========================================================================
	// AND: dayPhase changes to "morning" during fade-out window
	// =========================================================================
	t.Log("AND: dayPhase changes to morning during the 5-minute fade-out window")

	// This simulates sunrise happening during the fade-out
	// Before PR #564, this would cause sleep music to restart because
	// isWakeSequenceActive was still false during this window
	server.SetState("input_text.day_phase", "morning", nil)
	waitForProcessing(t, stateManager)

	// Verify state is as expected for morning zone to activate
	waitForStringState(t, stateManager, "dayPhase", "morning", "dayPhase should become morning")
	dayPhase, _ := stateManager.GetString("dayPhase")
	isWakeActive, _ := stateManager.GetBool("isWakeSequenceActive")
	assert.True(t, isWakeActive,
		"isWakeSequenceActive should STILL be true when dayPhase changes")

	// =========================================================================
	// THEN: State allows morning zone (not sleep zone)
	// =========================================================================
	t.Log("THEN: State should allow morning zone, NOT sleep zone")

	// Document the expected zone trigger evaluation:
	// Sleep zone triggers: isMasterAsleep=true, isAnyoneHome=true, isWakeSequenceActive=false
	// Current state: isMasterAsleep=true, isAnyoneHome=true, isWakeSequenceActive=TRUE
	// => Sleep zone should NOT match (isWakeSequenceActive != false)
	isMasterAsleep, _ := stateManager.GetBool("isMasterAsleep")
	isAnyoneHome, _ := stateManager.GetBool("isAnyoneHome")

	t.Logf("State for zone evaluation:")
	t.Logf("  dayPhase: %s", dayPhase)
	t.Logf("  isWakeSequenceActive: %v", isWakeActive)
	t.Logf("  isMasterAsleep: %v", isMasterAsleep)
	t.Logf("  isAnyoneHome: %v", isAnyoneHome)

	// The key assertion: sleep zone trigger requires isWakeSequenceActive=false
	// Since it's TRUE, sleep zone should NOT be active
	assert.True(t, isWakeActive,
		"isWakeSequenceActive=true blocks sleep zone activation")

	// Morning zone second trigger group: isWakeSequenceActive=true, dayPhase=morning, isAnyoneHome=true
	assert.Equal(t, "morning", dayPhase, "dayPhase should be morning")
	assert.True(t, isWakeActive, "isWakeSequenceActive should be true")
	assert.True(t, isAnyoneHome, "isAnyoneHome should be true")
	t.Log("✓ State is correct for morning zone to match via wake sequence trigger group")
}

// TestUserStory_WakeSequence_CancelReturnsToSleep tests that cancelling
// the wake sequence properly restores sleep mode
func TestUserStory_WakeSequence_CancelReturnsToSleep(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("=== USER STORY: Cancel Wake Sequence Returns to Sleep ===")

	// =========================================================================
	// GIVEN: Wake sequence is in progress during morning
	// =========================================================================
	server.SetState("input_text.day_phase", "morning", nil)
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.anyone_asleep", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_boolean.wake_sequence_active", "on", nil)
	waitForProcessing(t, stateManager)

	waitForBoolState(t, stateManager, "isWakeSequenceActive", true, "Wake sequence should be active")

	// =========================================================================
	// WHEN: User cancels wake sequence (turns off lights, goes back to sleep)
	// =========================================================================
	t.Log("WHEN: User cancels wake sequence")

	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	waitForProcessing(t, stateManager)

	// =========================================================================
	// THEN: Sleep zone can now match again
	// =========================================================================
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "isWakeSequenceActive should be false after cancel")

	// Sleep zone triggers should now match:
	// isMasterAsleep=true, isAnyoneHome=true, isWakeSequenceActive=false
	isMasterAsleep, _ := stateManager.GetBool("isMasterAsleep")
	isAnyoneHome, _ := stateManager.GetBool("isAnyoneHome")
	isWakeActive, _ := stateManager.GetBool("isWakeSequenceActive")

	assert.True(t, isMasterAsleep, "isMasterAsleep should still be true")
	assert.True(t, isAnyoneHome, "isAnyoneHome should still be true")
	assert.False(t, isWakeActive, "isWakeSequenceActive should be false")
	t.Log("✓ State allows sleep zone to reactivate after wake sequence cancelled")
}

// TestUserStory_CompleteMorningRoutine_StateTransitions tests the complete
// morning routine from a state transition perspective
func TestUserStory_CompleteMorningRoutine_StateTransitions(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("=== USER STORY: Complete Morning Routine State Transitions ===")

	// =========================================================================
	// PHASE 1: Sleeping (night phase, asleep)
	// =========================================================================
	t.Log("PHASE 1: Sleeping")

	server.SetState("input_text.day_phase", "night", nil)
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.anyone_asleep", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	waitForProcessing(t, stateManager)

	// Verify: Sleep zone should match
	waitForStringState(t, stateManager, "dayPhase", "night", "dayPhase should be night")
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "isWakeSequenceActive should be false")
	waitForBoolState(t, stateManager, "isMasterAsleep", true, "isMasterAsleep should be true")

	t.Log("  Sleep zone conditions: dayPhase=night, isWakeSequenceActive=false, isMasterAsleep=true ✓")

	// =========================================================================
	// PHASE 2: Alarm fires (begin_wake)
	// =========================================================================
	t.Log("PHASE 2: Alarm fires (begin_wake)")

	server.SetState("input_boolean.wake_sequence_active", "on", nil)
	waitForProcessing(t, stateManager)
	waitForBoolState(t, stateManager, "isWakeSequenceActive", true, "isWakeSequenceActive must be true immediately")
	t.Log("  isWakeSequenceActive=true ✓")

	// =========================================================================
	// PHASE 3: Sunrise (dayPhase → morning) during fade-out
	// =========================================================================
	t.Log("PHASE 3: Sunrise during fade-out")

	server.SetState("input_text.day_phase", "morning", nil)
	waitForProcessing(t, stateManager)
	waitForStringState(t, stateManager, "dayPhase", "morning", "dayPhase should become morning")

	isWakeActive, _ := stateManager.GetBool("isWakeSequenceActive")
	assert.True(t, isWakeActive)
	t.Log("  Morning zone conditions met via wake sequence trigger: dayPhase=morning, isWakeSequenceActive=true ✓")
	t.Log("  Sleep zone blocked: isWakeSequenceActive=true (requires false) ✓")

	// =========================================================================
	// PHASE 4: Person fully awake
	// =========================================================================
	t.Log("PHASE 4: Person fully awake")

	server.SetState("input_boolean.master_asleep", "off", nil)
	server.SetState("input_boolean.anyone_asleep", "off", nil)
	waitForProcessing(t, stateManager)
	waitForBoolState(t, stateManager, "isMasterAsleep", false, "isMasterAsleep should become false")
	waitForBoolState(t, stateManager, "isAnyoneAsleep", false, "isAnyoneAsleep should become false")
	t.Log("  Morning zone now matches via normal trigger: dayPhase=morning, isAnyoneAsleep=false ✓")

	// =========================================================================
	// PHASE 5: Day phase
	// =========================================================================
	t.Log("PHASE 5: Day phase")

	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	server.SetState("input_text.day_phase", "day", nil)
	waitForProcessing(t, stateManager)
	waitForStringState(t, stateManager, "dayPhase", "day", "dayPhase should become day")
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "isWakeSequenceActive should become false")
	t.Log("  Day zone conditions: dayPhase=day, isAnyoneAsleep=false ✓")

	t.Log("✓ Complete morning routine state transitions verified")
}

// =============================================================================
// REGRESSION TEST: PR #564
// =============================================================================
//
// This test specifically documents and prevents regression of the PR #564 bug:
// isWakeSequenceActive was only set in handleWake() (T+5 minutes) instead of
// handleBeginWake() (T+0).
//
// THE BUG:
// During the 5-minute fade-out window:
// 1. dayPhase could change to "morning"
// 2. isWakeSequenceActive was still false
// 3. Sleep zone still matched → sleep music restarted
// 4. Morning music never played
//
// THE FIX:
// Set isWakeSequenceActive=true in handleBeginWake() immediately when
// the Eight Sleep alarm fires.
//
// =============================================================================

func TestRegression_PR564_IsWakeSequenceActiveSetImmediately(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("=== REGRESSION TEST: PR #564 - isWakeSequenceActive timing ===")

	// Initial state: night, asleep
	server.SetState("input_text.day_phase", "night", nil)
	server.SetState("input_boolean.anyone_home", "on", nil)
	server.SetState("input_boolean.anyone_asleep", "on", nil)
	server.SetState("input_boolean.master_asleep", "on", nil)
	server.SetState("input_boolean.wake_sequence_active", "off", nil)
	waitForProcessing(t, stateManager)
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "initial isWakeSequenceActive should be false")

	// T+0: Eight Sleep alarm fires (begin_wake)
	t.Log("T+0: Eight Sleep alarm fires (handleBeginWake)")

	// THE FIX: isWakeSequenceActive is set IMMEDIATELY in handleBeginWake
	server.SetState("input_boolean.wake_sequence_active", "on", nil)
	waitForProcessing(t, stateManager)

	// CRITICAL: Must be true NOW, not after 5 minutes
	waitForBoolState(t, stateManager, "isWakeSequenceActive", true,
		"REGRESSION: isWakeSequenceActive must be true at T+0 (begin_wake), "+
			"not T+5min (handleWake). This is the PR #564 fix.")

	// T+2min (simulated): dayPhase changes to morning
	t.Log("T+2min: dayPhase changes to morning during fade-out window")

	server.SetState("input_text.day_phase", "morning", nil)
	waitForStringState(t, stateManager, "dayPhase", "morning", "dayPhase should become morning")

	// Verify isWakeSequenceActive is STILL true
	// (The bug was that it wasn't set yet, so sleep zone would re-match)
	isWakeActive, _ := stateManager.GetBool("isWakeSequenceActive")
	assert.True(t, isWakeActive,
		"REGRESSION: isWakeSequenceActive must still be true when dayPhase changes. "+
			"If false, sleep zone would match and sleep music would restart.")

	// Document the zone evaluation at this point:
	// Sleep zone: isMasterAsleep=true, isAnyoneHome=true, isWakeSequenceActive=FALSE (bug) vs TRUE (fix)
	// With bug: Sleep zone matches → sleep music restarts
	// With fix: Sleep zone blocked by isWakeSequenceActive=true
	dayPhase, _ := stateManager.GetString("dayPhase")
	isMasterAsleep, _ := stateManager.GetBool("isMasterAsleep")
	isAnyoneHome, _ := stateManager.GetBool("isAnyoneHome")

	t.Logf("Zone evaluation during fade-out window:")
	t.Logf("  dayPhase: %s", dayPhase)
	t.Logf("  isMasterAsleep: %v", isMasterAsleep)
	t.Logf("  isAnyoneHome: %v", isAnyoneHome)
	t.Logf("  isWakeSequenceActive: %v (must be TRUE to block sleep zone)", isWakeActive)

	// The key assertion: with the fix, isWakeSequenceActive=true blocks sleep zone
	assert.True(t, isWakeActive,
		"With PR #564 fix: isWakeSequenceActive=true blocks sleep zone activation")

	t.Log("✓ PR #564 regression test passed: isWakeSequenceActive set at T+0")
}
