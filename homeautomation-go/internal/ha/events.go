package ha

import (
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

// eventSubscriberEntry holds an event handler with its unique subscription ID.
type eventSubscriberEntry struct {
	subID   int
	handler EventHandler
}

// customEventJob is one event waiting for its handlers.
type customEventJob struct {
	eventType string
	data      json.RawMessage
	entries   []eventSubscriberEntry
}

// eventSubscription implements Subscription for SubscribeEvents.
type eventSubscription struct {
	eventType string
	subID     int
	client    *Client
}

// Unsubscribe stops delivery to this handler. Home Assistant keeps sending the event type until
// the connection ends. The client drops events that have no handler.
func (s *eventSubscription) Unsubscribe() error {
	s.client.eventSubsMu.Lock()
	defer s.client.eventSubsMu.Unlock()

	entries := s.client.eventSubs[s.eventType]
	for i, e := range entries {
		if e.subID == s.subID {
			s.client.eventSubs[s.eventType] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	return nil
}

// SubscribeEvents registers a handler for one event type other than state_changed.
// It may be called before Connect. The client subscribes to the event type in Home Assistant
// on every connection, including after a reconnect.
func (c *Client) SubscribeEvents(eventType string, handler EventHandler) (Subscription, error) {
	if eventType == "" || eventType == "state_changed" {
		return nil, fmt.Errorf("cannot subscribe to event type %q", eventType)
	}

	c.nextSubIDMu.Lock()
	subID := c.nextSubID
	c.nextSubID++
	c.nextSubIDMu.Unlock()

	c.eventSubsMu.Lock()
	c.eventSubs[eventType] = append(c.eventSubs[eventType], eventSubscriberEntry{subID: subID, handler: handler})
	c.eventSubsMu.Unlock()

	sub := &eventSubscription{eventType: eventType, subID: subID, client: c}

	if c.IsConnected() {
		if err := c.subscribeEventType(eventType); err != nil {
			_ = sub.Unsubscribe()
			return nil, err
		}
	}
	return sub, nil
}

// resetEventServerTypes forgets which event types the previous connection subscribed to.
// Connect calls it once per connection, before any subscribe request is sent.
func (c *Client) resetEventServerTypes() {
	c.eventSubsMu.Lock()
	defer c.eventSubsMu.Unlock()
	c.eventServerTypes = make(map[string]bool)
}

// subscribeEventType asks Home Assistant for one event type, once per connection.
func (c *Client) subscribeEventType(eventType string) error {
	c.eventSubsMu.Lock()
	if c.eventServerTypes[eventType] {
		c.eventSubsMu.Unlock()
		return nil
	}
	// Claim the type before sending so a concurrent caller does not subscribe twice.
	c.eventServerTypes[eventType] = true
	c.eventSubsMu.Unlock()

	_, err := c.sendMessage(&SubscribeEventsRequest{Type: "subscribe_events", EventType: eventType})
	if err != nil {
		c.eventSubsMu.Lock()
		delete(c.eventServerTypes, eventType)
		c.eventSubsMu.Unlock()
		return fmt.Errorf("subscribe to %s events: %w", eventType, err)
	}
	return nil
}

// subscribeRegisteredEvents subscribes the new connection to every event type that has a handler.
func (c *Client) subscribeRegisteredEvents() {
	c.eventSubsMu.Lock()
	types := make([]string, 0, len(c.eventSubs))
	for t, entries := range c.eventSubs {
		if len(entries) > 0 {
			types = append(types, t)
		}
	}
	c.eventSubsMu.Unlock()

	for _, t := range types {
		if err := c.subscribeEventType(t); err != nil {
			c.logger.Error("Failed to subscribe to events", zap.String("event_type", t), zap.Error(err))
		}
	}
}

// clearEventSubscribers drops every event handler. Disconnect calls it.
func (c *Client) clearEventSubscribers() {
	c.eventSubsMu.Lock()
	defer c.eventSubsMu.Unlock()
	c.eventSubs = make(map[string][]eventSubscriberEntry)
	c.eventServerTypes = make(map[string]bool)
}

// dispatchEvent queues an event other than state_changed for its handlers.
// One queue per event type keeps events of that type in the order they arrived.
func (c *Client) dispatchEvent(ev *Event) {
	c.eventSubsMu.Lock()
	entries := append([]eventSubscriberEntry(nil), c.eventSubs[ev.EventType]...)
	c.eventSubsMu.Unlock()
	if len(entries) == 0 {
		return
	}

	for range entries {
		c.handlerCount.Add(1)
	}

	// Same rule as handleEvent: never block receiveMessages on a full queue.
	c.entityDispatchMu.Lock()
	defer c.entityDispatchMu.Unlock()
	queue, ok := c.eventQueues[ev.EventType]
	if !ok {
		queue = make(chan customEventJob, entityEventQueueSize)
		c.eventQueues[ev.EventType] = queue
		go c.processEventQueue(queue)
	}
	select {
	case queue <- customEventJob{eventType: ev.EventType, data: ev.Data, entries: entries}:
	default:
		for range entries {
			c.handlerCount.Add(-1)
		}
		c.logger.Warn("event queue full, dropping event", zap.String("event_type", ev.EventType))
	}
}

// processEventQueue runs the handlers of one event, then moves to the next event.
func (c *Client) processEventQueue(queue <-chan customEventJob) {
	for job := range queue {
		var wg sync.WaitGroup
		for _, entry := range job.entries {
			wg.Add(1)
			go func(h EventHandler) {
				defer c.handlerCount.Add(-1)
				defer wg.Done()
				h(job.eventType, job.data)
			}(entry.handler)
		}
		wg.Wait()
		c.signalEventProcessed()
	}
}
