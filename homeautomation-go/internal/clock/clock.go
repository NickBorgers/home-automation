// Package clock provides a time abstraction for testable time-dependent code.
// Use RealClock for production and MockClock for testing.
package clock

import (
	"sync"
	"time"
)

// Clock is an interface for time operations, allowing time to be mocked in tests.
type Clock interface {
	// Now returns the current time
	Now() time.Time

	// After waits for the duration to elapse and then sends the current time on the returned channel
	After(d time.Duration) <-chan time.Time

	// AfterFunc waits for the duration to elapse and then calls f in its own goroutine.
	// It returns a Timer that can be used to cancel the call using its Stop method.
	AfterFunc(d time.Duration, f func()) Timer

	// Sleep pauses the current goroutine for at least the duration d
	Sleep(d time.Duration)

	// Since returns the time elapsed since t
	Since(t time.Time) time.Duration

	// NewTicker returns a new Ticker that will send the current time on its channel after each tick
	NewTicker(d time.Duration) Ticker
}

// Timer represents a single event that can be stopped
type Timer interface {
	// Stop prevents the Timer from firing. Returns true if the call stops the timer,
	// false if the timer has already expired or been stopped.
	Stop() bool

	// Reset changes the timer to expire after duration d.
	// Returns true if the timer had been active, false if the timer had expired or been stopped.
	Reset(d time.Duration) bool
}

// Ticker represents a repeating event
type Ticker interface {
	// C returns the channel on which ticks are delivered
	C() <-chan time.Time

	// Stop turns off the ticker. After Stop, no more ticks will be sent.
	Stop()
}

// RealClock implements Clock using the standard time package
type RealClock struct{}

// realTimer wraps time.Timer to implement our Timer interface
type realTimer struct {
	timer *time.Timer
}

// realTicker wraps time.Ticker to implement our Ticker interface
type realTicker struct {
	ticker *time.Ticker
}

// NewRealClock creates a new RealClock instance
func NewRealClock() *RealClock {
	return &RealClock{}
}

// Now returns the current time
func (c *RealClock) Now() time.Time {
	return time.Now()
}

// After waits for the duration to elapse and then sends the current time
func (c *RealClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// AfterFunc waits for the duration to elapse and then calls f
func (c *RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{timer: time.AfterFunc(d, f)}
}

// Sleep pauses the current goroutine for at least the duration d
func (c *RealClock) Sleep(d time.Duration) {
	time.Sleep(d)
}

// Since returns the time elapsed since t
func (c *RealClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

// NewTicker returns a new Ticker that will send the current time on its channel after each tick
func (c *RealClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{ticker: time.NewTicker(d)}
}

// Stop prevents the Timer from firing
func (t *realTimer) Stop() bool {
	return t.timer.Stop()
}

// Reset changes the timer to expire after duration d
func (t *realTimer) Reset(d time.Duration) bool {
	return t.timer.Reset(d)
}

// C returns the channel on which ticks are delivered
func (t *realTicker) C() <-chan time.Time {
	return t.ticker.C
}

// Stop turns off the ticker
func (t *realTicker) Stop() {
	t.ticker.Stop()
}

// MockClock is a Clock implementation for testing that allows manual time control
type MockClock struct {
	mu      sync.Mutex
	current time.Time
	timers  []*mockTimer
}

type mockTimer struct {
	clock    *MockClock
	deadline time.Time
	f        func()
	stopped  bool
	mu       sync.Mutex
}

type mockTicker struct {
	clock    *MockClock
	interval time.Duration
	ch       chan time.Time
	stopped  bool
	mu       sync.Mutex
}

// NewMockClock creates a new MockClock starting at the given time
func NewMockClock(start time.Time) *MockClock {
	return &MockClock{
		current: start,
		timers:  make([]*mockTimer, 0),
	}
}

// Now returns the mock current time
func (c *MockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// After returns a channel that will receive the time after duration d
func (c *MockClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.AfterFunc(d, func() {
		c.mu.Lock()
		t := c.current
		c.mu.Unlock()
		ch <- t
	})
	return ch
}

// AfterFunc schedules f to be called after duration d
func (c *MockClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()

	timer := &mockTimer{
		clock:    c,
		deadline: c.current.Add(d),
		f:        f,
		stopped:  false,
	}
	c.timers = append(c.timers, timer)
	return timer
}

// Sleep does nothing immediately in MockClock - time only advances via Advance()
func (c *MockClock) Sleep(d time.Duration) {
	// In mock mode, Sleep is a no-op. Use Advance() to move time forward.
	// This allows tests to control exactly when time passes.
}

// Since returns the time elapsed since t using the mock current time
func (c *MockClock) Since(t time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current.Sub(t)
}

// NewTicker returns a new mock Ticker. The ticker will receive ticks when Advance is called.
// Note: MockClock ticker ticks are delivered synchronously during Advance() calls, which
// is typically what you want in tests. The channel has a buffer of 1 to prevent blocking.
func (c *MockClock) NewTicker(d time.Duration) Ticker {
	ticker := &mockTicker{
		clock:    c,
		interval: d,
		ch:       make(chan time.Time, 1),
		stopped:  false,
	}

	// Schedule the first tick
	c.scheduleNextTick(ticker)

	return ticker
}

// scheduleNextTick schedules the next tick for a mock ticker
func (c *MockClock) scheduleNextTick(ticker *mockTicker) {
	c.AfterFunc(ticker.interval, func() {
		ticker.mu.Lock()
		if ticker.stopped {
			ticker.mu.Unlock()
			return
		}
		ticker.mu.Unlock()

		// Non-blocking send to the tick channel
		c.mu.Lock()
		t := c.current
		c.mu.Unlock()

		select {
		case ticker.ch <- t:
		default:
			// Drop tick if channel is full (mimics real ticker behavior)
		}

		// Schedule the next tick
		ticker.mu.Lock()
		if !ticker.stopped {
			ticker.mu.Unlock()
			c.scheduleNextTick(ticker)
		} else {
			ticker.mu.Unlock()
		}
	})
}

// C returns the channel on which ticks are delivered
func (t *mockTicker) C() <-chan time.Time {
	return t.ch
}

// Stop turns off the ticker
func (t *mockTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

// Advance moves the mock clock forward by duration d and fires any timers that have expired
func (c *MockClock) Advance(d time.Duration) {
	c.mu.Lock()
	newTime := c.current.Add(d)
	c.current = newTime

	// Find all timers that should fire
	var toFire []*mockTimer
	var remaining []*mockTimer

	for _, timer := range c.timers {
		timer.mu.Lock()
		if !timer.stopped && !timer.deadline.After(newTime) {
			toFire = append(toFire, timer)
		} else if !timer.stopped {
			remaining = append(remaining, timer)
		}
		timer.mu.Unlock()
	}

	c.timers = remaining
	c.mu.Unlock()

	// Fire timers outside the lock to prevent deadlocks
	for _, timer := range toFire {
		timer.mu.Lock()
		if !timer.stopped {
			timer.stopped = true
			f := timer.f
			timer.mu.Unlock()
			f()
		} else {
			timer.mu.Unlock()
		}
	}
}

// Set sets the mock clock to a specific time and fires any expired timers
func (c *MockClock) Set(t time.Time) {
	c.mu.Lock()
	oldTime := c.current
	c.mu.Unlock()

	if t.After(oldTime) {
		c.Advance(t.Sub(oldTime))
	} else {
		c.mu.Lock()
		c.current = t
		c.mu.Unlock()
	}
}

// Stop prevents the timer from firing
func (t *mockTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

// Reset changes the timer to expire after duration d from now.
// Lock ordering: clock.mu must be acquired before timer.mu to prevent deadlocks
// with Advance(), which iterates over timers while holding clock.mu.
// See CONCURRENCY_LESSONS.md Lesson 10 for details.
func (t *mockTimer) Reset(d time.Duration) bool {
	// Acquire clock.mu first (consistent with Advance lock ordering)
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()

	t.mu.Lock()
	defer t.mu.Unlock()

	wasActive := !t.stopped
	t.stopped = false
	t.deadline = t.clock.current.Add(d)

	// Re-add to timers list if it was stopped
	if !wasActive {
		t.clock.timers = append(t.clock.timers, t)
	}

	return wasActive
}
