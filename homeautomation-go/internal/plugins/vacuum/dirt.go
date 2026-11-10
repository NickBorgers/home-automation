package vacuum

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"homeautomation/internal/ha"

	"go.uber.org/zap"
)

// Scores for items without a usable ratio. A manual taint beats any interval taint that is not far overdue.
const (
	neverCleanedScore = 5.0
	manualScore       = 10.0
)

// errWriteSkipped means a write was not sent because of read-only mode.
var errWriteSkipped = errors.New("write skipped")

// itemKey is one room and treatment.
type itemKey struct {
	Room      string
	Treatment Treatment
}

func (k itemKey) String() string { return k.Room + "/" + string(k.Treatment) }

func helperSuffix(t Treatment) string {
	if t == TreatmentVacuum {
		return "_vacuum"
	}
	return ""
}

// dirtyEntity is the taint: on means the item needs cleaning. The owner may turn it on by hand.
func dirtyEntity(k itemKey) string {
	return "input_boolean.vacuum_dirty_" + k.Room + helperSuffix(k.Treatment)
}

// cleanedEntity holds the last clean time. It survives restarts; last_changed does not.
func cleanedEntity(k itemKey) string {
	return "input_datetime.vacuum_cleaned_" + k.Room + helperSuffix(k.Treatment)
}

// costEntity holds the learned battery percent to clean the item.
func costEntity(k itemKey) string {
	return "input_number.vacuum_cost_" + k.Room + helperSuffix(k.Treatment)
}

// tripEntity holds the learned battery percent to drive out and back.
const tripEntity = "input_number.vacuum_cost_trip"

// itemKeys lists every configured item, sorted: full for every room, vacuum where it has an interval.
func (c *SchedulerConfig) itemKeys() []itemKey {
	var keys []itemKey
	for _, room := range c.RoomNames() {
		keys = append(keys, itemKey{Room: room, Treatment: TreatmentFull})
		if c.Rooms[room].Vacuum > 0 {
			keys = append(keys, itemKey{Room: room, Treatment: TreatmentVacuum})
		}
	}
	return keys
}

// taint is the evaluated state of one item.
type taint struct {
	Key         itemKey
	Interval    time.Duration
	BoolOn      bool
	LastCleaned time.Time
	Dirty       bool
	Manual      bool
	Score       float64
	Cost        float64
	Missing     []string
}

// evaluateTaint decides whether an item is dirty. It is due once its interval has passed since the last clean,
// or when it was never cleaned. A boolean that is on without the item being due is a manual taint.
func evaluateTaint(now time.Time, interval time.Duration, boolOn bool, lastCleaned time.Time) (dirty, manual bool,
	score float64) {
	due := lastCleaned.IsZero() || now.Sub(lastCleaned) >= interval
	if lastCleaned.IsZero() {
		score = neverCleanedScore
	} else if interval > 0 {
		score = math.Max(0, float64(now.Sub(lastCleaned))/float64(interval))
	}
	dirty = due || boolOn
	manual = boolOn && !due
	if manual {
		score = math.Max(score, manualScore)
	}
	return dirty, manual, score
}

// parseDatetime reads an input_datetime. It prefers the timestamp attribute and falls back to the state text
// in loc. ok is false when the helper holds no usable time; a never-set helper reads as the zero time.
func parseDatetime(st *ha.State, loc *time.Location) (t time.Time, ok bool) {
	if st == nil {
		return time.Time{}, false
	}
	if ts, has := numberAttr(st.Attributes, "timestamp"); has {
		if ts <= 0 {
			return time.Time{}, true
		}
		sec, frac := math.Modf(ts)
		t = time.Unix(int64(sec), int64(frac*1e9))
		if t.Year() < 2000 {
			return time.Time{}, true
		}
		return t, true
	}
	s := strings.TrimSpace(st.State)
	switch s {
	case "", "unknown", "unavailable":
		return time.Time{}, true
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
	if err != nil {
		return time.Time{}, false
	}
	if t.Year() < 2000 {
		return time.Time{}, true
	}
	return t, true
}

func numberAttr(attrs map[string]interface{}, name string) (float64, bool) {
	switch v := attrs[name].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// parseNumber reads an input_number. ok is false when the state is not a number.
func parseNumber(st *ha.State) (float64, bool) {
	if st == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(st.State), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// getState reads an entity. ok is false when Home Assistant does not have it.
func (s *Scheduler) getState(entity string) (*ha.State, bool) {
	st, err := s.haClient.GetState(entity)
	if err != nil || st == nil {
		return nil, false
	}
	return st, true
}

// evaluateTaints reads every item's helpers and turns on the taint of items that became due. Items with a
// missing helper are reported and skipped. The caller holds tickMu.
func (s *Scheduler) evaluateTaints(now time.Time) {
	s.taints = s.taints[:0]
	for _, k := range s.cfg.itemKeys() {
		t := taint{Key: k, Interval: s.cfg.Rooms[k.Room].Interval(k.Treatment)}

		boolSt, okBool := s.getState(dirtyEntity(k))
		dtSt, okDt := s.getState(cleanedEntity(k))
		costSt, okCost := s.getState(costEntity(k))
		for _, m := range []struct {
			ok     bool
			entity string
		}{{okBool, dirtyEntity(k)}, {okDt, cleanedEntity(k)}, {okCost, costEntity(k)}} {
			if !m.ok {
				t.Missing = append(t.Missing, m.entity)
				s.warn("missing:"+m.entity, "missing helper "+m.entity+"; "+k.String()+" is skipped")
			}
		}

		if okBool {
			t.BoolOn = s.effectiveBool(dirtyEntity(k), boolSt.State == "on")
		}
		if okDt {
			cleaned, ok := parseDatetime(dtSt, s.location())
			if !ok {
				s.warn("unreadable:"+cleanedEntity(k), fmt.Sprintf("cannot read %s %q; treated as never cleaned",
					cleanedEntity(k), dtSt.State))
			}
			t.LastCleaned = cleaned
		}
		if over, ok := s.cleanedOverlay[k]; ok && over.After(t.LastCleaned) {
			t.LastCleaned = over
		}
		t.Dirty, t.Manual, t.Score = evaluateTaint(now, t.Interval, t.BoolOn, t.LastCleaned)
		t.Cost = s.itemCost(k, costSt, okCost)

		if len(t.Missing) == 0 && t.Dirty && !t.BoolOn {
			s.logger.Info("Vacuum item is due", zap.String("item", k.String()),
				zap.Duration("interval", t.Interval), zap.Time("last_cleaned", t.LastCleaned))
			s.setBool(dirtyEntity(k), true)
			t.BoolOn = s.effectiveBool(dirtyEntity(k), true)
		}
		s.taints = append(s.taints, t)
	}
}

// effectiveBool is Home Assistant's value unless this process wrote a value Home Assistant does not show yet.
func (s *Scheduler) effectiveBool(entity string, ha bool) bool {
	if over, ok := s.boolOverlay[entity]; ok {
		if over == ha {
			delete(s.boolOverlay, entity)
		}
		return over
	}
	return ha
}

// markCleaned records that the robot cleaned an item now: last clean time first, then the taint off.
func (s *Scheduler) markCleaned(k itemKey, now time.Time) {
	s.setCleaned(k, now)
	entity := dirtyEntity(k)
	st, ok := s.getState(entity)
	if !ok {
		return
	}
	if !s.effectiveBool(entity, st.State == "on") {
		return
	}
	s.expectOff[entity] = true
	if err := s.setBool(entity, false); err != nil {
		delete(s.expectOff, entity)
	}
}

// setCleaned writes an item's last clean time. It is also kept in memory, so read-only mode and slow
// Home Assistant updates still see it.
func (s *Scheduler) setCleaned(k itemKey, at time.Time) {
	if old, ok := s.cleanedOverlay[k]; !ok || at.After(old) {
		s.cleanedOverlay[k] = at
	}
	entity := cleanedEntity(k)
	_ = s.write(fmt.Sprintf("set %s to %s", entity, at.Format(time.RFC3339)), func() error {
		return s.haClient.CallService(s.ctx, "input_datetime", "set_datetime", map[string]interface{}{
			"entity_id": entity,
			"timestamp": float64(at.Unix()),
		})
	})
}

// setBool turns an input_boolean on or off. The value is also kept in memory until Home Assistant reports it
// or the entity changes, so read-only mode, a failed write and a cache that has not caught up all see it.
func (s *Scheduler) setBool(entity string, on bool) error {
	service := "turn_off"
	if on {
		service = "turn_on"
	}
	s.boolOverlay[entity] = on
	return s.write(service+" "+entity, func() error {
		return s.haClient.CallService(s.ctx, "input_boolean", service, map[string]interface{}{"entity_id": entity})
	})
}

// write runs a Home Assistant write unless the app is read-only. It logs failures.
func (s *Scheduler) write(desc string, fn func() error) error {
	if s.readOnly {
		s.logger.Info("READ-ONLY: would write helper", zap.String("write", desc))
		return errWriteSkipped
	}
	if err := fn(); err != nil {
		s.logger.Error("Vacuum helper write failed", zap.String("write", desc), zap.Error(err))
		return err
	}
	return nil
}

// boolEvent is a change of a taint boolean seen from Home Assistant.
type boolEvent struct {
	entity   string
	from, to string
	at       time.Time // when the change arrived
}

// queueBoolEvent records a taint boolean change for the tick goroutine.
func (s *Scheduler) queueBoolEvent(entity string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}
	ev := boolEvent{entity: entity, to: newState.State, at: s.timeProvider.Now()}
	if oldState != nil {
		ev.from = oldState.State
	}
	s.eventsMu.Lock()
	s.events = append(s.events, ev)
	s.eventsMu.Unlock()
	s.poke()
}

// processEvents handles queued taint changes. A boolean the owner turned off counts as cleaned now. The
// caller holds tickMu.
func (s *Scheduler) processEvents() {
	s.eventsMu.Lock()
	events := s.events
	s.events = nil
	s.eventsMu.Unlock()

	for _, ev := range events {
		k, ok := s.dirtyKeys[ev.entity]
		if !ok {
			continue
		}
		delete(s.boolOverlay, ev.entity) // Home Assistant has spoken
		if ev.from != "on" || ev.to != "off" {
			continue
		}
		if s.expectOff[ev.entity] {
			delete(s.expectOff, ev.entity)
			continue
		}
		s.logger.Info("Vacuum taint turned off by hand; treated as cleaned now", zap.String("item", k.String()))
		s.setCleaned(k, ev.at)
	}
}

// warn records a warning for shadow state and logs it once.
func (s *Scheduler) warn(key, msg string) {
	s.warnings[key] = msg
	if !s.warnedOnce[key] {
		s.warnedOnce[key] = true
		s.logger.Warn("Vacuum scheduler: "+msg, zap.String("key", key))
	}
}

func (s *Scheduler) warningList() []string {
	out := make([]string, 0, len(s.warnings))
	for _, k := range sortedKeys(s.warnings) {
		out = append(out, s.warnings[k])
	}
	sort.Strings(out)
	return out
}
