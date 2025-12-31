// Package ha provides the public interface definitions for Home Assistant
// client integration. These interfaces can be imported by external packages
// (including private plugin implementations).
//
// The actual implementation is in internal/ha, which is wrapped by these
// public interfaces for external consumption.
package ha

import (
	"time"
)

// State represents an entity state from Home Assistant.
type State struct {
	EntityID    string                 `json:"entity_id"`
	State       string                 `json:"state"`
	Attributes  map[string]interface{} `json:"attributes"`
	LastChanged time.Time              `json:"last_changed"`
	LastUpdated time.Time              `json:"last_updated"`
}

// StateChangeHandler is called when a state change event is received.
type StateChangeHandler func(entityID string, oldState, newState *State)

// Subscription represents an active event subscription.
type Subscription interface {
	Unsubscribe() error
}

// ServiceTarget represents the target for a service call.
type ServiceTarget struct {
	EntityID []string `json:"entity_id,omitempty"`
	LabelID  []string `json:"label_id,omitempty"`
	AreaID   []string `json:"area_id,omitempty"`
}

// Client defines the interface for Home Assistant WebSocket client.
// This interface matches internal/ha.HAClient and can be used by external packages.
type Client interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	GetState(entityID string) (*State, error)
	GetAllStates() ([]*State, error)
	CallService(domain, service string, data map[string]interface{}) error
	CallServiceWithTarget(domain, service string, target *ServiceTarget, data map[string]interface{}) error
	SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error)
	SetInputBoolean(name string, value bool) error
	SetInputNumber(name string, value float64) error
	SetInputText(name string, value string) error
}
