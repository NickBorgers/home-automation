package housemap

import (
	_ "embed"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"math"
	"sort"
	"strings"
)

//go:embed review.html.tmpl
var reviewTemplate string

var reviewTmpl = template.Must(template.New("review").Funcs(template.FuncMap{
	"f0": func(v float64) string { return fmt.Sprintf("%.0f", v) },
	"f1": func(v float64) string { return fmt.Sprintf("%.1f", v) },
}).Parse(reviewTemplate))

// ReviewOptions are the inputs to the review page that are not in the map.
type ReviewOptions struct {
	// Source names the map file in the page header.
	Source string

	// Seen optionally tells which room presence areas a camera sees. Areas missing from it are marked
	// unseen in the legend. Nil marks none.
	Seen map[string]bool
}

// AreaHue is the hue, 0 to 359, that tints an area. It comes from a hash of the name, so it stays the same
// from one render to the next and needs no per-area setting.
func AreaHue(area string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(area))
	return int(h.Sum32() % 360)
}

type reviewLine struct{ X1, Y1, X2, Y2 float64 }

type reviewSpace struct {
	Name, Kind, Area, Points string
	Hue                      int
}

type reviewDoor struct {
	Name, Class, KindLabel string
	X1, Y1, X2, Y2, Width  float64
	Zones                  []string
}

type reviewUnplaced struct{ Name, A, B, Lock string }

type reviewCamera struct {
	Name, Wedge, Note   string
	X, Y                float64
	Heading, Fov, Range float64
	Outdoor             bool
}

type reviewLabel struct {
	Class, Text string
	X, Y        float64
}

type reviewArea struct {
	Name   string
	Hue    int
	Unseen bool
}

type reviewPage struct {
	Source                        string
	Valetudo                      *Valetudo
	NRooms                        int
	Width, Height, NorthX, ScaleY float64
	ScaleBar                      int
	Grid                          []reviewLine
	Rooms, Outdoor, Lines         []reviewSpace
	Doors                         []reviewDoor
	Unplaced                      []reviewUnplaced
	Cameras                       []reviewCamera
	Labels                        []reviewLabel
	Areas                         []reviewArea
}

// WriteReviewHTML writes a review page for the map: a north-up plan on a 5 m grid with side panels for
// cameras, areas and doors. The page is one self-contained HTML fragment that starts with <title>, ready
// to publish as an artifact. It loads only Google Fonts.
func (m *Map) WriteReviewHTML(w io.Writer, opts ReviewOptions) error {
	// The box holds every space and camera, plus 2 m.
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	grow := func(p Point) {
		x0, y0, x1, y1 = math.Min(x0, p[0]), math.Min(y0, p[1]), math.Max(x1, p[0]), math.Max(y1, p[1])
	}
	for _, s := range m.Spaces {
		for _, p := range s.Polygon {
			grow(p)
		}
	}
	for _, c := range m.Cameras {
		grow(c.At)
	}
	x0, y0, x1, y1 = x0-svgMargin, y0-svgMargin, x1+svgMargin, y1+svgMargin
	sx := func(x float64) float64 { return (x - x0) * svgScale }
	sy := func(y float64) float64 { return (y1 - y) * svgScale }
	pts := func(points []Point) string {
		parts := make([]string, len(points))
		for i, p := range points {
			parts[i] = fmt.Sprintf("%.1f,%.1f", sx(p[0]), sy(p[1]))
		}
		return strings.Join(parts, " ")
	}

	pg := reviewPage{
		Source: opts.Source, Valetudo: m.Valetudo,
		Width: sx(x1), Height: sy(y0), NorthX: sx(x1) - 24, ScaleY: sy(y0) - 16, ScaleBar: int(scaleBarM * svgScale),
	}
	for x := math.Floor(x0/5)*5 + 5; x <= x1; x += 5 {
		pg.Grid = append(pg.Grid, reviewLine{sx(x), 0, sx(x), pg.Height})
	}
	for y := math.Floor(y0/5)*5 + 5; y <= y1; y += 5 {
		pg.Grid = append(pg.Grid, reviewLine{0, sy(y), pg.Width, sy(y)})
	}

	areas := map[string]bool{}
	for _, name := range sortedKeys(m.Spaces) {
		s := m.Spaces[name]
		c := centroid(s.Polygon)
		label := reviewLabel{Class: "on", Text: strings.ReplaceAll(name, "_", " "), X: sx(c[0]), Y: sy(c[1])}
		sp := reviewSpace{Name: name, Kind: s.Kind, Area: s.Area, Points: pts(s.Polygon)}
		if s.Kind == KindRoom {
			label.Class = "rn"
			sp.Hue = AreaHue(s.Area)
			pg.Rooms = append(pg.Rooms, sp)
			areas[s.Area] = true
		} else {
			pg.Outdoor = append(pg.Outdoor, sp)
		}
		pg.Labels = append(pg.Labels, label)
	}
	pg.NRooms = len(pg.Rooms)
	// Larger outdoor spaces first, so a pool drawn inside a yard stays visible.
	sort.SliceStable(pg.Outdoor, func(i, j int) bool {
		return PolygonArea(m.Spaces[pg.Outdoor[i].Name].Polygon) > PolygonArea(m.Spaces[pg.Outdoor[j].Name].Polygon)
	})
	for _, a := range sortedKeys(areas) {
		pg.Areas = append(pg.Areas, reviewArea{Name: a, Hue: AreaHue(a), Unseen: opts.Seen != nil && !opts.Seen[a]})
	}

	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		if !d.Placed() {
			pg.Unplaced = append(pg.Unplaced, reviewUnplaced{Name: name, A: d.Connects[0], B: d.Connects[1], Lock: d.Lock})
			continue
		}
		rd := reviewDoor{
			Name: name, Class: "door", KindLabel: d.Kind,
			X1: sx(d.Span[0][0]), Y1: sy(d.Span[0][1]), X2: sx(d.Span[1][0]), Y2: sy(d.Span[1][1]),
			Width: Distance(d.Span[0], d.Span[1]),
		}
		if d.Kind == KindOpening {
			rd.KindLabel = "open plan"
			if !d.Exterior {
				rd.Class = "opening"
			}
		}
		if d.Exterior {
			rd.Class += " ext"
		}
		for _, cam := range sortedKeys(d.Zones) {
			rd.Zones = append(rd.Zones, d.Zones[cam])
		}
		pg.Doors = append(pg.Doors, rd)
	}

	for _, name := range sortedKeys(m.Lines) {
		pg.Lines = append(pg.Lines, reviewSpace{Name: name, Points: pts(m.Lines[name].Line)})
	}

	for _, name := range sortedKeys(m.Cameras) {
		c := m.Cameras[name]
		wedge := []Point{c.At}
		for i := 0; i <= 16; i++ {
			r := (c.HeadingDeg - c.FovDeg/2 + c.FovDeg*float64(i)/16) * math.Pi / 180
			wedge = append(wedge, Point{c.At[0] + c.RangeM*math.Sin(r), c.At[1] + c.RangeM*math.Cos(r)})
		}
		x, y := sx(c.At[0]), sy(c.At[1])
		pg.Cameras = append(pg.Cameras, reviewCamera{
			Name: name, Wedge: pts(wedge), Note: c.Note, X: x, Y: y,
			Heading: c.HeadingDeg, Fov: c.FovDeg, Range: c.RangeM, Outdoor: m.Outdoor(c),
		})
		pg.Labels = append(pg.Labels, reviewLabel{Class: "cn", Text: strings.ReplaceAll(name, "_", " "), X: x + 8, Y: y - 6})
	}

	return reviewTmpl.Execute(w, pg)
}
