// Package housemap is a 2D map of a house, inside and out: rooms and outdoor spaces, the doors between them,
// fences, and cameras with their fields of view.
//
// It is the one place for house geometry and topology. Behaviour, such as idle limits and sensors, stays
// with the plugins. See docs/architecture/HOUSE_MAP.md. The map of a real house is private deployment data.
// This package holds only the schema, the checks and the geometry.
package housemap

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the map file a deployment keeps in its config directory.
const FileName = "house_map.yaml"

// Space kinds. A room is part of the house that room presence knows as an area.
const (
	KindRoom        = "room"
	KindGarage      = "garage"
	KindYard        = "yard"
	KindPatio       = "patio"
	KindDeck        = "deck"
	KindPool        = "pool"
	KindDriveway    = "driveway"
	KindOutbuilding = "outbuilding"
)

// Door kinds. An opening has no door leaf. An overhead door is a garage door.
const (
	KindDoor     = "door"
	KindOpening  = "opening"
	KindOverhead = "overhead"
)

// Line kinds.
const (
	KindFence = "fence"
)

var (
	spaceKinds = []string{KindRoom, KindGarage, KindYard, KindPatio, KindDeck, KindPool, KindDriveway, KindOutbuilding}
	doorKinds  = []string{KindDoor, KindOpening, KindOverhead}
	lineKinds  = []string{KindFence}
)

// Point is [x, y] in metres. x points east and y points north.
type Point [2]float64

// X is the east coordinate.
func (p Point) X() float64 { return p[0] }

// Y is the north coordinate.
func (p Point) Y() float64 { return p[1] }

// fileMap is the YAML file.
type fileMap struct {
	HouseMap *Map `yaml:"house_map"`
}

// Map is the house map.
type Map struct {
	// Frame describes the coordinate frame. Only metres, x east and y north are supported.
	Frame Frame `yaml:"frame"`

	// Valetudo optionally records how the map was drawn from a vacuum map. Only a deployment's own tools
	// read it.
	Valetudo *Valetudo `yaml:"valetudo"`

	// Spaces are rooms and outdoor spaces, by name.
	Spaces map[string]*Space `yaml:"spaces"`

	// Doors join two spaces, by name.
	Doors map[string]*Door `yaml:"doors"`

	// Lines are fences and other lines that are not spaces, by name.
	Lines map[string]*Line `yaml:"lines"`

	// Cameras, by name.
	Cameras map[string]*Camera `yaml:"cameras"`
}

// Frame describes the coordinate frame.
type Frame struct {
	Units  string `yaml:"units"`
	X      string `yaml:"x"`
	Y      string `yaml:"y"`
	Origin string `yaml:"origin"`
}

// Valetudo records how a vacuum map was turned into this frame. A deployment's tools use it to trace rooms
// again from a new vacuum map.
type Valetudo struct {
	// RotationDeg turns the vacuum map clockwise to north-up.
	RotationDeg float64 `yaml:"rotation_deg"`

	// PixelSizeM is the size of one vacuum map pixel, in metres.
	PixelSizeM float64 `yaml:"pixel_size_m"`

	// AnchorPx is a vacuum map pixel that lands on AnchorM in this frame.
	AnchorPx []float64 `yaml:"anchor_px"`
	AnchorM  []float64 `yaml:"anchor_m"`

	// DockM is where the vacuum's dock lands. When a new vacuum map moves it, the frame changed.
	DockM []float64 `yaml:"dock_m"`
}

// Space is a room or an outdoor space.
type Space struct {
	Kind string `yaml:"kind"`

	// Area is the room presence area this room belongs to. Rooms only.
	Area string `yaml:"area"`

	// Polygon is the outline, at least three points, in order.
	Polygon []Point `yaml:"polygon"`

	Note string `yaml:"note"`
}

// Door joins two spaces.
type Door struct {
	Kind string `yaml:"kind"`

	// Connects names the two spaces the door joins.
	Connects []string `yaml:"connects"`

	// Span is the two ends of the doorway. A door with no span is in the map but not yet placed.
	Span []Point `yaml:"span"`

	// Exterior marks a door from the house to the outside.
	Exterior bool `yaml:"exterior"`

	// Zones maps a Frigate camera id to the boundary zone that camera has in front of this door.
	Zones map[string]string `yaml:"zones"`

	// Lock is the entity that reports or controls the door's lock.
	Lock string `yaml:"lock"`

	// Sensors are other entities that report on the door.
	Sensors []string `yaml:"sensors"`

	Note string `yaml:"note"`
}

// Line is a fence or another line.
type Line struct {
	Kind string `yaml:"kind"`

	// Line is the points of the polyline, at least two.
	Line []Point `yaml:"line"`

	Note string `yaml:"note"`
}

// Camera is a camera with a field of view.
type Camera struct {
	// Frigate is the camera's Frigate id. Indoor Frigate cameras only.
	Frigate string `yaml:"frigate"`

	// At is where the camera is.
	At Point `yaml:"at"`

	// HeadingDeg is the middle of the view, in degrees clockwise from north.
	HeadingDeg float64 `yaml:"heading_deg"`

	// FovDeg is the horizontal field of view in degrees.
	FovDeg float64 `yaml:"fov_deg"`

	// RangeM is how far the camera can usefully see, in metres.
	RangeM float64 `yaml:"range_m"`

	// Entities are Home Assistant entities the camera reports through.
	Entities []string `yaml:"entities"`

	Note string `yaml:"note"`
}

// ValidationError lists every problem in a map, sorted.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "invalid house map: " + strings.Join(e.Problems, "; ")
}

// Load reads and validates a map file. A missing file returns (nil, nil): the deployment has no map.
func Load(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses and validates map YAML.
func Parse(data []byte) (*Map, error) {
	var f fileMap
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse house map: %w", err)
	}
	if f.HouseMap == nil {
		return nil, fmt.Errorf("house map has no house_map section")
	}
	if err := f.HouseMap.Validate(); err != nil {
		return nil, err
	}
	return f.HouseMap, nil
}

// Validate checks the map. It returns a *ValidationError that lists every problem.
func (m *Map) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if m.Frame.Units != "" && m.Frame.Units != "m" {
		add("frame: units %q must be m", m.Frame.Units)
	}
	if m.Frame.X != "" && m.Frame.X != "east" {
		add("frame: x %q must be east", m.Frame.X)
	}
	if m.Frame.Y != "" && m.Frame.Y != "north" {
		add("frame: y %q must be north", m.Frame.Y)
	}
	if v := m.Valetudo; v != nil {
		if v.PixelSizeM <= 0 {
			add("valetudo: pixel_size_m %g must be positive", v.PixelSizeM)
		}
		for _, f := range []struct {
			name string
			v    []float64
		}{{"anchor_px", v.AnchorPx}, {"anchor_m", v.AnchorM}, {"dock_m", v.DockM}} {
			if f.v != nil && len(f.v) != 2 {
				add("valetudo: %s needs 2 numbers, has %d", f.name, len(f.v))
			}
		}
	}
	if len(m.Spaces) == 0 {
		add("no spaces")
	}

	for _, name := range sortedKeys(m.Spaces) {
		s := m.Spaces[name]
		if s == nil {
			add("space %q: empty", name)
			continue
		}
		if !known(spaceKinds, s.Kind) {
			add("space %q: kind %q must be one of %s", name, s.Kind, strings.Join(spaceKinds, ", "))
		}
		if len(s.Polygon) < 3 {
			add("space %q: polygon needs at least 3 points, has %d", name, len(s.Polygon))
		}
		if s.Kind == KindRoom && s.Area == "" {
			add("space %q: a room needs an area", name)
		}
		if s.Kind != KindRoom && s.Area != "" {
			add("space %q: only a room has an area", name)
		}
	}

	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		if d == nil {
			add("door %q: empty", name)
			continue
		}
		if !known(doorKinds, d.Kind) {
			add("door %q: kind %q must be one of %s", name, d.Kind, strings.Join(doorKinds, ", "))
		}
		if len(d.Span) != 0 && len(d.Span) != 2 {
			add("door %q: span needs 2 points, has %d", name, len(d.Span))
		}
		if len(d.Connects) != 2 {
			add("door %q: connects needs 2 spaces, has %d", name, len(d.Connects))
			continue
		}
		a, b := d.Connects[0], d.Connects[1]
		if a == b {
			add("door %q: connects %q to itself", name, a)
		}
		sa, sb := m.Spaces[a], m.Spaces[b]
		for _, s := range []string{a, b} {
			if m.Spaces[s] == nil {
				add("door %q: unknown space %q", name, s)
			}
		}
		if sa == nil || sb == nil || !d.Exterior {
			continue
		}
		if !exteriorPair(sa.Kind, sb.Kind) && !exteriorPair(sb.Kind, sa.Kind) {
			add("door %q: an exterior door joins a room or garage to a space that is not a room, not %s and %s",
				name, sa.Kind, sb.Kind)
		}
	}

	for _, name := range sortedKeys(m.Lines) {
		l := m.Lines[name]
		if l == nil {
			add("line %q: empty", name)
			continue
		}
		if !known(lineKinds, l.Kind) {
			add("line %q: kind %q must be one of %s", name, l.Kind, strings.Join(lineKinds, ", "))
		}
		if len(l.Line) < 2 {
			add("line %q: needs at least 2 points, has %d", name, len(l.Line))
		}
	}

	for _, name := range sortedKeys(m.Cameras) {
		c := m.Cameras[name]
		if c == nil {
			add("camera %q: empty", name)
			continue
		}
		if c.FovDeg <= 0 || c.FovDeg > 360 {
			add("camera %q: fov_deg %g must be more than 0 and at most 360", name, c.FovDeg)
		}
		if c.HeadingDeg < 0 || c.HeadingDeg >= 360 {
			add("camera %q: heading_deg %g must be at least 0 and less than 360", name, c.HeadingDeg)
		}
		if c.RangeM <= 0 {
			add("camera %q: range_m %g must be positive", name, c.RangeM)
		}
	}

	if len(errs) > 0 {
		sort.Strings(errs)
		return &ValidationError{Problems: errs}
	}
	return nil
}

// exteriorPair reports whether an exterior door may lead from a space of kind inside to one of kind outside.
func exteriorPair(inside, outside string) bool {
	return (inside == KindRoom || inside == KindGarage) && outside != KindRoom
}

// Placed reports whether the door has a position.
func (d *Door) Placed() bool {
	return len(d.Span) == 2
}

// Building reports whether a space is part of the house: a room or the garage.
func (s *Space) Building() bool {
	return s.Kind == KindRoom || s.Kind == KindGarage
}

func known(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
