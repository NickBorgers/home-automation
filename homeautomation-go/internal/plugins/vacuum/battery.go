package vacuum

import (
	"fmt"
	"math"

	"homeautomation/internal/ha"

	"go.uber.org/zap"
)

// learnCost moves a cost toward a new sample by an exponential moving average.
func learnCost(old, sample, rate float64) float64 {
	return old + rate*(sample-old)
}

// firstGuessCost is the cost of a room before any sample: floor area times the per-square-metre default.
func firstGuessCost(areaM2, perM2 float64) float64 {
	return areaM2 * perM2
}

// number reads a learned cost. A value this process learned wins until Home Assistant shows it, so read-only
// mode, a failed write and a cache that has not caught up all use it. has is false when there is no
// positive value.
func (s *Scheduler) number(entity string, st *ha.State, ok bool) (float64, bool) {
	v, valid := 0.0, false
	if ok {
		v, valid = parseNumber(st)
	}
	if over, has := s.numOverlay[entity]; has {
		if valid && math.Abs(v-over) < 0.05 {
			delete(s.numOverlay, entity)
		}
		return over, over > 0
	}
	return v, valid && v > 0
}

// itemCost is the battery percent to clean an item: the learned helper value, or the first guess while the
// helper holds nothing useful.
func (s *Scheduler) itemCost(k itemKey, st *ha.State, ok bool) float64 {
	if v, has := s.number(costEntity(k), st, ok); has {
		return v
	}
	return firstGuessCost(s.graph.FloorArea(k.Room), s.cfg.Battery.DefaultCostPerM2[k.Treatment])
}

// tripCost is the battery percent to drive out and back. A missing helper falls back to the default, with a
// warning; the trip is not a room, so nothing is skipped.
func (s *Scheduler) tripCost() float64 {
	st, ok := s.getState(tripEntity)
	if !ok {
		s.warn("missing:"+tripEntity, "missing helper "+tripEntity+"; using default trip cost")
	}
	if v, has := s.number(tripEntity, st, ok); has {
		return v
	}
	return s.cfg.Battery.DefaultTripCost
}

// learn folds a battery sample into a cost helper and writes it, rounded to 0.1 %.
func (s *Scheduler) learn(entity string, current, sample float64) {
	if sample < 0 || math.IsNaN(sample) {
		return
	}
	next := math.Round(learnCost(current, sample, s.cfg.Battery.LearningRate)*10) / 10
	s.logger.Info("Vacuum battery cost sample",
		zap.String("helper", entity), zap.Float64("sample", sample),
		zap.Float64("old", current), zap.Float64("new", next))
	s.numOverlay[entity] = next
	_ = s.write(fmt.Sprintf("set %s to %.1f", entity, next), func() error {
		return s.haClient.CallService(s.ctx, "input_number", "set_value", map[string]interface{}{
			"entity_id": entity,
			"value":     next,
		})
	})
}

// learnItem folds a sample into an item's cost.
func (s *Scheduler) learnItem(k itemKey, sample float64) {
	st, ok := s.getState(costEntity(k))
	if !ok {
		return // missing helper: warned elsewhere, nothing to learn into
	}
	s.learn(costEntity(k), s.itemCost(k, st, ok), sample)
}

// learnTrip folds a sample into the trip cost.
func (s *Scheduler) learnTrip(sample float64) {
	if _, ok := s.getState(tripEntity); !ok {
		return
	}
	s.learn(tripEntity, s.tripCost(), sample)
}
