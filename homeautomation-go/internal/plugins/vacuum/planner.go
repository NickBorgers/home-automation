package vacuum

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// RoomGraph is the part of housemap.Graph the planner uses.
type RoomGraph interface {
	Rooms() []string
	Has(room string) bool
	Neighbors(room string) []string
	Area(room string) string
	ShortestPath(from, to string, avoid map[string]bool) (rooms []string, metres float64, ok bool)
}

// PlanItem is one dirty room and treatment.
type PlanItem struct {
	Room      string
	Treatment Treatment
	// Score ranks items: time since clean divided by interval. Manual taints score high.
	Score  float64
	Manual bool
	// Cost is the battery percent to clean the room with this treatment.
	Cost float64
}

// PlanInput is everything the planner looks at. The planner does not read anything else.
type PlanInput struct {
	Graph RoomGraph
	// DockRoom is where the robot starts and ends.
	DockRoom string
	// Items are the dirty items.
	Items []PlanItem
	// Segments maps a cleanable room to its Valetudo segment id.
	Segments map[string]string
	// BlockedAreas are areas that are occupied or whose occupancy is not known.
	BlockedAreas map[string]bool
	// SleeperRooms are the rooms of people who are asleep now.
	SleeperRooms []string

	Battery    int
	HasBattery bool
	Docked     bool

	TripCost float64
	StartMin float64
	Floor    float64
}

// Skip says why an item is not in the run.
type Skip struct {
	Room      string
	Treatment Treatment
	Reason    string
}

// Plan is the planner's answer. A plan with no rooms is "no run"; Reason says why.
type Plan struct {
	Treatment Treatment
	// Rooms are in the order the robot should clean them.
	Rooms    []string
	Segments []string
	// Items are the items the run clears: the packed items, plus vacuum items of rooms a full run cleans.
	Items []PlanItem
	// Cost is trip plus room costs. Budget is battery minus floor.
	Cost   float64
	Budget float64
	Reason string
	// Skipped has a reason for every dirty item left out.
	Skipped []Skip
	// NoEnter and NoClean are sorted, for shadow state.
	NoEnter []string
	NoClean []string
}

// Empty reports whether the plan starts no run.
func (p Plan) Empty() bool { return len(p.Rooms) == 0 }

// Key identifies a run: same treatment and rooms in the same order.
func (p Plan) Key() string {
	if p.Empty() {
		return ""
	}
	return string(p.Treatment) + ":" + strings.Join(p.Rooms, ",")
}

// Restrictions are the rooms the robot must not enter and must not clean.
type Restrictions struct {
	// NoEnter maps a room the robot must not enter to the reason: its area is blocked, or someone sleeps
	// there.
	NoEnter map[string]string
	// NoClean maps a room to the reason it must not be cleaned. It holds NoEnter too.
	NoClean map[string]string
}

// ComputeRestrictions applies rules 1 and 2. A room in a blocked area must not be entered (rule 1). A
// sleeper's room holds a person, so it must not be entered either, whatever room presence says. Every room
// one door away from a sleeper must not be cleaned, but may be driven through (rule 2).
func ComputeRestrictions(g RoomGraph, blockedAreas map[string]bool, sleeperRooms []string) Restrictions {
	r := Restrictions{NoEnter: map[string]string{}, NoClean: map[string]string{}}
	for _, room := range g.Rooms() {
		if a := g.Area(room); blockedAreas[a] {
			r.NoEnter[room] = "area " + a + " is occupied or unknown"
		}
	}
	sleepers := append([]string(nil), sleeperRooms...)
	sort.Strings(sleepers)
	for _, s := range sleepers {
		for _, n := range g.Neighbors(s) {
			if _, ok := r.NoClean[n]; !ok {
				r.NoClean[n] = "next to sleeper room " + s
			}
		}
	}
	for _, s := range sleepers {
		if _, blocked := r.NoEnter[s]; !blocked {
			r.NoEnter[s] = "sleeper room"
		}
	}
	for room, why := range r.NoEnter {
		r.NoClean[room] = why
	}
	return r
}

// LegCrosses returns the first room in noEnter on the shortest way from one room to another, or "" when the
// way is clear. The robot plans its own route, so the planner checks the shortest one, not any safe one.
// The start room is exempt: the robot is already there. ok is false when there is no way at all.
func LegCrosses(g RoomGraph, from, to string, noEnter map[string]string) (crossed string, ok bool) {
	rooms, _, ok := g.ShortestPath(from, to, nil)
	if !ok {
		return "", false
	}
	for _, r := range rooms[1:] {
		if _, blocked := noEnter[r]; blocked {
			return r, true
		}
	}
	return "", true
}

// BuildPlan picks the rooms for one run. It is a pure function of its input.
//
//  1. No run below the start minimum or when the robot is not docked.
//  2. Rooms in blocked areas are not entered; sleeper rooms and their neighbours are not cleaned.
//  3. Candidates are items that may be cleaned and whose shortest way from the dock enters no blocked room.
//  4. The best-scoring candidate sets the run's treatment. A full run also clears the room's vacuum taint.
//  5. Candidates are packed in score order while trip plus room costs fit in battery minus floor.
//  6. Rooms are ordered as a nearest-neighbour tour from the dock. A room whose leg enters a blocked room
//     is dropped.
func BuildPlan(in PlanInput) Plan {
	plan := Plan{Budget: float64(in.Battery) - in.Floor}

	restr := ComputeRestrictions(in.Graph, in.BlockedAreas, in.SleeperRooms)
	plan.NoEnter = sortedKeys(restr.NoEnter)
	plan.NoClean = sortedKeys(restr.NoClean)

	items := append([]PlanItem(nil), in.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Room != items[j].Room {
			return items[i].Room < items[j].Room
		}
		return items[i].Treatment < items[j].Treatment // full before vacuum
	})

	skipAll := func(reason string) Plan {
		plan.Reason = reason
		for _, it := range items {
			plan.Skipped = append(plan.Skipped, Skip{Room: it.Room, Treatment: it.Treatment, Reason: reason})
		}
		return plan
	}

	// Step 1.
	switch {
	case len(items) == 0:
		plan.Reason = "nothing is dirty"
		return plan
	case !in.HasBattery:
		return skipAll("battery level unknown")
	case float64(in.Battery) < in.StartMin:
		return skipAll(fmt.Sprintf("battery %d%% is below start minimum %g%%", in.Battery, in.StartMin))
	case !in.Docked:
		return skipAll("robot is not docked")
	case !in.Graph.Has(in.DockRoom):
		return skipAll(fmt.Sprintf("dock room %q is not in the house map", in.DockRoom))
	}
	if why, blocked := restr.NoEnter[in.DockRoom]; blocked {
		return skipAll(fmt.Sprintf("dock room %s: %s", in.DockRoom, why))
	}

	// Steps 2 and 3.
	var candidates []PlanItem
	for _, it := range items {
		reason := ""
		switch {
		case !in.Graph.Has(it.Room):
			reason = "room is not in the house map"
		case in.Segments[it.Room] == "":
			reason = "room has no segment id"
		}
		if reason == "" {
			if r, bad := restr.NoClean[it.Room]; bad {
				reason = r
			}
		}
		if reason == "" {
			crossed, ok := LegCrosses(in.Graph, in.DockRoom, it.Room, restr.NoEnter)
			switch {
			case !ok:
				reason = "no way from the dock"
			case crossed != "":
				reason = "way from the dock enters " + crossed + " (" + restr.NoEnter[crossed] + ")"
			}
		}
		if reason != "" {
			plan.Skipped = append(plan.Skipped, Skip{Room: it.Room, Treatment: it.Treatment, Reason: reason})
			continue
		}
		candidates = append(candidates, it)
	}
	if len(candidates) == 0 {
		plan.Reason = "no dirty room may be cleaned now"
		return plan
	}

	// Step 4.
	plan.Treatment = candidates[0].Treatment
	var sameTreatment []PlanItem
	vacuumOf := map[string]PlanItem{}
	for _, it := range candidates {
		switch {
		case it.Treatment == plan.Treatment:
			sameTreatment = append(sameTreatment, it)
		case plan.Treatment == TreatmentFull && it.Treatment == TreatmentVacuum:
			vacuumOf[it.Room] = it // cleared for free if its room is cleaned in full
		default:
			plan.Skipped = append(plan.Skipped, Skip{Room: it.Room, Treatment: it.Treatment,
				Reason: "this run is " + string(plan.Treatment)})
		}
	}

	// Step 5.
	if in.TripCost > plan.Budget {
		plan.Treatment = ""
		plan.Reason = fmt.Sprintf("trip cost %.1f%% does not fit battery budget %.1f%%", in.TripCost, plan.Budget)
		for _, it := range sameTreatment {
			plan.Skipped = append(plan.Skipped, Skip{Room: it.Room, Treatment: it.Treatment, Reason: plan.Reason})
		}
		for _, room := range sortedKeys(vacuumOf) {
			plan.Skipped = append(plan.Skipped, Skip{Room: room, Treatment: TreatmentVacuum, Reason: plan.Reason})
		}
		return plan
	}
	used := in.TripCost
	var packed []PlanItem
	for _, it := range sameTreatment {
		if used+it.Cost > plan.Budget+1e-9 {
			plan.Skipped = append(plan.Skipped, Skip{Room: it.Room, Treatment: it.Treatment,
				Reason: fmt.Sprintf("does not fit battery: needs %.1f%%, %.1f%% left", it.Cost, plan.Budget-used)})
			continue
		}
		used += it.Cost
		packed = append(packed, it)
	}

	// Step 6.
	tour, dropped := orderTour(in.Graph, in.DockRoom, packed, restr.NoEnter)
	for _, d := range dropped {
		plan.Skipped = append(plan.Skipped, Skip{Room: d.item.Room, Treatment: d.item.Treatment, Reason: d.reason})
	}
	inTour := map[string]bool{}
	plan.Cost = in.TripCost
	for _, it := range tour {
		inTour[it.Room] = true
		plan.Rooms = append(plan.Rooms, it.Room)
		plan.Segments = append(plan.Segments, in.Segments[it.Room])
		plan.Items = append(plan.Items, it)
		plan.Cost += it.Cost
	}
	for _, room := range sortedKeys(vacuumOf) {
		v := vacuumOf[room]
		if inTour[room] {
			v.Cost = 0
			plan.Items = append(plan.Items, v)
			continue
		}
		plan.Skipped = append(plan.Skipped, Skip{Room: room, Treatment: TreatmentVacuum,
			Reason: "this run is full and does not include the room"})
	}
	if len(plan.Rooms) == 0 {
		plan.Treatment = ""
		plan.Cost = 0
		plan.Items = nil
		plan.Reason = "no dirty room fits a safe tour"
	}
	return plan
}

type droppedItem struct {
	item   PlanItem
	reason string
}

// orderTour orders items as a nearest-neighbour tour from the dock, then checks every leg, dock to rooms to
// dock. A room whose incoming leg enters a blocked room is dropped, and so is the last room if the way home
// does. The tour is rebuilt until every leg is clear.
func orderTour(g RoomGraph, dock string, items []PlanItem, noEnter map[string]string) ([]PlanItem, []droppedItem) {
	var dropped []droppedItem
	remaining := append([]PlanItem(nil), items...)
	for len(remaining) > 0 {
		tour := nearestNeighbour(g, dock, remaining)
		bad := -1
		reason := ""
		prev := dock
		for i, it := range tour {
			if crossed, ok := LegCrosses(g, prev, it.Room, noEnter); !ok || crossed != "" {
				bad, reason = i, legReason(prev, it.Room, crossed, noEnter[crossed], ok)
				break
			}
			prev = it.Room
		}
		if bad < 0 {
			last := tour[len(tour)-1].Room
			if crossed, ok := LegCrosses(g, last, dock, noEnter); !ok || crossed != "" {
				bad, reason = len(tour)-1, legReason(last, dock, crossed, noEnter[crossed], ok)
			}
		}
		if bad < 0 {
			return tour, dropped
		}
		dropped = append(dropped, droppedItem{item: tour[bad], reason: reason})
		remaining = remaining[:0]
		for i, it := range tour {
			if i != bad {
				remaining = append(remaining, it)
			}
		}
	}
	return nil, dropped
}

func legReason(from, to, crossed, why string, ok bool) string {
	if !ok {
		return fmt.Sprintf("no way from %s to %s", from, to)
	}
	return fmt.Sprintf("way from %s to %s enters %s (%s)", from, to, crossed, why)
}

// nearestNeighbour orders items by always driving to the closest room next. Ties go by room name.
func nearestNeighbour(g RoomGraph, start string, items []PlanItem) []PlanItem {
	left := append([]PlanItem(nil), items...)
	var out []PlanItem
	at := start
	for len(left) > 0 {
		best, bestD := -1, math.Inf(1)
		for i, it := range left {
			_, d, ok := g.ShortestPath(at, it.Room, nil)
			if !ok {
				d = math.Inf(1)
			}
			if best < 0 || d < bestD || (d == bestD && it.Room < left[best].Room) {
				best, bestD = i, d
			}
		}
		out = append(out, left[best])
		at = left[best].Room
		left = append(left[:best], left[best+1:]...)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
