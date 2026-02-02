package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// Application-level ping/pong keepalive constants.
// Home Assistant expects JSON pings ({"id": N, "type": "ping"}) and responds with pongs.
// WebSocket control frame pings are NOT sufficient - HA only resets its idle timeout
// on application-layer JSON activity. See: https://developers.home-assistant.io/docs/api/websocket/
const (
	// pingInterval is how often we send application-level JSON pings.
	// Set aggressively low (5s) because Tailscale DERP relays and some NATs
	// may timeout connections as early as 10-15 seconds of inactivity.
	// The previous value of 15s was too slow - connections were timing out at
	// exactly 10 seconds before the first ping could be sent.
	pingInterval = 5 * time.Second

	// pongWait is the max time the connection can be silent before we consider it dead.
	// The read deadline is extended on ANY successful message read (not just pongs),
	// so this is effectively "max time without any messages from Home Assistant".
	// Set to 3x pingInterval to tolerate network latency and brief HA pauses.
	pongWait = 15 * time.Second

	// writeWait is the time allowed to write a message (including pings).
	writeWait = 10 * time.Second
)

// Note: TCP keepalive constants (tcpKeepIdle, tcpKeepInterval, tcpKeepCount) are
// defined in tcp_keepalive.go and configured via syscalls for proper dead connection
// detection. The old net.Dialer.KeepAlive approach was insufficient because it doesn't
// set TCP_KEEPIDLE (time before first probe), which defaults to 2 hours on Linux.
// See: https://github.com/golang/go/issues/62254

// Retry constants for service calls
const (
	// maxRetries is the number of retry attempts for transient network errors.
	// With exponential backoff (500ms, 1s, 2s, 4s, 8s, 16s, 30s, 30s, 30s, 30s, 30s, 30s),
	// this provides approximately 3 minutes of retry coverage to handle
	// Home Assistant restarts which can take 1.5-2 minutes.
	maxRetries = 12

	// initialRetryDelay is the base delay before first retry
	initialRetryDelay = 500 * time.Millisecond

	// maxRetryDelay caps the exponential backoff
	maxRetryDelay = 30 * time.Second
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
	CallService(ctx context.Context, domain, service string, data map[string]interface{}) error
	CallServiceWithTarget(ctx context.Context, domain, service string, target *ServiceTarget, data map[string]interface{}) error
	SubscribeStateChanges(entityID string, handler StateChangeHandler) (Subscription, error)
	SetInputBoolean(name string, value bool) error
	SetInputNumber(name string, value float64) error
	SetInputText(name string, value string) error
	SendNotification(deviceName string, notification *Notification) error
	SendNotificationToMultiple(deviceNames []string, notification *Notification) error
	ClearNotification(deviceName, tag string) error
	SetReconnectCallback(cb func())
	GetReconnectCount() int
	GetDisconnectCount() int
	GetLastDisconnectTime() time.Time
	GetWriteTimeoutCount() int
	GetConnectionDuration() time.Duration
	GetDevices() ([]*Device, error)
	GetEntityRegistry() ([]*EntityRegistryEntry, error)
}

// errConnectionClosed is returned when attempting to use a closed connection.
var errConnectionClosed = fmt.Errorf("connection is closed")

// managedConn wraps a WebSocket connection with lifecycle management.
// It provides thread-safe access and handles nil checks internally.
type managedConn struct {
	mu     sync.RWMutex
	conn   *websocket.Conn
	closed bool
}

// newManagedConn creates a new managed connection wrapper.
func newManagedConn(conn *websocket.Conn) *managedConn {
	return &managedConn{conn: conn}
}

// WriteJSON writes a JSON message to the connection.
// Returns errConnectionClosed if the connection is closed or nil.
func (m *managedConn) WriteJSON(v interface{}) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.conn == nil {
		return errConnectionClosed
	}
	return m.conn.WriteJSON(v)
}

// WriteMessage writes a message to the connection.
// Returns errConnectionClosed if the connection is closed or nil.
func (m *managedConn) WriteMessage(messageType int, data []byte) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.conn == nil {
		return errConnectionClosed
	}
	return m.conn.WriteMessage(messageType, data)
}

// ReadJSON reads a JSON message from the connection.
// Returns errConnectionClosed if the connection is closed or nil.
func (m *managedConn) ReadJSON(v interface{}) error {
	m.mu.RLock()
	conn := m.conn
	closed := m.closed
	m.mu.RUnlock()
	if closed || conn == nil {
		return errConnectionClosed
	}
	return conn.ReadJSON(v)
}

// SetReadDeadline sets the read deadline on the connection.
func (m *managedConn) SetReadDeadline(t time.Time) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.conn == nil {
		return errConnectionClosed
	}
	return m.conn.SetReadDeadline(t)
}

// SetWriteDeadline sets the write deadline on the connection.
func (m *managedConn) SetWriteDeadline(t time.Time) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.conn == nil {
		return errConnectionClosed
	}
	return m.conn.SetWriteDeadline(t)
}

// Close marks the connection as closed and closes the underlying connection.
func (m *managedConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	if m.conn != nil {
		return m.conn.Close()
	}
	return nil
}

// IsClosed returns true if the connection has been closed.
func (m *managedConn) IsClosed() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.closed
}

// subscriberEntry holds a handler with its unique subscription ID
type subscriberEntry struct {
	subID   int
	handler StateChangeHandler
}

// Client implements HAClient interface
//
// Lock ordering (to prevent deadlocks, always acquire in this order):
//  1. connMu - connection state and metrics
//  2. ctxMu - context for cancellation
//  3. writeMu - websocket writes (also protects msgID to ensure ordered sends)
//  4. pendingMu - pending response channels
//  5. subsMu - subscribers
//  6. nextSubIDMu - subscription ID counter
//  7. healthMu - health tracking (acquired last, never held while acquiring others)
//
// Note: msgIDMu has been eliminated; msgID is now protected by writeMu to ensure
// message IDs are allocated and sent atomically, preventing out-of-order sends.
//
// Note: metricsMu has been consolidated into connMu since both protect
// connection-related state. This reduces mutex count and simplifies lock ordering.
type Client struct {
	url       string
	token     string
	logger    *zap.Logger
	conn      *managedConn // Thread-safe connection wrapper
	connected bool
	connMu    sync.RWMutex // Protects connected, conn, reconnect, and connection metrics

	// Connection metrics (protected by connMu)
	disconnectCount    int       // Total number of disconnections
	lastDisconnectTime time.Time // When the last disconnect occurred
	writeTimeoutCount  int       // Number of write timeouts detected
	connectedAt        time.Time // When the current connection was established

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

	// Ping goroutine lifecycle management
	pingCancel context.CancelFunc // Cancels the ping goroutine; protected by connMu

	// Reconnect callback (called after successful reconnection)
	onReconnect   func()
	onReconnectMu sync.RWMutex

	// Reconnect metrics for observability
	reconnectCount   int
	reconnectCountMu sync.RWMutex

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

	// Cancel any existing ping goroutine from a previous connection
	if c.pingCancel != nil {
		c.pingCancel()
		c.pingCancel = nil
	}

	// Reset message ID counter for new connection
	// Each WebSocket session expects message IDs to start from 1
	// Protected by writeMu for consistency, though not strictly needed during Connect
	c.writeMu.Lock()
	c.msgID = 0
	c.writeMu.Unlock()

	// Connect to WebSocket with proper TCP keepalive configuration.
	// We use syscalls to configure TCP_KEEPIDLE, TCP_KEEPINTVL, and TCP_KEEPCNT
	// because Go's net.Dialer.KeepAlive only sets the interval but NOT the idle time
	// before the first probe (TCP_KEEPIDLE defaults to 2 hours on Linux).
	// See: https://github.com/golang/go/issues/62254
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{}
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			// Configure aggressive TCP keepalive using syscalls
			// This detects dead connections in ~25 seconds (10s idle + 5s * 3 probes)
			if err := setTCPKeepAlive(conn, tcpKeepIdle, tcpKeepInterval, tcpKeepCount); err != nil {
				c.logger.Warn("Failed to set TCP keepalive (non-fatal)", zap.Error(err))
				// Continue anyway - better to connect without optimal keepalive
				// than fail completely
			}

			return conn, nil
		},
		HandshakeTimeout: 45 * time.Second,
	}
	rawConn, _, err := dialer.Dial(c.url, nil)
	if err != nil {
		c.connMu.Unlock()
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	// Wrap connection with lifecycle management
	c.conn = newManagedConn(rawConn)

	// Record connection time for duration tracking (protected by connMu, already held)
	c.connectedAt = time.Now()

	c.logger.Debug("WebSocket connected with TCP keepalive enabled",
		zap.Duration("tcp_keep_idle", tcpKeepIdle),
		zap.Duration("tcp_keep_interval", tcpKeepInterval),
		zap.Int("tcp_keep_count", tcpKeepCount))

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

	// Set up keepalive with application-level pings
	// Set initial read deadline - will be extended when we receive pong responses
	c.conn.SetReadDeadline(time.Now().Add(pongWait))

	// Create a fresh context for the ping goroutine
	// This ensures the ping goroutine is properly cancelled on disconnect/reconnect
	pingCtx, pingCancel := context.WithCancel(context.Background())
	c.pingCancel = pingCancel

	// Start background ping sender with explicit connection and context
	// This eliminates races where the goroutine captures a stale connection reference
	conn := c.conn
	go c.sendPings(pingCtx, conn)

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

	// Cancel ping goroutine first to prevent it from using the closing connection
	if c.pingCancel != nil {
		c.pingCancel()
		c.pingCancel = nil
	}

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

// SetReconnectCallback registers a callback to be invoked after successful reconnection.
// This allows callers to perform actions like state resynchronization after connection recovery.
// The callback is invoked asynchronously to avoid blocking the reconnection loop.
func (c *Client) SetReconnectCallback(cb func()) {
	c.onReconnectMu.Lock()
	defer c.onReconnectMu.Unlock()
	c.onReconnect = cb
}

// GetReconnectCount returns the number of successful reconnections since client creation.
// This metric is useful for monitoring connection stability.
func (c *Client) GetReconnectCount() int {
	c.reconnectCountMu.RLock()
	defer c.reconnectCountMu.RUnlock()
	return c.reconnectCount
}

// GetDisconnectCount returns the number of disconnections since client creation.
func (c *Client) GetDisconnectCount() int {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.disconnectCount
}

// GetLastDisconnectTime returns when the last disconnect occurred.
// Returns zero time if no disconnects have occurred.
func (c *Client) GetLastDisconnectTime() time.Time {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.lastDisconnectTime
}

// GetWriteTimeoutCount returns the number of write timeouts detected.
// Write timeouts indicate the connection is stale and trigger reconnection.
func (c *Client) GetWriteTimeoutCount() int {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.writeTimeoutCount
}

// GetConnectionDuration returns how long the current connection has been active.
// Returns 0 if not currently connected.
func (c *Client) GetConnectionDuration() time.Duration {
	c.connMu.RLock()
	defer c.connMu.RUnlock()

	if !c.connected {
		return 0
	}

	if c.connectedAt.IsZero() {
		return 0
	}
	return time.Since(c.connectedAt)
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
	case *PingRequest:
		m.ID = msgID
	case *DeviceRegistryListRequest:
		m.ID = msgID
	case *EntityRegistryListRequest:
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
		// If write failed with timeout, the connection is likely dead.
		// Close it immediately to trigger reconnection rather than waiting
		// for the next ping or accumulating more failed retries.
		if strings.Contains(err.Error(), "i/o timeout") {
			c.connMu.Lock()
			c.writeTimeoutCount++
			timeoutNum := c.writeTimeoutCount
			c.connMu.Unlock()
			c.logger.Warn("Write timeout detected, closing connection to trigger reconnect",
				zap.Error(err), zap.Int("timeout_number", timeoutNum))
			conn.Close()
		}
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

		// Extend read deadline on ANY successful read, not just pongs.
		// This prevents timeouts when we're receiving lots of state_changed events
		// but a pong response is delayed due to network latency.
		// The connection is clearly alive if we're receiving any messages.
		conn.SetReadDeadline(time.Now().Add(pongWait))

		// Handle pong responses (no special action needed since we already extended deadline)
		if msg.Type == "pong" {
			continue
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
	wasConnected := c.connected
	c.connected = false

	// Cancel ping goroutine to prevent it from using the closing connection
	if c.pingCancel != nil {
		c.pingCancel()
		c.pingCancel = nil
	}

	// Close the old connection to allow Home Assistant to accept new connections.
	// Without this, HA may reject reconnection attempts with "bad handshake" because
	// the old WebSocket connection is still considered active on HA's side.
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	// Only log and count if we were actually connected (avoid duplicate disconnect handling)
	if !wasConnected {
		c.connMu.Unlock()
		return
	}

	// Update disconnect metrics (protected by connMu, already held)
	c.disconnectCount++
	c.lastDisconnectTime = time.Now()
	disconnectNum := c.disconnectCount
	shouldReconnect := c.reconnect
	c.connMu.Unlock()

	c.logger.Warn("Connection lost", zap.Int("disconnect_number", disconnectNum))

	if !shouldReconnect {
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

		// Increment reconnect count for observability
		c.reconnectCountMu.Lock()
		c.reconnectCount++
		reconnectNum := c.reconnectCount
		c.reconnectCountMu.Unlock()

		c.logger.Info("Reconnected successfully", zap.Int("reconnect_number", reconnectNum))

		// Invoke reconnect callback asynchronously to avoid blocking the reconnect loop
		c.onReconnectMu.RLock()
		cb := c.onReconnect
		c.onReconnectMu.RUnlock()

		if cb != nil {
			go func() {
				c.logger.Info("Invoking reconnect callback for state reconciliation")
				cb()
			}()
		}

		return
	}
}

// sendPings sends periodic application-level JSON pings to keep the connection alive.
// Home Assistant expects {"id": N, "type": "ping"} messages and responds with {"id": N, "type": "pong"}.
// WebSocket control frame pings are NOT sufficient - HA only resets its idle timeout on application-layer activity.
// See: https://developers.home-assistant.io/docs/api/websocket/
//
// The context and connection are passed explicitly to ensure clean goroutine lifecycle:
// - When the context is cancelled (on disconnect/reconnect), this goroutine exits cleanly
// - The connection reference is captured at startup, eliminating races with stale references
func (c *Client) sendPings(ctx context.Context, conn *managedConn) {
	// CRITICAL: Send an immediate ping right after connection to establish
	// application-layer activity. Without this, the first ping would only
	// come after pingInterval (5s), but NATs and DERP relays may timeout
	// the connection faster than that during the initial state sync.
	if err := c.sendImmediatePing(conn); err != nil {
		c.logger.Warn("Failed to send initial ping, closing connection", zap.Error(err))
		conn.Close()
		return
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check if connection is closed before attempting ping
			if conn.IsClosed() {
				return
			}

			if err := c.sendImmediatePing(conn); err != nil {
				c.logger.Warn("Failed to send application ping, closing connection", zap.Error(err))
				// Close the connection to immediately unblock receiveMessages,
				// which will call handleDisconnect() to trigger reconnection.
				// This is faster than waiting for the read deadline to expire.
				conn.Close()
				return
			}
		}
	}
}

// sendImmediatePing sends an application-level JSON ping immediately.
// Returns an error if the ping fails to send.
func (c *Client) sendImmediatePing(conn *managedConn) error {
	if conn == nil || conn.IsClosed() {
		return errConnectionClosed
	}
	c.writeMu.Lock()
	msgID := c.nextMsgID()
	pingReq := PingRequest{
		ID:   msgID,
		Type: "ping",
	}
	conn.SetWriteDeadline(time.Now().Add(writeWait))
	err := conn.WriteJSON(pingReq)
	c.writeMu.Unlock()

	if err != nil {
		return err
	}
	c.logger.Debug("Application ping sent successfully", zap.Int("id", msgID))
	return nil
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
// The context can be used to cancel the operation during shutdown.
func (c *Client) CallService(ctx context.Context, domain, service string, data map[string]interface{}) error {
	var lastErr error
	delay := initialRetryDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Check for context cancellation before each attempt
		select {
		case <-ctx.Done():
			return fmt.Errorf("service call cancelled: %w", ctx.Err())
		default:
		}

		if attempt > 0 {
			c.logger.Warn("Retrying service call",
				zap.String("domain", domain),
				zap.String("service", service),
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay),
				zap.Error(lastErr),
			)

			// Use select to respect context cancellation during retry delay
			select {
			case <-ctx.Done():
				return fmt.Errorf("service call cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}

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
// The context can be used to cancel the operation during shutdown.
func (c *Client) CallServiceWithTarget(ctx context.Context, domain, service string, target *ServiceTarget, data map[string]interface{}) error {
	var lastErr error
	delay := initialRetryDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Check for context cancellation before each attempt
		select {
		case <-ctx.Done():
			return fmt.Errorf("service call with target cancelled: %w", ctx.Err())
		default:
		}

		if attempt > 0 {
			c.logger.Warn("Retrying service call with target",
				zap.String("domain", domain),
				zap.String("service", service),
				zap.Int("attempt", attempt),
				zap.Duration("delay", delay),
				zap.Error(lastErr),
			)

			// Use select to respect context cancellation during retry delay
			select {
			case <-ctx.Done():
				return fmt.Errorf("service call with target cancelled during retry: %w", ctx.Err())
			case <-time.After(delay):
			}

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

	return c.CallService(context.Background(), "input_boolean", service, map[string]interface{}{
		"entity_id": fmt.Sprintf("input_boolean.%s", name),
	})
}

// SetInputNumber sets the value of an input_number
func (c *Client) SetInputNumber(name string, value float64) error {
	return c.CallService(context.Background(), "input_number", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_number.%s", name),
		"value":     value,
	})
}

// SetInputText sets the value of an input_text
func (c *Client) SetInputText(name string, value string) error {
	return c.CallService(context.Background(), "input_text", "set_value", map[string]interface{}{
		"entity_id": fmt.Sprintf("input_text.%s", name),
		"value":     value,
	})
}

// GetDevices retrieves all devices from the Home Assistant device registry.
// This provides device-level information including labels, area assignments, and manufacturer details.
func (c *Client) GetDevices() ([]*Device, error) {
	req := &DeviceRegistryListRequest{
		Type: "config/device_registry/list",
	}

	resp, err := c.sendMessage(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get device registry: %w", err)
	}

	var devices []*Device
	if err := json.Unmarshal(resp.Result, &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devices: %w", err)
	}

	return devices, nil
}

// GetEntityRegistry retrieves all entities from the Home Assistant entity registry.
// This provides entity-to-device mappings, labels, and disabled state information.
func (c *Client) GetEntityRegistry() ([]*EntityRegistryEntry, error) {
	req := &EntityRegistryListRequest{
		Type: "config/entity_registry/list",
	}

	resp, err := c.sendMessage(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get entity registry: %w", err)
	}

	var entities []*EntityRegistryEntry
	if err := json.Unmarshal(resp.Result, &entities); err != nil {
		return nil, fmt.Errorf("failed to unmarshal entity registry: %w", err)
	}

	return entities, nil
}
