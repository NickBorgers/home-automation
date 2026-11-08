package housemap

import (
	"fmt"
	"sort"

	"homeautomation/internal/plugins/roompresence"
)

// CrossCheck compares the map with a room presence config and returns every disagreement, sorted. The map
// holds where rooms and doors are. Room presence holds how it reads them. The two must describe the same
// house. m must be valid: see Validate.
func CrossCheck(m *Map, rp *roompresence.Config) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// Every room's area is a room presence area.
	for _, name := range sortedKeys(m.Spaces) {
		s := m.Spaces[name]
		if s.Kind != KindRoom {
			continue
		}
		if _, ok := rp.Areas[s.Area]; !ok {
			add("space %q: area %q is not in the room presence config", name, s.Area)
		}
	}

	// joined[pair] lists the doors between two different areas. linked[pair] is true when a door between
	// them has a boundary zone.
	joined := map[[2]string][]string{}
	linked := map[[2]string]bool{}
	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		a, b := m.areaOf(d.Connects[0]), m.areaOf(d.Connects[1])
		for _, cam := range sortedKeys(d.Zones) {
			zone := d.Zones[cam]
			from, to, ok := rp.BoundaryAreas(zone)
			if !ok {
				add("door %q: %s zone %q is not a boundary zone in room presence", name, cam, zone)
				continue
			}
			if a == "" || b == "" || pairOf(from, to) != pairOf(a, b) {
				add("door %q: %s zone %q is a boundary between %s and %s, but the door joins %s",
					name, cam, zone, from, to, describeAreas(a, b))
				continue
			}
			linked[pairOf(a, b)] = true
		}
		if a != "" && b != "" && a != b {
			joined[pairOf(a, b)] = append(joined[pairOf(a, b)], name)
		}
	}

	// Areas joined by a door are linked in room presence, by neighbors or by a boundary zone.
	for _, pair := range sortedPairs(joined) {
		if linked[pair] || neighbors(rp, pair[0], pair[1]) {
			continue
		}
		add("areas %s and %s are joined by %v, but room presence has neither neighbors nor a boundary zone "+
			"between them", pair[0], pair[1], joined[pair])
	}

	// Room presence neighbors are joined by a door.
	seen := map[[2]string]bool{}
	for _, area := range sortedKeys(rp.Areas) {
		for _, n := range rp.Areas[area].Neighbors {
			pair := pairOf(area, n)
			if seen[pair] || area == n {
				continue
			}
			seen[pair] = true
			if len(joined[pair]) == 0 {
				add("room presence neighbors %s and %s are not joined by any door or opening in the map", pair[0], pair[1])
			}
		}
	}

	// An area that leads outside has a room with an exterior door.
	exterior := map[string]bool{}
	for _, d := range m.Doors {
		if !d.Exterior {
			continue
		}
		for _, s := range d.Connects {
			if sp := m.Spaces[s]; sp != nil && sp.Kind == KindRoom {
				exterior[sp.Area] = true
			}
		}
	}
	for _, area := range sortedKeys(rp.Areas) {
		if rp.Areas[area].LeadsOutside && !exterior[area] {
			add("area %q leads outside, but no room of it has an exterior door in the map", area)
		}
	}

	sort.Strings(problems)
	return problems
}

// areaOf is the room presence area of a space, or "" for a space that is not a room.
func (m *Map) areaOf(space string) string {
	if s := m.Spaces[space]; s != nil && s.Kind == KindRoom {
		return s.Area
	}
	return ""
}

func describeAreas(a, b string) string {
	name := func(s string) string {
		if s == "" {
			return "a space with no area"
		}
		return s
	}
	return name(a) + " and " + name(b)
}

func neighbors(rp *roompresence.Config, a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		if ac := rp.Areas[pair[0]]; ac != nil {
			for _, n := range ac.Neighbors {
				if n == pair[1] {
					return true
				}
			}
		}
	}
	return false
}

func pairOf(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

func sortedPairs[V any](m map[[2]string]V) [][2]string {
	keys := make([][2]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}
