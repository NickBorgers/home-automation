package shadowstate

import (
	"fmt"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// ShadowInputUpdater is the interface that shadow trackers must implement
// to receive automatic input updates from SubscriptionHelper.
type ShadowInputUpdater interface {
	UpdateCurrentInputs(inputs map[string]interface{})
}

// SubscriptionHelper wraps HA and state subscriptions to automatically
// capture shadow state inputs before invoking handlers. This eliminates
// the need for every handler to manually call updateShadowInputs().
type SubscriptionHelper struct {
	haClient      ha.HAClient
	stateManager  *state.Manager
	registry      *SubscriptionRegistry
	inputHelper   *InputCaptureHelper
	shadowTracker ShadowInputUpdater
	pluginName    string
	logger        *zap.Logger

	// Track subscriptions for cleanup
	haSubscriptions    []ha.Subscription
	stateSubscriptions []state.Subscription

	// Track subscribed keys for fallback input capture (when registry is nil)
	subscribedStateKeys  []string
	subscribedHAEntities []string
}

// NewSubscriptionHelper creates a new subscription helper for a plugin.
// The shadowTracker receives automatic input updates before each handler runs.
func NewSubscriptionHelper(
	haClient ha.HAClient,
	stateManager *state.Manager,
	registry *SubscriptionRegistry,
	shadowTracker ShadowInputUpdater,
	pluginName string,
	logger *zap.Logger,
) *SubscriptionHelper {
	h := &SubscriptionHelper{
		haClient:             haClient,
		stateManager:         stateManager,
		registry:             registry,
		shadowTracker:        shadowTracker,
		pluginName:           pluginName,
		logger:               logger,
		haSubscriptions:      make([]ha.Subscription, 0),
		stateSubscriptions:   make([]state.Subscription, 0),
		subscribedStateKeys:  make([]string, 0),
		subscribedHAEntities: make([]string, 0),
	}

	// Create input helper for automatic capture
	if registry != nil {
		h.inputHelper = NewInputCaptureHelper(registry, haClient, stateManager)
	}

	return h
}

// captureInputs captures all registered inputs and updates the shadow tracker.
// This is called automatically before every handler.
func (h *SubscriptionHelper) captureInputs() {
	if h.shadowTracker == nil {
		return
	}

	// Use inputHelper if available (normal operation with registry)
	if h.inputHelper != nil {
		inputs := h.inputHelper.CaptureInputs(h.pluginName)
		h.shadowTracker.UpdateCurrentInputs(inputs)
		return
	}

	// Fallback: capture inputs directly from tracked subscriptions (for tests without registry)
	inputs := make(map[string]interface{})

	// Capture state variable values
	if h.stateManager != nil {
		allValues := h.stateManager.GetAllValues()
		for _, key := range h.subscribedStateKeys {
			if val, ok := allValues[key]; ok {
				inputs[key] = val
			}
		}
	}

	// Capture HA entity states
	if h.haClient != nil {
		for _, entityID := range h.subscribedHAEntities {
			if state, err := h.haClient.GetState(entityID); err == nil && state != nil {
				inputs[entityID] = state.State
			}
		}
	}

	if len(inputs) > 0 {
		h.shadowTracker.UpdateCurrentInputs(inputs)
	}
}

// CaptureInitialInputs captures all registered inputs at startup.
// Call this after all subscriptions are registered to populate shadow state
// before any events fire. This prevents empty inputs.current in shadow state.
func (h *SubscriptionHelper) CaptureInitialInputs() {
	h.captureInputs()
}

// SubscribeToSensor subscribes to a Home Assistant sensor entity and parses its
// state as a float64. Shadow state inputs are automatically captured before
// the handler is called.
func (h *SubscriptionHelper) SubscribeToSensor(entityID string, handler func(value float64)) error {
	// Register the subscription for input capture
	if h.registry != nil {
		h.registry.RegisterHASubscription(h.pluginName, entityID)
	}

	// Track the entity for fallback input capture
	h.subscribedHAEntities = append(h.subscribedHAEntities, entityID)

	sub, err := h.haClient.SubscribeStateChanges(entityID, func(entity string, oldState, newState *ha.State) {
		if newState == nil {
			return
		}

		// Capture shadow state inputs BEFORE calling the handler
		h.captureInputs()

		// Parse the state string as float64
		var val float64
		_, parseErr := fmt.Sscanf(newState.State, "%f", &val)
		if parseErr != nil {
			h.logger.Warn("Failed to parse sensor value as number",
				zap.String("entity_id", entityID),
				zap.String("value", newState.State))
			return
		}

		handler(val)
	})

	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", entityID, err)
	}

	h.haSubscriptions = append(h.haSubscriptions, sub)
	return nil
}

// SubscribeToEntity subscribes to a Home Assistant entity with full state access.
// Shadow state inputs are automatically captured before the handler is called.
func (h *SubscriptionHelper) SubscribeToEntity(entityID string, handler func(entityID string, oldState, newState *ha.State)) error {
	// Register the subscription for input capture
	if h.registry != nil {
		h.registry.RegisterHASubscription(h.pluginName, entityID)
	}

	// Track the entity for fallback input capture
	h.subscribedHAEntities = append(h.subscribedHAEntities, entityID)

	sub, err := h.haClient.SubscribeStateChanges(entityID, func(entity string, oldState, newState *ha.State) {
		// Capture shadow state inputs BEFORE calling the handler
		h.captureInputs()

		handler(entity, oldState, newState)
	})

	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", entityID, err)
	}

	h.haSubscriptions = append(h.haSubscriptions, sub)
	return nil
}

// SubscribeToState subscribes to a state variable change.
// Shadow state inputs are automatically captured before the handler is called.
func (h *SubscriptionHelper) SubscribeToState(key string, handler func(key string, oldValue, newValue interface{})) error {
	// Register the subscription for input capture
	if h.registry != nil {
		h.registry.RegisterStateSubscription(h.pluginName, key)
	}

	// Track the key for fallback input capture
	h.subscribedStateKeys = append(h.subscribedStateKeys, key)

	sub, err := h.stateManager.Subscribe(key, func(k string, oldValue, newValue interface{}) {
		// Capture shadow state inputs BEFORE calling the handler
		h.captureInputs()

		handler(k, oldValue, newValue)
	})

	if err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", key, err)
	}

	h.stateSubscriptions = append(h.stateSubscriptions, sub)
	return nil
}

// GetHASubscriptions returns all HA subscriptions (for manual cleanup if needed)
func (h *SubscriptionHelper) GetHASubscriptions() []ha.Subscription {
	return h.haSubscriptions
}

// GetStateSubscriptions returns all state subscriptions (for manual cleanup if needed)
func (h *SubscriptionHelper) GetStateSubscriptions() []state.Subscription {
	return h.stateSubscriptions
}

// UnsubscribeAll cleans up all subscriptions
func (h *SubscriptionHelper) UnsubscribeAll() {
	for _, sub := range h.haSubscriptions {
		sub.Unsubscribe()
	}
	h.haSubscriptions = nil

	for _, sub := range h.stateSubscriptions {
		sub.Unsubscribe()
	}
	h.stateSubscriptions = nil
}
