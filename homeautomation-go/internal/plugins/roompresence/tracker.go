package roompresence

import (
	"fmt"
	"sort"
	"time"

	"homeautomation/internal/shadowstate"
)

// Rule names, reported in shadow state.
const (
	ruleBoundaryExit = "boundary_exit" // rule 1: started in a boundary zone
	ruleHandoff      = "handoff"       // rule 2: picked up where another track ended or is running
	ruleResume       = "resume"        // a person who stood still in the area moved again
	ruleNearDoorway  = "near_doorway"  // rule 3: started next to an unseen area that holds a token
	ruleUnconfirmed  = "unconfirmed"   // rule 4: appeared from nowhere
	ruleTwin         = "twin"          // came straight back through a doorway before the old track ended
)

// How many ended tracks and removals to keep for shadow state.
const (
	recentTrackLimit = 30
	removalLimit     = 20
)

// twinQuiet is how long a track must have gone without an update before a new track in its doorway is
// taken to be the same person. A person still in view keeps sending updates.
const twinQuiet = 2 * time.Second

// FrigateEvent is one message on frigate/events.
type FrigateEvent struct {
	Type  string        `json:"type"`
	After FrigateObject `json:"after"`
}

// FrigateObject is the tracked object in a Frigate event.
type FrigateObject struct {
	ID            string   `json:"id"`
	Camera        string   `json:"camera"`
	Label         string   `json:"label"`
	CurrentZones  []string `json:"current_zones"`
	EnteredZones  []string `json:"entered_zones"`
	FalsePositive bool     `json:"false_positive"`
}

type token struct {
	id       string
	area     string
	since    time.Time // when the token arrived in its area
	lastSeen time.Time // when a track holding it last reported
	camera   string
	reason   string
}

type track struct {
	id         string
	camera     string
	start      time.Time
	lastUpdate time.Time
	end        time.Time
	zones      []string // entered zones, in entry order
	current    []string // current zones at the last message
	// lastCurrent is the last non-empty current zones. Frigate often ends a track with none, and
	// entered_zones lists each zone once, so its last entry is not where the person was last.
	lastCurrent []string
	started     bool // the first zone has been handled
	tokenID     string
	startArea   string
	area        string // area at the last message
	rule        string
	confirmed   bool
	outcome     string

	// pendingTarget is a seen area the token moves to when no other camera picks the person up.
	pendingTarget string
}

// exitEdge is an exit sensor turning on.
type exitEdge struct {
	at     time.Time
	entity string
}

// arrival is a person who walked up to an outside door: an approach sensor, then an exit sensor. It waits
// for an entry sensor.
type arrival struct {
	approach exitEdge
	door     exitEdge
}

type camera struct {
	lastHeartbeat time.Time
	online        bool
}

// Tracker keeps person tokens on the map of areas. It does no I/O and reads no clock. Every method takes
// the current time, so tests can replay captured MQTT logs. It is not safe for concurrent use.
type Tracker struct {
	cfg *Config

	tokens    map[string]*token
	nextToken int

	active  map[string]*track // by Frigate event ID
	ended   []*track          // most recent last
	removed []shadowstate.RoomPresenceRemoval

	areaLastSeen map[string]time.Time // last time a confirmed track was in a seen area
	frozenSince  map[string]time.Time // areas that are unknown because their cameras are offline

	cameras     map[string]*camera
	cameraAreas map[string]map[string]bool // camera -> areas it sees, from config and from zones

	unknownZones map[string]bool

	// exitEdges are recent exit sensor turn-ons, by area, oldest first. A token can still arrive for a
	// short time after the person went out, because Frigate ends a track a few seconds late.
	exitEdges map[string][]exitEdge

	// approachEdges are the last approach sensor turn-on, by area.
	approachEdges map[string]exitEdge
	// arrivals wait for an entry sensor, by area, oldest first.
	arrivals map[string][]arrival

	peopleKnown bool
	peopleHome  int
	guests      bool
	anyoneHome  bool
	nobodySince time.Time
	clearedAway bool

	entries   []string
	lastEntry *shadowstate.RoomPresenceEntry

	eventsHandled int
	lastEventAt   time.Time
}

// NewTracker builds a tracker with no tokens.
func NewTracker(cfg *Config) *Tracker {
	t := &Tracker{
		cfg:           cfg,
		tokens:        make(map[string]*token),
		active:        make(map[string]*track),
		areaLastSeen:  make(map[string]time.Time),
		frozenSince:   make(map[string]time.Time),
		cameras:       make(map[string]*camera),
		cameraAreas:   make(map[string]map[string]bool),
		unknownZones:  make(map[string]bool),
		exitEdges:     make(map[string][]exitEdge),
		approachEdges: make(map[string]exitEdge),
		arrivals:      make(map[string][]arrival),
		anyoneHome:    true,
	}
	for cam, areas := range cfg.Cameras {
		for _, a := range areas {
			t.addCameraArea(cam, a)
		}
	}
	return t
}

// HandleEvent applies one frigate/events message.
func (t *Tracker) HandleEvent(now time.Time, ev FrigateEvent) {
	obj := ev.After
	if obj.Label != "person" || obj.ID == "" {
		return
	}
	t.eventsHandled++
	t.lastEventAt = now

	tr, ok := t.active[obj.ID]
	if !ok {
		tr = &track{id: obj.ID, camera: obj.Camera, start: now}
		t.active[obj.ID] = tr
	}
	tr.lastUpdate = now
	tr.zones = append([]string(nil), obj.EnteredZones...)
	tr.current = append([]string(nil), obj.CurrentZones...)
	if len(obj.CurrentZones) > 0 {
		tr.lastCurrent = tr.current
	}
	t.learnZones(obj)

	if obj.FalsePositive {
		tr.outcome = "dropped: Frigate marked it a false positive"
		t.finishTrack(now, tr)
		return
	}

	if !tr.started {
		first, ok := t.firstZone(obj)
		if !ok {
			if ev.Type == "end" {
				tr.outcome = "dropped: never entered a known zone"
				t.finishTrack(now, tr)
			}
			return
		}
		t.startTrack(now, tr, first, obj)
	} else {
		t.followTrack(now, tr, obj)
	}

	if !tr.confirmed {
		t.maybeConfirm(now, tr, obj)
	}
	if tr.confirmed {
		t.refresh(now, tr)
	}

	if ev.Type == "end" {
		t.endTrack(now, tr, obj)
	}
}

// Heartbeat records a frigate/<camera>/status/detect message.
func (t *Tracker) Heartbeat(now time.Time, cam string) {
	c, ok := t.cameras[cam]
	if !ok {
		c = &camera{}
		t.cameras[cam] = c
	}
	c.lastHeartbeat = now
	if !c.online {
		c.online = true
		t.updateUnknownAreas(now)
	}
}

// SetPeople records who is home. count is the number of known people home. guests means the count is
// unknown. anyoneHome is isAnyoneHome.
func (t *Tracker) SetPeople(now time.Time, count int, guests, anyoneHome bool) {
	if anyoneHome != t.anyoneHome || !t.peopleKnown {
		if !anyoneHome {
			t.nobodySince = now
		}
		t.clearedAway = false
	}
	t.peopleKnown = true
	t.peopleHome = count
	t.guests = guests
	t.anyoneHome = anyoneHome
}

// BedOccupied makes sure an area with an occupied bed holds a token.
func (t *Tracker) BedOccupied(now time.Time, area string) {
	if _, ok := t.cfg.Areas[area]; !ok || len(t.tokensIn(area)) > 0 {
		return
	}
	t.newToken(now, area, "", "bed sensor is on", false)
}

// SensorChanged applies a bed, exit, approach or entry sensor's state change. from is the old state,
// empty when unknown.
func (t *Tracker) SensorChanged(now time.Time, entity, from, to string) {
	if to != "on" {
		return
	}
	roles := t.cfg.sensorAreas(entity)
	for _, area := range roles.beds {
		t.BedOccupied(now, area)
	}
	if from == "on" {
		return
	}
	edge := exitEdge{at: now, entity: entity}
	for _, area := range roles.approaches {
		t.approachEdges[area] = edge
	}
	for _, area := range roles.exits {
		if a, ok := t.approachEdges[area]; ok && now.Sub(a.at) >= t.cfg.ApproachLead && now.Sub(a.at) <= t.cfg.ArrivalWindow {
			// Farther out, then at the door: someone walking up, not someone leaving.
			delete(t.approachEdges, area)
			t.arrivals[area] = append(t.arrivals[area], arrival{approach: a, door: edge})
			continue
		}
		t.exitEdges[area] = append(t.exitEdges[area], edge)
		t.matchExits(now, area)
	}
	for _, area := range roles.entries {
		t.matchArrival(now, area, edge)
	}
}

// matchArrival finishes the oldest arrival at an area's door when an entry sensor turns on: the person who
// walked up opened or unlocked the door, so the area gets a token. An entry sensor with no arrival, such
// as someone inside unlocking the door to leave, does nothing.
func (t *Tracker) matchArrival(now time.Time, area string, entry exitEdge) {
	t.expireArrivals(now, area)
	pending := t.arrivals[area]
	if len(pending) == 0 {
		return
	}
	a := pending[0]
	t.arrivals[area] = pending[1:]
	if len(t.arrivals[area]) == 0 {
		delete(t.arrivals, area)
	}
	t.newToken(now, area, "", fmt.Sprintf("came in: %s, then %s, then %s turned on",
		a.approach.entity, a.door.entity, entry.entity), true)
}

// expireArrivals drops arrivals whose entry sensor did not turn on in time: a visitor who stayed outside.
func (t *Tracker) expireArrivals(now time.Time, area string) {
	keep := t.arrivals[area][:0]
	for _, a := range t.arrivals[area] {
		if now.Sub(a.door.at) <= t.cfg.ArrivalWindow {
			keep = append(keep, a)
		}
	}
	if len(keep) == 0 {
		delete(t.arrivals, area)
		return
	}
	t.arrivals[area] = keep
}

// matchExits removes the token of a person who went outside. An exit sensor turning on matches one token
// that arrived in the area at most exit_window before it, or at most handoff_window after it. The sensor
// must turn on after the person reached the door: a visitor already outside when someone walks to the
// door to answer it does not remove that person's token. Each turn-on removes one token, the most recent
// arrival. A token a track still holds is someone in view, so it stays.
func (t *Tracker) matchExits(now time.Time, area string) {
	keep := t.exitEdges[area][:0]
	for _, e := range t.exitEdges[area] {
		var best *token
		for _, tok := range t.tokensIn(area) {
			tooOld := tok.since.Before(e.at.Add(-t.cfg.ExitWindow))
			tooNew := tok.since.After(e.at.Add(t.cfg.HandoffWindow))
			if t.held(tok.id) || tooOld || tooNew {
				continue
			}
			if best == nil || tok.since.After(best.since) {
				best = tok
			}
		}
		if best != nil {
			t.removeToken(now, best, "went outside: "+e.entity+" turned on")
			continue
		}
		// A token may still arrive. Allow one tick past the window, since Tick runs once a second.
		if now.Sub(e.at) <= t.cfg.HandoffWindow+tickInterval {
			keep = append(keep, e)
		}
	}
	if len(keep) == 0 {
		delete(t.exitEdges, area)
		return
	}
	t.exitEdges[area] = keep
}

// Reconnected ends every open track as if Frigate had ended it. Messages may have been lost.
func (t *Tracker) Reconnected(now time.Time) {
	for _, id := range sortedKeys(t.active) {
		tr := t.active[id]
		if !tr.started {
			tr.outcome = "dropped: connection to Home Assistant was lost"
			t.finishTrack(now, tr)
			continue
		}
		t.endTrack(now, tr, FrigateObject{CurrentZones: tr.current, EnteredZones: tr.zones})
	}
}

// Tick applies the time-based rules: camera health, hand-off timeouts, expiry and nobody home.
func (t *Tracker) Tick(now time.Time) {
	for _, c := range t.cameras {
		if c.online && now.Sub(c.lastHeartbeat) > t.cfg.CameraHeartbeatTimeout {
			c.online = false
		}
	}
	t.updateUnknownAreas(now)

	for _, area := range sortedKeys(t.exitEdges) {
		t.matchExits(now, area)
	}
	for _, area := range sortedKeys(t.arrivals) {
		t.expireArrivals(now, area)
	}

	for _, tr := range t.ended {
		if tr.pendingTarget == "" || now.Sub(tr.end) < t.cfg.HandoffWindow {
			continue
		}
		target := tr.pendingTarget
		tr.pendingTarget = ""
		tok, ok := t.tokens[tr.tokenID]
		if !ok || t.held(tok.id) {
			continue
		}
		// A camera that sees the target area would have picked up a person who walked in. Nobody did,
		// so the person stepped into the doorway and back, or Frigate lost someone standing near it.
		if t.covered(target) {
			tr.outcome = "no camera picked the person up; token stays in " + tok.area
			continue
		}
		t.moveToken(now, tok, target, tr.camera, "no camera picked the person up; moved to "+target)
	}

	for _, id := range sortedKeys(t.tokens) {
		tok := t.tokens[id]
		if t.held(id) || t.unknown(tok.area) {
			continue
		}
		limit := t.cfg.Areas[tok.area].IdleLimit
		if limit == nil {
			continue
		}
		if now.Sub(t.lastEvidence(tok)) > *limit {
			t.removeToken(now, tok, fmt.Sprintf("idle for more than %s", *limit))
		}
	}

	if t.peopleKnown && !t.anyoneHome && !t.clearedAway && now.Sub(t.nobodySince) >= t.cfg.NobodyHomeDelay {
		t.clearedAway = true
		for _, id := range sortedKeys(t.tokens) {
			t.removeToken(now, t.tokens[id], "nobody home")
		}
	}
}

// Occupied reports whether an area holds a token.
func (t *Tracker) Occupied(area string) bool {
	return len(t.tokensIn(area)) > 0
}

// Outputs computes each configured output variable.
func (t *Tracker) Outputs() map[string]bool {
	out := make(map[string]bool, len(t.cfg.Outputs))
	for v, areas := range t.cfg.Outputs {
		for _, a := range areas {
			if t.Occupied(a) {
				out[v] = true
				break
			}
		}
		if !out[v] {
			out[v] = false
		}
	}
	return out
}

// KeptToken is a token in an unseen area, as kept across a restart.
type KeptToken struct {
	Area  string
	Since time.Time
}

// UnseenTokens lists the tokens in unseen areas, by area, then by arrival. Cameras find people in seen
// areas again after a restart, so only these need keeping.
func (t *Tracker) UnseenTokens() []KeptToken {
	var out []KeptToken
	for _, tok := range t.tokens {
		if a := t.cfg.Areas[tok.area]; a != nil && !a.Seen {
			out = append(out, KeptToken{Area: tok.area, Since: tok.since})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		return out[i].Since.Before(out[j].Since)
	})
	return out
}

// Restore puts kept tokens back, each with its first arrival time, so idle limits still count from then.
// It skips areas that are no longer unseen areas in the config. It returns how many tokens it made.
func (t *Tracker) Restore(now time.Time, kept []KeptToken) int {
	n := 0
	for _, k := range kept {
		if a := t.cfg.Areas[k.Area]; a == nil || a.Seen {
			continue
		}
		tok := t.newToken(now, k.Area, "", "restored after restart", false)
		if k.Since.Before(now) {
			tok.since = k.Since
			tok.lastSeen = k.Since
		}
		n++
	}
	return n
}

// DrainEntries returns the areas a token entered since the last call, oldest first.
func (t *Tracker) DrainEntries() []string {
	e := t.entries
	t.entries = nil
	return e
}

// ---------------------------------------------------------------------------
// Track rules
// ---------------------------------------------------------------------------

// firstZone is the first zone of the track that means something.
func (t *Tracker) firstZone(obj FrigateObject) (zoneInfo, bool) {
	for _, z := range append(append([]string(nil), obj.EnteredZones...), obj.CurrentZones...) {
		if zi := t.cfg.resolveZone(z); zi.Kind == zoneRoom || zi.Kind == zoneBoundary {
			return zi, true
		}
	}
	return zoneInfo{}, false
}

// currentZone is where the person is now: the current zone entered most recently. A boundary zone
// wins over a room zone, since it is drawn on top of the room's floor.
func (t *Tracker) currentZone(obj FrigateObject) (zoneInfo, bool) {
	order := make(map[string]int, len(obj.EnteredZones))
	for i, z := range obj.EnteredZones {
		order[z] = i
	}
	best, bestScore, found := zoneInfo{}, -1, false
	for _, z := range obj.CurrentZones {
		zi := t.cfg.resolveZone(z)
		if zi.Kind != zoneRoom && zi.Kind != zoneBoundary {
			continue
		}
		score := order[z]
		if zi.Kind == zoneBoundary {
			score += len(obj.EnteredZones) + 1
		}
		if score > bestScore {
			best, bestScore, found = zi, score, true
		}
	}
	return best, found
}

// firstMessageZones lists the areas and doorway zones in a track's first message. The first message can
// list several zones, and Frigate's order is not always the order the person walked in.
func (t *Tracker) firstMessageZones(obj FrigateObject) (areas []string, doorways []zoneInfo) {
	seen := map[string]bool{}
	for _, z := range append(append([]string(nil), obj.CurrentZones...), obj.EnteredZones...) {
		if seen[z] {
			continue
		}
		seen[z] = true
		switch zi := t.cfg.resolveZone(z); zi.Kind {
		case zoneRoom:
			areas = append(areas, zi.Area)
		case zoneBoundary:
			areas = append(areas, zi.Area)
			if t.cfg.Areas[zi.Beyond].Seen {
				areas = append(areas, zi.Beyond)
			}
			doorways = append(doorways, zi)
		}
	}
	return areas, doorways
}

// startTrack applies the start rules to a track's first message.
func (t *Tracker) startTrack(now time.Time, tr *track, first zoneInfo, obj FrigateObject) {
	near, doorways := t.firstMessageZones(obj)
	tr.started = true
	tr.startArea = first.Area
	tr.area = first.Area

	if first.Kind == zoneBoundary {
		// Rule 1: the person came out through the doorway.
		if t.cfg.Areas[first.Beyond].Seen {
			if t.tryHandoff(now, tr, first.Area, append(near, first.Beyond)) {
				return
			}
		}
		tr.rule = ruleBoundaryExit
		tr.confirmed = true
		reason := "came out of " + first.Beyond
		if tok := t.unheldToken(first.Beyond); tok != nil {
			t.attach(tr, tok)
			t.moveToken(now, tok, first.Area, tr.camera, reason)
			return
		}
		if t.takeTwin(now, tr, first.Area, doorways) {
			return
		}
		t.attach(tr, t.newToken(now, first.Area, tr.camera, reason+" (no token was there)", true))
		return
	}

	// Rule 2: the same person, handed off from another track.
	if t.tryHandoff(now, tr, first.Area, near) {
		return
	}
	if t.takeTwin(now, tr, first.Area, doorways) {
		return
	}

	// A person who stood still until Frigate lost them moved again, or a person came back from an unseen
	// area and was tracked only after the doorway. When both could explain the track, the person who
	// disappeared most recently is the one who reappeared.
	idle := t.unheldToken(first.Area)
	door := t.nearDoorwayToken(first.Area)
	if idle != nil && door != nil {
		if door.since.After(idle.lastSeen) {
			idle = nil
		} else {
			door = nil
		}
	}
	if idle != nil {
		tr.rule = ruleResume
		tr.confirmed = true
		t.attach(tr, idle)
		return
	}
	if door != nil {
		tr.rule = ruleNearDoorway
		tr.confirmed = true
		from := door.area
		t.attach(tr, door)
		t.moveToken(now, door, first.Area, tr.camera, "came back from "+from)
		return
	}

	// Rule 4: appeared from nowhere.
	tr.rule = ruleUnconfirmed
}

// tryHandoff attaches tr to the token of a track that just ended nearby, or that another camera is
// following nearby right now. also lists more areas the track's first message touched.
func (t *Tracker) tryHandoff(now time.Time, tr *track, area string, also []string) bool {
	near := func(a string) bool {
		if t.cfg.neighbors(a, area) {
			return true
		}
		for _, b := range also {
			if t.cfg.neighbors(a, b) {
				return true
			}
		}
		return false
	}

	var best *track
	var bestAt time.Time
	consider := func(c *track, at time.Time) {
		if c == tr || c.tokenID == "" || now.Sub(at) > t.cfg.HandoffWindow {
			return
		}
		if best == nil || at.After(bestAt) {
			best, bestAt = c, at
		}
	}
	for _, c := range t.ended {
		tok, ok := t.tokens[c.tokenID]
		if !ok || t.held(c.tokenID) {
			continue // gone, or another track already took it over
		}
		// The token must still be where the person was last seen. One that went into an unseen
		// area belongs to a person who left.
		if !near(tok.area) && c.pendingTarget != area {
			continue
		}
		consider(c, c.end)
	}
	for _, c := range t.active {
		if _, ok := t.tokens[c.tokenID]; ok && c.camera != tr.camera && near(c.area) {
			consider(c, c.lastUpdate)
		}
	}
	if best == nil {
		return false
	}

	tr.rule = ruleHandoff
	tr.confirmed = true
	best.pendingTarget = ""
	tok := t.tokens[best.tokenID]
	t.attach(tr, tok)
	if tok.area != area {
		t.moveToken(now, tok, area, tr.camera, "handed off from "+best.camera)
	}
	return true
}

// takeTwin handles Frigate ending a track a few seconds after the person leaves view. A new track in the
// same doorway before the old track ends is the same person coming straight back: it takes the old
// track's token.
func (t *Tracker) takeTwin(now time.Time, tr *track, area string, doorways []zoneInfo) bool {
	for _, d := range doorways {
		twin := t.doorwayTwin(now, tr, d)
		if twin == nil {
			continue
		}
		tr.rule = ruleTwin
		tr.confirmed = true
		tok := t.tokens[twin.tokenID]
		t.attach(tr, tok)
		if tok.area != area {
			t.moveToken(now, tok, area, tr.camera, "came straight back through "+d.Area+"_to_"+d.Beyond)
		}
		return true
	}
	return false
}

// doorwayTwin finds an active track that was last seen in the doorway, from either side, and has gone
// quiet: Frigate has lost the person but not ended the track yet. Only a doorway to an unseen area
// counts. Between two seen areas the hand-off rule already follows the person, and people standing near
// such a doorway would be mistaken for each other.
func (t *Tracker) doorwayTwin(now time.Time, tr *track, first zoneInfo) *track {
	if t.cfg.Areas[first.Area].Seen && t.cfg.Areas[first.Beyond].Seen {
		return nil
	}
	var best *track
	for _, id := range sortedKeys(t.active) {
		c := t.active[id]
		if c == tr || t.tokens[c.tokenID] == nil {
			continue
		}
		quiet := now.Sub(c.lastUpdate)
		if quiet < twinQuiet || quiet > t.cfg.HandoffWindow {
			continue
		}
		zi, ok := t.currentZone(FrigateObject{CurrentZones: c.lastCurrent, EnteredZones: c.zones})
		if !ok || zi.Kind != zoneBoundary {
			continue
		}
		same := zi.Area == first.Area && zi.Beyond == first.Beyond
		opposite := zi.Area == first.Beyond && zi.Beyond == first.Area
		if (same || opposite) && (best == nil || c.lastUpdate.After(best.lastUpdate)) {
			best = c
		}
	}
	return best
}

// covered reports whether an online camera sees the area.
func (t *Tracker) covered(area string) bool {
	for cam, areas := range t.cameraAreas {
		if c := t.cameras[cam]; c != nil && c.online && areas[area] {
			return true
		}
	}
	return false
}

// nearDoorwayToken finds an unheld token in an unseen area next to area. The most recent arrival wins.
func (t *Tracker) nearDoorwayToken(area string) *token {
	var best *token
	for _, name := range sortedKeys(t.cfg.Areas) {
		if name == area || t.cfg.Areas[name].Seen || !t.cfg.neighbors(area, name) {
			continue
		}
		for _, tok := range t.tokensIn(name) {
			if t.held(tok.id) {
				continue
			}
			if best == nil || tok.since.After(best.since) {
				best = tok
			}
		}
	}
	return best
}

// followTrack moves the token when the person walks into another area.
func (t *Tracker) followTrack(now time.Time, tr *track, obj FrigateObject) {
	cur, ok := t.currentZone(obj)
	if !ok {
		return
	}
	tr.area = cur.Area
	if tr.tokenID == "" {
		return
	}
	tok := t.tokens[tr.tokenID]
	if tok == nil {
		// The token expired or was dropped while the person was still in view. Give them a new one.
		t.attach(tr, t.newToken(now, cur.Area, tr.camera, "track outlived its token", false))
		return
	}
	if tok.area != cur.Area {
		t.moveToken(now, tok, cur.Area, tr.camera, "walked to "+cur.Area)
	}
}

// maybeConfirm turns an unconfirmed track into a token once it behaves like a real person.
func (t *Tracker) maybeConfirm(now time.Time, tr *track, obj FrigateObject) {
	if !tr.started {
		return
	}
	reason := ""
	distinct := map[string]bool{}
	for _, z := range obj.EnteredZones {
		zi := t.cfg.resolveZone(z)
		switch zi.Kind {
		case zoneBoundary:
			reason = "reached a doorway"
		case zoneRoom:
			if zi.Area != tr.startArea {
				reason = "walked to " + zi.Area
			}
		default:
			continue
		}
		distinct[z] = true
	}
	if reason == "" && len(distinct) >= 2 {
		reason = "entered two zones"
	}
	if reason == "" && t.peopleKnown && !t.guests && len(t.tokens) < t.peopleHome {
		reason = "fewer tokens than people home"
	}
	if reason == "" {
		return
	}
	tr.confirmed = true
	area := tr.area
	if area == "" {
		area = tr.startArea
	}
	t.attach(tr, t.newToken(now, area, tr.camera, "confirmed: "+reason, true))
}

// refresh records that a camera sees a person in the track's area.
func (t *Tracker) refresh(now time.Time, tr *track) {
	if tr.area != "" && t.cfg.Areas[tr.area].Seen {
		t.areaLastSeen[tr.area] = now
	}
	if tok := t.tokens[tr.tokenID]; tok != nil {
		tok.lastSeen = now
		tok.camera = tr.camera
	}
}

// endTrack applies the end rules.
func (t *Tracker) endTrack(now time.Time, tr *track, obj FrigateObject) {
	defer t.finishTrack(now, tr)

	if !tr.confirmed {
		tr.outcome = "dropped: never confirmed"
		return
	}
	tok := t.tokens[tr.tokenID]
	if tok == nil {
		tr.outcome = "token already gone"
		return
	}
	if t.heldByOther(tok.id, tr) {
		tr.outcome = "token stays with another camera's track"
		return
	}

	last, ok := t.lastZone(tr, obj)
	if !ok || last.Kind != zoneBoundary {
		tr.outcome = "token stays in " + tok.area
		return
	}
	if !t.cfg.Areas[last.Beyond].Seen {
		t.moveToken(now, tok, last.Beyond, tr.camera, "went into "+last.Beyond)
		tr.outcome = "went into " + last.Beyond
		return
	}
	tr.pendingTarget = last.Beyond
	tr.outcome = "waiting for a hand-off to " + last.Beyond
}

// lastZone is the zone the person was in when the track ended: the current zones, else the last
// non-empty current zones, else the last entered zone.
func (t *Tracker) lastZone(tr *track, obj FrigateObject) (zoneInfo, bool) {
	if zi, ok := t.currentZone(obj); ok {
		return zi, true
	}
	if zi, ok := t.currentZone(FrigateObject{CurrentZones: tr.lastCurrent, EnteredZones: obj.EnteredZones}); ok {
		return zi, true
	}
	for i := len(obj.EnteredZones) - 1; i >= 0; i-- {
		if zi := t.cfg.resolveZone(obj.EnteredZones[i]); zi.Kind == zoneRoom || zi.Kind == zoneBoundary {
			return zi, true
		}
	}
	return zoneInfo{}, false
}

func (t *Tracker) finishTrack(now time.Time, tr *track) {
	if _, ok := t.active[tr.id]; !ok {
		return
	}
	delete(t.active, tr.id)
	tr.end = now
	t.ended = append(t.ended, tr)
	if len(t.ended) > recentTrackLimit {
		t.ended = t.ended[len(t.ended)-recentTrackLimit:]
	}
}

// ---------------------------------------------------------------------------
// Tokens
// ---------------------------------------------------------------------------

func (t *Tracker) newToken(now time.Time, area, cam, reason string, entry bool) *token {
	t.nextToken++
	tok := &token{
		id:       fmt.Sprintf("t%d", t.nextToken),
		area:     area,
		since:    now,
		lastSeen: now,
		camera:   cam,
		reason:   reason,
	}
	t.tokens[tok.id] = tok
	if entry {
		t.signalEntry(now, area)
	}
	return tok
}

func (t *Tracker) moveToken(now time.Time, tok *token, area, cam, reason string) {
	from := tok.area
	tok.area = area
	tok.since = now
	tok.reason = reason
	if cam != "" {
		tok.camera = cam
	}
	if from != area {
		t.signalEntry(now, area)
	}
}

func (t *Tracker) removeToken(now time.Time, tok *token, reason string) {
	delete(t.tokens, tok.id)
	t.removed = append(t.removed, shadowstate.RoomPresenceRemoval{Token: tok.id, Area: tok.area, At: now, Reason: reason})
	if len(t.removed) > removalLimit {
		t.removed = t.removed[len(t.removed)-removalLimit:]
	}
	for _, tr := range t.active {
		if tr.tokenID == tok.id {
			tr.tokenID = ""
		}
	}
}

func (t *Tracker) attach(tr *track, tok *token) {
	tr.tokenID = tok.id
}

func (t *Tracker) signalEntry(now time.Time, area string) {
	t.entries = append(t.entries, area)
	t.lastEntry = &shadowstate.RoomPresenceEntry{Area: area, At: now}
}

// tokensIn lists the tokens in an area, in ID order.
func (t *Tracker) tokensIn(area string) []*token {
	var out []*token
	for _, id := range sortedKeys(t.tokens) {
		if t.tokens[id].area == area {
			out = append(out, t.tokens[id])
		}
	}
	return out
}

// unheldToken returns a token in the area that no active track holds, most recently arrived first.
func (t *Tracker) unheldToken(area string) *token {
	var best *token
	for _, tok := range t.tokensIn(area) {
		if t.held(tok.id) {
			continue
		}
		if best == nil || tok.since.After(best.since) {
			best = tok
		}
	}
	return best
}

func (t *Tracker) held(id string) bool {
	for _, tr := range t.active {
		if tr.tokenID == id {
			return true
		}
	}
	return false
}

func (t *Tracker) heldByOther(id string, self *track) bool {
	for _, tr := range t.active {
		if tr != self && tr.tokenID == id {
			return true
		}
	}
	return false
}

// lastEvidence is the time the idle limit counts from. In a seen area it is the last time a track holding
// the token reported. Another person seen in the same area is not evidence: their track holds their own
// token, so counting it would keep a phantom token alive as long as anyone stays in the room. A person
// whose track ended and who is seen again gets a new track, and the resume rule gives it this token back.
func (t *Tracker) lastEvidence(tok *token) time.Time {
	ref := tok.since
	if t.cfg.Areas[tok.area].Seen && tok.lastSeen.After(ref) {
		ref = tok.lastSeen
	}
	return ref
}

// ---------------------------------------------------------------------------
// Cameras and zones
// ---------------------------------------------------------------------------

func (t *Tracker) learnZones(obj FrigateObject) {
	for _, z := range append(append([]string(nil), obj.EnteredZones...), obj.CurrentZones...) {
		zi := t.cfg.resolveZone(z)
		switch zi.Kind {
		case zoneUnknown:
			t.unknownZones[z] = true
		case zoneRoom, zoneBoundary:
			t.addCameraArea(obj.Camera, zi.Area)
		}
	}
}

func (t *Tracker) addCameraArea(cam, area string) {
	if t.cameraAreas[cam] == nil {
		t.cameraAreas[cam] = make(map[string]bool)
	}
	t.cameraAreas[cam][area] = true
}

// unknown reports whether every camera that sees the area is offline.
func (t *Tracker) unknown(area string) bool {
	_, ok := t.frozenSince[area]
	return ok
}

// updateUnknownAreas freezes areas whose cameras all went offline, and thaws them when one returns.
// A thawed area's clocks move forward by the time it was frozen, so expiry resumes where it paused.
func (t *Tracker) updateUnknownAreas(now time.Time) {
	for area := range t.cfg.Areas {
		known, anyOnline := false, false
		for cam, areas := range t.cameraAreas {
			if !areas[area] {
				continue
			}
			c, ok := t.cameras[cam]
			if !ok {
				continue // never heard from: no evidence either way
			}
			known = true
			if c.online {
				anyOnline = true
			}
		}
		since, frozen := t.frozenSince[area]
		switch {
		case known && !anyOnline && !frozen:
			t.frozenSince[area] = now
		case frozen && (anyOnline || !known):
			pause := now.Sub(since)
			delete(t.frozenSince, area)
			for _, tok := range t.tokensIn(area) {
				tok.since = tok.since.Add(pause)
				tok.lastSeen = tok.lastSeen.Add(pause)
			}
			if seen, ok := t.areaLastSeen[area]; ok {
				t.areaLastSeen[area] = seen.Add(pause)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Shadow state
// ---------------------------------------------------------------------------

// Snapshot reports the tracker's state for shadow state.
func (t *Tracker) Snapshot() shadowstate.RoomPresenceOutputs {
	out := shadowstate.RoomPresenceOutputs{
		Outputs:       t.Outputs(),
		Areas:         make(map[string]shadowstate.RoomPresenceArea, len(t.cfg.Areas)),
		Tokens:        []shadowstate.RoomPresenceToken{},
		ActiveTracks:  []shadowstate.RoomPresenceTrack{},
		RecentTracks:  []shadowstate.RoomPresenceTrack{},
		Removed:       append([]shadowstate.RoomPresenceRemoval{}, t.removed...),
		Cameras:       make(map[string]shadowstate.RoomPresenceCamera, len(t.cameras)),
		UnknownZones:  sortedKeys(t.unknownZones),
		EventsHandled: t.eventsHandled,
		LastEventAt:   t.lastEventAt,
	}
	if t.lastEntry != nil {
		e := *t.lastEntry
		out.LastEntry = &e
	}
	for name, a := range t.cfg.Areas {
		out.Areas[name] = shadowstate.RoomPresenceArea{
			Occupied:     t.Occupied(name),
			Tokens:       len(t.tokensIn(name)),
			Seen:         a.Seen,
			Unknown:      t.unknown(name),
			LastPersonAt: t.areaLastSeen[name],
		}
	}
	for _, id := range sortedKeys(t.tokens) {
		tok := t.tokens[id]
		st := shadowstate.RoomPresenceToken{
			ID: tok.id, Area: tok.area, Since: tok.since, LastSeen: tok.lastSeen, Camera: tok.camera, Reason: tok.reason,
		}
		for _, tid := range sortedKeys(t.active) {
			if t.active[tid].tokenID == id {
				st.Tracks = append(st.Tracks, tid)
			}
		}
		out.Tokens = append(out.Tokens, st)
	}
	for _, id := range sortedKeys(t.active) {
		out.ActiveTracks = append(out.ActiveTracks, trackView(t.active[id]))
	}
	for i := len(t.ended) - 1; i >= 0; i-- {
		out.RecentTracks = append(out.RecentTracks, trackView(t.ended[i]))
	}
	for name, c := range t.cameras {
		areas := sortedKeys(t.cameraAreas[name])
		sort.Strings(areas)
		out.Cameras[name] = shadowstate.RoomPresenceCamera{LastHeartbeat: c.lastHeartbeat, Online: c.online, Areas: areas}
	}
	return out
}

func trackView(tr *track) shadowstate.RoomPresenceTrack {
	return shadowstate.RoomPresenceTrack{
		ID:        tr.id,
		Camera:    tr.camera,
		Start:     tr.start,
		End:       tr.end,
		Zones:     append([]string{}, tr.zones...),
		Rule:      tr.rule,
		Token:     tr.tokenID,
		Confirmed: tr.confirmed,
		Outcome:   tr.outcome,
	}
}
