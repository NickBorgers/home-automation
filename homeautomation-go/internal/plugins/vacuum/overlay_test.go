package vacuum

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A due taint is turned on once. While Home Assistant does not show it yet, the value is kept in memory and
// not written again on every tick.
func TestSchedulerDueTaintIsWrittenOnce(t *testing.T) {
	e := newSchedEnv(t, ModeLogOnly)
	e.ha.SetServiceError("input_boolean", "turn_on", errors.New("not acknowledged"))
	e.start()
	e.advance(13 * time.Hour)
	entity := "input_boolean.vacuum_dirty_kitchen_vacuum"
	require.Equal(t, "off", e.state(entity), "Home Assistant did not take the write")
	assert.True(t, e.s.boolOverlay[entity], "the written value is remembered")
	e.advance(30 * time.Second)
	e.advance(30 * time.Second)
	assert.True(t, e.s.boolOverlay[entity], "still remembered, so no new write")
	for _, it := range e.sched().Items {
		if it.Room == "kitchen" && it.Treatment == "vacuum" {
			assert.True(t, it.Dirty)
		}
	}
}
