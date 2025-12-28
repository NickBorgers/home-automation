package ha

import (
	"fmt"
	"sync"
	"time"
)

// MockClient implements HAClient interface for testing
type MockClient struct {
	states         map[string]*State
	statesMu       sync.RWMutex
	subscribers    map[string][]subscriberEntry
	subsMu         sync.RWMutex
	nextSubID      int
	nextSubIDMu    sync.Mutex
	connected      bool
	connMu         sync.RWMutex
	serviceCalls   []ServiceCall
	callsMu        sync.Mutex
	getStateCalls  map[string]int // Track GetState calls per entity
	getStateCallMu sync.Mutex

	// Error injection for testing
	serviceErrors   map[string]error // key: "domain.service"
	serviceErrorsMu sync.RWMutex

	// Transient error injection: fail N times, then succeed
	serviceFailCounts   map[string]int // key: "domain.service", value: remaining failures
	serviceFailError    map[string]error
	serviceFailCountsMu sync.Mutex
}

func (m *MockClient) clearSubscribers() {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()

	m.subscribers = make(map[string][]subscriberEntry)
}

// ServiceCall records a service call for testing
type ServiceCall struct {
	Domain  string
	Service string
	Data    map[string]interface{}
	Time    time.Time
}

// mockSubscription implements Subscription interface for MockClient
type mockSubscription struct {
	entityID string
	subID    int
	mock     *MockClient
}

func (s *mockSubscription) Unsubscribe() error {
	return s.mock.unsubscribe(s.entityID, s.subID)
}

// NewMockClient creates a new mock HA client
func NewMockClient() *MockClient {
	return &MockClient{
		states:            make(map[string]*State),
		subscribers:       make(map[string][]subscriberEntry),
		serviceCalls:      make([]ServiceCall, 0),
		getStateCalls:     make(map[string]int),
		serviceErrors:     make(map[string]error),
		serviceFailCounts: make(map[string]int),
		serviceFailError:  make(map[string]error),
		connected:         false,
	}
}

// SetServiceError configures the mock to return an error for a specific service call.
// The key format is "domain.service" (e.g., "media_player.volume_mute").
// Pass nil to clear an error.
func (m *MockClient) SetServiceError(domain, service string, err error) {
	m.serviceErrorsMu.Lock()
	defer m.serviceErrorsMu.Unlock()
	key := domain + "." + service
	if err == nil {
		delete(m.serviceErrors, key)
	} else {
		m.serviceErrors[key] = err
	}
}

// SetServiceFailCount configures the mock to fail a service call a specific number of times
// before succeeding. This is useful for testing retry logic.
// After failCount failures, subsequent calls will succeed.
// Pass 0 to clear.
func (m *MockClient) SetServiceFailCount(domain, service string, failCount int, err error) {
	m.serviceFailCountsMu.Lock()
	defer m.serviceFailCountsMu.Unlock()
	key := domain + "." + service
	if failCount == 0 {
		delete(m.serviceFailCounts, key)
		delete(m.serviceFailError, key)
	} else {
		m.serviceFailCounts[key] = failCount
		m.serviceFailError[key] = err
	}
}

// Connect simulates connecting to Home Assistant
func (m *MockClient) Connect() error {
	m.connMu.Lock()
	defer m.connMu.Unlock()

	if m.connected {
		return fmt.Errorf("already connected")
	}

	m.connected = true
	return nil
}

// Disconnect simulates disconnecting
func (m *MockClient) Disconnect() error {
	m.connMu.Lock()
	defer m.connMu.Unlock()

	m.connected = false
	m.clearSubscribers()
	return nil
}

// IsConnected returns connection status
func (m *MockClient) IsConnected() bool {
	m.connMu.RLock()
	defer m.connMu.RUnlock()
	return m.connected
}

// GetState retrieves a mock state
func (m *MockClient) GetState(entityID string) (*State, error) {
	// Track the GetState call
	m.getStateCallMu.Lock()
	m.getStateCalls[entityID]++
	m.getStateCallMu.Unlock()

	m.statesMu.RLock()
	defer m.statesMu.RUnlock()

	state, ok := m.states[entityID]
	if !ok {
		return nil, fmt.Errorf("entity %s not found", entityID)
	}

	return state, nil
}

// GetAllStates retrieves all mock states
func (m *MockClient) GetAllStates() ([]*State, error) {
	m.statesMu.RLock()
	defer m.statesMu.RUnlock()

	states := make([]*State, 0, len(m.states))
	for _, state := range m.states {
		states = append(states, state)
	}

	return states, nil
}

// CallService records a service call
func (m *MockClient) CallService(domain, service string, data map[string]interface{}) error {
	key := domain + "." + service

	// Check for transient failures first (fail N times, then succeed)
	m.serviceFailCountsMu.Lock()
	if remainingFailures, exists := m.serviceFailCounts[key]; exists && remainingFailures > 0 {
		m.serviceFailCounts[key] = remainingFailures - 1
		err := m.serviceFailError[key]
		m.serviceFailCountsMu.Unlock()
		return err
	}
	m.serviceFailCountsMu.Unlock()

	// Check for permanent injected errors
	m.serviceErrorsMu.RLock()
	if err, exists := m.serviceErrors[key]; exists {
		m.serviceErrorsMu.RUnlock()
		return err
	}
	m.serviceErrorsMu.RUnlock()

	m.callsMu.Lock()
	m.serviceCalls = append(m.serviceCalls, ServiceCall{
		Domain:  domain,
		Service: service,
		Data:    data,
		Time:    time.Now(),
	})
	m.callsMu.Unlock()

	// Update mock state based on service call
	// Handle both single entity_id and array of entity_ids
	if entityID, ok := data["entity_id"].(string); ok {
		m.updateStateFromServiceCall(entityID, domain, service, data)
	} else if entityIDs, ok := data["entity_id"].([]string); ok {
		for _, entityID := range entityIDs {
			m.updateStateFromServiceCall(entityID, domain, service, data)
		}
	}

	return nil
}

// SubscribeStateChanges subscribes to state changes
func (m *MockClient) SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error) {
	// Get unique subscription ID
	m.nextSubIDMu.Lock()
	subID := m.nextSubID
	m.nextSubID++
	m.nextSubIDMu.Unlock()

	// Add subscriber entry
	m.subsMu.Lock()
	m.subscribers[entityID] = append(m.subscribers[entityID], subscriberEntry{
		subID:   subID,
		handler: handler,
	})
	m.subsMu.Unlock()

	return &mockSubscription{
		entityID: entityID,
		subID:    subID,
		mock:     m,
	}, nil
}

// unsubscribe removes a specific subscription by entity ID and subscription ID
func (m *MockClient) unsubscribe(entityID string, subID int) error {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()

	subscribers, ok := m.subscribers[entityID]
	if !ok {
		return nil // Already unsubscribed
	}

	// Find and remove the subscription with matching subID
	for i, entry := range subscribers {
		if entry.subID == subID {
			// Remove this entry by slicing
			m.subscribers[entityID] = append(subscribers[:i], subscribers[i+1:]...)

			// If no more subscribers for this entity, delete the entry
			if len(m.subscribers[entityID]) == 0 {
				delete(m.subscribers, entityID)
			}
			break
		}
	}

	return nil
}

// SetInputBoolean sets a mock input_boolean
func (m *MockClient) SetInputBoolean(name string, value bool) error {
	service := "turn_off"
	if value {
		service = "turn_on"
	}

	return m.CallService("input_boolean", service, map[string]interface{}{
		"entity_id": fmt.Sprintf("input_boolean.%s", name),
	})
}

// SetInputNumber sets a mock input_number
func (m *MockClient) SetInputNumber(name string, value float64) error {
	return m.CallService("input_number", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_number.%s", name),
		"value":     value,
	})
}

// SetInputText sets a mock input_text
func (m *MockClient) SetInputText(name string, value string) error {
	return m.CallService("input_text", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_text.%s", name),
		"value":     value,
	})
}

// SetState sets a mock state (for testing)
func (m *MockClient) SetState(entityID string, stateValue string, attributes map[string]interface{}) {
	m.statesMu.Lock()

	now := time.Now()
	oldState := m.states[entityID]

	newState := &State{
		EntityID:    entityID,
		State:       stateValue,
		Attributes:  attributes,
		LastChanged: now,
		LastUpdated: now,
	}

	m.states[entityID] = newState
	m.statesMu.Unlock()

	// Notify subscribers (after unlocking to avoid deadlock when callbacks call back into the client)
	m.notifySubscribers(entityID, oldState, newState)
}

// SimulateStateChange simulates a state change event
func (m *MockClient) SimulateStateChange(entityID string, newStateValue string) {
	m.statesMu.Lock()
	oldState := m.states[entityID]

	now := time.Now()
	newState := &State{
		EntityID:    entityID,
		State:       newStateValue,
		Attributes:  make(map[string]interface{}),
		LastChanged: now,
		LastUpdated: now,
	}

	if oldState != nil {
		newState.Attributes = oldState.Attributes
	}

	m.states[entityID] = newState
	m.statesMu.Unlock()

	// Notify subscribers
	m.notifySubscribers(entityID, oldState, newState)
}

// GetServiceCalls returns all recorded service calls
func (m *MockClient) GetServiceCalls() []ServiceCall {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()

	calls := make([]ServiceCall, len(m.serviceCalls))
	copy(calls, m.serviceCalls)
	return calls
}

// ClearServiceCalls clears the service call history
func (m *MockClient) ClearServiceCalls() {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()
	m.serviceCalls = make([]ServiceCall, 0)
}

// updateStateFromServiceCall updates state based on a service call
func (m *MockClient) updateStateFromServiceCall(entityID, domain, service string, data map[string]interface{}) {
	// Compute state update while holding the lock
	m.statesMu.Lock()

	oldState := m.states[entityID]
	now := time.Now()

	var newStateValue string
	attributes := make(map[string]interface{})

	if oldState != nil {
		newStateValue = oldState.State
		// Make a shallow copy of attributes to avoid sharing references
		for k, v := range oldState.Attributes {
			attributes[k] = v
		}
	}

	switch domain {
	case "input_boolean", "switch":
		if service == "turn_on" {
			newStateValue = "on"
		} else if service == "turn_off" {
			newStateValue = "off"
		}
	case "input_number":
		if value, ok := data["value"].(float64); ok {
			newStateValue = fmt.Sprintf("%.2f", value)
		}
	case "input_text":
		if value, ok := data["value"].(string); ok {
			newStateValue = value
		}
	}

	newState := &State{
		EntityID:    entityID,
		State:       newStateValue,
		Attributes:  attributes,
		LastChanged: now,
		LastUpdated: now,
	}

	m.states[entityID] = newState

	// Only notify subscribers if state actually changed
	stateChanged := oldState == nil || oldState.State != newStateValue

	// Release lock before notifying subscribers to avoid deadlock when callbacks call back into the client
	m.statesMu.Unlock()

	// Notify subscribers only if state changed (outside the lock)
	if stateChanged {
		m.notifySubscribers(entityID, oldState, newState)
	}
}

// notifySubscribers notifies all subscribers of a state change
func (m *MockClient) notifySubscribers(entityID string, oldState, newState *State) {
	m.subsMu.RLock()
	entries := append([]subscriberEntry(nil), m.subscribers[entityID]...)
	m.subsMu.RUnlock()

	for _, entry := range entries {
		entry.handler(entityID, oldState, newState)
	}
}

// SetMockState is a convenience method for tests to set state with explicit State struct
func (m *MockClient) SetMockState(entityID string, state *State) {
	m.statesMu.Lock()
	defer m.statesMu.Unlock()

	m.states[entityID] = state
}

// WasGetStateCalled returns true if GetState was called for the given entity
func (m *MockClient) WasGetStateCalled(entityID string) bool {
	m.getStateCallMu.Lock()
	defer m.getStateCallMu.Unlock()

	count, ok := m.getStateCalls[entityID]
	return ok && count > 0
}

// GetStateCallCount returns the number of times GetState was called for the given entity
func (m *MockClient) GetStateCallCount(entityID string) int {
	m.getStateCallMu.Lock()
	defer m.getStateCallMu.Unlock()

	return m.getStateCalls[entityID]
}

// ClearGetStateCalls clears the GetState call tracking
func (m *MockClient) ClearGetStateCalls() {
	m.getStateCallMu.Lock()
	defer m.getStateCallMu.Unlock()

	m.getStateCalls = make(map[string]int)
}

// GetSubscribedEntities returns a list of all entity IDs that have active subscriptions
func (m *MockClient) GetSubscribedEntities() []string {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()

	entities := make([]string, 0, len(m.subscribers))
	for entityID := range m.subscribers {
		entities = append(entities, entityID)
	}
	return entities
}
