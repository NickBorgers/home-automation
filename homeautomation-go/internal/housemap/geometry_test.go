package housemap

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDistanceAndBearing(t *testing.T) {
	assert.InDelta(t, 5, Distance(Point{0, 0}, Point{3, 4}), 1e-9)
	tests := []struct {
		to   Point
		want float64
	}{
		{Point{0, 1}, 0}, {Point{1, 1}, 45}, {Point{1, 0}, 90}, {Point{0, -1}, 180}, {Point{-1, 0}, 270},
	}
	for _, tt := range tests {
		assert.InDelta(t, tt.want, Bearing(Point{0, 0}, tt.to), 1e-9, "%v", tt.to)
	}
}

func TestInPolygon(t *testing.T) {
	// An L shape: the notch at the top right is outside.
	l := []Point{{0, 0}, {4, 0}, {4, 2}, {2, 2}, {2, 4}, {0, 4}}
	assert.True(t, InPolygon(Point{1, 1}, l))
	assert.True(t, InPolygon(Point{1, 3}, l))
	assert.True(t, InPolygon(Point{3, 1}, l))
	assert.False(t, InPolygon(Point{3, 3}, l), "the notch")
	assert.False(t, InPolygon(Point{-1, 1}, l))
	assert.False(t, InPolygon(Point{1, 5}, l))
	assert.InDelta(t, 12, PolygonArea(l), 1e-9)
}

func TestSegmentDistance(t *testing.T) {
	a, b := Point{0, 0}, Point{4, 0}
	assert.InDelta(t, 2, SegmentDistance(Point{2, 2}, a, b), 1e-9)
	assert.InDelta(t, 5, SegmentDistance(Point{7, 4}, a, b), 1e-9, "past the end")
	assert.InDelta(t, 1, SegmentDistance(Point{1, 0}, a, a), 1e-9, "a zero-length segment")
}

func TestSpaceAt(t *testing.T) {
	m := exampleMap(t)
	assert.Equal(t, "kitchen", m.SpaceAt(Point{8, 2}))
	assert.Equal(t, "porch", m.SpaceAt(Point{6.5, 9}))
	assert.Equal(t, "yard", m.SpaceAt(Point{18, 15}), "the smallest space wins")
	assert.Equal(t, "", m.SpaceAt(Point{100, 100}))
}

func TestDoorMidpoint(t *testing.T) {
	m := exampleMap(t)
	assert.Equal(t, Point{6.5, 10}, m.Doors["front"].Midpoint())
	assert.Equal(t, "porch", m.SpaceAt(Point{6.5, 9.9}))
}

func TestCameraCovers(t *testing.T) {
	c := &Camera{At: Point{0, 0}, HeadingDeg: 350, FovDeg: 90, RangeM: 10}
	tests := []struct {
		name string
		p    Point
		want bool
	}{
		{"ahead", Point{0, 5}, true},
		{"edge of view, past north", Point{math.Sin(30 * math.Pi / 180), math.Cos(30 * math.Pi / 180)}, true},
		{"just outside the view", Point{math.Sin(40 * math.Pi / 180), math.Cos(40 * math.Pi / 180)}, false},
		{"left edge", Point{math.Sin(-50 * math.Pi / 180), math.Cos(-50 * math.Pi / 180)}, true},
		{"behind", Point{0, -1}, false},
		{"out of range", Point{0, 11}, false},
		{"at the camera", Point{0, 0}, true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, c.Covers(tt.p), tt.name)
	}
	all := &Camera{At: Point{0, 0}, HeadingDeg: 0, FovDeg: 360, RangeM: 2}
	assert.True(t, all.Covers(Point{0, -1}))
	assert.False(t, all.Covers(Point{0, -3}))
}

func TestOutdoor(t *testing.T) {
	m := exampleMap(t)
	assert.True(t, m.Outdoor(m.Cameras["porch_cam"]))
	assert.False(t, m.Outdoor(m.Cameras["cam_hall"]))

	// A Frigate camera is indoor even when it is drawn just outside a room.
	m.Cameras["cam_hall"].At = Point{-1, -1}
	assert.False(t, m.Outdoor(m.Cameras["cam_hall"]))
	m.Cameras["cam_hall"].Frigate = ""
	assert.True(t, m.Outdoor(m.Cameras["cam_hall"]))
	m.Cameras["cam_hall"].At = Point{5, 6}
	assert.False(t, m.Outdoor(m.Cameras["cam_hall"]), "inside the hall")
}

func TestRing_PointsAreAtTheDistance(t *testing.T) {
	m := exampleMap(t)
	ring := m.Ring(5, 1)
	assert.NotEmpty(t, ring)
	for _, p := range ring {
		nearest := math.Inf(1)
		for _, s := range m.Spaces {
			if s.Building() {
				assert.False(t, InPolygon(p, s.Polygon), "%v is inside %v", p, s)
				nearest = math.Min(nearest, polygonDistance(p, s.Polygon))
			}
		}
		assert.InDelta(t, 5, nearest, 0.02, "%v", p)
	}
	// The house is 14 m by 10 m. Its 5 m ring is a rounded rectangle about 80 m long.
	assert.InDelta(t, 80, float64(len(ring)), 25)
	assert.Nil(t, m.Ring(0, 1))
	assert.Nil(t, m.Ring(5, 0))
}

func TestCoverageGaps_Example(t *testing.T) {
	m := exampleMap(t)
	gaps := m.CoverageGaps(5, 1)
	ring := m.Ring(5, 1)
	assert.NotEmpty(t, gaps, "one camera cannot see all round the house")
	assert.Less(t, len(gaps), len(ring), "the porch camera sees the front")

	isGap := func(p Point) bool {
		for _, g := range gaps {
			if Distance(g, p) < 1 {
				return true
			}
		}
		return false
	}
	assert.False(t, isGap(Point{6.5, 15}), "north of the porch, in the porch camera's view")
	assert.True(t, isGap(Point{3, -5}), "south of the lounge, behind the house")

	// Indoor cameras do not count, even when the ring is in their range.
	m.Cameras = map[string]*Camera{"cam_lounge": m.Cameras["cam_lounge"]}
	assert.Len(t, m.CoverageGaps(5, 1), len(ring))
}
