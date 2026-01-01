package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestFixture provides a connected client with a mock WebSocket server.
// It handles authentication and event subscription automatically.
type TestFixture struct {
	t         *testing.T
	Server    *httptest.Server
	Client    *Client
	conn      *websocket.Conn
	token     string
	requestCh chan interface{}
	done      chan struct{}
}

// RequestHandler processes a request and returns a response message.
// Return nil to use default success response.
type RequestHandler func(req interface{}) *Message

// NewTestFixture creates a connected client fixture ready for testing.
// The handler is called for each request after auth and subscribe_events.
func NewTestFixture(t *testing.T, handler RequestHandler) *TestFixture {
	t.Helper()
	token := "test_token"

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	fixture := &TestFixture{
		t:         t,
		token:     token,
		requestCh: make(chan interface{}, 10),
		done:      make(chan struct{}),
	}

	fixture.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()
		fixture.conn = conn

		// Auth flow
		conn.WriteJSON(Message{Type: "auth_required"})
		var authMsg AuthMessage
		conn.ReadJSON(&authMsg)
		conn.WriteJSON(Message{Type: "auth_ok"})

		// Subscribe events
		var subMsg SubscribeEventsRequest
		conn.ReadJSON(&subMsg)
		success := true
		conn.WriteJSON(Message{ID: subMsg.ID, Type: "result", Success: &success})

		// Handle requests until done
		for {
			select {
			case <-fixture.done:
				return
			default:
			}

			conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			_, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					return
				}
				// Timeout is expected, continue
				continue
			}

			// Parse message type
			var msg struct {
				ID   int    `json:"id"`
				Type string `json:"type"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}

			var req interface{}
			switch msg.Type {
			case "call_service":
				var r CallServiceRequest
				json.Unmarshal(data, &r)
				req = &r
			case "get_states":
				var r GetStatesRequest
				json.Unmarshal(data, &r)
				req = &r
			case "ping":
				// Respond to application-level pings with pongs
				conn.WriteJSON(Message{ID: msg.ID, Type: "pong"})
				continue
			default:
				continue
			}

			// Non-blocking send to request channel
			select {
			case fixture.requestCh <- req:
			default:
			}

			// Get response from handler
			var resp *Message
			if handler != nil {
				resp = handler(req)
			}
			if resp == nil {
				resp = SuccessResponse()
			}
			resp.ID = msg.ID
			conn.WriteJSON(resp)
		}
	}))

	url := "ws" + strings.TrimPrefix(fixture.Server.URL, "http")
	fixture.Client = NewClient(url, token, zap.NewNop())

	err := fixture.Client.Connect()
	require.NoError(t, err)

	return fixture
}

// Close shuts down the fixture
func (f *TestFixture) Close() {
	close(f.done)
	f.Client.Disconnect()
	f.Server.Close()
}

// SuccessResponse returns a simple success response
func SuccessResponse() *Message {
	success := true
	return &Message{Type: "result", Success: &success}
}

// StatesResponse returns a get_states response with the given states
func StatesResponse(states []*State) *Message {
	success := true
	data, _ := json.Marshal(states)
	return &Message{Type: "result", Success: &success, Result: data}
}

// ErrorResponse returns an error response
func ErrorResponse(code, message string) *Message {
	success := false
	return &Message{
		Type:    "result",
		Success: &success,
		Error:   &Error{Code: code, Message: message},
	}
}

// WaitForRequest waits for a request with timeout
func (f *TestFixture) WaitForRequest(timeout time.Duration) interface{} {
	select {
	case req := <-f.requestCh:
		return req
	case <-time.After(timeout):
		f.t.Fatal("Timeout waiting for request")
		return nil
	}
}
