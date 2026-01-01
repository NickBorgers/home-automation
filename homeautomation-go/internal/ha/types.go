package ha

import (
	"encoding/json"
	"time"
)

// Message represents a base WebSocket message to/from Home Assistant
type Message struct {
	ID      int             `json:"id,omitempty"`
	Type    string          `json:"type"`
	Success *bool           `json:"success,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	Event   *Event          `json:"event,omitempty"`
}

// Error represents an error response from Home Assistant
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AuthMessage represents authentication request
type AuthMessage struct {
	Type        string `json:"type"`
	AccessToken string `json:"access_token,omitempty"`
}

// AuthOkMessage represents successful authentication
type AuthOkMessage struct {
	Type      string `json:"type"`
	HAVersion string `json:"ha_version"`
}

// Event represents an event message from Home Assistant
type Event struct {
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
	Origin    string          `json:"origin"`
	TimeFired time.Time       `json:"time_fired"`
}

// StateChangedEvent represents a state_changed event
type StateChangedEvent struct {
	EntityID string `json:"entity_id"`
	NewState *State `json:"new_state"`
	OldState *State `json:"old_state"`
}

// State represents an entity state
type State struct {
	EntityID    string                 `json:"entity_id"`
	State       string                 `json:"state"`
	Attributes  map[string]interface{} `json:"attributes"`
	LastChanged time.Time              `json:"last_changed"`
	LastUpdated time.Time              `json:"last_updated"`
	Context     *Context               `json:"context,omitempty"`
}

// Context represents the context of a state change
type Context struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	UserID   string `json:"user_id,omitempty"`
}

// CallServiceRequest represents a call_service request
type CallServiceRequest struct {
	ID          int                    `json:"id"`
	Type        string                 `json:"type"`
	Domain      string                 `json:"domain"`
	Service     string                 `json:"service"`
	ServiceData map[string]interface{} `json:"service_data,omitempty"`
	Target      *ServiceTarget         `json:"target,omitempty"`
}

// ServiceTarget represents service call target
type ServiceTarget struct {
	EntityID []string `json:"entity_id,omitempty"`
	LabelID  []string `json:"label_id,omitempty"`
	AreaID   []string `json:"area_id,omitempty"`
}

// GetStatesRequest represents a get_states request
type GetStatesRequest struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
}

// SubscribeEventsRequest represents a subscribe_events request
type SubscribeEventsRequest struct {
	ID        int    `json:"id"`
	Type      string `json:"type"`
	EventType string `json:"event_type,omitempty"`
}

// PingRequest represents an application-level ping to Home Assistant.
// Home Assistant expects JSON pings (not WebSocket control frame pings)
// to keep the connection alive. See: https://developers.home-assistant.io/docs/api/websocket/
type PingRequest struct {
	ID   int    `json:"id"`
	Type string `json:"type"` // Always "ping"
}

// StateChangeHandler is called when a state change event is received
type StateChangeHandler func(entityID string, oldState, newState *State)

// Subscription represents an active event subscription
type Subscription interface {
	Unsubscribe() error
}

// subscription implements Subscription interface
type subscription struct {
	entityID string
	subID    int
	client   *Client
}

func (s *subscription) Unsubscribe() error {
	return s.client.unsubscribe(s.entityID, s.subID)
}
