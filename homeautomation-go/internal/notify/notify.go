// Package notify provides a shared TTS announcement helper that snapshots
// speaker volume, overrides to a configured announcement volume, sends the
// TTS via Home Assistant, and restores the prior volume after a delay.
//
// All plugins that need to make verbal announcements should depend on the
// Notifier interface rather than calling tts.speak directly. This guarantees
// announcements remain audible regardless of the speakers' current state
// (ducked music, paused playback, low background volume) and centralises
// behavior such as sleep-aware volume reduction.
package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Notifier sends verbal (TTS) announcements to speakers.
type Notifier interface {
	Announce(ctx context.Context, message string, opts ...AnnounceOption) error
}

// UrgencyLevel controls volume behavior when the master is asleep.
type UrgencyLevel int

const (
	// UrgencyRoutine uses the asleep volume when isMasterAsleep is true.
	UrgencyRoutine UrgencyLevel = iota

	// UrgencyUrgent uses the awake volume even when asleep. Use for security
	// alerts (doorbell, vehicle arrival, intruder detection) that must be heard.
	UrgencyUrgent
)

// MasterAsleepStateVar is the state variable consulted for sleep-aware volume.
const MasterAsleepStateVar = "isMasterAsleep"

type announceOpts struct {
	speakers []string
	urgency  UrgencyLevel
}

// AnnounceOption customizes a single Announce call.
type AnnounceOption func(*announceOpts)

// WithSpeakers overrides the default speaker list for this announcement.
func WithSpeakers(speakers []string) AnnounceOption {
	return func(o *announceOpts) {
		o.speakers = append([]string(nil), speakers...)
	}
}

// WithUrgency sets the announcement urgency. Default is UrgencyRoutine.
func WithUrgency(level UrgencyLevel) AnnounceOption {
	return func(o *announceOpts) { o.urgency = level }
}

// Manager is the default Notifier implementation backed by Home Assistant.
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	cfg          Config
	logger       *zap.Logger
	readOnly     bool

	wg sync.WaitGroup
}

// NewManager constructs a Notifier. stateManager may be nil in tests; sleep-aware
// volume falls back to AwakeVolumePercent in that case.
func NewManager(haClient ha.HAClient, stateManager *state.Manager, cfg Config, logger *zap.Logger, readOnly bool) *Manager {
	cfg.applyDefaults()
	return &Manager{
		haClient:     haClient,
		stateManager: stateManager,
		cfg:          cfg,
		logger:       logger.Named("notify"),
		readOnly:     readOnly,
	}
}

// WaitForRestores blocks until all pending volume restores complete. Use during
// graceful shutdown to avoid leaving speakers at the announcement volume.
func (m *Manager) WaitForRestores() { m.wg.Wait() }

// Announce speaks message on the configured speakers. The TTS service call is
// synchronous; the volume restore is scheduled asynchronously after
// cfg.RestoreDelay so the caller does not block waiting for playback to end.
//
// Snapshot and restore failures are logged but do not fail the call - an
// audible announcement at an unknown final volume is preferable to no
// announcement.
func (m *Manager) Announce(ctx context.Context, message string, opts ...AnnounceOption) error {
	o := announceOpts{
		speakers: m.cfg.DefaultSpeakers,
		urgency:  UrgencyRoutine,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if len(o.speakers) == 0 {
		return fmt.Errorf("notify.Announce: no speakers configured")
	}
	if message == "" {
		return fmt.Errorf("notify.Announce: empty message")
	}

	targetVolume := m.targetVolumePercent(o.urgency)

	if m.readOnly {
		m.logger.Info("READ-ONLY: would announce TTS",
			zap.String("message", message),
			zap.Strings("speakers", o.speakers),
			zap.Int("target_volume_percent", targetVolume),
			zap.Int("urgency", int(o.urgency)))
		return nil
	}

	var priorVolumes map[string]float64
	if m.cfg.SnapshotRestore {
		priorVolumes = m.snapshotVolumes(o.speakers)
		m.setVolumes(ctx, o.speakers, float64(targetVolume)/100.0)
	}

	if err := m.haClient.CallService(ctx, m.cfg.TTSDomain, m.cfg.TTSService, map[string]interface{}{
		"entity_id":              m.cfg.TTSEntityID,
		"media_player_entity_id": o.speakers,
		"message":                message,
		"cache":                  true,
	}); err != nil {
		// On TTS failure, restore immediately so we don't leave speakers loud.
		if m.cfg.SnapshotRestore && len(priorVolumes) > 0 {
			m.restoreVolumesNow(ctx, priorVolumes)
		}
		return fmt.Errorf("tts.%s call failed: %w", m.cfg.TTSService, err)
	}

	m.logger.Info("Announcement sent",
		zap.String("message", message),
		zap.Strings("speakers", o.speakers),
		zap.Int("target_volume_percent", targetVolume),
		zap.Int("urgency", int(o.urgency)))

	if m.cfg.SnapshotRestore && len(priorVolumes) > 0 {
		m.scheduleRestore(ctx, priorVolumes)
	}
	return nil
}

func (m *Manager) targetVolumePercent(urgency UrgencyLevel) int {
	if urgency == UrgencyRoutine && m.isMasterAsleep() {
		return m.cfg.AsleepVolumePercent
	}
	return m.cfg.AwakeVolumePercent
}

func (m *Manager) isMasterAsleep() bool {
	if m.stateManager == nil {
		return false
	}
	v, err := m.stateManager.GetBool(MasterAsleepStateVar)
	if err != nil {
		// Conservative: treat as awake so announcements stay loud.
		return false
	}
	return v
}

// snapshotVolumes reads the current volume_level for each speaker. Failures
// are logged and the speaker is omitted from the returned map (so it is not
// restored later).
func (m *Manager) snapshotVolumes(speakers []string) map[string]float64 {
	out := make(map[string]float64, len(speakers))
	for _, entityID := range speakers {
		st, err := m.haClient.GetState(entityID)
		if err != nil {
			m.logger.Warn("Failed to snapshot speaker volume; will not restore",
				zap.String("entity_id", entityID),
				zap.Error(err))
			continue
		}
		raw, ok := st.Attributes["volume_level"]
		if !ok {
			m.logger.Debug("Speaker has no volume_level attribute; will not restore",
				zap.String("entity_id", entityID))
			continue
		}
		v, ok := raw.(float64)
		if !ok {
			m.logger.Debug("Speaker volume_level is not float; will not restore",
				zap.String("entity_id", entityID),
				zap.Any("value", raw))
			continue
		}
		out[entityID] = v
	}
	return out
}

func (m *Manager) setVolumes(ctx context.Context, speakers []string, level float64) {
	// One service call per speaker. Calling media_player.volume_set with a
	// list entity_id is supported but failures are harder to diagnose; per-
	// speaker calls give clear error attribution.
	for _, entityID := range speakers {
		err := m.haClient.CallService(ctx, "media_player", "volume_set", map[string]interface{}{
			"entity_id":    entityID,
			"volume_level": level,
		})
		if err != nil {
			m.logger.Warn("Failed to set speaker volume for announcement",
				zap.String("entity_id", entityID),
				zap.Float64("volume_level", level),
				zap.Error(err))
		}
	}
}

func (m *Manager) scheduleRestore(parentCtx context.Context, priorVolumes map[string]float64) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		select {
		case <-time.After(m.cfg.RestoreDelay):
		case <-parentCtx.Done():
			// Parent cancelled (likely shutdown). Restore now anyway.
		}
		// Use a fresh context for the restore - parent may already be done.
		restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		m.restoreVolumesNow(restoreCtx, priorVolumes)
	}()
}

func (m *Manager) restoreVolumesNow(ctx context.Context, priorVolumes map[string]float64) {
	for entityID, level := range priorVolumes {
		err := m.haClient.CallService(ctx, "media_player", "volume_set", map[string]interface{}{
			"entity_id":    entityID,
			"volume_level": level,
		})
		if err != nil {
			m.logger.Warn("Failed to restore speaker volume after announcement",
				zap.String("entity_id", entityID),
				zap.Float64("volume_level", level),
				zap.Error(err))
		}
	}
}
