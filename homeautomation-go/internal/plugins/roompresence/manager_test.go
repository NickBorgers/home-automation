package roompresence

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTestManager(t *testing.T, cfg *Config, readOnly bool) (*Manager, *ha.MockClient, *state.Manager) {
	t.Helper()
	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.nick_home", "on", nil)
	mockHA.SetState("input_boolean.kitchen_occupied", "off", nil)
	sm := state.NewManager(mockHA, zap.NewNop(), false)
	require.NoError(t, sm.SyncFromHA())

	m := NewManager(context.Background(), mockHA, sm, cfg, "", zap.NewNop(), readOnly, nil)
	clock := t0
	var mu sync.Mutex
	m.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		clock = clock.Add(100 * time.Millisecond)
		return clock
	}
	return m, mockHA, sm
}

func frigateEvent(t *testing.T, typ, id, cam string, entered []string, current ...string) ha.MQTTMessage {
	t.Helper()
	payload, err := json.Marshal(FrigateEvent{Type: typ, After: FrigateObject{
		ID: id, Camera: cam, Label: "person", EnteredZones: entered, CurrentZones: current,
	}})
	require.NoError(t, err)
	return ha.MQTTMessage{Topic: eventsTopic, Payload: string(payload)}
}

func TestManager_Unconfigured(t *testing.T) {
	m, mockHA, _ := newTestManager(t, nil, false)
	require.NoError(t, m.Start())
	defer m.Stop()

	s := m.GetShadowState()
	assert.False(t, s.Outputs.Configured)
	assert.Contains(t, s.Outputs.Reason, ConfigFileName)
	assert.False(t, m.Occupied("kitchen"))
	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "c", []string{"kitchen"}, "kitchen"))
}

func TestManager_LogOnlyComputesButDoesNotWrite(t *testing.T) {
	cfg := exampleConfig(t)
	m, mockHA, sm := newTestManager(t, cfg, false)
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_kitchen",
		[]string{"kitchen_to_hall"}, "kitchen_to_hall"))
	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_kitchen",
		[]string{"kitchen_to_hall", "kitchen"}, "kitchen"))

	assert.True(t, m.Occupied("kitchen"))
	s := m.GetShadowState()
	assert.True(t, s.Outputs.Configured)
	assert.Equal(t, ModeLogOnly, s.Outputs.Mode)
	assert.True(t, s.Outputs.Outputs["isKitchenOccupied"])

	v, err := sm.GetBool("isKitchenOccupied")
	require.NoError(t, err)
	assert.False(t, v, "log_only must not write outputs")
	assert.Empty(t, mockHA.GetServiceCalls())
}

func TestManager_ActiveWritesChangedOutputs(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.Mode = ModeActive
	m, mockHA, sm := newTestManager(t, cfg, false)

	var mu sync.Mutex
	var entries []string
	m.OnEntry(func(area string) {
		mu.Lock()
		entries = append(entries, area)
		mu.Unlock()
	})
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_kitchen",
		[]string{"kitchen_to_hall", "kitchen"}, "kitchen"))
	v, err := sm.GetBool("isKitchenOccupied")
	require.NoError(t, err)
	assert.True(t, v)

	mu.Lock()
	assert.Contains(t, entries, "kitchen")
	mu.Unlock()

	// The same value is not written twice.
	calls := len(mockHA.GetServiceCalls())
	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_kitchen",
		[]string{"kitchen_to_hall", "kitchen"}, "kitchen"))
	assert.Len(t, mockHA.GetServiceCalls(), calls)
}

func TestManager_ReadOnlyNeverWrites(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.Mode = ModeActive
	m, mockHA, sm := newTestManager(t, cfg, true)
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_kitchen",
		[]string{"kitchen_to_hall", "kitchen"}, "kitchen"))
	assert.True(t, m.Occupied("kitchen"))
	v, _ := sm.GetBool("isKitchenOccupied")
	assert.False(t, v)
}

func TestManager_HeartbeatAndReconnect(t *testing.T) {
	m, mockHA, _ := newTestManager(t, exampleConfig(t), false)
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateMQTT(heartbeatTopic, ha.MQTTMessage{Topic: "frigate/cam_hall/status/detect", Payload: "online"})
	assert.True(t, m.GetShadowState().Outputs.Cameras["cam_hall"].Online)

	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_lounge",
		[]string{"lounge", "lounge_to_study"}, "lounge_to_study"))
	mockHA.SimulateMQTT(eventsTopic, ha.MQTTMessage{Topic: eventsTopic, Resubscribed: true})
	assert.True(t, m.Occupied("study"), "a reconnect ends the open track at its doorway")
	assert.Empty(t, m.GetShadowState().Outputs.ActiveTracks)

	// Garbage is ignored.
	mockHA.SimulateMQTT(eventsTopic, ha.MQTTMessage{Topic: eventsTopic, Payload: "{"})
}

func TestManager_NobodyHomeFromState(t *testing.T) {
	m, mockHA, sm := newTestManager(t, exampleConfig(t), false)
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateMQTT(eventsTopic, frigateEvent(t, "update", "a", "cam_lounge",
		[]string{"lounge_to_study"}, "lounge_to_study"))
	require.True(t, m.Occupied("lounge"))

	require.NoError(t, sm.SetBool("isNickHome", false))
	require.Eventually(t, func() bool {
		v, _ := sm.GetBool("isAnyoneHome")
		return !v
	}, time.Second, 10*time.Millisecond)
	m.updatePeople()

	// Jump the clock past the delay and tick.
	m.now = func() time.Time { return t0.Add(time.Hour) }
	m.apply(func(t *Tracker, now time.Time) { t.Tick(now) })
	assert.False(t, m.Occupied("lounge"))
}

func TestManager_BedSensor(t *testing.T) {
	m, mockHA, _ := newTestManager(t, exampleConfig(t), false)
	mockHA.SetState("binary_sensor.example_bed_presence", "off", nil)
	require.NoError(t, m.Start())
	defer m.Stop()

	mockHA.SimulateStateChange("binary_sensor.example_bed_presence", "on")
	require.Eventually(t, func() bool { return m.Occupied("bedroom") }, time.Second, 10*time.Millisecond)
}

// failingMQTT is a MockClient whose MQTT subscriptions fail, as when Home Assistant has no MQTT integration.
type failingMQTT struct{ *ha.MockClient }

func (failingMQTT) SubscribeMQTT(string, ha.MQTTHandler) (ha.Subscription, error) {
	return nil, errors.New("unknown command")
}

func TestManager_MQTTUnavailableIsUnconfigured(t *testing.T) {
	mockHA := ha.NewMockClient()
	sm := state.NewManager(mockHA, zap.NewNop(), false)
	m := NewManager(context.Background(), failingMQTT{mockHA}, sm, exampleConfig(t), "", zap.NewNop(), false, nil)
	require.NoError(t, m.Start())
	defer m.Stop()

	s := m.GetShadowState()
	assert.False(t, s.Outputs.Configured)
	assert.Contains(t, s.Outputs.Reason, "MQTT")
}

func TestHeartbeatCamera(t *testing.T) {
	cam, ok := heartbeatCamera("frigate/camera_1/status/detect")
	assert.True(t, ok)
	assert.Equal(t, "camera_1", cam)
	_, ok = heartbeatCamera("frigate/camera_1/motion")
	assert.False(t, ok)
}
