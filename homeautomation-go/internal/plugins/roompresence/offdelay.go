package roompresence

import "time"

// offDelay holds each output true until the computed value has stayed false for output_off_delay. True
// passes at once and cancels a pending off. With a delay of 0 it passes every value unchanged.
//
// It takes the time on every call and has no timer of its own. The manager runs it after every tracker
// operation, and the tick loop is one of those, so a pending off ends on the first tick at or after its
// deadline: at most tickInterval late. It is not safe for concurrent use.
type offDelay struct {
	delay time.Duration
	value map[string]bool      // last value returned, by output
	until map[string]time.Time // deadline of each pending off, by output
}

func newOffDelay(delay time.Duration) *offDelay {
	return &offDelay{delay: delay, value: make(map[string]bool), until: make(map[string]time.Time)}
}

// hold starts a pending off for an output, as if it had just turned false after being true. The manager
// uses it at startup for an output that Home Assistant already shows true.
func (d *offDelay) hold(now time.Time, key string) {
	if d.delay <= 0 {
		return
	}
	d.value[key] = true
	d.until[key] = now.Add(d.delay)
}

// apply returns the outputs to report, given the values the tracker computed now.
func (d *offDelay) apply(now time.Time, computed map[string]bool) map[string]bool {
	out := make(map[string]bool, len(computed))
	for key, v := range computed {
		if v || d.delay <= 0 || !d.value[key] {
			delete(d.until, key)
		} else {
			deadline, pending := d.until[key]
			if !pending {
				deadline = now.Add(d.delay)
				d.until[key] = deadline
			}
			if now.Before(deadline) {
				v = true
			} else {
				delete(d.until, key)
			}
		}
		d.value[key] = v
		out[key] = v
	}
	return out
}

// pending returns the deadline of each pending off, or nil when none is pending.
func (d *offDelay) pending() map[string]time.Time {
	if len(d.until) == 0 {
		return nil
	}
	out := make(map[string]time.Time, len(d.until))
	for k, t := range d.until {
		out[k] = t
	}
	return out
}
