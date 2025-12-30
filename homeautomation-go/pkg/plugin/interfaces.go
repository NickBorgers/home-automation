// Package plugin provides the plugin system interfaces and registry for the
// home automation system. Plugins can register themselves with the global
// registry using init() functions, allowing for compile-time plugin selection
// and override mechanisms for private implementations.
package plugin

import "homeautomation/pkg/shadowstate"

// Plugin is the core interface that all plugins must implement.
// Plugins are responsible for automation logic in a specific domain
// (e.g., security, lighting, music).
type Plugin interface {
	// Name returns the unique identifier for this plugin.
	// This name is used for registration and logging.
	Name() string

	// Start begins the plugin's operation.
	// - Sets up subscriptions to state changes
	// - Starts any background goroutines
	// - Returns error if initialization fails
	Start() error

	// Stop gracefully shuts down the plugin.
	// - Unsubscribes from all state changes
	// - Stops any background goroutines
	// - Releases resources
	Stop()
}

// Resettable is an optional interface for plugins that support the system-wide
// reset mechanism. When the reset state variable is triggered, the Reset
// Coordinator calls Reset() on all plugins implementing this interface.
type Resettable interface {
	// Reset re-evaluates all conditions and recalculates state.
	// - Clears any rate limiters or timers
	// - Re-applies current state conditions
	// - Returns error if reset fails
	Reset() error
}

// ShadowStateProvider is an optional interface for plugins that track their
// decision-making for observability. Shadow state captures the inputs that
// led to each action, enabling debugging and verification.
type ShadowStateProvider interface {
	// GetShadowState returns the current shadow state for the plugin.
	// The returned state captures recent decisions and their triggering inputs.
	GetShadowState() shadowstate.PluginShadowState
}

// Factory is a function that creates a new plugin instance given a context.
// Factories are registered with the global registry and called during
// application startup to instantiate plugins.
type Factory func(ctx *Context) (Plugin, error)
