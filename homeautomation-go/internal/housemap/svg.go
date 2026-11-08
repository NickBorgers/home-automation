package housemap

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// SVG drawing settings. Colors are fixed and read on a light background in any theme.
const (
	svgScale    = 20.0 // pixels per metre
	svgMargin   = 2.0  // metres around the drawing
	scaleBarM   = 5.0
	labelSizePx = 10
)

// areaColors tint rooms. Each area gets the next color, in sorted area order.
var areaColors = []string{
	"#f4c7a1", "#a8d5ba", "#a9c8ec", "#f2e394", "#d7b4e8", "#f5a9a9",
	"#b8e0d2", "#e6c9a8", "#c3d69b", "#f0b8d0", "#9fd3e0", "#d9d2c5",
}

// kindColors fill spaces that are not rooms.
var kindColors = map[string]string{
	KindGarage:      "#c8c8c8",
	KindYard:        "#dcefd0",
	KindPatio:       "#e3ddd3",
	KindDeck:        "#d9bf9a",
	KindPool:        "#a6d8f0",
	KindDriveway:    "#d0d0d0",
	KindOutbuilding: "#c9b79c",
}

// WriteSVG draws the map: spaces, doors, lines, cameras with their views, a north arrow and a 5 m scale bar.
// North is up.
func (m *Map) WriteSVG(w io.Writer) error {
	minX, minY, maxX, maxY := m.bounds()
	minX, minY, maxX, maxY = minX-svgMargin, minY-svgMargin, maxX+svgMargin, maxY+svgMargin
	width, height := (maxX-minX)*svgScale, (maxY-minY)*svgScale
	px := func(p Point) (float64, float64) { return (p[0] - minX) * svgScale, (maxY - p[1]) * svgScale }
	pts := func(points []Point) string {
		var b strings.Builder
		for i, p := range points {
			x, y := px(p)
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%.1f,%.1f", x, y)
		}
		return b.String()
	}

	bw := bufio.NewWriter(w)
	out := func(format string, args ...any) { fmt.Fprintf(bw, format, args...) }
	out(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" `+
		`font-family="sans-serif" font-size="%d">`+"\n", width, height, width, height, labelSizePx)
	out(`<rect width="100%%" height="100%%" fill="#ffffff"/>` + "\n")

	// Spaces: largest first, so a pool drawn inside a yard stays visible.
	areaColor := map[string]string{}
	for _, name := range sortedKeys(m.Spaces) {
		if s := m.Spaces[name]; s.Kind == KindRoom {
			areaColor[s.Area] = ""
		}
	}
	for i, area := range sortedKeys(areaColor) {
		areaColor[area] = areaColors[i%len(areaColors)]
	}
	names := sortedKeys(m.Spaces)
	order := make([]string, len(names))
	copy(order, names)
	sort.SliceStable(order, func(i, j int) bool {
		return PolygonArea(m.Spaces[order[i]].Polygon) > PolygonArea(m.Spaces[order[j]].Polygon)
	})
	out(`<g id="spaces" stroke="#555555" stroke-width="1">` + "\n")
	for _, name := range order {
		s := m.Spaces[name]
		fill := kindColors[s.Kind]
		if s.Kind == KindRoom {
			fill = areaColor[s.Area]
		}
		out(`<polygon points="%s" fill="%s"><title>%s</title></polygon>`+"\n", pts(s.Polygon), fill,
			esc(spaceTitle(name, s)))
	}
	out("</g>\n")

	out(`<g id="lines" fill="none" stroke="#8b5a2b" stroke-width="2" stroke-dasharray="6 3">` + "\n")
	for _, name := range sortedKeys(m.Lines) {
		out(`<polyline points="%s"><title>%s</title></polyline>`+"\n", pts(m.Lines[name].Line), esc(name))
	}
	out("</g>\n")

	out(`<g id="cameras">` + "\n")
	for _, name := range sortedKeys(m.Cameras) {
		c := m.Cameras[name]
		color := "#1f6fd1"
		if m.Outdoor(c) {
			color = "#d17a1f"
		}
		cx, cy := px(c.At)
		out(`<path d="%s" fill="%s" fill-opacity="0.15" stroke="%s" stroke-opacity="0.6"/>`+"\n",
			wedgePath(c, px), color, color)
		out(`<circle cx="%.1f" cy="%.1f" r="4" fill="%s"><title>%s</title></circle>`+"\n", cx, cy, color, esc(name))
	}
	out("</g>\n")

	out(`<g id="doors" stroke-linecap="round">` + "\n")
	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		if !d.Placed() {
			continue
		}
		stroke, dash := "#333333", ""
		switch {
		case d.Exterior:
			stroke = "#c0392b"
		case d.Kind == KindOpening:
			stroke, dash = "#ffffff", ` stroke-dasharray="3 2"`
		}
		x1, y1 := px(d.Span[0])
		x2, y2 := px(d.Span[1])
		out(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="4"%s><title>%s</title></line>`+"\n",
			x1, y1, x2, y2, stroke, dash, esc(name))
	}
	out("</g>\n")

	out(`<g id="labels" fill="#222222" text-anchor="middle">` + "\n")
	for _, name := range names {
		x, y := px(centroid(m.Spaces[name].Polygon))
		out(`<text x="%.1f" y="%.1f">%s</text>`+"\n", x, y, esc(name))
	}
	for _, name := range sortedKeys(m.Cameras) {
		x, y := px(m.Cameras[name].At)
		out(`<text x="%.1f" y="%.1f" font-size="8" fill="#444444">%s</text>`+"\n", x, y-6, esc(name))
	}
	out("</g>\n")

	// North arrow, top right.
	nx, ny := width-30, 20.0
	out(`<g id="north"><path d="M %.1f %.1f L %.1f %.1f L %.1f %.1f Z" fill="#222222"/>`+
		`<text x="%.1f" y="%.1f" text-anchor="middle" font-size="12">N</text></g>`+"\n",
		nx, ny, nx-7, ny+22, nx+7, ny+22, nx, ny+36)

	// Scale bar, bottom left.
	sx, sy := 20.0, height-20
	out(`<g id="scale"><line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#222222" stroke-width="3"/>`+
		`<text x="%.1f" y="%.1f" text-anchor="middle">%g m</text></g>`+"\n",
		sx, sy, sx+scaleBarM*svgScale, sy, sx+scaleBarM*svgScale/2, sy-6, scaleBarM)

	out("</svg>\n")
	return bw.Flush()
}

func spaceTitle(name string, s *Space) string {
	if s.Kind == KindRoom {
		return fmt.Sprintf("%s (room, area %s)", name, s.Area)
	}
	return fmt.Sprintf("%s (%s)", name, s.Kind)
}

// wedgePath draws a camera's field of view as a pie slice, or a circle for a full view.
func wedgePath(c *Camera, px func(Point) (float64, float64)) string {
	at := func(bearing float64) (float64, float64) {
		r := bearing * math.Pi / 180
		return px(Point{c.At[0] + c.RangeM*math.Sin(r), c.At[1] + c.RangeM*math.Cos(r)})
	}
	cx, cy := px(c.At)
	rad := c.RangeM * svgScale
	if c.FovDeg >= 360 {
		return fmt.Sprintf("M %.1f %.1f m %.1f 0 a %.1f %.1f 0 1 0 %.1f 0 a %.1f %.1f 0 1 0 %.1f 0",
			cx, cy, -rad, rad, rad, 2*rad, rad, rad, -2*rad)
	}
	x1, y1 := at(c.HeadingDeg - c.FovDeg/2)
	x2, y2 := at(c.HeadingDeg + c.FovDeg/2)
	large := 0
	if c.FovDeg > 180 {
		large = 1
	}
	// A larger bearing is clockwise on a north-up drawing, which is sweep 1 in SVG.
	return fmt.Sprintf("M %.1f %.1f L %.1f %.1f A %.1f %.1f 0 %d 1 %.1f %.1f Z", cx, cy, x1, y1, rad, rad, large, x2, y2)
}

// bounds is the box around every point and camera view in the map.
func (m *Map) bounds() (minX, minY, maxX, maxY float64) {
	minX, minY = math.Inf(1), math.Inf(1)
	maxX, maxY = math.Inf(-1), math.Inf(-1)
	grow := func(p Point) {
		minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
		maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
	}
	for _, s := range m.Spaces {
		for _, p := range s.Polygon {
			grow(p)
		}
	}
	for _, l := range m.Lines {
		for _, p := range l.Line {
			grow(p)
		}
	}
	for _, c := range m.Cameras {
		grow(c.At)
		for b := 0.0; b < 360; b += 15 {
			p := Point{c.At[0] + c.RangeM*math.Sin(b*math.Pi/180), c.At[1] + c.RangeM*math.Cos(b*math.Pi/180)}
			if c.Covers(p) {
				grow(p)
			}
		}
	}
	if math.IsInf(minX, 1) {
		return 0, 0, 0, 0
	}
	return minX, minY, maxX, maxY
}

// centroid is the mean of a polygon's points. It is good enough to place a label.
func centroid(poly []Point) Point {
	var c Point
	for _, p := range poly {
		c[0] += p[0] / float64(len(poly))
		c[1] += p[1] / float64(len(poly))
	}
	return c
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
