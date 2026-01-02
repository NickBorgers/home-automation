package logbuffer

import (
	"testing"
	"time"
)

func TestNewBuffer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		size         int
		wantCapacity int
	}{
		{"default size", 0, DefaultBufferSize},
		{"negative size", -1, DefaultBufferSize},
		{"custom size", 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			b := NewBuffer(tt.size)
			if b.Capacity() != tt.wantCapacity {
				t.Errorf("Capacity() = %d, want %d", b.Capacity(), tt.wantCapacity)
			}
			if b.Count() != 0 {
				t.Errorf("Count() = %d, want 0", b.Count())
			}
		})
	}
}

func TestBuffer_Add(t *testing.T) {
	t.Parallel()
	b := NewBuffer(3)

	// Add first event
	e1 := Event{Timestamp: time.Now(), Level: "info", Message: "first"}
	b.Add(e1)
	if b.Count() != 1 {
		t.Errorf("Count() = %d, want 1", b.Count())
	}

	// Add second event
	e2 := Event{Timestamp: time.Now(), Level: "info", Message: "second"}
	b.Add(e2)
	if b.Count() != 2 {
		t.Errorf("Count() = %d, want 2", b.Count())
	}

	// Add third event
	e3 := Event{Timestamp: time.Now(), Level: "info", Message: "third"}
	b.Add(e3)
	if b.Count() != 3 {
		t.Errorf("Count() = %d, want 3", b.Count())
	}

	// Add fourth event (should trigger overflow)
	e4 := Event{Timestamp: time.Now(), Level: "info", Message: "fourth"}
	b.Add(e4)
	if b.Count() != 3 {
		t.Errorf("Count() = %d, want 3 (overflow)", b.Count())
	}
	if !b.HasOverflowed() {
		t.Error("HasOverflowed() = false, want true")
	}
}

func TestBuffer_GetEvents(t *testing.T) {
	t.Parallel()
	b := NewBuffer(5)

	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Add events with different timestamps
	for i := 0; i < 5; i++ {
		b.Add(Event{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Level:     "info",
			Message:   "event",
			Fields:    map[string]interface{}{"index": i},
		})
	}

	t.Run("all events", func(t *testing.T) {

		events := b.GetEvents(time.Time{}, 0)
		if len(events) != 5 {
			t.Errorf("len(events) = %d, want 5", len(events))
		}
		// Verify chronological order
		for i := 0; i < len(events); i++ {
			if events[i].Fields["index"] != i {
				t.Errorf("events[%d].Fields[\"index\"] = %v, want %d", i, events[i].Fields["index"], i)
			}
		}
	})

	t.Run("events since timestamp", func(t *testing.T) {

		// "since" returns events strictly AFTER the timestamp

		since := baseTime.Add(2 * time.Minute)
		events := b.GetEvents(since, 0)
		if len(events) != 2 {
			t.Errorf("len(events) = %d, want 2", len(events))
		}
		// First event should be at minute 3 (strictly after minute 2)
		if events[0].Fields["index"] != 3 {
			t.Errorf("events[0].Fields[\"index\"] = %v, want 3", events[0].Fields["index"])
		}
	})

	t.Run("limited events", func(t *testing.T) {

		events := b.GetEvents(time.Time{}, 2)
		if len(events) != 2 {
			t.Errorf("len(events) = %d, want 2", len(events))
		}
	})

	t.Run("since and limit combined", func(t *testing.T) {

		// "since" returns events strictly AFTER the timestamp

		since := baseTime.Add(1 * time.Minute)
		events := b.GetEvents(since, 2)
		if len(events) != 2 {
			t.Errorf("len(events) = %d, want 2", len(events))
		}
		// First event should be at minute 2 (strictly after minute 1)
		if events[0].Fields["index"] != 2 {
			t.Errorf("events[0].Fields[\"index\"] = %v, want 2", events[0].Fields["index"])
		}
	})
}

func TestBuffer_GetEvents_WithOverflow(t *testing.T) {
	t.Parallel()
	b := NewBuffer(3)

	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Add 5 events to a buffer of size 3 (first 2 will be overwritten)
	for i := 0; i < 5; i++ {
		b.Add(Event{
			Timestamp: baseTime.Add(time.Duration(i) * time.Minute),
			Level:     "info",
			Message:   "event",
			Fields:    map[string]interface{}{"index": i},
		})
	}

	events := b.GetEvents(time.Time{}, 0)
	if len(events) != 3 {
		t.Errorf("len(events) = %d, want 3", len(events))
	}

	// Should have events 2, 3, 4 (oldest 0, 1 were overwritten)
	expectedIndices := []int{2, 3, 4}
	for i, expected := range expectedIndices {
		if events[i].Fields["index"] != expected {
			t.Errorf("events[%d].Fields[\"index\"] = %v, want %d", i, events[i].Fields["index"], expected)
		}
	}
}

func TestBuffer_Clear(t *testing.T) {
	t.Parallel()
	b := NewBuffer(5)

	// Add some events
	for i := 0; i < 3; i++ {
		b.Add(Event{Timestamp: time.Now(), Level: "info", Message: "event"})
	}

	b.Clear()

	if b.Count() != 0 {
		t.Errorf("Count() = %d, want 0", b.Count())
	}
	if b.HasOverflowed() {
		t.Error("HasOverflowed() = true, want false after clear")
	}

	events := b.GetEvents(time.Time{}, 0)
	if len(events) != 0 {
		t.Errorf("len(events) = %d, want 0", len(events))
	}
}

func TestBuffer_EmptyBuffer(t *testing.T) {
	t.Parallel()
	b := NewBuffer(5)

	events := b.GetEvents(time.Time{}, 0)
	if len(events) != 0 {
		t.Errorf("len(events) = %d, want 0", len(events))
	}
}

func TestBuffer_Concurrent(t *testing.T) {
	t.Parallel()
	b := NewBuffer(100)

	// Test concurrent writes and reads
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 50; i++ {
			b.Add(Event{
				Timestamp: time.Now(),
				Level:     "info",
				Message:   "concurrent write",
			})
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 50; i++ {
			_ = b.GetEvents(time.Time{}, 0)
			_ = b.Count()
			_ = b.HasOverflowed()
		}
		done <- true
	}()

	<-done
	<-done

	// If we get here without a race detector panic, the test passes
}
