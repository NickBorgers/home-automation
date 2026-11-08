package housemap

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const examplePath = "testdata/example_house_map.yaml"

func exampleMap(t *testing.T) *Map {
	t.Helper()
	m, err := Load(examplePath)
	require.NoError(t, err)
	require.NotNil(t, m)
	return m
}

func TestLoad_Example(t *testing.T) {
	m := exampleMap(t)
	assert.Equal(t, "m", m.Frame.Units)
	assert.Len(t, m.Spaces, 7)
	assert.Equal(t, []string{"porch", "yard"}, m.Doors["front"].Connects)
	assert.True(t, m.Doors["front"].Exterior)
	assert.Equal(t, "hall_to_kitchen", m.Doors["kitchen_hall"].Zones["cam_hall"])
	assert.Equal(t, Point{8.2, 10.2}, m.Cameras["porch_cam"].At)
	assert.Equal(t, 8.2, m.Cameras["porch_cam"].At.X())
	assert.Equal(t, 10.2, m.Cameras["porch_cam"].At.Y())
	assert.True(t, m.Spaces["porch"].Building())
	assert.False(t, m.Spaces["yard"].Building())
}

func TestLoad_MissingFileIsNil(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), FileName))
	assert.NoError(t, err)
	assert.Nil(t, m)
}

func TestLoad_ReadError(t *testing.T) {
	_, err := Load(t.TempDir()) // a directory cannot be read as a file
	assert.Error(t, err)
}

func TestParse_Errors(t *testing.T) {
	_, err := Parse([]byte("house_map: [not, a, map"))
	assert.ErrorContains(t, err, "parse house map")
	_, err = Parse([]byte("other: {}"))
	assert.ErrorContains(t, err, "no house_map section")
	_, err = Parse([]byte("house_map: {}"))
	assert.ErrorContains(t, err, "no spaces")
}

const square = "[[0, 0], [4, 0], [4, 4], [0, 4]]"

func TestValidate_Rules(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"bad frame units", "frame: {units: ft}", `units "ft" must be m`},
		{"bad frame x", "frame: {x: west}", `x "west" must be east`},
		{"bad frame y", "frame: {y: south}", `y "south" must be north`},
		{"valetudo pixel size", "valetudo: {rotation_deg: 90}", "pixel_size_m 0 must be positive"},
		{"valetudo anchor", "valetudo: {pixel_size_m: 0.05, anchor_px: [1, 2, 3]}", "anchor_px needs 2 numbers, has 3"},
		{"valetudo dock", "valetudo: {pixel_size_m: 0.05, dock_m: [1]}", "dock_m needs 2 numbers, has 1"},
		{"empty space", "spaces: {a: }", `space "a": empty`},
		{"unknown space kind", "spaces: {a: {kind: attic, polygon: " + square + "}}", `kind "attic" must be one of`},
		{"short polygon", "spaces: {a: {kind: yard, polygon: [[0, 0], [1, 1]]}}", "polygon needs at least 3 points"},
		{"room without area", "spaces: {a: {kind: room, polygon: " + square + "}}", "a room needs an area"},
		{"area outside a room", "spaces: {a: {kind: yard, area: x, polygon: " + square + "}}", "only a room has an area"},
		{"empty door", "doors: {d: }", `door "d": empty`},
		{"unknown door kind", "doors: {d: {kind: hatch, connects: [r1, r2], span: [[0, 0], [1, 0]]}}", `kind "hatch"`},
		{"one-point span", "doors: {d: {kind: door, connects: [r1, r2], span: [[0, 0]]}}", "span needs 2 points"},
		{"three-point span", "doors: {d: {kind: door, connects: [r1, r2], span: [[0, 0], [1, 0], [2, 0]]}}",
			"span needs 2 points"},
		{"one space", "doors: {d: {kind: door, connects: [r1], span: [[0, 0], [1, 0]]}}", "connects needs 2 spaces"},
		{"same space", "doors: {d: {kind: door, connects: [r1, r1], span: [[0, 0], [1, 0]]}}", `connects "r1" to itself`},
		{"unknown space", "doors: {d: {kind: door, connects: [r1, nope], span: [[0, 0], [1, 0]]}}", `unknown space "nope"`},
		{"exterior between rooms",
			"doors: {d: {kind: door, exterior: true, connects: [r1, r2], span: [[0, 0], [1, 0]]}}",
			"an exterior door joins a room or garage"},
		{"exterior between outdoor spaces",
			"doors: {d: {kind: opening, exterior: true, connects: [y1, y2], span: [[0, 0], [1, 0]]}}",
			"an exterior door joins a room or garage"},
		{"empty line", "lines: {l: }", `line "l": empty`},
		{"unknown line kind", "lines: {l: {kind: wall, line: [[0, 0], [1, 0]]}}", `kind "wall"`},
		{"short line", "lines: {l: {kind: fence, line: [[0, 0]]}}", "needs at least 2 points"},
		{"empty camera", "cameras: {c: }", `camera "c": empty`},
		{"zero fov", "cameras: {c: {fov_deg: 0, heading_deg: 0, range_m: 5}}", "fov_deg 0 must be"},
		{"wide fov", "cameras: {c: {fov_deg: 361, heading_deg: 0, range_m: 5}}", "fov_deg 361 must be"},
		{"heading 360", "cameras: {c: {fov_deg: 90, heading_deg: 360, range_m: 5}}", "heading_deg 360 must be"},
		{"negative heading", "cameras: {c: {fov_deg: 90, heading_deg: -1, range_m: 5}}", "heading_deg -1 must be"},
		{"zero range", "cameras: {c: {fov_deg: 90, heading_deg: 0}}", "range_m 0 must be positive"},
	}
	base := "spaces: {r1: {kind: room, area: a1, polygon: " + square + "}, r2: {kind: room, area: a2, polygon: " +
		square + "}, y1: {kind: yard, polygon: " + square + "}, y2: {kind: yard, polygon: " + square + "}}"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := "house_map:\n  " + base + "\n  " + tt.yaml + "\n"
			if strings.HasPrefix(tt.yaml, "spaces:") {
				doc = "house_map:\n  " + tt.yaml + "\n"
			}
			_, err := Parse([]byte(doc))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestValidate_CollectsAllErrorsSorted(t *testing.T) {
	_, err := Parse([]byte(`house_map:
  spaces:
    b: {kind: room, polygon: [[0, 0], [1, 0]]}
    a: {kind: shed, polygon: [[0, 0], [1, 0], [1, 1]]}
  cameras:
    c: {fov_deg: 0, heading_deg: 0, range_m: 0}
`))
	var verr *ValidationError
	require.True(t, errors.As(err, &verr))
	assert.Len(t, verr.Problems, 5)
	assert.IsIncreasing(t, verr.Problems)
	assert.True(t, strings.HasPrefix(err.Error(), "invalid house map: "))
}

func TestParse_KeepsValetudo(t *testing.T) {
	m, err := Parse([]byte(`house_map:
  valetudo: {rotation_deg: 90, pixel_size_m: 0.05, anchor_px: [400, 300.5], anchor_m: [6, 5], dock_m: [9, 1]}
  spaces: {y: {kind: yard, polygon: ` + square + `}}
`))
	require.NoError(t, err)
	assert.Equal(t, &Valetudo{RotationDeg: 90, PixelSizeM: 0.05, AnchorPx: []float64{400, 300.5},
		AnchorM: []float64{6, 5}, DockM: []float64{9, 1}}, m.Valetudo)
}

func TestValidate_ExteriorDoorsAllowed(t *testing.T) {
	_, err := Parse([]byte(`house_map:
  spaces:
    utility: {kind: room, area: utility, polygon: ` + square + `}
    garage: {kind: garage, polygon: ` + square + `}
    drive: {kind: driveway, polygon: ` + square + `}
  doors:
    into_garage: {kind: door, exterior: true, connects: [utility, garage], span: [[0, 0], [1, 0]]}
    overhead: {kind: overhead, exterior: true, connects: [drive, garage], span: [[0, 0], [1, 0]]}
`))
	assert.NoError(t, err)
}
