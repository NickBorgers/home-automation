package state

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silentlySetHAState changes a mock HA entity without delivering a state-change event.
// This models an event the app missed because the WebSocket was down.
func silentlySetHAState(c *ha.MockClient, entityID, value string) {
	now := time.Now()
	c.SetMockState(entityID, &ha.State{
		EntityID:    entityID,
		State:       value,
		Attributes:  map[string]interface{}{},
		LastChanged: now,
		LastUpdated: now,
	})
}

// TestManager_SyncFromHA_NotifiesSubscribersOfMissedChanges covers the reconnect path.
// While the WebSocket is down, HA can change a value without the app seeing the event.
// The resync must tell subscribers, or plugins like lighting never react to the change.
func TestManager_SyncFromHA_NotifiesSubscribersOfMissedChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		initial    string
		afterGap   string
		wantNotify bool
	}{
		{name: "value changed during gap", initial: "sunset", afterGap: "dusk", wantNotify: true},
		{name: "value unchanged during gap", initial: "sunset", afterGap: "sunset", wantNotify: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockClient := ha.NewMockClient()
			mockClient.SetState("input_text.day_phase", tt.initial, map[string]interface{}{})
			mockClient.Connect()

			manager := NewManager(mockClient, testlogger.New(), false)
			require.NoError(t, manager.SyncFromHA())

			var mu sync.Mutex
			var calls [][2]interface{}
			_, err := manager.Subscribe("dayPhase", func(key string, oldValue, newValue interface{}) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, [2]interface{}{oldValue, newValue})
			})
			require.NoError(t, err)

			// HA changes while the app is disconnected: no event reaches the app.
			silentlySetHAState(mockClient, "input_text.day_phase", tt.afterGap)
			require.NoError(t, manager.SyncFromHA())

			mu.Lock()
			defer mu.Unlock()
			if !tt.wantNotify {
				assert.Empty(t, calls)
				return
			}
			require.Len(t, calls, 1)
			assert.Equal(t, tt.initial, calls[0][0])
			assert.Equal(t, tt.afterGap, calls[0][1])
			value, err := manager.GetString("dayPhase")
			require.NoError(t, err)
			assert.Equal(t, tt.afterGap, value)
		})
	}
}

// TestManager_SyncFromHA_FirstSyncDoesNotNotify checks that the startup sync stays silent.
// Nothing has run yet, so there is no earlier value to compare against.
func TestManager_SyncFromHA_FirstSyncDoesNotNotify(t *testing.T) {
	t.Parallel()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.day_phase", "dusk", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, testlogger.New(), false)

	var notified atomic.Int32
	_, err := manager.Subscribe("dayPhase", func(key string, oldValue, newValue interface{}) {
		notified.Add(1)
	})
	require.NoError(t, err)

	require.NoError(t, manager.SyncFromHA())
	assert.Equal(t, int32(0), notified.Load())
}

// TestManager_SetString_WriteReachesHAButCallFails_ResyncNotifies reproduces the 2026-10-24 dusk incident.
// The write reaches HA, then the connection drops before HA confirms. SetString rolls back
// and returns an error. The reconnect resync must still deliver the new value to subscribers.
func TestManager_SetString_WriteReachesHAButCallFails_ResyncNotifies(t *testing.T) {
	t.Parallel()
	mockClient := ha.NewMockClient()
	mockClient.SetState("input_text.day_phase", "sunset", map[string]interface{}{})
	mockClient.Connect()

	manager := NewManager(mockClient, testlogger.New(), false)
	require.NoError(t, manager.SyncFromHA())

	var mu sync.Mutex
	var seen []string
	_, err := manager.Subscribe("dayPhase", func(key string, oldValue, newValue interface{}) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, newValue.(string))
	})
	require.NoError(t, err)

	// The call fails from the app's view, but HA applied the change.
	mockClient.SetServiceError("input_text", "set_value", fmt.Errorf("client disconnected"))
	require.Error(t, manager.SetString("dayPhase", "dusk"))
	mockClient.SetServiceError("input_text", "set_value", nil)
	silentlySetHAState(mockClient, "input_text.day_phase", "dusk")

	// Reconnect callback runs the resync.
	require.NoError(t, manager.SyncFromHA())

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"dusk"}, seen, "lighting must hear about dusk after the resync")
}
