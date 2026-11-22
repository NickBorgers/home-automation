package vacuum

import (
	"testing"
	"time"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// In the test house the pantry is behind doors from the mud room (the dock), the lounge, the kitchen and the
// bed. The pantry is within earshot of the lounge.
func TestComputeRestrictionsQuietFor(t *testing.T) {
	g := loadTestMap(t).Graph()
	quiet := map[string][]string{"pantry": {"family", "lounge"}}

	t.Run("a blocked area keeps the room from being cleaned, not from being entered", func(t *testing.T) {
		r := ComputeRestrictions(g, map[string]bool{"lounge": true}, nil, quiet)
		assert.Equal(t, "within earshot of area lounge, which is occupied or unknown", r.NoClean["pantry"])
		assert.NotContains(t, r.NoEnter, "pantry")
		assert.Equal(t, "area lounge is occupied or unknown", r.NoEnter["lounge"])
		assert.NotContains(t, r.NoClean, "kitchen", "rooms beyond are not affected")
	})

	t.Run("the first listed blocked area is named", func(t *testing.T) {
		r := ComputeRestrictions(g, map[string]bool{"lounge": true, "family": true}, nil, quiet)
		assert.Equal(t, "within earshot of area family, which is occupied or unknown", r.NoClean["pantry"])
	})

	t.Run("clear areas leave the room alone", func(t *testing.T) {
		r := ComputeRestrictions(g, map[string]bool{"kitchen": true}, nil, quiet)
		assert.NotContains(t, r.NoClean, "pantry")
	})

	t.Run("the room's own blocked area wins as the reason", func(t *testing.T) {
		r := ComputeRestrictions(g, map[string]bool{"lounge": true, "pantry": true}, nil, quiet)
		assert.Equal(t, "area pantry is occupied or unknown", r.NoClean["pantry"])
		assert.Equal(t, "area pantry is occupied or unknown", r.NoEnter["pantry"])
	})
}

func TestBuildPlanQuietForDrivesThrough(t *testing.T) {
	g := loadTestMap(t).Graph()
	in := baseInput(g, full("pantry", 3, 5), full("kitchen", 2, 5))
	in.BlockedAreas = map[string]bool{"lounge": true}
	in.QuietFor = map[string][]string{"pantry": {"lounge"}}

	p := BuildPlan(in)
	assert.Equal(t, []string{"kitchen"}, p.Rooms, "the kitchen is reached through the pantry")
	assert.Equal(t, "within earshot of area lounge, which is occupied or unknown",
		skipReason(p, "pantry", TreatmentFull))
	assert.Contains(t, p.NoClean, "pantry")
	assert.NotContains(t, p.NoEnter, "pantry")

	in.BlockedAreas = map[string]bool{}
	p = BuildPlan(in)
	assert.Equal(t, []string{"pantry", "kitchen"}, p.Rooms, "with the lounge clear the pantry is cleaned")
}

func TestSchedulerQuietForUnknownArea(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		r := cfg.Rooms["pantry"]
		r.QuietFor = []string{"lounge", "attic"}
		cfg.Rooms["pantry"] = r
	})
	e.start()
	o := e.sched()
	require.NotNil(t, o)
	assert.False(t, o.Configured)
	assert.Equal(t, `scheduler.rooms.pantry.quiet_for: area "attic" is not an area in the house map`, o.Reason)
	assert.Empty(t, e.robot.Commands())
}

func TestSchedulerQuietForAreaIsNotARoomName(t *testing.T) {
	// bed and bath are rooms of area suite: the area name is valid, the room names are not.
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		r := cfg.Rooms["office"]
		r.QuietFor = []string{"suite"}
		cfg.Rooms["office"] = r
	})
	assert.Equal(t, "", e.s.reason)

	e = newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		r := cfg.Rooms["office"]
		r.QuietFor = []string{"bed"}
		cfg.Rooms["office"] = r
	})
	assert.Contains(t, e.s.reason, `area "bed" is not an area in the house map`)
}

// A run that is cleaning a room when its quiet_for area fills reacts as it does when a sleeper's neighbour
// becomes no-clean: the robot goes home.
func TestSchedulerQuietForMidRunGoesHome(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(cfg *SchedulerConfig, _ *housemap.Map) {
		r := cfg.Rooms["pantry"]
		r.QuietFor = []string{"lounge"}
		cfg.Rooms["pantry"] = r
	})
	e.start()
	e.startRun("pantry")
	e.moveTo("pantry", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	clearAreas(e.hub, "lounge")
	e.s.TickForTest(true)
	assert.Equal(t, []string{"/api/v2/robot/capabilities/BasicControlCapability map[action:home]"},
		e.commandPaths())
	assert.Contains(t, e.sched().Run.AbortReason, "within earshot of area lounge")
}

func TestLoadConfigQuietFor(t *testing.T) {
	cfg := loadConfigText(t, `
vacuum:
  scheduler:
    mode: log_only
    valetudo_url: http://robot.test
    rooms:
      pantry: {segment_id: "2", full: 48h, quiet_for: [lounge, family]}
`)
	sc := cfg.Vacuum.Scheduler
	require.NoError(t, sc.Err)
	assert.Equal(t, []string{"lounge", "family"}, sc.Rooms["pantry"].QuietFor)
}
