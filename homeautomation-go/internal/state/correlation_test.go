package state

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEventContext(t *testing.T) {
	t.Parallel()

	t.Run("creates event context with correlation ID", func(t *testing.T) {
		ctx := NewEventContext("isWakeSequenceActive", false, true)

		require.NotNil(t, ctx)
		assert.NotEmpty(t, ctx.CorrelationID)
		assert.Equal(t, "isWakeSequenceActive", ctx.TriggerKey)
		assert.Equal(t, false, ctx.TriggerOldValue)
		assert.Equal(t, true, ctx.TriggerNewValue)
		assert.False(t, ctx.Timestamp.IsZero())
	})

	t.Run("correlation ID has correct format", func(t *testing.T) {
		ctx := NewEventContext("testVar", nil, "value")

		// Format should be: {timestamp_ms}-{counter}
		parts := strings.Split(ctx.CorrelationID, "-")
		assert.Len(t, parts, 2)

		// First part should be a valid timestamp (milliseconds)
		assert.Greater(t, len(parts[0]), 10) // Millisecond timestamps are 13 digits
	})

	t.Run("correlation IDs are unique", func(t *testing.T) {
		ids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			ctx := NewEventContext("testVar", nil, i)
			assert.False(t, ids[ctx.CorrelationID], "Duplicate correlation ID: %s", ctx.CorrelationID)
			ids[ctx.CorrelationID] = true
		}
	})

	t.Run("correlation IDs are monotonically increasing", func(t *testing.T) {
		ctx1 := NewEventContext("testVar", nil, 1)
		ctx2 := NewEventContext("testVar", nil, 2)
		ctx3 := NewEventContext("testVar", nil, 3)

		// IDs should be lexicographically ordered (since timestamp prefix increases)
		assert.True(t, ctx1.CorrelationID < ctx2.CorrelationID || ctx1.CorrelationID == ctx2.CorrelationID)
		assert.True(t, ctx2.CorrelationID < ctx3.CorrelationID || ctx2.CorrelationID == ctx3.CorrelationID)
	})

	t.Run("timestamp is close to now", func(t *testing.T) {
		before := time.Now()
		ctx := NewEventContext("testVar", nil, nil)
		after := time.Now()

		assert.True(t, ctx.Timestamp.After(before) || ctx.Timestamp.Equal(before))
		assert.True(t, ctx.Timestamp.Before(after) || ctx.Timestamp.Equal(after))
	})
}

func TestEventContext_String(t *testing.T) {
	t.Parallel()

	ctx := NewEventContext("dayPhase", "morning", "day")
	str := ctx.String()

	assert.Contains(t, str, "event[")
	assert.Contains(t, str, ctx.CorrelationID)
	assert.Contains(t, str, "dayPhase")
}
