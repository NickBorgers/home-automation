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
)

// How many ended tracks and removals to keep for shadow state.
const (
	recentTrackLimit = 30
	removalLimit     = 20
)

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
	started    bool     // the first zone has been handled
	tokenID    string
	startArea  string
	area       string // area at the last message
	rule       string
	confirmed  bool
	outcome    string

	// pendingTarget is a seen area the token moves to when no other camera picks the person up.
	pendingTarget string
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
		cfg:          cfg,
		tokens:       make(map[string]*token),
		active:       make(map[string]*track),
		areaLastSeen: make(map[string]time.Time),
		frozenSince:  make(map[string]time.Time),
		cameras:      make(map[string]*camera),
		cameraAreas:  make(map[string]map[string]bool),
		unknownZones: make(map[string]bool),
		anyoneHome:   true,
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
		t.startTrack(now, tr, first)
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
	t.enforceCount(now)
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
	t.enforceCount(now)
}

// BedOccupied makes sure an area with an occupied bed holds a token.
func (t *Tracker) BedOccupied(now time.Time, area string) {
	if _, ok := t.cfg.Areas[area]; !ok || len(t.tokensIn(area)) > 0 {
		return
	}
	t.newToken(now, area, "", "bed sensor is on", false)
	t.enforceCount(now)
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

	for _, tr := range t.ended {
		if tr.pendingTarget == "" || now.Sub(tr.end) < t.cfg.HandoffWindow {
			continue
		}
		target := tr.pendingTarget
		tr.pendingTarget = ""
		if tok, ok := t.tokens[tr.tokenID]; ok && !t.held(tok.id) {
			t.moveToken(now, tok, target, tr.camera, "no camera picked the person up; moved to "+target)
		}
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

	t.enforceCount(now)
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

func (t *Tracker) startTrack(now time.Time, tr *track, first zoneInfo) {
	tr.started = true
	tr.startArea = first.Area
	tr.area = first.Area

	if first.Kind == zoneBoundary {
		// Rule 1: the person came out through the doorway.
		if t.cfg.Areas[first.Beyond].Seen {
			if t.tryHandoff(now, tr, first.Area, first.Beyond) {
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
		t.attach(tr, t.newToken(now, first.Area, tr.camera, reason+" (no token was there)", true))
		return
	}

	// Rule 2: the same person, handed off from another track.
	if t.tryHandoff(now, tr, first.Area, "") {
		return
	}

	// A person who stood still long enough for Frigate to lose them moved again.
	if tok := t.unheldToken(first.Area); tok != nil {
		tr.rule = ruleResume
		tr.confirmed = true
		t.attach(tr, tok)
		return
	}

	// Rule 3: a person came back from an unseen area and was tracked only after the doorway.
	if tok := t.nearDoorwayToken(first.Area); tok != nil {
		tr.rule = ruleNearDoorway
		tr.confirmed = true
		from := tok.area
		t.attach(tr, tok)
		t.moveToken(now, tok, first.Area, tr.camera, "came back from "+from)
		return
	}

	// Rule 4: appeared from nowhere.
	tr.rule = ruleUnconfirmed
}

// tryHandoff attaches tr to the token of a track that just ended nearby, or that another camera is
// following nearby right now. via is a seen area beyond a boundary the track started in, or "".
func (t *Tracker) tryHandoff(now time.Time, tr *track, area, via string) bool {
	near := func(a string) bool { return t.cfg.neighbors(a, area) || (via != "" && t.cfg.neighbors(a, via)) }

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

// nearDoorwayToken finds a token in an unseen area next to area. The most recent arrival wins.
func (t *Tracker) nearDoorwayToken(area string) *token {
	var best *token
	for _, name := range sortedKeys(t.cfg.Areas) {
		if name == area || t.cfg.Areas[name].Seen || !t.cfg.neighbors(area, name) {
			continue
		}
		for _, tok := range t.tokensIn(name) {
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

	last, ok := t.lastZone(obj)
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

// lastZone is the zone the person was in when the track ended.
func (t *Tracker) lastZone(obj FrigateObject) (zoneInfo, bool) {
	if zi, ok := t.currentZone(obj); ok {
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

// lastEvidence is the time the idle limit counts from.
func (t *Tracker) lastEvidence(tok *token) time.Time {
	ref := tok.since
	if t.cfg.Areas[tok.area].Seen {
		if seen := t.areaLastSeen[tok.area]; seen.After(ref) {
			ref = seen
		}
	}
	return ref
}

// enforceCount drops the least recently seen tokens while there are more tokens than people home.
func (t *Tracker) enforceCount(now time.Time) {
	if !t.peopleKnown || t.guests || t.peopleHome <= 0 {
		return
	}
	for len(t.tokens) > t.peopleHome {
		var drop *token
		for _, id := range sortedKeys(t.tokens) {
			tok := t.tokens[id]
			if drop == nil || t.lastEvidence(tok).Before(t.lastEvidence(drop)) {
				drop = tok
			}
		}
		t.removeToken(now, drop, "more tokens than people home")
	}
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
