package ha

import (
	"fmt"
	"sync"

	"go.uber.org/zap"
)

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

// MQTTSubscribeRequest is the Home Assistant WebSocket command mqtt/subscribe.
type MQTTSubscribeRequest struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Topic string `json:"topic"`
}

// mqttSubscriberEntry holds an MQTT handler with its unique subscription ID.
type mqttSubscriberEntry struct {
	subID   int
	handler MQTTHandler
}

// mqttJob is one MQTT message waiting for its handlers.
type mqttJob struct {
	msg     MQTTMessage
	entries []mqttSubscriberEntry
}

// mqttSubscription implements Subscription for SubscribeMQTT.
type mqttSubscription struct {
	topic  string
	subID  int
	client *Client
}

// Unsubscribe stops delivery to this handler. Home Assistant keeps sending the topic until the connection
// ends. The client drops messages that have no handler.
func (s *mqttSubscription) Unsubscribe() error {
	s.client.mqttMu.Lock()
	defer s.client.mqttMu.Unlock()

	entries := s.client.mqttSubs[s.topic]
	for i, e := range entries {
		if e.subID == s.subID {
			s.client.mqttSubs[s.topic] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	return nil
}

// SubscribeMQTT registers a handler for an MQTT topic filter, such as "frigate/#". Home Assistant's MQTT
// integration must be connected to the broker. It may be called before Connect. The client subscribes on
// every connection, including after a reconnect, and then sends each handler a message with Resubscribed
// set. Messages on one topic filter reach handlers in the order Home Assistant sent them.
func (c *Client) SubscribeMQTT(topic string, handler MQTTHandler) (Subscription, error) {
	if topic == "" {
		return nil, fmt.Errorf("cannot subscribe to an empty MQTT topic")
	}

	c.nextSubIDMu.Lock()
	subID := c.nextSubID
	c.nextSubID++
	c.nextSubIDMu.Unlock()

	c.mqttMu.Lock()
	c.mqttSubs[topic] = append(c.mqttSubs[topic], mqttSubscriberEntry{subID: subID, handler: handler})
	c.mqttMu.Unlock()

	sub := &mqttSubscription{topic: topic, subID: subID, client: c}

	if c.IsConnected() {
		if err := c.subscribeMQTTTopic(topic); err != nil {
			_ = sub.Unsubscribe()
			return nil, err
		}
	}
	return sub, nil
}

// resetMQTTServerTopics forgets the previous connection's MQTT subscriptions. Connect calls it once per
// connection, before any subscribe request is sent.
func (c *Client) resetMQTTServerTopics() {
	c.mqttMu.Lock()
	defer c.mqttMu.Unlock()
	c.mqttServerTopics = make(map[string]bool)
	c.mqttTopicByMsgID = make(map[int]string)
}

// registerMQTTMsgID records which topic filter a subscribe message ID belongs to. sendMessage calls it
// before the request is written, so a message that arrives before the result is not dropped.
func (c *Client) registerMQTTMsgID(msgID int, topic string) {
	c.mqttMu.Lock()
	defer c.mqttMu.Unlock()
	c.mqttTopicByMsgID[msgID] = topic
}

// subscribeMQTTTopic asks Home Assistant for one topic filter, once per connection.
func (c *Client) subscribeMQTTTopic(topic string) error {
	c.mqttMu.Lock()
	if c.mqttServerTopics[topic] {
		c.mqttMu.Unlock()
		return nil
	}
	// Claim the topic before sending so a concurrent caller does not subscribe twice.
	c.mqttServerTopics[topic] = true
	c.mqttMu.Unlock()

	if _, err := c.sendMessage(&MQTTSubscribeRequest{Type: "mqtt/subscribe", Topic: topic}); err != nil {
		c.mqttMu.Lock()
		delete(c.mqttServerTopics, topic)
		c.mqttMu.Unlock()
		return fmt.Errorf("subscribe to MQTT topic %s: %w", topic, err)
	}
	return nil
}

// subscribeRegisteredMQTT subscribes a new connection to every topic filter that has a handler. On a
// reconnect it then tells each handler that messages may have been missed.
func (c *Client) subscribeRegisteredMQTT(reconnect bool) {
	c.mqttMu.Lock()
	topics := make([]string, 0, len(c.mqttSubs))
	for t, entries := range c.mqttSubs {
		if len(entries) > 0 {
			topics = append(topics, t)
		}
	}
	c.mqttMu.Unlock()

	for _, t := range topics {
		// The marker goes first, so it is queued before any message of the new subscription.
		if reconnect {
			c.dispatchMQTT(t, MQTTMessage{Topic: t, Resubscribed: true})
		}
		if err := c.subscribeMQTTTopic(t); err != nil {
			c.logger.Error("Failed to subscribe to MQTT topic", zap.String("topic", t), zap.Error(err))
		}
	}
}

// clearMQTTSubscribers drops every MQTT handler. Disconnect calls it.
func (c *Client) clearMQTTSubscribers() {
	c.mqttMu.Lock()
	defer c.mqttMu.Unlock()
	c.mqttSubs = make(map[string][]mqttSubscriberEntry)
	c.mqttServerTopics = make(map[string]bool)
	c.mqttTopicByMsgID = make(map[int]string)
}

// handleMQTTEvent routes an event message to MQTT handlers when its ID belongs to an MQTT subscription.
// It reports whether the message was an MQTT message.
func (c *Client) handleMQTTEvent(msg *Message) bool {
	if msg.ID == 0 {
		return false
	}
	c.mqttMu.Lock()
	topic, ok := c.mqttTopicByMsgID[msg.ID]
	c.mqttMu.Unlock()
	if !ok {
		return false
	}
	c.dispatchMQTT(topic, MQTTMessage{
		Topic:   msg.Event.Topic,
		Payload: msg.Event.Payload,
		QoS:     msg.Event.QoS,
		Retain:  msg.Event.Retain,
	})
	return true
}

// dispatchMQTT queues a message for the handlers of one topic filter. One queue per filter keeps its
// messages in the order they arrived.
func (c *Client) dispatchMQTT(topic string, msg MQTTMessage) {
	c.mqttMu.Lock()
	entries := append([]mqttSubscriberEntry(nil), c.mqttSubs[topic]...)
	c.mqttMu.Unlock()
	if len(entries) == 0 {
		return
	}

	for range entries {
		c.handlerCount.Add(1)
	}

	// Same rule as handleEvent: never block receiveMessages on a full queue.
	c.entityDispatchMu.Lock()
	defer c.entityDispatchMu.Unlock()
	queue, ok := c.mqttQueues[topic]
	if !ok {
		queue = make(chan mqttJob, entityEventQueueSize)
		c.mqttQueues[topic] = queue
		go c.processMQTTQueue(queue)
	}
	select {
	case queue <- mqttJob{msg: msg, entries: entries}:
	default:
		for range entries {
			c.handlerCount.Add(-1)
		}
		c.logger.Warn("MQTT queue full, dropping message", zap.String("topic", msg.Topic))
	}
}

// processMQTTQueue runs the handlers of one message, then moves to the next message.
func (c *Client) processMQTTQueue(queue <-chan mqttJob) {
	for job := range queue {
		var wg sync.WaitGroup
		for _, entry := range job.entries {
			wg.Add(1)
			go func(h MQTTHandler) {
				defer c.handlerCount.Add(-1)
				defer wg.Done()
				h(job.msg)
			}(entry.handler)
		}
		wg.Wait()
		c.signalEventProcessed()
	}
}
