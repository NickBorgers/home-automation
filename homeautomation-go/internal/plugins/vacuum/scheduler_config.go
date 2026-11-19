package vacuum

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scheduler modes, like room presence.
const (
	ModeOff     = "off"
	ModeLogOnly = "log_only"
	ModeActive  = "active"
)

// Treatment is what a run does to a room.
type Treatment string

// Treatments. A full clean vacuums and mops. It also clears the vacuum taint.
const (
	TreatmentFull   Treatment = "full"
	TreatmentVacuum Treatment = "vacuum"
)

// Scheduler defaults.
const (
	defaultStartMin       = 70.0
	defaultFloor          = 30.0
	defaultLearningRate   = 0.3
	defaultTripCost       = 5.0
	defaultCostPerM2Full  = 0.6
	defaultCostPerM2Vac   = 0.4
	defaultReplanInterval = 5 * time.Minute
	defaultStableFor      = 10 * time.Minute
	defaultTrackInterval  = 30 * time.Second
	defaultTravelTrack    = 5 * time.Second
	defaultMinDwell       = 2 * time.Minute
	defaultNextRoomDwell  = time.Minute
	defaultStallTimeout   = 30 * time.Minute
)

// defaultWaterEmptyError is the error text of a Dreame mop dock whose clean water tank is empty.
const defaultWaterEmptyError = "Clean Water Tank empty"

// Fencing and stranded-robot defaults.
const (
	defaultFenceMarginM  = 0.25
	maxFenceMarginM      = 1.0
	defaultStrandedAfter = 5 * time.Minute
	// defaultFenceHelperGrace is how long input_text.vacuum_fence_saved may read "unknown" before the
	// scheduler treats it as empty.
	defaultFenceHelperGrace = 2 * time.Minute
)

// roomNamePattern keeps room names usable inside Home Assistant entity ids.
var roomNamePattern = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

// SchedulerConfig is the dirty-room scheduler's part of vacuum_config.yaml.
type SchedulerConfig struct {
	// Mode is off, log_only or active. Empty means off.
	Mode string `yaml:"mode"`

	// ValetudoURL is the robot's base URL, such as http://192.0.2.10.
	ValetudoURL string `yaml:"valetudo_url"`

	// DockRoom is the house-map room the dock stands in. Empty means the room at valetudo.dock_m.
	DockRoom string `yaml:"dock_room"`

	ReplanIntervalRaw string `yaml:"replan_interval"`
	StableForRaw      string `yaml:"stable_for"`
	TrackIntervalRaw  string `yaml:"track_interval"`
	MinDwellRaw       string `yaml:"min_dwell"`
	// NextRoomDwellRaw is how long the robot must stay in the next room of our own tour, without going back
	// to the room it cleans, before that room counts and the room before it is done. It keeps a brief blip
	// over an open boundary from ending a room early. Default 1m: two track_interval polls after the first.
	NextRoomDwellRaw string `yaml:"next_room_dwell"`
	// StallTimeoutRaw is how long a run may make no progress before it ends as stalled or error. See
	// stallPoll in runner.go for what counts as no progress. Default 30m.
	StallTimeoutRaw string `yaml:"stall_timeout"`

	// TravelFanSpeed is the fan preset, such as "low", for the robot's drives between rooms on a run the
	// scheduler started in active mode. The treatment's fan speed is set only while the robot is in the room
	// it is cleaning. Empty turns quiet travel off: the treatment's fan speed is set for the whole run.
	TravelFanSpeed string `yaml:"travel_fan_speed"`
	// TravelTrackIntervalRaw is how often the robot is polled while quiet travel is on and the robot is not
	// in the room it is cleaning, so the fan goes up soon after it arrives. Default 5s.
	TravelTrackIntervalRaw string `yaml:"travel_track_interval"`

	Battery    BatteryConfig                 `yaml:"battery"`
	Treatments map[Treatment]TreatmentConfig `yaml:"treatments"`
	Sleepers   []SleeperConfig               `yaml:"sleepers"`
	// HoldWhile lists conditions on boolean state variables, such as isTVPlaying true. While any holds, no
	// new run starts. A run already going goes on.
	HoldWhile []HoldCondition `yaml:"hold_while"`

	// Rooms are the rooms the scheduler cleans, by house-map room name. Other rooms are only driven through.
	Rooms map[string]RoomConfig `yaml:"rooms"`

	// FenceNoEnter puts Valetudo virtual walls across the doors into no_enter rooms before a run the
	// scheduler starts, and takes them away when the run ends. Default true. See fence.go.
	FenceNoEnter *bool `yaml:"fence_no_enter"`
	// FenceMarginM is how far, in metres, each wall reaches past both door jambs. Default 0.25.
	FenceMarginM *float64 `yaml:"fence_margin_m"`
	// FenceHelperGraceRaw is how long input_text.vacuum_fence_saved may read "unknown" at startup before it
	// counts as empty. Default 2m; 0 means at once. "unavailable" is never treated as empty.
	FenceHelperGraceRaw string `yaml:"fence_helper_grace"`
	// FenceHelperGrace is the parsed fence_helper_grace.
	FenceHelperGrace time.Duration `yaml:"-"`
	// StrandedAfterRaw is how long the robot may stand idle or paused away from the dock, with no run
	// tracked, before an active scheduler sends it home. Default 5m.
	StrandedAfterRaw string `yaml:"stranded_after"`
	// StrandedAfter is the parsed stranded_after.
	StrandedAfter time.Duration `yaml:"-"`

	// WaterEmptyError is text in the error sensor's state that means the mop dock's clean water tank is
	// empty, matched without regard to case. While the tank is empty, runs are vacuum only. Default
	// "Clean Water Tank empty". See water.go.
	WaterEmptyError string `yaml:"water_empty_error"`
	// errorSensorID and noErrorValue come from the vacuum: section. An empty errorSensorID turns the
	// out-of-water rule off.
	errorSensorID string
	noErrorValue  string

	// Parsed by validate.
	ReplanInterval time.Duration `yaml:"-"`
	StableFor      time.Duration `yaml:"-"`
	TrackInterval  time.Duration `yaml:"-"`
	MinDwell       time.Duration `yaml:"-"`
	NextRoomDwell  time.Duration `yaml:"-"`
	StallTimeout   time.Duration `yaml:"-"`
	// TravelTrackInterval is the parsed travel_track_interval.
	TravelTrackInterval time.Duration `yaml:"-"`

	// Err says why the scheduler cannot run. The error announcer keeps working either way.
	Err error `yaml:"-"`
}

// BatteryConfig holds the battery rules.
type BatteryConfig struct {
	// StartMin is the lowest battery percent a run may start at.
	StartMin *float64 `yaml:"start_min"`
	// Floor is the battery percent a run must still have when it ends.
	Floor *float64 `yaml:"floor"`
	// LearningRate weighs a new cost sample in the moving average.
	LearningRate float64 `yaml:"learning_rate"`
	// DefaultCostPerM2 is the first guess of battery percent per square metre, per treatment.
	DefaultCostPerM2 map[Treatment]float64 `yaml:"default_cost_per_m2"`
	// DefaultTripCost is the first guess of battery percent to drive out and back.
	DefaultTripCost float64 `yaml:"default_trip_cost"`
}

// TreatmentConfig holds the Valetudo presets a treatment sets before a run. An empty preset is left as it is.
type TreatmentConfig struct {
	OperationMode string `yaml:"operation_mode"`
	FanSpeed      string `yaml:"fan_speed"`
	WaterGrade    string `yaml:"water_grade"`
}

// SleeperConfig names a sleep state variable and the rooms it protects.
type SleeperConfig struct {
	// State is a boolean state variable, such as isMasterAsleep.
	State string `yaml:"state"`
	// Require is an optional boolean state variable that must also be on, such as isHaveGuests.
	Require string `yaml:"require"`
	// Rooms are the sleeper's rooms. They and their door neighbours are not cleaned while State is on.
	Rooms []string `yaml:"rooms"`
}

// HoldCondition holds new runs while a boolean state variable has a value. It has the shape of the
// variable conditions in the music and lighting configs.
type HoldCondition struct {
	// Variable is a boolean state variable, such as isTVPlaying.
	Variable string `yaml:"variable"`
	// Value is the value that holds runs.
	Value bool `yaml:"value"`
}

// RoomConfig is one cleanable room.
type RoomConfig struct {
	// SegmentID is the room's Valetudo segment id.
	SegmentID string `yaml:"segment_id"`
	// FullRaw is how long after a full clean the room is dirty again. Required.
	FullRaw string `yaml:"full"`
	// VacuumRaw, when set, adds a vacuum-only taint with its own interval.
	VacuumRaw string `yaml:"vacuum"`

	Full   time.Duration `yaml:"-"`
	Vacuum time.Duration `yaml:"-"`
}

// FenceEnabled reports whether fence_no_enter is on, after defaults.
func (c *SchedulerConfig) FenceEnabled() bool { return c.FenceNoEnter == nil || *c.FenceNoEnter }

// FenceMargin is fence_margin_m after defaults.
func (c *SchedulerConfig) FenceMargin() float64 {
	if c.FenceMarginM == nil {
		return defaultFenceMarginM
	}
	return *c.FenceMarginM
}

// StartMinPercent is the start minimum after defaults.
func (b BatteryConfig) StartMinPercent() float64 { return *b.StartMin }

// FloorPercent is the floor after defaults.
func (b BatteryConfig) FloorPercent() float64 { return *b.Floor }

// Enabled reports whether the scheduler should run.
func (c *SchedulerConfig) Enabled() bool {
	return (c.Mode == ModeLogOnly || c.Mode == ModeActive) && c.Err == nil
}

// Interval is how long after a clean an item is dirty again. 0 means the room has no such taint.
func (r RoomConfig) Interval(t Treatment) time.Duration {
	if t == TreatmentVacuum {
		return r.Vacuum
	}
	return r.Full
}

// RoomNames lists the configured rooms, sorted.
func (c *SchedulerConfig) RoomNames() []string {
	names := make([]string, 0, len(c.Rooms))
	for n := range c.Rooms {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// applyDefaultsAndValidate fills defaults and records any problem in Err. It never fails the whole config:
// a broken scheduler section must not stop the error announcer.
func (c *SchedulerConfig) applyDefaultsAndValidate() {
	c.Err = c.validate()
}

func (c *SchedulerConfig) validate() error {
	if c.Mode == "" {
		c.Mode = ModeOff
	}
	switch c.Mode {
	case ModeOff, ModeLogOnly, ModeActive:
	default:
		return fmt.Errorf("scheduler.mode %q must be off, log_only or active", c.Mode)
	}

	var err error
	for _, d := range []struct {
		name string
		raw  string
		def  time.Duration
		out  *time.Duration
	}{
		{"replan_interval", c.ReplanIntervalRaw, defaultReplanInterval, &c.ReplanInterval},
		{"stable_for", c.StableForRaw, defaultStableFor, &c.StableFor},
		{"track_interval", c.TrackIntervalRaw, defaultTrackInterval, &c.TrackInterval},
		{"min_dwell", c.MinDwellRaw, defaultMinDwell, &c.MinDwell},
		{"next_room_dwell", c.NextRoomDwellRaw, defaultNextRoomDwell, &c.NextRoomDwell},
		{"stall_timeout", c.StallTimeoutRaw, defaultStallTimeout, &c.StallTimeout},
		{"travel_track_interval", c.TravelTrackIntervalRaw, defaultTravelTrack, &c.TravelTrackInterval},
	} {
		if *d.out, err = parseDuration("scheduler."+d.name, d.raw, d.def, false); err != nil {
			return err
		}
	}

	if c.WaterEmptyError == "" {
		c.WaterEmptyError = defaultWaterEmptyError
	}

	if c.NextRoomDwell > c.MinDwell {
		return fmt.Errorf("scheduler.next_room_dwell %s must not be longer than scheduler.min_dwell %s",
			c.NextRoomDwell, c.MinDwell)
	}

	if c.TravelFanSpeed != strings.TrimSpace(c.TravelFanSpeed) {
		return fmt.Errorf("scheduler.travel_fan_speed %q must not have leading or trailing spaces", c.TravelFanSpeed)
	}

	b := &c.Battery
	if b.StartMin == nil {
		v := defaultStartMin
		b.StartMin = &v
	}
	if b.Floor == nil {
		v := defaultFloor
		b.Floor = &v
	}
	if *b.Floor < 0 || *b.Floor >= *b.StartMin || *b.StartMin > 100 {
		return fmt.Errorf("scheduler.battery: need 0 <= floor < start_min <= 100, got floor %g, start_min %g",
			*b.Floor, *b.StartMin)
	}
	if b.LearningRate == 0 {
		b.LearningRate = defaultLearningRate
	}
	if b.LearningRate <= 0 || b.LearningRate > 1 {
		return fmt.Errorf("scheduler.battery.learning_rate %g must be more than 0 and at most 1", b.LearningRate)
	}
	if b.DefaultTripCost == 0 {
		b.DefaultTripCost = defaultTripCost
	}
	if b.DefaultTripCost < 0 {
		return fmt.Errorf("scheduler.battery.default_trip_cost %g must not be negative", b.DefaultTripCost)
	}
	if b.DefaultCostPerM2 == nil {
		b.DefaultCostPerM2 = map[Treatment]float64{}
	}
	for t, def := range map[Treatment]float64{TreatmentFull: defaultCostPerM2Full, TreatmentVacuum: defaultCostPerM2Vac} {
		if b.DefaultCostPerM2[t] == 0 {
			b.DefaultCostPerM2[t] = def
		}
	}
	for t, v := range b.DefaultCostPerM2 {
		if !validTreatment(t) {
			return fmt.Errorf("scheduler.battery.default_cost_per_m2: unknown treatment %q", t)
		}
		if v < 0 {
			return fmt.Errorf("scheduler.battery.default_cost_per_m2.%s %g must not be negative", t, v)
		}
	}

	if c.Treatments == nil {
		c.Treatments = map[Treatment]TreatmentConfig{}
	}
	for t := range c.Treatments {
		if !validTreatment(t) {
			return fmt.Errorf("scheduler.treatments: unknown treatment %q (use full or vacuum)", t)
		}
	}
	if tc := c.Treatments[TreatmentFull]; tc.OperationMode == "" {
		tc.OperationMode = "vacuum_and_mop"
		c.Treatments[TreatmentFull] = tc
	}
	if tc := c.Treatments[TreatmentVacuum]; tc.OperationMode == "" {
		tc.OperationMode = "vacuum"
		c.Treatments[TreatmentVacuum] = tc
	}

	for i, s := range c.Sleepers {
		if s.State == "" {
			return fmt.Errorf("scheduler.sleepers[%d]: state is required", i)
		}
		if len(s.Rooms) == 0 {
			return fmt.Errorf("scheduler.sleepers[%d] (%s): rooms are required", i, s.State)
		}
	}

	for i, h := range c.HoldWhile {
		if strings.TrimSpace(h.Variable) == "" {
			return fmt.Errorf("scheduler.hold_while[%d]: variable is required", i)
		}
	}

	for _, name := range c.RoomNames() {
		r := c.Rooms[name]
		if !roomNamePattern.MatchString(name) {
			return fmt.Errorf("scheduler.rooms: %q must be lower case letters, digits and single underscores", name)
		}
		if name == "trip" {
			return fmt.Errorf("scheduler.rooms: %q is reserved for input_number.vacuum_cost_trip", name)
		}
		if strings.TrimSpace(r.SegmentID) == "" {
			return fmt.Errorf("scheduler.rooms.%s: segment_id is required", name)
		}
		if r.FullRaw == "" {
			return fmt.Errorf("scheduler.rooms.%s: full interval is required", name)
		}
		if r.Full, err = parseDuration("scheduler.rooms."+name+".full", r.FullRaw, 0, false); err != nil {
			return err
		}
		if r.Vacuum, err = parseDuration("scheduler.rooms."+name+".vacuum", r.VacuumRaw, 0, true); err != nil {
			return err
		}
		c.Rooms[name] = r
	}

	if c.StrandedAfter, err = parseDuration("scheduler.stranded_after", c.StrandedAfterRaw, defaultStrandedAfter,
		false); err != nil {
		return err
	}
	c.FenceHelperGrace = defaultFenceHelperGrace
	if c.FenceHelperGraceRaw != "" {
		g, perr := time.ParseDuration(c.FenceHelperGraceRaw)
		if perr != nil {
			return fmt.Errorf("parse scheduler.fence_helper_grace %q: %w", c.FenceHelperGraceRaw, perr)
		}
		if g < 0 {
			return fmt.Errorf("scheduler.fence_helper_grace must not be negative, got %s", g)
		}
		c.FenceHelperGrace = g
	}
	if m := c.FenceMargin(); m < 0 || m > maxFenceMarginM {
		return fmt.Errorf("scheduler.fence_margin_m %g must be at least 0 and at most %g", m, maxFenceMarginM)
	}

	if c.Mode == ModeOff {
		return nil
	}
	if len(c.Rooms) == 0 {
		return fmt.Errorf("scheduler: no rooms")
	}
	u, perr := url.Parse(c.ValetudoURL)
	if c.ValetudoURL == "" || perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("scheduler.valetudo_url %q must be an http or https URL", c.ValetudoURL)
	}
	return nil
}

// parseDuration parses a Go duration. Empty gives def; allowEmpty with def 0 means "not set".
func parseDuration(name, raw string, def time.Duration, allowEmpty bool) (time.Duration, error) {
	if raw == "" {
		if def == 0 && !allowEmpty {
			return 0, fmt.Errorf("%s is required", name)
		}
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", name, d)
	}
	return d, nil
}

func validTreatment(t Treatment) bool {
	return t == TreatmentFull || t == TreatmentVacuum
}
