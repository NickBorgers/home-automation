package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/housemap"
	"homeautomation/internal/plugins/roompresence"
	"homeautomation/internal/plugins/vacuum"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/internal/valetudo"
	"homeautomation/internal/valetudo/valetudotest"
	"homeautomation/pkg/occupancy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Vacuum Scheduler Scenario Tests
//
// User story:
//   "Clean the rooms that are dirty, when nobody is in the way and nobody is
//    asleep next door, and only as much as the battery allows."
//
// The house is the made-up example from docs/architecture/HOUSE_MAP.md, with
// room presence running on its example config. Frigate events reach room
// presence through the mock Home Assistant, room presence publishes into the
// occupancy hub, and the vacuum scheduler reads the hub. The robot is a fake
// Valetudo server.
//
//   study | hall    | bedroom      The dock is in the lounge.
//   ------+---------+              The only way from the lounge to the hall,
//   lounge| kitchen |              the bedroom and the porch is through the
//         (porch north of hall)    kitchen.
//
// INVARIANTS:
//   - Rule 1: the robot never drives through a room where a person is.
//   - Rule 2: a sleeper's room and its door neighbours are not cleaned, but
//     may be driven through.
//   - Rule 3: a run starts at 70 % or more and is packed to end at 30 % or more.
//   - A room is marked clean when the robot leaves it for the next room.
//   - When someone walks into the next room, the robot goes home and the
//     unfinished rooms stay dirty.
// ============================================================================

var vacuumT0 = time.Date(2026, 11, 10, 14, 0, 0, 0, time.UTC)

var vacuumRooms = []string{"lounge", "kitchen", "study", "hall", "bedroom", "porch"}

// Room centres of the example house, in metres.
var vacuumCentres = map[string][2]float64{
	"lounge": {3, 2.5}, "kitchen": {8, 2.5}, "study": {2, 7}, "hall": {7, 6.5}, "bedroom": {12, 5.5},
	"porch": {6.5, 9},
}

type vacuumScenario struct {
	t      *testing.T
	server *MockHAServer
	client *ha.Client
	sm     *state.Manager
	robot  *valetudotest.Robot
	hub    *occupancy.Hub
	rp     *roompresence.Manager
	clk    *clock.MockClock
	sched  *vacuum.Scheduler
	shadow *shadowstate.VacuumTracker
}

// vacuumPixel turns a house point into a Valetudo map pixel for the frame set below.
func vacuumPixel(x, y float64) (float64, float64) {
	return x / 0.05, 400 - y/0.05
}

func setupVacuumScenario(t *testing.T, cleanedAgo map[string]time.Duration, costs map[string]float64) *vacuumScenario {
	t.Helper()
	return setupVacuumScenarioWith(t, cleanedAgo, costs, "", nil)
}

// setupVacuumScenarioWith is setupVacuumScenario with extra scheduler config lines, indented as keys of
// scheduler:, and an optional hook that prepares the fake robot before the scheduler starts.
func setupVacuumScenarioWith(t *testing.T, cleanedAgo map[string]time.Duration, costs map[string]float64,
	extraConfig string, prepare func(robot *valetudotest.Robot)) *vacuumScenario {
	t.Helper()
	server, client, sm, baseCleanup := setupTest(t)
	t.Cleanup(baseCleanup)

	// Every room has its helpers. Rooms were cleaned an hour ago unless the test says otherwise.
	for _, room := range vacuumRooms {
		ago := time.Hour
		if d, ok := cleanedAgo[room]; ok {
			ago = d
		}
		server.SetState("input_boolean.vacuum_dirty_"+room, "off", map[string]interface{}{})
		server.SetState("input_datetime.vacuum_cleaned_"+room, "", map[string]interface{}{
			"timestamp": float64(vacuumT0.Add(-ago).Unix())})
		server.SetState("input_number.vacuum_cost_"+room, fmt.Sprintf("%.1f", costs[room]), map[string]interface{}{})
	}
	server.SetState("input_number.vacuum_cost_trip", fmt.Sprintf("%.1f", costs["trip"]), map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	require.Eventually(t, func() bool {
		st, err := client.GetState("input_number.vacuum_cost_trip")
		return err == nil && st != nil
	}, 5*time.Second, 10*time.Millisecond)

	// Room presence on the example config, publishing into the hub.
	hub := occupancy.NewHub()
	rpCfg, err := roompresence.LoadConfig(filepath.Join("..", "..", "internal", "plugins", "roompresence",
		"testdata", "example_config.yaml"))
	require.NoError(t, err)
	rp := roompresence.NewManager(context.Background(), client, sm, rpCfg, "", testlogger.New(), false, nil)
	rp.SetOccupancy(hub)
	require.NoError(t, rp.Start())
	t.Cleanup(rp.Stop)
	require.Eventually(t, func() bool { return server.MQTTSubscriptionCount() == 2 }, 5*time.Second,
		10*time.Millisecond)
	require.Eventually(t, func() bool { return len(hub.Areas()) == 6 }, 5*time.Second, 10*time.Millisecond)

	// The example house map, with a Valetudo frame: 5 cm pixels, no rotation, the dock in the lounge.
	m, err := housemap.Load(filepath.Join("..", "..", "internal", "housemap", "testdata", "example_house_map.yaml"))
	require.NoError(t, err)
	m.Valetudo = &housemap.Valetudo{PixelSizeM: 0.05, AnchorPx: []float64{0, 400}, AnchorM: []float64{0, 0},
		DockM: []float64{1, 1}}

	robot := valetudotest.New()
	t.Cleanup(robot.Close)
	robot.SetChargerPixel(vacuumPixel(1, 1))
	px, py := vacuumPixel(1, 1)
	robot.SetRobotPixel(px, py, true)
	if prepare != nil {
		prepare(robot)
	}

	cfg := loadVacuumSchedulerConfig(t, robot.URL, extraConfig)
	clk := clock.NewMockClock(vacuumT0)
	tracker := shadowstate.NewVacuumTracker()
	s := vacuum.NewScheduler(context.Background(), client, sm, cfg, m, valetudo.NewClient(robot.URL, nil), hub,
		tracker, nil, testlogger.New(), false, clk)
	s.SetManualTicksForTest()
	require.NoError(t, s.Start())
	t.Cleanup(s.Stop)

	return &vacuumScenario{t: t, server: server, client: client, sm: sm, robot: robot, hub: hub, rp: rp, clk: clk,
		sched: s, shadow: tracker}
}

func loadVacuumSchedulerConfig(t *testing.T, url, extra string) *vacuum.SchedulerConfig {
	t.Helper()
	text := `vacuum:
  scheduler:
    mode: active
    valetudo_url: "` + url + `"
    sleepers:
      - {state: isMasterAsleep, rooms: [bedroom]}
    rooms:
      lounge:  {segment_id: "1", full: 24h}
      kitchen: {segment_id: "2", full: 24h}
      study:   {segment_id: "3", full: 24h}
      hall:    {segment_id: "4", full: 24h}
      bedroom: {segment_id: "5", full: 24h}
      porch:   {segment_id: "6", full: 24h}
` + extra
	path := filepath.Join(t.TempDir(), "vacuum_config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
	cfg, err := vacuum.LoadConfig(path)
	require.NoError(t, err)
	require.NoError(t, cfg.Vacuum.Scheduler.Err)
	return &cfg.Vacuum.Scheduler
}

func (v *vacuumScenario) tick() {
	v.sched.TickForTest(true)
}

func (v *vacuumScenario) advance(d time.Duration) {
	v.clk.Advance(d)
	v.sched.TickForTest(false)
}

func (v *vacuumScenario) out() *shadowstate.VacuumSchedulerOutputs {
	return v.shadow.GetState().Outputs.Scheduler
}

// markDirty turns taints on by hand and waits until the app sees them.
func (v *vacuumScenario) markDirty(rooms ...string) {
	for _, r := range rooms {
		v.server.SetState("input_boolean.vacuum_dirty_"+r, "on", map[string]interface{}{})
	}
	for _, r := range rooms {
		v.waitClient("input_boolean.vacuum_dirty_"+r, "on")
	}
}

func (v *vacuumScenario) waitClient(entity, want string) {
	require.Eventually(v.t, func() bool {
		st, err := v.client.GetState(entity)
		return err == nil && st != nil && st.State == want
	}, 5*time.Second, 10*time.Millisecond, entity)
}

func (v *vacuumScenario) moveTo(room string, battery int) {
	c := vacuumCentres[room]
	px, py := vacuumPixel(c[0], c[1])
	v.robot.SetRobotPixel(px, py, true)
	v.robot.SetBattery(battery)
}

// step moves the robot and lets 30 seconds pass.
func (v *vacuumScenario) step(room string, battery int) {
	v.moveTo(room, battery)
	v.advance(30 * time.Second)
}

// walkIntoKitchen sends the Frigate events of a person walking from the hall into the kitchen.
func (v *vacuumScenario) walkIntoKitchen() {
	publishFrigate(v.t, v.server, "new", "p", "cam_kitchen", []string{})
	publishFrigate(v.t, v.server, "update", "p", "cam_kitchen", []string{"kitchen_to_hall"}, "kitchen_to_hall")
	publishFrigate(v.t, v.server, "update", "p", "cam_kitchen", []string{"kitchen_to_hall", "kitchen"}, "kitchen")
	require.Eventually(v.t, func() bool {
		occ, known := v.hub.Occupied("kitchen")
		return occ && known
	}, 5*time.Second, 10*time.Millisecond, "room presence publishes the kitchen as occupied")
}

// startRun lets the start gate pass for the current plan.
func (v *vacuumScenario) startRun() {
	v.t.Helper()
	v.tick()
	require.NotNil(v.t, v.out().Plan.PendingSince, "a plan is pending: %+v", v.out().Plan)
	v.advance(10 * time.Minute)
	require.NotNil(v.t, v.out().Run, "the run started")
}

func (v *vacuumScenario) skipReason(room string) string {
	for _, s := range v.out().Plan.Skipped {
		if s.Room == room {
			return s.Reason
		}
	}
	return ""
}

func (v *vacuumScenario) segmentCleans() []string {
	var out []string
	for _, c := range v.robot.Commands() {
		if c.Path == "/api/v2/robot/capabilities/MapSegmentationCapability" {
			out = append(out, fmt.Sprint(c.Body["segment_ids"]))
		}
	}
	return out
}

// GIVEN the hall, the study and the porch are dirty
// WHEN someone walks into the kitchen
// THEN only the study is cleaned: the hall and the porch are behind the kitchen
func TestScenario_Vacuum_PersonBlocksPath(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)

	t.Log("GIVEN: the hall, the study and the porch are dirty")
	v.markDirty("hall", "study", "porch")

	t.Log("WHEN: a person walks into the kitchen")
	v.walkIntoKitchen()
	v.startRun()

	t.Log("THEN: the run cleans only the study")
	assert.Equal(t, []string{"[3]"}, v.segmentCleans())
	assert.Contains(t, v.skipReason("hall"), "way from the dock enters kitchen")
	assert.Contains(t, v.skipReason("porch"), "way from the dock enters kitchen")
	assert.Contains(t, v.out().Plan.NoEnter, "kitchen")
}

// GIVEN the kitchen, the hall and the porch are dirty
// WHEN master is asleep in the bedroom
// THEN the hall, next to the bedroom, is not cleaned, but the robot drives through it to clean the porch
func TestScenario_Vacuum_SleeperNeighbourSkippedButDrivenThrough(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)

	t.Log("GIVEN: the kitchen, the hall and the porch are dirty")
	v.markDirty("kitchen", "hall", "porch")

	t.Log("WHEN: master is asleep")
	v.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	waitForBoolState(t, v.sm, "isMasterAsleep", true)
	v.startRun()

	t.Log("THEN: the kitchen and the porch are cleaned, the hall is skipped")
	assert.Equal(t, []string{"[2 6]"}, v.segmentCleans())
	assert.Equal(t, "next to sleeper room bedroom", v.skipReason("hall"))

	t.Log("AND: driving through the hall does not stop the run")
	v.step("kitchen", 95)
	v.step("hall", 94)
	v.step("porch", 93)
	assert.False(t, v.out().Run.Aborted, "the hall may be driven through")
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_kitchen", "off")
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_hall").State, "the hall stays dirty")
	for _, c := range v.robot.Commands() {
		assert.NotEqual(t, "/api/v2/robot/capabilities/BasicControlCapability", c.Path, "no pause, no return")
	}
}

// GIVEN four overdue rooms whose costs add up to more than the battery allows
// WHEN the robot is docked at 80 %
// THEN the most overdue rooms are packed until trip plus costs reach 80 - 30 = 50 %
func TestScenario_Vacuum_BatteryPacking(t *testing.T) {
	v := setupVacuumScenario(t,
		map[string]time.Duration{"study": 72 * time.Hour, "kitchen": 48 * time.Hour, "hall": 36 * time.Hour,
			"porch": 30 * time.Hour},
		map[string]float64{"study": 20, "kitchen": 20, "hall": 15, "porch": 5, "trip": 5})

	t.Log("GIVEN: the robot is docked at 69 %")
	v.robot.SetBattery(69)
	v.tick()
	assert.Contains(t, v.out().Plan.Reason, "below start minimum 70%")

	t.Log("WHEN: it charges to 80 %")
	v.robot.SetBattery(80)
	v.startRun()

	t.Log("THEN: study 20 + kitchen 20 + porch 5 + trip 5 = 50 fits; the hall (15) does not")
	assert.Equal(t, []string{"study", "kitchen", "porch"}, v.out().Run.Targets)
	assert.Equal(t, []string{"[3 2 6]"}, v.segmentCleans())
	var hall string
	for _, s := range v.out().Plan.Skipped {
		if s.Room == "hall" {
			hall = s.Reason
		}
	}
	assert.Contains(t, hall, "does not fit battery: needs 15.0%")
}

// GIVEN a run of the study and the kitchen
// WHEN the robot leaves the study for the kitchen
// THEN the study is marked clean at once; the kitchen stays dirty until the robot leaves it
func TestScenario_Vacuum_RoomMarkedCleanAsRobotLeaves(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("study", "kitchen")
	v.startRun()
	require.Equal(t, []string{"study", "kitchen"}, v.out().Run.Targets)

	t.Log("WHEN: the robot cleans the study and drives through the lounge to the kitchen")
	v.step("study", 97)
	v.step("study", 93)
	v.step("lounge", 92)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_study").State,
		"the study is not done while the robot might come back")
	v.step("kitchen", 91)

	t.Log("THEN: the study is clean, the kitchen is not yet")
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_study", "off")
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_kitchen").State)
	ts, ok := v.server.GetState("input_datetime.vacuum_cleaned_study").Attributes["timestamp"].(float64)
	require.True(t, ok)
	assert.Equal(t, v.clk.Now().Unix(), int64(ts))

	t.Log("AND: the kitchen is clean once the robot is back on the dock for good")
	v.step("kitchen", 85)
	v.robot.SetStatus("returning", "none")
	v.step("kitchen", 84)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 83)
	v.step("lounge", 83)
	waitForServerState(t, v.server, "input_boolean.vacuum_dirty_kitchen", "off")
	assert.Equal(t, "completed", v.out().LastRun.Outcome)
	assert.Equal(t, []string{"study", "kitchen"}, v.out().LastRun.Done)
}

// GIVEN a run of the study and then the kitchen
// WHEN someone walks into the kitchen while the robot cleans the study
// THEN the robot goes home at once, and the kitchen stays dirty
func TestScenario_Vacuum_AbortWhenSomeoneWalksIntoNextRoom(t *testing.T) {
	v := setupVacuumScenario(t, nil, nil)
	v.markDirty("study", "kitchen")
	v.startRun()
	v.step("study", 97)
	v.robot.ClearCommands()

	t.Log("WHEN: a person walks into the kitchen")
	v.walkIntoKitchen()
	v.tick()

	t.Log("THEN: the robot is sent home; the way from the study through the lounge is clear")
	cmds := v.robot.Commands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "/api/v2/robot/capabilities/BasicControlCapability", cmds[0].Path)
	assert.Equal(t, "home", cmds[0].Body["action"])
	assert.Contains(t, v.out().Run.AbortReason, "kitchen")

	v.step("lounge", 95)
	v.robot.SetStatus("docked", "none")
	v.step("lounge", 95)
	v.step("lounge", 95)
	assert.Equal(t, "aborted", v.out().LastRun.Outcome)
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_kitchen").State, "unfinished rooms stay dirty")
	assert.Equal(t, "on", v.server.GetState("input_boolean.vacuum_dirty_study").State,
		"the study was not finished either")
}
