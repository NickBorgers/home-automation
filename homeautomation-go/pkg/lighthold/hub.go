// Package lighthold shares time-limited light ownership between plugins.
package lighthold

import (
	"sort"
	"sync"
	"time"
)

// Hub stores light holds by entity and owner.
type Hub struct {
	mu    sync.Mutex
	holds map[string]map[string]time.Time
}

// NewHub creates an empty light hold hub.
func NewHub() *Hub {
	return &Hub{holds: make(map[string]map[string]time.Time)}
}

// Hold keeps entityID on for owner until the supplied time. A later call by
// the same owner replaces its previous end time.
func (h *Hub) Hold(entityID, owner string, until time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holds[entityID] == nil {
		h.holds[entityID] = make(map[string]time.Time)
	}
	h.holds[entityID][owner] = until
}

// Release removes owner's hold on entityID.
func (h *Hub) Release(entityID, owner string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.holds[entityID], owner)
	if len(h.holds[entityID]) == 0 {
		delete(h.holds, entityID)
	}
}

// Held reports whether entityID has at least one unexpired hold.
func (h *Hub) Held(entityID string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune(now)
	return len(h.holds[entityID]) > 0
}

// HeldEntities returns the owners of every entity with an unexpired hold.
func (h *Hub) HeldEntities(now time.Time) map[string][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prune(now)
	result := make(map[string][]string, len(h.holds))
	for entityID, owners := range h.holds {
		for owner := range owners {
			result[entityID] = append(result[entityID], owner)
		}
		sort.Strings(result[entityID])
	}
	return result
}

func (h *Hub) prune(now time.Time) {
	for entityID, owners := range h.holds {
		for owner, until := range owners {
			if !until.After(now) {
				delete(owners, owner)
			}
		}
		if len(owners) == 0 {
			delete(h.holds, entityID)
		}
	}
}
