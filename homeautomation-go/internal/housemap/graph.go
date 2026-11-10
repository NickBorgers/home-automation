package housemap

import (
	"container/heap"
	"math"
	"sort"
)

// Graph is the room graph a robot vacuum drives on. Its nodes are the rooms of the map. Its edges are the
// interior doors and openings between two rooms. Exterior doors, overhead doors and spaces that are not
// rooms are left out.
//
// Path lengths run from door midpoint to door midpoint, so only placed doors carry a path. Neighbors also
// counts doors that are not placed yet, so a check that keeps a robot away from a room errs on the safe side.
type Graph struct {
	m         *Map
	rooms     map[string]bool
	exits     map[string][]graphDoor // placed doors out of each room
	neighbors map[string][]string    // every interior door, placed or not
}

// graphDoor is a placed door seen from one of its rooms.
type graphDoor struct {
	name string
	to   string
	mid  Point
}

// Graph builds the room graph of the map.
func (m *Map) Graph() *Graph {
	g := &Graph{
		m:         m,
		rooms:     make(map[string]bool),
		exits:     make(map[string][]graphDoor),
		neighbors: make(map[string][]string),
	}
	for name, s := range m.Spaces {
		if s != nil && s.Kind == KindRoom {
			g.rooms[name] = true
		}
	}
	seen := make(map[[2]string]bool)
	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		if d == nil || d.Exterior || d.Kind == KindOverhead || len(d.Connects) != 2 {
			continue
		}
		a, b := d.Connects[0], d.Connects[1]
		if !g.rooms[a] || !g.rooms[b] || a == b {
			continue
		}
		if !seen[[2]string{a, b}] {
			seen[[2]string{a, b}], seen[[2]string{b, a}] = true, true
			g.neighbors[a] = append(g.neighbors[a], b)
			g.neighbors[b] = append(g.neighbors[b], a)
		}
		if d.Placed() {
			mid := d.Midpoint()
			g.exits[a] = append(g.exits[a], graphDoor{name: name, to: b, mid: mid})
			g.exits[b] = append(g.exits[b], graphDoor{name: name, to: a, mid: mid})
		}
	}
	for r := range g.neighbors {
		sort.Strings(g.neighbors[r])
	}
	return g
}

// Has reports whether room is a node of the graph.
func (g *Graph) Has(room string) bool {
	return g.rooms[room]
}

// Rooms lists the rooms of the graph, sorted.
func (g *Graph) Rooms() []string {
	return sortedKeys(g.rooms)
}

// Neighbors lists the rooms one interior door or opening away from room, sorted. Doors that are not placed
// count.
func (g *Graph) Neighbors(room string) []string {
	return append([]string(nil), g.neighbors[room]...)
}

// Area is the room presence area of a room, or "" when room is not in the graph.
func (g *Graph) Area(room string) string {
	if !g.rooms[room] {
		return ""
	}
	return g.m.Spaces[room].Area
}

// Center is the mean of a room's polygon points. Paths start and end there.
func (g *Graph) Center(room string) Point {
	return centroid(g.m.Spaces[room].Polygon)
}

// FloorArea is the area of a room's polygon in square metres, or 0 when room is not in the graph.
func (g *Graph) FloorArea(room string) float64 {
	if !g.rooms[room] {
		return 0
	}
	return PolygonArea(g.m.Spaces[room].Polygon)
}

// ShortestPath finds the shortest way from the centre of room from to the centre of room to, through
// placed interior doors. It returns the rooms in order, from first and to last, and the length in metres.
//
// avoid names rooms the path must not enter. It applies to to and to every room in between, but not to
// from: a robot that is already in a room can always leave it. ok is false when either room is not in the
// graph or no path exists.
func (g *Graph) ShortestPath(from, to string, avoid map[string]bool) (rooms []string, metres float64, ok bool) {
	if !g.rooms[from] || !g.rooms[to] {
		return nil, 0, false
	}
	if from == to {
		return []string{from}, 0, true
	}
	if avoid[to] {
		return nil, 0, false
	}

	// Dijkstra over door crossings. A state is "just crossed door d into room r". The start state is the
	// centre of from.
	type state struct {
		door string
		room string
	}
	start := state{room: from}
	dist := map[state]float64{start: 0}
	prev := make(map[state]state)
	pos := map[state]Point{start: g.Center(from)}
	pq := &pathQueue{{s: start, d: 0}}
	done := make(map[state]bool)
	var end state
	best := math.Inf(1)
	target := g.Center(to)

	for pq.Len() > 0 {
		item := heap.Pop(pq).(pathItem)
		cur := item.s.(state)
		if done[cur] {
			continue
		}
		done[cur] = true
		if item.d >= best {
			break
		}
		if cur.room == to {
			if total := item.d + Distance(pos[cur], target); total < best {
				best, end = total, cur
			}
			continue
		}
		for _, ex := range g.exits[cur.room] {
			if avoid[ex.to] && ex.to != from {
				continue
			}
			next := state{door: ex.name, room: ex.to}
			nd := item.d + Distance(pos[cur], ex.mid)
			if old, seen := dist[next]; seen && old <= nd {
				continue
			}
			dist[next], prev[next], pos[next] = nd, cur, ex.mid
			heap.Push(pq, pathItem{s: next, d: nd})
		}
	}
	if math.IsInf(best, 1) {
		return nil, 0, false
	}
	for s := end; ; s = prev[s] {
		rooms = append(rooms, s.room)
		if s == start {
			break
		}
	}
	for i, j := 0, len(rooms)-1; i < j; i, j = i+1, j-1 {
		rooms[i], rooms[j] = rooms[j], rooms[i]
	}
	return rooms, best, true
}

// ToFrame turns a Valetudo map pixel into a point in the house frame. It is the same transform as the
// deployment tool that traces rooms from a vacuum map: turn by RotationDeg about AnchorPx, scale by
// PixelSizeM, flip y (Valetudo's y points down), then move AnchorPx onto AnchorM.
func (v *Valetudo) ToFrame(px Point) Point {
	th := v.RotationDeg * math.Pi / 180
	apx, apy := pair(v.AnchorPx)
	amx, amy := pair(v.AnchorM)
	dx, dy := px[0]-apx, px[1]-apy
	rx := dx*math.Cos(th) - dy*math.Sin(th)
	ry := dx*math.Sin(th) + dy*math.Cos(th)
	return Point{amx + rx*v.PixelSizeM, amy - ry*v.PixelSizeM}
}

// pair reads an optional two-number field. A missing field is the origin.
func pair(v []float64) (float64, float64) {
	if len(v) != 2 {
		return 0, 0
	}
	return v[0], v[1]
}

type pathItem struct {
	s any
	d float64
}

type pathQueue []pathItem

func (q pathQueue) Len() int           { return len(q) }
func (q pathQueue) Less(i, j int) bool { return q[i].d < q[j].d }
func (q pathQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *pathQueue) Push(x any)        { *q = append(*q, x.(pathItem)) }
func (q *pathQueue) Pop() any          { old := *q; it := old[len(old)-1]; *q = old[:len(old)-1]; return it }
