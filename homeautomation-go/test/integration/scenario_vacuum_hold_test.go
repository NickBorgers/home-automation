package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Hold While Scenario Tests
//
// User story:
//   "Do not start a run while the TV is on. The cleaning is too loud. Start
//    it once the TV is off and things have been quiet for a while. A run that
//    is already going may finish."
//
// Same example house as scenario_vacuum_test.go. The scheduler config holds
// new runs while isTVPlaying is true. isTVPlaying is the state variable the
// TV plugin keeps, from input_boolean.tv_playing.
//
// INVARIANTS:
//   - While a hold_while condition holds, no run starts, even with a valid plan.
//   - When the hold ends, the plan must hold for stable_for before it starts.
//   - A run that is going is never stopped by the hold.
// ============================================================================

const vacuumHoldConfig = `    hold_while:
      - {variable: isTVPlaying, value: true}
`

func (v *vacuumScenario) setTV(on bool) {
	state := "off"
	if on {
		state = "on"
	}
	v.server.SetState("input_boolean.tv_playing", state, map[string]interface{}{})
	waitForBoolState(v.t, v.sm, "isTVPlaying", on)
}

// GIVEN the study is dirty and the TV is on
// WHEN the plan would otherwise start, and later the TV goes off
// THEN nothing starts while the TV is on, and the run starts stable_for after it goes off
func TestScenario_Vacuum_HoldWhileTVPlaying(t *testing.T) {
	v := setupVacuumScenarioWith(t, nil, nil, vacuumHoldConfig, nil)
	v.server.SetState("input_boolean.tv_playing", "off", map[string]interface{}{})
	waitForBoolState(t, v.sm, "isTVPlaying", false)

	t.Log("GIVEN: the study is dirty and the TV is on")
	v.setTV(true)
	v.markDirty("study")
	v.tick()

	t.Log("WHEN: half an hour passes")
	for i := 0; i < 60; i++ {
		v.advance(30 * time.Second)
	}

	t.Log("THEN: no run, and the plan says why")
	assert.Nil(t, v.out().Run)
	assert.Empty(t, v.segmentCleans())
	assert.Equal(t, "held: isTVPlaying is true", v.out().Plan.Reason)

	t.Log("WHEN: the TV goes off")
	v.setTV(false)
	v.tick()
	require.NotNil(t, v.out().Plan.PendingSince, "the plan waits for stable_for from now")
	v.advance(9 * time.Minute)
	assert.Nil(t, v.out().Run, "not before stable_for")

	t.Log("THEN: the run starts stable_for after the TV went off")
	v.advance(time.Minute)
	require.NotNil(t, v.out().Run)
	assert.Equal(t, []string{"[3]"}, v.segmentCleans())

	t.Log("AND: the TV going on again does not stop the run")
	v.setTV(true)
	v.step("lounge", 99)
	v.step("study", 98)
	require.NotNil(t, v.out().Run)
	assert.False(t, v.out().Run.Aborted)
	for _, c := range v.robot.Commands() {
		assert.NotEqual(t, "/api/v2/robot/capabilities/BasicControlCapability", c.Path, "no pause, no return")
	}
}
