package ntfy

import "sync"

// MockClient is a mock implementation of Notifier for testing
type MockClient struct {
	mu    sync.Mutex
	Calls []Message
	Error error // Set this to make Send return an error
}

// NewMockClient creates a new mock ntfy client
func NewMockClient() *MockClient {
	return &MockClient{
		Calls: make([]Message, 0),
	}
}

// Send records the message and returns the configured error (if any)
func (m *MockClient) Send(msg *Message) error {
	if m.Error != nil {
		return m.Error
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if msg != nil {
		m.Calls = append(m.Calls, *msg)
	}
	return nil
}

// GetCalls returns a copy of all recorded calls
func (m *MockClient) GetCalls() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]Message, len(m.Calls))
	copy(result, m.Calls)
	return result
}

// Reset clears all recorded calls and errors
func (m *MockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Calls = make([]Message, 0)
	m.Error = nil
}

// SetError configures the mock to return an error on Send
func (m *MockClient) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Error = err
}
