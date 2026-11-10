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
 {"__class":"PresetSelectionStateAttribute","type":"operation_mode","value":"vacuum_and_mop"}
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
