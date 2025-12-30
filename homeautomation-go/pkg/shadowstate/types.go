// Package shadowstate provides public interface types for the shadow state system.
// External plugins can import this package to implement the ShadowStateProvider
// interface, enabling full integration with the shadow state observability system.
//
// Internal implementation details (trackers, registries) remain in the internal
// package. This package only exposes the interface types needed by external plugins.
package shadowstate

import "time"

// PluginShadowState is the interface that all plugin shadow states must implement.
// Shadow state captures the inputs that led to each action, enabling debugging
// and verification of plugin behavior.
//
// External plugins should implement this interface to participate in the
// shadow state observability system. The shadow state will be exposed via
// the /api/shadow endpoint.
type PluginShadowState interface {
	// GetCurrentInputs returns the current values of all inputs this plugin monitors.
	// These are the "live" values that would be used if an action were taken now.
	GetCurrentInputs() map[string]interface{}

	// GetLastActionInputs returns the input values that were present when the
	// plugin last took an action. This enables comparing what changed between
	// the last action and the current state.
	GetLastActionInputs() map[string]interface{}

	// GetOutputs returns the plugin's output state. The concrete type varies
	// by plugin and should be a struct that marshals to JSON.
	GetOutputs() interface{}

	// GetMetadata returns metadata about this shadow state, including
	// the plugin name and when it was last updated.
	GetMetadata() StateMetadata
}

// StateMetadata contains metadata about the shadow state.
// This is included in every plugin's shadow state for observability.
type StateMetadata struct {
	// LastUpdated is when this shadow state was last modified.
	LastUpdated time.Time `json:"lastUpdated"`

	// PluginName identifies which plugin this shadow state belongs to.
	PluginName string `json:"pluginName"`
}

// InputSnapshot represents a snapshot of input values at a specific time.
// This can be used by plugins to track input history or for debugging.
type InputSnapshot struct {
	// Timestamp is when this snapshot was taken.
	Timestamp time.Time `json:"timestamp"`

	// Values contains the input values at the time of the snapshot.
	Values map[string]interface{} `json:"values"`
}
