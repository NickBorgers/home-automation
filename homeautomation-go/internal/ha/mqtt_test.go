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

// mqttServer is a fake Home Assistant that answers mqtt/subscribe. Like Home Assistant, it can send a
// retained message before the subscribe result.
type mqttServer struct {
	*httptest.Server
	mu       sync.Mutex
	conn     *websocket.Conn
	subIDs   map[string]int // topic filter -> subscribe message ID, for the current connection
	topics   []string       // every mqtt/subscribe received, across connections
	retained map[string]string
	writeMu  sync.Mutex
}

func newMQTTServer(t *testing.T, retained map[string]string) *mqttServer {
	t.Helper()
	ms := &mqttServer{retained: retained}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	ms.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ms.mu.Lock()
		ms.conn = conn
		ms.subIDs = make(map[string]int)
		ms.mu.Unlock()

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
				ID    int    `json:"id"`
				Type  string `json:"type"`
				Topic string `json:"topic"`
			}
			if json.Unmarshal(data, &req) != nil {
				continue
			}
			ok := true
			ms.writeMu.Lock()
			switch req.Type {
			case "ping":
				conn.WriteJSON(Message{ID: req.ID, Type: "pong"})
			case "subscribe_events":
				conn.WriteJSON(Message{ID: req.ID, Type: "result", Success: &ok})
			case "mqtt/subscribe":
				ms.mu.Lock()
				ms.subIDs[req.Topic] = req.ID
				ms.topics = append(ms.topics, req.Topic)
				ms.mu.Unlock()
				for topic, payload := range ms.retained {
					conn.WriteJSON(Message{ID: req.ID, Type: "event",
						Event: &Event{Topic: topic, Payload: payload, Retain: true}})
				}
				conn.WriteJSON(Message{ID: req.ID, Type: "result", Success: &ok})
			}
			ms.writeMu.Unlock()
		}
	}))
	t.Cleanup(ms.Server.Close)
	return ms
}

func (ms *mqttServer) url() string { return "ws" + strings.TrimPrefix(ms.URL, "http") }

func (ms *mqttServer) subscribedTopics() []string {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return append([]string(nil), ms.topics...)
}

// publish sends one message on the subscription for filter.
func (ms *mqttServer) publish(t *testing.T, filter, topic, payload string) {
	t.Helper()
	ms.mu.Lock()
	conn, id := ms.conn, ms.subIDs[filter]
	ms.mu.Unlock()
	require.NotNil(t, conn)
	require.NotZero(t, id, "no subscription for %s", filter)
	ms.writeMu.Lock()
	defer ms.writeMu.Unlock()
	require.NoError(t, conn.WriteJSON(Message{ID: id, Type: "event", Event: &Event{Topic: topic, Payload: payload}}))
}

// dropConnection closes the server side of the current connection.
func (ms *mqttServer) dropConnection() {
	ms.mu.Lock()
	conn := ms.conn
	ms.subIDs = make(map[string]int)
	ms.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func receiveMQTT(t *testing.T, ch <-chan MQTTMessage) MQTTMessage {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for MQTT message")
		return MQTTMessage{}
	}
}

func TestSubscribeMQTT_BeforeConnect_DeliversRetainedAndLiveInOrder(t *testing.T) {
	ms := newMQTTServer(t, map[string]string{"frigate/available": "online"})
	client := NewClient(ms.url(), "tok", zap.NewNop())

	got := make(chan MQTTMessage, 16)
	_, err := client.SubscribeMQTT("frigate/#", func(m MQTTMessage) { got <- m })
	require.NoError(t, err)

	require.NoError(t, client.Connect())
	defer client.Disconnect()
	assert.Equal(t, []string{"frigate/#"}, ms.subscribedTopics())

	// The retained message came before the subscribe result and still reaches the handler.
	first := receiveMQTT(t, got)
	assert.Equal(t, "frigate/available", first.Topic)
	assert.Equal(t, "online", first.Payload)
	assert.True(t, first.Retain)
	assert.False(t, first.Resubscribed)

	for i := 0; i < 20; i++ {
		ms.publish(t, "frigate/#", "frigate/events", string(rune('a'+i)))
	}
	for i := 0; i < 20; i++ {
		m := receiveMQTT(t, got)
		assert.Equal(t, "frigate/events", m.Topic)
		assert.Equal(t, string(rune('a'+i)), m.Payload, "messages must arrive in order")
	}
}

func TestSubscribeMQTT_AfterConnect(t *testing.T) {
	ms := newMQTTServer(t, nil)
	client := NewClient(ms.url(), "tok", zap.NewNop())
	require.NoError(t, client.Connect())
	defer client.Disconnect()

	got := make(chan MQTTMessage, 4)
	_, err := client.SubscribeMQTT("frigate/events", func(m MQTTMessage) { got <- m })
	require.NoError(t, err)

	ms.publish(t, "frigate/events", "frigate/events", `{"type":"new"}`)
	assert.Equal(t, `{"type":"new"}`, receiveMQTT(t, got).Payload)
}

func TestSubscribeMQTT_Unsubscribe(t *testing.T) {
	ms := newMQTTServer(t, nil)
	client := NewClient(ms.url(), "tok", zap.NewNop())
	require.NoError(t, client.Connect())
	defer client.Disconnect()

	var mu sync.Mutex
	count := 0
	sub, err := client.SubscribeMQTT("frigate/#", func(MQTTMessage) {
		mu.Lock()
		count++
		mu.Unlock()
	})
	require.NoError(t, err)
	require.NoError(t, sub.Unsubscribe())

	ms.publish(t, "frigate/#", "frigate/events", "x")
	client.WaitForHandlers()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Zero(t, count)
}

func TestSubscribeMQTT_EmptyTopic(t *testing.T) {
	client := NewClient("ws://unused", "tok", zap.NewNop())
	_, err := client.SubscribeMQTT("", func(MQTTMessage) {})
	assert.Error(t, err)
}

func TestSubscribeMQTT_ReconnectSendsMarkerThenResubscribes(t *testing.T) {
	ms := newMQTTServer(t, nil)
	client := NewClient(ms.url(), "tok", zap.NewNop())

	got := make(chan MQTTMessage, 16)
	_, err := client.SubscribeMQTT("frigate/#", func(m MQTTMessage) { got <- m })
	require.NoError(t, err)
	require.NoError(t, client.Connect())
	defer client.Disconnect()

	ms.publish(t, "frigate/#", "frigate/events", "before")
	assert.Equal(t, "before", receiveMQTT(t, got).Payload)

	ms.dropConnection()

	marker := receiveMQTT(t, got)
	assert.True(t, marker.Resubscribed, "the first message after a reconnect is the marker")
	assert.Equal(t, "frigate/#", marker.Topic)

	require.Eventually(t, func() bool { return len(ms.subscribedTopics()) == 2 }, 5*time.Second, 10*time.Millisecond)
	ms.publish(t, "frigate/#", "frigate/events", "after")
	m := receiveMQTT(t, got)
	assert.Equal(t, "after", m.Payload)
	assert.False(t, m.Resubscribed)
}

func TestSubscribeMQTT_OtherEventsStillRouteByType(t *testing.T) {
	ms := newMQTTServer(t, nil)
	client := NewClient(ms.url(), "tok", zap.NewNop())

	mqttGot := make(chan MQTTMessage, 4)
	_, err := client.SubscribeMQTT("frigate/#", func(m MQTTMessage) { mqttGot <- m })
	require.NoError(t, err)
	evGot := make(chan string, 4)
	_, err = client.SubscribeEvents("lutron_caseta_button_event", func(eventType string, _ json.RawMessage) {
		evGot <- eventType
	})
	require.NoError(t, err)
	require.NoError(t, client.Connect())
	defer client.Disconnect()

	ms.writeMu.Lock()
	ms.mu.Lock()
	conn := ms.conn
	ms.mu.Unlock()
	require.NoError(t, conn.WriteJSON(Message{ID: 999, Type: "event",
		Event: &Event{EventType: "lutron_caseta_button_event", Data: json.RawMessage(`{}`)}}))
	ms.writeMu.Unlock()

	select {
	case et := <-evGot:
		assert.Equal(t, "lutron_caseta_button_event", et)
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
	}
	assert.Empty(t, mqttGot)
}
