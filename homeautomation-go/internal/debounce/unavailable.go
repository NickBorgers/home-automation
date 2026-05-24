// Package debounce provides small reusable debouncers for noisy state changes.
package debounce

import (
	"strings"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
)

// UnavailableDebouncer holds transient Home Assistant unavailable/unknown
// entity states before forwarding them to the wrapped handler.
type UnavailableDebouncer struct {
	delay   time.Duration
	clock   clock.Clock
	handler ha.StateChangeHandler

	mu      sync.Mutex
	timer   clock.Timer
	pending pendingUnavailable
	stopped bool
}

type pendingUnavailable struct {
	entityID string
	oldState *ha.State
	newState *ha.State
}

// NewUnavailableDebouncer creates a debouncer around a Home Assistant state
// change handler.
func NewUnavailableDebouncer(
	delay time.Duration,
	clk clock.Clock,
	handler ha.StateChangeHandler,
) *UnavailableDebouncer {
	if clk == nil {
		clk = clock.NewRealClock()
	}
	return &UnavailableDebouncer{
		delay:   delay,
		clock:   clk,
		handler: handler,
	}
}

// HandleStateChange forwards normal state changes immediately and debounces
// unavailable/unknown states.
func (d *UnavailableDebouncer) HandleStateChange(entityID string, oldState, newState *ha.State) {
	if d == nil || d.handler == nil {
		return
	}
	if !isUnavailableState(newState) || d.delay <= 0 {
		if !d.cancelPending() {
			return
		}
		d.handler(entityID, oldState, newState)
		return
	}

	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.pending = pendingUnavailable{
		entityID: entityID,
		oldState: cloneState(oldState),
		newState: cloneState(newState),
	}
	d.timer = d.clock.AfterFunc(d.delay, d.forwardPending)
	d.mu.Unlock()
}

// Stop cancels any pending unavailable state.
func (d *UnavailableDebouncer) Stop() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = nil
	d.pending = pendingUnavailable{}
	d.mu.Unlock()
}

// cancelPending cancels any in-flight debounce timer and clears pending state.
// It returns false when the debouncer is stopped, which signals the caller
// (HandleStateChange) to drop even legitimate non-unavailable events during shutdown.
func (d *UnavailableDebouncer) cancelPending() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return false
	}
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = nil
	d.pending = pendingUnavailable{}
	return true
}

// HasPending reports whether an unavailable state is currently being debounced.
// Test-only: lets integration tests synchronize on the debouncer state instead
// of waiting on real-time HA WebSocket dispatch.
func (d *UnavailableDebouncer) HasPending() bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending.newState != nil
}

func (d *UnavailableDebouncer) forwardPending() {
	d.mu.Lock()
	if d.stopped || d.pending.newState == nil {
		d.mu.Unlock()
		return
	}
	pending := d.pending
	d.timer = nil
	d.pending = pendingUnavailable{}
	d.mu.Unlock()

	d.handler(pending.entityID, pending.oldState, pending.newState)
}

func isUnavailableState(state *ha.State) bool {
	if state == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(state.State)) {
	case "unavailable", "unknown":
		return true
	default:
		return false
	}
}

func cloneState(state *ha.State) *ha.State {
	if state == nil {
		return nil
	}
	copied := *state
	if state.Attributes != nil {
		copied.Attributes = make(map[string]interface{}, len(state.Attributes))
		for k, v := range state.Attributes {
			copied.Attributes[k] = v
		}
	}
	return &copied
}
