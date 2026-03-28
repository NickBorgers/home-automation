package ha

import (
	"context"
	"encoding/json"
	"errors"
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

	// State sequence injection: return different states on successive GetState calls
	stateSequences   map[string][]string // key: entityID, value: sequence of states to return
	stateSequenceIdx map[string]int      // key: entityID, value: current index in sequence
	stateSequenceMu  sync.Mutex

	// Device and entity registry for dynamic discovery
	devices        []*Device
	devicesMu      sync.RWMutex
	entityRegistry []*EntityRegistryEntry
	entityRegMu    sync.RWMutex

	// Service response injection for CallServiceWithResponse
	serviceResponses   map[string]json.RawMessage // key: "domain.service"
	serviceResponsesMu sync.RWMutex

	// Config entry reload tracking
	configReloads   []ConfigEntryReload
	configReloadsMu sync.Mutex
	reloadErrors    map[string]error // key: entry_id
	reloadErrorsMu  sync.RWMutex
}

// ConfigEntryReload records a config entry reload for testing
type ConfigEntryReload struct {
	EntryID string
	Time    time.Time
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
	Target  *ServiceTarget
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
		stateSequences:    make(map[string][]string),
		stateSequenceIdx:  make(map[string]int),
		serviceResponses:  make(map[string]json.RawMessage),
		configReloads:     make([]ConfigEntryReload, 0),
		reloadErrors:      make(map[string]error),
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

// IsHealthy returns true for mock client (always healthy in tests)
func (m *MockClient) IsHealthy() bool {
	return m.IsConnected()
}

// GetState retrieves a mock state
func (m *MockClient) GetState(entityID string) (*State, error) {
	// Track the GetState call
	m.getStateCallMu.Lock()
	m.getStateCalls[entityID]++
	m.getStateCallMu.Unlock()

	// Check if there's a state sequence configured for this entity
	m.stateSequenceMu.Lock()
	if seq, exists := m.stateSequences[entityID]; exists && len(seq) > 0 {
		idx := m.stateSequenceIdx[entityID]
		stateValue := seq[idx]
		// Advance to next state in sequence, staying at last if exhausted
		if idx < len(seq)-1 {
			m.stateSequenceIdx[entityID] = idx + 1
		}
		m.stateSequenceMu.Unlock()

		return &State{
			EntityID:    entityID,
			State:       stateValue,
			Attributes:  make(map[string]interface{}),
			LastChanged: time.Now(),
			LastUpdated: time.Now(),
		}, nil
	}
	m.stateSequenceMu.Unlock()

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
func (m *MockClient) CallService(ctx context.Context, domain, service string, data map[string]interface{}) error {
	// Check for context cancellation
	select {
	case <-ctx.Done():
		return fmt.Errorf("service call cancelled: %w", ctx.Err())
	default:
	}

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

// CallServiceWithTarget records a service call with target
func (m *MockClient) CallServiceWithTarget(ctx context.Context, domain, service string, target *ServiceTarget, data map[string]interface{}) error {
	// Check for context cancellation
	select {
	case <-ctx.Done():
		return fmt.Errorf("service call with target cancelled: %w", ctx.Err())
	default:
	}

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
		Target:  target,
		Time:    time.Now(),
	})
	m.callsMu.Unlock()

	return nil
}

// CallServiceWithResponse records a service call and returns injected response data.
func (m *MockClient) CallServiceWithResponse(ctx context.Context, domain, service string, data map[string]interface{}) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("service call cancelled: %w", ctx.Err())
	default:
	}

	key := domain + "." + service

	// Check for permanent injected errors
	m.serviceErrorsMu.RLock()
	if err, exists := m.serviceErrors[key]; exists {
		m.serviceErrorsMu.RUnlock()
		return nil, err
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

	// Return injected response if available
	m.serviceResponsesMu.RLock()
	resp, exists := m.serviceResponses[key]
	m.serviceResponsesMu.RUnlock()
	if exists {
		return resp, nil
	}

	return nil, nil
}

// SetServiceResponse configures the mock to return specific response data for CallServiceWithResponse.
// The key format is "domain.service" (e.g., "weather.get_forecasts").
func (m *MockClient) SetServiceResponse(domain, service string, response json.RawMessage) {
	m.serviceResponsesMu.Lock()
	defer m.serviceResponsesMu.Unlock()
	m.serviceResponses[domain+"."+service] = response
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

	return m.CallService(context.Background(), "input_boolean", service, map[string]interface{}{
		"entity_id": fmt.Sprintf("input_boolean.%s", name),
	})
}

// SetInputNumber sets a mock input_number
func (m *MockClient) SetInputNumber(name string, value float64) error {
	return m.CallService(context.Background(), "input_number", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_number.%s", name),
		"value":     value,
	})
}

// SetInputText sets a mock input_text
func (m *MockClient) SetInputText(name string, value string) error {
	return m.CallService(context.Background(), "input_text", "set_value", map[string]interface{}{
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

// ClearServiceCalls clears the service call history.
// Deprecated: Use ServiceCallCount() + GetServiceCallsSince() instead for race-free tracking.
func (m *MockClient) ClearServiceCalls() {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()
	m.serviceCalls = make([]ServiceCall, 0)
}

// ServiceCallCount returns the current number of recorded service calls.
// Use this to take a snapshot index before triggering an action, then pass
// the index to GetServiceCallsSince() to retrieve only the new calls.
func (m *MockClient) ServiceCallCount() int {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()
	return len(m.serviceCalls)
}

// GetServiceCallsSince returns all service calls recorded after the given index.
// Use with ServiceCallCount() for race-free assertion windows:
//
//	snapshot := mock.ServiceCallCount()
//	// ... trigger action ...
//	calls := mock.GetServiceCallsSince(snapshot)
func (m *MockClient) GetServiceCallsSince(index int) []ServiceCall {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()
	if index >= len(m.serviceCalls) {
		return nil
	}
	calls := make([]ServiceCall, len(m.serviceCalls)-index)
	copy(calls, m.serviceCalls[index:])
	return calls
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

// SetStateSequence configures a sequence of states to return on successive GetState calls.
// The first call returns states[0], second returns states[1], etc.
// After the sequence is exhausted, the last state continues to be returned.
// Pass nil or empty slice to clear the sequence.
func (m *MockClient) SetStateSequence(entityID string, states []string) {
	m.stateSequenceMu.Lock()
	defer m.stateSequenceMu.Unlock()

	if len(states) == 0 {
		delete(m.stateSequences, entityID)
		delete(m.stateSequenceIdx, entityID)
	} else {
		m.stateSequences[entityID] = states
		m.stateSequenceIdx[entityID] = 0
	}
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

// SendNotification sends a notification via the mock client.
// Records the call as a service call for verification in tests.
func (m *MockClient) SendNotification(deviceName string, notification *Notification) error {
	if err := validateNotification(deviceName, notification); err != nil {
		return err
	}

	serviceData := buildNotificationServiceData(notification)
	serviceName := fmt.Sprintf("mobile_app_%s", deviceName)

	return m.CallService(context.Background(), "notify", serviceName, serviceData)
}

// SendNotificationToMultiple sends a notification to multiple devices.
// If multiple notifications fail, all errors are aggregated and returned.
func (m *MockClient) SendNotificationToMultiple(deviceNames []string, notification *Notification) error {
	if len(deviceNames) == 0 {
		return fmt.Errorf("at least one device name is required")
	}

	var errs []error
	for _, deviceName := range deviceNames {
		if err := m.SendNotification(deviceName, notification); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", deviceName, err))
		}
	}

	return errors.Join(errs...)
}

// ClearNotification clears a notification with the specified tag on a device.
func (m *MockClient) ClearNotification(deviceName, tag string) error {
	if deviceName == "" {
		return fmt.Errorf("device name is required")
	}
	if tag == "" {
		return fmt.Errorf("tag is required to clear a notification")
	}

	serviceData := buildClearNotificationServiceData(tag)
	serviceName := fmt.Sprintf("mobile_app_%s", deviceName)

	return m.CallService(context.Background(), "notify", serviceName, serviceData)
}

// GetNotificationCalls returns all notification service calls for verification.
// Filters service calls to only include notify domain calls.
func (m *MockClient) GetNotificationCalls() []ServiceCall {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()

	var notifications []ServiceCall
	for _, call := range m.serviceCalls {
		if call.Domain == "notify" {
			notifications = append(notifications, call)
		}
	}
	return notifications
}

// SetReconnectCallback is a no-op for the mock client.
// It implements the HAClient interface for testing.
func (m *MockClient) SetReconnectCallback(cb func()) {
	// No-op in mock - reconnection logic is not simulated
}

// GetReconnectCount returns 0 for the mock client.
// It implements the HAClient interface for testing.
func (m *MockClient) GetReconnectCount() int {
	return 0
}

// GetDisconnectCount returns 0 for the mock client.
// It implements the HAClient interface for testing.
func (m *MockClient) GetDisconnectCount() int {
	return 0
}

// GetLastDisconnectTime returns zero time for the mock client.
// It implements the HAClient interface for testing.
func (m *MockClient) GetLastDisconnectTime() time.Time {
	return time.Time{}
}

// GetWriteTimeoutCount returns 0 for the mock client.
// It implements the HAClient interface for testing.
func (m *MockClient) GetWriteTimeoutCount() int {
	return 0
}

// GetConnectionDuration returns 0 for the mock client.
// It implements the HAClient interface for testing.
func (m *MockClient) GetConnectionDuration() time.Duration {
	return 0
}

// WaitForHandlers is a no-op for MockClient since MockClient dispatches
// handlers synchronously (no goroutines spawned).
func (m *MockClient) WaitForHandlers() {}

// GetDevices returns the mock device registry.
// It implements the HAClient interface for testing.
func (m *MockClient) GetDevices() ([]*Device, error) {
	m.devicesMu.RLock()
	defer m.devicesMu.RUnlock()

	devices := make([]*Device, len(m.devices))
	copy(devices, m.devices)
	return devices, nil
}

// GetEntityRegistry returns the mock entity registry.
// It implements the HAClient interface for testing.
func (m *MockClient) GetEntityRegistry() ([]*EntityRegistryEntry, error) {
	m.entityRegMu.RLock()
	defer m.entityRegMu.RUnlock()

	entities := make([]*EntityRegistryEntry, len(m.entityRegistry))
	copy(entities, m.entityRegistry)
	return entities, nil
}

// SetDevices sets the mock device registry for testing.
func (m *MockClient) SetDevices(devices []*Device) {
	m.devicesMu.Lock()
	defer m.devicesMu.Unlock()
	m.devices = devices
}

// SetEntityRegistry sets the mock entity registry for testing.
func (m *MockClient) SetEntityRegistry(entities []*EntityRegistryEntry) {
	m.entityRegMu.Lock()
	defer m.entityRegMu.Unlock()
	m.entityRegistry = entities
}

// AddDevice adds a device to the mock device registry.
func (m *MockClient) AddDevice(device *Device) {
	m.devicesMu.Lock()
	defer m.devicesMu.Unlock()
	m.devices = append(m.devices, device)
}

// AddEntityRegistryEntry adds an entity to the mock entity registry.
func (m *MockClient) AddEntityRegistryEntry(entry *EntityRegistryEntry) {
	m.entityRegMu.Lock()
	defer m.entityRegMu.Unlock()
	m.entityRegistry = append(m.entityRegistry, entry)
}

// ReloadConfigEntry records a config entry reload for testing.
func (m *MockClient) ReloadConfigEntry(ctx context.Context, entryID string) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("config entry reload cancelled: %w", ctx.Err())
	default:
	}

	m.reloadErrorsMu.RLock()
	if err, exists := m.reloadErrors[entryID]; exists {
		m.reloadErrorsMu.RUnlock()
		return err
	}
	m.reloadErrorsMu.RUnlock()

	m.configReloadsMu.Lock()
	m.configReloads = append(m.configReloads, ConfigEntryReload{
		EntryID: entryID,
		Time:    time.Now(),
	})
	m.configReloadsMu.Unlock()

	return nil
}

// SetReloadError configures the mock to return an error for a config entry reload.
func (m *MockClient) SetReloadError(entryID string, err error) {
	m.reloadErrorsMu.Lock()
	defer m.reloadErrorsMu.Unlock()
	if err == nil {
		delete(m.reloadErrors, entryID)
	} else {
		m.reloadErrors[entryID] = err
	}
}

// GetConfigEntryReloads returns all recorded config entry reloads.
func (m *MockClient) GetConfigEntryReloads() []ConfigEntryReload {
	m.configReloadsMu.Lock()
	defer m.configReloadsMu.Unlock()

	reloads := make([]ConfigEntryReload, len(m.configReloads))
	copy(reloads, m.configReloads)
	return reloads
}

// ClearConfigEntryReloads clears the config entry reload history.
func (m *MockClient) ClearConfigEntryReloads() {
	m.configReloadsMu.Lock()
	defer m.configReloadsMu.Unlock()
	m.configReloads = make([]ConfigEntryReload, 0)
}
