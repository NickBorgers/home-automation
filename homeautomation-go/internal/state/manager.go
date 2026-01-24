package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"homeautomation/internal/ha"

	"go.uber.org/zap"
)

// ErrReadOnlyMode is returned when attempting to modify state in read-only mode
var ErrReadOnlyMode = errors.New("state manager is in read-only mode")

// StateChangeHandler is called when a state variable changes
type StateChangeHandler func(key string, oldValue, newValue interface{})

// Subscription represents an active state change subscription
type Subscription interface {
	Unsubscribe()
}

type subscription struct {
	key     string
	id      uint64
	manager *Manager
}

func (s *subscription) Unsubscribe() {
	s.manager.unsubscribe(s.key, s.id)
}

// Manager manages state synchronization with Home Assistant
type Manager struct {
	client      ha.HAClient
	logger      *zap.Logger
	cache       map[string]interface{}
	cacheMu     sync.RWMutex
	variables   map[string]StateVariable
	entityToKey map[string]string
	subscribers map[string]map[uint64]StateChangeHandler
	subsMu      sync.RWMutex
	haSubs      map[string]ha.Subscription
	haSubsMu    sync.Mutex
	nextSubID   uint64
	readOnly    bool

	// wakeSequenceLatch is internal state for computed isAnyoneHomeAndAwake.
	// When the wake sequence activates (isWakeSequenceActive false->true),
	// this latch is set to keep isAnyoneHomeAndAwake=true even if someone
	// is still asleep. The latch clears when isMasterAsleep becomes false.
	// Protected by cacheMu for simplicity (accessed during recomputation).
	wakeSequenceLatch bool
}

// NewManager creates a new state manager
func NewManager(client ha.HAClient, logger *zap.Logger, readOnly bool) *Manager {
	variables := VariablesByKey()
	entityToKey := make(map[string]string)

	for key, v := range variables {
		entityToKey[v.EntityID] = key
	}

	return &Manager{
		client:      client,
		logger:      logger,
		cache:       make(map[string]interface{}),
		variables:   variables,
		entityToKey: entityToKey,
		subscribers: make(map[string]map[uint64]StateChangeHandler),
		haSubs:      make(map[string]ha.Subscription),
		readOnly:    readOnly,
	}
}

// SyncFromHA reads all state variables from Home Assistant
func (m *Manager) SyncFromHA() error {
	m.logger.Info("Syncing state from Home Assistant...")

	states, err := m.client.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get states: %w", err)
	}

	// Create a map for quick lookup
	stateMap := make(map[string]*ha.State)
	for _, state := range states {
		stateMap[state.EntityID] = state
	}

	// Sync each variable
	syncCount := 0
	localCount := 0
	for _, variable := range AllVariables {
		// Skip local-only variables (not synced with HA)
		if variable.LocalOnly {
			m.cacheMu.Lock()
			m.cache[variable.Key] = variable.Default
			m.cacheMu.Unlock()
			localCount++
			m.logger.Debug("Initialized local-only variable",
				zap.String("key", variable.Key))
			continue
		}

		state, ok := stateMap[variable.EntityID]
		if !ok {
			m.logger.Warn("Entity not found in HA, using default",
				zap.String("entity_id", variable.EntityID),
				zap.String("key", variable.Key))
			m.cacheMu.Lock()
			m.cache[variable.Key] = variable.Default
			m.cacheMu.Unlock()
			continue
		}

		// Parse and cache the value
		value, err := m.parseStateValue(state.State, variable.Type)
		if err != nil {
			m.logger.Error("Failed to parse state value",
				zap.String("entity_id", variable.EntityID),
				zap.String("key", variable.Key),
				zap.Error(err))
			m.cacheMu.Lock()
			m.cache[variable.Key] = variable.Default
			m.cacheMu.Unlock()
			continue
		}

		m.cacheMu.Lock()
		m.cache[variable.Key] = value
		m.cacheMu.Unlock()
		syncCount++

		// Subscribe to state changes
		if err := m.subscribeToEntity(variable.EntityID, variable.Key); err != nil {
			m.logger.Warn("Failed to subscribe to entity",
				zap.String("entity_id", variable.EntityID),
				zap.Error(err))
		}
	}

	m.logger.Info("State sync complete",
		zap.Int("synced", syncCount),
		zap.Int("local_only", localCount),
		zap.Int("total", len(AllVariables)))

	return nil
}

// parseStateValue parses a state string into the appropriate type
func (m *Manager) parseStateValue(stateStr string, varType StateType) (interface{}, error) {
	switch varType {
	case TypeBool:
		return stateStr == "on", nil
	case TypeNumber:
		return strconv.ParseFloat(stateStr, 64)
	case TypeString:
		return stateStr, nil
	case TypeJSON:
		var result interface{}
		if err := json.Unmarshal([]byte(stateStr), &result); err != nil {
			// If it's not valid JSON, return empty object
			return map[string]interface{}{}, nil
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown type: %s", varType)
	}
}

// subscribeToEntity subscribes to state changes for an entity
func (m *Manager) subscribeToEntity(entityID, key string) error {
	m.haSubsMu.Lock()
	if _, exists := m.haSubs[entityID]; exists {
		m.haSubsMu.Unlock()
		return nil
	}
	m.haSubsMu.Unlock()

	sub, err := m.client.SubscribeStateChanges(entityID, func(entity string, oldState, newState *ha.State) {
		if newState == nil {
			return
		}

		variable, ok := m.variables[key]
		if !ok {
			return
		}

		// Parse new value
		newValue, err := m.parseStateValue(newState.State, variable.Type)
		if err != nil {
			m.logger.Error("Failed to parse state change",
				zap.String("entity_id", entityID),
				zap.String("key", key),
				zap.Error(err))
			return
		}

		// Update cache and check if value actually changed
		m.cacheMu.Lock()
		oldValue := m.cache[key]
		if reflect.DeepEqual(oldValue, newValue) {
			// Value hasn't changed, skip notification
			m.cacheMu.Unlock()
			return
		}
		m.cache[key] = newValue
		m.cacheMu.Unlock()

		m.logger.Debug("State changed",
			zap.String("key", key),
			zap.Any("old", oldValue),
			zap.Any("new", newValue))

		// Notify subscribers
		m.notifySubscribers(key, oldValue, newValue)
	})

	if err != nil {
		return err
	}

	m.haSubsMu.Lock()
	m.haSubs[entityID] = sub
	m.haSubsMu.Unlock()
	return nil
}

// notifySubscribers notifies all subscribers of a state change
func (m *Manager) notifySubscribers(key string, oldValue, newValue interface{}) {
	m.subsMu.RLock()
	entries := m.subscribers[key]
	ids := make([]uint64, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	handlers := make([]StateChangeHandler, 0, len(ids))
	for _, id := range ids {
		handlers = append(handlers, entries[id])
	}
	m.subsMu.RUnlock()

	for idx, handler := range handlers {
		func(h StateChangeHandler, ordinal int) {
			defer func() {
				if r := recover(); r != nil {
					m.logger.Warn("State change handler panicked",
						zap.String("key", key),
						zap.Int("handler_index", ordinal),
						zap.Any("panic", r),
						zap.Stack("stack"))
				}
			}()

			h(key, oldValue, newValue)
		}(handler, idx)
	}
}

func (m *Manager) ensureWritable(variable StateVariable) error {
	if variable.ReadOnly {
		return fmt.Errorf("variable %s is read-only", variable.Key)
	}
	// Allow writes to computed outputs even in read-only mode
	// These are values calculated by the Go code that need to be published to HA
	if m.readOnly && !variable.LocalOnly && !variable.ComputedOutput {
		return ErrReadOnlyMode
	}
	return nil
}

// GetBool retrieves a boolean state variable
func (m *Manager) GetBool(key string) (bool, error) {
	variable, ok := m.variables[key]
	if !ok {
		return false, fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeBool {
		return false, fmt.Errorf("variable %s is not a boolean", key)
	}

	m.cacheMu.RLock()
	value, ok := m.cache[key]
	m.cacheMu.RUnlock()

	if !ok {
		return variable.Default.(bool), nil
	}

	boolValue, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("cached value for %s is not a boolean", key)
	}

	return boolValue, nil
}

// SetBool sets a boolean state variable
func (m *Manager) SetBool(key string, value bool) error {
	variable, ok := m.variables[key]
	if !ok {
		return fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeBool {
		return fmt.Errorf("variable %s is not a boolean", key)
	}
	if err := m.ensureWritable(variable); err != nil {
		return err
	}

	// Check if value has actually changed
	m.cacheMu.Lock()
	oldValue, ok := m.cache[key]
	if ok {
		if oldBool, isBool := oldValue.(bool); isBool && oldBool == value {
			// Value hasn't changed, skip update
			m.cacheMu.Unlock()
			return nil
		}
	}

	// Update cache
	m.cache[key] = value
	m.cacheMu.Unlock()

	// Skip HA sync for local-only variables, but still notify subscribers
	if variable.LocalOnly {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Skip HA sync if no client (e.g., in tests)
	if m.client == nil {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Sync to HA
	entityName := extractEntityName(variable.EntityID)
	if err := m.client.SetInputBoolean(entityName, value); err != nil {
		// Rollback cache on error
		m.cacheMu.Lock()
		m.cache[key] = oldValue
		m.cacheMu.Unlock()
		return fmt.Errorf("failed to set HA value: %w", err)
	}

	// Notify subscribers after successful HA sync
	// The HA callback will skip notification since cache already has the new value
	m.notifySubscribers(key, oldValue, value)

	return nil
}

// GetString retrieves a string state variable
func (m *Manager) GetString(key string) (string, error) {
	variable, ok := m.variables[key]
	if !ok {
		return "", fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeString {
		return "", fmt.Errorf("variable %s is not a string", key)
	}

	m.cacheMu.RLock()
	value, ok := m.cache[key]
	m.cacheMu.RUnlock()

	if !ok {
		return variable.Default.(string), nil
	}

	strValue, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("cached value for %s is not a string", key)
	}

	return strValue, nil
}

// SetString sets a string state variable
func (m *Manager) SetString(key string, value string) error {
	variable, ok := m.variables[key]
	if !ok {
		return fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeString {
		return fmt.Errorf("variable %s is not a string", key)
	}
	if err := m.ensureWritable(variable); err != nil {
		return err
	}

	// Check if value has actually changed
	m.cacheMu.Lock()
	oldValue, ok := m.cache[key]
	if ok {
		if oldStr, isStr := oldValue.(string); isStr && oldStr == value {
			// Value hasn't changed, skip update
			m.cacheMu.Unlock()
			return nil
		}
	}

	// Update cache
	m.cache[key] = value
	m.cacheMu.Unlock()

	// Skip HA sync for local-only variables, but still notify subscribers
	if variable.LocalOnly {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Skip HA sync if no client (e.g., in tests)
	if m.client == nil {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Sync to HA
	entityName := extractEntityName(variable.EntityID)
	if err := m.client.SetInputText(entityName, value); err != nil {
		// Rollback cache on error
		m.cacheMu.Lock()
		m.cache[key] = oldValue
		m.cacheMu.Unlock()
		return fmt.Errorf("failed to set HA value: %w", err)
	}

	// Notify subscribers after successful HA sync
	// The HA callback will skip notification since cache already has the new value
	m.notifySubscribers(key, oldValue, value)

	return nil
}

// GetNumber retrieves a number state variable
func (m *Manager) GetNumber(key string) (float64, error) {
	variable, ok := m.variables[key]
	if !ok {
		return 0, fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeNumber {
		return 0, fmt.Errorf("variable %s is not a number", key)
	}

	m.cacheMu.RLock()
	value, ok := m.cache[key]
	m.cacheMu.RUnlock()

	if !ok {
		return variable.Default.(float64), nil
	}

	numValue, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("cached value for %s is not a number", key)
	}

	return numValue, nil
}

// SetNumber sets a number state variable
func (m *Manager) SetNumber(key string, value float64) error {
	variable, ok := m.variables[key]
	if !ok {
		return fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeNumber {
		return fmt.Errorf("variable %s is not a number", key)
	}
	if err := m.ensureWritable(variable); err != nil {
		return err
	}

	// Check if value has actually changed
	m.cacheMu.Lock()
	oldValue, ok := m.cache[key]
	if ok {
		if oldNum, isNum := oldValue.(float64); isNum && oldNum == value {
			// Value hasn't changed, skip update
			m.cacheMu.Unlock()
			return nil
		}
	}

	// Update cache
	m.cache[key] = value
	m.cacheMu.Unlock()

	// Skip HA sync for local-only variables, but still notify subscribers
	if variable.LocalOnly {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Skip HA sync if no client (e.g., in tests)
	if m.client == nil {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Sync to HA
	entityName := extractEntityName(variable.EntityID)
	if err := m.client.SetInputNumber(entityName, value); err != nil {
		// Rollback cache on error
		m.cacheMu.Lock()
		m.cache[key] = oldValue
		m.cacheMu.Unlock()
		return fmt.Errorf("failed to set HA value: %w", err)
	}

	// Notify subscribers after successful HA sync
	// The HA callback will skip notification since cache already has the new value
	m.notifySubscribers(key, oldValue, value)

	return nil
}

// GetJSON retrieves a JSON state variable
func (m *Manager) GetJSON(key string, target interface{}) error {
	variable, ok := m.variables[key]
	if !ok {
		return fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeJSON {
		return fmt.Errorf("variable %s is not JSON", key)
	}

	m.cacheMu.RLock()
	value, ok := m.cache[key]
	m.cacheMu.RUnlock()

	if !ok {
		jsonBytes, err := marshalJSONValue(variable.Default)
		if err != nil {
			return fmt.Errorf("invalid default for %s: %w", key, err)
		}
		return json.Unmarshal(jsonBytes, target)
	}

	// Marshal and unmarshal to convert to target type
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal cached value: %w", err)
	}

	return json.Unmarshal(jsonBytes, target)
}

// SetJSON sets a JSON state variable
func (m *Manager) SetJSON(key string, value interface{}) error {
	variable, ok := m.variables[key]
	if !ok {
		return fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeJSON {
		return fmt.Errorf("variable %s is not JSON", key)
	}
	if err := m.ensureWritable(variable); err != nil {
		return err
	}

	// Check if value has actually changed (using deep equality for JSON)
	m.cacheMu.Lock()
	oldValue, ok := m.cache[key]
	if ok && reflect.DeepEqual(oldValue, value) {
		// Value hasn't changed, skip update
		m.cacheMu.Unlock()
		return nil
	}

	// Update cache
	m.cache[key] = value
	m.cacheMu.Unlock()

	// Skip HA sync for local-only variables, but still notify subscribers
	if variable.LocalOnly {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Skip HA sync if no client (e.g., in tests)
	if m.client == nil {
		m.notifySubscribers(key, oldValue, value)
		return nil
	}

	// Convert to JSON string for HA
	jsonBytes, err := json.Marshal(value)
	if err != nil {
		// Rollback cache on error
		m.cacheMu.Lock()
		m.cache[key] = oldValue
		m.cacheMu.Unlock()
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Sync to HA
	entityName := extractEntityName(variable.EntityID)
	if err := m.client.SetInputText(entityName, string(jsonBytes)); err != nil {
		// Rollback cache on error
		m.cacheMu.Lock()
		m.cache[key] = oldValue
		m.cacheMu.Unlock()
		return fmt.Errorf("failed to set HA value: %w", err)
	}

	// Notify subscribers after successful HA sync
	// The HA callback will skip notification since cache already has the new value
	m.notifySubscribers(key, oldValue, value)

	return nil
}

// CompareAndSwapBool atomically compares and swaps a boolean value
func (m *Manager) CompareAndSwapBool(key string, old, new bool) (bool, error) {
	variable, ok := m.variables[key]
	if !ok {
		return false, fmt.Errorf("variable %s not found", key)
	}

	if variable.Type != TypeBool {
		return false, fmt.Errorf("variable %s is not a boolean", key)
	}
	if err := m.ensureWritable(variable); err != nil {
		return false, err
	}

	m.cacheMu.Lock()

	currentValue, ok := m.cache[key]
	if !ok {
		currentValue = variable.Default
	}

	currentBool, ok := currentValue.(bool)
	if !ok {
		m.cacheMu.Unlock()
		return false, fmt.Errorf("cached value for %s is not a boolean", key)
	}

	if currentBool != old {
		m.cacheMu.Unlock()
		return false, nil
	}

	// Update cache (still holding lock)
	m.cache[key] = new

	// Release lock before calling HA client to avoid deadlock
	m.cacheMu.Unlock()

	// Skip HA sync if no client (e.g., in tests)
	if m.client == nil {
		return true, nil
	}

	// Sync to HA
	entityName := extractEntityName(variable.EntityID)
	if err := m.client.SetInputBoolean(entityName, new); err != nil {
		// Rollback on error
		m.cacheMu.Lock()
		m.cache[key] = old
		m.cacheMu.Unlock()
		return false, fmt.Errorf("failed to set HA value: %w", err)
	}

	return true, nil
}

// Subscribe subscribes to state changes for a variable
func (m *Manager) Subscribe(key string, handler StateChangeHandler) (Subscription, error) {
	if _, ok := m.variables[key]; !ok {
		return nil, fmt.Errorf("variable %s not found", key)
	}

	variable := m.variables[key]
	if !variable.LocalOnly {
		if err := m.ensureHASubscription(variable); err != nil {
			return nil, err
		}
	}

	subID := atomic.AddUint64(&m.nextSubID, 1)
	m.subsMu.Lock()
	if _, ok := m.subscribers[key]; !ok {
		m.subscribers[key] = make(map[uint64]StateChangeHandler)
	}
	m.subscribers[key][subID] = handler
	m.subsMu.Unlock()

	return &subscription{
		key:     key,
		id:      subID,
		manager: m,
	}, nil
}

// unsubscribe removes a specific subscription
func (m *Manager) unsubscribe(key string, id uint64) {
	m.subsMu.Lock()
	handlers, ok := m.subscribers[key]
	if !ok {
		m.subsMu.Unlock()
		return
	}
	delete(handlers, id)
	if len(handlers) == 0 {
		delete(m.subscribers, key)
	}
	empty := len(handlers) == 0
	m.subsMu.Unlock()

	if empty {
		m.teardownHASubscription(key)
	}
}

func (m *Manager) ensureHASubscription(variable StateVariable) error {
	if variable.EntityID == "" {
		return nil
	}
	m.haSubsMu.Lock()
	_, ok := m.haSubs[variable.EntityID]
	m.haSubsMu.Unlock()
	if ok {
		return nil
	}
	return m.subscribeToEntity(variable.EntityID, variable.Key)
}

func (m *Manager) teardownHASubscription(key string) {
	variable, ok := m.variables[key]
	if !ok || variable.LocalOnly || variable.EntityID == "" {
		return
	}

	m.haSubsMu.Lock()
	sub, ok := m.haSubs[variable.EntityID]
	if ok {
		delete(m.haSubs, variable.EntityID)
	}
	m.haSubsMu.Unlock()

	if !ok {
		return
	}

	if err := sub.Unsubscribe(); err != nil {
		m.logger.Warn("Failed to unsubscribe from HA entity", zap.String("entity_id", variable.EntityID), zap.Error(err))
	}
}

// GetAllValues returns all cached values
func (m *Manager) GetAllValues() map[string]interface{} {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()

	values := make(map[string]interface{})
	for k, v := range m.cache {
		values[k] = v
	}
	return values
}

// extractEntityName extracts the entity name from full entity ID
// e.g., "input_boolean.nick_home" -> "nick_home"
func extractEntityName(entityID string) string {
	for i := len(entityID) - 1; i >= 0; i-- {
		if entityID[i] == '.' {
			return entityID[i+1:]
		}
	}
	return entityID
}

func marshalJSONValue(value interface{}) ([]byte, error) {
	switch v := value.(type) {
	case nil:
		return []byte("null"), nil
	case json.RawMessage:
		return v, nil
	case []byte:
		if !json.Valid(v) {
			return nil, fmt.Errorf("invalid JSON bytes")
		}
		return v, nil
	case string:
		if json.Valid([]byte(v)) {
			return []byte(v), nil
		}
		return json.Marshal(v)
	default:
		return json.Marshal(v)
	}
}
