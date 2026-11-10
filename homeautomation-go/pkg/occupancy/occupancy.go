// Package occupancy shares room presence between plugins. Room presence publishes the state of each area.
// Other plugins, such as the vacuum scheduler, read it and subscribe to changes.
//
// The hub knows nothing until a publisher fills it. An area it has not heard of, or an area whose cameras
// are offline, is not known. A reader that must keep people safe treats "not known" as occupied.
package occupancy

import (
	"sort"
	"sync"
)

// Status is one area's state.
type Status struct {
	// Occupied is true while the area holds a person.
	Occupied bool
	// Unknown is true while every camera that sees the area is offline.
	Unknown bool
}

// Hub holds the latest area states. The zero value is not usable; use NewHub.
type Hub struct {
	mu     sync.Mutex
	areas  map[string]Status
	subs   map[int]func()
	nextID int
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{areas: map[string]Status{}, subs: map[int]func(){}}
}

// Publish replaces every area's state. Subscribers run, without the hub's lock held, only when something
// changed.
func (h *Hub) Publish(areas map[string]Status) {
	next := make(map[string]Status, len(areas))
	for k, v := range areas {
		next[k] = v
	}
	h.mu.Lock()
	changed := len(next) != len(h.areas)
	if !changed {
		for k, v := range next {
			if old, ok := h.areas[k]; !ok || old != v {
				changed = true
				break
			}
		}
	}
	h.areas = next
	subs := h.subscribers()
	h.mu.Unlock()
	if changed {
		for _, fn := range subs {
			fn()
		}
	}
}

// Clear forgets every area, for example when the publisher stops working. Every area becomes not known.
func (h *Hub) Clear() {
	h.Publish(nil)
}

// Occupied reports an area's state. known is false when the area was never published or its cameras are
// offline; occupied is then false and means nothing.
func (h *Hub) Occupied(area string) (occupied, known bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.areas[area]
	if !ok || s.Unknown {
		return false, false
	}
	return s.Occupied, true
}

// Areas lists the published areas, sorted.
func (h *Hub) Areas() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.areas))
	for k := range h.areas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Subscribe calls fn after each change. fn must not block for long. The returned function unsubscribes.
func (h *Hub) Subscribe(fn func()) (unsubscribe func()) {
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subs[id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
}

// subscribers copies the callbacks in subscription order. The caller holds mu.
func (h *Hub) subscribers() []func() {
	ids := make([]int, 0, len(h.subs))
	for id := range h.subs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]func(), 0, len(ids))
	for _, id := range ids {
		out = append(out, h.subs[id])
	}
	return out
}
