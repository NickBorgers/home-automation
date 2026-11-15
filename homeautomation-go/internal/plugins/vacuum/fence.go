package vacuum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"homeautomation/internal/housemap"
	"homeautomation/internal/valetudo"

	"go.uber.org/zap"
)

// Fencing puts Valetudo virtual walls across the doors into no_enter rooms before a run the scheduler starts.
// The robot plans its own route, and may drive through a room the planner's room graph keeps it out of, for
// example to get around a dog lying in a doorway. A virtual wall is the one thing its path planner obeys.
//
// The robot holds one set of walls and zones, and the owner draws some of them. The scheduler saves the
// owner's set to a Home Assistant helper before it adds its walls, and puts the owner's set back exactly when
// the run ends, whatever the outcome. The helper survives a restart: at startup a saved set is put back once
// no run is tracked. If the helper is empty at startup, any wall that matches a wall the scheduler would draw
// across a house-map door is taken away, so a lost save still leaves no scheduler wall behind.
//
// Walls are set only before the run starts. Valetudo itself lets restrictions change at any time (its Dreame
// code sends the firmware a map_edit action and only the firmware's answer decides), but the scheduler never
// changes them during a job: what the firmware does with a change in the middle of a job is not known.
// Occupancy changes during a run use the live-safety rules instead (pause or return home).

// fenceEntity holds the owner's virtual restrictions while the scheduler's walls are up. "" means nothing is
// waiting to be restored.
const fenceEntity = "input_text.vacuum_fence_saved"

// fenceStoreDefaultMax is Home Assistant's default input_text length, used when the helper does not report
// max. fenceStoreMaxLen is the longest any input_text can hold.
const (
	fenceStoreDefaultMax = 100
	fenceStoreMaxLen     = 255
)

// fenceTolCm is how far two points may be apart, in centimetres, and still be the same point. Valetudo
// rounds every restriction point to a whole centimetre.
const fenceTolCm = 1.0

// fenceVerifyTries is how many times the restrictions are read back after a change. Valetudo answers from
// the map it polled last and polls again only after a change, so a change may show a few seconds late.
//
// The read-backs run in the tick and hold tickMu. Normally the first or second read matches, in about a
// second. In the worst case every read times out: fenceVerifyTries times (commandTimeout plus
// defaultFenceVerifyDelay), about a minute, and twice that when a failed fence is undone at once. Ticks are
// then late by that much; nothing runs meanwhile, because no run started.
const fenceVerifyTries = 10

// defaultFenceVerifyDelay is the wait between two read-backs.
const defaultFenceVerifyDelay = time.Second

// fenceState is what the scheduler knows about its walls on the robot. The caller holds tickMu.
type fenceState struct {
	// pending is true while scheduler walls may be on the robot. Nothing starts until they are gone.
	pending bool
	// owner is the owner's set to put back. nil means it is not known: scheduler walls are then found by
	// their geometry and taken away, and everything else is kept.
	owner *valetudo.Restrictions
	// checked is true once the helper was read at startup.
	checked bool
	// problem says why the last fencing or restore failed; "" when it worked.
	problem string
}

// doorWall is a wall across one door, in house metres. It reaches margin past both jambs.
type doorWall struct {
	door string
	from string // the room the robot may be in
	into string // the room it must not enter
	a, b housemap.Point
}

// interiorDoor reports whether a door joins two rooms of the room graph, as the graph counts doors.
func interiorDoor(d *housemap.Door, g *housemap.Graph) bool {
	return d != nil && !d.Exterior && d.Kind != housemap.KindOverhead && len(d.Connects) == 2 &&
		d.Connects[0] != d.Connects[1] && g.Has(d.Connects[0]) && g.Has(d.Connects[1])
}

// extendSpan lengthens a door span by margin at both ends, along the span.
func extendSpan(a, b housemap.Point, margin float64) (housemap.Point, housemap.Point) {
	l := housemap.Distance(a, b)
	if l == 0 {
		return a, b
	}
	ux, uy := (b[0]-a[0])/l, (b[1]-a[1])/l
	return housemap.Point{a[0] - ux*margin, a[1] - uy*margin}, housemap.Point{b[0] + ux*margin, b[1] + uy*margin}
}

// fenceWalls lists a wall for every door from a room the robot can reach from the dock into a no_enter room.
// A room is reachable when a way from the dock gets to it without entering a no_enter room; doors that are
// not placed count, as they do in the room graph. A door into a no_enter room that is not placed cannot be
// walled; it is listed in unplaced. Walls are sorted by door name.
func fenceWalls(m *housemap.Map, g *housemap.Graph, dock string, noEnter map[string]string,
	margin float64) (walls []doorWall, unplaced []string) {
	reach := map[string]bool{}
	if _, blocked := noEnter[dock]; g.Has(dock) && !blocked {
		reach[dock] = true
		queue := []string{dock}
		for len(queue) > 0 {
			r := queue[0]
			queue = queue[1:]
			for _, n := range g.Neighbors(r) {
				if _, blocked := noEnter[n]; !blocked && !reach[n] {
					reach[n] = true
					queue = append(queue, n)
				}
			}
		}
	}
	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		if !interiorDoor(d, g) {
			continue
		}
		a, b := d.Connects[0], d.Connects[1]
		_, aNo := noEnter[a]
		_, bNo := noEnter[b]
		from, into := "", ""
		switch {
		case reach[a] && bNo:
			from, into = a, b
		case reach[b] && aNo:
			from, into = b, a
		default:
			continue
		}
		if !d.Placed() {
			unplaced = append(unplaced, name)
			continue
		}
		pa, pb := extendSpan(d.Span[0], d.Span[1], margin)
		walls = append(walls, doorWall{door: name, from: from, into: into, a: pa, b: pb})
	}
	return walls, unplaced
}

// allDoorWalls lists the wall the scheduler would draw across every placed interior door. A wall on the
// robot that matches one of them is taken to be the scheduler's.
func allDoorWalls(m *housemap.Map, g *housemap.Graph, margin float64) []doorWall {
	var out []doorWall
	for _, name := range sortedKeys(m.Doors) {
		d := m.Doors[name]
		if !interiorDoor(d, g) || !d.Placed() {
			continue
		}
		pa, pb := extendSpan(d.Span[0], d.Span[1], margin)
		out = append(out, doorWall{door: name, from: d.Connects[0], into: d.Connects[1], a: pa, b: pb})
	}
	return out
}

// toValetudo turns a wall in house metres into Valetudo centimetres, rounded to whole centimetres as Valetudo
// stores them. cmPerPx is the map's pixel size.
func toValetudo(v *housemap.Valetudo, cmPerPx float64, w doorWall) valetudo.VirtualWall {
	pa, pb := v.FromFrame(w.a), v.FromFrame(w.b)
	return valetudo.NewVirtualWall(math.Round(pa[0]*cmPerPx), math.Round(pa[1]*cmPerPx),
		math.Round(pb[0]*cmPerPx), math.Round(pb[1]*cmPerPx))
}

func wallsToValetudo(v *housemap.Valetudo, cmPerPx float64, walls []doorWall) []valetudo.VirtualWall {
	out := make([]valetudo.VirtualWall, 0, len(walls))
	for _, w := range walls {
		out = append(out, toValetudo(v, cmPerPx, w))
	}
	return out
}

// plannedRoute is every room on the planned way: from the dock to each target in order, and back. Each way is
// the shortest one, as the planner checks it. steps are the pairs of rooms the way crosses between.
func plannedRoute(g RoomGraph, dock string, targets []string) (rooms map[string]bool, steps [][2]string) {
	rooms = map[string]bool{dock: true}
	seen := map[[2]string]bool{}
	prev := dock
	for _, to := range append(append([]string(nil), targets...), dock) {
		path, _, ok := g.ShortestPath(prev, to, nil)
		if ok {
			for i, r := range path {
				rooms[r] = true
				if i > 0 && !seen[[2]string{path[i-1], r}] {
					seen[[2]string{path[i-1], r}] = true
					steps = append(steps, [2]string{path[i-1], r})
				}
			}
		}
		prev = to
	}
	return rooms, steps
}

// checkFenceKeepsRoute makes sure no wall cuts the planned way. A wall must never stand in a door the run
// drives through, nor fence in the dock room. The plan already keeps the way out of no_enter rooms, so this
// only fails when the house map's doors are drawn so close together that an extended wall reaches across
// another door.
func checkFenceKeepsRoute(m *housemap.Map, g *housemap.Graph, dock string, targets []string,
	walls []doorWall) error {
	_, steps := plannedRoute(g, dock, targets)
	for _, w := range walls {
		if w.into == dock {
			return fmt.Errorf("wall across %s would fence in the dock room %s", w.door, dock)
		}
	}
	for _, st := range steps {
		for _, name := range sortedKeys(m.Doors) {
			d := m.Doors[name]
			if !interiorDoor(d, g) || !d.Placed() || !joins(d, st[0], st[1]) {
				continue
			}
			for _, w := range walls {
				if w.door == name || segmentsTouch(w.a, w.b, d.Span[0], d.Span[1]) {
					return fmt.Errorf("wall across %s (into %s) would block door %s between %s and %s on the "+
						"planned way", w.door, w.into, name, st[0], st[1])
				}
			}
		}
	}
	return nil
}

func joins(d *housemap.Door, a, b string) bool {
	return (d.Connects[0] == a && d.Connects[1] == b) || (d.Connects[0] == b && d.Connects[1] == a)
}

// segmentsTouch reports whether segments p1-p2 and p3-p4 cross or touch.
func segmentsTouch(p1, p2, p3, p4 housemap.Point) bool {
	const eps = 1e-9
	cross := func(o, a, b housemap.Point) float64 {
		return (a[0]-o[0])*(b[1]-o[1]) - (a[1]-o[1])*(b[0]-o[0])
	}
	onSeg := func(p, q, r housemap.Point) bool { // q on segment p-r, given collinear
		return math.Min(p[0], r[0])-eps <= q[0] && q[0] <= math.Max(p[0], r[0])+eps &&
			math.Min(p[1], r[1])-eps <= q[1] && q[1] <= math.Max(p[1], r[1])+eps
	}
	sign := func(v float64) int {
		switch {
		case v > eps:
			return 1
		case v < -eps:
			return -1
		}
		return 0
	}
	d1, d2 := sign(cross(p3, p4, p1)), sign(cross(p3, p4, p2))
	d3, d4 := sign(cross(p1, p2, p3)), sign(cross(p1, p2, p4))
	if d1*d2 < 0 && d3*d4 < 0 {
		return true
	}
	return (d1 == 0 && onSeg(p3, p1, p4)) || (d2 == 0 && onSeg(p3, p2, p4)) ||
		(d3 == 0 && onSeg(p1, p3, p2)) || (d4 == 0 && onSeg(p1, p4, p2))
}

// ---------------------------------------------------------------------------
// Comparing restrictions
// ---------------------------------------------------------------------------

func samePoint(a, b valetudo.RestrictionPoint) bool {
	return math.Abs(a.X-b.X) <= fenceTolCm && math.Abs(a.Y-b.Y) <= fenceTolCm
}

func sameWall(a, b valetudo.VirtualWall) bool {
	return (samePoint(a.Points.PA, b.Points.PA) && samePoint(a.Points.PB, b.Points.PB)) ||
		(samePoint(a.Points.PA, b.Points.PB) && samePoint(a.Points.PB, b.Points.PA))
}

func zoneCorners(z valetudo.RestrictedZone) []valetudo.RestrictionPoint {
	c := []valetudo.RestrictionPoint{z.Points.PA, z.Points.PB, z.Points.PC, z.Points.PD}
	sort.Slice(c, func(i, j int) bool {
		if c[i].X != c[j].X {
			return c[i].X < c[j].X
		}
		return c[i].Y < c[j].Y
	})
	return c
}

func sameZone(a, b valetudo.RestrictedZone) bool {
	if a.Type != b.Type {
		return false
	}
	ca, cb := zoneCorners(a), zoneCorners(b)
	for i := range ca {
		if !samePoint(ca[i], cb[i]) {
			return false
		}
	}
	return true
}

// matchAll reports whether two lists hold the same items, in any order.
func matchAll[T any](a, b []T, same func(x, y T) bool) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, x := range a {
		found := false
		for j, y := range b {
			if !used[j] && same(x, y) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// sameRestrictions reports whether two sets hold the same walls and zones, in any order.
func sameRestrictions(a, b valetudo.Restrictions) bool {
	return matchAll(a.VirtualWalls, b.VirtualWalls, sameWall) &&
		matchAll(a.RestrictedZones, b.RestrictedZones, sameZone)
}

// stripWalls takes away every wall that matches one of the scheduler's walls. Zones are always kept.
func stripWalls(cur valetudo.Restrictions, ours []valetudo.VirtualWall) (valetudo.Restrictions, int) {
	out := valetudo.Restrictions{VirtualWalls: []valetudo.VirtualWall{},
		RestrictedZones: append([]valetudo.RestrictedZone{}, cur.RestrictedZones...)}
	n := 0
	for _, w := range cur.VirtualWalls {
		mine := false
		for _, o := range ours {
			mine = mine || sameWall(w, o)
		}
		if mine {
			n++
			continue
		}
		out.VirtualWalls = append(out.VirtualWalls, w)
	}
	return out, n
}

// matchedDoors names the doors whose scheduler wall is on the robot. cm holds the walls in Valetudo
// centimetres, in the order of walls.
func matchedDoors(cur valetudo.Restrictions, walls []doorWall, cm []valetudo.VirtualWall) []string {
	var out []string
	for i, w := range walls {
		for _, c := range cur.VirtualWalls {
			if sameWall(c, cm[i]) {
				out = append(out, w.door)
				break
			}
		}
	}
	return out
}

// withWalls is the owner's set plus the scheduler's walls.
func withWalls(owner valetudo.Restrictions, walls []valetudo.VirtualWall) valetudo.Restrictions {
	return valetudo.Restrictions{
		VirtualWalls:    append(append([]valetudo.VirtualWall{}, owner.VirtualWalls...), walls...),
		RestrictedZones: append([]valetudo.RestrictedZone{}, owner.RestrictedZones...),
	}
}

// ---------------------------------------------------------------------------
// The saved copy
// ---------------------------------------------------------------------------

// fenceStore is the helper's JSON, short enough for 255 characters. A wall is [ax, ay, bx, by]. A zone is
// [type, ax, ay, cx, cy] when it is a rectangle along the axes, as every Dreame zone is, and
// [type, ax, ay, bx, by, cx, cy, dx, dy] otherwise. Type is "r" for regular and "m" for mop:
//
//	{"w":[],"z":[["r",3005,4150,3085,4205]]}
type fenceStore struct {
	W [][]float64         `json:"w"`
	Z [][]json.RawMessage `json:"z"`
}

var zoneCodes = map[string]string{valetudo.ZoneRegular: "r", valetudo.ZoneMop: "m"}

// encodeFence writes the owner's set as the helper's JSON.
func encodeFence(r valetudo.Restrictions) (string, error) {
	type zoneOut []any
	out := struct {
		W [][]float64 `json:"w"`
		Z []zoneOut   `json:"z"`
	}{W: [][]float64{}, Z: []zoneOut{}}
	for _, w := range r.VirtualWalls {
		out.W = append(out.W, []float64{w.Points.PA.X, w.Points.PA.Y, w.Points.PB.X, w.Points.PB.Y})
	}
	for _, z := range r.RestrictedZones {
		code, ok := zoneCodes[z.Type]
		if !ok {
			return "", fmt.Errorf("restricted zone type %q cannot be saved", z.Type)
		}
		p := z.Points
		if p.PB == (valetudo.RestrictionPoint{X: p.PC.X, Y: p.PA.Y}) &&
			p.PD == (valetudo.RestrictionPoint{X: p.PA.X, Y: p.PC.Y}) {
			out.Z = append(out.Z, zoneOut{code, p.PA.X, p.PA.Y, p.PC.X, p.PC.Y})
			continue
		}
		out.Z = append(out.Z, zoneOut{code, p.PA.X, p.PA.Y, p.PB.X, p.PB.Y, p.PC.X, p.PC.Y, p.PD.X, p.PD.Y})
	}
	b, err := json.Marshal(out)
	return string(b), err
}

// decodeFence reads the helper's JSON.
func decodeFence(value string) (*valetudo.Restrictions, error) {
	var s fenceStore
	dec := json.NewDecoder(strings.NewReader(value))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, err
	}
	if s.W == nil || s.Z == nil {
		return nil, errors.New("saved restrictions need both w and z")
	}
	r := &valetudo.Restrictions{VirtualWalls: []valetudo.VirtualWall{}, RestrictedZones: []valetudo.RestrictedZone{}}
	for i, w := range s.W {
		if len(w) != 4 {
			return nil, fmt.Errorf("wall %d: want 4 numbers, got %d", i, len(w))
		}
		r.VirtualWalls = append(r.VirtualWalls, valetudo.NewVirtualWall(w[0], w[1], w[2], w[3]))
	}
	for i, raw := range s.Z {
		if len(raw) != 5 && len(raw) != 9 {
			return nil, fmt.Errorf("zone %d: want a type and 4 or 8 numbers, got %d items", i, len(raw))
		}
		var code string
		if err := json.Unmarshal(raw[0], &code); err != nil {
			return nil, fmt.Errorf("zone %d type: %w", i, err)
		}
		var z valetudo.RestrictedZone
		for typ, c := range zoneCodes {
			if c == code {
				z.Type = typ
			}
		}
		if z.Type == "" {
			return nil, fmt.Errorf("zone %d: unknown type %q", i, code)
		}
		n := make([]float64, len(raw)-1)
		for j := range n {
			if err := json.Unmarshal(raw[j+1], &n[j]); err != nil {
				return nil, fmt.Errorf("zone %d point: %w", i, err)
			}
		}
		pt := func(x, y float64) valetudo.RestrictionPoint { return valetudo.RestrictionPoint{X: x, Y: y} }
		if len(n) == 4 {
			z.Points.PA, z.Points.PB, z.Points.PC, z.Points.PD = pt(n[0], n[1]), pt(n[2], n[1]), pt(n[2], n[3]),
				pt(n[0], n[3])
		} else {
			z.Points.PA, z.Points.PB, z.Points.PC, z.Points.PD = pt(n[0], n[1]), pt(n[2], n[3]), pt(n[4], n[5]),
				pt(n[6], n[7])
		}
		r.RestrictedZones = append(r.RestrictedZones, z)
	}
	return r, nil
}

// fenceStoreMax is the longest value the helper takes: its max attribute, at most 255.
func fenceStoreMax(attrs map[string]interface{}) int {
	max := fenceStoreDefaultMax
	if v, ok := numberAttr(attrs, "max"); ok && v > 0 {
		max = int(v)
	}
	if max > fenceStoreMaxLen {
		max = fenceStoreMaxLen
	}
	return max
}

// helperUnusable reports whether a helper state carries no value Home Assistant knows.
func helperUnusable(state string) bool {
	return state == "unknown" || state == "unavailable"
}

// ---------------------------------------------------------------------------
// Fencing a run and restoring the owner's set
// ---------------------------------------------------------------------------

func (s *Scheduler) readRestrictions() (*valetudo.Restrictions, error) {
	var r *valetudo.Restrictions
	err := s.withCommandTimeout(func(ctx context.Context) error {
		var err error
		r, err = s.robot.Restrictions(ctx)
		return err
	})
	return r, err
}

func (s *Scheduler) putRestrictions(r valetudo.Restrictions) error {
	return s.withCommandTimeout(func(ctx context.Context) error { return s.robot.SetRestrictions(ctx, r) })
}

// verifyRestrictions reads the restrictions back until they match want.
func (s *Scheduler) verifyRestrictions(want valetudo.Restrictions) error {
	var got *valetudo.Restrictions
	var err error
	for i := 0; i < fenceVerifyTries; i++ {
		if i > 0 {
			select {
			case <-s.ctx.Done():
				return s.ctx.Err()
			case <-time.After(s.fenceVerifyDelay):
			}
		}
		got, err = s.readRestrictions()
		if err == nil && sameRestrictions(*got, want) {
			return nil
		}
	}
	if err != nil {
		return fmt.Errorf("read back: %w", err)
	}
	return fmt.Errorf("read back %d walls and %d zones, want %d walls and %d zones", len(got.VirtualWalls),
		len(got.RestrictedZones), len(want.VirtualWalls), len(want.RestrictedZones))
}

// setFenceStore writes the helper.
func (s *Scheduler) setFenceStore(value string) error {
	return s.write("set "+fenceEntity, func() error {
		return s.haClient.CallService(s.ctx, "input_text", "set_value", map[string]interface{}{
			"entity_id": fenceEntity,
			"value":     value,
		})
	})
}

// fenceFrame is the house map's valetudo frame when fencing can use it, else nil.
func (s *Scheduler) fenceFrame() *housemap.Valetudo {
	if s.houseMap == nil {
		return nil
	}
	return s.houseMap.Valetudo
}

// fenceForRun walls off the doors into no_enter rooms before our run starts. It returns the doors it walled,
// or nil when nothing needed a wall or fencing is off. An error means the run must not start; the owner's set
// is then back on the robot, or waiting to be put back. Only an active, writable scheduler calls it. The
// caller holds tickMu.
//
// The order is: read the robot's set, save the owner's set to the helper, write the owner's set plus the
// scheduler's walls, and read it back.
func (s *Scheduler) fenceForRun(p Plan) ([]string, error) {
	v := s.fenceFrame()
	if !s.cfg.FenceEnabled() || v == nil {
		return nil, nil // fenceWarnings says why
	}
	st, ok := s.getState(fenceEntity)
	if !ok {
		return nil, nil // fenceWarnings says why
	}
	// Scheduler walls that may still be up could cut this run's way, so they block a start even when this
	// run needs no wall. Until the helper was read at startup, nobody knows.
	if !s.fence.checked {
		return nil, fmt.Errorf("%s could not be read since startup (%s); saved virtual restrictions may "+
			"still be waiting to be put back", fenceEntity, st.State)
	}
	if s.fence.pending {
		return nil, errors.New("the owner's virtual restrictions from an earlier run are not restored yet")
	}

	restr := ComputeRestrictions(s.graph, s.blockedAreas(), s.sleeperRooms())
	walls, unplaced := fenceWalls(s.houseMap, s.graph, s.dockRoom, restr.NoEnter, s.cfg.FenceMargin())
	for _, d := range unplaced {
		s.warn("fence:unplaced:"+d, "door "+d+" leads into a no-enter room but has no span in the house map; "+
			"it cannot be fenced")
	}
	if len(walls) == 0 {
		return nil, nil
	}
	// The helper matters only when walls are needed.
	if helperUnusable(st.State) {
		return nil, fmt.Errorf("%s is %s", fenceEntity, st.State)
	}
	if strings.TrimSpace(st.State) != "" {
		return nil, fmt.Errorf("%s still holds saved restrictions; they are put back before a new run",
			fenceEntity)
	}
	if err := checkFenceKeepsRoute(s.houseMap, s.graph, s.dockRoom, p.Rooms, walls); err != nil {
		return nil, err
	}

	var m *valetudo.Map
	if err := s.withCommandTimeout(func(ctx context.Context) error {
		var err error
		m, err = s.robot.Map(ctx)
		return err
	}); err != nil {
		return nil, fmt.Errorf("read the robot map: %w", err)
	}
	if problem := s.frameProblem(m); problem != "" {
		return nil, errors.New(problem)
	}
	ours := wallsToValetudo(v, m.PixelSize, walls)

	cur, err := s.readRestrictions()
	if err != nil {
		return nil, fmt.Errorf("read the robot's virtual restrictions: %w", err)
	}
	all := allDoorWalls(s.houseMap, s.graph, s.cfg.FenceMargin())
	allCm := wallsToValetudo(v, m.PixelSize, all)
	owner, stale := stripWalls(*cur, allCm)
	if stale > 0 {
		s.logger.Warn("Vacuum found scheduler walls left on the robot; they are not saved as the owner's",
			zap.Int("walls", stale), zap.Strings("doors", matchedDoors(*cur, all, allCm)))
	}
	value, err := encodeFence(owner)
	if err != nil {
		return nil, err
	}
	if max := fenceStoreMax(st.Attributes); len(value) > max {
		return nil, fmt.Errorf("the owner's virtual restrictions need %d characters; %s holds %d (set its "+
			"maximum length to 255)", len(value), fenceEntity, max)
	}
	if err := s.setFenceStore(value); err != nil {
		return nil, fmt.Errorf("save the owner's virtual restrictions: %w", err)
	}
	s.fence.pending, s.fence.owner = true, &owner

	want := withWalls(owner, ours)
	if err := s.putRestrictions(want); err != nil {
		s.undoFence()
		return nil, fmt.Errorf("write virtual walls: %w", err)
	}
	if err := s.verifyRestrictions(want); err != nil {
		s.undoFence()
		return nil, fmt.Errorf("virtual walls did not take: %w", err)
	}
	doors := make([]string, 0, len(walls))
	for i, w := range walls {
		doors = append(doors, w.door)
		s.logger.Info("Vacuum fenced a no-enter room", zap.String("door", w.door), zap.String("from", w.from),
			zap.String("into", w.into), zap.String("why", restr.NoEnter[w.into]), zap.Any("wall_cm", ours[i].Points))
	}
	s.logger.Info("Vacuum virtual walls set", zap.Strings("doors", doors),
		zap.Int("owner_walls", len(owner.VirtualWalls)), zap.Int("owner_zones", len(owner.RestrictedZones)))
	return doors, nil
}

// undoFence puts the owner's set back after a start failed. If that fails too, it stays pending and is tried
// again on every tick with no run.
func (s *Scheduler) undoFence() {
	if err := s.restoreFence(); err != nil {
		s.fence.problem = "could not restore the owner's virtual restrictions: " + err.Error()
		s.logger.Error("Vacuum could not restore the owner's virtual restrictions; will try again", zap.Error(err))
	}
}

// logWouldFence logs the walls a log_only scheduler would set. Like a real start, it reads the robot map for
// its pixel size and to check the frame. It never reads or writes the restrictions. The caller holds tickMu.
func (s *Scheduler) logWouldFence(p Plan) {
	v := s.fenceFrame()
	if !s.cfg.FenceEnabled() || v == nil {
		return
	}
	restr := ComputeRestrictions(s.graph, s.blockedAreas(), s.sleeperRooms())
	walls, unplaced := fenceWalls(s.houseMap, s.graph, s.dockRoom, restr.NoEnter, s.cfg.FenceMargin())
	if len(walls) == 0 && len(unplaced) == 0 {
		return
	}
	var m *valetudo.Map
	if err := s.withCommandTimeout(func(ctx context.Context) error {
		var err error
		m, err = s.robot.Map(ctx)
		return err
	}); err != nil {
		s.logger.Warn("Vacuum would not start: cannot read the robot map to place the walls", zap.Error(err))
		return
	}
	if problem := s.frameProblem(m); problem != "" {
		s.logger.Warn("Vacuum would not start: the walls cannot be placed", zap.String("problem", problem))
		return
	}
	ours := wallsToValetudo(v, m.PixelSize, walls)
	for i, w := range walls {
		s.logger.Info("Vacuum would fence a no-enter room", zap.String("mode", s.cfg.Mode),
			zap.String("door", w.door), zap.String("from", w.from), zap.String("into", w.into),
			zap.String("why", restr.NoEnter[w.into]), zap.Any("wall_cm", ours[i].Points))
	}
	for _, d := range unplaced {
		s.logger.Warn("Vacuum cannot fence a door into a no-enter room: it has no span", zap.String("door", d))
	}
	if len(walls) > 0 {
		if err := checkFenceKeepsRoute(s.houseMap, s.graph, s.dockRoom, p.Rooms, walls); err != nil {
			s.logger.Warn("Vacuum would not start: the walls would cut the planned way", zap.Error(err))
		}
	}
}

// settleFence runs on every tick with no run tracked. At the first one it reads the helper; then it puts the
// owner's set back while scheduler walls may be on the robot. Only an active, writable scheduler touches the
// robot or the helper. The caller holds tickMu.
func (s *Scheduler) settleFence() {
	if s.cfg.Mode != ModeActive || s.readOnly {
		return
	}
	if !s.fence.checked {
		st, ok := s.getState(fenceEntity)
		switch {
		case !ok:
			// No helper: the scheduler never fenced, because fencing needs it.
			s.fence.checked = true
		case helperUnusable(st.State):
			return // ask again on the next tick; nothing starts meanwhile
		case strings.TrimSpace(st.State) == "":
			// Nothing saved. Still take away any scheduler wall, in case a save was lost. Scheduler walls are
			// found by their geometry, which needs the frame. Without a frame the scheduler never set a wall,
			// so there is nothing to take away.
			s.fence.checked = true
			s.fence.pending = s.fenceFrame() != nil
		default:
			s.fence.checked, s.fence.pending = true, true
			owner, err := decodeFence(strings.TrimSpace(st.State))
			if err != nil {
				s.logger.Error("Vacuum cannot read the saved virtual restrictions; taking away scheduler walls only",
					zap.String("entity_id", fenceEntity), zap.Error(err))
			} else {
				s.fence.owner = owner
				s.logger.Info("Vacuum found saved virtual restrictions; putting them back",
					zap.Int("walls", len(owner.VirtualWalls)), zap.Int("zones", len(owner.RestrictedZones)))
			}
		}
	}
	if !s.fence.pending {
		return
	}
	if err := s.restoreFence(); err != nil {
		s.fence.problem = "could not restore the owner's virtual restrictions: " + err.Error()
		s.logger.Error("Vacuum could not restore the owner's virtual restrictions; will try again", zap.Error(err))
	}
}

// restoreFence puts the owner's set back and clears the helper. Without a known owner's set it takes away the
// walls that match the scheduler's door walls and keeps everything else. It writes only when the robot's set
// differs. The caller holds tickMu.
func (s *Scheduler) restoreFence() error {
	cur, err := s.readRestrictions()
	if err != nil {
		return fmt.Errorf("read the robot's virtual restrictions: %w", err)
	}
	var target valetudo.Restrictions
	if s.fence.owner != nil {
		target = *s.fence.owner
	} else {
		v := s.fenceFrame()
		if v == nil {
			return errors.New("house map has no valetudo frame to find scheduler walls by")
		}
		// The walls are rebuilt with the house map's pixel size, as the scheduler drew them: fencing refuses
		// to start unless the robot map has the same size. If the robot made a new map since, nothing matches
		// and nothing is taken away; the walls then have to be removed by hand.
		var n int
		all := allDoorWalls(s.houseMap, s.graph, s.cfg.FenceMargin())
		allCm := wallsToValetudo(v, v.PixelSizeM*100, all)
		target, n = stripWalls(*cur, allCm)
		if n > 0 {
			// Named, so an owner's wall drawn exactly on a scheduler wall can be found and drawn again.
			s.logger.Warn("Vacuum found scheduler walls with no saved copy; taking them away", zap.Int("walls", n),
				zap.Strings("doors", matchedDoors(*cur, all, allCm)))
		}
	}
	changed := !sameRestrictions(*cur, target)
	if changed {
		if err := s.putRestrictions(target); err != nil {
			return fmt.Errorf("write virtual restrictions: %w", err)
		}
		if err := s.verifyRestrictions(target); err != nil {
			return err
		}
	}
	// Clear the helper whatever the cache shows: the save at the start of a short run may not have come back
	// from Home Assistant yet, and a copy left behind would be put back over the owner's later changes.
	if _, ok := s.getState(fenceEntity); ok {
		if err := s.setFenceStore(""); err != nil {
			return fmt.Errorf("clear %s: %w", fenceEntity, err)
		}
	}
	if changed {
		s.logger.Info("Vacuum restored the owner's virtual restrictions",
			zap.Int("walls", len(target.VirtualWalls)), zap.Int("zones", len(target.RestrictedZones)))
	}
	s.fence = fenceState{checked: true}
	return nil
}

// fenceWarnings keeps fencing problems in shadow state for as long as they hold. The caller holds tickMu.
func (s *Scheduler) fenceWarnings() {
	if s.reason != "" {
		return
	}
	if s.cfg.FenceEnabled() && s.cfg.Mode == ModeActive {
		if st, ok := s.getState(fenceEntity); !ok {
			s.warn("fence:helper", "missing helper "+fenceEntity+"; no-enter rooms are not fenced with virtual walls")
		} else if helperUnusable(st.State) {
			s.warn("fence:helper", fenceEntity+" is "+st.State+"; a run that needs virtual walls does not start")
		}
		if s.fenceFrame() == nil {
			s.warn("fence:frame", "house map has no valetudo frame; no-enter rooms are not fenced with virtual walls")
		}
	}
	if s.cfg.Mode != ModeActive || s.readOnly {
		// Only an active, writable scheduler puts a saved set back. Say so if one is waiting.
		if st, ok := s.getState(fenceEntity); ok && !helperUnusable(st.State) && strings.TrimSpace(st.State) != "" {
			s.warn("fence:pending", fenceEntity+" holds the owner's saved virtual restrictions; scheduler walls "+
				"may still be on the robot. They are put back in active mode, or restore them by hand")
		}
	}
	if s.fence.problem != "" {
		s.warn("fence:problem", s.fence.problem)
	}
}
