package state

import (
	"fmt"
	"sync/atomic"
	"time"
)

// EventContext provides correlation metadata for cross-plugin event tracking.
// This allows tracing events as they propagate from state changes through
// plugin handlers and resulting actions.
type EventContext struct {
	// CorrelationID uniquely identifies this event chain
	CorrelationID string `json:"correlationId"`

	// Timestamp when the event was created
	Timestamp time.Time `json:"timestamp"`

	// TriggerKey is the state variable that initiated this event chain
	TriggerKey string `json:"triggerKey"`

	// TriggerOldValue is the previous value of the trigger variable
	TriggerOldValue interface{} `json:"triggerOldValue,omitempty"`

	// TriggerNewValue is the new value of the trigger variable
	TriggerNewValue interface{} `json:"triggerNewValue,omitempty"`
}

// correlationCounter provides a monotonic counter for correlation IDs
var correlationCounter uint64

// NewEventContext creates a new event context for tracking a state change event.
// The correlation ID format is: {timestamp_ms}-{counter} which allows both
// uniqueness and chronological ordering.
func NewEventContext(triggerKey string, oldValue, newValue interface{}) *EventContext {
	now := time.Now()
	counter := atomic.AddUint64(&correlationCounter, 1)

	return &EventContext{
		CorrelationID:   fmt.Sprintf("%d-%d", now.UnixMilli(), counter),
		Timestamp:       now,
		TriggerKey:      triggerKey,
		TriggerOldValue: oldValue,
		TriggerNewValue: newValue,
	}
}

// String returns a human-readable representation of the event context
func (e *EventContext) String() string {
	return fmt.Sprintf("event[%s] trigger=%s at=%s",
		e.CorrelationID, e.TriggerKey, e.Timestamp.Format(time.RFC3339Nano))
}
