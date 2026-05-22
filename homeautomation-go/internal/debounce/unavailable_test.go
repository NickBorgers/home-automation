package debounce

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type debouncedCall struct {
	entityID string
	oldState *ha.State
	newState *ha.State
}

func TestUnavailableDebouncer_ForwardsNormalStateImmediately(t *testing.T) {
	t.Parallel()
	clk := clock.NewMockClock(time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC))
	var calls []debouncedCall
	debouncer := NewUnavailableDebouncer(time.Minute, clk, func(entityID string, oldState, newState *ha.State) {
		calls = append(calls, debouncedCall{entityID: entityID, oldState: oldState, newState: newState})
	})

	debouncer.HandleStateChange("sensor.robot", nil, state("sensor.robot", "No error"))

	require.Len(t, calls, 1)
	assert.Equal(t, "sensor.robot", calls[0].entityID)
	assert.Equal(t, "No error", calls[0].newState.State)
}

func TestUnavailableDebouncer_DiscardsUnavailableOnRecovery(t *testing.T) {
	t.Parallel()
	clk := clock.NewMockClock(time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC))
	var calls []debouncedCall
	debouncer := NewUnavailableDebouncer(time.Minute, clk, func(entityID string, oldState, newState *ha.State) {
		calls = append(calls, debouncedCall{entityID: entityID, oldState: oldState, newState: newState})
	})

	debouncer.HandleStateChange("sensor.robot", state("sensor.robot", "No error"), state("sensor.robot", "unavailable"))
	clk.Advance(30 * time.Second)
	debouncer.HandleStateChange("sensor.robot", state("sensor.robot", "unavailable"), state("sensor.robot", "No error"))
	clk.Advance(time.Minute)

	require.Len(t, calls, 1)
	assert.Equal(t, "No error", calls[0].newState.State)
}

func TestUnavailableDebouncer_ForwardsUnavailableAfterDelay(t *testing.T) {
	t.Parallel()
	clk := clock.NewMockClock(time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC))
	var calls []debouncedCall
	debouncer := NewUnavailableDebouncer(time.Minute, clk, func(entityID string, oldState, newState *ha.State) {
		calls = append(calls, debouncedCall{entityID: entityID, oldState: oldState, newState: newState})
	})

	debouncer.HandleStateChange("sensor.robot", state("sensor.robot", "No error"), state("sensor.robot", "unknown"))
	clk.Advance(59 * time.Second)
	assert.Empty(t, calls)

	clk.Advance(time.Second)

	require.Len(t, calls, 1)
	assert.Equal(t, "sensor.robot", calls[0].entityID)
	assert.Equal(t, "No error", calls[0].oldState.State)
	assert.Equal(t, "unknown", calls[0].newState.State)
}

func TestUnavailableDebouncer_StopCancelsPending(t *testing.T) {
	t.Parallel()
	clk := clock.NewMockClock(time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC))
	var calls []debouncedCall
	debouncer := NewUnavailableDebouncer(time.Minute, clk, func(entityID string, oldState, newState *ha.State) {
		calls = append(calls, debouncedCall{entityID: entityID, oldState: oldState, newState: newState})
	})

	debouncer.HandleStateChange("sensor.robot", nil, state("sensor.robot", "unavailable"))
	debouncer.Stop()
	clk.Advance(time.Minute)

	assert.Empty(t, calls)
}

func state(entityID, value string) *ha.State {
	return &ha.State{
		EntityID:   entityID,
		State:      value,
		Attributes: map[string]interface{}{"friendly_name": "Robot"},
	}
}
