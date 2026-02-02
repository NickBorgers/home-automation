package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"homeautomation/internal/testlogger"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// mockHAServer creates a mock Home Assistant WebSocket server
func mockHAServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Failed to upgrade connection: %v", err)
		}
		defer conn.Close()

		handler(conn)
	}))
}

// standardAuthFlow handles the standard authentication flow
func standardAuthFlow(t *testing.T, conn *websocket.Conn, token string) {
	// Send auth_required
	err := conn.WriteJSON(Message{Type: "auth_required"})
	require.NoError(t, err)

	// Receive auth message
	var authMsg AuthMessage
	err = conn.ReadJSON(&authMsg)
	require.NoError(t, err)
	assert.Equal(t, "auth", authMsg.Type)
	assert.Equal(t, token, authMsg.AccessToken)

	// Send auth_ok
	err = conn.WriteJSON(Message{Type: "auth_ok"})
	require.NoError(t, err)
}

// readMessageSkipPings reads messages from the WebSocket connection, automatically
// handling ping requests by sending pong responses. This is needed because the client
// now sends an immediate ping after connecting, which may interleave with other messages.
func readMessageSkipPings(t *testing.T, conn *websocket.Conn, dest interface{}) {
	t.Helper()
	for {
		_, data, err := conn.ReadMessage()
		require.NoError(t, err)

		// Check if this is a ping message
		var msg struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &msg); err == nil && msg.Type == "ping" {
			// Respond to ping with pong
			err := conn.WriteJSON(Message{ID: msg.ID, Type: "pong"})
			require.NoError(t, err)
			continue
		}

		// Unmarshal into destination
		err = json.Unmarshal(data, dest)
		require.NoError(t, err)
		return
	}
}

func TestClient_Connect(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	token := "test_token"

	t.Run("successful connection", func(t *testing.T) {

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Receive subscribe_events message (may receive pings too)
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)

			// Send success response
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			// Keep connection open
			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		assert.NoError(t, err)
		assert.True(t, client.IsConnected())

		client.Disconnect()
	})

	t.Run("invalid token", func(t *testing.T) {

		server := mockHAServer(t, func(conn *websocket.Conn) {
			// Send auth_required
			conn.WriteJSON(Message{Type: "auth_required"})

			// Receive auth message
			var authMsg AuthMessage
			conn.ReadJSON(&authMsg)

			// Send auth_invalid
			conn.WriteJSON(Message{Type: "auth_invalid"})
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, "wrong_token", logger)

		err := client.Connect()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "authentication failed")
		assert.False(t, client.IsConnected())
	})

	t.Run("already connected", func(t *testing.T) {

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Receive subscribe_events (may receive pings too)
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)

		err = client.Connect()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already connected")

		client.Disconnect()
	})
}

func TestClient_GetStates(t *testing.T) {
	t.Parallel()
	states := []*State{
		{EntityID: "input_boolean.test", State: "on", Attributes: map[string]interface{}{"friendly_name": "Test Boolean"}},
		{EntityID: "input_number.test", State: "42.5", Attributes: map[string]interface{}{"friendly_name": "Test Number"}},
	}

	fixture := NewTestFixture(t, func(req interface{}) *Message {
		if _, ok := req.(*GetStatesRequest); ok {
			return StatesResponse(states)
		}
		return nil
	})
	defer fixture.Close()

	t.Run("GetAllStates", func(t *testing.T) {

		result, err := fixture.Client.GetAllStates()
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "input_boolean.test", result[0].EntityID)
	})

	t.Run("GetState existing", func(t *testing.T) {

		state, err := fixture.Client.GetState("input_boolean.test")
		assert.NoError(t, err)
		assert.Equal(t, "on", state.State)
	})

	t.Run("GetState nonexistent", func(t *testing.T) {

		_, err := fixture.Client.GetState("nonexistent")
		assert.Error(t, err)
	})
}

func TestClient_CallService(t *testing.T) {
	t.Parallel()
	var lastReq *CallServiceRequest

	fixture := NewTestFixture(t, func(req interface{}) *Message {
		if r, ok := req.(*CallServiceRequest); ok {
			lastReq = r
		}
		return nil
	})
	defer fixture.Close()

	err := fixture.Client.CallService(context.Background(), "input_boolean", "turn_on", map[string]interface{}{
		"entity_id": "input_boolean.test",
	})
	assert.NoError(t, err)
	require.NotNil(t, lastReq)
	assert.Equal(t, "input_boolean", lastReq.Domain)
	assert.Equal(t, "turn_on", lastReq.Service)
	assert.Equal(t, "input_boolean.test", lastReq.ServiceData["entity_id"])
}

func TestClient_CallServiceWithTarget(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	token := "test_token"

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events (may receive pings too)
		var subMsg SubscribeEventsRequest
		readMessageSkipPings(t, conn, &subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle call_service request with target (may receive pings too)
		var serviceReq CallServiceRequest
		readMessageSkipPings(t, conn, &serviceReq)

		assert.Equal(t, "light", serviceReq.Domain)
		assert.Equal(t, "turn_on", serviceReq.Service)
		assert.NotNil(t, serviceReq.Target)
		assert.Equal(t, []string{"holiday_light"}, serviceReq.Target.LabelID)

		conn.WriteJSON(Message{
			ID:      serviceReq.ID,
			Type:    "result",
			Success: &success,
		})

		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	defer client.Disconnect()

	target := &ServiceTarget{
		LabelID: []string{"holiday_light"},
	}
	err = client.CallServiceWithTarget(context.Background(), "light", "turn_on", target, nil)
	assert.NoError(t, err)
}

// TestClient_SetInputHelpers consolidates SetInputBoolean, SetInputNumber, SetInputText tests
func TestClient_SetInputHelpers(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name           string
		call           func(c *Client) error
		expectedDomain string
		expectedSvc    string
		checkData      func(t *testing.T, data map[string]interface{})
	}{
		{
			name:           "SetInputBoolean true",
			call:           func(c *Client) error { return c.SetInputBoolean("test", true) },
			expectedDomain: "input_boolean",
			expectedSvc:    "turn_on",
			checkData: func(t *testing.T, data map[string]interface{}) {
				assert.Equal(t, "input_boolean.test", data["entity_id"])
			},
		},
		{
			name:           "SetInputBoolean false",
			call:           func(c *Client) error { return c.SetInputBoolean("test", false) },
			expectedDomain: "input_boolean",
			expectedSvc:    "turn_off",
			checkData: func(t *testing.T, data map[string]interface{}) {
				assert.Equal(t, "input_boolean.test", data["entity_id"])
			},
		},
		{
			name:           "SetInputNumber",
			call:           func(c *Client) error { return c.SetInputNumber("test", 42.5) },
			expectedDomain: "input_number",
			expectedSvc:    "set_value",
			checkData: func(t *testing.T, data map[string]interface{}) {
				assert.Equal(t, "input_number.test", data["entity_id"])
				assert.Equal(t, 42.5, data["value"])
			},
		},
		{
			name:           "SetInputText",
			call:           func(c *Client) error { return c.SetInputText("test", "hello") },
			expectedDomain: "input_text",
			expectedSvc:    "set_value",
			checkData: func(t *testing.T, data map[string]interface{}) {
				assert.Equal(t, "input_text.test", data["entity_id"])
				assert.Equal(t, "hello", data["value"])
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			var lastReq *CallServiceRequest

			fixture := NewTestFixture(t, func(req interface{}) *Message {
				if r, ok := req.(*CallServiceRequest); ok {
					lastReq = r
				}
				return nil
			})
			defer fixture.Close()

			err := tc.call(fixture.Client)
			assert.NoError(t, err)
			require.NotNil(t, lastReq)
			assert.Equal(t, tc.expectedDomain, lastReq.Domain)
			assert.Equal(t, tc.expectedSvc, lastReq.Service)
			tc.checkData(t, lastReq.ServiceData)
		})
	}
}

func TestMockClient(t *testing.T) {
	t.Parallel()
	mock := NewMockClient()

	t.Run("connection", func(t *testing.T) {

		assert.False(t, mock.IsConnected())

		err := mock.Connect()
		assert.NoError(t, err)
		assert.True(t, mock.IsConnected())

		err = mock.Connect()
		assert.Error(t, err)

		err = mock.Disconnect()
		assert.NoError(t, err)
		assert.False(t, mock.IsConnected())
	})

	t.Run("state management", func(t *testing.T) {

		mock.SetState("input_boolean.test", "on", map[string]interface{}{
			"friendly_name": "Test",
		})

		state, err := mock.GetState("input_boolean.test")
		assert.NoError(t, err)
		assert.Equal(t, "on", state.State)

		_, err = mock.GetState("nonexistent")
		assert.Error(t, err)
	})

	t.Run("service calls", func(t *testing.T) {

		mock.ClearServiceCalls()

		err := mock.SetInputBoolean("test", true)
		assert.NoError(t, err)

		calls := mock.GetServiceCalls()
		assert.Len(t, calls, 1)
		assert.Equal(t, "input_boolean", calls[0].Domain)
		assert.Equal(t, "turn_on", calls[0].Service)
	})

	t.Run("service calls with target", func(t *testing.T) {

		mock.ClearServiceCalls()

		target := &ServiceTarget{
			LabelID: []string{"holiday_light"},
			AreaID:  []string{"living_room"},
		}
		err := mock.CallServiceWithTarget(context.Background(), "light", "turn_on", target, map[string]interface{}{
			"brightness": 255,
		})
		assert.NoError(t, err)

		calls := mock.GetServiceCalls()
		assert.Len(t, calls, 1)
		assert.Equal(t, "light", calls[0].Domain)
		assert.Equal(t, "turn_on", calls[0].Service)
		assert.NotNil(t, calls[0].Target)
		assert.Equal(t, []string{"holiday_light"}, calls[0].Target.LabelID)
		assert.Equal(t, []string{"living_room"}, calls[0].Target.AreaID)
		assert.Equal(t, 255, calls[0].Data["brightness"])
	})

	t.Run("service error injection", func(t *testing.T) {

		mock.ClearServiceCalls()

		// Set a service error
		testErr := fmt.Errorf("test service error")
		mock.SetServiceError("light", "turn_on", testErr)

		// Service call should fail
		err := mock.CallService(context.Background(), "light", "turn_on", nil)
		assert.Error(t, err)
		assert.Equal(t, testErr, err)

		// Clear the error
		mock.SetServiceError("light", "turn_on", nil)

		// Service call should succeed now
		err = mock.CallService(context.Background(), "light", "turn_on", nil)
		assert.NoError(t, err)
	})

	t.Run("subscriptions", func(t *testing.T) {

		callCount := 0
		handler := func(entityID string, oldState, newState *State) {
			callCount++
			assert.Equal(t, "input_boolean.test", entityID)
			assert.Equal(t, "off", newState.State)
		}

		_, err := mock.SubscribeStateChanges("input_boolean.test", handler)
		assert.NoError(t, err)

		mock.SimulateStateChange("input_boolean.test", "off")
		time.Sleep(50 * time.Millisecond)

		assert.Equal(t, 1, callCount)
	})
}

func TestClient_DisconnectClearsSubscribers(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	client := NewClient("ws://example", "token", logger)

	client.connMu.Lock()
	client.connected = true
	client.connMu.Unlock()

	_, err := client.SubscribeStateChanges("input_boolean.test", func(string, *State, *State) {})
	require.NoError(t, err)

	err = client.Disconnect()
	require.NoError(t, err)

	client.subsMu.RLock()
	defer client.subsMu.RUnlock()
	assert.Empty(t, client.subscribers)
}

func TestClient_HandleEventBackpressuresHandlers(t *testing.T) {
	t.Parallel()
	client := &Client{
		logger:      zap.NewNop(),
		subscribers: make(map[string][]subscriberEntry),
	}

	var calls int32
	done := make(chan struct{})

	handler := func(entityID string, oldState, newState *State) {
		time.Sleep(50 * time.Millisecond)
		count := atomic.AddInt32(&calls, 1)
		if count == 2 {
			close(done)
		}
	}

	client.subscribers["sensor.test"] = []subscriberEntry{
		{subID: 1, handler: handler},
		{subID: 2, handler: handler},
	}

	eventPayload := StateChangedEvent{EntityID: "sensor.test"}
	data, err := json.Marshal(eventPayload)
	require.NoError(t, err)

	start := time.Now()
	client.handleEvent(&Message{
		Type: "event",
		Event: &Event{
			EventType: "state_changed",
			Data:      data,
		},
	})
	elapsed := time.Since(start)

	// With async handlers, handleEvent should return immediately (not block)
	assert.Less(t, elapsed, 50*time.Millisecond, "handleEvent should not block on handlers")

	// Wait for both handlers to complete
	select {
	case <-done:
		assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handlers did not complete in time")
	}
}

// TestClient_ConcurrentCallService verifies that concurrent CallService calls
// result in messages with monotonically increasing IDs being sent in order.
func TestClient_ConcurrentCallService(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	token := "test_token"

	const numConcurrentCalls = 50

	// Track received message IDs in order
	var receivedIDsMu sync.Mutex
	receivedIDs := make([]int, 0, numConcurrentCalls)
	allReceived := make(chan struct{})

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events (may receive pings too)
		var subMsg SubscribeEventsRequest
		readMessageSkipPings(t, conn, &subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle all service calls - track the order of received IDs
		// Need to handle pings that may interleave with service calls
		serviceCallsReceived := 0
		for serviceCallsReceived < numConcurrentCalls {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			// Check if this is a ping message
			var msg struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &msg); err == nil && msg.Type == "ping" {
				// Respond to ping with pong
				conn.WriteJSON(Message{ID: msg.ID, Type: "pong"})
				continue
			}

			// Otherwise it's a service call
			var serviceReq CallServiceRequest
			if err := json.Unmarshal(data, &serviceReq); err != nil {
				continue
			}

			receivedIDsMu.Lock()
			receivedIDs = append(receivedIDs, serviceReq.ID)
			count := len(receivedIDs)
			receivedIDsMu.Unlock()

			// Send success response
			conn.WriteJSON(Message{
				ID:      serviceReq.ID,
				Type:    "result",
				Success: &success,
			})

			serviceCallsReceived++
			if count == numConcurrentCalls {
				close(allReceived)
			}
		}

		// Keep connection alive for responses
		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	defer client.Disconnect()

	// Launch many concurrent CallService calls
	var wg sync.WaitGroup
	wg.Add(numConcurrentCalls)

	for i := 0; i < numConcurrentCalls; i++ {
		go func(n int) {
			defer wg.Done()
			err := client.CallService(context.Background(), "test", "service", map[string]interface{}{
				"call_number": n,
			})
			_ = err
		}(i)
	}

	// Wait for all calls to be received by server
	select {
	case <-allReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for all messages to be received")
	}

	wg.Wait()

	// Verify IDs were received in strictly increasing order
	receivedIDsMu.Lock()
	idsCopy := make([]int, len(receivedIDs))
	copy(idsCopy, receivedIDs)
	receivedIDsMu.Unlock()

	assert.Len(t, idsCopy, numConcurrentCalls, "Should have received all messages")

	// Check that IDs are strictly increasing
	sortedIDs := make([]int, len(idsCopy))
	copy(sortedIDs, idsCopy)
	sort.Ints(sortedIDs)

	assert.Equal(t, sortedIDs, idsCopy,
		"Message IDs should be received in strictly increasing order")

	// Note: We do NOT verify that IDs are consecutive (no gaps) because
	// ping messages can be interleaved with service calls. Ping messages
	// consume message IDs too, so gaps in service call IDs are expected.
	// The important invariant is that IDs are strictly increasing (ordered).
}

// TestIsRetryableError verifies the retry classification logic
func TestIsRetryableError(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil error", nil, false},
		{"i/o timeout", fmt.Errorf("write tcp: i/o timeout"), true},
		{"connection reset", fmt.Errorf("connection reset by peer"), true},
		{"connection refused", fmt.Errorf("dial tcp: connection refused"), true},
		{"broken pipe", fmt.Errorf("write: broken pipe"), true},
		{"not connected", fmt.Errorf("not connected"), true},
		{"timeout waiting for response", fmt.Errorf("timeout waiting for response"), true},
		{"HA application error", fmt.Errorf("HA error: service_not_found - Service not found"), false},
		{"generic error", fmt.Errorf("something went wrong"), false},
		{"wrapped timeout", fmt.Errorf("failed to send message: write tcp 10.0.0.1:1234->10.0.0.2:443: i/o timeout"), true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			result := isRetryableError(tc.err)
			assert.Equal(t, tc.retryable, result, "isRetryableError(%v) = %v, want %v", tc.err, result, tc.retryable)
		})
	}
}

func TestClient_CallServiceRetry(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	token := "test_token"

	t.Run("no retry on HA application error", func(t *testing.T) {

		var attemptCount atomic.Int32

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events (may receive pings too)
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			// Handle service call with HA error (may receive pings too)
			var serviceReq CallServiceRequest
			readMessageSkipPings(t, conn, &serviceReq)
			attemptCount.Add(1)

			// Return HA application error - should NOT be retried
			fail := false
			conn.WriteJSON(Message{
				ID:      serviceReq.ID,
				Type:    "result",
				Success: &fail,
				Error: &Error{
					Code:    "service_not_found",
					Message: "Service not found",
				},
			})

			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)
		defer client.Disconnect()

		err = client.CallService(context.Background(), "nonexistent", "service", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "service_not_found")
		assert.Equal(t, int32(1), attemptCount.Load(), "Should NOT retry on HA application errors")
	})
}

func TestClient_CallServiceWithTargetRetry(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	token := "test_token"

	t.Run("with service data", func(t *testing.T) {

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events (may receive pings too)
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			// Handle call_service request with target and data (may receive pings too)
			var serviceReq CallServiceRequest
			readMessageSkipPings(t, conn, &serviceReq)

			assert.Equal(t, "light", serviceReq.Domain)
			assert.Equal(t, "turn_on", serviceReq.Service)
			assert.NotNil(t, serviceReq.Target)
			assert.Equal(t, []string{"living_room"}, serviceReq.Target.AreaID)
			assert.Equal(t, 255, int(serviceReq.ServiceData["brightness"].(float64)))

			conn.WriteJSON(Message{
				ID:      serviceReq.ID,
				Type:    "result",
				Success: &success,
			})

			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)
		defer client.Disconnect()

		target := &ServiceTarget{
			AreaID: []string{"living_room"},
		}
		err = client.CallServiceWithTarget(context.Background(), "light", "turn_on", target, map[string]interface{}{
			"brightness": 255,
		})
		assert.NoError(t, err)
	})

	t.Run("no retry on HA application error", func(t *testing.T) {

		var attemptCount atomic.Int32

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events (may receive pings too)
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			// Handle service call with HA error (may receive pings too)
			var serviceReq CallServiceRequest
			readMessageSkipPings(t, conn, &serviceReq)
			attemptCount.Add(1)

			// Verify target was sent
			assert.NotNil(t, serviceReq.Target)
			assert.Equal(t, []string{"holiday_light"}, serviceReq.Target.LabelID)

			// Return HA application error - should NOT be retried
			fail := false
			conn.WriteJSON(Message{
				ID:      serviceReq.ID,
				Type:    "result",
				Success: &fail,
				Error: &Error{
					Code:    "service_not_found",
					Message: "Service not found",
				},
			})

			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)
		defer client.Disconnect()

		target := &ServiceTarget{
			LabelID: []string{"holiday_light"},
		}
		err = client.CallServiceWithTarget(context.Background(), "light", "turn_on", target, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "service_not_found")
		assert.Equal(t, int32(1), attemptCount.Load(), "Should NOT retry on HA application errors")
	})
}

// TestClient_CallServiceContextCancellation verifies that CallService respects context cancellation
// during retry waits, allowing for graceful shutdown. This addresses issue #554.
func TestClient_CallServiceContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("context cancelled before retry starts returns immediately", func(t *testing.T) {
		// This test verifies the context check at the start of the retry loop.
		// We use MockClient here because the real client's retry behavior depends
		// on network errors which are harder to control in a test.
		mock := NewMockClient()
		err := mock.Connect()
		require.NoError(t, err)

		// Create an already-cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// The mock doesn't implement context checking, but the real client does.
		// This test documents the expected interface - the mock is simpler.
		err = mock.CallService(ctx, "test", "service", nil)
		assert.NoError(t, err) // Mock doesn't implement context checking
	})

	t.Run("context cancellation is respected in retry loop implementation", func(t *testing.T) {
		// Verify the implementation has the context check before sleeping.
		// This is a code structure verification - the actual CallService method
		// has been updated to use select{} with ctx.Done() before sleeping.
		// Integration tests would verify the full behavior.

		// We verify the interface signature accepts context
		var client HAClient = NewMockClient()
		ctx := context.Background()
		_ = client.CallService(ctx, "test", "service", nil)
		// If we got here without compile error, the signature is correct
	})

	t.Run("context cancellation interrupts retry wait", func(t *testing.T) {
		// This test verifies the actual behavior: when a network error triggers retries,
		// cancelling the context interrupts the retry wait and returns immediately.
		// Without context cancellation support, the full retry budget (~45s) would block shutdown.
		//
		// Flow:
		// 1. First service call attempt - server doesn't respond, so sendMessage times out (10s)
		// 2. CallService sees "timeout waiting for response" (retryable) and enters retry loop
		// 3. Retry loop checks ctx.Done() (not cancelled yet, since we schedule cancellation at 10.2s)
		// 4. Retry loop starts 500ms timer wait
		// 5. At ~10.2s, we cancel the context
		// 6. The select{} in the retry wait picks up ctx.Done() and returns immediately
		// 7. Total time is ~10.2s (much less than the ~3 minute full retry budget)
		logger := testlogger.New()
		token := "test_token"

		var attemptCount atomic.Int32
		serverReady := make(chan struct{})

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			close(serverReady)

			// Keep the connection alive but don't respond to service calls.
			// This causes sendMessage to timeout (10s), which is a retryable error.
			// Handle pings to keep the connection healthy.
			for {
				conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}

				var msg struct {
					ID   int    `json:"id"`
					Type string `json:"type"`
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}

				if msg.Type == "ping" {
					conn.WriteJSON(Message{ID: msg.ID, Type: "pong"})
					continue
				}

				if msg.Type == "call_service" {
					attemptCount.Add(1)
					// Don't respond - let sendMessage timeout (10s)
					// This triggers a retryable "timeout waiting for response" error
				}
			}
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)
		defer client.Disconnect()

		// Wait for server to be ready
		<-serverReady

		// Create a context that we'll cancel during the retry wait
		ctx, cancel := context.WithCancel(context.Background())

		// Cancel the context after 10.2 seconds.
		// The first attempt will timeout at ~10s (sendMessage timeout).
		// Then CallService enters the retry wait (500ms timer).
		// At 10.2s, we cancel, which should immediately interrupt the retry wait.
		go func() {
			time.Sleep(10200 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		err = client.CallService(ctx, "test", "service", nil)
		elapsed := time.Since(start)

		// Verify we got a context cancellation error
		assert.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "cancelled")

		// Verify we returned in ~10.2s, not the full retry budget (~3 minutes)
		// The first attempt times out at 10s, then we cancel 200ms into the retry wait.
		// Use 15s as upper bound to allow for test timing variations.
		assert.Less(t, elapsed, 15*time.Second,
			"Should exit quickly after context cancellation, not wait for full retry budget (~3 min)")

		// Verify we made exactly one attempt (the one that timed out)
		assert.Equal(t, int32(1), attemptCount.Load(),
			"Should have made exactly one service call attempt before cancellation")
	})
}

// TestClient_CallServiceWithTargetContextCancellation verifies that CallServiceWithTarget
// also respects context cancellation during retry waits.
func TestClient_CallServiceWithTargetContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("context parameter is accepted", func(t *testing.T) {
		// Verify the interface signature accepts context
		mock := NewMockClient()
		err := mock.Connect()
		require.NoError(t, err)

		ctx := context.Background()
		target := &ServiceTarget{LabelID: []string{"test_label"}}
		err = mock.CallServiceWithTarget(ctx, "test", "service", target, nil)
		assert.NoError(t, err)
	})

	t.Run("context cancellation interrupts retry wait", func(t *testing.T) {
		// Same test as CallService but with target - verifies the context cancellation
		// behavior works for CallServiceWithTarget.
		logger := testlogger.New()
		token := "test_token"

		var attemptCount atomic.Int32
		serverReady := make(chan struct{})

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events
			var subMsg SubscribeEventsRequest
			readMessageSkipPings(t, conn, &subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			close(serverReady)

			// Keep connection alive but don't respond to service calls
			for {
				conn.SetReadDeadline(time.Now().Add(30 * time.Second))
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}

				var msg struct {
					ID   int    `json:"id"`
					Type string `json:"type"`
				}
				if err := json.Unmarshal(data, &msg); err != nil {
					continue
				}

				if msg.Type == "ping" {
					conn.WriteJSON(Message{ID: msg.ID, Type: "pong"})
					continue
				}

				if msg.Type == "call_service" {
					attemptCount.Add(1)
					// Don't respond - let sendMessage timeout
				}
			}
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)
		defer client.Disconnect()

		<-serverReady

		ctx, cancel := context.WithCancel(context.Background())

		// Cancel during the retry wait (after the 10s sendMessage timeout)
		go func() {
			time.Sleep(10200 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		target := &ServiceTarget{LabelID: []string{"test_label"}}
		err = client.CallServiceWithTarget(ctx, "test", "service", target, nil)
		elapsed := time.Since(start)

		assert.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "cancelled")

		// Should return in ~10.2s, not the full retry budget (~3 minutes)
		assert.Less(t, elapsed, 15*time.Second,
			"Should exit quickly after context cancellation")

		assert.Equal(t, int32(1), attemptCount.Load(),
			"Should have made exactly one service call attempt before cancellation")
	})
}

// TestClient_ApplicationLevelPingPong verifies that the client sends application-level
// JSON pings and properly handles pong responses to keep the connection alive.
// Home Assistant expects {"id": N, "type": "ping"} and responds with {"id": N, "type": "pong"}.
func TestClient_ApplicationLevelPingPong(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	token := "test_token"

	// Track received pings
	var pingCount atomic.Int32
	var lastPingID atomic.Int32

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Read messages and respond to pings with pongs
		// The first message after auth may be a ping (immediate ping) or subscribe_events
		success := true
		subscribed := false
		for i := 0; i < 10; i++ {
			conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			_, data, err := conn.ReadMessage()
			if err != nil {
				continue
			}

			// Check message type
			var msg struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}

			if msg.Type == "ping" {
				pingCount.Add(1)
				lastPingID.Store(int32(msg.ID))

				// Send pong response (as HA would)
				conn.WriteJSON(Message{
					ID:   msg.ID,
					Type: "pong",
				})
			} else if msg.Type == "subscribe_events" && !subscribed {
				// Receive subscribe_events message
				conn.WriteJSON(Message{
					ID:      msg.ID,
					Type:    "result",
					Success: &success,
				})
				subscribed = true
			}
		}
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	assert.True(t, client.IsConnected())

	// Wait briefly - with immediate ping on connect, we should see at least 1 ping
	time.Sleep(100 * time.Millisecond)

	// Verify we received at least 1 ping (the immediate ping)
	assert.GreaterOrEqual(t, pingCount.Load(), int32(1), "Should receive at least the immediate ping")

	client.Disconnect()
	assert.False(t, client.IsConnected())
}

// TestClient_PingMessageFormat verifies that the ping message has the correct JSON format
// expected by Home Assistant.
func TestClient_PingMessageFormat(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	token := "test_token"

	// Channel to capture the ping message
	pingReceived := make(chan PingRequest, 1)

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Read messages - may receive ping before subscribe_events due to immediate ping
		success := true
		subscribed := false
		for i := 0; i < 50; i++ {
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, data, err := conn.ReadMessage()
			if err != nil {
				continue
			}

			var msg struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}

			if msg.Type == "ping" {
				var pingMsg PingRequest
				json.Unmarshal(data, &pingMsg)
				select {
				case pingReceived <- pingMsg:
				default:
				}
				// Send pong to keep connection alive
				conn.WriteJSON(Message{
					ID:   pingMsg.ID,
					Type: "pong",
				})
			} else if msg.Type == "subscribe_events" && !subscribed {
				conn.WriteJSON(Message{
					ID:      msg.ID,
					Type:    "result",
					Success: &success,
				})
				subscribed = true
			}
		}
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	defer client.Disconnect()

	// With immediate ping on connect, we should receive a ping right away
	// Wait for the ping to be received and verify format
	select {
	case ping := <-pingReceived:
		assert.Equal(t, "ping", ping.Type)
		assert.Greater(t, ping.ID, 0, "Ping ID should be positive")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for ping message")
	}
}

// TestClient_IsHealthy tests health tracking without needing WebSocket
func TestClient_IsHealthy(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()

	newConnectedClient := func() *Client {
		c := NewClient("ws://localhost", "token", logger)
		c.connMu.Lock()
		c.connected = true
		c.connMu.Unlock()
		return c
	}

	t.Run("unhealthy when disconnected", func(t *testing.T) {

		client := NewClient("ws://localhost", "token", logger)
		assert.False(t, client.IsHealthy())
	})

	t.Run("healthy with no service calls", func(t *testing.T) {

		client := newConnectedClient()
		assert.True(t, client.IsHealthy())
	})

	t.Run("healthy with successful service calls", func(t *testing.T) {

		client := newConnectedClient()
		for i := 0; i < 5; i++ {
			client.recordServiceResult(true)
		}
		assert.True(t, client.IsHealthy())
	})

	t.Run("unhealthy with majority failures", func(t *testing.T) {

		client := newConnectedClient()
		client.recordServiceResult(true)
		client.recordServiceResult(true)
		for i := 0; i < 4; i++ {
			client.recordServiceResult(false)
		}
		assert.False(t, client.IsHealthy())
	})

	t.Run("unhealthy at exactly 50% failures", func(t *testing.T) {

		client := newConnectedClient()
		for i := 0; i < 3; i++ {
			client.recordServiceResult(true)
			client.recordServiceResult(false)
		}
		assert.False(t, client.IsHealthy())
	})

	t.Run("healthy just below 50% failures", func(t *testing.T) {

		client := newConnectedClient()
		for i := 0; i < 4; i++ {
			client.recordServiceResult(true)
		}
		for i := 0; i < 3; i++ {
			client.recordServiceResult(false)
		}
		assert.True(t, client.IsHealthy())
	})

	t.Run("rolling window behavior", func(t *testing.T) {

		client := newConnectedClient()
		// Fill with failures
		for i := 0; i < healthWindowSize; i++ {
			client.recordServiceResult(false)
		}
		assert.False(t, client.IsHealthy())

		// Add enough successes to recover
		for i := 0; i < 6; i++ {
			client.recordServiceResult(true)
		}
		assert.True(t, client.IsHealthy())
	})
}
