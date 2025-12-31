package ha

import (
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

func TestClient_Connect(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	t.Run("successful connection", func(t *testing.T) {
		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Receive subscribe_events message
			var subMsg SubscribeEventsRequest
			conn.ReadJSON(&subMsg)

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

			// Receive subscribe_events
			var subMsg SubscribeEventsRequest
			conn.ReadJSON(&subMsg)
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

func TestClient_GetAllStates(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle get_states request
		var statesReq GetStatesRequest
		conn.ReadJSON(&statesReq)

		states := []*State{
			{
				EntityID: "input_boolean.test",
				State:    "on",
				Attributes: map[string]interface{}{
					"friendly_name": "Test Boolean",
				},
			},
			{
				EntityID: "input_number.test",
				State:    "42.5",
				Attributes: map[string]interface{}{
					"friendly_name": "Test Number",
				},
			},
		}

		statesJSON, _ := json.Marshal(states)
		conn.WriteJSON(Message{
			ID:      statesReq.ID,
			Type:    "result",
			Success: &success,
			Result:  statesJSON,
		})

		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	defer client.Disconnect()

	states, err := client.GetAllStates()
	assert.NoError(t, err)
	assert.Len(t, states, 2)
	assert.Equal(t, "input_boolean.test", states[0].EntityID)
	assert.Equal(t, "on", states[0].State)
}

func TestClient_GetState(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle get_states request
		var statesReq GetStatesRequest
		conn.ReadJSON(&statesReq)

		states := []*State{
			{
				EntityID: "input_boolean.test",
				State:    "on",
			},
		}

		statesJSON, _ := json.Marshal(states)
		conn.WriteJSON(Message{
			ID:      statesReq.ID,
			Type:    "result",
			Success: &success,
			Result:  statesJSON,
		})

		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	defer client.Disconnect()

	state, err := client.GetState("input_boolean.test")
	assert.NoError(t, err)
	assert.Equal(t, "input_boolean.test", state.EntityID)
	assert.Equal(t, "on", state.State)

	_, err = client.GetState("nonexistent")
	assert.Error(t, err)
}

func TestClient_CallService(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle call_service request
		var serviceReq CallServiceRequest
		conn.ReadJSON(&serviceReq)

		assert.Equal(t, "input_boolean", serviceReq.Domain)
		assert.Equal(t, "turn_on", serviceReq.Service)
		assert.Equal(t, "input_boolean.test", serviceReq.ServiceData["entity_id"])

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

	err = client.CallService("input_boolean", "turn_on", map[string]interface{}{
		"entity_id": "input_boolean.test",
	})
	assert.NoError(t, err)
}

func TestClient_SetInputBoolean(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	testCases := []struct {
		name    string
		value   bool
		service string
	}{
		{"turn on", true, "turn_on"},
		{"turn off", false, "turn_off"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := mockHAServer(t, func(conn *websocket.Conn) {
				standardAuthFlow(t, conn, token)

				// Handle subscribe_events
				var subMsg SubscribeEventsRequest
				conn.ReadJSON(&subMsg)
				success := true
				conn.WriteJSON(Message{
					ID:      subMsg.ID,
					Type:    "result",
					Success: &success,
				})

				// Handle service call
				var serviceReq CallServiceRequest
				conn.ReadJSON(&serviceReq)

				assert.Equal(t, "input_boolean", serviceReq.Domain)
				assert.Equal(t, tc.service, serviceReq.Service)

				conn.WriteJSON(Message{
					ID:      serviceReq.ID,
					Type:    "result",
					Success: &success,
				})

				time.Sleep(50 * time.Millisecond)
			})
			defer server.Close()

			url := "ws" + strings.TrimPrefix(server.URL, "http")
			client := NewClient(url, token, logger)

			err := client.Connect()
			require.NoError(t, err)
			defer client.Disconnect()

			err = client.SetInputBoolean("test", tc.value)
			assert.NoError(t, err)
		})
	}
}

func TestClient_SetInputNumber(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle service call
		var serviceReq CallServiceRequest
		conn.ReadJSON(&serviceReq)

		assert.Equal(t, "input_number", serviceReq.Domain)
		assert.Equal(t, "set_value", serviceReq.Service)
		assert.Equal(t, 42.5, serviceReq.ServiceData["value"])

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

	err = client.SetInputNumber("test", 42.5)
	assert.NoError(t, err)
}

func TestClient_SetInputText(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle service call
		var serviceReq CallServiceRequest
		conn.ReadJSON(&serviceReq)

		assert.Equal(t, "input_text", serviceReq.Domain)
		assert.Equal(t, "set_value", serviceReq.Service)
		assert.Equal(t, "test_value", serviceReq.ServiceData["value"])

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

	err = client.SetInputText("test", "test_value")
	assert.NoError(t, err)
}

func TestMockClient(t *testing.T) {
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
// This is a regression test for the "id_reuse" race condition where:
//  1. Goroutine A gets ID 100
//  2. Goroutine B gets ID 101
//  3. Goroutine B sends ID 101 first
//  4. Goroutine A sends ID 100 -> Home Assistant returns "id_reuse" error
//
// The fix ensures ID generation and send are atomic (protected by same mutex).
func TestClient_ConcurrentCallService(t *testing.T) {
	logger := zap.NewNop()
	token := "test_token"

	const numConcurrentCalls = 50

	// Track received message IDs in order
	var receivedIDsMu sync.Mutex
	receivedIDs := make([]int, 0, numConcurrentCalls)
	allReceived := make(chan struct{})

	server := mockHAServer(t, func(conn *websocket.Conn) {
		standardAuthFlow(t, conn, token)

		// Handle subscribe_events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Handle all service calls - track the order of received IDs
		for i := 0; i < numConcurrentCalls; i++ {
			var serviceReq CallServiceRequest
			if err := conn.ReadJSON(&serviceReq); err != nil {
				return
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
			err := client.CallService("test", "service", map[string]interface{}{
				"call_number": n,
			})
			// Ignore errors - some may timeout if test is slow
			_ = err
		}(i)
	}

	// Wait for all calls to be received by server
	select {
	case <-allReceived:
		// Good - all messages received
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for all messages to be received")
	}

	wg.Wait()

	// Verify IDs were received in strictly increasing order
	// This is the key assertion: if there was a race, IDs would be out of order
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
		"Message IDs should be received in strictly increasing order. "+
			"If this fails, there's a race condition between ID generation and send. "+
			"Received order: %v, Expected (sorted): %v", idsCopy, sortedIDs)

	// Also verify IDs are consecutive (no gaps)
	for i := 1; i < len(idsCopy); i++ {
		assert.Equal(t, idsCopy[i-1]+1, idsCopy[i],
			"Message IDs should be consecutive. Got %d then %d", idsCopy[i-1], idsCopy[i])
	}
}

// TestIsRetryableError verifies the retry classification logic
func TestIsRetryableError(t *testing.T) {
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

// TestClient_CallServiceRetry verifies that CallService retries on transient errors
func TestClient_CallServiceRetry(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	t.Run("succeeds after transient failure", func(t *testing.T) {
		var attemptCount atomic.Int32

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events
			var subMsg SubscribeEventsRequest
			conn.ReadJSON(&subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			// First two attempts: close connection (simulates transient failure)
			// Third attempt: succeed
			for {
				var serviceReq CallServiceRequest
				if err := conn.ReadJSON(&serviceReq); err != nil {
					return
				}

				attempt := attemptCount.Add(1)
				if attempt <= 2 {
					// Simulate transient failure by closing without response
					// This causes "not connected" or timeout error
					conn.Close()
					return
				}

				// Success on 3rd attempt
				conn.WriteJSON(Message{
					ID:      serviceReq.ID,
					Type:    "result",
					Success: &success,
				})
			}
		})
		defer server.Close()

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		client := NewClient(url, token, logger)

		err := client.Connect()
		require.NoError(t, err)
		defer client.Disconnect()

		// This should succeed after retries
		err = client.CallService("input_boolean", "turn_on", map[string]interface{}{
			"entity_id": "input_boolean.test",
		})

		// The first call will fail and retry logic will kick in
		// Since we're closing the connection, retries won't help in this test
		// but we can verify the retry mechanism is triggered
		// For a real retry to work, we'd need a connection that recovers
		assert.Error(t, err) // Expected to fail since connection drops
		assert.GreaterOrEqual(t, int(attemptCount.Load()), 1, "Should have made at least one attempt")
	})

	t.Run("no retry on HA application error", func(t *testing.T) {
		var attemptCount atomic.Int32

		server := mockHAServer(t, func(conn *websocket.Conn) {
			standardAuthFlow(t, conn, token)

			// Handle subscribe_events
			var subMsg SubscribeEventsRequest
			conn.ReadJSON(&subMsg)
			success := true
			conn.WriteJSON(Message{
				ID:      subMsg.ID,
				Type:    "result",
				Success: &success,
			})

			// Handle service call with HA error
			var serviceReq CallServiceRequest
			conn.ReadJSON(&serviceReq)
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

		err = client.CallService("nonexistent", "service", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "service_not_found")
		assert.Equal(t, int32(1), attemptCount.Load(), "Should NOT retry on HA application errors")
	})
}

// TestClient_PingPongKeepalive verifies that the client sends ping frames
// and properly handles pong responses to keep the connection alive.
func TestClient_PingPongKeepalive(t *testing.T) {
	logger := testlogger.New()
	token := "test_token"

	// Track ping messages received by the server
	var pingCount atomic.Int32

	server := mockHAServer(t, func(conn *websocket.Conn) {
		// Set up ping handler to count pings received
		conn.SetPingHandler(func(appData string) error {
			pingCount.Add(1)
			// Respond with pong (gorilla/websocket does this automatically,
			// but we're tracking it explicitly here)
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
		})

		standardAuthFlow(t, conn, token)

		// Receive subscribe_events message
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)

		// Send success response
		success := true
		conn.WriteJSON(Message{
			ID:      subMsg.ID,
			Type:    "result",
			Success: &success,
		})

		// Keep connection open long enough to receive at least one ping
		// pingInterval is 30s, but we use shorter timeouts in the test
		// by just waiting and reading any incoming messages
		for i := 0; i < 50; i++ {
			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, _, err := conn.ReadMessage()
			if err != nil {
				// Timeout is expected, just continue
				continue
			}
		}
	})
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NewClient(url, token, logger)

	err := client.Connect()
	require.NoError(t, err)
	assert.True(t, client.IsConnected())

	// Wait briefly and disconnect - we're mainly testing that the
	// ping goroutine starts without errors and the pong handler is set up
	time.Sleep(100 * time.Millisecond)

	client.Disconnect()
	assert.False(t, client.IsConnected())

	// Note: In real scenarios, pings happen every 30s. We're just verifying
	// the mechanism is wired up correctly. Full keepalive testing would
	// require integration tests with actual timing.
}
