package ha

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// cacheFakeHA is a minimal Home Assistant WebSocket server for state cache
// tests. It counts get_states requests and lets a test push events, delay a
// get_states reply, or drop the connection.
type cacheFakeHA struct {
	t      *testing.T
	server *httptest.Server

	mu     sync.Mutex
	states map[string]*State
	conn   *websocket.Conn

	writeMu sync.Mutex

	getStates   atomic.Int32
	subscribeOK bool

	// beforeStatesReply, if set, runs after the get_states snapshot is taken
	// and before it is written, to simulate events racing the seed.
	beforeStatesReply func()
}

func newCacheFakeHA(t *testing.T, subscribeOK bool, states ...*State) *cacheFakeHA {
	t.Helper()
	f := &cacheFakeHA{t: t, states: make(map[string]*State), subscribeOK: subscribeOK}
	for _, s := range states {
		f.states[s.EntityID] = s
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *cacheFakeHA) url() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http")
}

func (f *cacheFakeHA) write(conn *websocket.Conn, v interface{}) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_ = conn.WriteJSON(v)
}

func (f *cacheFakeHA) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()

	f.write(conn, Message{Type: "auth_required"})
	var auth AuthMessage
	if err := conn.ReadJSON(&auth); err != nil {
		return
	}
	f.write(conn, Message{Type: "auth_ok"})

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		success := true
		switch msg.Type {
		case "ping":
			f.write(conn, Message{ID: msg.ID, Type: "pong"})
		case "subscribe_events":
			ok := f.subscribeOK
			f.write(conn, Message{ID: msg.ID, Type: "result", Success: &ok})
		case "get_states":
			f.getStates.Add(1)
			f.mu.Lock()
			snapshot := make([]*State, 0, len(f.states))
			for _, s := range f.states {
				snapshot = append(snapshot, s)
			}
			hook := f.beforeStatesReply
			f.mu.Unlock()
			result, _ := json.Marshal(snapshot)
			if hook != nil {
				hook()
			}
			f.write(conn, Message{ID: msg.ID, Type: "result", Success: &success, Result: result})
		default:
			f.write(conn, Message{ID: msg.ID, Type: "result", Success: &success})
		}
	}
}

// sendEvent pushes a state_changed event. newState nil means the entity was removed.
func (f *cacheFakeHA) sendEvent(entityID string, newState *State) {
	data, err := json.Marshal(StateChangedEvent{EntityID: entityID, NewState: newState})
	require.NoError(f.t, err)
	f.sendRawEvent(data)
}

func (f *cacheFakeHA) sendRawEvent(data json.RawMessage) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	f.write(conn, Message{Type: "event", Event: &Event{EventType: "state_changed", Data: data}})
}

// setState changes the server's state and, if notify is set, sends the event.
func (f *cacheFakeHA) setState(s *State, notify bool) {
	f.mu.Lock()
	f.states[s.EntityID] = s
	f.mu.Unlock()
	if notify {
		f.sendEvent(s.EntityID, s)
	}
}

func (f *cacheFakeHA) dropConnection() {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	_ = conn.Close()
}

func connectCacheClient(t *testing.T, f *cacheFakeHA) *Client {
	t.Helper()
	client := NewClient(f.url(), "token", zap.NewNop())
	require.NoError(t, client.Connect())
	t.Cleanup(func() { _ = client.Disconnect() })
	return client
}

func st(id, value string, updated time.Time) *State {
	return &State{EntityID: id, State: value, LastChanged: updated, LastUpdated: updated,
		Attributes: map[string]interface{}{}}
}

func TestStateCache_ServesFromCacheOnlyWhenSubscribed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		subscribeOK bool
		wantFetches []int32 // get_states count after each of three GetState calls
		wantStatus  string
	}{
		{"subscribed cache seeds once then serves locally", true, []int32{1, 1, 1}, "valid"},
		{"unsubscribed cache falls back to a live fetch every time", false, []int32{1, 2, 3}, "disconnected"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newCacheFakeHA(t, tc.subscribeOK, st("light.a", "on", time.Now()))
			client := connectCacheClient(t, f)

			for i, want := range tc.wantFetches {
				s, err := client.GetState("light.a")
				require.NoError(t, err)
				assert.Equal(t, "on", s.State)
				assert.Equal(t, want, f.getStates.Load(), "get_states count after call %d", i+1)
			}

			_, err := client.GetState("light.missing")
			assert.Error(t, err, "missing entity is an error whether cached or live")
			assert.Equal(t, tc.wantStatus, client.StateCacheStats().Status)
		})
	}
}

func TestStateCache_EventUpdatesCacheBeforeSubscribersRun(t *testing.T) {
	t.Parallel()
	t0 := time.Now()
	f := newCacheFakeHA(t, true, st("light.a", "off", t0), st("light.gone", "on", t0))
	client := connectCacheClient(t, f)

	_, err := client.GetState("light.a") // seed
	require.NoError(t, err)

	seen := make(chan string, 1)
	_, err = client.SubscribeStateChanges("light.a", func(_ string, _, newState *State) {
		s, err := client.GetState("light.a")
		if err != nil {
			seen <- "error: " + err.Error()
			return
		}
		seen <- s.State
	})
	require.NoError(t, err)

	f.setState(st("light.a", "on", t0.Add(time.Second)), true)
	select {
	case got := <-seen:
		assert.Equal(t, "on", got, "subscriber must see the value from the event that woke it")
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not run")
	}

	// Removed entity: new_state null deletes it from the cache.
	f.sendEvent("light.gone", nil)
	require.Eventually(t, func() bool {
		_, err := client.GetState("light.gone")
		return err != nil
	}, 2*time.Second, 5*time.Millisecond)

	assert.Equal(t, int32(1), f.getStates.Load(), "no further get_states after the seed")
}

func TestStateCache_EventsDuringSeedAreNotOverwrittenBySnapshot(t *testing.T) {
	t.Parallel()
	t0 := time.Now()
	f := newCacheFakeHA(t, true,
		st("light.a", "old", t0),
		st("light.removed", "on", t0),
	)
	// After the snapshot is taken, but before it is sent, the server changes
	// light.a, removes light.removed and adds light.new. The events reach the
	// client ahead of the stale snapshot.
	f.beforeStatesReply = func() {
		f.sendEvent("light.a", st("light.a", "new", t0.Add(time.Second)))
		f.sendEvent("light.removed", nil)
		f.sendEvent("light.new", st("light.new", "on", t0.Add(time.Second)))
	}
	client := connectCacheClient(t, f)

	s, err := client.GetState("light.a")
	require.NoError(t, err)
	assert.Equal(t, "new", s.State, "stale snapshot must not overwrite a newer event")

	_, err = client.GetState("light.removed")
	assert.Error(t, err, "removal during seed must win over the snapshot")

	s, err = client.GetState("light.new")
	require.NoError(t, err)
	assert.Equal(t, "on", s.State)

	assert.Equal(t, int32(1), f.getStates.Load())
	assert.Equal(t, "valid", client.StateCacheStats().Status)
}

func TestStateCache_FinishSeedMerge(t *testing.T) {
	t.Parallel()
	t0 := time.Now()
	tests := []struct {
		name     string
		snapshot *State // nil: entity absent from snapshot
		event    *State // nil: removal event
		hasEvent bool
		want     string // "" means the entity must be absent
	}{
		{"no event keeps snapshot", st("x", "snap", t0), nil, false, "snap"},
		{"newer event beats snapshot", st("x", "snap", t0), st("x", "event", t0.Add(time.Second)), true, "event"},
		{"equal timestamps keep event", st("x", "snap", t0), st("x", "event", t0), true, "event"},
		{"strictly newer snapshot beats older event", st("x", "snap", t0.Add(time.Second)), st("x", "event", t0), true,
			"snap"},
		{"removal beats snapshot", st("x", "snap", t0), nil, true, ""},
		{"event adds entity missing from snapshot", nil, st("x", "event", t0), true, "event"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var sc stateCache
			epoch, _, _ := sc.invalidate(cacheDisconnected)
			require.True(t, sc.markSubscribed(epoch))
			seedEpoch, done, owner := sc.beginSeed()
			require.True(t, owner)
			if tc.hasEvent {
				sc.applyEvent("x", tc.event)
			}
			var snapshot []*State
			if tc.snapshot != nil {
				snapshot = append(snapshot, tc.snapshot)
			}
			applied, _ := sc.finishSeed(seedEpoch, done, snapshot, nil)
			require.True(t, applied)

			got, exists, ok := sc.lookup("x")
			require.True(t, ok)
			if tc.want == "" {
				assert.False(t, exists)
				return
			}
			require.True(t, exists)
			assert.Equal(t, tc.want, got.State)
		})
	}
}

func TestStateCache_SeedFromEarlierEpochIsDiscarded(t *testing.T) {
	t.Parallel()
	var sc stateCache
	epoch, _, _ := sc.invalidate(cacheDisconnected)
	require.True(t, sc.markSubscribed(epoch))
	seedEpoch, done, owner := sc.beginSeed()
	require.True(t, owner)

	// Connection drops and a new one subscribes while the seed is in flight.
	sc.invalidate(cacheDisconnected)
	newEpoch, _, _ := sc.invalidate(cacheDisconnected)
	require.True(t, sc.markSubscribed(newEpoch))
	assert.False(t, sc.markSubscribed(epoch), "stale epoch must not mark a cache subscribed")

	applied, _ := sc.finishSeed(seedEpoch, done, []*State{st("x", "stale", time.Now())}, nil)
	assert.False(t, applied)
	_, _, ok := sc.lookup("x")
	assert.False(t, ok, "cache must not become valid from an old connection's seed")
	select {
	case <-done:
	default:
		t.Fatal("finishSeed must always close done")
	}
	assert.Equal(t, "unseeded", sc.stats().Status)
}

func TestStateCache_DisconnectInvalidatesAndReconnectReseeds(t *testing.T) {
	t.Parallel()
	f := newCacheFakeHA(t, true, st("light.a", "before", time.Now()))
	client := connectCacheClient(t, f)

	s, err := client.GetState("light.a")
	require.NoError(t, err)
	require.Equal(t, "before", s.State)
	require.Equal(t, int32(1), f.getStates.Load())

	// The change happens while no client is subscribed, so no event will ever
	// tell the client about it. Only a re-seed can pick it up.
	f.setState(st("light.a", "after", time.Now()), false)
	f.dropConnection()

	require.Eventually(t, func() bool {
		return client.StateCacheStats().Status != "valid"
	}, 2*time.Second, 5*time.Millisecond, "disconnect must invalidate the cache")

	// Reconnect happens after a 1s backoff.
	require.Eventually(t, func() bool {
		return client.GetReconnectCount() == 1 && client.StateCacheStats().Status == "unseeded"
	}, 5*time.Second, 10*time.Millisecond)

	s, err = client.GetState("light.a")
	require.NoError(t, err)
	assert.Equal(t, "after", s.State, "reconnect must re-seed, not serve the pre-disconnect value")
	assert.Equal(t, int32(2), f.getStates.Load())
	assert.Equal(t, "valid", client.StateCacheStats().Status)
}

func TestStateCache_UndecodableEventInvalidatesAndReseeds(t *testing.T) {
	t.Parallel()
	f := newCacheFakeHA(t, true, st("light.a", "on", time.Now()))
	client := connectCacheClient(t, f)
	_, err := client.GetState("light.a")
	require.NoError(t, err)

	f.sendRawEvent(json.RawMessage(`{"entity_id": 5}`))
	require.Eventually(t, func() bool {
		return client.StateCacheStats().Status == "unseeded"
	}, 2*time.Second, 5*time.Millisecond)

	_, err = client.GetState("light.a")
	require.NoError(t, err)
	assert.Equal(t, int32(2), f.getStates.Load(), "next GetState re-seeds")
	assert.Equal(t, "valid", client.StateCacheStats().Status)
}

func TestStateCache_ConcurrentSeedersShareOneFetch(t *testing.T) {
	t.Parallel()
	f := newCacheFakeHA(t, true, st("light.a", "on", time.Now()))
	release := make(chan struct{})
	f.beforeStatesReply = func() { <-release }
	client := connectCacheClient(t, f)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := client.GetState("light.a")
			if assert.NoError(t, err) {
				assert.Equal(t, "on", s.State)
			}
		}()
	}
	require.Eventually(t, func() bool { return f.getStates.Load() == 1 }, 2*time.Second, time.Millisecond)
	close(release)
	wg.Wait()
	assert.Equal(t, int32(1), f.getStates.Load(), "callers waiting on a seed must not fetch on their own")
}

// seededClient returns a client whose cache is valid, without a network.
func seededClient(t *testing.T, states ...*State) *Client {
	t.Helper()
	client := NewClient("ws://unused", "token", zap.NewNop())
	epoch, _, _ := client.stateCache.invalidate(cacheDisconnected)
	require.True(t, client.stateCache.markSubscribed(epoch))
	seedEpoch, done, owner := client.stateCache.beginSeed()
	require.True(t, owner)
	applied, _ := client.stateCache.finishSeed(seedEpoch, done, states, nil)
	require.True(t, applied)
	return client
}

func eventMessage(t *testing.T, entityID string, newState *State) *Message {
	t.Helper()
	data, err := json.Marshal(StateChangedEvent{EntityID: entityID, NewState: newState})
	require.NoError(t, err)
	return &Message{Type: "event", Event: &Event{EventType: "state_changed", Data: data}}
}

func TestStateCache_ReturnedStateIsACopy(t *testing.T) {
	t.Parallel()
	orig := &State{
		EntityID: "media_player.a",
		State:    "playing",
		Attributes: map[string]interface{}{
			"volume": 0.5,
			"nested": map[string]interface{}{"k": "v"},
			"list":   []interface{}{"one", map[string]interface{}{"deep": "x"}},
		},
		Context: &Context{ID: "ctx"},
	}
	client := seededClient(t, orig)

	got, err := client.GetState("media_player.a")
	require.NoError(t, err)
	got.State = "mutated"
	got.Attributes["volume"] = 1.0
	got.Attributes["nested"].(map[string]interface{})["k"] = "mutated"
	got.Attributes["list"].([]interface{})[0] = "mutated"
	got.Attributes["list"].([]interface{})[1].(map[string]interface{})["deep"] = "mutated"
	got.Context.ID = "mutated"

	again, err := client.GetState("media_player.a")
	require.NoError(t, err)
	assert.Equal(t, "playing", again.State)
	assert.Equal(t, 0.5, again.Attributes["volume"])
	assert.Equal(t, "v", again.Attributes["nested"].(map[string]interface{})["k"])
	assert.Equal(t, "one", again.Attributes["list"].([]interface{})[0])
	assert.Equal(t, "x", again.Attributes["list"].([]interface{})[1].(map[string]interface{})["deep"])
	assert.Equal(t, "ctx", again.Context.ID)

	// A subscriber mutating its newState must not reach the cache either.
	mutated := make(chan struct{})
	_, err = client.SubscribeStateChanges("media_player.a", func(_ string, _, newState *State) {
		newState.Attributes["volume"] = 9.0
		close(mutated)
	})
	require.NoError(t, err)
	client.handleEvent(eventMessage(t, "media_player.a", &State{
		EntityID: "media_player.a", State: "paused", Attributes: map[string]interface{}{"volume": 0.2},
	}))
	<-mutated
	client.WaitForHandlers()
	again, err = client.GetState("media_player.a")
	require.NoError(t, err)
	assert.Equal(t, 0.2, again.Attributes["volume"])
	require.NoError(t, client.Disconnect())
}

func TestStateCache_ConcurrentReadersAndEventWriter(t *testing.T) {
	t.Parallel()
	const entities = 50
	initial := make([]*State, entities)
	for i := range initial {
		initial[i] = &State{EntityID: fmt.Sprintf("sensor.s%d", i), State: "0",
			Attributes: map[string]interface{}{"n": 0.0, "tags": []interface{}{"a"}}}
	}
	client := seededClient(t, initial...)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func(r int) {
			defer readers.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				s, err := client.GetState(fmt.Sprintf("sensor.s%d", (i+r)%entities))
				if err != nil {
					continue // briefly removed by the writer
				}
				s.Attributes["n"] = -1.0 // must not race with the cache or other readers
				s.Attributes["tags"].([]interface{})[0] = "mutated"
			}
		}(r)
	}

	for i := 0; i < 2000; i++ {
		id := fmt.Sprintf("sensor.s%d", i%entities)
		if i%97 == 0 {
			client.handleEvent(eventMessage(t, id, nil))
			continue
		}
		client.handleEvent(eventMessage(t, id, &State{EntityID: id, State: fmt.Sprint(i),
			Attributes: map[string]interface{}{"n": float64(i), "tags": []interface{}{"a"}}}))
	}
	close(stop)
	readers.Wait()

	s, err := client.GetState("sensor.s1")
	require.NoError(t, err)
	assert.Equal(t, "1951", s.State, "last event for sensor.s1 wins")
	assert.Equal(t, "a", s.Attributes["tags"].([]interface{})[0])
}
