// Package vacuum monitors the robot vacuum's error sensor and announces
// actionable errors via TTS.
//
// Today this is the only behavior. The plugin is structured (Manager + YAML
// config + shadow tracker) so future features — scheduling, room sequencing,
// per-room vacuum/mop parameters, service-call triggering — slot in without
// restructuring.
package vacuum

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

// Default cadence for the background ticker that re-evaluates whether a stale
// error needs to be re-announced. The ticker only fires the announcement if
// (now - lastAnnouncedAt) >= config.Announcement.RepeatInterval, so this can
// be small without spamming.
const defaultRepeatCheckInterval = 1 * time.Minute

// Manager handles vacuum error announcements.
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	notifier     notify.Notifier
	logger       *zap.Logger
	readOnly     bool
	timeProvider plugin.TimeProvider

	cfg *Config

	shadowTracker *shadowstate.VacuumTracker
	subHelper     *shadowstate.SubscriptionHelper

	// repeatCheckInterval controls how often the background ticker re-checks
	// for repeat announcements. Tests inject a smaller value via SetRepeatCheckIntervalForTest.
	repeatCheckInterval time.Duration

	mu              sync.Mutex
	currentError    string
	lastAnnouncedAt time.Time

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewManager constructs a Manager.
//
// timeProvider may be nil (defaults to plugin.RealTimeProvider). cfg must be
// non-nil and already validated by LoadConfig.
func NewManager(
	ctx context.Context,
	haClient ha.HAClient,
	stateManager *state.Manager,
	notifier notify.Notifier,
	cfg *Config,
	logger *zap.Logger,
	readOnly bool,
	timeProvider plugin.TimeProvider,
	registry *shadowstate.SubscriptionRegistry,
) *Manager {
	if timeProvider == nil {
		timeProvider = plugin.RealTimeProvider{}
	}
	tracker := shadowstate.NewVacuumTracker()
	return &Manager{
		ctx:                 ctx,
		haClient:            haClient,
		stateManager:        stateManager,
		notifier:            notifier,
		logger:              logger.Named("vacuum"),
		readOnly:            readOnly,
		timeProvider:        timeProvider,
		cfg:                 cfg,
		shadowTracker:       tracker,
		subHelper:           shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, tracker, "vacuum", logger.Named("vacuum")),
		repeatCheckInterval: defaultRepeatCheckInterval,
		stopCh:              make(chan struct{}),
	}
}

// GetShadowState returns the current shadow state snapshot.
func (m *Manager) GetShadowState() *shadowstate.VacuumShadowState {
	return m.shadowTracker.GetState()
}

// Start subscribes to the error sensor, captures initial state, and launches
// the repeat-check goroutine.
func (m *Manager) Start() error {
	m.logger.Info("Starting Vacuum Manager",
		zap.String("error_sensor", m.cfg.Vacuum.ErrorSensorID),
		zap.Duration("repeat_interval", m.cfg.Vacuum.Announcement.RepeatInterval))

	if err := m.subHelper.SubscribeToEntity(m.cfg.Vacuum.ErrorSensorID, m.handleErrorChange); err != nil {
		return fmt.Errorf("subscribe to vacuum error sensor: %w", err)
	}
	m.subHelper.CaptureInitialInputs()

	// Treat the sensor's current state as if we just received it. If the vacuum
	// already has an active error when we start up, announce it.
	if cur, err := m.haClient.GetState(m.cfg.Vacuum.ErrorSensorID); err == nil && cur != nil {
		m.handleErrorChange(m.cfg.Vacuum.ErrorSensorID, nil, cur)
	} else if err != nil {
		m.logger.Warn("Failed to read initial vacuum error sensor state",
			zap.String("entity", m.cfg.Vacuum.ErrorSensorID),
			zap.Error(err))
	}

	m.wg.Add(1)
	go m.runRepeatLoop()

	m.logger.Info("Vacuum Manager started")
	return nil
}

// Stop tears down subscriptions and stops the repeat goroutine.
func (m *Manager) Stop() {
	m.logger.Info("Stopping Vacuum Manager")
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	m.subHelper.UnsubscribeAll()
	m.wg.Wait()
	m.logger.Info("Vacuum Manager stopped")
}

// Reset clears the cached error so the next sensor change is treated as fresh.
func (m *Manager) Reset() error {
	m.mu.Lock()
	m.currentError = ""
	m.lastAnnouncedAt = time.Time{}
	m.mu.Unlock()
	m.shadowTracker.SetCurrentError("")
	return nil
}

// handleErrorChange responds to error sensor changes.
//
// State transitions:
//   - any → "No error":            clear, no announcement.
//   - "No error" → real error:     announce + arm repeat.
//   - real error A → real error A: no-op (timer keeps cadence).
//   - real error A → real error B: announce (message updated).
func (m *Manager) handleErrorChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	value := strings.TrimSpace(newState.State)
	if value == "" {
		// Skip transient empty/unknown states.
		return
	}

	if value == m.cfg.Vacuum.NoErrorValue {
		m.mu.Lock()
		hadError := m.currentError != ""
		m.currentError = ""
		m.lastAnnouncedAt = time.Time{}
		m.mu.Unlock()

		if hadError {
			m.logger.Info("Vacuum error cleared", zap.String("entity", entityID))
		}
		m.shadowTracker.SetCurrentError("")
		return
	}

	m.mu.Lock()
	if value == m.currentError {
		m.mu.Unlock()
		return
	}
	m.currentError = value
	m.mu.Unlock()

	m.shadowTracker.SetCurrentError(value)
	m.logger.Info("Vacuum error active",
		zap.String("entity", entityID),
		zap.String("error", value))

	m.maybeAnnounce(value)
}

// maybeAnnounce sends a TTS announcement for errorDesc as a Deferable
// announcement. The notifier suppresses delivery while master is asleep and
// returns notify.ErrSuppressedAsleep; we update lastAnnouncedAt regardless so
// the 2h repeat cadence applies uniformly to suppressed and spoken events.
func (m *Manager) maybeAnnounce(errorDesc string) {
	now := m.timeProvider.Now()
	message := fmt.Sprintf("%s: %s", m.cfg.Vacuum.Announcement.MessagePrefix, errorDesc)

	err := m.notifier.Announce(m.ctx, message,
		notify.WithSpeakers(m.cfg.Vacuum.Announcement.Speakers),
		notify.WithUrgency(notify.UrgencyDeferable))
	switch {
	case errors.Is(err, notify.ErrSuppressedAsleep):
		m.logger.Info("Vacuum error TTS suppressed (master asleep)",
			zap.String("error", errorDesc))
		m.shadowTracker.RecordSuppressedWhileAsleep(now)
		m.mu.Lock()
		m.lastAnnouncedAt = now
		m.mu.Unlock()
	case err != nil:
		m.logger.Error("Failed to send vacuum announcement",
			zap.String("message", message),
			zap.Error(err))
		// Still record the attempt so the repeat timer doesn't immediately retry.
		m.recordAnnounced(now, message)
	default:
		m.logger.Info("Vacuum announcement sent",
			zap.String("message", message),
			zap.Strings("speakers", m.cfg.Vacuum.Announcement.Speakers))
		m.recordAnnounced(now, message)
	}
}

func (m *Manager) recordAnnounced(at time.Time, message string) {
	m.mu.Lock()
	m.lastAnnouncedAt = at
	m.mu.Unlock()
	m.shadowTracker.RecordAnnouncement(message, at)
}

// runRepeatLoop periodically re-evaluates whether to re-announce a stale error.
func (m *Manager) runRepeatLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.repeatCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.tickRepeat()
		}
	}
}

// tickRepeat checks whether the current error has been unresolved long enough
// to deserve a re-announcement, and if so sends one.
func (m *Manager) tickRepeat() {
	m.mu.Lock()
	currentErr := m.currentError
	last := m.lastAnnouncedAt
	m.mu.Unlock()

	if currentErr == "" {
		return
	}

	now := m.timeProvider.Now()
	if last.IsZero() || now.Sub(last) >= m.cfg.Vacuum.Announcement.RepeatInterval {
		// Re-validate under lock: error may have cleared between the snapshot above and now.
		m.mu.Lock()
		if m.currentError != currentErr {
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		m.maybeAnnounce(currentErr)
	}
}

// SetRepeatCheckIntervalForTest overrides the background tick cadence. Tests
// only — never call from production code.
func (m *Manager) SetRepeatCheckIntervalForTest(d time.Duration) {
	m.repeatCheckInterval = d
}

// TickRepeatForTest invokes the periodic check synchronously. Tests only.
func (m *Manager) TickRepeatForTest() {
	m.tickRepeat()
}
