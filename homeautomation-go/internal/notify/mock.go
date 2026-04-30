package notify

import (
	"context"
	"sync"
)

// MockNotifier records Announce calls for tests. Safe for concurrent use.
type MockNotifier struct {
	mu    sync.Mutex
	calls []MockCall

	// Err, when non-nil, is returned from Announce.
	Err error
}

// MockCall captures a single Announce invocation.
type MockCall struct {
	Message  string
	Speakers []string
	Urgency  UrgencyLevel
}

// Announce implements Notifier.
func (m *MockNotifier) Announce(_ context.Context, message string, opts ...AnnounceOption) error {
	o := announceOpts{urgency: UrgencyRoutine}
	for _, opt := range opts {
		opt(&o)
	}
	m.mu.Lock()
	m.calls = append(m.calls, MockCall{
		Message:  message,
		Speakers: append([]string(nil), o.speakers...),
		Urgency:  o.urgency,
	})
	m.mu.Unlock()
	return m.Err
}

// Calls returns a copy of the recorded calls.
func (m *MockNotifier) Calls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]MockCall, len(m.calls))
	copy(out, m.calls)
	return out
}

// Reset clears recorded calls.
func (m *MockNotifier) Reset() {
	m.mu.Lock()
	m.calls = nil
	m.mu.Unlock()
}
