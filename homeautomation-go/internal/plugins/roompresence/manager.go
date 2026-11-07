package roompresence

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// MQTT topic filters the plugin reads.
const (
	eventsTopic    = "frigate/events"
	heartbeatTopic = "frigate/+/status/detect"
)

// tickInterval is how often the time-based rules run.
const tickInterval = time.Second

// peopleVars are the state variables counted as people home.
var peopleVars = []string{"isNickHome", "isCarolineHome", "isAssistantHere"}

// Manager is the room presence plugin.
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	cfg          *Config
	reason       string // why the plugin is unconfigured; empty when configured

	shadowTracker *shadowstate.RoomPresenceTracker
	subHelper     *shadowstate.SubscriptionHelper

	// applyMu serializes apply, so output writes reach Home Assistant in the order they were computed.
	// It is taken before mu.
	applyMu sync.Mutex
	// mu guards tracker, written and entryHandlers.
	mu            sync.Mutex
	tracker       *Tracker
	written       map[string]bool // last value written per output, in active mode
	entryHandlers []func(area string)

	mqttSubs []ha.Subscription
	cancel   context.CancelFunc
	done     chan struct{}

	// now is injectable for tests.
	now func() time.Time
}

// NewManager builds a Manager. cfg nil means unconfigured; reason says why.
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, cfg *Config, reason string,
	logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewRoomPresenceTracker()
	m := &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("roompresence"),
		readOnly:      readOnly,
		cfg:           cfg,
		reason:        reason,
		shadowTracker: shadowTracker,
		subHelper: shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "roompresence",
			logger.Named("roompresence")),
		written: make(map[string]bool),
		now:     time.Now,
	}
	if cfg == nil && reason == "" {
		m.reason = "no " + ConfigFileName
	}
	if cfg != nil {
		m.tracker = NewTracker(cfg)
	}
	return m
}

// OnEntry registers a callback for "a token just arrived from outside the area". It runs without the
// manager's lock held. The lux controller can use it for a fast fade-in.
func (m *Manager) OnEntry(handler func(area string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entryHandlers = append(m.entryHandlers, handler)
}

// Start subscribes to Frigate MQTT and the inputs, and starts the timer.
func (m *Manager) Start() error {
	if m.tracker == nil {
		m.logger.Info("Room presence unconfigured", zap.String("reason", m.reason))
		m.publishShadow()
		return nil
	}

	for _, topic := range []string{eventsTopic, heartbeatTopic} {
		sub, err := m.haClient.SubscribeMQTT(topic, m.handleMQTT)
		if err != nil {
			// Without MQTT the plugin cannot see anyone. Stay up but do nothing.
			m.unsubscribeMQTT()
			m.mu.Lock()
			m.tracker = nil
			m.reason = "MQTT subscribe failed: " + err.Error()
			m.mu.Unlock()
			m.logger.Error("Room presence unconfigured: MQTT subscribe failed", zap.Error(err))
			m.publishShadow()
			return nil
		}
		m.mqttSubs = append(m.mqttSubs, sub)
	}

	for _, key := range append(append([]string(nil), peopleVars...), "isAnyoneHome", "isHaveGuests") {
		if err := m.subHelper.SubscribeToState(key, func(string, interface{}, interface{}) { m.updatePeople() }); err != nil {
			m.logger.Warn("Cannot subscribe to state variable", zap.String("key", key), zap.Error(err))
		}
	}
	for _, name := range sortedKeys(m.cfg.Areas) {
		area := name
		for _, entity := range m.cfg.Areas[name].BedSensors {
			err := m.subHelper.SubscribeToEntity(entity, func(_ string, _, newState *ha.State) {
				if newState != nil && newState.State == "on" {
					m.apply(func(t *Tracker, now time.Time) { t.BedOccupied(now, area) })
				}
			})
			if err != nil {
				m.logger.Warn("Cannot subscribe to bed sensor", zap.String("entity_id", entity), zap.Error(err))
			}
		}
	}
	m.subHelper.CaptureInitialInputs()
	// SubscribeToEntity is change-only. Apply the current bed sensor state at startup so a sensor that
	// is already on before the plugin starts still occupies the area.
	for _, name := range sortedKeys(m.cfg.Areas) {
		area := name
		for _, entity := range m.cfg.Areas[name].BedSensors {
			if s, err := m.haClient.GetState(entity); err == nil && s != nil && s.State == "on" {
				m.apply(func(t *Tracker, now time.Time) { t.BedOccupied(now, area) })
			}
		}
	}
	m.updatePeople()

	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	go m.tickLoop(ctx)

	m.logger.Info("Room presence started",
		zap.String("mode", m.cfg.Mode),
		zap.Bool("read_only", m.readOnly),
		zap.Int("areas", len(m.cfg.Areas)),
		zap.Int("outputs", len(m.cfg.Outputs)))
	return nil
}

// Stop ends subscriptions and the timer.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
		<-m.done
		m.cancel = nil
	}
	m.unsubscribeMQTT()
	m.subHelper.UnsubscribeAll()
}

func (m *Manager) unsubscribeMQTT() {
	for _, s := range m.mqttSubs {
		_ = s.Unsubscribe()
	}
	m.mqttSubs = nil
}

// GetShadowState returns the latest shadow state snapshot.
func (m *Manager) GetShadowState() *shadowstate.RoomPresenceShadowState {
	return m.shadowTracker.GetState()
}

// Occupied reports whether an area holds a token. It is false when unconfigured.
func (m *Manager) Occupied(area string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tracker != nil && m.tracker.Occupied(area)
}

func (m *Manager) tickLoop(ctx context.Context) {
	defer close(m.done)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.apply(func(t *Tracker, now time.Time) { t.Tick(now) })
		}
	}
}

// handleMQTT applies one Frigate message.
func (m *Manager) handleMQTT(msg ha.MQTTMessage) {
	if msg.Resubscribed {
		m.logger.Info("Home Assistant reconnected; ending open tracks")
		m.apply(func(t *Tracker, now time.Time) { t.Reconnected(now) })
		return
	}
	if msg.Topic == eventsTopic {
		var ev FrigateEvent
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			m.logger.Warn("Cannot parse Frigate event", zap.Error(err))
			return
		}
		m.apply(func(t *Tracker, now time.Time) { t.HandleEvent(now, ev) })
		return
	}
	if cam, ok := heartbeatCamera(msg.Topic); ok && msg.Payload == "online" {
		m.apply(func(t *Tracker, now time.Time) { t.Heartbeat(now, cam) })
	}
}

// heartbeatCamera reads the camera from frigate/<camera>/status/detect.
func heartbeatCamera(topic string) (string, bool) {
	parts := strings.Split(topic, "/")
	if len(parts) == 4 && parts[0] == "frigate" && parts[2] == "status" && parts[3] == "detect" {
		return parts[1], true
	}
	return "", false
}

// updatePeople reads who is home and passes it to the tracker.
func (m *Manager) updatePeople() {
	count := 0
	for _, key := range peopleVars {
		if v, err := m.stateManager.GetBool(key); err == nil && v {
			count++
		}
	}
	guests, _ := m.stateManager.GetBool("isHaveGuests")
	anyone, err := m.stateManager.GetBool("isAnyoneHome")
	if err != nil {
		anyone = count > 0
	}
	m.apply(func(t *Tracker, now time.Time) { t.SetPeople(now, count, guests, anyone) })
}

// apply runs one tracker operation, then writes outputs, publishes shadow state and signals entries.
func (m *Manager) apply(op func(t *Tracker, now time.Time)) {
	m.applyMu.Lock()
	m.mu.Lock()
	if m.tracker == nil {
		m.mu.Unlock()
		m.applyMu.Unlock()
		return
	}
	op(m.tracker, m.now())
	entries := m.tracker.DrainEntries()
	outputs := m.tracker.Outputs()
	snapshot := m.tracker.Snapshot()
	handlers := append([]func(string){}, m.entryHandlers...)
	writes := m.pendingWrites(outputs)
	m.mu.Unlock()

	for _, w := range writes {
		if err := m.stateManager.SetBool(w.key, w.value); err != nil {
			m.logger.Error("Cannot write output", zap.String("key", w.key), zap.Error(err))
			m.mu.Lock()
			delete(m.written, w.key) // try again on the next change
			m.mu.Unlock()
			continue
		}
		m.logger.Info("Room presence output", zap.String("key", w.key), zap.Bool("value", w.value))
	}

	m.setShadow(snapshot)
	m.applyMu.Unlock()

	for _, area := range entries {
		m.logger.Debug("Person entered area", zap.String("area", area))
		for _, h := range handlers {
			h(area)
		}
	}
}

type write struct {
	key   string
	value bool
}

// pendingWrites lists outputs that changed since they were last written. Only active mode writes, and
// never in read-only mode. The caller holds mu.
func (m *Manager) pendingWrites(outputs map[string]bool) []write {
	if m.cfg.Mode != ModeActive || m.readOnly {
		return nil
	}
	var out []write
	for _, key := range sortedKeys(outputs) {
		v := outputs[key]
		if prev, ok := m.written[key]; ok && prev == v {
			continue
		}
		m.written[key] = v
		out = append(out, write{key: key, value: v})
	}
	return out
}

func (m *Manager) setShadow(o shadowstate.RoomPresenceOutputs) {
	o.Configured = true
	o.Mode = m.cfg.Mode
	o.ReadOnly = m.readOnly
	m.shadowTracker.SetOutputs(o)
}

// publishShadow reports the unconfigured state.
func (m *Manager) publishShadow() {
	m.mu.Lock()
	reason := m.reason
	m.mu.Unlock()
	m.shadowTracker.SetOutputs(shadowstate.RoomPresenceOutputs{
		Configured:   false,
		Reason:       reason,
		Tokens:       []shadowstate.RoomPresenceToken{},
		ActiveTracks: []shadowstate.RoomPresenceTrack{},
		RecentTracks: []shadowstate.RoomPresenceTrack{},
		Removed:      []shadowstate.RoomPresenceRemoval{},
		UnknownZones: []string{},
	})
}
