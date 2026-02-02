package clock

import (
	"sync"
	"testing"
	"time"
)

// TestMockClock_ConcurrentResetAndAdvance tests that Reset() and Advance()
// can be called concurrently without deadlocking. This validates the fix for
// the lock ordering violation described in Issue #552.
//
// Before the fix, Reset() acquired timer.mu then clock.mu, while Advance()
// acquired clock.mu then timer.mu, creating a classic deadlock scenario:
//
//	Goroutine A (Reset):   timer.mu ✓ → clock.mu (blocked)
//	Goroutine B (Advance): clock.mu ✓ → timer.mu (blocked)
//
// The fix ensures both methods acquire clock.mu before timer.mu.
func TestMockClock_ConcurrentResetAndAdvance(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Create a timer
	fired := make(chan struct{}, 100)
	timer := clock.AfterFunc(10*time.Second, func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	// Run concurrent Reset and Advance operations
	// If there's a lock ordering bug, this will deadlock
	var wg sync.WaitGroup
	iterations := 100

	// Goroutine 1: Repeatedly call Reset
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			timer.Reset(10 * time.Second)
		}
	}()

	// Goroutine 2: Repeatedly call Advance
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			clock.Advance(1 * time.Second)
		}
	}()

	// Use a timeout to detect deadlock
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected: concurrent Reset and Advance blocked for 5 seconds")
	}
}

// TestMockClock_ResetBasic tests basic Reset functionality
func TestMockClock_ResetBasic(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Create a timer that fires after 10 seconds
	fired := false
	timer := clock.AfterFunc(10*time.Second, func() {
		fired = true
	})

	// Advance 5 seconds - timer should not fire
	clock.Advance(5 * time.Second)
	if fired {
		t.Error("Timer fired too early")
	}

	// Reset timer to 20 seconds from now
	wasActive := timer.Reset(20 * time.Second)
	if !wasActive {
		t.Error("Reset should return true for active timer")
	}

	// Advance 10 more seconds (total 15 from start) - timer should not fire
	// because we reset it to fire 20 seconds from the reset time
	clock.Advance(10 * time.Second)
	if fired {
		t.Error("Timer fired before reset deadline")
	}

	// Advance 10 more seconds - now timer should fire
	clock.Advance(10 * time.Second)
	if !fired {
		t.Error("Timer should have fired after reset deadline")
	}
}

// TestMockClock_ResetStoppedTimer tests that Reset can reactivate a stopped timer
func TestMockClock_ResetStoppedTimer(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Create and stop a timer
	fired := false
	timer := clock.AfterFunc(10*time.Second, func() {
		fired = true
	})
	timer.Stop()

	// Reset should return false for stopped timer and reactivate it
	wasActive := timer.Reset(5 * time.Second)
	if wasActive {
		t.Error("Reset should return false for stopped timer")
	}

	// Advance past the new deadline - timer should fire
	clock.Advance(6 * time.Second)
	if !fired {
		t.Error("Timer should have fired after being reset from stopped state")
	}
}

// TestMockClock_AdvanceFiresTimers tests that Advance correctly fires expired timers
func TestMockClock_AdvanceFiresTimers(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Create multiple timers with different deadlines
	fired := make([]int, 0, 3)
	var mu sync.Mutex

	clock.AfterFunc(5*time.Second, func() {
		mu.Lock()
		fired = append(fired, 1)
		mu.Unlock()
	})
	clock.AfterFunc(10*time.Second, func() {
		mu.Lock()
		fired = append(fired, 2)
		mu.Unlock()
	})
	clock.AfterFunc(15*time.Second, func() {
		mu.Lock()
		fired = append(fired, 3)
		mu.Unlock()
	})

	// Advance 12 seconds - should fire timers 1 and 2
	clock.Advance(12 * time.Second)

	mu.Lock()
	if len(fired) != 2 {
		t.Errorf("Expected 2 timers to fire, got %d", len(fired))
	}
	mu.Unlock()

	// Advance 5 more seconds - should fire timer 3
	clock.Advance(5 * time.Second)

	mu.Lock()
	if len(fired) != 3 {
		t.Errorf("Expected 3 timers to fire, got %d", len(fired))
	}
	mu.Unlock()
}

// TestMockClock_Now tests the Now method
func TestMockClock_Now(t *testing.T) {
	start := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := NewMockClock(start)

	if !clock.Now().Equal(start) {
		t.Errorf("Now() should return start time, got %v", clock.Now())
	}

	clock.Advance(1 * time.Hour)

	expected := start.Add(1 * time.Hour)
	if !clock.Now().Equal(expected) {
		t.Errorf("Now() after advance should return %v, got %v", expected, clock.Now())
	}
}

// TestMockClock_After tests the After method
func TestMockClock_After(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	ch := clock.After(10 * time.Second)

	// Should not receive anything yet
	select {
	case <-ch:
		t.Error("After channel should not have value yet")
	default:
		// Expected
	}

	// Advance time and check
	clock.Advance(10 * time.Second)

	select {
	case received := <-ch:
		expected := time.Date(2025, 1, 1, 0, 0, 10, 0, time.UTC)
		if !received.Equal(expected) {
			t.Errorf("After channel should receive %v, got %v", expected, received)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("After channel should have value after advancing")
	}
}

// TestMockClock_Since tests the Since method
func TestMockClock_Since(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := NewMockClock(start)

	past := start.Add(-1 * time.Hour)
	elapsed := clock.Since(past)

	if elapsed != 1*time.Hour {
		t.Errorf("Since should return 1 hour, got %v", elapsed)
	}

	clock.Advance(30 * time.Minute)
	elapsed = clock.Since(past)

	if elapsed != 90*time.Minute {
		t.Errorf("Since after advance should return 90 minutes, got %v", elapsed)
	}
}

// TestMockClock_Set tests the Set method
func TestMockClock_Set(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := NewMockClock(start)

	// Set to a future time
	future := start.Add(5 * time.Hour)
	clock.Set(future)

	if !clock.Now().Equal(future) {
		t.Errorf("Now() after Set should return %v, got %v", future, clock.Now())
	}

	// Set to a past time (relative to current)
	past := start.Add(1 * time.Hour)
	clock.Set(past)

	if !clock.Now().Equal(past) {
		t.Errorf("Now() after Set to past should return %v, got %v", past, clock.Now())
	}
}

// TestMockClock_Ticker tests the NewTicker functionality
func TestMockClock_Ticker(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	ticker := clock.NewTicker(5 * time.Second)
	ticks := 0

	// Advance and count ticks
	for i := 0; i < 3; i++ {
		clock.Advance(5 * time.Second)
		select {
		case <-ticker.C():
			ticks++
		default:
			// Tick might have been dropped if channel was full
		}
	}

	if ticks < 1 {
		t.Errorf("Expected at least 1 tick, got %d", ticks)
	}

	// Stop ticker
	ticker.Stop()
}

// TestMockTimer_Stop tests the Stop method
func TestMockTimer_Stop(t *testing.T) {
	clock := NewMockClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	fired := false
	timer := clock.AfterFunc(10*time.Second, func() {
		fired = true
	})

	// Stop before firing
	wasActive := timer.Stop()
	if !wasActive {
		t.Error("Stop should return true for active timer")
	}

	// Advance past deadline
	clock.Advance(15 * time.Second)

	if fired {
		t.Error("Stopped timer should not fire")
	}

	// Stop again should return false
	wasActive = timer.Stop()
	if wasActive {
		t.Error("Stop should return false for already stopped timer")
	}
}
