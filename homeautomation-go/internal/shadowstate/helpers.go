package shadowstate

import (
	"fmt"

	"homeautomation/internal/ha"
)

// StateManager defines the interface needed for state value retrieval.
// This avoids a circular import with the state package.
type StateManager interface {
	GetBool(key string) (bool, error)
	GetString(key string) (string, error)
	GetNumber(key string) (float64, error)
}

// InputCaptureHelper provides automatic input capture for plugins based on
// their registered subscriptions. This reduces boilerplate and ensures
// shadow state inputs are always in sync with actual subscriptions.
type InputCaptureHelper struct {
	registry     *SubscriptionRegistry
	haClient     ha.HAClient
	stateManager StateManager
}

// NewInputCaptureHelper creates a new input capture helper
func NewInputCaptureHelper(registry *SubscriptionRegistry, haClient ha.HAClient, stateManager StateManager) *InputCaptureHelper {
	return &InputCaptureHelper{
		registry:     registry,
		haClient:     haClient,
		stateManager: stateManager,
	}
}

// CaptureInputs automatically captures all registered subscriptions for a plugin.
// Returns a map of input names to their current values, suitable for passing
// to a shadow state tracker's UpdateCurrentInputs method.
func (h *InputCaptureHelper) CaptureInputs(pluginName string) map[string]interface{} {
	inputs := make(map[string]interface{})

	// Capture all HA entity subscriptions
	for _, entityID := range h.registry.GetHASubscriptions(pluginName) {
		if state, err := h.haClient.GetState(entityID); err == nil && state != nil {
			inputs[entityID] = state.State
		}
	}

	// Capture all state variable subscriptions
	for _, stateKey := range h.registry.GetStateSubscriptions(pluginName) {
		if val, err := h.getStateValue(stateKey); err == nil {
			inputs[stateKey] = val
		}
	}

	return inputs
}

// CaptureInputsWithAdditional captures all registered subscriptions plus
// any additional custom inputs. This is useful when plugins need to track
// inputs that aren't registered subscriptions (e.g., trigger information).
func (h *InputCaptureHelper) CaptureInputsWithAdditional(pluginName string, additional map[string]interface{}) map[string]interface{} {
	inputs := h.CaptureInputs(pluginName)

	// Merge additional inputs (they override auto-captured ones if there's a conflict)
	for k, v := range additional {
		inputs[k] = v
	}

	return inputs
}

// getStateValue retrieves a state variable value with automatic type inference.
// Tries bool -> string -> number in that order.
func (h *InputCaptureHelper) getStateValue(key string) (interface{}, error) {
	// Try bool first (most common in this codebase)
	if val, err := h.stateManager.GetBool(key); err == nil {
		return val, nil
	}

	// Try string
	if val, err := h.stateManager.GetString(key); err == nil {
		return val, nil
	}

	// Try number
	if val, err := h.stateManager.GetNumber(key); err == nil {
		return val, nil
	}

	// If all fail, return an error (the variable might not exist)
	return nil, fmt.Errorf("unable to get value for state variable %s", key)
}
