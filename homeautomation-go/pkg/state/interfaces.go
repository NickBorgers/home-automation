// Package state provides the public interface definitions for state management.
// These interfaces can be imported by external packages (including private
// plugin implementations).
//
// The actual implementation is in internal/state, which is wrapped by these
// public interfaces for external consumption.
package state

// StateChangeHandler is called when a state variable changes.
type StateChangeHandler func(key string, oldValue, newValue interface{})

// Subscription represents an active state change subscription.
type Subscription interface {
	Unsubscribe()
}

// Manager defines the interface for state management.
// This interface matches the public methods of internal/state.Manager.
type Manager interface {
	// Sync methods
	SyncFromHA() error

	// Boolean operations
	GetBool(key string) (bool, error)
	SetBool(key string, value bool) error
	CompareAndSwapBool(key string, old, new bool) (bool, error)

	// String operations
	GetString(key string) (string, error)
	SetString(key string, value string) error

	// Number operations
	GetNumber(key string) (float64, error)
	SetNumber(key string, value float64) error

	// JSON operations
	GetJSON(key string, target interface{}) error
	SetJSON(key string, value interface{}) error

	// Subscription
	Subscribe(key string, handler StateChangeHandler) (Subscription, error)

	// Query
	GetAllValues() map[string]interface{}

	// WaitForProcessing blocks until all in-flight event handler goroutines complete.
	WaitForProcessing()
}
