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
// If logFilePath is set, it can also read historical events from a log file.
type Buffer struct {
	mu          sync.RWMutex
	events      []Event
	size        int
	head        int // next write position
	count       int // number of events currently stored
	overflow    bool
	logFilePath string // optional path to JSON log file for reading historical events
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

// NewBufferWithFile creates a ring buffer that also reads from a log file.
// The log file is read when GetEvents is called to retrieve historical events.
// The in-memory buffer stores events since application start for quick access.
func NewBufferWithFile(size int, logFilePath string) *Buffer {
	if size <= 0 {
		size = DefaultBufferSize
	}
	return &Buffer{
		events:      make([]Event, size),
		size:        size,
		logFilePath: logFilePath,
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
// If a log file is configured, events are read from the file for persistence across restarts.
func (b *Buffer) GetEvents(since time.Time, limit int) []Event {
	// If we have a log file configured, read from it
	if b.logFilePath != "" {
		events, err := ReadEventsFromFile(b.logFilePath, since, limit)
		if err == nil && len(events) > 0 {
			return events
		}
		// Fall through to in-memory buffer if file read fails
	}

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

// Count returns the number of events currently in the buffer (or file if configured).
func (b *Buffer) Count() int {
	// If file-backed, count events from file
	if b.logFilePath != "" {
		events, err := ReadEventsFromFile(b.logFilePath, time.Time{}, 0)
		if err == nil {
			return len(events)
		}
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// Capacity returns the maximum capacity of the buffer.
func (b *Buffer) Capacity() int {
	return b.size
}

// HasOverflowed returns true if any events have been dropped due to buffer overflow.
// For file-backed buffers, this always returns false since the file contains all events.
func (b *Buffer) HasOverflowed() bool {
	if b.logFilePath != "" {
		return false // File-backed buffer keeps all events
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.overflow
}

// IsFileBacked returns true if this buffer reads from a log file.
func (b *Buffer) IsFileBacked() bool {
	return b.logFilePath != ""
}

// LogFilePath returns the path to the log file, or empty string if not file-backed.
func (b *Buffer) LogFilePath() string {
	return b.logFilePath
}

// Clear removes all events from the buffer.
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
	b.overflow = false
}
