package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// WebSocket keepalive constants
const (
	// pingInterval is how often we send ping frames to keep connection alive.
	// Set well under typical proxy/load balancer timeouts (often 60-90s).
	pingInterval = 30 * time.Second

	// pongWait is the max time to wait for a pong response.
	// If no pong received within this time, connection is considered dead.
	// Set to 2x pingInterval to tolerate one missed ping.
	pongWait = 60 * time.Second

	// writeWait is the time allowed to write a ping message.
	writeWait = 10 * time.Second
)

// Retry constants for service calls
const (
	// maxRetries is the number of retry attempts for transient network errors.
	// With exponential backoff (500ms, 1s, 2s, 4s, 8s, 15s, 15s, 15s, 15s),
	// this provides approximately 75 seconds of retry coverage to handle
	// network outages lasting up to 1 minute.
	maxRetries = 9

	// initialRetryDelay is the base delay before first retry
	initialRetryDelay = 500 * time.Millisecond

	// maxRetryDelay caps the exponential backoff
	maxRetryDelay = 15 * time.Second
)

// Health tracking constants
const (
	// healthWindowSize is the number of recent service call results to track
	healthWindowSize = 10

	// unhealthyThreshold is the failure rate above which the client is considered unhealthy
	unhealthyThreshold = 0.5

	// minResultsForHealth is the minimum number of results needed before declaring unhealthy
	minResultsForHealth = 3
)

// HAClient defines the interface for Home Assistant WebSocket client
type HAClient interface {
	Connect() error
	Disconnect() error
	IsConnected() bool
	IsHealthy() bool
	GetState(entityID string) (*State, error)
	GetAllStates() ([]*State, error)
	CallService(domain, service string, data map[string]interface{}) error
	CallServiceWithTarget(domain, service string, target *ServiceTarget, data map[string]interface{}) error
	SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error)
	SetInputBoolean(name string, value bool) error
	SetInputNumber(name string, value float64) error
	SetInputText(name string, value string) error
}

// subscriberEntry holds a handler with its unique subscription ID
type subscriberEntry struct {
	subID   int
	handler StateChangeHandler
}

// Client implements HAClient interface
//
// Lock ordering (to prevent deadlocks, always acquire in this order):
//  1. connMu - connection state
//  2. ctxMu - context for cancellation
//  3. writeMu - websocket writes (also protects msgID to ensure ordered sends)
//  4. pendingMu - pending response channels
//  5. subsMu - subscribers
//  6. nextSubIDMu - subscription ID counter
//  7. healthMu - health tracking (acquired last, never held while acquiring others)
//
// Note: msgIDMu has been eliminated; msgID is now protected by writeMu to ensure
// message IDs are allocated and sent atomically, preventing out-of-order sends.
type Client struct {
	url         string
	token       string
	logger      *zap.Logger
	conn        *websocket.Conn
	connected   bool
	connMu      sync.RWMutex
	msgID       int // Protected by writeMu (not separate mutex) to ensure atomic ID+send
	pending     map[int]chan Message
	pendingMu   sync.Mutex
	subscribers map[string][]subscriberEntry
	subsMu      sync.RWMutex
	nextSubID   int
	nextSubIDMu sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	ctxMu       sync.RWMutex // Protects ctx and cancel
	reconnect   bool
	writeMu     sync.Mutex // Protects websocket writes AND msgID counter

	// Health tracking for service calls (rolling window)
	healthMu      sync.RWMutex
	recentResults []bool // circular buffer: true=success, false=failure
	resultIndex   int    // next write position in circular buffer
	resultCount   int    // how many results recorded (up to healthWindowSize)
}

func (c *Client) clearSubscribers() {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()

	if len(c.subscribers) == 0 {
		c.subscribers = make(map[string][]subscriberEntry)
		return
	}

	c.subscribers = make(map[string][]subscriberEntry)
}

// resetContext cancels the current context and creates a new one.
// This function acquires ctxMu internally - callers should NOT hold ctxMu.
// Safe to call while holding connMu (follows lock ordering).
func (c *Client) resetContext() {
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
}

// NewClient creates a new Home Assistant WebSocket client
func NewClient(url, token string, logger *zap.Logger) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		url:           url,
		token:         token,
		logger:        logger,
		pending:       make(map[int]chan Message),
		subscribers:   make(map[string][]subscriberEntry),
		ctx:           ctx,
		cancel:        cancel,
		reconnect:     true,
		recentResults: make([]bool, healthWindowSize),
	}
}

// Connect establishes WebSocket connection and authenticates
func (c *Client) Connect() error {
	c.connMu.Lock()

	if c.connected {
		c.connMu.Unlock()
		return fmt.Errorf("already connected")
	}

	// Reset message ID counter for new connection
	// Each WebSocket session expects message IDs to start from 1
	// Protected by writeMu for consistency, though not strictly needed during Connect
	c.writeMu.Lock()
	c.msgID = 0
	c.writeMu.Unlock()

	// Connect to WebSocket
	conn, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		c.connMu.Unlock()
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	c.conn = conn

	// Receive auth_required message
	var authRequired Message
	if err := c.conn.ReadJSON(&authRequired); err != nil {
		c.conn.Close()
		c.connMu.Unlock()
		return fmt.Errorf("failed to read auth_required: %w", err)
	}

	if authRequired.Type != "auth_required" {
		c.conn.Close()
		c.connMu.Unlock()
		return fmt.Errorf("expected auth_required, got %s", authRequired.Type)
	}

	// Send authentication
	authMsg := AuthMessage{
		Type:        "auth",
		AccessToken: c.token,
	}
	c.writeMu.Lock()
	err = c.conn.WriteJSON(authMsg)
	c.writeMu.Unlock()

	if err != nil {
		c.conn.Close()
		c.connMu.Unlock()
		return fmt.Errorf("failed to send auth: %w", err)
	}

	// Receive auth response
	var authResponse Message
	if err := c.conn.ReadJSON(&authResponse); err != nil {
		c.conn.Close()
		c.connMu.Unlock()
		return fmt.Errorf("failed to read auth response: %w", err)
	}

	if authResponse.Type == "auth_invalid" {
		c.conn.Close()
		c.connMu.Unlock()
		return fmt.Errorf("authentication failed: invalid token")
	}

	if authResponse.Type != "auth_ok" {
		c.conn.Close()
		c.connMu.Unlock()
		return fmt.Errorf("expected auth_ok, got %s", authResponse.Type)
	}

	c.resetContext()
	c.connected = true
	c.reconnect = true
	c.logger.Info("Connected to Home Assistant")

	// Set up WebSocket keepalive
	// Set initial read deadline - will be extended by pong handler
	c.conn.SetReadDeadline(time.Now().Add(pongWait))

	// Set pong handler to extend read deadline on each pong received
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// Start background ping sender
	go c.sendPings()

	// Start background message receiver
	go c.receiveMessages()

	// Release lock before calling subscribeToStateChanges to avoid deadlock
	c.connMu.Unlock()

	// Subscribe to state_changed events
	if err := c.subscribeToStateChanges(); err != nil {
		c.logger.Warn("Failed to subscribe to state changes", zap.Error(err))
	}

	return nil
}

// Disconnect closes the WebSocket connection
func (c *Client) Disconnect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if !c.connected {
		return nil
	}

	c.reconnect = false

	// Cancel context
	c.ctxMu.Lock()
	c.cancel()
	c.ctxMu.Unlock()

	c.connected = false

	if c.conn != nil {
		// Send close message (protected by writeMu)
		c.writeMu.Lock()
		c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.writeMu.Unlock()

		c.conn.Close()
		c.conn = nil
	}

	c.clearSubscribers()
	c.logger.Info("Disconnected from Home Assistant")
	return nil
}

// IsConnected returns true if client is connected
func (c *Client) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.connected
}

// recordServiceResult records the result of a service call for health tracking.
// Uses a circular buffer to track the last healthWindowSize results.
func (c *Client) recordServiceResult(success bool) {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()

	c.recentResults[c.resultIndex] = success
	c.resultIndex = (c.resultIndex + 1) % healthWindowSize
	if c.resultCount < healthWindowSize {
		c.resultCount++
	}
}

// IsHealthy returns true if the client is connected and service calls are succeeding.
// Returns false if disconnected or if more than 50% of recent service calls failed.
// Requires at least minResultsForHealth results before declaring unhealthy.
func (c *Client) IsHealthy() bool {
	if !c.IsConnected() {
		return false
	}

	c.healthMu.RLock()
	defer c.healthMu.RUnlock()

	// Not enough data to determine health - assume healthy
	if c.resultCount < minResultsForHealth {
		return true
	}

	// Count failures in the tracked results
	failures := 0
	for i := 0; i < c.resultCount; i++ {
		if !c.recentResults[i] {
			failures++
		}
	}

	failureRate := float64(failures) / float64(c.resultCount)
	return failureRate < unhealthyThreshold
}

// nextMsgID returns the next message ID.
// IMPORTANT: Caller must hold writeMu to ensure atomic ID allocation + send.
func (c *Client) nextMsgID() int {
	c.msgID++
	return c.msgID
}

// sendMessage sends a message and waits for response.
// The message ID is assigned atomically with the send to prevent out-of-order delivery.
// This fixes the race condition where goroutines could get sequential IDs but send them
// out of order, causing Home Assistant to return "id_reuse" errors.
func (c *Client) sendMessage(msg interface{}) (*Message, error) {
	// Capture connection reference while holding the lock to prevent race with Disconnect().
	// Invariant: connected == true implies conn != nil (enforced by Connect/Disconnect).
	c.connMu.RLock()
	if !c.connected {
		c.connMu.RUnlock()
		return nil, fmt.Errorf("not connected")
	}
	conn := c.conn
	c.connMu.RUnlock()

	// Get context for cancellation check
	c.ctxMu.RLock()
	ctx := c.ctx
	c.ctxMu.RUnlock()

	// Create response channel (created before locking writeMu to minimize lock hold time)
	respChan := make(chan Message, 1)

	// CRITICAL: Hold writeMu from ID generation through send to ensure messages are
	// sent in ID order. This prevents the race where:
	//   1. Goroutine A gets ID 100
	//   2. Goroutine B gets ID 101
	//   3. Goroutine B sends ID 101 first
	//   4. Goroutine A sends ID 100 -> HA returns "id_reuse" error
	c.writeMu.Lock()

	// Generate message ID while holding writeMu
	msgID := c.nextMsgID()

	// Assign ID to the message
	switch m := msg.(type) {
	case *CallServiceRequest:
		m.ID = msgID
	case *GetStatesRequest:
		m.ID = msgID
	case *SubscribeEventsRequest:
		m.ID = msgID
	default:
		c.writeMu.Unlock()
		return nil, fmt.Errorf("unsupported message type")
	}

	// Register response channel before sending (still holding writeMu)
	c.pendingMu.Lock()
	c.pending[msgID] = respChan
	c.pendingMu.Unlock()

	// Send message while still holding writeMu
	err := conn.WriteJSON(msg)
	c.writeMu.Unlock()

	// Clean up on exit
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, msgID)
		c.pendingMu.Unlock()
	}()

	if err != nil {
		return nil, fmt.Errorf("failed to send message: %w", err)
	}

	// Wait for response with timeout.
	// This timeout (10s) is intentionally slightly higher than Home Assistant's
	// internal timeout for Sonos operations (9.5s), allowing HA to return errors
	// rather than having the Go client timeout first.
	select {
	case resp := <-respChan:
		if resp.Success != nil && !*resp.Success {
			if resp.Error != nil {
				return nil, fmt.Errorf("HA error: %s - %s", resp.Error.Code, resp.Error.Message)
			}
			return nil, fmt.Errorf("request failed")
		}
		return &resp, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("timeout waiting for response")
	case <-ctx.Done():
		return nil, fmt.Errorf("client disconnected")
	}
}

// receiveMessages handles incoming messages in the background.
// Called from Connect() after connection is established, so conn is guaranteed non-nil.
func (c *Client) receiveMessages() {
	// Capture context reference - replaced on reconnect, so we capture once at start.
	// When cancelled (by Disconnect or resetContext), we exit gracefully.
	c.ctxMu.RLock()
	ctx := c.ctx
	c.ctxMu.RUnlock()

	// Capture connection reference to avoid race with Disconnect setting c.conn = nil.
	// This goroutine is spawned from Connect() which guarantees conn != nil at this point.
	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	for {
		// Check if context is cancelled before blocking on read
		select {
		case <-ctx.Done():
			return
		default:
		}

		var msg Message
		if err := conn.ReadJSON(&msg); err != nil {
			c.logger.Error("Failed to read message", zap.Error(err))
			c.handleDisconnect()
			return
		}

		// Handle event messages
		if msg.Type == "event" {
			c.handleEvent(&msg)
			continue
		}

		// Route response to waiting goroutine
		if msg.ID > 0 {
			c.pendingMu.Lock()
			if ch, ok := c.pending[msg.ID]; ok {
				select {
				case ch <- msg:
				default:
					c.logger.Warn("Response channel full", zap.Int("msg_id", msg.ID))
				}
			}
			c.pendingMu.Unlock()
		}
	}
}

// handleEvent processes event messages
func (c *Client) handleEvent(msg *Message) {
	if msg.Event == nil {
		return
	}

	// Only handle state_changed events
	if msg.Event.EventType != "state_changed" {
		return
	}

	var eventData StateChangedEvent
	if err := json.Unmarshal(msg.Event.Data, &eventData); err != nil {
		c.logger.Error("Failed to unmarshal state_changed event", zap.Error(err))
		return
	}

	// Notify subscribers
	c.subsMu.RLock()
	entries := append([]subscriberEntry(nil), c.subscribers[eventData.EntityID]...)
	c.subsMu.RUnlock()

	// Call handlers in separate goroutines to avoid blocking receiveMessages
	// This prevents deadlocks when handlers try to send messages back to HA
	for _, entry := range entries {
		go entry.handler(eventData.EntityID, eventData.OldState, eventData.NewState)
	}
}

// handleDisconnect handles connection loss
func (c *Client) handleDisconnect() {
	c.connMu.Lock()
	c.connected = false
	c.connMu.Unlock()

	c.logger.Warn("Connection lost")

	if !c.reconnect {
		return
	}

	// Attempt to reconnect with exponential backoff
	go c.attemptReconnect()
}

// attemptReconnect tries to reconnect with exponential backoff
func (c *Client) attemptReconnect() {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		// Get context for cancellation check
		c.ctxMu.RLock()
		ctx := c.ctx
		c.ctxMu.RUnlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		c.logger.Info("Attempting to reconnect...")

		if err := c.Connect(); err != nil {
			c.logger.Error("Reconnection failed", zap.Error(err))
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		c.logger.Info("Reconnected successfully")
		return
	}
}

// sendPings sends periodic ping frames to keep the WebSocket connection alive.
// This prevents proxies/load balancers from terminating idle connections.
func (c *Client) sendPings() {
	// Capture context reference at startup
	c.ctxMu.RLock()
	ctx := c.ctx
	c.ctxMu.RUnlock()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if still connected before attempting ping
			c.connMu.RLock()
			if !c.connected {
				c.connMu.RUnlock()
				return
			}
			conn := c.conn
			c.connMu.RUnlock()

			// Send ping with write deadline
			c.writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait))
			c.writeMu.Unlock()

			if err != nil {
				c.logger.Warn("Failed to send ping", zap.Error(err))
				// Don't trigger disconnect here - let receiveMessages handle it
				// when the read deadline expires
				return
			}
		}
	}
}

// subscribeToStateChanges subscribes to all state_changed events
func (c *Client) subscribeToStateChanges() error {
	req := &SubscribeEventsRequest{
		Type:      "subscribe_events",
		EventType: "state_changed",
	}

	_, err := c.sendMessage(req)
	return err
}

// GetState retrieves the state of an entity
func (c *Client) GetState(entityID string) (*State, error) {
	states, err := c.GetAllStates()
	if err != nil {
		return nil, err
	}

	for _, state := range states {
		if state.EntityID == entityID {
			return state, nil
		}
	}

	return nil, fmt.Errorf("entity %s not found", entityID)
}

// GetAllStates retrieves all entity states
func (c *Client) GetAllStates() ([]*State, error) {
	req := &GetStatesRequest{
		Type: "get_states",
	}

	resp, err := c.sendMessage(req)
	if err != nil {
		return nil, err
	}

	var states []*State
	if err := json.Unmarshal(resp.Result, &states); err != nil {
		return nil, fmt.Errorf("failed to unmarshal states: %w", err)
	}

	return states, nil
}

// isRetryableError determines if an error is a transient network error worth retrying.
// Returns true for connection errors, timeouts, and websocket close errors.
// Returns false for HA application errors (which won't succeed on retry).
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()

	// Network-level errors that indicate transient issues
	retryablePatterns := []string{
		"i/o timeout",
		"connection reset",
		"connection refused",
		"broken pipe",
		"no such host",
		"network is unreachable",
		"not connected",
		"use of closed network connection",
		"timeout waiting for response",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// WebSocket close errors are retryable (connection dropped)
	if websocket.IsCloseError(err, websocket.CloseAbnormalClosure, websocket.CloseGoingAway) {
		return true
	}

	return false
}

// CallService calls a Home Assistant service with automatic retry for transient errors.
// Uses exponential backoff starting at 500ms, doubling each retry (capped at 15s).
func (c *Client) CallService(domain, service string, data map[string]interface{}) error {
	var lastErr error
	delay := initialRetryDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			c.logger.Warn("Retrying service call",
				zap.String("domain", domain),
				zap.String("service", service),
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay),
				zap.Error(lastErr),
			)
			time.Sleep(delay)

			// Exponential backoff
			delay *= 2
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
		}

		req := &CallServiceRequest{
			Type:        "call_service",
			Domain:      domain,
			Service:     service,
			ServiceData: data,
		}

		_, err := c.sendMessage(req)
		if err == nil {
			c.recordServiceResult(true)
			if attempt > 0 {
				c.logger.Info("Service call succeeded after retry",
					zap.String("domain", domain),
					zap.String("service", service),
					zap.Int("attempts", attempt+1),
				)
			}
			return nil
		}

		lastErr = err

		// Only retry for transient network errors
		if !isRetryableError(err) {
			c.recordServiceResult(false)
			return err
		}
	}

	c.recordServiceResult(false)
	return fmt.Errorf("service call failed after %d attempts: %w", maxRetries+1, lastErr)
}

// CallServiceWithTarget calls a Home Assistant service with an explicit target.
// Uses the same retry logic as CallService.
func (c *Client) CallServiceWithTarget(domain, service string, target *ServiceTarget, data map[string]interface{}) error {
	var lastErr error
	delay := initialRetryDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			c.logger.Warn("Retrying service call with target",
				zap.String("domain", domain),
				zap.String("service", service),
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay),
				zap.Error(lastErr),
			)
			time.Sleep(delay)

			// Exponential backoff
			delay *= 2
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
		}

		req := &CallServiceRequest{
			Type:        "call_service",
			Domain:      domain,
			Service:     service,
			Target:      target,
			ServiceData: data,
		}

		_, err := c.sendMessage(req)
		if err == nil {
			if attempt > 0 {
				c.logger.Info("Service call with target succeeded after retry",
					zap.String("domain", domain),
					zap.String("service", service),
					zap.Int("attempts", attempt+1),
				)
			}
			return nil
		}

		lastErr = err

		// Only retry for transient network errors
		if !isRetryableError(err) {
			return err
		}
	}

	return fmt.Errorf("service call with target failed after %d attempts: %w", maxRetries+1, lastErr)
}

// SubscribeStateChanges subscribes to state changes for a specific entity
func (c *Client) SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error) {
	// Get unique subscription ID
	c.nextSubIDMu.Lock()
	subID := c.nextSubID
	c.nextSubID++
	c.nextSubIDMu.Unlock()

	// Add subscriber entry
	c.subsMu.Lock()
	c.subscribers[entityID] = append(c.subscribers[entityID], subscriberEntry{
		subID:   subID,
		handler: handler,
	})
	c.subsMu.Unlock()

	return &subscription{
		entityID: entityID,
		subID:    subID,
		client:   c,
	}, nil
}

// unsubscribe removes a specific subscription by entity ID and subscription ID
func (c *Client) unsubscribe(entityID string, subID int) error {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()

	subscribers, ok := c.subscribers[entityID]
	if !ok {
		return nil // Already unsubscribed
	}

	// Find and remove the subscription with matching subID
	for i, entry := range subscribers {
		if entry.subID == subID {
			// Remove this entry by slicing
			c.subscribers[entityID] = append(subscribers[:i], subscribers[i+1:]...)

			// If no more subscribers for this entity, delete the entry
			if len(c.subscribers[entityID]) == 0 {
				delete(c.subscribers, entityID)
			}
			break
		}
	}

	return nil
}

// SetInputBoolean sets the value of an input_boolean
func (c *Client) SetInputBoolean(name string, value bool) error {
	service := "turn_off"
	if value {
		service = "turn_on"
	}

	return c.CallService("input_boolean", service, map[string]interface{}{
		"entity_id": fmt.Sprintf("input_boolean.%s", name),
	})
}

// SetInputNumber sets the value of an input_number
func (c *Client) SetInputNumber(name string, value float64) error {
	return c.CallService("input_number", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_number.%s", name),
		"value":     value,
	})
}

// SetInputText sets the value of an input_text
func (c *Client) SetInputText(name string, value string) error {
	return c.CallService("input_text", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_text.%s", name),
		"value":     value,
	})
}
