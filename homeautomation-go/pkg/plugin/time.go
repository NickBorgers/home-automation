package plugin

import "time"

// TimeProvider is an interface for getting the current time.
// This allows tests to inject a fixed time instead of using time.Now().
// Plugins that need testable time should use ctx.TimeProvider, falling
// back to RealTimeProvider when it's nil.
type TimeProvider interface {
	Now() time.Time
}

// RealTimeProvider returns the actual current time.
type RealTimeProvider struct{}

// Now returns the current time.
func (r RealTimeProvider) Now() time.Time {
	return time.Now()
}

// FixedTimeProvider returns a fixed time (for testing).
type FixedTimeProvider struct {
	FixedTime time.Time
}

// Now returns the fixed time.
func (f FixedTimeProvider) Now() time.Time {
	return f.FixedTime
}
