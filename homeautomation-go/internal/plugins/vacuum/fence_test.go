package vacuum

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"homeautomation/internal/housemap"
	"homeautomation/internal/valetudo"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const restrictionsPath = "/api/v2/robot/capabilities/" + valetudo.CapabilityRestrictions

// ownerRestrictions is the live robot's set today: no walls, one zone the owner drew in the entryway.
const ownerRestrictions = `{"virtualWalls":[],"restrictedZones":[{"points":{"pA":{"x":3005,"y":4150},
"pB":{"x":3085,"y":4150},"pC":{"x":3085,"y":4205},"pD":{"x":3005,"y":4205}},"type":"regular"}]}`

// ownerStored is ownerRestrictions as the helper keeps it.
const ownerStored = `{"w":[],"z":[["r",3005,4150,3085,4205]]}`

// The walls across the bed's two doors in the test house, with the 0.25 m margin, in Valetudo cm. The frame
// maps x metres to 100x cm and y metres to 2000-100y cm.
var (
	wallOfficeBed = valetudo.NewVirtualWall(400, 1425, 400, 1275) // office_bed [4,6]-[4,7]
	wallPantryBed = valetudo.NewVirtualWall(475, 1600, 625, 1600) // pantry_bed [5,4]-[6,4]
)

func decodeRestrictions(t *testing.T, js string) valetudo.Restrictions {
	t.Helper()
	var r valetudo.Restrictions
	require.NoError(t, json.Unmarshal([]byte(js), &r))
	return r
}

func ownerSet(t *testing.T) valetudo.Restrictions {
	return decodeRestrictions(t, ownerRestrictions)
}

// robotSet is what the fake robot holds now.
func (e *schedEnv) robotSet() valetudo.Restrictions {
	return decodeRestrictions(e.t, e.robot.Restrictions())
}

// restrictionPuts counts the restriction writes the robot got.
func (e *schedEnv) restrictionPuts() int {
	n := 0
	for _, c := range e.robot.Commands() {
		if c.Path == restrictionsPath {
			n++
		}
	}
	return n
}

// newFenceEnv is an active scheduler with the owner's zone on the robot and master asleep, so the bed is a
// no-enter room next to the pantry and the office. Reads back without waiting.
func newFenceEnv(t *testing.T, opts ...func(cfg *SchedulerConfig, m *housemap.Map)) *schedEnv {
	t.Helper()
	e := newSchedEnv(t, ModeActive, opts...)
	e.s.fenceVerifyDelay = time.Millisecond
	e.robot.SetRestrictions(ownerRestrictions)
	require.NoError(t, e.sm.SetBool("isMasterAsleep", true))
	return e
}

func observe(e *schedEnv) *observer.ObservedLogs {
	core, logs := observer.New(zap.InfoLevel)
	e.s.logger = zap.New(core)
	return logs
}

// ---------------------------------------------------------------------------
// Wall computation
// ---------------------------------------------------------------------------

func TestFenceWallsSyntheticMap(t *testing.T) {
	m := loadTestMap(t)
	g := m.Graph()

	t.Run("sleeper room: both doors from reachable rooms", func(t *testing.T) {
		walls, unplaced := fenceWalls(m, g, "mud", map[string]string{"bed": "sleeper room"}, 0.25)
		require.Len(t, walls, 2)
		assert.Empty(t, unplaced)
		assert.Equal(t, doorWall{door: "office_bed", from: "office", into: "bed",
			a: housemap.Point{4, 5.75}, b: housemap.Point{4, 7.25}}, walls[0])
		assert.Equal(t, doorWall{door: "pantry_bed", from: "pantry", into: "bed",
			a: housemap.Point{4.75, 4}, b: housemap.Point{6.25, 4}}, walls[1])
	})

	t.Run("open wing: one wall, nothing behind it", func(t *testing.T) {
		noEnter := map[string]string{"family": "x", "strip": "x", "den": "x"}
		walls, _ := fenceWalls(m, g, "mud", noEnter, 0.25)
		require.Len(t, walls, 1, "the study is behind the wing, so den_study needs no wall")
		assert.Equal(t, "kitchen_family", walls[0].door)
		assert.Equal(t, "kitchen", walls[0].from)
	})

	t.Run("a door between two no-enter rooms gets no wall", func(t *testing.T) {
		walls, _ := fenceWalls(m, g, "mud", map[string]string{"bed": "x", "bath": "x"}, 0.25)
		var doors []string
		for _, w := range walls {
			doors = append(doors, w.door)
		}
		assert.Equal(t, []string{"office_bed", "pantry_bed"}, doors)
	})

	t.Run("no margin is the door span", func(t *testing.T) {
		walls, _ := fenceWalls(m, g, "mud", map[string]string{"bed": "x"}, 0)
		assert.Equal(t, housemap.Point{5, 4}, walls[1].a)
		assert.Equal(t, housemap.Point{6, 4}, walls[1].b)
	})

	t.Run("blocked dock: nothing is reachable", func(t *testing.T) {
		walls, _ := fenceWalls(m, g, "mud", map[string]string{"mud": "x", "pantry": "x"}, 0.25)
		assert.Empty(t, walls)
	})

	t.Run("a door with no span is reported", func(t *testing.T) {
		m := loadTestMap(t)
		m.Doors["office_bed"].Span = nil
		walls, unplaced := fenceWalls(m, m.Graph(), "mud", map[string]string{"bed": "x"}, 0.25)
		require.Len(t, walls, 1)
		assert.Equal(t, "pantry_bed", walls[0].door)
		assert.Equal(t, []string{"office_bed"}, unplaced)
	})

	t.Run("exterior doors are never walled", func(t *testing.T) {
		all := allDoorWalls(m, g, 0.25)
		for _, w := range all {
			assert.NotEqual(t, "front", w.door)
		}
		assert.Len(t, all, 11)
	})
}

func TestFenceWallToValetudo(t *testing.T) {
	m := loadTestMap(t)
	walls, _ := fenceWalls(m, m.Graph(), "mud", map[string]string{"bed": "x"}, 0.25)
	got := wallsToValetudo(m.Valetudo, 5, walls)
	assert.Equal(t, []valetudo.VirtualWall{wallOfficeBed, wallPantryBed}, got)

	// A turned frame: the wall lands where ToFrame says it is.
	turned := &housemap.Valetudo{RotationDeg: 150, PixelSizeM: 0.05, AnchorPx: []float64{400, 300.5},
		AnchorM: []float64{10, 8}}
	w := toValetudo(turned, 5, doorWall{a: housemap.Point{9.13397459621556, 7.5}, b: housemap.Point{10, 8}})
	assert.Equal(t, valetudo.NewVirtualWall(2100, 1503, 2000, 1503), w)
}

func TestCheckFenceKeepsRoute(t *testing.T) {
	m := loadTestMap(t)
	g := m.Graph()
	walls, _ := fenceWalls(m, g, "mud", map[string]string{"bed": "x"}, 0.25)
	assert.NoError(t, checkFenceKeepsRoute(m, g, "mud", []string{"kitchen", "lounge"}, walls))
	assert.NoError(t, checkFenceKeepsRoute(m, g, "mud", []string{"office"}, walls))
	// A plan whose shortest leg runs through the walled room is cut. The planner never makes one: its legs
	// avoid no_enter rooms. The check still catches it. Office to kitchen is shortest through the bed.
	assert.ErrorContains(t, checkFenceKeepsRoute(m, g, "mud", []string{"office", "kitchen"}, walls),
		"would block door")

	// The office door moves to the corner next to the pantry-bed door, and a wide margin reaches it.
	m.Doors["mud_office"].Span = []housemap.Point{{3, 4}, {4, 4}}
	g = m.Graph()
	walls, _ = fenceWalls(m, g, "mud", map[string]string{"bed": "x"}, 1.0)
	err := checkFenceKeepsRoute(m, g, "mud", []string{"office"}, walls)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pantry_bed")
	assert.Contains(t, err.Error(), "mud_office")
	assert.NoError(t, checkFenceKeepsRoute(m, g, "mud", []string{"kitchen"}, walls),
		"a run that does not use the office door is fine")

	err = checkFenceKeepsRoute(m, g, "bed", nil, walls)
	assert.ErrorContains(t, err, "fence in the dock room")
}

func TestSegmentsTouch(t *testing.T) {
	p := func(x, y float64) housemap.Point { return housemap.Point{x, y} }
	assert.True(t, segmentsTouch(p(0, 0), p(2, 2), p(0, 2), p(2, 0)), "cross")
	assert.True(t, segmentsTouch(p(0, 0), p(2, 0), p(2, 0), p(3, 1)), "end to end")
	assert.True(t, segmentsTouch(p(0, 0), p(4, 0), p(1, 0), p(2, 0)), "collinear overlap")
	assert.False(t, segmentsTouch(p(0, 0), p(1, 0), p(2, 0), p(3, 0)), "collinear apart")
	assert.False(t, segmentsTouch(p(0, 0), p(1, 0), p(0, 1), p(1, 1)), "parallel")
	assert.False(t, segmentsTouch(p(0, 0), p(1, 1), p(2, 0), p(3, -1)), "apart")
}

// ---------------------------------------------------------------------------
// The saved copy and comparing sets
// ---------------------------------------------------------------------------

func TestFenceStoreRoundTrip(t *testing.T) {
	owner := ownerSet(t)
	value, err := encodeFence(owner)
	require.NoError(t, err)
	assert.Equal(t, ownerStored, value, "a Dreame rectangle keeps only two corners")
	back, err := decodeFence(value)
	require.NoError(t, err)
	assert.Equal(t, owner, *back)

	var slanted valetudo.RestrictedZone
	slanted.Type = valetudo.ZoneMop
	slanted.Points.PA, slanted.Points.PB = valetudo.RestrictionPoint{X: 0, Y: 0}, valetudo.RestrictionPoint{X: 10, Y: 5}
	slanted.Points.PC, slanted.Points.PD = valetudo.RestrictionPoint{X: 5, Y: 15}, valetudo.RestrictionPoint{X: -5, Y: 10}
	mixed := valetudo.Restrictions{
		VirtualWalls:    []valetudo.VirtualWall{valetudo.NewVirtualWall(1, 2, 3, 4)},
		RestrictedZones: []valetudo.RestrictedZone{owner.RestrictedZones[0], slanted},
	}
	value, err = encodeFence(mixed)
	require.NoError(t, err)
	assert.Equal(t, `{"w":[[1,2,3,4]],"z":[["r",3005,4150,3085,4205],["m",0,0,10,5,5,15,-5,10]]}`, value)
	back, err = decodeFence(value)
	require.NoError(t, err)
	assert.Equal(t, mixed, *back)

	empty, err := encodeFence(valetudo.Restrictions{})
	require.NoError(t, err)
	assert.Equal(t, `{"w":[],"z":[]}`, empty, "an empty owner's set is still a saved set, unlike \"\"")

	for _, bad := range []string{`nope`, `{}`, `{"w":[[1,2,3]],"z":[]}`, `{"w":[],"z":[["x",1,2,3,4]]}`,
		`{"w":[],"z":[["r",1,2,3]]}`, `{"w":[],"z":[],"extra":1}`} {
		_, err := decodeFence(bad)
		assert.Error(t, err, bad)
	}
}

func TestSameRestrictionsAndStrip(t *testing.T) {
	owner := ownerSet(t)
	fenced := withWalls(owner, []valetudo.VirtualWall{wallOfficeBed, wallPantryBed})
	assert.False(t, sameRestrictions(owner, fenced))

	// Order, endpoint order and a centimetre of rounding do not matter.
	read := valetudo.Restrictions{
		VirtualWalls:    []valetudo.VirtualWall{valetudo.NewVirtualWall(626, 1600, 475, 1601), wallOfficeBed},
		RestrictedZones: owner.RestrictedZones,
	}
	assert.True(t, sameRestrictions(fenced, read))
	read.VirtualWalls[0] = valetudo.NewVirtualWall(628, 1600, 475, 1600)
	assert.False(t, sameRestrictions(fenced, read), "3 cm is another wall")

	ownerWall := valetudo.NewVirtualWall(100, 100, 200, 100)
	cur := withWalls(owner, []valetudo.VirtualWall{wallPantryBed, ownerWall})
	got, n := stripWalls(cur, []valetudo.VirtualWall{wallOfficeBed, wallPantryBed})
	assert.Equal(t, 1, n)
	assert.Equal(t, withWalls(owner, []valetudo.VirtualWall{ownerWall}), got, "the owner's wall and zone stay")

	m := loadTestMap(t)
	all := allDoorWalls(m, m.Graph(), 0.25)
	assert.Equal(t, []string{"pantry_bed"}, matchedDoors(cur, all, wallsToValetudo(m.Valetudo, 5, all)))
}

// ---------------------------------------------------------------------------
// The start sequence
// ---------------------------------------------------------------------------

func TestFenceStartSequence(t *testing.T) {
	e := newFenceEnv(t)
	e.start()
	e.tick() // the startup check: the robot holds no scheduler wall, so nothing is written
	assert.Zero(t, e.restrictionPuts())
	before := len(e.robot.Requests())
	e.markDirty("kitchen")
	e.s.TickForTest(true)
	require.NotNil(t, e.sched().Plan.PendingSince)
	e.advance(10 * time.Minute)

	// Read, save, write owner's set plus walls, read back, then start.
	var seq []string
	for _, r := range e.robot.Requests()[before:] {
		if strings.Contains(r, "CombinedVirtualRestrictions") || strings.HasPrefix(r, "PUT") {
			seq = append(seq, strings.TrimPrefix(r, "/api/v2/robot/capabilities/"))
		}
	}
	assert.Equal(t, []string{
		"GET " + restrictionsPath,
		"PUT " + restrictionsPath,
		"GET " + restrictionsPath,
		"PUT /api/v2/robot/capabilities/OperationModeControlCapability/preset",
		"PUT /api/v2/robot/capabilities/FanSpeedControlCapability/preset",
		"PUT /api/v2/robot/capabilities/WaterUsageControlCapability/preset",
		"PUT /api/v2/robot/capabilities/MapSegmentationCapability",
	}, seq)

	assert.Equal(t, ownerStored, e.state(fenceEntity), "the owner's set is saved")
	got := e.robotSet()
	assert.True(t, sameRestrictions(withWalls(ownerSet(t), []valetudo.VirtualWall{wallOfficeBed, wallPantryBed}), got),
		"the owner's zone stays, and the bed's doors are walled: %+v", got)
	run := e.sched().Run
	require.NotNil(t, run)
	assert.Equal(t, []string{"office_bed", "pantry_bed"}, run.FencedDoors)
	assert.True(t, e.sched().FenceRestorePending)

	t.Log("the run completes: the owner's set is back exactly and the helper is empty")
	e.step("kitchen", 90)
	e.robot.SetStatus("docked", "none")
	e.step("mud", 89)
	e.step("mud", 89)
	require.Equal(t, "completed", e.sched().LastRun.Outcome)
	assert.Equal(t, ownerSet(t), e.robotSet())
	assert.Equal(t, "", e.state(fenceEntity))
	assert.False(t, e.sched().FenceRestorePending)
}

func TestFenceNothingToFenceTouchesNothing(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.robot.SetRestrictions(ownerRestrictions)
	e.start()
	e.startRun("kitchen")
	assert.Zero(t, e.restrictionPuts(), "no no-enter room: no write")
	assert.Empty(t, e.sched().Run.FencedDoors)
	assert.Equal(t, "", e.state(fenceEntity))
}

func TestFenceFailureDoesNotStart(t *testing.T) {
	segmentStarted := func(e *schedEnv) bool {
		for _, c := range e.robot.Commands() {
			if c.Path == "/api/v2/robot/capabilities/MapSegmentationCapability" {
				return true
			}
		}
		return false
	}
	tryStart := func(e *schedEnv) {
		e.markDirty("kitchen")
		e.tick()
		e.advance(10 * time.Minute)
	}

	t.Run("cannot read the restrictions", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.robot.FailMethod(http.MethodGet, restrictionsPath, http.StatusInternalServerError)
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.False(t, segmentStarted(e))
		assert.Zero(t, e.restrictionPuts())
		assert.Equal(t, "", e.state(fenceEntity))
		assert.True(t, e.hasWarning("read the robot's virtual restrictions"), "%v", e.sched().Warnings)
	})

	t.Run("cannot save the owner's set", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.ha.SetServiceError("input_text", "set_value", errors.New("boom"))
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.False(t, segmentStarted(e))
		assert.Zero(t, e.restrictionPuts(), "no wall goes up before the owner's set is saved")
		assert.Equal(t, ownerSet(t), e.robotSet())
	})

	t.Run("owner's set does not fit the helper", func(t *testing.T) {
		e := newFenceEnv(t)
		e.ha.SetState(fenceEntity, "", map[string]interface{}{"max": float64(20)})
		e.start()
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.Zero(t, e.restrictionPuts())
		assert.True(t, e.hasWarning("maximum length to 255"), "%v", e.sched().Warnings)
	})

	t.Run("write fails", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.robot.FailMethod(http.MethodPut, restrictionsPath, http.StatusInternalServerError)
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.False(t, segmentStarted(e))
		assert.Equal(t, ownerSet(t), e.robotSet())
		assert.Equal(t, "", e.state(fenceEntity), "the robot still has the owner's set; nothing is pending")
		assert.False(t, e.sched().FenceRestorePending)
	})

	t.Run("walls do not take", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.robot.IgnoreRestrictionWrites(true)
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.False(t, segmentStarted(e))
		assert.Equal(t, ownerSet(t), e.robotSet())
		assert.Equal(t, "", e.state(fenceEntity))
	})

	t.Run("a preset fails after the walls went up", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.robot.Fail("/api/v2/robot/capabilities/OperationModeControlCapability/preset", http.StatusInternalServerError)
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.False(t, segmentStarted(e))
		assert.Equal(t, 2, e.restrictionPuts(), "walls up, then the owner's set back")
		assert.Equal(t, ownerSet(t), e.robotSet())
		assert.Equal(t, "", e.state(fenceEntity))
	})

	t.Run("a wall would cut the way", func(t *testing.T) {
		e := newFenceEnv(t, func(cfg *SchedulerConfig, m *housemap.Map) {
			m.Doors["mud_office"].Span = []housemap.Point{{3, 4}, {4, 4}}
			margin := 1.0
			cfg.FenceMarginM = &margin
		})
		e.start()
		e.ha.SetState(dirtyEntity(itemKey{Room: "kitchen", Treatment: TreatmentFull}), "off", nil)
		e.markDirty("lounge")
		e.tick()
		e.advance(10 * time.Minute)
		require.NotNil(t, e.sched().Run, "the lounge run does not use the office door")
	})

	t.Run("frame does not fit", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.robot.SetChargerPixel(pixelAt(5, 5))
		tryStart(e)
		assert.Nil(t, e.sched().Run)
		assert.Zero(t, e.restrictionPuts())
		assert.True(t, e.hasWarning("the dock moved"), "%v", e.sched().Warnings)
	})
}

func TestFenceOffOrUnavailable(t *testing.T) {
	t.Run("fence_no_enter false", func(t *testing.T) {
		off := false
		e := newFenceEnv(t, func(cfg *SchedulerConfig, _ *housemap.Map) { cfg.FenceNoEnter = &off })
		e.start()
		e.startRun("kitchen")
		assert.Zero(t, e.restrictionPuts())
	})
	t.Run("missing helper warns and runs without walls", func(t *testing.T) {
		e := newFenceEnv(t)
		e.ha.SetMockState(fenceEntity, nil) // the helper does not exist
		e.start()
		e.startRun("kitchen")
		assert.Zero(t, e.restrictionPuts())
		assert.True(t, e.hasWarning("missing helper input_text.vacuum_fence_saved"), "%v", e.sched().Warnings)
	})
	t.Run("helper unavailable: no start", func(t *testing.T) {
		e := newFenceEnv(t)
		e.ha.SetState(fenceEntity, "unavailable", nil)
		e.start()
		e.markDirty("kitchen")
		e.tick()
		e.advance(10 * time.Minute)
		assert.Nil(t, e.sched().Run)
		assert.True(t, e.hasWarning("input_text.vacuum_fence_saved is unavailable"), "%v", e.sched().Warnings)
	})
	t.Run("helper unavailable since startup: no start even without walls", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive) // nobody asleep, every area clear: no wall is needed
		e.ha.SetState(fenceEntity, "unavailable", nil)
		e.start()
		e.markDirty("kitchen")
		e.tick()
		e.advance(10 * time.Minute)
		assert.Nil(t, e.sched().Run, "saved walls from before the restart may still be up")
	})
	t.Run("helper unavailable later: a run without walls starts", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive)
		e.start()
		e.tick() // startup check done
		e.ha.SetState(fenceEntity, "unavailable", nil)
		e.markDirty("kitchen")
		e.s.TickForTest(true)
		e.advance(10 * time.Minute)
		require.NotNil(t, e.sched().Run)
		assert.Zero(t, e.restrictionPuts())
	})
	t.Run("log_only logs the walls and sends nothing", func(t *testing.T) {
		e := newSchedEnv(t, ModeLogOnly)
		require.NoError(t, e.sm.SetBool("isMasterAsleep", true))
		logs := observe(e)
		e.start()
		e.markDirty("kitchen")
		e.tick()
		e.advance(10 * time.Minute)
		would := logs.FilterMessage("Vacuum would fence a no-enter room").All()
		require.Len(t, would, 2)
		assert.Equal(t, "office_bed", would[0].ContextMap()["door"])
		assert.Equal(t, "pantry_bed", would[1].ContextMap()["door"])
		assert.Empty(t, e.robot.Commands())
		for _, r := range e.robot.Requests() {
			assert.NotContains(t, r, "CombinedVirtualRestrictions", "log_only does not even read them")
		}
	})
	t.Run("read-only sends and logs no walls", func(t *testing.T) {
		e := newFenceEnv(t)
		e.s.readOnly = true
		logs := observe(e)
		e.start()
		e.markDirty("kitchen")
		e.tick()
		e.advance(10 * time.Minute)
		assert.Empty(t, logs.FilterMessage("Vacuum would fence a no-enter room").All())
		assert.Empty(t, e.robot.Commands())
	})
}

// ---------------------------------------------------------------------------
// Restore on every end
// ---------------------------------------------------------------------------

func TestFenceRestoredOnEveryEnd(t *testing.T) {
	for _, tt := range []struct {
		outcome string
		end     func(e *schedEnv)
	}{
		{"completed", func(e *schedEnv) {
			e.step("kitchen", 90)
			e.robot.SetStatus("docked", "none")
			e.step("mud", 89)
			e.step("mud", 89)
		}},
		{"aborted", func(e *schedEnv) {
			e.step("kitchen", 90)
			clearAreas(e.hub, "kitchen")
			e.s.TickForTest(true) // the robot is in a room someone walked into: home
			e.robot.SetStatus("docked", "none")
			e.step("mud", 89)
			e.step("mud", 89)
		}},
		{"stopped", func(e *schedEnv) { // never left the dock
			e.robot.SetStatus("docked", "none")
			e.advance(time.Minute)
			e.advance(3 * time.Minute)
		}},
		{"stalled", func(e *schedEnv) {
			e.step("kitchen", 90)
			e.robot.SetStatus("idle", "resumable")
			for i := 0; i < 62; i++ {
				e.advance(30 * time.Second)
			}
		}},
		{"error", func(e *schedEnv) {
			e.step("kitchen", 90)
			e.robot.SetStatus("error", "none")
			for i := 0; i < 62; i++ {
				e.advance(30 * time.Second)
			}
		}},
	} {
		t.Run(tt.outcome, func(t *testing.T) {
			e := newFenceEnv(t)
			e.start()
			e.startRun("kitchen")
			require.NotEqual(t, ownerSet(t), e.robotSet(), "walls are up")
			tt.end(e)
			require.NotNil(t, e.sched().LastRun)
			assert.Equal(t, tt.outcome, e.sched().LastRun.Outcome)
			assert.Equal(t, ownerSet(t), e.robotSet())
			assert.Equal(t, "", e.state(fenceEntity))
			assert.False(t, e.sched().FenceRestorePending)
		})
	}
}

func TestFenceRestoreRetriedAndBlocksNextRun(t *testing.T) {
	e := newFenceEnv(t)
	e.start()
	e.startRun("kitchen")
	e.step("kitchen", 90)
	e.robot.FailMethod(http.MethodPut, restrictionsPath, http.StatusInternalServerError)
	e.robot.SetStatus("docked", "none")
	e.step("mud", 89)
	e.step("mud", 89)
	require.Nil(t, e.sched().Run)
	assert.True(t, e.sched().FenceRestorePending)
	assert.Equal(t, ownerStored, e.state(fenceEntity), "kept until the robot has the owner's set back")
	assert.True(t, e.hasWarning("could not restore"), "%v", e.sched().Warnings)

	t.Log("a new dirty room does not start while the walls are still up")
	e.robot.ClearCommands()
	e.markDirty("lounge")
	e.tick()
	e.advance(10 * time.Minute)
	assert.Nil(t, e.sched().Run)
	assert.True(t, e.hasWarning("could not restore"), "the refused start keeps the real cause: %v",
		e.sched().Warnings)

	t.Log("the robot takes writes again: the owner's set goes back on the next tick")
	e.robot.FailMethod(http.MethodPut, restrictionsPath, 0)
	e.advance(30 * time.Second)
	assert.Equal(t, ownerSet(t), e.robotSet())
	assert.Equal(t, "", e.state(fenceEntity))
	assert.False(t, e.sched().FenceRestorePending)
}

// ---------------------------------------------------------------------------
// Restore at startup
// ---------------------------------------------------------------------------

func TestFenceRestoredAtStartup(t *testing.T) {
	fencedJSON := func(t *testing.T) string {
		b, err := json.Marshal(withWalls(ownerSet(t), []valetudo.VirtualWall{wallOfficeBed, wallPantryBed}))
		require.NoError(t, err)
		return string(b)
	}

	t.Run("saved set is put back", func(t *testing.T) {
		e := newFenceEnv(t)
		e.robot.SetRestrictions(fencedJSON(t))
		e.ha.SetState(fenceEntity, ownerStored, map[string]interface{}{"max": float64(255)})
		e.start()
		e.tick()
		assert.Equal(t, ownerSet(t), e.robotSet())
		assert.Equal(t, "", e.state(fenceEntity))
	})

	t.Run("save lost: only walls that match the scheduler's door walls go", func(t *testing.T) {
		e := newFenceEnv(t)
		ownerWall := valetudo.NewVirtualWall(100, 100, 200, 100)
		b, err := json.Marshal(withWalls(ownerSet(t), []valetudo.VirtualWall{wallPantryBed, ownerWall}))
		require.NoError(t, err)
		e.robot.SetRestrictions(string(b))
		e.start()
		e.tick()
		assert.Equal(t, withWalls(ownerSet(t), []valetudo.VirtualWall{ownerWall}), e.robotSet())
	})

	t.Run("nothing to take away writes nothing", func(t *testing.T) {
		e := newFenceEnv(t)
		e.start()
		e.tick()
		assert.Zero(t, e.restrictionPuts())
	})

	t.Run("not while a run is tracked", func(t *testing.T) {
		e := newFenceEnv(t)
		e.robot.SetRestrictions(fencedJSON(t))
		e.ha.SetState(fenceEntity, ownerStored, map[string]interface{}{"max": float64(255)})
		e.robot.SetStatus("cleaning", "segment")
		e.start()
		e.tick()
		e.step("kitchen", 90)
		require.NotNil(t, e.sched().Run, "the job from before the restart is tracked")
		assert.Zero(t, e.restrictionPuts(), "walls stay while the robot is on its job")
		e.robot.SetStatus("docked", "none")
		e.step("mud", 89)
		e.step("mud", 89)
		require.Nil(t, e.sched().Run)
		assert.Equal(t, ownerSet(t), e.robotSet())
		assert.Equal(t, "", e.state(fenceEntity))
	})

	t.Run("log_only leaves it and warns", func(t *testing.T) {
		e := newSchedEnv(t, ModeLogOnly)
		e.robot.SetRestrictions(fencedJSON(t))
		e.ha.SetState(fenceEntity, ownerStored, map[string]interface{}{"max": float64(255)})
		e.start()
		e.tick()
		assert.Empty(t, e.robot.Commands())
		assert.True(t, e.hasWarning("holds the owner's saved virtual restrictions"), "%v", e.sched().Warnings)
	})

	t.Run("unreadable copy: scheduler walls still go", func(t *testing.T) {
		e := newFenceEnv(t)
		e.robot.SetRestrictions(fencedJSON(t))
		e.ha.SetState(fenceEntity, "garbage", map[string]interface{}{"max": float64(255)})
		e.start()
		e.tick()
		assert.Equal(t, ownerSet(t), e.robotSet())
		assert.Equal(t, "", e.state(fenceEntity))
	})
}

// ---------------------------------------------------------------------------
// Stopped after a pause, stranded robots, off-route rooms
// ---------------------------------------------------------------------------

// A Dreame reports idle, not paused, after the pause command, with the flag resumable or none. The run stays
// ours: no stall, no end, and home as soon as the way clears.
func TestSchedulerIdleAfterOccupancyPauseIsOurPause(t *testing.T) {
	for _, flag := range []string{"resumable", "none"} {
		t.Run(flag, func(t *testing.T) {
			e := newSchedEnv(t, ModeActive)
			e.start()
			e.startRun("kitchen", "lounge")
			e.moveTo("lounge", 97)
			e.advance(30 * time.Second)
			clearAreas(e.hub, "pantry")
			e.s.TickForTest(true)
			require.True(t, e.sched().Run.Paused)
			e.robot.SetStatus("idle", flag)
			e.robot.ClearCommands()

			for i := 0; i < 240; i++ { // two hours
				e.advance(30 * time.Second)
			}
			require.NotNil(t, e.sched().Run, "waiting for the way home is not a stall, nor the end of the run")
			assert.True(t, e.sched().Run.Paused)
			assert.Empty(t, e.robot.Commands(), "nothing is sent while the return path is blocked")

			clearAreas(e.hub)
			e.s.TickForTest(true)
			assert.Equal(t, []string{cmdHome}, e.commandPaths())
		})
	}
}

func TestSchedulerErrorAfterPauseStillStalls(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	e.startRun("kitchen", "lounge")
	e.moveTo("lounge", 97)
	e.advance(30 * time.Second)
	clearAreas(e.hub, "pantry")
	e.s.TickForTest(true)
	require.True(t, e.sched().Run.Paused)
	e.robot.SetStatus("error", "none")
	for i := 0; i < 62; i++ {
		e.advance(30 * time.Second)
	}
	require.Nil(t, e.sched().Run)
	assert.Equal(t, "error", e.sched().LastRun.Outcome)
}

func TestSchedulerSendsStrandedRobotHome(t *testing.T) {
	t.Run("home once the robot stood for stranded_after and the way is clear", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive)
		e.start()
		e.moveTo("kitchen", 80)
		e.robot.SetStatus("idle", "none")
		e.tick()
		e.advance(4 * time.Minute)
		assert.Empty(t, e.robot.Commands(), "not yet")
		e.advance(time.Minute)
		assert.Equal(t, []string{cmdHome}, e.commandPaths())
		e.robot.SetStatus("idle", "none") // the robot did not move
		e.advance(time.Minute)
		assert.Len(t, e.robot.Commands(), 1, "not again right away")
		e.advance(4 * time.Minute)
		assert.Len(t, e.robot.Commands(), 2, "again after another stranded_after")
	})

	t.Run("waits while the return path is blocked", func(t *testing.T) {
		e := newSchedEnv(t, ModeActive)
		e.start()
		clearAreas(e.hub, "pantry")
		e.moveTo("kitchen", 80)
		e.robot.SetStatus("paused", "resumable")
		e.tick()
		e.advance(10 * time.Minute)
		assert.Empty(t, e.robot.Commands())
		assert.True(t, e.hasWarning("way home enters pantry"), "%v", e.sched().Warnings)
		clearAreas(e.hub)
		e.s.TickForTest(true)
		assert.Equal(t, []string{cmdHome}, e.commandPaths())
	})

	t.Run("left alone", func(t *testing.T) {
		for name, tt := range map[string]struct {
			mode, status string
			noPos        bool
		}{
			"log_only":       {ModeLogOnly, "idle", false},
			"error":          {ModeActive, "error", false},
			"docked":         {ModeActive, "docked", false},
			"returning":      {ModeActive, "returning", false},
			"room not known": {ModeActive, "idle", true},
		} {
			t.Run(name, func(t *testing.T) {
				e := newSchedEnv(t, tt.mode)
				e.start()
				e.moveTo("kitchen", 80)
				if tt.noPos {
					e.robot.SetRobotPixel(0, 0, false)
				}
				e.robot.SetStatus(tt.status, "none")
				e.tick()
				e.advance(30 * time.Minute)
				assert.Empty(t, e.robot.Commands())
			})
		}
	})
}

// Tonight's case: the robot's own path planner drove into a room the plan kept it out of. The scheduler reacts
// on the first poll that sees it there, and logs every room change with whether it is on the planned way.
func TestSchedulerOffRouteIntoNoEnterRoomReactsAtOnce(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	logs := observe(e)
	e.start()
	clearAreas(e.hub, "family") // the wing is one room: family, strip and den
	e.startRun("kitchen")
	e.step("pantry", 97)
	e.robot.ClearCommands()

	e.step("family", 96) // off the route, in a no-enter room
	assert.Equal(t, []string{cmdHome}, e.commandPaths(), "the way home from the family room is clear")
	run := e.sched().Run
	require.NotNil(t, run)
	assert.True(t, run.Aborted)
	assert.Contains(t, run.AbortReason, "robot is in family, which it must not enter")

	moves := logs.FilterMessage("Vacuum moved to another room").All()
	require.NotEmpty(t, moves)
	assert.Equal(t, "pantry", moves[len(moves)-1].ContextMap()["room"])
	assert.Equal(t, true, moves[len(moves)-1].ContextMap()["on_route"])
	off := logs.FilterMessage("Vacuum is off its planned route").All()
	require.Len(t, off, 1)
	assert.Equal(t, "family", off[0].ContextMap()["room"])
	assert.Equal(t, false, off[0].ContextMap()["on_route"])
}

// Deeper in the wing, the way home runs through more of it: the robot pauses where it is.
func TestSchedulerOffRouteDeepInNoEnterRegionPauses(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	e.start()
	clearAreas(e.hub, "family")
	e.startRun("kitchen")
	e.step("pantry", 97)
	e.robot.ClearCommands()
	e.step("strip", 96)
	assert.Equal(t, []string{cmdPause}, e.commandPaths())
	assert.Contains(t, e.sched().Run.AbortReason, "robot is in strip")
}

func TestSchedulerOffRouteRoomThatIsAllowedOnlyLogs(t *testing.T) {
	e := newSchedEnv(t, ModeActive)
	logs := observe(e)
	e.start()
	e.startRun("kitchen")
	e.robot.ClearCommands()
	e.step("office", 97)
	assert.Empty(t, e.robot.Commands())
	assert.Len(t, logs.FilterMessage("Vacuum is off its planned route").All(), 1)
}
