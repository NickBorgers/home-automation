// Package ha provides the public interface definitions for Home Assistant
// client integration. These interfaces can be imported by external packages
// (including private plugin implementations).
//
// The actual implementation is in internal/ha, which is wrapped by these
// public interfaces for external consumption.
package ha

import (
	"context"
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
	CallService(ctx context.Context, domain, service string, data map[string]interface{}) error
	CallServiceWithTarget(ctx context.Context, domain, service string, target *ServiceTarget, data map[string]interface{}) error
	SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error)
	SetInputBoolean(name string, value bool) error
	SetInputNumber(name string, value float64) error
	SetInputText(name string, value string) error
}

// MQTTMessage is one MQTT message that Home Assistant forwarded from the broker.
type MQTTMessage struct {
	Topic   string
	Payload string
	QoS     int
	Retain  bool

	// Resubscribed is true on a marker message, not a broker message. The client sends it to every
	// handler after it subscribes again on a new connection. Messages sent while the connection was down
	// are lost, so a handler that keeps state across messages should treat that state as stale.
	Resubscribed bool
}

// MQTTHandler is called once per MQTT message on a subscribed topic filter.
type MQTTHandler func(msg MQTTMessage)

// MQTTSubscriber reads MQTT topics through Home Assistant's mqtt/subscribe command. The Client from
// WrapClient implements it. It is a separate interface, so existing Client implementations, such as
// test mocks, keep compiling. Check for it with a type assertion:
//
//	sub, ok := client.(ha.MQTTSubscriber)
type MQTTSubscriber interface {
	// SubscribeMQTT registers a handler for a topic filter, such as "sensors/+/state". The client
	// subscribes again after every reconnect, then sends each handler a message with Resubscribed set.
	SubscribeMQTT(topic string, handler MQTTHandler) (Subscription, error)
}
