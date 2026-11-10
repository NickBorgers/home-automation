package vacuum

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/housemap"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/internal/valetudo"
	"homeautomation/internal/valetudo/valetudotest"
	"homeautomation/pkg/occupancy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var schedT0 = time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)

type schedEnv struct {
	t       *testing.T
	ha      *ha.MockClient
	sm      *state.Manager
	robot   *valetudotest.Robot
	hub     *occupancy.Hub
	clk     *clock.MockClock
	tracker *shadowstate.VacuumTracker
	cfg     *SchedulerConfig
	s       *Scheduler
}

// testSchedulerConfig cleans every room of the test house. The kitchen also has a vacuum-only taint.
func testSchedulerConfig(mode string) *SchedulerConfig {
	cfg := &SchedulerConfig{
		Mode:        mode,
		ValetudoURL: "http://robot.test",
		Sleepers: []SleeperConfig{
			{State: "isMasterAsleep", Rooms: []string{"bed"}},
			{State: "isGuestAsleep", Require: "isHaveGuests", Rooms: []string{"bath"}},
		},
		Rooms: map[string]RoomConfig{
			"mud":     {SegmentID: "1", FullRaw: "72h"},
			"pantry":  {SegmentID: "2", FullRaw: "48h"},
			"kitchen": {SegmentID: "3", FullRaw: "48h", VacuumRaw: "12h"},
			"office":  {SegmentID: "4", FullRaw: "84h"},
			"bed":     {SegmentID: "5", FullRaw: "48h"},
			"bath":    {SegmentID: "6", FullRaw: "48h"},
			"lounge":  {SegmentID: "7", FullRaw: "84h"},
		},
		Treatments: map[Treatment]TreatmentConfig{
			TreatmentFull:   {OperationMode: "vacuum_and_mop", FanSpeed: "medium", WaterGrade: "medium"},
			TreatmentVacuum: {OperationMode: "vacuum", FanSpeed: "medium"},
		},
	}
	cfg.applyDefaultsAndValidate()
	return cfg
}

func newSchedEnv(t *testing.T, mode string, opts ...func(cfg *SchedulerConfig, m *housemap.Map)) *schedEnv {
	t.Helper()
	cfg := testSchedulerConfig(mode)
	require.NoError(t, cfg.Err)
	m := loadTestMap(t)
	for _, o := range opts {
		o(cfg, m)
	}

	mockHA := ha.NewMockClient()
	clk := clock.NewMockClock(schedT0)
	// Every helper exists. Every item was cleaned an hour ago, so nothing is dirty yet.
	for _, k := range cfg.itemKeys() {
		mockHA.SetState(dirtyEntity(k), "off", map[string]interface{}{})
		mockHA.SetState(cleanedEntity(k), "", map[string]interface{}{
			"timestamp": float64(schedT0.Add(-time.Hour).Unix())})
		mockHA.SetState(costEntity(k), "0.0", map[string]interface{}{})
	}
	mockHA.SetState(tripEntity, "0.0", map[string]interface{}{})
	sm := state.NewManager(mockHA, zap.NewNop(), false)
	require.NoError(t, sm.SyncFromHA())

	hub := occupancy.NewHub()
	clearAreas(hub)

	robot := valetudotest.New()
	t.Cleanup(robot.Close)
	robot.SetChargerPixel(pixelAt(1, 1))
	robot.SetRobotPixel(pixelAtOK(1, 1))

	tracker := shadowstate.NewVacuumTracker()
	s := NewScheduler(context.Background(), mockHA, sm, cfg, m, valetudo.NewClient(robot.URL, nil), hub, tracker,
		nil, zap.NewNop(), false, clk)
	s.SetManualTicksForTest()
	e := &schedEnv{t: t, ha: mockHA, sm: sm, robot: robot, hub: hub, clk: clk, tracker: tracker, cfg: cfg, s: s}
	return e
}

func (e *schedEnv) start() {
	require.NoError(e.t, e.s.Start())
	e.t.Cleanup(e.s.Stop)
}

// pixelAt turns a test-house point into a Valetudo pixel: anchor_px [0, 400], 5 cm pixels, y flipped.
func pixelAt(x, y float64) (float64, float64) {
	return x / 0.05, 400 - y/0.05
}

func pixelAtOK(x, y float64) (float64, float64, bool) {
	px, py := pixelAt(x, y)
	return px, py, true
}

var roomCentres = map[string][2]float64{
	"mud": {2, 2}, "pantry": {6, 2}, "kitchen": {10, 2}, "office": {2, 6}, "bed": {6, 6}, "bath": {10, 6},
	"lounge": {6, -2},
}

// moveTo puts the robot in a room and sets its battery.
func (e *schedEnv) moveTo(room string, battery int) {
	c := roomCentres[room]
	e.robot.SetRobotPixel(pixelAtOK(c[0], c[1]))
	e.robot.SetBattery(battery)
}

func clearAreas(hub *occupancy.Hub, occupied ...string) {
	areas := map[string]occupancy.Status{}
	for _, a := range []string{"mud", "pantry", "kitchen", "office", "suite", "lounge"} {
		areas[a] = occupancy.Status{}
	}
	for _, a := range occupied {
		areas[a] = occupancy.Status{Occupied: true}
	}
	hub.Publish(areas)
}

func (e *schedEnv) tick() {
	e.s.TickForTest(false)
}

func (e *schedEnv) advance(d time.Duration) {
	e.clk.Advance(d)
	e.tick()
}

func (e *schedEnv) markDirty(rooms ...string) {
	for _, r := range rooms {
		e.ha.SetState(dirtyEntity(itemKey{Room: r, Treatment: TreatmentFull}), "on", map[string]interface{}{})
	}
}

func (e *schedEnv) sched() *shadowstate.VacuumSchedulerOutputs {
	return e.tracker.GetState().Outputs.Scheduler
}

func (e *schedEnv) state(entity string) string {
	st, err := e.ha.GetState(entity)
	require.NoError(e.t, err)
	return st.State
}

func (e *schedEnv) cleanedAt(k itemKey) time.Time {
	st, err := e.ha.GetState(cleanedEntity(k))
	require.NoError(e.t, err)
	got, ok := parseDatetime(st, time.UTC)
	require.True(e.t, ok)
	return got
}

func (e *schedEnv) cost(entity string) float64 {
	v, err := strconv.ParseFloat(e.state(entity), 64)
	require.NoError(e.t, err)
	return v
}

func (e *schedEnv) commandPaths() []string {
	var out []string
	for _, c := range e.robot.Commands() {
		out = append(out, c.Path+" "+fmt.Sprint(c.Body))
	}
	return out
}

func haWrites(m *ha.MockClient) []ha.ServiceCall {
	return m.GetServiceCalls()
}

// startRun makes rooms dirty and lets the start gate pass. It returns after the run started.
func (e *schedEnv) startRun(rooms ...string) {
	e.t.Helper()
	e.markDirty(rooms...)
	e.tick()
	require.NotNil(e.t, e.sched().Plan)
	require.NotNil(e.t, e.sched().Plan.PendingSince, "plan is pending: %+v", e.sched().Plan)
	e.advance(10 * time.Minute)
	require.NotNil(e.t, e.sched().Run, "run started")
}

// ---------------------------------------------------------------------------

func TestSchedulerUnconfiguredReasons(t *testing.T) {
	tests := []struct {
		name string
		opt  func(cfg *SchedulerConfig, m *housemap.Map)
		want string
	}{
		{"off", func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.Mode = ModeOff }, "mode is off"},
		{"config error", func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.Err = fmt.Errorf("broken") }, "broken"},
		{"dock room", func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.DockRoom = "garage" }, "dock room"},
		{"dock from frame", func(_ *SchedulerConfig, m *housemap.Map) { m.Valetudo.DockM = []float64{50, 50} },
			"dock room"},
		{"sleeper room", func(cfg *SchedulerConfig, _ *housemap.Map) {
			cfg.Sleepers = []SleeperConfig{{State: "isMasterAsleep", Rooms: []string{"attic"}}}
		}, "sleeper room \"attic\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newSchedEnv(t, ModeActive, tt.opt)
			e.start()
			o := e.sched()
			require.NotNil(t, o)
			assert.False(t, o.Configured)
			assert.Contains(t, o.Reason, tt.want)
			assert.Empty(t, e.robot.Commands())
		})
	}

	s := NewScheduler(context.Background(), ha.NewMockClient(), nil, testSchedulerConfig(ModeLogOnly), nil, nil,
		nil, shadowstate.NewVacuumTracker(), nil, zap.NewNop(), false, nil)
	require.NoError(t, s.Start())
	s.Stop()
	assert.Contains(t, s.reason, "no house map")
}

func TestSchedulerDockRoomFromFrame(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	assert.Equal(t, "", e.s.reason)
	assert.Equal(t, "mud", e.s.dockRoom)
}

func TestSchedulerTurnsOnDueTaints(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.tick()
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"))

	e.advance(12 * time.Hour) // kitchen vacuum (12h) is now due; nothing else is
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"))
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen"))

	for _, it := range e.sched().Items {
		if it.Room == "kitchen" && it.Treatment == "vacuum" {
			assert.True(t, it.Dirty)
			assert.False(t, it.Manual)
		}
	}
	assert.Empty(t, e.robot.Commands(), "log_only never commands the robot")
}

func TestSchedulerReadOnlyWritesNothingButStillPlans(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.s.readOnly = true
	e.start()
	e.advance(13 * time.Hour)
	assert.Empty(t, haWrites(e.ha), "read-only writes no helper")
	require.NotNil(t, e.sched().Plan)
	assert.Equal(t, []string{"kitchen"}, e.sched().Plan.Rooms, "the in-memory taint still plans")
	e.advance(11 * time.Minute)
	assert.Empty(t, e.robot.Commands(), "read-only sends no robot command")
	assert.Nil(t, e.sched().Run)
	assert.True(t, e.sched().ReadOnly)
}

func TestSchedulerMissingHelperIsSkipped(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.ha.SetMockState("input_datetime.vacuum_cleaned_office", nil) // the helper does not exist
	e.start()
	e.markDirty("office")
	assert.NotPanics(t, e.tick)
	o := e.sched()
	assert.Contains(t, o.Warnings, "missing helper input_datetime.vacuum_cleaned_office; office/full is skipped")
	require.NotNil(t, o.Plan)
	assert.Empty(t, o.Plan.Rooms)
	found := false
	for _, sk := range o.Plan.Skipped {
		if sk.Room == "office" {
			found = true
			assert.Contains(t, sk.Reason, "missing helper")
		}
	}
	assert.True(t, found)
}

func TestSchedulerStartGate(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.markDirty("kitchen", "lounge")
	e.tick()
	p := e.sched().Plan
	require.NotNil(t, p)
	assert.Equal(t, []string{"lounge", "kitchen"}, p.Rooms)
	require.NotNil(t, p.StartsAt)
	assert.Equal(t, schedT0.Add(10*time.Minute), *p.StartsAt)

	e.advance(9 * time.Minute)
	assert.Empty(t, e.robot.Commands(), "the plan has not held for 10 minutes yet")

	// The plan changes: someone walks into the lounge. The gate starts again.
	clearAreas(e.hub, "lounge")
	e.s.TickForTest(true)
	assert.Equal(t, []string{"kitchen"}, e.sched().Plan.Rooms)
	clearAreas(e.hub)
	e.s.TickForTest(true)
	e.advance(9 * time.Minute)
	assert.Empty(t, e.robot.Commands(), "a changed plan restarts the gate")

	e.advance(time.Minute)
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/OperationModeControlCapability/preset map[name:vacuum_and_mop]",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/WaterUsageControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/MapSegmentationCapability map[action:start_segment_action customOrder:true " +
			"iterations:1 segment_ids:[7 3]]",
	}, e.commandPaths())
	run := e.sched().Run
	require.NotNil(t, run)
	assert.Equal(t, "scheduler", run.Origin)
	assert.Equal(t, []string{"lounge", "kitchen"}, run.Targets)
}

func TestSchedulerLogOnlyLogsButNeverCommands(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.markDirty("kitchen")
	e.tick()
	e.advance(10 * time.Minute)
	e.advance(10 * time.Minute)
	assert.Empty(t, e.robot.Commands())
	assert.Nil(t, e.sched().Run)
	assert.Equal(t, "full:kitchen", e.s.loggedWould)
}

func TestSchedulerVacuumTreatmentPresets(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.advance(13 * time.Hour) // only the kitchen vacuum taint is due
	e.advance(10 * time.Minute)
	assert.Equal(t, []string{
		"/api/v2/robot/capabilities/OperationModeControlCapability/preset map[name:vacuum]",
		"/api/v2/robot/capabilities/FanSpeedControlCapability/preset map[name:medium]",
		"/api/v2/robot/capabilities/MapSegmentationCapability map[action:start_segment_action customOrder:true " +
			"iterations:1 segment_ids:[3]]",
	}, e.commandPaths(), "no water grade for a vacuum-only run")
}

func TestSchedulerPresetFailureDoesNotStart(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.robot.Fail("/api/v2/robot/capabilities/OperationModeControlCapability/preset", http.StatusBadRequest)
	e.markDirty("kitchen")
	e.tick()
	e.advance(10 * time.Minute)
	assert.Nil(t, e.sched().Run)
	for _, c := range e.robot.Commands() {
		assert.NotContains(t, c.Path, "MapSegmentation", "no clean without the right mode")
	}
	assert.Contains(t, e.sched().Robot.LastError, "OperationModeControlCapability")
	e.advance(time.Minute)
	assert.Empty(t, e.robot.Commands(), "a refused start waits for the gate again")
}

// TestSchedulerTracksOwnRun follows a run room by room: each room is marked clean when the robot leaves it,
// battery drops become cost samples, and the rest is the trip.
func TestSchedulerTracksOwnRun(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	runStart := e.clk.Now()

	step := func(room string, battery int) {
		e.moveTo(room, battery)
		e.advance(30 * time.Second)
	}
	step("pantry", 98) // driving through
	step("lounge", 97) // first target
	step("lounge", 93)
	step("pantry", 92) // leaving the lounge
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_lounge"), "not done until the next target")
	step("kitchen", 91)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_lounge"), "lounge done when the kitchen starts")
	assert.True(t, e.cleanedAt(itemKey{"lounge", TreatmentFull}).After(runStart))
	assert.InDelta(t, 8.2, e.cost("input_number.vacuum_cost_lounge"), 1e-9, "9.6 + 0.3 * (5 - 9.6)")

	step("kitchen", 85)
	e.robot.SetStatus("returning", "none")
	step("kitchen", 84)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen"),
		"heading to the dock may be a mop wash: the last room waits for the end of the run")

	e.robot.SetStatus("docked", "none")
	step("mud", 83)
	assert.NotNil(t, e.sched().Run, "one docked poll is not yet the end")
	step("mud", 83)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen"), "kitchen done when the run ends")
	assert.InDelta(t, 9.1, e.cost("input_number.vacuum_cost_kitchen"), 1e-9, "9.6 + 0.3 * (8 - 9.6)")
	assert.InDelta(t, 6.1, e.cost("input_number.vacuum_cost_kitchen_vacuum"), 1e-9,
		"6.4 + 0.3 * (8 * 0.4 / 0.6 - 6.4)")
	vacuumCleaned := e.cleanedAt(itemKey{"kitchen", TreatmentVacuum})
	assert.True(t, vacuumCleaned.After(runStart), "a full clean also clears the vacuum taint")
	assert.Nil(t, e.sched().Run)
	last := e.sched().LastRun
	require.NotNil(t, last)
	assert.Equal(t, "completed", last.Outcome)
	assert.Equal(t, []string{"lounge", "kitchen"}, last.Done)
	assert.Equal(t, 100, last.StartBattery)
	assert.InDelta(t, 4.7, e.cost(tripEntity), 1e-9, "trip sample 17 - 13 = 4: 5 + 0.3 * (4 - 5)")

	// Our own turn_off events are not mistaken for the owner turning a taint off.
	before := e.cleanedAt(itemKey{"lounge", TreatmentFull})
	e.advance(time.Minute)
	assert.Equal(t, before, e.cleanedAt(itemKey{"lounge", TreatmentFull}))
}

func TestSchedulerPassingThroughALaterTargetDoesNotFinishIt(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	// Tour: pantry, then kitchen.
	e.startRun("pantry", "kitchen")
	require.Equal(t, []string{"pantry", "kitchen"}, e.sched().Run.Targets)

	e.moveTo("kitchen", 99) // the robot is seen in the kitchen once, out of order
	e.advance(30 * time.Second)
	e.moveTo("pantry", 98)
	e.advance(30 * time.Second)
	assert.Equal(t, "pantry", e.sched().Run.Current)
	assert.Empty(t, e.sched().Run.Done, "a short visit to a later target finishes nothing")
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen"))
}

func TestSchedulerAbortsWhenNextRoomBecomesOccupied(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	// Someone walks into the kitchen, the next target. The way home from the lounge is clear.
	clearAreas(e.hub, "kitchen")
	e.s.TickForTest(true)
	assert.Equal(t, []string{"/api/v2/robot/capabilities/BasicControlCapability map[action:home]"},
		e.commandPaths())
	run := e.sched().Run
	require.NotNil(t, run)
	assert.True(t, run.Aborted)
	assert.Contains(t, run.AbortReason, "kitchen")

	// The lounge is not finished: the robot was sent home from it.
	e.moveTo("pantry", 95)
	e.advance(30 * time.Second)
	e.moveTo("mud", 94)
	e.robot.SetStatus("docked", "none")
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	assert.Equal(t, "aborted", e.sched().LastRun.Outcome)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_lounge"), "unfinished rooms stay dirty")
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen"))
	assert.Len(t, e.robot.Commands(), 1, "home is sent once")
}

func TestSchedulerPausesWhenWayHomeIsBlocked(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()

	// Someone stands in the pantry: the way to the kitchen and the way home both cross it.
	clearAreas(e.hub, "pantry")
	e.s.TickForTest(true)
	assert.Equal(t, []string{"/api/v2/robot/capabilities/BasicControlCapability map[action:pause]"},
		e.commandPaths())
	assert.True(t, e.sched().Run.Paused)

	e.advance(time.Minute)
	assert.Len(t, e.robot.Commands(), 1, "still blocked: no new command")

	clearAreas(e.hub)
	e.s.TickForTest(true)
	assert.Equal(t, "/api/v2/robot/capabilities/BasicControlCapability map[action:home]", e.commandPaths()[1],
		"the way cleared: go home, do not resume")
	assert.False(t, e.sched().Run.Paused)
}

func TestSchedulerUnknownAreaCountsAsOccupied(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	areas := map[string]occupancy.Status{}
	for _, a := range []string{"mud", "pantry", "office", "suite", "lounge"} {
		areas[a] = occupancy.Status{}
	}
	areas["kitchen"] = occupancy.Status{Unknown: true}
	e.hub.Publish(areas)
	e.markDirty("kitchen")
	e.tick()
	assert.Empty(t, e.sched().Plan.Rooms)
	assert.Contains(t, e.sched().Plan.NoEnter, "kitchen")

	e.hub.Clear()
	e.s.TickForTest(true)
	assert.Contains(t, e.sched().Plan.Reason, "dock room mud", "nothing known: not even the dock room is clear")
}

func TestSchedulerSleeperRules(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.markDirty("kitchen", "pantry", "office", "bath")

	require.NoError(t, e.sm.SetBool("isMasterAsleep", true))
	e.s.TickForTest(true)
	p := e.sched().Plan
	assert.Equal(t, []string{"kitchen"}, p.Rooms, "bed's neighbours are skipped; the pantry is driven through")
	assert.Equal(t, []string{"bath", "bed", "office", "pantry"}, p.NoClean)

	require.NoError(t, e.sm.SetBool("isMasterAsleep", false))
	require.NoError(t, e.sm.SetBool("isGuestAsleep", true))
	e.s.TickForTest(true)
	assert.Contains(t, e.sched().Plan.Rooms, "bath", "a guest asleep without guests over protects nothing")

	require.NoError(t, e.sm.SetBool("isHaveGuests", true))
	e.s.TickForTest(true)
	assert.NotContains(t, e.sched().Plan.Rooms, "bath")
	assert.Contains(t, e.sched().Plan.NoClean, "bed", "the bath's neighbour")
}

func TestSchedulerAbortsWhenSomeoneFallsAsleep(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("office", "kitchen")
	e.moveTo("office", 97)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()
	require.NoError(t, e.sm.SetBool("isMasterAsleep", true)) // office is next to the bed
	e.s.TickForTest(true)
	assert.Equal(t, []string{"/api/v2/robot/capabilities/BasicControlCapability map[action:home]"},
		e.commandPaths())
	assert.Contains(t, e.sched().Run.AbortReason, "next to sleeper room bed")
}

func TestSchedulerReturnsAtBatteryFloor(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen")
	e.moveTo("kitchen", 31)
	e.advance(30 * time.Second)
	e.robot.ClearCommands()
	e.moveTo("kitchen", 30)
	e.advance(30 * time.Second)
	assert.Equal(t, []string{"/api/v2/robot/capabilities/BasicControlCapability map[action:home]"},
		e.commandPaths())
	assert.Contains(t, e.sched().Run.AbortReason, "floor")
}

func TestSchedulerRunThatNeverLeavesTheDock(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen")
	e.robot.SetStatus("docked", "none")
	e.advance(time.Minute)
	assert.NotNil(t, e.sched().Run, "a fresh run gets time to leave the dock")
	e.advance(3 * time.Minute)
	assert.Nil(t, e.sched().Run)
	assert.Equal(t, "stopped", e.sched().LastRun.Outcome)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen"))
}

func TestSchedulerTracksExternalRun(t *testing.T) {
	tests := []struct {
		mode        string
		fullCleared bool
		vacCleared  bool
	}{
		{"vacuum_and_mop", true, true},
		{"vacuum", false, true},
		{"mop", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			e := newSchedEnv(t, ModeLogOnly)
			e.start()
			e.tick()
			e.robot.SetPreset("operation_mode", tt.mode)
			e.robot.SetStatus("cleaning", "segment")
			e.moveTo("pantry", 99)
			e.advance(30 * time.Second)
			run := e.sched().Run
			require.NotNil(t, run)
			assert.Equal(t, "external", run.Origin)
			assert.Equal(t, "", run.Current, "an external room counts only after the dwell time")

			for i := 0; i < 5; i++ {
				e.moveTo("kitchen", 98-i)
				e.advance(30 * time.Second)
			}
			assert.Equal(t, "kitchen", e.sched().Run.Current)
			e.robot.SetStatus("returning", "none")
			e.advance(30 * time.Second)
			e.robot.SetStatus("docked", "none")
			e.moveTo("mud", 90)
			e.advance(30 * time.Second)
			e.advance(30 * time.Second)

			last := e.sched().LastRun
			require.NotNil(t, last)
			assert.Equal(t, []string{"kitchen"}, last.Done, "the pantry was only driven through")
			cleanedFull := e.cleanedAt(itemKey{"kitchen", TreatmentFull}).After(schedT0)
			cleanedVac := e.cleanedAt(itemKey{"kitchen", TreatmentVacuum}).After(schedT0)
			assert.Equal(t, tt.fullCleared, cleanedFull)
			assert.Equal(t, tt.vacCleared, cleanedVac)
			assert.False(t, e.cleanedAt(itemKey{"pantry", TreatmentFull}).After(schedT0))
			assert.Empty(t, e.robot.Commands(), "an external run is only watched")
		})
	}
}

func TestSchedulerManualOffCountsAsCleaned(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.advance(13 * time.Hour)
	require.Equal(t, "on", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"))

	// The owner turns the taint off by hand.
	e.ha.SetState("input_boolean.vacuum_dirty_kitchen_vacuum", "off", map[string]interface{}{})
	e.advance(time.Minute)
	assert.Equal(t, e.clk.Now().Add(-time.Minute).Unix(),
		e.cleanedAt(itemKey{"kitchen", TreatmentVacuum}).Unix(), "treated as cleaned when turned off")
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen_vacuum"), "and not turned on again")
}

func TestSchedulerNoPositionFallsBackToWholeRun(t *testing.T) {
	e := newSchedEnv(t, ModeActive, func(_ *SchedulerConfig, m *housemap.Map) {
		m.Valetudo.PixelSizeM = 0.04 // does not match the robot's 5 cm: the position is not trusted
	})
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("kitchen", 90)
	e.advance(30 * time.Second)
	assert.Equal(t, "on", e.state("input_boolean.vacuum_dirty_lounge"))
	assert.False(t, e.sched().Run.PositionAvailable)
	assert.Contains(t, e.sched().Warnings[0], "pixel size")

	e.robot.SetStatus("docked", "none")
	e.moveTo("mud", 75)
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_lounge"))
	assert.Equal(t, "off", e.state("input_boolean.vacuum_dirty_kitchen"))
	// Drop 25, trip 5, rest 20 split evenly by area: 10 each. 9.6 + 0.3 * (10 - 9.6) = 9.72 -> 9.7.
	assert.InDelta(t, 9.7, e.cost("input_number.vacuum_cost_kitchen"), 1e-9)
	assert.InDelta(t, 0.0, e.cost(tripEntity), 1e-9, "the trip is not learned without positions")
}

func TestSchedulerDockDriftDistrustsPosition(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.robot.SetChargerPixel(pixelAt(5, 5))
	e.robot.SetStatus("cleaning", "segment")
	e.tick()
	assert.Contains(t, e.sched().Warnings[0], "the dock moved")
	assert.Equal(t, "", e.sched().Robot.Room)
}

func TestSchedulerRobotUnreachable(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.robot.Fail("/api/v2/robot/state/attributes", http.StatusBadGateway)
	assert.NotPanics(t, e.tick)
	o := e.sched()
	assert.Contains(t, o.Robot.LastError, "502")
	assert.Nil(t, o.Plan)
}

func TestSchedulerShadowState(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.start()
	e.markDirty("kitchen")
	e.tick()
	o := e.sched()
	assert.True(t, o.Configured)
	assert.Equal(t, "log_only", o.Mode)
	assert.Equal(t, "mud", o.DockRoom)
	assert.Equal(t, "docked", o.Robot.Status)
	assert.Equal(t, 100, o.Robot.Battery)
	assert.Len(t, o.Items, 8)
	assert.InDelta(t, 5.0, o.TripCost, 1e-9)
	for _, it := range o.Items {
		if it.Room == "kitchen" && it.Treatment == "full" {
			assert.True(t, it.Manual)
			assert.InDelta(t, 9.6, it.Cost, 1e-9, "16 m² x 0.6")
			assert.Equal(t, 48.0, it.IntervalHours)
		}
	}
	assert.Equal(t, []string{"kitchen"}, o.Plan.Rooms)
}
