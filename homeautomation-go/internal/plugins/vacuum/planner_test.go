package vacuum

import (
	"testing"

	"homeautomation/internal/housemap"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTestMap(t *testing.T) *housemap.Map {
	t.Helper()
	m, err := housemap.Load("testdata/house_map.yaml")
	require.NoError(t, err)
	require.NotNil(t, m)
	return m
}

var testSegments = map[string]string{
	"mud": "1", "pantry": "2", "kitchen": "3", "office": "4", "bed": "5", "bath": "6", "lounge": "7",
}

func full(room string, score, cost float64) PlanItem {
	return PlanItem{Room: room, Treatment: TreatmentFull, Score: score, Cost: cost}
}

func vac(room string, score, cost float64) PlanItem {
	return PlanItem{Room: room, Treatment: TreatmentVacuum, Score: score, Cost: cost}
}

func baseInput(g RoomGraph, items ...PlanItem) PlanInput {
	return PlanInput{
		Graph: g, DockRoom: "mud", Items: items, Segments: testSegments,
		BlockedAreas: map[string]bool{}, Battery: 100, HasBattery: true, Docked: true,
		TripCost: 5, StartMin: 70, Floor: 30,
	}
}

func skipReason(p Plan, room string, tr Treatment) string {
	for _, s := range p.Skipped {
		if s.Room == room && s.Treatment == tr {
			return s.Reason
		}
	}
	return ""
}

func TestBuildPlan(t *testing.T) {
	g := loadTestMap(t).Graph()

	tests := []struct {
		name   string
		mutate func(in *PlanInput)
		items  []PlanItem
		rooms  []string
		treat  Treatment
		reason string            // plan reason when no run
		skips  map[string]string // "room/treatment" -> substring of reason
	}{
		{
			name:   "nothing dirty",
			reason: "nothing is dirty",
		},
		{
			name:   "rule 3: below start minimum",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.Battery = 69 },
			reason: "battery 69% is below start minimum 70%",
			skips:  map[string]string{"kitchen/full": "below start minimum"},
		},
		{
			name:   "rule 3: exactly the start minimum may start",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.Battery = 70 },
			rooms:  []string{"kitchen"},
			treat:  TreatmentFull,
		},
		{
			name:   "battery unknown",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.HasBattery = false },
			reason: "battery level unknown",
		},
		{
			name:   "not docked",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.Docked = false },
			reason: "robot is not docked",
		},
		{
			name:   "rule 1: dock room occupied",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.BlockedAreas["mud"] = true },
			reason: "dock room mud: area mud is occupied or unknown",
		},
		{
			name:   "dock room not in map",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.DockRoom = "garage" },
			reason: "not in the house map",
		},
		{
			name:  "everything clear: nearest-neighbour order",
			items: []PlanItem{full("kitchen", 3, 5), full("office", 2, 5), full("pantry", 1, 5)},
			rooms: []string{"office", "pantry", "kitchen"},
			treat: TreatmentFull,
		},
		{
			name:   "rule 1: occupied room is not cleaned",
			items:  []PlanItem{full("kitchen", 3, 5), full("office", 2, 5)},
			mutate: func(in *PlanInput) { in.BlockedAreas["kitchen"] = true },
			rooms:  []string{"office"},
			treat:  TreatmentFull,
			skips:  map[string]string{"kitchen/full": "area kitchen is occupied or unknown"},
		},
		{
			name:   "rule 1: person in the pantry blocks every room behind it",
			items:  []PlanItem{full("kitchen", 3, 5), full("lounge", 2, 5), full("bath", 2, 5), full("office", 1, 5)},
			mutate: func(in *PlanInput) { in.BlockedAreas["pantry"] = true },
			rooms:  []string{"office"},
			treat:  TreatmentFull,
			skips: map[string]string{
				"kitchen/full": "way from the dock enters pantry",
				"lounge/full":  "way from the dock enters pantry",
				"bath/full":    "way from the dock enters pantry",
			},
		},
		{
			// The bed can be reached through the office, but the robot's shortest way is through the pantry.
			name:   "rule 1: the shortest way counts, not any safe way",
			items:  []PlanItem{full("bed", 3, 5)},
			mutate: func(in *PlanInput) { in.BlockedAreas["pantry"] = true },
			reason: "no dirty room may be cleaned now",
			skips:  map[string]string{"bed/full": "way from the dock enters pantry"},
		},
		{
			name: "rule 2: sleeper room and its neighbours are not cleaned, but the pantry is driven through",
			items: []PlanItem{full("bed", 5, 5), full("pantry", 4, 5), full("bath", 4, 5), full("office", 4, 5),
				full("kitchen", 1, 5)},
			mutate: func(in *PlanInput) { in.SleeperRooms = []string{"bed"} },
			rooms:  []string{"kitchen"},
			treat:  TreatmentFull,
			skips: map[string]string{
				"bed/full":    "sleeper room",
				"pantry/full": "next to sleeper room bed",
				"bath/full":   "next to sleeper room bed",
				"office/full": "next to sleeper room bed",
			},
		},
		{
			// The pantry's area is clear, but someone sleeps there. The bath is two doors from the pantry, so
			// it may be cleaned, but its shortest way crosses the pantry.
			name:   "rule 1: the shortest way may not cross a sleeper's room, even when its area is clear",
			items:  []PlanItem{full("bath", 3, 5), full("office", 1, 5)},
			mutate: func(in *PlanInput) { in.SleeperRooms = []string{"pantry"} },
			rooms:  []string{"office"},
			treat:  TreatmentFull,
			skips:  map[string]string{"bath/full": "way from the dock enters pantry (sleeper room)"},
		},
		{
			name:   "rule 1: no run when someone sleeps in the dock room",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.SleeperRooms = []string{"mud"} },
			reason: "dock room mud: sleeper room",
		},
		{
			name:   "occupied wins over sleeper neighbour as reason",
			items:  []PlanItem{full("pantry", 2, 5)},
			mutate: func(in *PlanInput) { in.SleeperRooms = []string{"bed"}; in.BlockedAreas["pantry"] = true },
			reason: "no dirty room may be cleaned now",
			skips:  map[string]string{"pantry/full": "area pantry is occupied or unknown"},
		},
		{
			name: "rule 3: greedy packing in score order skips what does not fit",
			// Budget 80-30 = 50. Trip 5. kitchen 20 -> 25, lounge 20 -> 45, office 10 does not fit, mud 5 fits.
			items: []PlanItem{full("kitchen", 4, 20), full("lounge", 3, 20), full("office", 2, 10),
				full("mud", 1, 5)},
			mutate: func(in *PlanInput) { in.Battery = 80 },
			rooms:  []string{"mud", "lounge", "kitchen"},
			treat:  TreatmentFull,
			skips:  map[string]string{"office/full": "does not fit battery: needs 10.0%, 5.0% left"},
		},
		{
			name:   "rule 3: trip alone does not fit",
			items:  []PlanItem{full("kitchen", 2, 1)},
			mutate: func(in *PlanInput) { in.Battery = 72; in.TripCost = 43 },
			reason: "trip cost 43.0% does not fit battery budget 42.0%",
			skips:  map[string]string{"kitchen/full": "trip cost"},
		},
		{
			name:   "rule 3: exact fit is allowed",
			items:  []PlanItem{full("kitchen", 2, 45)},
			mutate: func(in *PlanInput) { in.Battery = 80 },
			rooms:  []string{"kitchen"},
			treat:  TreatmentFull,
		},
		{
			name:  "best item sets the treatment: vacuum run skips full items",
			items: []PlanItem{vac("kitchen", 5, 3), full("office", 2, 5), vac("lounge", 1, 3)},
			rooms: []string{"lounge", "kitchen"},
			treat: TreatmentVacuum,
			skips: map[string]string{"office/full": "this run is vacuum"},
		},
		{
			name:  "full run clears the vacuum taint of its rooms for free",
			items: []PlanItem{full("kitchen", 5, 10), vac("kitchen", 4, 3), vac("lounge", 3, 3)},
			rooms: []string{"kitchen"},
			treat: TreatmentFull,
			skips: map[string]string{"lounge/vacuum": "this run is full"},
		},
		{
			name:   "room without segment id",
			items:  []PlanItem{full("kitchen", 2, 5)},
			mutate: func(in *PlanInput) { in.Segments = map[string]string{} },
			reason: "no dirty room may be cleaned now",
			skips:  map[string]string{"kitchen/full": "no segment id"},
		},
		{
			name:   "room not in map",
			items:  []PlanItem{full("attic", 2, 5)},
			mutate: func(in *PlanInput) { in.Segments = map[string]string{"attic": "9"} },
			reason: "no dirty room may be cleaned now",
			skips:  map[string]string{"attic/full": "not in the house map"},
		},
		{
			name:   "unknown area counts as blocked (caller passes it in BlockedAreas)",
			items:  []PlanItem{full("office", 2, 5)},
			mutate: func(in *PlanInput) { in.BlockedAreas["office"] = true },
			reason: "no dirty room may be cleaned now",
			skips:  map[string]string{"office/full": "occupied or unknown"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInput(g, tt.items...)
			if tt.mutate != nil {
				tt.mutate(&in)
			}
			p := BuildPlan(in)
			assert.Equal(t, tt.rooms, p.Rooms)
			assert.Equal(t, tt.treat, p.Treatment)
			if tt.reason != "" {
				assert.Contains(t, p.Reason, tt.reason)
				assert.True(t, p.Empty())
				assert.Equal(t, "", p.Key())
			} else {
				assert.Empty(t, p.Reason)
			}
			for key, want := range tt.skips {
				var room string
				var tr Treatment
				for i := range key {
					if key[i] == '/' {
						room, tr = key[:i], Treatment(key[i+1:])
					}
				}
				assert.Contains(t, skipReason(p, room, tr), want, key)
			}
			// Every dirty item is either in the run or has a reason.
			covered := map[string]bool{}
			for _, it := range p.Items {
				covered[it.Room+"/"+string(it.Treatment)] = true
			}
			for _, s := range p.Skipped {
				k := s.Room + "/" + string(s.Treatment)
				assert.False(t, covered[k], "%s is both run and skipped", k)
				assert.NotEmpty(t, s.Reason)
				covered[k] = true
			}
			for _, it := range tt.items {
				assert.True(t, covered[it.Room+"/"+string(it.Treatment)], "%s/%s has no outcome", it.Room, it.Treatment)
			}
		})
	}
}

func TestBuildPlanResultDetails(t *testing.T) {
	g := loadTestMap(t).Graph()
	in := baseInput(g, full("kitchen", 5, 10), vac("kitchen", 4, 3), full("office", 2, 8))
	in.SleeperRooms = []string{"bath"}
	p := BuildPlan(in)

	assert.Equal(t, []string{"office", "kitchen"}, p.Rooms)
	assert.Equal(t, []string{"4", "3"}, p.Segments)
	assert.Equal(t, "full:office,kitchen", p.Key())
	assert.InDelta(t, 23.0, p.Cost, 1e-9, "trip 5 + kitchen 10 + office 8; the free vacuum item adds nothing")
	assert.InDelta(t, 70.0, p.Budget, 1e-9)
	assert.Equal(t, []string{"bath", "bed"}, p.NoClean)
	assert.Equal(t, []string{"bath"}, p.NoEnter, "a sleeper's room is not entered")
	require.Len(t, p.Items, 3)
	assert.Equal(t, PlanItem{Room: "kitchen", Treatment: TreatmentVacuum, Score: 4, Cost: 0}, p.Items[2])
}

func TestBuildPlanIsPure(t *testing.T) {
	g := loadTestMap(t).Graph()
	items := []PlanItem{full("office", 1, 5), full("kitchen", 3, 5)}
	in := baseInput(g, items...)
	first := BuildPlan(in)
	second := BuildPlan(in)
	assert.Equal(t, first, second)
	assert.Equal(t, "office", items[0].Room, "input items are not reordered")
}

// fakeGraph lets a test control shortest paths directly.
type fakeGraph struct {
	rooms     []string
	areas     map[string]string
	neighbors map[string][]string
	paths     map[[2]string][]string
}

func (f *fakeGraph) Rooms() []string { return f.rooms }
func (f *fakeGraph) Has(room string) bool {
	for _, r := range f.rooms {
		if r == room {
			return true
		}
	}
	return false
}
func (f *fakeGraph) Neighbors(room string) []string { return f.neighbors[room] }
func (f *fakeGraph) Area(room string) string        { return f.areas[room] }
func (f *fakeGraph) ShortestPath(from, to string, _ map[string]bool) ([]string, float64, bool) {
	if from == to {
		return []string{from}, 0, true
	}
	if p, ok := f.paths[[2]string{from, to}]; ok {
		return p, float64(len(p)), true
	}
	if p, ok := f.paths[[2]string{to, from}]; ok {
		rev := make([]string, len(p))
		for i := range p {
			rev[i] = p[len(p)-1-i]
		}
		return rev, float64(len(p)), true
	}
	return nil, 0, false
}

// TestOrderTourDropsRoomWhoseLegIsBlocked covers a room that is safe from the dock but whose way from the
// previous room in the tour crosses a blocked room.
func TestOrderTourDropsRoomWhoseLegIsBlocked(t *testing.T) {
	g := &fakeGraph{
		rooms: []string{"dock", "a", "b", "x"},
		areas: map[string]string{"dock": "d", "a": "a", "b": "b", "x": "x"},
		paths: map[[2]string][]string{
			{"dock", "a"}: {"dock", "a"},
			{"dock", "b"}: {"dock", "b", "b", "b"}, // farther than a, so a comes first
			{"a", "b"}:    {"a", "x", "b"},
			{"dock", "x"}: {"dock", "x"},
		},
	}
	in := PlanInput{
		Graph: g, DockRoom: "dock", Segments: map[string]string{"a": "1", "b": "2"},
		Items:        []PlanItem{full("a", 2, 1), full("b", 1, 1)},
		BlockedAreas: map[string]bool{"x": true}, Battery: 100, HasBattery: true, Docked: true,
		StartMin: 70, Floor: 30,
	}
	p := BuildPlan(in)
	assert.Equal(t, []string{"a"}, p.Rooms)
	assert.Contains(t, skipReason(p, "b", TreatmentFull), "way from a to b enters x")
}

func TestOrderTourDropsRoomWithBlockedWayHome(t *testing.T) {
	g := &fakeGraph{
		rooms: []string{"dock", "a", "x"},
		areas: map[string]string{"dock": "d", "a": "a", "x": "x"},
		paths: map[[2]string][]string{{"dock", "a"}: {"dock", "a"}},
	}
	// The way out is clear, but the way home is not: a one-way quirk only a fake graph can have.
	g.paths[[2]string{"a", "dock"}] = []string{"a", "x", "dock"}
	in := PlanInput{
		Graph: g, DockRoom: "dock", Segments: map[string]string{"a": "1"},
		Items:        []PlanItem{full("a", 2, 1)},
		BlockedAreas: map[string]bool{"x": true}, Battery: 100, HasBattery: true, Docked: true,
		StartMin: 70, Floor: 30,
	}
	p := BuildPlan(in)
	assert.True(t, p.Empty())
	assert.Equal(t, "no dirty room fits a safe tour", p.Reason)
	assert.Contains(t, skipReason(p, "a", TreatmentFull), "way from a to dock enters x")
}

func TestComputeRestrictions(t *testing.T) {
	g := loadTestMap(t).Graph()
	r := ComputeRestrictions(g, map[string]bool{"suite": true}, []string{"office"})
	assert.Equal(t, map[string]string{
		"bed":    "area suite is occupied or unknown",
		"bath":   "area suite is occupied or unknown",
		"office": "sleeper room",
	}, r.NoEnter, "a sleeper's room is not entered, even when its area is clear")
	assert.Equal(t, "sleeper room", r.NoClean["office"])
	_, neighbourBlocked := r.NoEnter["mud"]
	assert.False(t, neighbourBlocked, "a sleeper's neighbour may be driven through")
	assert.Equal(t, "next to sleeper room office", r.NoClean["mud"])
	assert.Equal(t, "area suite is occupied or unknown", r.NoClean["bed"], "occupied is the stronger reason")
	_, ok := r.NoClean["kitchen"]
	assert.False(t, ok)
}

func TestLegCrosses(t *testing.T) {
	g := loadTestMap(t).Graph()
	noEnter := map[string]string{"pantry": "pantry"}
	crossed, ok := LegCrosses(g, "mud", "kitchen", noEnter)
	assert.True(t, ok)
	assert.Equal(t, "pantry", crossed)
	crossed, ok = LegCrosses(g, "pantry", "mud", noEnter)
	assert.True(t, ok)
	assert.Equal(t, "", crossed, "the start room is exempt")
	_, ok = LegCrosses(g, "mud", "attic", noEnter)
	assert.False(t, ok)
}
