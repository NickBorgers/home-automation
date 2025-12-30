// Package logbuffer provides an in-memory ring buffer for storing recent log events.
// This enables the timeline visualization to access recent logs directly without
// requiring copy/paste or file upload.
package logbuffer

import (
	"sync"
	"time"
)

// DefaultBufferSize is the default number of events to store in the ring buffer.
const DefaultBufferSize = 10000

// Event represents a single log event that can be displayed on the timeline.
type Event struct {
	Timestamp time.Time              `json:"ts"`
	Level     string                 `json:"level"`
	Message   string                 `json:"msg"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// Buffer is a thread-safe ring buffer for storing log events.
type Buffer struct {
	mu       sync.RWMutex
	events   []Event
	size     int
	head     int // next write position
	count    int // number of events currently stored
	overflow bool
}

// NewBuffer creates a new ring buffer with the specified capacity.
func NewBuffer(size int) *Buffer {
	if size <= 0 {
		size = DefaultBufferSize
	}
	return &Buffer{
		events: make([]Event, size),
		size:   size,
	}
}

// Add adds an event to the buffer. If the buffer is full, the oldest event is overwritten.
func (b *Buffer) Add(event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.events[b.head] = event
	b.head = (b.head + 1) % b.size
	if b.count < b.size {
		b.count++
	} else {
		b.overflow = true
	}
}

// GetEvents returns all events since the specified timestamp, up to the limit.
// Events are returned in chronological order (oldest first).
// If since is zero, all events are considered.
// If limit is 0, all matching events are returned.
func (b *Buffer) GetEvents(since time.Time, limit int) []Event {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return []Event{}
	}

	// Calculate the start position (oldest event)
	start := 0
	if b.count == b.size {
		start = b.head // oldest event is at head when buffer is full
	}

	// Collect events in chronological order
	result := make([]Event, 0, b.count)
	for i := 0; i < b.count; i++ {
		idx := (start + i) % b.size
		event := b.events[idx]

		// Filter by timestamp
		if !since.IsZero() && event.Timestamp.Before(since) {
			continue
		}

		result = append(result, event)

		// Check limit
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result
}

// Count returns the number of events currently in the buffer.
func (b *Buffer) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// Capacity returns the maximum capacity of the buffer.
func (b *Buffer) Capacity() int {
	return b.size
}

// HasOverflowed returns true if any events have been dropped due to buffer overflow.
func (b *Buffer) HasOverflowed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.overflow
}

// Clear removes all events from the buffer.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
	b.overflow = false
}
