package housemap

import (
	"math"
	"sort"
)

// Distance is the straight-line distance between two points, in metres.
func Distance(a, b Point) float64 {
	return math.Hypot(b[0]-a[0], b[1]-a[1])
}

// InPolygon reports whether p is inside the polygon, by ray casting. Points exactly on an edge may fall
// either way.
func InPolygon(p Point, poly []Point) bool {
	in := false
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		a, b := poly[i], poly[j]
		if (a[1] > p[1]) != (b[1] > p[1]) {
			x := a[0] + (p[1]-a[1])*(b[0]-a[0])/(b[1]-a[1])
			if p[0] < x {
				in = !in
			}
		}
	}
	return in
}

// PolygonArea is the area of a simple polygon, in square metres.
func PolygonArea(poly []Point) float64 {
	sum := 0.0
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		sum += poly[j][0]*poly[i][1] - poly[i][0]*poly[j][1]
	}
	return math.Abs(sum) / 2
}

// SegmentDistance is the distance from p to the segment from a to b.
func SegmentDistance(p, a, b Point) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return Distance(p, a)
	}
	t := ((p[0]-a[0])*dx + (p[1]-a[1])*dy) / l2
	t = math.Max(0, math.Min(1, t))
	return Distance(p, Point{a[0] + t*dx, a[1] + t*dy})
}

// polygonDistance is the distance from p to the nearest edge of the polygon.
func polygonDistance(p Point, poly []Point) float64 {
	best := math.Inf(1)
	for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
		best = math.Min(best, SegmentDistance(p, poly[j], poly[i]))
	}
	return best
}

// SpaceAt names the space that holds p. Spaces may overlap, for example a pool drawn inside a yard, so the
// smallest space that holds p wins. It returns "" when no space holds p.
func (m *Map) SpaceAt(p Point) string {
	best, bestArea := "", math.Inf(1)
	for _, name := range sortedKeys(m.Spaces) {
		s := m.Spaces[name]
		if !InPolygon(p, s.Polygon) {
			continue
		}
		if a := PolygonArea(s.Polygon); a < bestArea {
			best, bestArea = name, a
		}
	}
	return best
}

// Midpoint is the middle of the doorway. The door must be placed.
func (d *Door) Midpoint() Point {
	return Point{(d.Span[0][0] + d.Span[1][0]) / 2, (d.Span[0][1] + d.Span[1][1]) / 2}
}

// Bearing is the direction from a to b, in degrees clockwise from north, in [0, 360).
func Bearing(a, b Point) float64 {
	deg := math.Atan2(b[0]-a[0], b[1]-a[1]) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	return deg
}

// Covers reports whether p is within the camera's range and inside its field of view. Walls and other
// things in the way are not modelled.
func (c *Camera) Covers(p Point) bool {
	d := Distance(c.At, p)
	if d > c.RangeM {
		return false
	}
	if d == 0 || c.FovDeg >= 360 {
		return true
	}
	off := math.Abs(math.Mod(Bearing(c.At, p)-c.HeadingDeg+540, 360) - 180)
	return off <= c.FovDeg/2
}

// Outdoor reports whether the camera looks at the outside. A camera with a Frigate id is indoor. Any other
// camera is outdoor unless it stands inside a room. A camera on an outside wall counts as outdoor. Indoor
// cameras do not see through walls, so coverage of the outside ignores them.
func (m *Map) Outdoor(c *Camera) bool {
	if c.Frigate != "" {
		return false
	}
	for _, s := range m.Spaces {
		if s.Kind == KindRoom && InPolygon(c.At, s.Polygon) {
			return false
		}
	}
	return true
}

// ringTolerance is how much closer than the ring distance a sampled point may be to the house.
const ringTolerance = 0.01

// Ring samples points that are distance metres outside the house, about step metres apart. The house is
// every room and garage. A point is on the ring when it is outside every part of the house and its nearest
// part is distance away. Points are sorted, so the result is stable.
func (m *Map) Ring(distance, step float64) []Point {
	if distance <= 0 || step <= 0 {
		return nil
	}
	var polys [][]Point
	for _, name := range sortedKeys(m.Spaces) {
		if s := m.Spaces[name]; s.Building() {
			polys = append(polys, s.Polygon)
		}
	}

	var candidates []Point
	for _, poly := range polys {
		for i, j := 0, len(poly)-1; i < len(poly); j, i = i, i+1 {
			a, b := poly[j], poly[i]
			l := Distance(a, b)
			if l == 0 {
				continue
			}
			// Both normals: which one points out of the house does not matter, the filter below decides.
			nx, ny := -(b[1]-a[1])/l*distance, (b[0]-a[0])/l*distance
			n := int(math.Ceil(l / step))
			for k := 0; k <= n; k++ {
				t := float64(k) / float64(n)
				x, y := a[0]+t*(b[0]-a[0]), a[1]+t*(b[1]-a[1])
				candidates = append(candidates, Point{x + nx, y + ny}, Point{x - nx, y - ny})
			}
			// Round the corner at b.
			arcSteps := int(math.Ceil(2 * math.Pi * distance / step))
			for k := 0; k < arcSteps; k++ {
				ang := 2 * math.Pi * float64(k) / float64(arcSteps)
				candidates = append(candidates, Point{b[0] + distance*math.Sin(ang), b[1] + distance*math.Cos(ang)})
			}
		}
	}

	var ring []Point
	for _, p := range candidates {
		ok := true
		for _, poly := range polys {
			if InPolygon(p, poly) || polygonDistance(p, poly) < distance-ringTolerance {
				ok = false
				break
			}
		}
		if ok {
			ring = append(ring, p)
		}
	}
	return thin(ring, step/2)
}

// thin drops points closer than gap to a point already kept, and sorts the rest.
func thin(points []Point, gap float64) []Point {
	sort.Slice(points, func(i, j int) bool {
		if points[i][0] != points[j][0] {
			return points[i][0] < points[j][0]
		}
		return points[i][1] < points[j][1]
	})
	var kept []Point
	for _, p := range points {
		near := false
		for k := len(kept) - 1; k >= 0 && p[0]-kept[k][0] < gap; k-- {
			if Distance(p, kept[k]) < gap {
				near = true
				break
			}
		}
		if !near {
			kept = append(kept, p)
		}
	}
	return kept
}

// CoverageGaps samples the ring distance metres outside the house, about step metres apart, and returns the
// points no outdoor camera covers.
func (m *Map) CoverageGaps(distance, step float64) []Point {
	var outdoor []*Camera
	for _, name := range sortedKeys(m.Cameras) {
		if c := m.Cameras[name]; m.Outdoor(c) {
			outdoor = append(outdoor, c)
		}
	}
	var gaps []Point
	for _, p := range m.Ring(distance, step) {
		covered := false
		for _, c := range outdoor {
			if c.Covers(p) {
				covered = true
				break
			}
		}
		if !covered {
			gaps = append(gaps, p)
		}
	}
	return gaps
}

// nearestBuilding names the room or garage nearest to p.
func (m *Map) nearestBuilding(p Point) string {
	best, bestD := "", math.Inf(1)
	for _, name := range sortedKeys(m.Spaces) {
		s := m.Spaces[name]
		if !s.Building() {
			continue
		}
		if d := polygonDistance(p, s.Polygon); d < bestD {
			best, bestD = name, d
		}
	}
	return best
}
