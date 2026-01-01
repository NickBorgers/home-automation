package shadowstate

import (
	"testing"

	"homeautomation/internal/ha"

	"go.uber.org/zap"
)

// mockShadowTracker implements ShadowInputUpdater for testing
type mockShadowTracker struct {
	inputs      map[string]interface{}
	updateCount int
}

func newMockShadowTracker() *mockShadowTracker {
	return &mockShadowTracker{
		inputs: make(map[string]interface{}),
	}
}

func (m *mockShadowTracker) UpdateCurrentInputs(inputs map[string]interface{}) {
	m.inputs = inputs
	m.updateCount++
}

// mockHAClient implements ha.HAClient for testing
type mockHAClient struct {
	states      map[string]*ha.State
	subscribers map[string][]ha.StateChangeHandler
}

func newMockHAClient() *mockHAClient {
	return &mockHAClient{
		states:      make(map[string]*ha.State),
		subscribers: make(map[string][]ha.StateChangeHandler),
	}
}

func (m *mockHAClient) Connect() error                     { return nil }
func (m *mockHAClient) Disconnect() error                  { return nil }
func (m *mockHAClient) IsConnected() bool                  { return true }
func (m *mockHAClient) IsHealthy() bool                    { return true }
func (m *mockHAClient) GetAllStates() ([]*ha.State, error) { return nil, nil }
func (m *mockHAClient) CallService(domain, service string, data map[string]interface{}) error {
	return nil
}
func (m *mockHAClient) CallServiceWithTarget(domain, service string, target *ha.ServiceTarget, data map[string]interface{}) error {
	return nil
}
func (m *mockHAClient) SetInputBoolean(name string, value bool) error   { return nil }
func (m *mockHAClient) SetInputNumber(name string, value float64) error { return nil }
func (m *mockHAClient) SetInputText(name string, value string) error    { return nil }
func (m *mockHAClient) SendNotification(deviceName string, notification *ha.Notification) error {
	return nil
}
func (m *mockHAClient) SendNotificationToMultiple(deviceNames []string, notification *ha.Notification) error {
	return nil
}
func (m *mockHAClient) ClearNotification(deviceName, tag string) error { return nil }

func (m *mockHAClient) GetState(entityID string) (*ha.State, error) {
	if s, ok := m.states[entityID]; ok {
		return s, nil
	}
	return &ha.State{EntityID: entityID, State: "unknown"}, nil
}

func (m *mockHAClient) SubscribeStateChanges(entityID string, handler ha.StateChangeHandler) (ha.Subscription, error) {
	m.subscribers[entityID] = append(m.subscribers[entityID], handler)
	return &mockSubscription{entityID: entityID, client: m}, nil
}

// simulateStateChange simulates a state change for testing
func (m *mockHAClient) simulateStateChange(entityID string, oldState, newState *ha.State) {
	for _, handler := range m.subscribers[entityID] {
		handler(entityID, oldState, newState)
	}
}

type mockSubscription struct {
	entityID string
	client   *mockHAClient
}

func (s *mockSubscription) Unsubscribe() error {
	s.client.subscribers[s.entityID] = nil
	return nil
}

func TestSubscriptionHelper_SubscribeToSensor(t *testing.T) {
	logger := zap.NewNop()
	haClient := newMockHAClient()
	registry := NewSubscriptionRegistry()
	tracker := newMockShadowTracker()

	helper := NewSubscriptionHelper(haClient, nil, registry, tracker, "test", logger)

	// Track if handler was called
	handlerCalled := false
	var receivedValue float64

	err := helper.SubscribeToSensor("sensor.test", func(value float64) {
		handlerCalled = true
		receivedValue = value
	})

	if err != nil {
		t.Fatalf("SubscribeToSensor failed: %v", err)
	}

	// Verify registration
	subs := registry.GetHASubscriptions("test")
	if len(subs) != 1 || subs[0] != "sensor.test" {
		t.Errorf("Expected sensor.test to be registered, got %v", subs)
	}

	// Simulate state change
	haClient.simulateStateChange("sensor.test", nil, &ha.State{
		EntityID: "sensor.test",
		State:    "42.5",
	})

	if !handlerCalled {
		t.Error("Handler was not called")
	}

	if receivedValue != 42.5 {
		t.Errorf("Expected value 42.5, got %v", receivedValue)
	}

	// Shadow tracker should have been updated
	if tracker.updateCount == 0 {
		t.Error("Shadow tracker was not updated")
	}
}

func TestSubscriptionHelper_SubscribeToEntity(t *testing.T) {
	logger := zap.NewNop()
	haClient := newMockHAClient()
	registry := NewSubscriptionRegistry()
	tracker := newMockShadowTracker()

	helper := NewSubscriptionHelper(haClient, nil, registry, tracker, "test", logger)

	// Track if handler was called
	handlerCalled := false
	var receivedState *ha.State

	err := helper.SubscribeToEntity("sensor.test", func(entityID string, oldState, newState *ha.State) {
		handlerCalled = true
		receivedState = newState
	})

	if err != nil {
		t.Fatalf("SubscribeToEntity failed: %v", err)
	}

	// Simulate state change
	newState := &ha.State{EntityID: "sensor.test", State: "on"}
	haClient.simulateStateChange("sensor.test", nil, newState)

	if !handlerCalled {
		t.Error("Handler was not called")
	}

	if receivedState.State != "on" {
		t.Errorf("Expected state 'on', got %v", receivedState.State)
	}

	// Shadow tracker should have been updated
	if tracker.updateCount == 0 {
		t.Error("Shadow tracker was not updated")
	}
}

func TestSubscriptionHelper_UnsubscribeAll(t *testing.T) {
	logger := zap.NewNop()
	haClient := newMockHAClient()
	registry := NewSubscriptionRegistry()
	tracker := newMockShadowTracker()

	helper := NewSubscriptionHelper(haClient, nil, registry, tracker, "test", logger)

	// Subscribe to multiple entities
	_ = helper.SubscribeToSensor("sensor.test1", func(value float64) {})
	_ = helper.SubscribeToSensor("sensor.test2", func(value float64) {})

	if len(helper.GetHASubscriptions()) != 2 {
		t.Errorf("Expected 2 subscriptions, got %d", len(helper.GetHASubscriptions()))
	}

	// Unsubscribe all
	helper.UnsubscribeAll()

	if len(helper.GetHASubscriptions()) != 0 {
		t.Errorf("Expected 0 subscriptions after unsubscribe, got %d", len(helper.GetHASubscriptions()))
	}
}

func TestSubscriptionHelper_CapturesInputsBeforeHandler(t *testing.T) {
	logger := zap.NewNop()
	haClient := newMockHAClient()
	registry := NewSubscriptionRegistry()
	tracker := newMockShadowTracker()

	// Pre-populate some state in the HA client for capture
	haClient.states["sensor.other"] = &ha.State{EntityID: "sensor.other", State: "100"}

	helper := NewSubscriptionHelper(haClient, nil, registry, tracker, "test", logger)

	// Register another entity so it gets captured
	registry.RegisterHASubscription("test", "sensor.other")

	var capturedInputsAtHandlerTime map[string]interface{}

	err := helper.SubscribeToSensor("sensor.trigger", func(value float64) {
		// Capture what the tracker saw when handler was called
		capturedInputsAtHandlerTime = make(map[string]interface{})
		for k, v := range tracker.inputs {
			capturedInputsAtHandlerTime[k] = v
		}
	})

	if err != nil {
		t.Fatalf("SubscribeToSensor failed: %v", err)
	}

	// Simulate state change
	haClient.simulateStateChange("sensor.trigger", nil, &ha.State{
		EntityID: "sensor.trigger",
		State:    "50",
	})

	// Verify that inputs were captured (should include sensor.other and sensor.trigger)
	if capturedInputsAtHandlerTime == nil {
		t.Fatal("Inputs were not captured before handler")
	}

	// sensor.other should have been captured since it was registered
	if _, ok := capturedInputsAtHandlerTime["sensor.other"]; !ok {
		t.Error("Expected sensor.other to be in captured inputs")
	}
}

func TestSubscriptionHelper_NilRegistry(t *testing.T) {
	logger := zap.NewNop()
	haClient := newMockHAClient()
	tracker := newMockShadowTracker()

	// Create helper with nil registry
	helper := NewSubscriptionHelper(haClient, nil, nil, tracker, "test", logger)

	// Should still work, just without input capture
	handlerCalled := false

	err := helper.SubscribeToSensor("sensor.test", func(value float64) {
		handlerCalled = true
	})

	if err != nil {
		t.Fatalf("SubscribeToSensor failed: %v", err)
	}

	// Simulate state change
	haClient.simulateStateChange("sensor.test", nil, &ha.State{
		EntityID: "sensor.test",
		State:    "42.5",
	})

	if !handlerCalled {
		t.Error("Handler was not called")
	}

	// With fallback input capture, shadow tracker should be updated even without registry
	// (as long as the HA client can provide state for subscribed entities)
	if tracker.updateCount != 1 {
		t.Errorf("Shadow tracker should have been updated via fallback capture, got %d updates", tracker.updateCount)
	}
}
