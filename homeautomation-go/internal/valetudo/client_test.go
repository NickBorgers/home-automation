package valetudo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"homeautomation/internal/valetudo"
	"homeautomation/internal/valetudo/valetudotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realAttributes is the shape of a real robot's /api/v2/robot/state/attributes reply, trimmed.
const realAttributes = `[
 {"__class":"ConsumableStateAttribute","type":"brush","subType":"main","remaining":{"value":17000,"unit":"minutes"}},
 {"__class":"BatteryStateAttribute","level":87,"flag":"charging"},
 {"__class":"StatusStateAttribute","value":"docked","flag":"none"},
 {"__class":"PresetSelectionStateAttribute","type":"fan_speed","value":"medium"},
 {"__class":"PresetSelectionStateAttribute","type":"water_grade","value":"low"},
 {"__class":"PresetSelectionStateAttribute","type":"operation_mode","value":"vacuum_and_mop"},
 {"__class":"DockStatusStateAttribute","value":"drying"}
]`

// realMap is the shape of a real robot's /api/v2/robot/state/map reply, trimmed. Entity points are in
// centimetres.
const realMap = `{"__class":"ValetudoMap","pixelSize":5,"size":{"x":5120,"y":5120},
 "layers":[
  {"__class":"MapLayer","type":"floor","pixels":[],"compressedPixels":[1,2,3],"metaData":{}},
  {"__class":"MapLayer","type":"segment","compressedPixels":[1,2,3],"metaData":{"segmentId":"20","name":"Kitchen","active":false}},
  {"__class":"MapLayer","type":"segment","compressedPixels":[1,2,3],"metaData":{"segmentId":"21","name":"Pantry"}}
 ],
 "entities":[
  {"__class":"PointMapEntity","type":"charger_location","points":[3500,3800],"metaData":{}},
  {"__class":"PathMapEntity","type":"path","points":[1,2,3,4],"metaData":{}},
  {"__class":"PointMapEntity","type":"robot_position","points":[3600,3900],"metaData":{"angle":90}}
 ]}`

func serveFixed(t *testing.T, path, body string) *valetudo.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return valetudo.NewClient(srv.URL+"/", nil)
}

func TestStateParsesRealReply(t *testing.T) {
	c := serveFixed(t, "/api/v2/robot/state/attributes", realAttributes)
	s, err := c.State(context.Background())
	require.NoError(t, err)
	assert.True(t, s.HasBattery)
	assert.Equal(t, 87, s.Battery)
	assert.Equal(t, "docked", s.Status)
	assert.True(t, s.Docked())
	assert.Equal(t, "drying", s.DockStatus)
	assert.Equal(t, map[string]string{"fan_speed": "medium", "water_grade": "low",
		"operation_mode": "vacuum_and_mop"}, s.Presets)
}

func TestStateWithoutStatusIsAnError(t *testing.T) {
	c := serveFixed(t, "/api/v2/robot/state/attributes", `[{"__class":"BatteryStateAttribute","level":50}]`)
	_, err := c.State(context.Background())
	assert.ErrorContains(t, err, "StatusStateAttribute")
}

func TestMapParsesRealReply(t *testing.T) {
	c := serveFixed(t, "/api/v2/robot/state/map", realMap)
	m, err := c.Map(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5.0, m.PixelSize)
	px, ok := m.RobotPixel()
	require.True(t, ok)
	assert.Equal(t, [2]float64{720, 780}, px, "centimetres divided by the pixel size")
	dock, ok := m.ChargerPixel()
	require.True(t, ok)
	assert.Equal(t, [2]float64{700, 760}, dock)
	assert.Equal(t, []valetudo.Segment{{ID: "20", Name: "Kitchen"}, {ID: "21", Name: "Pantry"}}, m.Segments)
}

func TestMapWithoutRobot(t *testing.T) {
	c := serveFixed(t, "/api/v2/robot/state/map", `{"pixelSize":5,"layers":[],"entities":[]}`)
	m, err := c.Map(context.Background())
	require.NoError(t, err)
	_, ok := m.RobotPixel()
	assert.False(t, ok)
	_, ok = (&valetudo.Map{Robot: &[2]float64{1, 1}}).RobotPixel()
	assert.False(t, ok, "no pixel size, no pixel")
}

func TestCommandsSendValetudoRequestShapes(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	c := valetudo.NewClient(robot.URL, nil)
	ctx := context.Background()

	require.NoError(t, c.SetPreset(ctx, valetudo.CapabilityOperationMode, "vacuum_and_mop"))
	require.NoError(t, c.SetPreset(ctx, valetudo.CapabilityFanSpeed, "medium"))
	require.NoError(t, c.SetPreset(ctx, valetudo.CapabilityWaterUsage, "medium"))
	require.NoError(t, c.CleanSegments(ctx, []string{"20", "21"}))
	require.NoError(t, c.Basic(ctx, valetudo.ActionPause))
	require.NoError(t, c.Basic(ctx, valetudo.ActionHome))

	cmds := robot.Commands()
	require.Len(t, cmds, 6)
	assert.Equal(t, "/api/v2/robot/capabilities/OperationModeControlCapability/preset", cmds[0].Path)
	assert.Equal(t, map[string]any{"name": "vacuum_and_mop"}, cmds[0].Body)
	assert.Equal(t, "/api/v2/robot/capabilities/FanSpeedControlCapability/preset", cmds[1].Path)
	assert.Equal(t, "/api/v2/robot/capabilities/WaterUsageControlCapability/preset", cmds[2].Path)
	assert.Equal(t, "/api/v2/robot/capabilities/MapSegmentationCapability", cmds[3].Path)
	assert.Equal(t, map[string]any{"action": "start_segment_action", "segment_ids": []any{"20", "21"},
		"iterations": float64(1), "customOrder": true}, cmds[3].Body)
	assert.Equal(t, "/api/v2/robot/capabilities/BasicControlCapability", cmds[4].Path)
	assert.Equal(t, map[string]any{"action": "pause"}, cmds[4].Body)
	assert.Equal(t, map[string]any{"action": "home"}, cmds[5].Body)

	s, err := c.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, "returning", s.Status)
	assert.Equal(t, "vacuum_and_mop", s.Presets["operation_mode"])
}

func TestBadArgumentsSendNothing(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	c := valetudo.NewClient(robot.URL, nil)
	ctx := context.Background()

	assert.Error(t, c.CleanSegments(ctx, nil))
	assert.Error(t, c.SetPreset(ctx, "SpeakerVolumeControlCapability", "x"))
	assert.Error(t, c.SetPreset(ctx, valetudo.CapabilityFanSpeed, ""))
	assert.Error(t, c.Basic(ctx, "dance"))
	assert.Empty(t, robot.Commands())
}

func TestSegments(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	robot.AddSegment("20", "Kitchen")
	segs, err := valetudo.NewClient(robot.URL, nil).Segments(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []valetudo.Segment{{ID: "20", Name: "Kitchen"}}, segs)
}

func TestPresets(t *testing.T) {
	c := serveFixed(t, "/api/v2/robot/capabilities/FanSpeedControlCapability/presets",
		`["low","medium","high","max"]`)
	names, err := c.Presets(context.Background(), valetudo.CapabilityFanSpeed)
	require.NoError(t, err)
	assert.Equal(t, []string{"low", "medium", "high", "max"}, names)

	_, err = c.Presets(context.Background(), valetudo.CapabilityWaterUsage)
	assert.ErrorContains(t, err, "HTTP 404")
	_, err = c.Presets(context.Background(), "SpeakerVolumeControlCapability")
	assert.ErrorContains(t, err, "unknown preset capability")

	robot := valetudotest.New()
	defer robot.Close()
	robot.SetPresetList(valetudo.CapabilityFanSpeed, []string{"low", "max"})
	names, err = valetudo.NewClient(robot.URL, nil).Presets(context.Background(), valetudo.CapabilityFanSpeed)
	require.NoError(t, err)
	assert.Equal(t, []string{"low", "max"}, names)
}

func TestFakeFailsOnePresetName(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	c := valetudo.NewClient(robot.URL, nil)
	ctx := context.Background()
	robot.FailPreset(valetudo.CapabilityFanSpeed, "max", http.StatusBadRequest)
	assert.ErrorContains(t, c.SetPreset(ctx, valetudo.CapabilityFanSpeed, "max"), "HTTP 400")
	require.NoError(t, c.SetPreset(ctx, valetudo.CapabilityFanSpeed, "low"))
	robot.FailPreset(valetudo.CapabilityFanSpeed, "max", 0)
	require.NoError(t, c.SetPreset(ctx, valetudo.CapabilityFanSpeed, "max"))
	require.Len(t, robot.Commands(), 2, "a failed command is not recorded")
}

func TestHTTPErrorsAreReported(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	c := valetudo.NewClient(robot.URL, nil)
	robot.Fail("/api/v2/robot/state/attributes", http.StatusServiceUnavailable)
	_, err := c.State(context.Background())
	assert.ErrorContains(t, err, "HTTP 503")

	robot.Fail("/api/v2/robot/capabilities/BasicControlCapability", http.StatusBadRequest)
	assert.ErrorContains(t, c.Basic(context.Background(), valetudo.ActionHome), "HTTP 400")

	bad := serveFixed(t, "/api/v2/robot/state/map", `not json`)
	_, err = bad.Map(context.Background())
	assert.ErrorContains(t, err, "decode")

	_, err = valetudo.NewClient("http://127.0.0.1:1", nil).State(context.Background())
	assert.Error(t, err, "connection refused")
}

// realRestrictions is the live robot's reply to GET CombinedVirtualRestrictionsCapability: no walls and one
// zone the owner drew.
const realRestrictions = `{"__class":"ValetudoVirtualRestrictions","metaData":{},"virtualWalls":[],
 "restrictedZones":[{"__class":"ValetudoRestrictedZone","metaData":{},"points":{"pA":{"x":3005,"y":4150},
 "pB":{"x":3085,"y":4150},"pC":{"x":3085,"y":4205},"pD":{"x":3005,"y":4205}},"type":"regular"}]}`

func TestRestrictionsParsesRealReply(t *testing.T) {
	c := serveFixed(t, "/api/v2/robot/capabilities/CombinedVirtualRestrictionsCapability", realRestrictions)
	r, err := c.Restrictions(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, r.VirtualWalls)
	assert.Empty(t, r.VirtualWalls)
	require.Len(t, r.RestrictedZones, 1)
	z := r.RestrictedZones[0]
	assert.Equal(t, valetudo.ZoneRegular, z.Type)
	assert.Equal(t, valetudo.RestrictionPoint{X: 3005, Y: 4150}, z.Points.PA)
	assert.Equal(t, valetudo.RestrictionPoint{X: 3085, Y: 4150}, z.Points.PB)
	assert.Equal(t, valetudo.RestrictionPoint{X: 3085, Y: 4205}, z.Points.PC)
	assert.Equal(t, valetudo.RestrictionPoint{X: 3005, Y: 4205}, z.Points.PD)
}

func TestSetRestrictionsSendsValetudoShape(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	c := valetudo.NewClient(robot.URL, nil)
	ctx := context.Background()

	// Empty lists go out as arrays, not null: Valetudo answers 400 otherwise.
	require.NoError(t, c.SetRestrictions(ctx, valetudo.Restrictions{}))
	cmds := robot.Commands()
	require.Len(t, cmds, 1)
	assert.Equal(t, "/api/v2/robot/capabilities/CombinedVirtualRestrictionsCapability", cmds[0].Path)
	assert.Equal(t, map[string]any{"virtualWalls": []any{}, "restrictedZones": []any{}}, cmds[0].Body)

	var z valetudo.RestrictedZone
	z.Type = valetudo.ZoneMop
	z.Points.PA, z.Points.PB = valetudo.RestrictionPoint{X: 1, Y: 2}, valetudo.RestrictionPoint{X: 3, Y: 2}
	z.Points.PC, z.Points.PD = valetudo.RestrictionPoint{X: 3, Y: 4}, valetudo.RestrictionPoint{X: 1, Y: 4}
	want := valetudo.Restrictions{
		VirtualWalls:    []valetudo.VirtualWall{valetudo.NewVirtualWall(10, 20, 30, 40)},
		RestrictedZones: []valetudo.RestrictedZone{z},
	}
	require.NoError(t, c.SetRestrictions(ctx, want))
	assert.JSONEq(t, `{"virtualWalls":[{"points":{"pA":{"x":10,"y":20},"pB":{"x":30,"y":40}}}],
		"restrictedZones":[{"type":"mop","points":{"pA":{"x":1,"y":2},"pB":{"x":3,"y":2},"pC":{"x":3,"y":4},
		"pD":{"x":1,"y":4}}}]}`, robot.Restrictions())

	got, err := c.Restrictions(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, *got, "a read gives back what was written")
}

func TestSetRestrictionsRejectsUnknownZoneType(t *testing.T) {
	robot := valetudotest.New()
	defer robot.Close()
	c := valetudo.NewClient(robot.URL, nil)
	var z valetudo.RestrictedZone
	z.Type = "lava"
	err := c.SetRestrictions(context.Background(), valetudo.Restrictions{RestrictedZones: []valetudo.RestrictedZone{z}})
	assert.Error(t, err)
	assert.Empty(t, robot.Commands())
}
