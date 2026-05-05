package tts

import (
	"context"
	"sync"
)

// MockSynthesizer is a Synthesizer for tests. Returns URL (default
// "http://test/audio/mock.mp3") and records every text it was asked to speak.
// Safe for concurrent use.
type MockSynthesizer struct {
	mu       sync.Mutex
	URL      string
	Err      error
	messages []string
}

// SynthesizeAndServe implements Synthesizer.
func (m *MockSynthesizer) SynthesizeAndServe(_ context.Context, text string) (string, error) {
	m.mu.Lock()
	m.messages = append(m.messages, text)
	m.mu.Unlock()
	if m.Err != nil {
		return "", m.Err
	}
	if m.URL != "" {
		return m.URL, nil
	}
	return "http://test/audio/mock.mp3", nil
}

// Messages returns a copy of every text Synthesize was asked to speak.
func (m *MockSynthesizer) Messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.messages))
	copy(out, m.messages)
	return out
}

// Reset clears recorded messages.
func (m *MockSynthesizer) Reset() {
	m.mu.Lock()
	m.messages = nil
	m.mu.Unlock()
}
