package roompresence

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func exampleConfig(t *testing.T) *Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "example_config.yaml"))
	require.NoError(t, err)
	cfg, err := ParseConfig(data)
	require.NoError(t, err)
	return cfg
}

// scene drives a tracker with a clock measured in seconds from t0.
type scene struct {
	t  *testing.T
	tr *Tracker
}

func newScene(t *testing.T) *scene {
	return &scene{t: t, tr: NewTracker(exampleConfig(t))}
}

func at(sec float64) time.Time { return t0.Add(time.Duration(sec * float64(time.Second))) }

func (s *scene) event(sec float64, typ, id, cam string, entered []string, current ...string) {
	s.tr.HandleEvent(at(sec), FrigateEvent{Type: typ, After: FrigateObject{
		ID: id, Camera: cam, Label: "person", EnteredZones: entered, CurrentZones: current,
	}})
}

// walk sends new, an update in each zone, and end, one second apart, starting at sec. The person is in
// the last zone at the end.
func (s *scene) walk(sec float64, id, cam string, zones ...string) float64 {
	s.event(sec, "new", id, cam, nil)
	for i := range zones {
		sec++
		s.event(sec, "update", id, cam, zones[:i+1], zones[i])
	}
	sec++
	s.event(sec, "end", id, cam, zones, zones[len(zones)-1])
	return sec
}

func (s *scene) tick(sec float64) { s.tr.Tick(at(sec)) }

func (s *scene) occupied(areas ...string) {
	s.t.Helper()
	for _, a := range areas {
		assert.True(s.t, s.tr.Occupied(a), "%s should be occupied", a)
	}
}

func (s *scene) empty(areas ...string) {
	s.t.Helper()
	for _, a := range areas {
		assert.False(s.t, s.tr.Occupied(a), "%s should be empty", a)
	}
}

func (s *scene) tokens(n int) {
	s.t.Helper()
	assert.Len(s.t, s.tr.tokens, n)
}

// seed puts one token in an area, as if a person had been seen there.
func (s *scene) seed(sec float64, area string) {
	s.tr.newToken(at(sec), area, "", "seeded by test", false)
}

func TestConfig_Example(t *testing.T) {
	cfg := exampleConfig(t)
	assert.Equal(t, ModeLogOnly, cfg.Mode)
	assert.Equal(t, 5*time.Second, cfg.HandoffWindow)
	require.NotNil(t, cfg.Areas["hall"].IdleLimit)
	assert.Equal(t, 5*time.Minute, *cfg.Areas["hall"].IdleLimit)
	assert.Nil(t, cfg.Areas["bedroom"].IdleLimit, "absent idle_limit never expires")

	assert.Equal(t, zoneInfo{Kind: zoneRoom, Area: "kitchen"}, cfg.resolveZone("kitchen"))
	assert.Equal(t, zoneInfo{Kind: zoneBoundary, Area: "lounge", Beyond: "study"}, cfg.resolveZone("lounge_to_study"))
	assert.Equal(t, zoneInfo{Kind: zoneRoom, Area: "hall"}, cfg.resolveZone("hall_doormat"))
	assert.Equal(t, zoneUnknown, cfg.resolveZone("garden").Kind)
	assert.Equal(t, zoneUnknown, cfg.resolveZone("lounge_to_garden").Kind)
	assert.True(t, cfg.neighbors("kitchen", "lounge"), "neighbors are symmetric")

	area, beyond, ok := cfg.BoundaryAreas("lounge_to_study")
	assert.True(t, ok)
	assert.Equal(t, []string{"lounge", "study"}, []string{area, beyond})
	_, _, ok = cfg.BoundaryAreas("kitchen")
	assert.False(t, ok, "a room zone is not a boundary")
}

func TestConfig_Defaults(t *testing.T) {
	cfg, err := ParseConfig([]byte("room_presence:\n  areas:\n    den: {seen: true}\n"))
	require.NoError(t, err)
	assert.Equal(t, ModeLogOnly, cfg.Mode)
	assert.Equal(t, defaultHandoffWindow, cfg.HandoffWindow)
	assert.Equal(t, defaultCameraHeartbeatTimeout, cfg.CameraHeartbeatTimeout)
	assert.Equal(t, defaultNobodyHomeDelay, cfg.NobodyHomeDelay)
	assert.Equal(t, defaultExitWindow, cfg.ExitWindow)
	assert.Equal(t, defaultArrivalWindow, cfg.ArrivalWindow)
	assert.Equal(t, defaultApproachLead, cfg.ApproachLead)
}

func TestConfig_Invalid(t *testing.T) {
	cases := map[string]string{
		"bad mode":               "room_presence:\n  mode: loud\n  areas: {den: {seen: true}}\n",
		"no areas":               "room_presence:\n  mode: log_only\n",
		"no section":             "other: {}\n",
		"_to_ in area":           "room_presence:\n  areas: {den_to_hall: {seen: true}}\n",
		"unknown neighbor":       "room_presence:\n  areas: {den: {neighbors: [attic]}}\n",
		"unknown output":         "room_presence:\n  areas: {den: {}}\n  outputs: {isDen: [attic]}\n",
		"empty output":           "room_presence:\n  areas: {den: {}}\n  outputs: {isDen: []}\n",
		"output is input":        "room_presence:\n  areas: {den: {}}\n  outputs: {isNickHome: [den]}\n",
		"bad override":           "room_presence:\n  areas: {den: {}}\n  zone_overrides: {z: attic}\n",
		"unknown camera":         "room_presence:\n  areas: {den: {}}\n  cameras: {c: [attic]}\n",
		"zero idle limit":        "room_presence:\n  areas: {den: {idle_limit: 0s}}\n",
		"not yaml":               "room_presence: [",
		"exit seen":              "room_presence:\n  areas: {den: {seen: true, leads_outside: true, exit_sensors: [b.x]}}\n",
		"exit not outside":       "room_presence:\n  areas: {den: {exit_sensors: [b.x]}}\n",
		"negative exit":          "room_presence:\n  exit_window: -1s\n  areas: {den: {}}\n",
		"approach without entry": "room_presence:\n  areas: {den: {leads_outside: true, exit_sensors: [b.x], approach_sensors: [b.y]}}\n",
		"entry without approach": "room_presence:\n  areas: {den: {leads_outside: true, exit_sensors: [b.x], entry_sensors: [b.z]}}\n",
		"approach without exit":  "room_presence:\n  areas: {den: {leads_outside: true, approach_sensors: [b.y], entry_sensors: [b.z]}}\n",
		"approach is exit":       "room_presence:\n  areas: {den: {leads_outside: true, exit_sensors: [b.x], approach_sensors: [b.x], entry_sensors: [b.z]}}\n",
		"lead past window":       "room_presence:\n  approach_lead: 2m\n  arrival_window: 1m\n  areas: {den: {}}\n",
	}
	for name, yml := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseConfig([]byte(yml))
			assert.Error(t, err)
		})
	}
}

func TestLoadConfig_MissingFileIsUnconfigured(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), ConfigFileName))
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

// Rule 1: a track that starts in a boundary zone came out of the area beyond it.
func TestRule1_BoundaryStartMovesTokenOut(t *testing.T) {
	s := newScene(t)
	s.seed(0, "study")

	s.event(10, "new", "a", "cam_lounge", nil)
	s.event(10.3, "update", "a", "cam_lounge", []string{"lounge_to_study"}, "lounge_to_study")

	s.empty("study")
	s.occupied("lounge")
	s.tokens(1)
	assert.Equal(t, []string{"lounge"}, s.tr.DrainEntries(), "coming out of the study is an entry")
}

func TestRule1_BoundaryStartWithoutTokenCreatesOne(t *testing.T) {
	s := newScene(t)
	s.event(10, "update", "a", "cam_lounge", []string{"lounge_to_study"}, "lounge_to_study")
	s.occupied("lounge")
	s.tokens(1)
}

// A track that ends in a boundary zone to an unseen area went through the doorway.
func TestEnd_BoundaryToUnseenMovesTokenIn(t *testing.T) {
	s := newScene(t)
	s.event(0, "update", "a", "cam_lounge", []string{"lounge_to_study"}, "lounge_to_study")
	s.event(2, "update", "a", "cam_lounge", []string{"lounge_to_study", "lounge"}, "lounge")
	s.event(4, "end", "a", "cam_lounge", []string{"lounge_to_study", "lounge"}, "lounge", "lounge_to_study")

	s.occupied("study")
	s.empty("lounge")
	s.tokens(1)
}

// "When I sit still behind the counter for 10 minutes, the kitchen stays occupied."
func TestEnd_RoomZoneTokenStaysUntilIdleLimit(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	end := s.walk(1, "a", "cam_lounge", "lounge", "kitchen")

	s.tick(end + 10*60)
	s.occupied("kitchen")
	s.tick(end + 19*60)
	s.occupied("kitchen")
	s.tick(end + 21*60)
	s.empty("kitchen")
	require.NotEmpty(t, s.tr.removed)
	assert.Contains(t, s.tr.removed[len(s.tr.removed)-1].Reason, "idle")
}

// "Another person sitting in the kitchen does not keep a phantom token alive." Each person's own track
// is the evidence for their token, not any person seen in the area.
func TestIdle_OtherPersonDoesNotKeepPhantomTokenAlive(t *testing.T) {
	s := newScene(t)
	limit := 10 * time.Minute
	s.tr.cfg.Areas["kitchen"].IdleLimit = &limit
	s.seed(0, "kitchen") // t1, the phantom: nobody holds it
	s.seed(0, "hall")    // t2, the person who walks in
	end := s.walk(1, "a", "cam_hall", "hall", "hall_to_kitchen")
	s.event(end+1, "update", "b", "cam_kitchen", []string{"kitchen"}, "kitchen")
	require.Equal(t, ruleHandoff, s.tr.active["b"].rule)
	require.Equal(t, "t2", s.tr.active["b"].tokenID)
	s.tokens(2)

	// The person stays in view of the kitchen camera for 15 minutes.
	for sec := end + 10; sec <= end+15*60; sec += 10 {
		s.event(sec, "update", "b", "cam_kitchen", []string{"kitchen"}, "kitchen")
		s.tick(sec)
	}

	s.tokens(1)
	assert.Contains(t, s.tr.tokens, "t2")
	require.NotEmpty(t, s.tr.removed)
	last := s.tr.removed[len(s.tr.removed)-1]
	assert.Equal(t, "t1", last.Token)
	assert.Equal(t, "idle for more than 10m0s", last.Reason)
}

// A person who sits still until Frigate loses them keeps their token while new tracks keep seeing them.
func TestIdle_NewTrackOfSamePersonKeepsToken(t *testing.T) {
	s := newScene(t)
	limit := 10 * time.Minute
	s.tr.cfg.Areas["kitchen"].IdleLimit = &limit
	s.seed(0, "kitchen")

	// Frigate loses the person and finds them again with a new track every 8 minutes, for 40 minutes.
	for i := 0; i < 5; i++ {
		start := float64(i*8*60) + 1
		end := s.walk(start, fmt.Sprintf("p%d", i), "cam_kitchen", "kitchen")
		last := s.tr.ended[len(s.tr.ended)-1]
		assert.Equal(t, ruleResume, last.rule)
		assert.Equal(t, "t1", last.tokenID)
		s.tick(end + 7*60)
	}
	s.tokens(1)
	s.occupied("kitchen")
	assert.Empty(t, s.tr.removed)
}

// Rule 2: a new track that starts where another just ended is the same person.
func TestRule2_HandoffKeepsOneToken(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	end := s.walk(1, "a", "cam_lounge", "lounge")
	s.walk(end+1, "b", "cam_kitchen", "kitchen")

	s.tokens(1)
	s.occupied("kitchen")
	s.empty("lounge")
}

func TestRule2_HandoffWindowExpires(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	end := s.walk(1, "a", "cam_lounge", "lounge")

	// Ten seconds later is outside the window. The lounge token is idle, so the person "resumes" there
	// only if they appear in the lounge. In the kitchen they are a new, unconfirmed track.
	s.event(end+10, "update", "b", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.tokens(1)
	s.empty("kitchen")
	s.occupied("lounge")
}

// Two cameras that see the same person share one token.
func TestRule2_OverlappingCamerasShareToken(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	s.event(1, "update", "a", "cam_lounge", []string{"lounge"}, "lounge")
	s.event(1.5, "update", "b", "cam_kitchen", []string{"lounge"}, "lounge")
	s.event(3, "update", "a", "cam_lounge", []string{"lounge", "kitchen"}, "kitchen")
	s.event(3.2, "update", "b", "cam_kitchen", []string{"lounge", "kitchen"}, "kitchen")

	s.tokens(1)
	s.occupied("kitchen")
	snap := s.tr.Snapshot()
	require.Len(t, snap.Tokens, 1)
	assert.ElementsMatch(t, []string{"a", "b"}, snap.Tokens[0].Tracks)
}

// A person who sat still until Frigate lost them is picked up again by the same token.
func TestResume_IdleTokenIsReused(t *testing.T) {
	s := newScene(t)
	s.seed(0, "kitchen")
	s.event(300, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.tokens(1)
	assert.Equal(t, ruleResume, s.tr.active["a"].rule)
	assert.Empty(t, s.tr.DrainEntries(), "standing up again is not an entry")
}

// Rule 3: a person who comes back from an unseen room is often tracked only after the doorway.
func TestRule3_NearDoorwayMovesTokenOut(t *testing.T) {
	s := newScene(t)
	s.seed(0, "study")
	s.event(120, "update", "a", "cam_lounge", []string{"lounge"}, "lounge")

	s.empty("study")
	s.occupied("lounge")
	assert.Equal(t, ruleNearDoorway, s.tr.active["a"].rule)
}

// When an idle token and a doorway token could both explain a new track, the latest disappearance wins.
func TestResumeOrDoorway_MostRecentDisappearanceWins(t *testing.T) {
	s := newScene(t)
	s.seed(5, "lounge") // t1: sat still on the couch, last seen at 5 s
	s.seed(13, "study") // t2: went into the study at 13 s

	s.event(60, "update", "b", "cam_lounge", []string{"lounge"}, "lounge")
	assert.Equal(t, ruleNearDoorway, s.tr.active["b"].rule, "the study entry is newer than the couch")
	s.empty("study")
	s.tokens(2)

	s2 := newScene(t)
	s2.seed(13, "study")
	s2.seed(30, "lounge")
	s2.event(60, "update", "b", "cam_lounge", []string{"lounge"}, "lounge")
	assert.Equal(t, ruleResume, s2.tr.active["b"].rule, "the couch sitter was lost more recently")
	s2.occupied("study")
}

// "When I walk into the study, the lounge does not go dark while someone else is on the couch."
func TestStory_StudyEntryLeavesCouchSitter(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge") // on the couch, out of Frigate's view
	s.seed(0, "lounge") // walking
	s.event(1, "update", "a", "cam_lounge", []string{"lounge"}, "lounge")
	s.event(3, "update", "a", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	s.event(5, "end", "a", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")

	s.occupied("lounge", "study")
	s.tokens(2)
}

// Rule 4 and the false-person check: "A false person in the kitchen does not make it occupied."
func TestRule4_FalsePersonStaysUnconfirmed(t *testing.T) {
	s := newScene(t)
	s.tr.SetPeople(at(0), 1, false, true)
	s.seed(0, "hall")

	for i := 0.0; i < 30; i++ {
		s.event(10+i, "update", "ghost", "cam_kitchen", []string{"kitchen"}, "kitchen")
	}
	s.empty("kitchen")
	s.event(41, "end", "ghost", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.empty("kitchen")
	s.tokens(1)
	assert.Equal(t, "dropped: never confirmed", s.tr.ended[len(s.tr.ended)-1].outcome)
}

func TestRule4_ConfirmedWhenItWalks(t *testing.T) {
	s := newScene(t)
	s.tr.SetPeople(at(0), 1, false, true)
	s.seed(0, "hall")
	s.event(10, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.empty("kitchen")
	s.event(12, "update", "a", "cam_kitchen", []string{"kitchen", "lounge"}, "lounge")
	s.occupied("lounge")
	// The people count never removes a token: the hall token stays until its idle limit.
	s.tokens(2)
	s.occupied("hall")
}

func TestRule4_ConfirmedWhenFewerTokensThanPeople(t *testing.T) {
	s := newScene(t)
	s.tr.SetPeople(at(0), 2, false, true)
	s.event(10, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.occupied("kitchen")
}

func TestRule4_GuestsMakeTheCountUnknown(t *testing.T) {
	s := newScene(t)
	s.tr.SetPeople(at(0), 2, true, true)
	s.event(10, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.empty("kitchen")
}

// A track that ends at a boundary to a seen area moves its token there if no camera picks it up.
func TestEnd_BoundaryToSeenWithoutPickup(t *testing.T) {
	s := newScene(t)
	s.seed(0, "kitchen")
	s.event(1, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.event(2, "update", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen_to_hall")
	s.event(3, "end", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen_to_hall")
	s.tick(4)
	s.occupied("kitchen")
	s.tick(9)
	s.occupied("hall")
	s.empty("kitchen")
}

func TestEnd_BoundaryToSeenWithPickup(t *testing.T) {
	s := newScene(t)
	s.seed(0, "kitchen")
	s.event(1, "update", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen_to_hall")
	s.event(2, "end", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen_to_hall")
	s.event(2.4, "update", "b", "cam_hall", []string{"hall_to_kitchen"}, "hall_to_kitchen")
	s.event(3, "update", "b", "cam_hall", []string{"hall_to_kitchen", "hall"}, "hall")
	s.tick(10)

	s.tokens(1)
	s.occupied("hall")
	s.empty("kitchen")
	assert.Equal(t, ruleHandoff, s.tr.active["b"].rule)
}

// "When a camera goes offline, its rooms do not go dark."
func TestCameraFailure_FreezesAndResumesExpiry(t *testing.T) {
	s := newScene(t)
	s.tr.Heartbeat(at(0), "cam_hall")
	s.seed(0, "hall")
	s.walk(1, "a", "cam_hall", "hall") // hall idle limit 5m; last seen at 2s
	for sec := 10.0; sec <= 60; sec += 10 {
		s.tr.Heartbeat(at(sec), "cam_hall")
		s.tick(sec)
	}
	// 4 minutes in, the camera has stopped.
	s.tick(60 + 31)
	assert.True(t, s.tr.unknown("hall"))
	s.tick(60 + 30*60)
	s.occupied("hall")

	// It comes back 30 minutes later. About 3 of the 5 minutes remain.
	back := 60.0 + 30*60
	s.tr.Heartbeat(at(back), "cam_hall")
	s.tick(back)
	assert.False(t, s.tr.unknown("hall"))
	for sec := back + 10; sec <= back+5*60; sec += 10 {
		s.tr.Heartbeat(at(sec), "cam_hall")
		s.tick(sec)
		if sec == back+60 {
			s.occupied("hall")
		}
	}
	s.empty("hall")
}

// The idle clock counts from the token's last sighting, and that clock also pauses while the camera is down.
func TestCameraFailure_PausesLastSeenClock(t *testing.T) {
	s := newScene(t)
	s.tr.Heartbeat(at(0), "cam_hall")
	s.seed(0, "hall")
	end := s.walk(200, "a", "cam_hall", "hall") // resumes the token; last seen 3m22s after it arrived
	require.Equal(t, ruleResume, s.tr.ended[len(s.tr.ended)-1].rule)
	for sec := 10.0; sec <= end+10; sec += 10 {
		s.tr.Heartbeat(at(sec), "cam_hall")
		s.tick(sec)
	}
	s.tick(end + 10 + 31)
	require.True(t, s.tr.unknown("hall"))

	back := end + 10 + 30*60
	s.tr.Heartbeat(at(back), "cam_hall")
	s.tick(back)
	for sec := back + 10; sec <= back+5*60; sec += 10 {
		s.tr.Heartbeat(at(sec), "cam_hall")
		s.tick(sec)
		if sec == back+4*60 {
			s.occupied("hall") // about 40s of the 5m limit had passed when the camera stopped
		}
	}
	s.empty("hall")
}

func TestNobodyHome_ClearsTokensAfterDelay(t *testing.T) {
	s := newScene(t)
	s.seed(0, "kitchen")
	s.seed(0, "study")
	s.tr.SetPeople(at(10), 0, false, false)
	s.tick(10 + 4*60)
	s.tokens(2)
	s.tick(10 + 5*60)
	s.tokens(0)
}

// The people count does not cap tokens. Guests are not counted, and a dropped token can be the wrong person.
func TestPeopleCount_DoesNotRemoveTokens(t *testing.T) {
	s := newScene(t)
	s.seed(0, "study")
	s.seed(100, "kitchen")
	s.tr.SetPeople(at(200), 1, false, true)
	s.tick(201)
	s.tokens(2)
	s.occupied("kitchen", "study")
}

// A track ends in a doorway to a seen area that an online camera covers, and no camera picks the person
// up. They stepped into the doorway and back, or Frigate lost someone standing near it: the token stays.
func TestEnd_BoundaryToCoveredAreaWithoutPickupStays(t *testing.T) {
	s := newScene(t)
	s.tr.addCameraArea("cam_hall", "hall") // as from a config cameras: entry or an earlier hall zone
	for sec := 0.0; sec <= 10; sec++ {
		s.tr.Heartbeat(at(sec), "cam_hall")
	}
	s.seed(0, "kitchen")
	s.event(1, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.event(2, "update", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen_to_hall")
	s.event(3, "end", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen_to_hall")
	s.tick(9)
	s.occupied("kitchen")
	s.empty("hall")
	assert.Contains(t, s.tr.ended[len(s.tr.ended)-1].outcome, "token stays in kitchen")
}

// The new camera's first message lists a room and the doorway the person came through. The old track has
// not ended yet. The token is handed over through the doorway instead of making a second one.
func TestHandoff_ThroughDoorwayInFirstMessage(t *testing.T) {
	s := newScene(t)
	s.seed(0, "kitchen")
	s.event(1, "update", "a", "cam_kitchen", []string{"kitchen"}, "kitchen")
	s.event(2, "update", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"}, "kitchen", "kitchen_to_hall")
	s.event(4, "update", "b", "cam_hall", []string{"hall", "hall_to_kitchen"}, "hall_to_kitchen")
	s.event(5, "update", "b", "cam_hall", []string{"hall", "hall_to_kitchen"}, "hall")
	s.event(6, "end", "a", "cam_kitchen", []string{"kitchen", "kitchen_to_hall"})
	s.tick(20)
	s.tokens(1)
	s.occupied("hall")
	s.empty("kitchen")
	assert.Equal(t, ruleHandoff, s.tr.active["b"].rule)
}

// Frigate ends a track a few seconds after the person leaves view. A person who stepped into an unseen
// room and came straight back starts a new track in the same doorway before the old one ends. That is one
// person: the old track's end must not leave a token in the unseen room.
func TestTwin_CameStraightBackBeforeOldTrackEnded(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	s.event(1, "update", "a", "cam_lounge", []string{"lounge"}, "lounge")
	s.event(2, "update", "a", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	s.event(2.5, "update", "a", "cam_lounge", []string{"lounge", "lounge_to_study"})
	s.event(5, "update", "b", "cam_lounge", []string{"lounge_to_study"}, "lounge_to_study")
	s.event(5.5, "end", "a", "cam_lounge", []string{"lounge", "lounge_to_study"})
	s.event(6, "update", "b", "cam_lounge", []string{"lounge_to_study", "lounge"}, "lounge")
	s.tokens(1)
	s.occupied("lounge")
	s.empty("study")
}

// A track still sending updates is a different person, even in the same doorway.
func TestTwin_NotWhileOldTrackIsStillInView(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	s.event(1, "update", "a", "cam_lounge", []string{"lounge"}, "lounge")
	s.event(2, "update", "a", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	s.event(2.2, "update", "b", "cam_lounge", []string{"lounge_to_study"}, "lounge_to_study")
	s.tokens(2)
}

func TestBedSensor_EnsuresToken(t *testing.T) {
	s := newScene(t)
	s.tr.BedOccupied(at(0), "bedroom")
	s.occupied("bedroom")
	s.tr.BedOccupied(at(1), "bedroom")
	s.tokens(1)
	s.tick(48 * 3600)
	s.occupied("bedroom") // no idle limit
	assert.Empty(t, s.tr.DrainEntries(), "a bed sensor is not an entry")
}

// Frigate often ends a track with no current zones. The last zone the person stood in decides.
func TestEnd_EmptyCurrentZonesUsesLastNonEmpty(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	s.event(1, "update", "a", "cam_lounge", []string{"lounge", "kitchen"}, "kitchen")
	s.event(2, "update", "a", "cam_lounge", []string{"lounge", "kitchen", "lounge_to_study"}, "lounge_to_study")
	s.event(3, "update", "a", "cam_lounge", []string{"lounge", "kitchen", "lounge_to_study"}, "lounge")
	s.event(4, "update", "a", "cam_lounge", []string{"lounge", "kitchen", "lounge_to_study"})
	s.event(5, "end", "a", "cam_lounge", []string{"lounge", "kitchen", "lounge_to_study"})
	s.occupied("lounge")
	s.empty("study")
}

func TestReconnected_EndsOpenTracks(t *testing.T) {
	s := newScene(t)
	s.seed(0, "lounge")
	s.event(1, "update", "a", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	s.tr.Reconnected(at(2))
	assert.Empty(t, s.tr.active)
	s.occupied("study")
}

func TestUnknownZonesAndFalsePositives(t *testing.T) {
	s := newScene(t)
	s.event(1, "update", "a", "cam_lounge", []string{"garden"}, "garden")
	s.event(2, "end", "a", "cam_lounge", []string{"garden"}, "garden")
	assert.Equal(t, []string{"garden"}, s.tr.Snapshot().UnknownZones)
	s.tokens(0)

	s.tr.HandleEvent(at(3), FrigateEvent{Type: "update", After: FrigateObject{
		ID: "fp", Camera: "cam_lounge", Label: "person", FalsePositive: true,
		EnteredZones: []string{"lounge_to_study"}, CurrentZones: []string{"lounge_to_study"},
	}})
	s.tokens(0)

	s.tr.HandleEvent(at(4), FrigateEvent{Type: "update", After: FrigateObject{
		ID: "cat", Camera: "cam_lounge", Label: "cat", EnteredZones: []string{"lounge_to_study"},
	}})
	s.tokens(0)
}

func TestOutputs(t *testing.T) {
	s := newScene(t)
	assert.Equal(t, map[string]bool{"isKitchenOccupied": false}, s.tr.Outputs())
	s.seed(0, "kitchen")
	assert.Equal(t, map[string]bool{"isKitchenOccupied": true}, s.tr.Outputs())
}

func TestSnapshot(t *testing.T) {
	s := newScene(t)
	s.tr.Heartbeat(at(0), "cam_lounge")
	s.event(1, "update", "a", "cam_lounge", []string{"lounge_to_study"}, "lounge_to_study")
	snap := s.tr.Snapshot()
	assert.True(t, snap.Areas["lounge"].Occupied)
	assert.Equal(t, 1, snap.EventsHandled)
	require.Len(t, snap.ActiveTracks, 1)
	assert.Equal(t, ruleBoundaryExit, snap.ActiveTracks[0].Rule)
	assert.True(t, snap.Cameras["cam_lounge"].Online)
	assert.Contains(t, snap.Cameras["cam_lounge"].Areas, "lounge")
	require.NotNil(t, snap.LastEntry)
	assert.Equal(t, "lounge", snap.LastEntry.Area)
}

const yardPerson = "binary_sensor.example_yard_person"

// walkToPorch walks a person from the hall out through hall_to_porch, starting at sec. The track ends 4 s
// after the person leaves view, as Frigate's do. It returns when the porch token arrives.
func (s *scene) walkToPorch(sec float64, id string) float64 {
	s.event(sec, "update", id, "cam_hall", []string{"hall"}, "hall")
	s.event(sec+1, "update", id, "cam_hall", []string{"hall", "hall_to_porch"}, "hall_to_porch")
	s.event(sec+5, "end", id, "cam_hall", []string{"hall", "hall_to_porch"}, "hall_to_porch")
	s.occupied("porch")
	return sec + 5
}

// "When I walk out the front door, the porch does not stay occupied."
func TestExit_SensorAfterArrivalRemovesToken(t *testing.T) {
	s := newScene(t)
	arrived := s.walkToPorch(0, "a")
	s.tr.SensorChanged(at(arrived+3), yardPerson, "off", "on")
	s.empty("porch")
	s.tokens(0)
	assert.Contains(t, s.tr.removed[len(s.tr.removed)-1].Reason, "went outside")
}

// Frigate ends a track a few seconds late, so the yard can see the person before the token arrives.
func TestExit_SensorJustBeforeArrivalRemovesToken(t *testing.T) {
	s := newScene(t)
	s.event(0, "update", "a", "cam_hall", []string{"hall"}, "hall")
	s.event(1, "update", "a", "cam_hall", []string{"hall", "hall_to_porch"}, "hall_to_porch")
	s.tr.SensorChanged(at(3), yardPerson, "unknown", "on")
	s.event(5, "end", "a", "cam_hall", []string{"hall", "hall_to_porch"}, "hall_to_porch")
	s.occupied("porch")
	s.tick(6)
	s.empty("porch")
}

// "When I go to the door to let a visitor in, my token stays." The visitor was outside first.
func TestExit_VisitorAlreadyOutsideKeepsToken(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), yardPerson, "off", "on")
	arrived := s.walkToPorch(20, "a")
	s.tick(arrived + 1)
	s.tick(arrived + 10)
	s.occupied("porch")
}

// A person outside long after someone went to the porch says nothing about that person.
func TestExit_SensorAfterWindowKeepsToken(t *testing.T) {
	s := newScene(t)
	arrived := s.walkToPorch(0, "a")
	s.tr.SensorChanged(at(arrived+31), yardPerson, "off", "on")
	s.occupied("porch")
}

// One person outside removes one token. A sensor that was already on is not a new person.
func TestExit_OneTurnOnRemovesOneToken(t *testing.T) {
	s := newScene(t)
	s.seed(0, "porch")
	s.seed(1, "porch")
	arrived := 1.0
	s.tokens(2)
	s.tr.SensorChanged(at(arrived+1), yardPerson, "off", "on")
	s.tokens(1)
	s.tr.SensorChanged(at(arrived+2), yardPerson, "on", "on")
	s.tokens(1)
	s.tr.SensorChanged(at(arrived+3), yardPerson, "on", "off")
	s.tr.SensorChanged(at(arrived+4), yardPerson, "off", "on")
	s.tokens(0)
}

// A person who comes back in through the doorway gets a token again.
func TestExit_ComingBackInCreatesToken(t *testing.T) {
	s := newScene(t)
	arrived := s.walkToPorch(0, "a")
	s.tr.SensorChanged(at(arrived+2), yardPerson, "off", "on")
	s.empty("porch")
	s.event(arrived+60, "update", "b", "cam_hall", []string{"hall_to_porch"}, "hall_to_porch")
	s.occupied("hall")
}

// A bed sensor goes through SensorChanged too.
func TestSensorChanged_BedSensor(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), "binary_sensor.example_bed_presence", "off", "on")
	s.occupied("bedroom")
	s.tr.SensorChanged(at(1), "binary_sensor.unrelated", "off", "on")
	s.tokens(1)
}

const (
	streetPerson = "binary_sensor.example_street_person"
	porchUnlock  = "input_boolean.example_porch_door_unlocked"
)

// "If they appear farther away, then right at the door, they're arriving." The door then unlocks.
func TestArrival_FarThenNearThenUnlockCreatesToken(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), streetPerson, "off", "on")
	s.tr.SensorChanged(at(8), yardPerson, "off", "on")
	s.empty("porch")
	s.tr.SensorChanged(at(15), porchUnlock, "off", "on")
	s.occupied("porch")
	s.tokens(1)
	for _, tok := range s.tr.tokens {
		assert.Contains(t, tok.reason, "came in")
	}
	assert.Contains(t, s.tr.DrainEntries(), "porch")
}

// The arrival token is the person the hall camera then sees come in.
func TestArrival_TokenWalksIn(t *testing.T) {
	s := newScene(t)
	s.tr.Heartbeat(at(0), "cam_hall")
	s.tr.SensorChanged(at(0), streetPerson, "off", "on")
	s.tr.SensorChanged(at(8), yardPerson, "off", "on")
	s.tr.SensorChanged(at(15), porchUnlock, "off", "on")
	s.event(20, "update", "a", "cam_hall", []string{"hall_to_porch"}, "hall_to_porch")
	s.event(21, "update", "a", "cam_hall", []string{"hall_to_porch", "hall"}, "hall")
	s.occupied("hall")
	s.empty("porch")
	s.tokens(1)
}

// A visitor walks up to the door and the door is not opened: no token.
func TestArrival_VisitorWithoutUnlockGetsNoToken(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), streetPerson, "off", "on")
	s.tr.SensorChanged(at(8), yardPerson, "off", "on")
	s.tick(70)
	s.tr.SensorChanged(at(75), porchUnlock, "off", "on")
	s.tokens(0)
}

// "If they appear right at the door, then farther away, they're leaving." Both cameras see a person at
// the door, so the street camera turns on with the yard camera, not before it. The exit rule still applies.
func TestArrival_LeavingStillRemovesToken(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), porchUnlock, "off", "on") // unlocked from inside: no arrival
	arrived := s.walkToPorch(1, "a")
	s.tr.SensorChanged(at(arrived+0.5), streetPerson, "off", "on")
	s.tr.SensorChanged(at(arrived+1.3), yardPerson, "off", "on")
	s.empty("porch")
	s.tokens(0)
}

// An approach long before the door is somebody else, so the door sensor is an exit as usual.
func TestArrival_OldApproachIsNotAnArrival(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), streetPerson, "off", "on")
	arrived := s.walkToPorch(100, "a")
	s.tr.SensorChanged(at(arrived+2), yardPerson, "off", "on")
	s.tokens(0)
}

// An unlock with nobody walking up does nothing.
func TestArrival_UnlockAloneDoesNothing(t *testing.T) {
	s := newScene(t)
	s.tr.SensorChanged(at(0), porchUnlock, "off", "on")
	s.tokens(0)
}

func TestVacuum_DoorwayIntoRobotAreaIsTheRobot(t *testing.T) {
	s := newScene(t)
	s.tr.SetVacuumArea("study") // no camera sees the study; the robot shows only in its doorway
	s.walk(0, "r", "cam_lounge", "lounge", "lounge_to_study")
	s.tokens(0)
	s.empty("study", "lounge")
	assert.Equal(t, "dropped: the robot vacuum is in study", s.tr.Snapshot().RecentTracks[0].Outcome)
}

func TestVacuum_TrackStaysDroppedAfterRobotMoves(t *testing.T) {
	s := newScene(t)
	s.tr.SetVacuumArea("lounge")
	s.event(0, "new", "r", "cam_lounge", nil, "lounge")
	s.event(1, "update", "r", "cam_lounge", []string{"lounge"}, "lounge")
	s.tr.SetVacuumArea("") // the robot's next poll is late, or it docked
	s.event(2, "update", "r", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	s.event(3, "end", "r", "cam_lounge", []string{"lounge", "lounge_to_study"}, "lounge_to_study")
	s.tokens(0)
}

func TestVacuum_OtherAreasUnchanged(t *testing.T) {
	s := newScene(t)
	s.tr.SetVacuumArea("kitchen")
	s.walk(0, "p", "cam_lounge", "lounge", "lounge_to_study")
	s.occupied("study")
}
