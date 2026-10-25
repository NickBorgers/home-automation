package ha

import (
	"sync"
	"sync/atomic"
)

// stateCache is a local copy of every entity's state, kept current by the
// state_changed events the client already receives. It lets GetState answer
// without a get_states round trip, which returns every entity (~0.9 MB) and
// was being issued once per GetState call.
//
// # States
//
//   - cacheDisconnected: no confirmed state_changed subscription on the current
//     connection (initial state, after any disconnect, or if subscribing
//     failed). Events are ignored and the cache cannot be seeded.
//   - cacheUnseeded: subscribed, but holds no trusted data (fresh connection,
//     failed seed, or an event that could not be decoded). The next GetState
//     claims the seed.
//   - cacheWarming: one GetState caller owns an outstanding seed get_states.
//     Events are recorded in pending instead of entities.
//   - cacheValid: seeded and subscribed. Events are applied to entities and
//     GetState is served from memory.
//
// GetState reads the cache only in cacheValid. In every other state it falls
// back to a live get_states, so a stale answer is never returned.
//
// # Seed/subscribe race
//
// The seed snapshot can be older or newer than events that arrive while it is
// in flight. Warming begins before the get_states request is written, and the
// subscription is already confirmed, so every change the snapshot might miss
// produces an event that is recorded in pending. When the snapshot arrives,
// each pending entity resolves as follows:
//
//   - removal event (new_state null): the entity is deleted.
//   - otherwise the event's state is kept, unless the snapshot copy has a
//     strictly later last_updated. That only happens when the event was older
//     than the snapshot, in which case the newer event is still queued behind
//     it on the socket and will be applied in order once the cache is valid.
//
// Keeping the event by default means a snapshot taken before an event can
// never overwrite it, and no update is lost.
//
// # Epochs
//
// Every invalidation increments epoch. A seed records the epoch it started in,
// and its result is discarded if the epoch changed while the request was in
// flight (for example a disconnect and reconnect). markSubscribed takes the
// epoch from the moment the connection came up, so a subscription confirmed on
// a connection that has since dropped cannot mark a later connection seedable.
//
// # Values and copies
//
// Stored *State values are never mutated after they are stored; an update
// replaces the pointer. Readers receive deep copies (cloneState), because
// Attributes is a map whose values can themselves be maps and slices decoded
// from JSON, and callers are free to modify what they get back.
//
// # Locking
//
// mu is a leaf lock: no other lock is acquired while holding it, and no I/O or
// callback runs under it. It may be taken while connMu is held.
type stateCache struct {
	mu       sync.RWMutex
	status   cacheStatus
	epoch    uint64
	entities map[string]*State // populated only in cacheValid
	pending  map[string]*State // events seen in cacheWarming; nil value = removed
	seedDone chan struct{}     // closed when the current warming attempt ends

	hits      atomic.Int64 // GetState answered from the cache (including "not found")
	fallbacks atomic.Int64 // GetState fell back to a live get_states
	seeds     atomic.Int64 // successful seeds
}

type cacheStatus int

const (
	cacheDisconnected cacheStatus = iota
	cacheUnseeded
	cacheWarming
	cacheValid
)

func (s cacheStatus) String() string {
	switch s {
	case cacheDisconnected:
		return "disconnected"
	case cacheUnseeded:
		return "unseeded"
	case cacheWarming:
		return "warming"
	case cacheValid:
		return "valid"
	default:
		return "unknown"
	}
}

// StateCacheStats reports the state cache's status and counters.
type StateCacheStats struct {
	Status    string
	Entities  int
	Hits      int64
	Fallbacks int64
	Seeds     int64
}

// lookup returns a copy of the cached state. ok is false when the cache is not
// valid, in which case the caller must not trust exists or the state.
func (sc *stateCache) lookup(entityID string) (state *State, exists, ok bool) {
	sc.mu.RLock()
	if sc.status != cacheValid {
		sc.mu.RUnlock()
		return nil, false, false
	}
	stored, exists := sc.entities[entityID]
	sc.mu.RUnlock()
	return cloneState(stored), exists, true
}

// invalidate moves the cache to the given non-valid status, drops all data and
// starts a new epoch. It returns the new epoch, the previous status and how
// many entities were dropped, for logging.
func (sc *stateCache) invalidate(to cacheStatus) (epoch uint64, prev cacheStatus, dropped int) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	prev = sc.status
	dropped = len(sc.entities)
	sc.status = to
	sc.epoch++
	sc.entities = nil
	sc.pending = nil
	return sc.epoch, prev, dropped
}

// markUnseeded drops cached data after an error while keeping the cache
// seedable on the same connection. It does nothing when the cache is already
// disconnected or unseeded, and reports whether it changed anything.
func (sc *stateCache) markUnseeded() (prev cacheStatus, changed bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	prev = sc.status
	if prev != cacheWarming && prev != cacheValid {
		return prev, false
	}
	sc.status = cacheUnseeded
	sc.epoch++
	sc.entities = nil
	sc.pending = nil
	return prev, true
}

// markSubscribed records that the state_changed subscription for the
// connection that started in epoch is confirmed. It is a no-op if anything
// has invalidated the cache since.
func (sc *stateCache) markSubscribed(epoch uint64) bool {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.epoch != epoch || sc.status != cacheDisconnected {
		return false
	}
	sc.status = cacheUnseeded
	return true
}

// beginSeed claims the seed when the cache is unseeded. The owner must call
// finishSeed with the returned epoch and done channel. When another caller
// already owns the seed, owner is false and done is that seed's channel, so
// the caller can wait for it. In any other status done is nil.
func (sc *stateCache) beginSeed() (epoch uint64, done chan struct{}, owner bool) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	switch sc.status {
	case cacheUnseeded:
		sc.status = cacheWarming
		sc.pending = make(map[string]*State)
		sc.seedDone = make(chan struct{})
		return sc.epoch, sc.seedDone, true
	case cacheWarming:
		return 0, sc.seedDone, false
	default:
		return 0, nil, false
	}
}

// finishSeed ends a seed attempt and always closes done. On success, and only
// if the epoch is unchanged, it merges pending events over the snapshot (see
// the type comment) and marks the cache valid. It reports whether the
// snapshot was applied and how many pending events were merged.
func (sc *stateCache) finishSeed(
	epoch uint64, done chan struct{}, states []*State, err error,
) (applied bool, merged int) {
	defer close(done)
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.epoch != epoch || sc.status != cacheWarming {
		return false, 0
	}
	if err != nil {
		sc.status = cacheUnseeded
		sc.pending = nil
		return false, 0
	}

	entities := make(map[string]*State, len(states))
	for _, s := range states {
		if s != nil {
			entities[s.EntityID] = s
		}
	}
	for id, ev := range sc.pending {
		if ev == nil {
			delete(entities, id)
			continue
		}
		if snap, ok := entities[id]; ok && snap.LastUpdated.After(ev.LastUpdated) {
			continue
		}
		entities[id] = ev
	}

	merged = len(sc.pending)
	sc.entities = entities
	sc.pending = nil
	sc.status = cacheValid
	sc.seeds.Add(1)
	return true, merged
}

// applyEvent records one state_changed event. newState nil means the entity
// was removed. The cache keeps newState as is, so the caller must not mutate
// it afterwards or hand it to code that might.
func (sc *stateCache) applyEvent(entityID string, newState *State) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	switch sc.status {
	case cacheValid:
		if newState == nil {
			delete(sc.entities, entityID)
		} else {
			sc.entities[entityID] = newState
		}
	case cacheWarming:
		sc.pending[entityID] = newState
	}
}

func (sc *stateCache) stats() StateCacheStats {
	sc.mu.RLock()
	status, n := sc.status, len(sc.entities)
	sc.mu.RUnlock()
	return StateCacheStats{
		Status:    status.String(),
		Entities:  n,
		Hits:      sc.hits.Load(),
		Fallbacks: sc.fallbacks.Load(),
		Seeds:     sc.seeds.Load(),
	}
}

// cloneState returns a deep copy of s. Attribute values come from JSON, so the
// only containers are map[string]interface{} and []interface{}; everything
// else is an immutable scalar.
func cloneState(s *State) *State {
	if s == nil {
		return nil
	}
	cp := *s
	if s.Attributes != nil {
		cp.Attributes = cloneAttributes(s.Attributes)
	}
	if s.Context != nil {
		ctx := *s.Context
		cp.Context = &ctx
	}
	return &cp
}

func cloneAttributes(m map[string]interface{}) map[string]interface{} {
	cp := make(map[string]interface{}, len(m))
	for k, v := range m {
		cp[k] = cloneValue(v)
	}
	return cp
}

func cloneValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return cloneAttributes(t)
	case []interface{}:
		cp := make([]interface{}, len(t))
		for i, e := range t {
			cp[i] = cloneValue(e)
		}
		return cp
	default:
		return v
	}
}
