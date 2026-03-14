// Package testutil provides testing utilities for home automation plugins.
// This package contains a mock Home Assistant WebSocket server and helpers
// for writing integration tests.
package testutil

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// connWrapper wraps a WebSocket connection with its write mutex
type connWrapper struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

// MockHAServer simulates a Home Assistant WebSocket server
type MockHAServer struct {
	server       *http.Server
	listener     net.Listener // Listener for dynamic port allocation
	addr         string
	actualAddr   string // The actual address after binding (useful when port is 0)
	states       map[string]*EntityState
	statesMu     sync.RWMutex
	connections  []*connWrapper
	connsMu      sync.Mutex
	eventDelay   time.Duration // Simulates network latency
	token        string
	serviceCalls []ServiceCall // Track all service calls for verification
	callsMu      sync.Mutex    // Protects serviceCalls
}

// EntityState represents a Home Assistant entity state
type EntityState struct {
	EntityID    string                 `json:"entity_id"`
	State       string                 `json:"state"`
	Attributes  map[string]interface{} `json:"attributes"`
	LastChanged time.Time              `json:"last_changed"`
	LastUpdated time.Time              `json:"last_updated"`
}

// Message represents a WebSocket message
type Message struct {
	ID      int             `json:"id,omitempty"`
	Type    string          `json:"type"`
	Success *bool           `json:"success,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Event   *Event          `json:"event,omitempty"`
}

// Event represents a Home Assistant event
type Event struct {
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
	Origin    string          `json:"origin"`
	TimeFired time.Time       `json:"time_fired"`
}

// StateChangedEvent represents a state_changed event
type StateChangedEvent struct {
	EntityID string       `json:"entity_id"`
	NewState *EntityState `json:"new_state"`
	OldState *EntityState `json:"old_state"`
}

// AuthMessage represents authentication request
type AuthMessage struct {
	Type        string `json:"type"`
	AccessToken string `json:"access_token,omitempty"`
}

// CallServiceRequest represents a service call
type CallServiceRequest struct {
	ID          int                    `json:"id"`
	Type        string                 `json:"type"`
	Domain      string                 `json:"domain"`
	Service     string                 `json:"service"`
	ServiceData map[string]interface{} `json:"service_data,omitempty"`
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

// NewMockHAServer creates a new mock HA server
func NewMockHAServer(addr, token string) *MockHAServer {
	return &MockHAServer{
		addr:         addr,
		states:       make(map[string]*EntityState),
		connections:  make([]*connWrapper, 0),
		eventDelay:   10 * time.Millisecond, // Simulate network latency
		token:        token,
		serviceCalls: make([]ServiceCall, 0),
	}
}

// Addr returns the actual address the server is listening on.
// This is useful when the server was created with port 0 for dynamic allocation.
func (s *MockHAServer) Addr() string {
	if s.actualAddr != "" {
		return s.actualAddr
	}
	return s.addr
}

// SetEventDelay sets the delay for broadcasting events
func (s *MockHAServer) SetEventDelay(delay time.Duration) {
	s.eventDelay = delay
}

// Start starts the mock server
func (s *MockHAServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/websocket", s.handleWebSocket)

	// Use net.Listen for proper dynamic port allocation
	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}
	s.listener = listener
	s.actualAddr = listener.Addr().String()

	s.server = &http.Server{
		Handler: mux,
	}

	go func() {
		if err := s.server.Serve(listener); err != http.ErrServerClosed {
			log.Printf("Mock HA server error: %v", err)
		}
	}()

	// Wait for server to start (reduced since we already have a listener)
	time.Sleep(10 * time.Millisecond)
	return nil
}

// Stop stops the mock server
func (s *MockHAServer) Stop() error {
	s.connsMu.Lock()
	for _, wrapper := range s.connections {
		wrapper.conn.Close()
	}
	s.connections = nil
	s.connsMu.Unlock()

	if s.server != nil {
		return s.server.Close()
	}
	return nil
}

// SetState sets a state and broadcasts change event
func (s *MockHAServer) SetState(entityID, state string, attributes map[string]interface{}) {
	s.statesMu.Lock()
	oldState := s.states[entityID]

	now := time.Now()
	newState := &EntityState{
		EntityID:    entityID,
		State:       state,
		Attributes:  attributes,
		LastChanged: now,
		LastUpdated: now,
	}

	s.states[entityID] = newState
	s.statesMu.Unlock()

	// Broadcast state_changed event with delay
	if s.eventDelay > 0 {
		time.Sleep(s.eventDelay)
	}
	s.broadcastStateChange(entityID, oldState, newState)
}

// GetState retrieves a state
func (s *MockHAServer) GetState(entityID string) *EntityState {
	s.statesMu.RLock()
	defer s.statesMu.RUnlock()
	return s.states[entityID]
}

// InitializeStates sets up initial state for testing
func (s *MockHAServer) InitializeStates() {
	// Boolean states
	boolEntities := []string{
		"nick_home", "caroline_home", "tori_here",
		"any_owner_home", "anyone_home", "anyone_home_and_awake",
		"master_asleep", "guest_asleep", "anyone_asleep", "everyone_asleep",
		"guest_bedroom_door_open", "have_guests",
		"apple_tv_playing", "tv_playing", "tv_on",
		"fade_out_in_progress", "free_energy_available", "grid_available",
		"expecting_someone", "reset",
		"wake_sequence_active",          // Added for wake sequence user story tests
		"front_of_house_person_present", // Person detection at front of house
	}

	for _, name := range boolEntities {
		s.SetState(fmt.Sprintf("input_boolean.%s", name), "off", map[string]interface{}{
			"friendly_name": name,
		})
	}

	// Number states
	s.SetState("input_number.alarm_time", "0", map[string]interface{}{})
	s.SetState("input_number.remaining_solar_generation", "0", map[string]interface{}{})
	s.SetState("input_number.this_hour_solar_generation", "0", map[string]interface{}{})

	// Text states
	s.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	s.SetState("input_text.sun_event", "sunrise", map[string]interface{}{})
	s.SetState("input_text.music_playback_type", "", map[string]interface{}{})
	s.SetState("input_text.currently_playing_music_uri", "", map[string]interface{}{})
	s.SetState("input_text.music_playlist_rotation", "{}", map[string]interface{}{})
	s.SetState("input_text.battery_energy_level", "green", map[string]interface{}{})
	s.SetState("input_text.current_energy_level", "green", map[string]interface{}{})
	s.SetState("input_text.solar_production_energy_level", "white", map[string]interface{}{})
}

// handleWebSocket handles WebSocket connections
func (s *MockHAServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}

	wrapper := &connWrapper{conn: conn}

	s.connsMu.Lock()
	s.connections = append(s.connections, wrapper)
	s.connsMu.Unlock()

	defer func() {
		s.connsMu.Lock()
		for i, w := range s.connections {
			if w.conn == conn {
				s.connections = append(s.connections[:i], s.connections[i+1:]...)
				break
			}
		}
		s.connsMu.Unlock()
		conn.Close()
	}()

	// Send auth_required
	wrapper.writeMu.Lock()
	conn.WriteJSON(Message{Type: "auth_required"})
	wrapper.writeMu.Unlock()

	// Receive auth
	var authMsg AuthMessage
	if err := conn.ReadJSON(&authMsg); err != nil {
		log.Printf("Failed to read auth: %v", err)
		return
	}

	// Validate token
	if authMsg.AccessToken != s.token {
		wrapper.writeMu.Lock()
		conn.WriteJSON(Message{Type: "auth_invalid"})
		wrapper.writeMu.Unlock()
		return
	}

	// Send auth_ok
	wrapper.writeMu.Lock()
	conn.WriteJSON(Message{Type: "auth_ok"})
	wrapper.writeMu.Unlock()

	// Handle messages
	for {
		var msg json.RawMessage
		if err := conn.ReadJSON(&msg); err != nil {
			log.Printf("Connection closed: %v", err)
			return
		}

		// Parse message type
		var baseMsg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(msg, &baseMsg); err != nil {
			continue
		}

		switch baseMsg.Type {
		case "subscribe_events":
			s.handleSubscribeEvents(wrapper, msg)
		case "get_states":
			s.handleGetStates(wrapper, msg)
		case "call_service":
			s.handleCallService(wrapper, msg)
		case "ping":
			s.handlePing(wrapper, msg)
		}
	}
}

// handlePing responds to JSON ping messages with a pong to keep the connection alive.
// The real HA client sends {"id": N, "type": "ping"} and expects {"id": N, "type": "pong"}.
func (s *MockHAServer) handlePing(wrapper *connWrapper, msg json.RawMessage) {
	var req struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(msg, &req); err != nil {
		return
	}
	wrapper.writeMu.Lock()
	wrapper.conn.WriteJSON(Message{
		ID:   req.ID,
		Type: "pong",
	})
	wrapper.writeMu.Unlock()
}

// handleSubscribeEvents handles event subscriptions
func (s *MockHAServer) handleSubscribeEvents(wrapper *connWrapper, msg json.RawMessage) {
	var req SubscribeEventsRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return
	}

	success := true
	wrapper.writeMu.Lock()
	wrapper.conn.WriteJSON(Message{
		ID:      req.ID,
		Type:    "result",
		Success: &success,
	})
	wrapper.writeMu.Unlock()
}

// handleGetStates handles get_states requests
func (s *MockHAServer) handleGetStates(wrapper *connWrapper, msg json.RawMessage) {
	var req GetStatesRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return
	}

	s.statesMu.RLock()
	states := make([]*EntityState, 0, len(s.states))
	for _, state := range s.states {
		states = append(states, state)
	}
	s.statesMu.RUnlock()

	statesJSON, _ := json.Marshal(states)
	success := true
	wrapper.writeMu.Lock()
	wrapper.conn.WriteJSON(Message{
		ID:      req.ID,
		Type:    "result",
		Success: &success,
		Result:  statesJSON,
	})
	wrapper.writeMu.Unlock()
}

// handleCallService handles service calls
func (s *MockHAServer) handleCallService(wrapper *connWrapper, msg json.RawMessage) {
	var req CallServiceRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return
	}

	// Track the service call for test verification
	s.callsMu.Lock()
	s.serviceCalls = append(s.serviceCalls, ServiceCall{
		Timestamp:   time.Now(),
		Domain:      req.Domain,
		Service:     req.Service,
		ServiceData: req.ServiceData,
	})
	s.callsMu.Unlock()

	// Update state based on service call
	entityID, _ := req.ServiceData["entity_id"].(string)

	switch req.Domain {
	case "input_boolean":
		newState := "off"
		if req.Service == "turn_on" {
			newState = "on"
		}

		s.statesMu.RLock()
		oldState := s.states[entityID]
		s.statesMu.RUnlock()

		if oldState != nil {
			s.SetState(entityID, newState, oldState.Attributes)
		}

	case "input_number":
		if value, ok := req.ServiceData["value"].(float64); ok {
			s.statesMu.RLock()
			oldState := s.states[entityID]
			s.statesMu.RUnlock()

			newState := fmt.Sprintf("%.2f", value)
			if oldState != nil {
				s.SetState(entityID, newState, oldState.Attributes)
			}
		}

	case "input_text":
		if value, ok := req.ServiceData["value"].(string); ok {
			s.statesMu.RLock()
			oldState := s.states[entityID]
			s.statesMu.RUnlock()

			if oldState != nil {
				s.SetState(entityID, value, oldState.Attributes)
			}
		}

	case "scene":
		// Scene activations are fire-and-forget, just acknowledge
		// Don't need to track state changes for scenes

	case "light":
		// Light service calls (turn_on, turn_off, etc.)
		// For testing, we just acknowledge them without state changes

	case "notify", "tts":
		// Notification and TTS services - just acknowledge

	case "cover":
		// Cover service calls (open_cover, close_cover)
		// For testing, we can update state if needed
		if entityID != "" {
			s.statesMu.RLock()
			oldState := s.states[entityID]
			s.statesMu.RUnlock()

			if oldState != nil {
				newState := "closed"
				if req.Service == "open_cover" {
					newState = "open"
				}
				s.SetState(entityID, newState, oldState.Attributes)
			}
		}

	default:
		// Unknown service domain - still acknowledge to prevent timeouts
	}

	success := true
	wrapper.writeMu.Lock()
	wrapper.conn.WriteJSON(Message{
		ID:      req.ID,
		Type:    "result",
		Success: &success,
	})
	wrapper.writeMu.Unlock()
}

// broadcastStateChange broadcasts a state change event to all connections
func (s *MockHAServer) broadcastStateChange(entityID string, oldState, newState *EntityState) {
	eventData := StateChangedEvent{
		EntityID: entityID,
		NewState: newState,
		OldState: oldState,
	}

	eventDataJSON, _ := json.Marshal(eventData)

	event := &Event{
		EventType: "state_changed",
		Data:      eventDataJSON,
		Origin:    "LOCAL",
		TimeFired: time.Now(),
	}

	msg := Message{
		Type:  "event",
		Event: event,
	}

	s.connsMu.Lock()
	wrappers := make([]*connWrapper, len(s.connections))
	copy(wrappers, s.connections)
	s.connsMu.Unlock()

	for _, wrapper := range wrappers {
		// Write to each connection with per-connection mutex
		wrapper.writeMu.Lock()
		wrapper.conn.WriteJSON(msg)
		wrapper.writeMu.Unlock()
	}
}

// GetServiceCalls returns all service calls since last clear
func (s *MockHAServer) GetServiceCalls() []ServiceCall {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	calls := make([]ServiceCall, len(s.serviceCalls))
	copy(calls, s.serviceCalls)
	return calls
}

// ClearServiceCalls resets the service call log.
// Deprecated: Use ServiceCallCount() + GetServiceCallsSince() instead for race-free tracking.
func (s *MockHAServer) ClearServiceCalls() {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	s.serviceCalls = nil
}

// ServiceCallCount returns the current number of recorded service calls.
// Use this to take a snapshot index before triggering an action, then pass
// the index to GetServiceCallsSince() to retrieve only the new calls.
func (s *MockHAServer) ServiceCallCount() int {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	return len(s.serviceCalls)
}

// GetServiceCallsSince returns all service calls recorded after the given index.
// Use with ServiceCallCount() for race-free assertion windows:
//
//	snapshot := server.ServiceCallCount()
//	// ... trigger action ...
//	calls := server.GetServiceCallsSince(snapshot)
func (s *MockHAServer) GetServiceCallsSince(index int) []ServiceCall {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()
	if index >= len(s.serviceCalls) {
		return nil
	}
	calls := make([]ServiceCall, len(s.serviceCalls)-index)
	copy(calls, s.serviceCalls[index:])
	return calls
}

// FindServiceCall finds the most recent service call matching criteria
// Returns nil if no matching call found
func (s *MockHAServer) FindServiceCall(domain, service string, entityID string) *ServiceCall {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()

	// Search backwards to find most recent match
	for i := len(s.serviceCalls) - 1; i >= 0; i-- {
		call := s.serviceCalls[i]
		if call.Domain == domain && call.Service == service {
			// If no entity ID specified, match on domain/service only
			if entityID == "" {
				return &call
			}
			// Check if entity_id matches
			if eid, ok := call.ServiceData["entity_id"].(string); ok && eid == entityID {
				return &call
			}
		}
	}
	return nil
}

// CountServiceCalls counts service calls matching criteria
func (s *MockHAServer) CountServiceCalls(domain, service string) int {
	s.callsMu.Lock()
	defer s.callsMu.Unlock()

	count := 0
	for _, call := range s.serviceCalls {
		if call.Domain == domain && call.Service == service {
			count++
		}
	}
	return count
}
