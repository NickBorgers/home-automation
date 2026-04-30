package notify

import (
	"context"
	"sync"
)

// Call records a single invocation of MockNotifier.Speak.
type Call struct {
	Message  string
	Urgency  Urgency
	Speakers []string
}

// MockNotifier is a test double for Notifier.
type MockNotifier struct {
	mu    sync.Mutex
	calls []Call
	err   error
}

// NewMockNotifier creates a MockNotifier that records calls and returns nil errors.
func NewMockNotifier() *MockNotifier {
	return &MockNotifier{}
}

// Speak records the call and returns the configured error (if any).
func (m *MockNotifier) Speak(_ context.Context, message string, urgency Urgency, speakers []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.calls = append(m.calls, Call{
		Message:  message,
		Urgency:  urgency,
		Speakers: speakers,
	})
	return nil
}

// GetCalls returns a copy of all recorded calls.
func (m *MockNotifier) GetCalls() []Call {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Call, len(m.calls))
	copy(result, m.calls)
	return result
}

// CallCount returns the number of recorded Speak calls.
func (m *MockNotifier) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// SetError makes subsequent Speak calls return err instead of recording the call.
func (m *MockNotifier) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Reset clears all recorded calls and any configured error.
func (m *MockNotifier) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
	m.err = nil
}
