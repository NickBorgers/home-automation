package alert

import (
	"context"
	"sync"
)

// MockAlerter records Send calls for tests. Safe for concurrent use.
type MockAlerter struct {
	mu    sync.Mutex
	calls []Alert
	// Err, when non-nil, is returned from Send.
	Err error
}

// Send implements Alerter. Always records the call; returns Err if set.
func (m *MockAlerter) Send(_ context.Context, a Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, a)
	return m.Err
}

// Calls returns a copy of the recorded alerts.
func (m *MockAlerter) Calls() []Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Alert, len(m.calls))
	copy(out, m.calls)
	return out
}

// Reset clears recorded calls.
func (m *MockAlerter) Reset() {
	m.mu.Lock()
	m.calls = nil
	m.mu.Unlock()
}
