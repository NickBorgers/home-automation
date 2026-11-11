package earlywake

import (
	"context"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Manager is the early wake plugin.
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	cfg          *Config
	logger       *zap.Logger
	readOnly     bool

	shadowTracker *shadowstate.EarlyWakeTracker
	registry      *shadowstate.SubscriptionRegistry
	subHelper     *shadowstate.SubscriptionHelper

	// userMu protects appUserID. It stays empty until auth/current_user answers.
	userMu    sync.Mutex
	appUserID string

	// Injectable for tests.
	now func() time.Time
}

// NewManager builds a Manager. It does not subscribe until Start.
func NewManager(
	ctx context.Context,
	haClient ha.HAClient,
	stateManager *state.Manager,
	cfg *Config,
	logger *zap.Logger,
	readOnly bool,
) *Manager {
	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		cfg:           cfg,
		logger:        logger.Named("earlywake"),
		readOnly:      readOnly,
		shadowTracker: shadowstate.NewEarlyWakeTracker(),
		now:           time.Now,
	}
}

// SetSubscriptionRegistry supplies the registry used for automatic shadow input
// capture. Call it before Start. Without it, inputs are captured from the
// plugin's own subscriptions.
func (m *Manager) SetSubscriptionRegistry(registry *shadowstate.SubscriptionRegistry) {
	m.registry = registry
}

// GetShadowState returns the latest shadow state snapshot.
func (m *Manager) GetShadowState() *shadowstate.EarlyWakeShadowState {
	return m.shadowTracker.GetState()
}

// Start learns the app's user ID and subscribes to the wake lights, isAnyoneAsleep, and isMasterAsleep.
//
// If the app's user ID cannot be read, Start still succeeds. Detection then retries
// the lookup on each light change and suppresses all detection until it succeeds,
// because without the ID the app's own commands would look like a person's.
func (m *Manager) Start() error {
	m.subHelper = shadowstate.NewSubscriptionHelper(
		m.haClient, m.stateManager, m.registry, m.shadowTracker, "earlywake", m.logger)

	userID := m.lookupAppUserID()
	m.shadowTracker.SetConfig(userID, m.cfg.WakeLights, m.readOnly)

	for _, light := range m.cfg.WakeLights {
		if err := m.subHelper.SubscribeToEntity(light, m.handleLightChange); err != nil {
			m.subHelper.UnsubscribeAll()
			return err
		}
	}
	if err := m.subHelper.SubscribeToState("isAnyoneAsleep", m.handleAnyoneAsleepChange); err != nil {
		m.subHelper.UnsubscribeAll()
		return err
	}
	if err := m.subHelper.SubscribeToState("isMasterAsleep", m.handleMasterAsleepChange); err != nil {
		m.subHelper.UnsubscribeAll()
		return err
	}
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("Early wake detector started",
		zap.Strings("wake_lights", m.cfg.WakeLights),
		zap.Bool("app_user_known", userID != ""),
		zap.Bool("read_only", m.readOnly))
	return nil
}

// Stop ends all subscriptions.
func (m *Manager) Stop() {
	if m.subHelper != nil {
		m.subHelper.UnsubscribeAll()
	}
}

// lookupAppUserID returns the app's HA user ID, asking Home Assistant if it is not known yet.
// It returns "" if Home Assistant cannot tell.
//
// userMu is held for the entire HA round-trip to serialise concurrent lookups.
// This is safe: handlers run in separate goroutines from the HA event loop, so
// the response message can be delivered while a handler is blocked here.
func (m *Manager) lookupAppUserID() string {
	m.userMu.Lock()
	defer m.userMu.Unlock()
	if m.appUserID != "" {
		return m.appUserID
	}
	id, err := m.haClient.GetCurrentUserID()
	if err != nil || id == "" {
		m.logger.Warn("early wake: cannot read the app's Home Assistant user; manual light detection is paused",
			zap.Error(err))
		return ""
	}
	m.appUserID = id
	m.logger.Info("early wake: learned the app's Home Assistant user", zap.String("app_user_id", id))
	return id
}

// handleLightChange judges one wake light change. Only an off->on turn-on matters.
func (m *Manager) handleLightChange(entityID string, oldState, newState *ha.State) {
	if newState == nil || newState.State != "on" {
		return
	}
	oldValue := "none"
	if oldState != nil {
		oldValue = oldState.State
	}
	// Only off -> on is a turn-on. on -> on is a brightness or color change.
	// unavailable -> on is a bulb coming back after a power cut: Hue bulbs
	// default to on, with no user in the context, so it must not wake the house.
	if oldValue != "off" {
		return
	}

	change := shadowstate.EarlyWakeLightChange{
		At:       m.now(),
		EntityID: entityID,
		OldState: oldValue,
		NewState: newState.State,
	}
	if newState.Context != nil {
		change.ContextUserID = newState.Context.UserID
		change.ContextParent = newState.Context.ParentID
	}

	change.Decision = m.judge(newState.Context)
	change.Manual = change.Decision == ""
	if !change.Manual {
		m.shadowTracker.RecordLightChange(change)
		m.logger.Debug("early wake: light turn-on ignored",
			zap.String("entity_id", entityID),
			zap.String("reason", change.Decision))
		return
	}

	asleep, err := m.stateManager.GetBool("isAnyoneAsleep")
	if err != nil {
		change.Decision = "cannot read isAnyoneAsleep"
		change.Error = err.Error()
		m.shadowTracker.RecordLightChange(change)
		m.logger.Error("early wake: cannot read isAnyoneAsleep", zap.Error(err))
		return
	}
	if !asleep {
		change.Decision = "manual, but nobody is asleep"
		m.shadowTracker.RecordLightChange(change)
		return
	}

	active, err := m.stateManager.GetBool("isEarlyWakeActive")
	if err == nil && active {
		change.Decision = "manual, early wake already on"
		m.shadowTracker.RecordLightChange(change)
		return
	}

	m.shadowTracker.SnapshotInputsForAction()
	if m.readOnly {
		change.Decision = "manual while asleep: would start early wake (read-only)"
		m.shadowTracker.RecordLightChange(change)
		m.logger.Info("READ-ONLY: would start early wake (manual light turn-on while asleep)",
			zap.String("entity_id", entityID),
			zap.String("context_user_id", change.ContextUserID))
		return
	}

	if err := m.stateManager.SetBool("isEarlyWakeActive", true); err != nil {
		change.Decision = "manual while asleep: failed to start early wake"
		change.Error = err.Error()
		m.shadowTracker.RecordLightChange(change)
		m.logger.Error("early wake: failed to set isEarlyWakeActive",
			zap.String("entity_id", entityID), zap.Error(err))
		return
	}
	change.Decision = "manual while asleep: early wake started"
	m.shadowTracker.RecordLightChange(change)
	m.shadowTracker.RecordActivated(change.At, entityID)
	m.logger.Info("early wake started: manual light turn-on while someone is asleep",
		zap.String("entity_id", entityID),
		zap.String("context_user_id", change.ContextUserID))
}

// judge returns why a turn-on is not manual, or "" if it is.
//
//   - No context: not manual. Home Assistant always sends one; without it the
//     source is unknown, so the house stays asleep.
//   - A parent ID: an automation or script made the change.
//   - The app's user ID: the app's own command (lux control, scenes, Picos, path lights).
//   - An empty user ID or another user's ID: a person (Hue app, wall switch, dashboard).
func (m *Manager) judge(ctx *ha.Context) string {
	if ctx == nil {
		return "no context: source unknown"
	}
	if ctx.ParentID != "" {
		return "automation (context has a parent)"
	}
	appUserID := m.lookupAppUserID()
	if appUserID == "" {
		return "app user unknown: cannot tell the app's own commands apart"
	}
	if ctx.UserID == appUserID {
		return "the app's own command"
	}
	return ""
}

// handleAnyoneAsleepChange clears early wake on a normal wake (isAnyoneAsleep true->false).
func (m *Manager) handleAnyoneAsleepChange(_ string, oldValue, newValue interface{}) {
	oldBool, _ := oldValue.(bool)
	newBool, _ := newValue.(bool)
	if !oldBool || newBool {
		return
	}

	active, err := m.stateManager.GetBool("isEarlyWakeActive")
	if err != nil {
		m.logger.Error("early wake: cannot read isEarlyWakeActive", zap.Error(err))
		return
	}
	if !active {
		return
	}

	m.shadowTracker.SnapshotInputsForAction()
	if m.readOnly {
		m.logger.Info("READ-ONLY: would clear early wake (everyone woke up)")
		return
	}
	if err := m.stateManager.SetBool("isEarlyWakeActive", false); err != nil {
		m.logger.Error("early wake: failed to clear isEarlyWakeActive", zap.Error(err))
		return
	}
	m.shadowTracker.RecordCleared(m.now())
	m.logger.Info("early wake cleared: everyone woke up")
}

// handleMasterAsleepChange clears early wake at bedtime (isMasterAsleep false->true).
//
// A guest can count as asleep all day, and early wake can be turned on from the
// dashboard while everyone is awake. In both cases isAnyoneAsleep never falls,
// so without this clear early wake would carry into the night.
func (m *Manager) handleMasterAsleepChange(_ string, oldValue, newValue interface{}) {
	oldBool, _ := oldValue.(bool)
	newBool, _ := newValue.(bool)
	if oldBool || !newBool {
		return
	}

	active, err := m.stateManager.GetBool("isEarlyWakeActive")
	if err != nil {
		m.logger.Error("early wake: cannot read isEarlyWakeActive", zap.Error(err))
		return
	}
	if !active {
		return
	}

	m.shadowTracker.SnapshotInputsForAction()
	if m.readOnly {
		m.logger.Info("READ-ONLY: would clear early wake (bedtime)")
		return
	}
	if err := m.stateManager.SetBool("isEarlyWakeActive", false); err != nil {
		m.logger.Error("early wake: failed to clear isEarlyWakeActive", zap.Error(err))
		return
	}
	m.shadowTracker.RecordCleared(m.now())
	m.logger.Info("early wake cleared: bedtime")
}
