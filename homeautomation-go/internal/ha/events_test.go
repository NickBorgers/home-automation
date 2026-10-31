package ha

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// eventServer is a fake Home Assistant that records subscribe_events requests and can push events.
type eventServer struct {
	*httptest.Server
	mu         sync.Mutex
	subscribed []string
	conn       *websocket.Conn
	writeMu    sync.Mutex
}

func newEventServer(t *testing.T) *eventServer {
	t.Helper()
	es := &eventServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		es.mu.Lock()
		es.conn = conn
		es.mu.Unlock()

		conn.WriteJSON(Message{Type: "auth_required"})
		var auth AuthMessage
		conn.ReadJSON(&auth)
		conn.WriteJSON(Message{Type: "auth_ok"})

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				ID        int    `json:"id"`
				Type      string `json:"type"`
				EventType string `json:"event_type"`
			}
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			ok := true
			switch req.Type {
			case "ping":
				es.writeMu.Lock()
				conn.WriteJSON(Message{ID: req.ID, Type: "pong"})
				es.writeMu.Unlock()
			case "subscribe_events":
				es.mu.Lock()
				es.subscribed = append(es.subscribed, req.EventType)
				es.mu.Unlock()
				es.writeMu.Lock()
				conn.WriteJSON(Message{ID: req.ID, Type: "result", Success: &ok})
				es.writeMu.Unlock()
			}
		}
	}))
	t.Cleanup(es.Server.Close)
	return es
}

func (es *eventServer) url() string { return "ws" + strings.TrimPrefix(es.URL, "http") }

func (es *eventServer) subscribedTypes() []string {
	es.mu.Lock()
	defer es.mu.Unlock()
	return append([]string(nil), es.subscribed...)
}

func (es *eventServer) push(t *testing.T, eventType string, data string) {
	t.Helper()
	es.mu.Lock()
	conn := es.conn
	es.mu.Unlock()
	require.NotNil(t, conn)
	es.writeMu.Lock()
	defer es.writeMu.Unlock()
	require.NoError(t, conn.WriteJSON(Message{
		Type: "event",
		Event: &Event{
			EventType: eventType,
			Data:      json.RawMessage(data),
			TimeFired: time.Now(),
		},
	}))
}

func TestSubscribeEvents_BeforeConnect(t *testing.T) {
	es := newEventServer(t)
	client := NewClient(es.url(), "tok", zap.NewNop())

	got := make(chan string, 4)
	_, err := client.SubscribeEvents("lutron_caseta_button_event", func(eventType string, data json.RawMessage) {
		got <- eventType + " " + string(data)
	})
	require.NoError(t, err)

	require.NoError(t, client.Connect())
	defer client.Disconnect()

	assert.Contains(t, es.subscribedTypes(), "lutron_caseta_button_event")

	es.push(t, "lutron_caseta_button_event", `{"action":"press"}`)
	es.push(t, "some_other_event", `{"x":1}`)
	es.push(t, "lutron_caseta_button_event", `{"action":"release"}`)

	// Events of one type arrive in order, and events nobody subscribed to are dropped.
	assert.Equal(t, `lutron_caseta_button_event {"action":"press"}`, waitFor(t, got))
	assert.Equal(t, `lutron_caseta_button_event {"action":"release"}`, waitFor(t, got))
	select {
	case extra := <-got:
		t.Fatalf("unexpected event: %s", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSubscribeEvents_AfterConnectSubscribesOncePerType(t *testing.T) {
	es := newEventServer(t)
	client := NewClient(es.url(), "tok", zap.NewNop())
	require.NoError(t, client.Connect())
	defer client.Disconnect()

	first := make(chan string, 4)
	second := make(chan string, 4)
	_, err := client.SubscribeEvents("test_event", func(_ string, data json.RawMessage) { first <- string(data) })
	require.NoError(t, err)
	_, err = client.SubscribeEvents("test_event", func(_ string, data json.RawMessage) { second <- string(data) })
	require.NoError(t, err)

	count := 0
	for _, typ := range es.subscribedTypes() {
		if typ == "test_event" {
			count++
		}
	}
	assert.Equal(t, 1, count, "one server subscription serves both handlers")

	es.push(t, "test_event", `{"n":1}`)
	assert.Equal(t, `{"n":1}`, waitFor(t, first))
	assert.Equal(t, `{"n":1}`, waitFor(t, second))
}

func TestSubscribeEvents_Unsubscribe(t *testing.T) {
	es := newEventServer(t)
	client := NewClient(es.url(), "tok", zap.NewNop())
	require.NoError(t, client.Connect())
	defer client.Disconnect()

	got := make(chan string, 4)
	sub, err := client.SubscribeEvents("test_event", func(_ string, data json.RawMessage) { got <- string(data) })
	require.NoError(t, err)
	require.NoError(t, sub.Unsubscribe())

	es.push(t, "test_event", `{"n":1}`)
	select {
	case v := <-got:
		t.Fatalf("handler ran after Unsubscribe: %s", v)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSubscribeEvents_RejectsStateChanged(t *testing.T) {
	client := NewClient("ws://unused", "tok", zap.NewNop())
	_, err := client.SubscribeEvents("state_changed", func(string, json.RawMessage) {})
	assert.Error(t, err)
	_, err = client.SubscribeEvents("", func(string, json.RawMessage) {})
	assert.Error(t, err)
}

func waitFor(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return ""
	}
}
