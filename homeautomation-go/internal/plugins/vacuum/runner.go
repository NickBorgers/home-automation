package vacuum

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/housemap"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/internal/valetudo"
	"homeautomation/pkg/occupancy"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// Robot is the part of the Valetudo client the scheduler uses.
type Robot interface {
	State(ctx context.Context) (*valetudo.State, error)
	Map(ctx context.Context) (*valetudo.Map, error)
	CleanSegments(ctx context.Context, ids []string) error
	SetPreset(ctx context.Context, capability, name string) error
	Basic(ctx context.Context, action string) error
}

// Run origins.
const (
	originScheduler = "scheduler"
	originExternal  = "external"
)

// Run outcomes.
const (
	outcomeCompleted = "completed"
	outcomeAborted   = "aborted"
	outcomeStopped   = "stopped"
)

// commandTimeout bounds a pause or home command, so a robot that has gone silent fails fast and the next
// tick can try again.
const commandTimeout = 5 * time.Second

// startGrace is how long a run we sent may stay docked before it counts as not started.
const startGrace = 3 * time.Minute

// endConfirmPolls is how many polls in a row must show the run ended before it counts as ended. A robot
// with a washing dock goes back to the dock in the middle of a job to wash its mop, and to recharge.
const endConfirmPolls = 2

// dockDriftM is how far the dock may seem to move before the map frame is treated as changed. It matches
// the deployment's tracing tool.
const dockDriftM = 0.5

// Scheduler is the dirty-room scheduler. One goroutine runs every tick; Home Assistant and occupancy
// callbacks only queue events and wake it.
type Scheduler struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	timeProvider plugin.TimeProvider
	cfg          *SchedulerConfig
	houseMap     *housemap.Map
	graph        *housemap.Graph
	robot        Robot
	occupancy    *occupancy.Hub
	shadow       *shadowstate.VacuumTracker
	subHelper    *shadowstate.SubscriptionHelper

	reason    string // why the scheduler cannot run; empty when it can
	dockRoom  string
	dirtyKeys map[string]itemKey

	// tickMu serializes ticks. Everything below it, down to eventsMu, belongs to the tick.
	tickMu         sync.Mutex
	taints         []taint
	plan           *Plan
	planAt         time.Time
	lastPlanAt     time.Time
	pendingKey     string
	pendingSince   time.Time
	loggedWould    string
	run            *run
	lastRun        *run
	robotState     *valetudo.State
	robotErr       string
	lastPoll       time.Time
	robotRoom      string
	cleanedOverlay map[itemKey]time.Time
	boolOverlay    map[string]bool
	numOverlay     map[string]float64
	expectOff      map[string]bool
	warnings       map[string]string
	warnedOnce     map[string]bool

	eventsMu sync.Mutex
	events   []boolEvent

	wake        chan struct{}
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
	manualTicks bool
	unsubOcc    func()
}

// NewScheduler builds a scheduler. houseMap, robot and occ may be nil; the scheduler then reports why it
// cannot run. timeProvider nil means real time.
func NewScheduler(
	ctx context.Context,
	haClient ha.HAClient,
	stateManager *state.Manager,
	cfg *SchedulerConfig,
	houseMap *housemap.Map,
	robot Robot,
	occ *occupancy.Hub,
	tracker *shadowstate.VacuumTracker,
	registry *shadowstate.SubscriptionRegistry,
	logger *zap.Logger,
	readOnly bool,
	timeProvider plugin.TimeProvider,
) *Scheduler {
	if timeProvider == nil {
		timeProvider = plugin.RealTimeProvider{}
	}
	log := logger.Named("vacuum.scheduler")
	s := &Scheduler{
		ctx:            ctx,
		haClient:       haClient,
		stateManager:   stateManager,
		logger:         log,
		readOnly:       readOnly,
		timeProvider:   timeProvider,
		cfg:            cfg,
		houseMap:       houseMap,
		robot:          robot,
		occupancy:      occ,
		shadow:         tracker,
		subHelper:      shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, tracker, "vacuum", log),
		dirtyKeys:      map[string]itemKey{},
		cleanedOverlay: map[itemKey]time.Time{},
		boolOverlay:    map[string]bool{},
		numOverlay:     map[string]float64{},
		expectOff:      map[string]bool{},
		warnings:       map[string]string{},
		warnedOnce:     map[string]bool{},
		wake:           make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}
	s.reason = s.configure()
	return s
}

// configure checks the config against the house map. It returns why the scheduler cannot run.
func (s *Scheduler) configure() string {
	switch {
	case s.cfg.Err != nil:
		return s.cfg.Err.Error()
	case !s.cfg.Enabled():
		return "mode is off"
	case s.houseMap == nil:
		return "no house map (" + housemap.FileName + ")"
	case s.robot == nil:
		return "no Valetudo client"
	}
	s.graph = s.houseMap.Graph()

	s.dockRoom = s.cfg.DockRoom
	if s.dockRoom == "" && s.houseMap.Valetudo != nil && len(s.houseMap.Valetudo.DockM) == 2 {
		d := s.houseMap.Valetudo.DockM
		s.dockRoom = s.houseMap.SpaceAt(housemap.Point{d[0], d[1]})
	}
	if !s.graph.Has(s.dockRoom) {
		return fmt.Sprintf("dock room %q is not a room in the house map; set scheduler.dock_room or valetudo.dock_m",
			s.dockRoom)
	}
	for _, sl := range s.cfg.Sleepers {
		for _, r := range sl.Rooms {
			if !s.graph.Has(r) {
				return fmt.Sprintf("sleeper room %q (%s) is not a room in the house map", r, sl.State)
			}
		}
	}
	for _, k := range s.cfg.itemKeys() {
		s.dirtyKeys[dirtyEntity(k)] = k
	}
	return ""
}

// Start subscribes to inputs and starts the tick loop. A scheduler that cannot run only publishes why.
func (s *Scheduler) Start() error {
	if s.reason != "" {
		if s.cfg.Mode != ModeOff || s.cfg.Err != nil {
			s.logger.Warn("Vacuum scheduler not running", zap.String("reason", s.reason))
		}
		s.publish(s.timeProvider.Now())
		return nil
	}
	for _, room := range s.cfg.RoomNames() {
		if !s.graph.Has(room) {
			s.logger.Warn("Vacuum scheduler room is not in the house map; it will be skipped", zap.String("room", room))
		}
	}
	if s.houseMap.Valetudo == nil {
		s.logger.Warn("House map has no valetudo frame; robot position is not available")
	}

	if s.occupancy != nil {
		s.unsubOcc = s.occupancy.Subscribe(s.poke)
	} else {
		s.logger.Warn("No occupancy hub; every area counts as occupied and nothing will be cleaned")
	}
	for _, key := range s.sleeperVars() {
		if err := s.subHelper.SubscribeToState(key, func(string, interface{}, interface{}) { s.poke() }); err != nil {
			s.logger.Warn("Cannot subscribe to sleeper state variable; it counts as asleep",
				zap.String("key", key), zap.Error(err))
		}
	}
	for _, entity := range sortedKeys(s.dirtyKeys) {
		if err := s.subHelper.SubscribeToEntity(entity, s.queueBoolEvent); err != nil {
			s.logger.Warn("Cannot subscribe to taint helper", zap.String("entity", entity), zap.Error(err))
		}
	}
	s.subHelper.CaptureInitialInputs()

	s.logger.Info("Vacuum scheduler started",
		zap.String("mode", s.cfg.Mode), zap.Bool("read_only", s.readOnly),
		zap.String("dock_room", s.dockRoom), zap.Int("rooms", len(s.cfg.Rooms)))

	if !s.manualTicks {
		s.wg.Add(1)
		go s.loop()
	}
	return nil
}

// Stop ends the loop and the subscriptions.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	if s.unsubOcc != nil {
		s.unsubOcc()
	}
	s.subHelper.UnsubscribeAll()
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.TrackInterval)
	defer ticker.Stop()
	s.tick(true)
	for {
		select {
		case <-s.stopCh:
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.tick(false)
		case <-s.wake:
			s.tick(true)
		}
	}
}

// poke wakes the loop after an input changed. It never blocks.
func (s *Scheduler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// tick is one step: read the robot, track a run, update taints, plan, and maybe start a run. woke means an
// input changed.
//
// tickMu is held across the robot's HTTP calls, so ticks never overlap. Stop waits for an in-flight call;
// every call is bounded by the client timeout, and commands by commandTimeout.
func (s *Scheduler) tick(woke bool) {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	now := s.timeProvider.Now()
	s.warnings = map[string]string{}
	defer s.forgetClearedWarnings()

	s.processEvents()
	st, err := s.robot.State(s.ctx)
	if err != nil {
		s.robotErr = err.Error()
		s.warn("robot", "cannot read the robot: "+err.Error())
		if r := s.run; r != nil && r.origin == originScheduler && s.robotState != nil {
			// The robot cannot be read, but it may still take commands. Keep the safety rules working with
			// what was last seen: the last state and the last room.
			s.checkSafety(s.robotState, "", false)
		}
		s.evaluateTaints(now)
		s.publish(now)
		return
	}
	prev := s.robotState
	s.robotState, s.robotErr, s.lastPoll = st, "", now
	if prev == nil || prev.Battery != st.Battery || prev.Status != st.Status {
		woke = true
	}

	s.trackRun(now, st)
	s.evaluateTaints(now)
	if s.run == nil && (woke || s.pendingKey != "" || now.Sub(s.lastPlanAt) >= s.cfg.ReplanInterval) {
		s.replan(now)
	}
	s.publish(now)
}

// ---------------------------------------------------------------------------
// Planning and the start gate
// ---------------------------------------------------------------------------

// blockedAreas lists areas that are occupied or not known. Without a hub every area is blocked.
func (s *Scheduler) blockedAreas() map[string]bool {
	out := map[string]bool{}
	for _, room := range s.graph.Rooms() {
		a := s.graph.Area(room)
		if s.occupancy == nil {
			out[a] = true
			continue
		}
		occ, known := s.occupancy.Occupied(a)
		if occ || !known {
			out[a] = true
		}
	}
	return out
}

func (s *Scheduler) sleeperVars() []string {
	seen := map[string]bool{}
	var out []string
	for _, sl := range s.cfg.Sleepers {
		for _, v := range []string{sl.State, sl.Require} {
			if v != "" && !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	return out
}

// sleeperRooms lists the rooms of people asleep now. A variable that cannot be read counts as asleep, and
// a require variable that cannot be read counts as on.
func (s *Scheduler) sleeperRooms() []string {
	var out []string
	for _, sl := range s.cfg.Sleepers {
		asleep, err := s.stateManager.GetBool(sl.State)
		if err != nil {
			s.warn("sleeper:"+sl.State, "cannot read "+sl.State+"; its rooms count as asleep")
			asleep = true
		}
		if asleep && sl.Require != "" {
			req, err := s.stateManager.GetBool(sl.Require)
			if err != nil {
				s.warn("sleeper:"+sl.Require, "cannot read "+sl.Require+"; counted as on")
				req = true
			}
			asleep = req
		}
		if asleep {
			out = append(out, sl.Rooms...)
		}
	}
	return out
}

func (s *Scheduler) segments() map[string]string {
	out := make(map[string]string, len(s.cfg.Rooms))
	for name, r := range s.cfg.Rooms {
		out[name] = r.SegmentID
	}
	return out
}

// replan builds a plan and runs the start gate: the same plan must hold for StableFor before it starts.
func (s *Scheduler) replan(now time.Time) {
	st := s.robotState
	in := PlanInput{
		Graph:        s.graph,
		DockRoom:     s.dockRoom,
		Segments:     s.segments(),
		BlockedAreas: s.blockedAreas(),
		SleeperRooms: s.sleeperRooms(),
		Battery:      st.Battery,
		HasBattery:   st.HasBattery,
		Docked:       st.Docked(),
		TripCost:     s.tripCost(),
		StartMin:     s.cfg.Battery.StartMinPercent(),
		Floor:        s.cfg.Battery.FloorPercent(),
	}
	var missing []Skip
	for _, t := range s.taints {
		if len(t.Missing) > 0 {
			missing = append(missing, Skip{Room: t.Key.Room, Treatment: t.Key.Treatment,
				Reason: "missing helper " + t.Missing[0]})
			continue
		}
		if t.Dirty {
			in.Items = append(in.Items, PlanItem{Room: t.Key.Room, Treatment: t.Key.Treatment, Score: t.Score,
				Manual: t.Manual, Cost: t.Cost})
		}
	}
	p := BuildPlan(in)
	p.Skipped = append(p.Skipped, missing...)
	s.plan, s.planAt, s.lastPlanAt = &p, now, now

	if p.Empty() {
		if s.pendingKey != "" {
			s.logger.Info("Vacuum plan dropped", zap.String("reason", p.Reason))
		}
		s.pendingKey = ""
		return
	}
	if p.Key() != s.pendingKey {
		s.pendingKey, s.pendingSince = p.Key(), now
		s.logger.Info("Vacuum plan pending", zap.String("plan", p.Key()), zap.Float64("cost", p.Cost),
			zap.Float64("budget", p.Budget), zap.Duration("stable_for", s.cfg.StableFor))
		return
	}
	if now.Sub(s.pendingSince) < s.cfg.StableFor {
		return
	}
	s.startRun(now, p)
}

// startRun sets the treatment's presets and starts a segment clean. In log_only mode or read-only, it only
// logs the run it would start.
func (s *Scheduler) startRun(now time.Time, p Plan) {
	if s.cfg.Mode != ModeActive || s.readOnly {
		if s.loggedWould != p.Key() {
			s.loggedWould = p.Key()
			s.logger.Info("Vacuum would start a run", zap.String("mode", s.cfg.Mode),
				zap.Bool("read_only", s.readOnly), zap.String("treatment", string(p.Treatment)),
				zap.Strings("rooms", p.Rooms), zap.Strings("segments", p.Segments),
				zap.Float64("cost", p.Cost), zap.Float64("budget", p.Budget))
		}
		return
	}
	tc := s.cfg.Treatments[p.Treatment]
	for _, preset := range []struct{ capability, name string }{
		{valetudo.CapabilityOperationMode, tc.OperationMode},
		{valetudo.CapabilityFanSpeed, tc.FanSpeed},
		{valetudo.CapabilityWaterUsage, tc.WaterGrade},
	} {
		if preset.name == "" {
			continue
		}
		if err := s.robot.SetPreset(s.ctx, preset.capability, preset.name); err != nil {
			s.startFailed(now, fmt.Errorf("set %s %q: %w", preset.capability, preset.name, err))
			return
		}
	}
	if err := s.robot.CleanSegments(s.ctx, p.Segments); err != nil {
		s.startFailed(now, fmt.Errorf("start segment clean: %w", err))
		return
	}
	s.logger.Info("Vacuum run started", zap.String("treatment", string(p.Treatment)),
		zap.Strings("rooms", p.Rooms), zap.Strings("segments", p.Segments),
		zap.Float64("cost", p.Cost), zap.Float64("budget", p.Budget))
	s.run = newRun(originScheduler, p.Treatment, p.Rooms, now, s.robotState)
	s.pendingKey = ""
}

// startFailed restarts the gate, so a robot that refuses is not asked again every tick.
func (s *Scheduler) startFailed(now time.Time, err error) {
	s.logger.Error("Vacuum run did not start", zap.Error(err))
	s.robotErr = err.Error()
	s.pendingSince = now
}

// ---------------------------------------------------------------------------
// Tracking
// ---------------------------------------------------------------------------

// run is a run being tracked.
type run struct {
	origin    string
	treatment Treatment // "" for an external run that clears nothing, such as mop only
	targets   []string  // in order; nil for an external run
	startedAt time.Time
	// leftDock is true once the robot was seen anywhere but docked. Until then "docked" means "not started yet".
	leftDock bool
	// endPolls counts polls in a row that show the run ended.
	endPolls int

	startBattery, lastBattery int
	haveBattery               bool

	current       string
	done          []string
	doneSet       map[string]bool
	room          string // room at the last poll; "" when unknown
	roomSince     time.Time
	lastKnownRoom string
	roomDrop      map[string]float64
	sampled       map[string]float64
	totalDrop     float64
	positionSeen  bool

	aborted     bool
	abortReason string
	paused      bool
	homeSent    bool
	endedAt     time.Time
	outcome     string
}

func newRun(origin string, t Treatment, targets []string, now time.Time, st *valetudo.State) *run {
	r := &run{
		origin: origin, treatment: t, targets: append([]string(nil), targets...), startedAt: now,
		doneSet: map[string]bool{}, roomDrop: map[string]float64{},
		sampled: map[string]float64{},
	}
	if st != nil && st.HasBattery {
		r.startBattery, r.lastBattery, r.haveBattery = st.Battery, st.Battery, true
	}
	return r
}

// externalTreatment is what a run someone else started does, from the robot's operation mode. Mop only
// clears nothing: a full clean needs vacuum and mop.
func externalTreatment(st *valetudo.State) Treatment {
	switch st.Presets[valetudo.PresetOperationMode] {
	case "vacuum_and_mop":
		return TreatmentFull
	case "", "vacuum":
		return TreatmentVacuum
	default:
		return ""
	}
}

// nextTarget is the first target the run has not finished and is not cleaning now.
func (r *run) nextTarget() string {
	for _, t := range r.targets {
		if !r.doneSet[t] && t != r.current {
			return t
		}
	}
	return ""
}

func (r *run) remaining() []string {
	var out []string
	for _, t := range r.targets {
		if !r.doneSet[t] {
			out = append(out, t)
		}
	}
	return out
}

// trackRun starts, follows and ends runs. The caller holds tickMu.
func (s *Scheduler) trackRun(now time.Time, st *valetudo.State) {
	if s.run == nil {
		if st.Status != valetudo.StatusCleaning {
			s.robotRoom = ""
			return
		}
		s.run = newRun(originExternal, externalTreatment(st), nil, now, st)
		s.logger.Info("Vacuum run started elsewhere; tracking it",
			zap.String("treatment", string(s.run.treatment)),
			zap.String("operation_mode", st.Presets[valetudo.PresetOperationMode]))
	}
	r := s.run

	room, posOK := s.locate()
	s.observe(r, now, st, room, posOK)
	if posOK {
		s.robotRoom = room
	} else {
		s.robotRoom = ""
	}

	if st.Status != valetudo.StatusDocked {
		r.leftDock = true
	}
	// A job in progress keeps the flag "resumable" while the robot is docked, returning, paused or idle, for
	// example to wash its mop or to recharge. A dock that is washing also means the job may go on. Only a
	// docked or idle robot with neither counts as the end, and only after endConfirmPolls polls in a row.
	// Until then the room being cleaned is not finished: the robot may come back to it.
	midJob := st.Flag == valetudo.FlagResumable || st.DockStatus == valetudo.DockStatusCleaning
	ended := (st.Status == valetudo.StatusDocked || st.Status == valetudo.StatusIdle) && !midJob
	if !ended || !r.leftDock {
		r.endPolls = 0
	}
	switch {
	case st.Status == valetudo.StatusDocked && !r.leftDock:
		// Our run was just sent; the robot has not left the dock yet. Give it a little while.
		if now.Sub(r.startedAt) > startGrace {
			s.logger.Warn("Vacuum run was sent but the robot never left the dock")
			s.finish(now, outcomeStopped)
			return
		}
	case ended:
		r.endPolls++
		if r.endPolls >= endConfirmPolls {
			outcome := outcomeCompleted
			switch {
			case r.aborted:
				outcome = outcomeAborted
			case st.Status == valetudo.StatusIdle:
				outcome = outcomeStopped
			}
			s.finish(now, outcome)
			return
		}
	}
	if r.origin == originScheduler {
		s.checkSafety(st, room, posOK)
	}
}

// observe takes one poll: battery drop goes to the room the robot was in at the previous poll, and room
// changes follow the tour. The caller holds tickMu.
func (s *Scheduler) observe(r *run, now time.Time, st *valetudo.State, room string, posOK bool) {
	if st.HasBattery {
		if r.haveBattery && st.Battery < r.lastBattery {
			drop := float64(r.lastBattery - st.Battery)
			r.totalDrop += drop
			if r.room != "" {
				r.roomDrop[r.room] += drop
			}
		}
		if !r.haveBattery {
			r.startBattery, r.haveBattery = st.Battery, true
		}
		r.lastBattery = st.Battery
	}
	if !posOK {
		r.room = ""
		return
	}
	r.positionSeen = true
	if room != r.room {
		r.room, r.roomSince = room, now
	}
	if room != "" {
		r.lastKnownRoom = room
	}
	// Only a cleaning robot moves through the tour. Time spent docked, returning or paused, such as a mop
	// wash in the middle of a job, does not count as dwelling in a room.
	cleaning := st.Status == valetudo.StatusCleaning && st.Flag != valetudo.FlagResumable
	if !cleaning {
		r.roomSince = now
	}
	if r.aborted || !cleaning || room == "" || room == r.current || r.doneSet[room] {
		return
	}
	target := false
	if r.origin == originScheduler {
		for _, t := range r.targets {
			target = target || t == room
		}
	} else {
		_, target = s.cfg.Rooms[room]
	}
	if !target {
		return
	}
	// The next room of our own tour counts at once. Any other room, such as a room the robot drives through
	// or a room of an external run, counts once the robot has stayed in it for MinDwell.
	expected := r.origin == originScheduler && room == r.nextTarget()
	if !expected && now.Sub(r.roomSince) < s.cfg.MinDwell {
		return
	}
	if r.current != "" {
		s.markDone(now, r.current)
	}
	r.current = room
	s.logger.Info("Vacuum entered room", zap.String("room", room), zap.String("origin", r.origin))
}

// markDone records that the robot finished a room: taints cleared and a battery cost sample learned.
func (s *Scheduler) markDone(now time.Time, room string) {
	r := s.run
	if r.doneSet[room] {
		return
	}
	r.doneSet[room] = true
	r.done = append(r.done, room)

	// A full clean clears both taints. A vacuum-only clean clears the vacuum taint.
	var cleared []Treatment
	if rc, ok := s.cfg.Rooms[room]; ok {
		if r.treatment == TreatmentFull {
			cleared = append(cleared, TreatmentFull)
		}
		if (r.treatment == TreatmentFull || r.treatment == TreatmentVacuum) && rc.Vacuum > 0 {
			cleared = append(cleared, TreatmentVacuum)
		}
	}
	names := make([]string, 0, len(cleared))
	for _, t := range cleared {
		s.markCleaned(itemKey{Room: room, Treatment: t}, now)
		names = append(names, string(t))
	}
	r.sampled[room] = r.roomDrop[room]
	if len(cleared) > 0 && cleared[0] == r.treatment && r.positionSeen {
		s.learnItem(itemKey{Room: room, Treatment: r.treatment}, r.roomDrop[room])
		primaryRate := s.cfg.Battery.DefaultCostPerM2[r.treatment]
		for _, treatment := range cleared[1:] {
			s.learnItem(itemKey{Room: room, Treatment: treatment},
				r.roomDrop[room]*s.cfg.Battery.DefaultCostPerM2[treatment]/primaryRate)
		}
	}
	s.logger.Info("Vacuum finished room", zap.String("room", room), zap.String("origin", r.origin),
		zap.Strings("cleared", names), zap.Float64("battery_drop", r.roomDrop[room]),
		zap.String("mode", s.cfg.Mode))
}

// finish ends the tracked run. A completed run finishes its current room and teaches the trip cost.
func (s *Scheduler) finish(now time.Time, outcome string) {
	r := s.run
	if outcome == outcomeCompleted {
		if r.current != "" {
			s.markDone(now, r.current)
		}
		switch {
		case r.origin == originScheduler && !r.positionSeen:
			s.finishWithoutPosition(now)
		case r.positionSeen && len(r.done) > 0:
			trip := r.totalDrop
			for _, room := range r.done {
				trip -= r.sampled[room]
			}
			if trip >= 0 {
				s.learnTrip(trip)
			}
		}
	}
	r.current = ""
	r.endedAt, r.outcome = now, outcome
	s.logger.Info("Vacuum run ended", zap.String("origin", r.origin), zap.String("outcome", outcome),
		zap.Strings("done", r.done), zap.Strings("unfinished", r.remaining()),
		zap.Float64("battery_drop", r.totalDrop), zap.String("abort_reason", r.abortReason))
	s.lastRun, s.run = r, nil
	s.lastPlanAt = time.Time{} // plan again right away
}

// finishWithoutPosition handles a completed run of ours when the map position never worked: every target
// counts as done, and the battery drop beyond the trip is split by floor area.
func (s *Scheduler) finishWithoutPosition(now time.Time) {
	r := s.run
	trip := s.tripCost()
	rest := math.Max(0, r.totalDrop-trip)
	var total float64
	for _, room := range r.targets {
		total += s.graph.FloorArea(room)
	}
	s.logger.Warn("Vacuum run finished without robot position; marking every target done",
		zap.Strings("targets", r.targets))
	for _, room := range r.targets {
		if r.doneSet[room] {
			continue
		}
		s.markDone(now, room)
		if total > 0 && r.treatment != "" {
			s.learnItem(itemKey{Room: room, Treatment: r.treatment}, rest*s.graph.FloorArea(room)/total)
		}
	}
}

// locate reads the robot's room from the Valetudo map. ok is false when the position cannot be trusted.
func (s *Scheduler) locate() (room string, ok bool) {
	v := s.houseMap.Valetudo
	if v == nil {
		s.warn("position", "house map has no valetudo frame; robot position is not available")
		return "", false
	}
	m, err := s.robot.Map(s.ctx)
	if err != nil {
		s.warn("position", "cannot read the robot map: "+err.Error())
		return "", false
	}
	if math.Abs(m.PixelSize*0.01-v.PixelSizeM) > 1e-9 {
		s.warn("position", fmt.Sprintf("Valetudo pixel size %g cm does not match pixel_size_m %g", m.PixelSize,
			v.PixelSizeM))
		return "", false
	}
	if dock, has := m.ChargerPixel(); has && len(v.DockM) == 2 {
		at := v.ToFrame(housemap.Point{dock[0], dock[1]})
		if d := housemap.Distance(at, housemap.Point{v.DockM[0], v.DockM[1]}); d > dockDriftM {
			s.warn("position", fmt.Sprintf("the dock moved %.1f m in the house frame; the robot probably made a "+
				"new map. Fit the valetudo frame again", d))
			return "", false
		}
	}
	px, has := m.RobotPixel()
	if !has {
		s.warn("position", "the robot map has no robot position")
		return "", false
	}
	return s.houseMap.SpaceAt(v.ToFrame(housemap.Point{px[0], px[1]})), true
}

// ---------------------------------------------------------------------------
// Live safety
// ---------------------------------------------------------------------------

// checkSafety re-checks our own run. When the rest of the tour becomes unsafe, the robot goes home if its
// way home is clear, and pauses where it is if not. A paused robot goes home once the way clears. The caller
// holds tickMu.
//
// The way home is checked for as long as the robot is on its way home: after an abort, after a pause, and
// while it returns at the end of the run.
func (s *Scheduler) checkSafety(st *valetudo.State, room string, posOK bool) {
	r := s.run
	restr := ComputeRestrictions(s.graph, s.blockedAreas(), s.sleeperRooms())

	// Where the robot is. Without a trusted position, assume the last room seen, else the dock.
	at := room
	if !posOK || at == "" || !s.graph.Has(at) {
		at = r.lastKnownRoom
	}
	if !s.graph.Has(at) {
		at = s.dockRoom
	}

	goingHome := r.aborted || r.paused || st.Status == valetudo.StatusReturning
	if !goingHome {
		reason := s.unsafeReason(st, restr, at)
		if reason == "" {
			return
		}
		r.aborted, r.abortReason = true, reason
		s.logger.Warn("Vacuum run is no longer safe", zap.String("reason", reason), zap.String("robot_room", at),
			zap.Strings("unfinished", r.remaining()))
	}

	crossed, ok := LegCrosses(s.graph, at, s.dockRoom, restr.NoEnter)
	clear := ok && crossed == ""
	switch {
	case clear && (r.paused || (r.aborted && !r.homeSent)):
		why := r.abortReason
		if r.paused {
			why = "way home is clear again"
		}
		if s.command(valetudo.ActionHome, why) == nil {
			r.homeSent, r.paused = true, false
		}
	case !clear && !r.paused:
		why := "way home is blocked"
		if crossed != "" {
			why += ": " + crossed
		}
		if s.command(valetudo.ActionPause, why) == nil {
			r.paused, r.homeSent = true, false
		}
	}
}

// unsafeReason says why the rest of the run is no longer safe, or "".
func (s *Scheduler) unsafeReason(st *valetudo.State, restr Restrictions, at string) string {
	if st.HasBattery && float64(st.Battery) <= s.cfg.Battery.FloorPercent() {
		return fmt.Sprintf("battery %d%% reached the floor %g%%", st.Battery, s.cfg.Battery.FloorPercent())
	}
	prev := at
	for _, room := range s.run.remaining() {
		if why, bad := restr.NoClean[room]; bad {
			return "room " + room + ": " + why
		}
		if crossed, ok := LegCrosses(s.graph, prev, room, restr.NoEnter); !ok || crossed != "" {
			return legReason(prev, room, crossed, restr.NoEnter[crossed], ok)
		}
		prev = room
	}
	if crossed, ok := LegCrosses(s.graph, prev, s.dockRoom, restr.NoEnter); !ok || crossed != "" {
		return legReason(prev, s.dockRoom, crossed, restr.NoEnter[crossed], ok)
	}
	return ""
}

// command sends a basic control action. Only an active, writable scheduler sends anything.
func (s *Scheduler) command(action, why string) error {
	if s.cfg.Mode != ModeActive || s.readOnly {
		s.logger.Info("Vacuum would send command", zap.String("action", action), zap.String("why", why),
			zap.String("mode", s.cfg.Mode), zap.Bool("read_only", s.readOnly))
		return nil
	}
	ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
	defer cancel()
	if err := s.robot.Basic(ctx, action); err != nil {
		s.logger.Error("Vacuum command failed", zap.String("action", action), zap.Error(err))
		return err
	}
	s.logger.Info("Vacuum command sent", zap.String("action", action), zap.String("why", why))
	return nil
}

// ---------------------------------------------------------------------------
// Shadow state
// ---------------------------------------------------------------------------

func (s *Scheduler) location() *time.Location {
	return time.Local
}

// publish replaces the scheduler's shadow state.
func (s *Scheduler) publish(now time.Time) {
	o := &shadowstate.VacuumSchedulerOutputs{
		Mode:       s.cfg.Mode,
		ReadOnly:   s.readOnly,
		Configured: s.reason == "",
		Reason:     s.reason,
		DockRoom:   s.dockRoom,
		Items:      []shadowstate.VacuumItem{},
		Warnings:   s.warningList(),
		UpdatedAt:  now,
	}
	if s.reason != "" {
		s.shadow.SetScheduler(o)
		return
	}
	o.Robot = shadowstate.VacuumRobot{Room: s.robotRoom, LastPollAt: s.lastPoll, LastError: s.robotErr}
	if st := s.robotState; st != nil {
		o.Robot.Status, o.Robot.Flag, o.Robot.Battery, o.Robot.HasBattery = st.Status, st.Flag, st.Battery, st.HasBattery
	}
	for _, t := range s.taints {
		item := shadowstate.VacuumItem{
			Room: t.Key.Room, Treatment: string(t.Key.Treatment), Dirty: t.Dirty, Manual: t.Manual,
			IntervalHours: t.Interval.Hours(), Score: math.Round(t.Score*100) / 100,
			Cost: math.Round(t.Cost*10) / 10, Missing: append([]string(nil), t.Missing...),
		}
		if !t.LastCleaned.IsZero() {
			lc := t.LastCleaned
			item.LastCleaned = &lc
		}
		o.Items = append(o.Items, item)
	}
	tripSt, tripOK := s.getState(tripEntity)
	if v, has := s.number(tripEntity, tripSt, tripOK); has {
		o.TripCost = v
	} else {
		o.TripCost = s.cfg.Battery.DefaultTripCost
	}
	if p := s.plan; p != nil {
		vp := &shadowstate.VacuumPlan{
			PlannedAt: s.planAt, Treatment: string(p.Treatment), Rooms: append([]string{}, p.Rooms...),
			Segments: append([]string{}, p.Segments...), Cost: p.Cost, Budget: p.Budget, Reason: p.Reason,
			NoEnter: append([]string{}, p.NoEnter...), NoClean: append([]string{}, p.NoClean...),
			Skipped: []shadowstate.VacuumSkip{},
		}
		for _, sk := range p.Skipped {
			vp.Skipped = append(vp.Skipped, shadowstate.VacuumSkip{Room: sk.Room, Treatment: string(sk.Treatment),
				Reason: sk.Reason})
		}
		if s.pendingKey != "" && s.pendingKey == p.Key() {
			since, starts := s.pendingSince, s.pendingSince.Add(s.cfg.StableFor)
			vp.PendingSince, vp.StartsAt = &since, &starts
		}
		o.Plan = vp
	}
	o.Run = runView(s.run)
	o.LastRun = runView(s.lastRun)
	s.shadow.SetScheduler(o)
}

func runView(r *run) *shadowstate.VacuumRun {
	if r == nil {
		return nil
	}
	v := &shadowstate.VacuumRun{
		Origin: r.origin, Treatment: string(r.treatment), Targets: append([]string(nil), r.targets...),
		Done: append([]string{}, r.done...), Current: r.current, StartedAt: r.startedAt, Outcome: r.outcome,
		StartBattery: r.startBattery, Battery: r.lastBattery, Aborted: r.aborted, AbortReason: r.abortReason,
		Paused: r.paused, PositionAvailable: r.positionSeen,
	}
	if !r.endedAt.IsZero() {
		e := r.endedAt
		v.EndedAt = &e
	}
	return v
}

// ---------------------------------------------------------------------------
// Test hooks
// ---------------------------------------------------------------------------

// SetManualTicksForTest stops Start from launching the loop; the test calls TickForTest instead. Tests only.
func (s *Scheduler) SetManualTicksForTest() {
	s.manualTicks = true
}

// TickForTest runs one tick. woke says an input changed. Tests only.
func (s *Scheduler) TickForTest(woke bool) {
	s.tick(woke)
}
