package housemap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// graphMap is a small made-up house:
//
//	y 8 +-------+-------+-------+
//	    | closet|  bed  |pantry |
//	y 4 +-------+-------+-------+
//	    |  mud  | hall  |kitchen|
//	y 0 +-------+-------+-------+
//	    x 0     4       8      12
//
// mud-hall, hall-kitchen, hall-bed, kitchen-pantry and bed-pantry are placed. bed-closet is not placed.
// mud has an exterior door to the yard, and the garage joins mud by an overhead door.
const graphMap = `house_map:
  spaces:
    mud:     {kind: room, area: entry,   polygon: [[0, 0], [4, 0], [4, 4], [0, 4]]}
    hall:    {kind: room, area: middle,  polygon: [[4, 0], [8, 0], [8, 4], [4, 4]]}
    kitchen: {kind: room, area: kitchen, polygon: [[8, 0], [12, 0], [12, 4], [8, 4]]}
    closet:  {kind: room, area: suite,   polygon: [[0, 4], [4, 4], [4, 8], [0, 8]]}
    bed:     {kind: room, area: suite,   polygon: [[4, 4], [8, 4], [8, 8], [4, 8]]}
    pantry:  {kind: room, area: kitchen, polygon: [[8, 4], [12, 4], [12, 8], [8, 8]]}
    garage:  {kind: garage, polygon: [[-6, 0], [0, 0], [0, 4], [-6, 4]]}
    yard:    {kind: yard, polygon: [[-10, -10], [20, -10], [20, 20], [-10, 20]]}
  doors:
    mud_hall:       {kind: door,    connects: [mud, hall],       span: [[4, 1], [4, 2]]}
    hall_kitchen:   {kind: opening, connects: [hall, kitchen],   span: [[8, 1], [8, 2]]}
    hall_bed:       {kind: door,    connects: [hall, bed],       span: [[5, 4], [6, 4]]}
    kitchen_pantry: {kind: opening, connects: [kitchen, pantry], span: [[9, 4], [10, 4]]}
    bed_pantry:     {kind: door,    connects: [bed, pantry],     span: [[8, 6], [8, 7]]}
    bed_closet:     {kind: door,    connects: [bed, closet]}
    front:          {kind: door, exterior: true, connects: [mud, yard], span: [[0, 1], [0, 2]]}
    garage_mud:     {kind: overhead, connects: [garage, mud], span: [[0, 3], [0, 3.5]]}
`

func loadGraph(t *testing.T) *Graph {
	t.Helper()
	m, err := Parse([]byte(graphMap))
	require.NoError(t, err)
	return m.Graph()
}

func TestGraphNodesAndNeighbors(t *testing.T) {
	g := loadGraph(t)

	assert.Equal(t, []string{"bed", "closet", "hall", "kitchen", "mud", "pantry"}, g.Rooms())
	assert.False(t, g.Has("garage"), "a garage is not a room")
	assert.False(t, g.Has("yard"))

	assert.Equal(t, []string{"hall"}, g.Neighbors("mud"), "exterior and overhead doors are not edges")
	assert.Equal(t, []string{"bed", "kitchen", "mud"}, g.Neighbors("hall"))
	assert.Equal(t, []string{"closet", "hall", "pantry"}, g.Neighbors("bed"), "an unplaced door still counts")
	assert.Empty(t, g.Neighbors("nowhere"))

	assert.Equal(t, "suite", g.Area("bed"))
	assert.Equal(t, "", g.Area("garage"))
	assert.InDelta(t, 16.0, g.FloorArea("kitchen"), 1e-9)
	assert.Equal(t, Point{10, 2}, g.Center("kitchen"))
}

func TestGraphShortestPath(t *testing.T) {
	g := loadGraph(t)

	tests := []struct {
		name     string
		from, to string
		avoid    []string
		want     []string
		ok       bool
	}{
		{name: "same room", from: "hall", to: "hall", want: []string{"hall"}, ok: true},
		{name: "neighbours", from: "mud", to: "hall", want: []string{"mud", "hall"}, ok: true},
		{name: "two doors", from: "mud", to: "kitchen", want: []string{"mud", "hall", "kitchen"}, ok: true},
		// Through bed is 10.6 m, through kitchen 11.0 m.
		{name: "shorter of two ways", from: "mud", to: "pantry", want: []string{"mud", "hall", "bed", "pantry"},
			ok: true},
		{name: "avoid picks the other way", from: "mud", to: "pantry", avoid: []string{"bed"},
			want: []string{"mud", "hall", "kitchen", "pantry"}, ok: true},
		{name: "both ways avoided", from: "mud", to: "pantry", avoid: []string{"bed", "kitchen"}},
		{name: "avoided target", from: "mud", to: "kitchen", avoid: []string{"kitchen"}},
		{name: "start room is exempt", from: "bed", to: "mud", avoid: []string{"bed"},
			want: []string{"bed", "hall", "mud"}, ok: true},
		{name: "unplaced door has no path", from: "hall", to: "closet"},
		{name: "unknown room", from: "mud", to: "attic"},
		{name: "not a room", from: "mud", to: "garage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			avoid := map[string]bool{}
			for _, r := range tt.avoid {
				avoid[r] = true
			}
			rooms, metres, ok := g.ShortestPath(tt.from, tt.to, avoid)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, rooms)
			if ok && len(rooms) > 1 {
				assert.Greater(t, metres, 0.0)
			}
		})
	}

	_, viaBed, _ := g.ShortestPath("mud", "pantry", nil)
	_, viaKitchen, _ := g.ShortestPath("mud", "pantry", map[string]bool{"bed": true})
	assert.InDelta(t, 10.58, viaBed, 0.01)
	assert.InDelta(t, 11.04, viaKitchen, 0.01)

	_, back, _ := g.ShortestPath("pantry", "mud", nil)
	assert.InDelta(t, viaBed, back, 1e-9, "paths are symmetric")
}

// TestValetudoToFrame uses the numbers of the deployment's Python tracing tool tests
// (scripts/test_house_map_retrace.py) and values printed by that tool's to_house for a turned frame.
func TestValetudoToFrame(t *testing.T) {
	plain := &Valetudo{RotationDeg: 0, PixelSizeM: 0.05, AnchorPx: []float64{0, 0}, AnchorM: []float64{0, 0}}
	assert.Equal(t, Point{0, 0}, plain.ToFrame(Point{0, 0}))
	// The dock-drift test moves the charger to 1000 cm, 1000 cm: pixel 200, 200 at 5 cm per pixel.
	got := plain.ToFrame(Point{200, 200})
	assert.InDelta(t, 10.0, got[0], 1e-9)
	assert.InDelta(t, -10.0, got[1], 1e-9)

	turned := &Valetudo{RotationDeg: 150, PixelSizeM: 0.05, AnchorPx: []float64{400, 300.5},
		AnchorM: []float64{10, 8}}
	for _, tt := range []struct {
		px   Point
		want Point
	}{
		{Point{400, 300.5}, Point{10.0, 8.0}},
		{Point{420, 300.5}, Point{9.13397459621556, 7.5}},
		{Point{400, 320.5}, Point{9.5, 8.86602540378444}},
		{Point{512.3, 250.0}, Point{6.399767357750379, 3.0057858554442936}},
	} {
		got := turned.ToFrame(tt.px)
		assert.InDelta(t, tt.want[0], got[0], 1e-9, "x of %v", tt.px)
		assert.InDelta(t, tt.want[1], got[1], 1e-9, "y of %v", tt.px)
	}

	noAnchor := &Valetudo{PixelSizeM: 0.05}
	assert.Equal(t, Point{1, -1}, noAnchor.ToFrame(Point{20, 20}), "a missing anchor is the origin")
}

func TestGraphOpenRegion(t *testing.T) {
	g := loadGraph(t)

	assert.Equal(t, []string{"hall", "kitchen", "pantry"}, g.OpenRegion("hall"),
		"hall opens to the kitchen, and the kitchen opens to the pantry")
	assert.Equal(t, []string{"hall", "kitchen", "pantry"}, g.OpenRegion("pantry"),
		"the region is the same from any of its rooms")
	assert.Equal(t, []string{"bed"}, g.OpenRegion("bed"), "doors do not join rooms, even to rooms of a region")
	assert.Equal(t, []string{"mud"}, g.OpenRegion("mud"), "exterior and overhead doors do not join rooms")
	assert.Nil(t, g.OpenRegion("garage"), "a garage is not in the graph")
	assert.Nil(t, g.OpenRegion("nowhere"))
}

// Only interior openings between two rooms join rooms. An exterior opening, an opening to a space that is not
// a room, and a door do not. An opening that is not placed yet does.
func TestGraphOpenRegionOnlyInteriorOpenings(t *testing.T) {
	m, err := Parse([]byte(`house_map:
  spaces:
    a:      {kind: room, area: a, polygon: [[0, 0], [4, 0], [4, 4], [0, 4]]}
    b:      {kind: room, area: b, polygon: [[4, 0], [8, 0], [8, 4], [4, 4]]}
    c:      {kind: room, area: c, polygon: [[8, 0], [12, 0], [12, 4], [8, 4]]}
    d:      {kind: room, area: d, polygon: [[12, 0], [16, 0], [16, 4], [12, 4]]}
    garage: {kind: garage, polygon: [[-6, 0], [0, 0], [0, 4], [-6, 4]]}
    patio:  {kind: patio, polygon: [[0, -4], [16, -4], [16, 0], [0, 0]]}
    yard:   {kind: yard, polygon: [[-10, -10], [20, -10], [20, 20], [-10, 20]]}
  doors:
    a_b:      {kind: opening, connects: [a, b]}
    b_c:      {kind: door,    connects: [b, c], span: [[8, 1], [8, 2]]}
    c_patio:  {kind: opening, exterior: true, connects: [c, patio], span: [[9, 0], [11, 0]]}
    d_patio:  {kind: opening, exterior: true, connects: [d, patio], span: [[13, 0], [15, 0]]}
    a_garage: {kind: opening, connects: [garage, a], span: [[0, 1], [0, 3]]}
`))
	require.NoError(t, err)
	g := m.Graph()

	assert.Equal(t, []string{"a", "b"}, g.OpenRegion("a"), "an opening that is not placed counts")
	assert.Equal(t, []string{"a", "b"}, g.OpenRegion("b"))
	assert.Equal(t, []string{"c"}, g.OpenRegion("c"), "a door and an opening to the outside do not join rooms")
	assert.Equal(t, []string{"d"}, g.OpenRegion("d"), "two rooms open to the same patio are not one room")
	assert.Nil(t, g.OpenRegion("garage"))
}
