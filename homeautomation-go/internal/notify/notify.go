// Package notify provides a shared verbal-announcement helper. Each
// announcement is synthesized to MP3 by a Kokoro TTS server, served from a
// local HTTP file server, and played on Sonos speakers via Home Assistant's
// media_player.play_media with announce: true. The Sonos integration in HA
// snapshots the queue, current track, position, transport state, and volume
// before the announcement, ducks/plays the announcement at the configured
// awake_volume_percent, and restores everything when playback ends — so
// resumption of the prior music is seamless.
//
// All plugins that need to make verbal announcements should depend on the
// Notifier interface rather than constructing service calls directly. This
// guarantees announcements remain audible regardless of the speakers' current
// state and centralises the asleep-aware suppression policy.
package notify

import (
	"context"
	"errors"
	"fmt"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/tts"

	"go.uber.org/zap"
)

// Notifier sends verbal (TTS) announcements to speakers.
type Notifier interface {
	Announce(ctx context.Context, message string, opts ...AnnounceOption) error
}

// ErrSuppressedAsleep is returned by Announce when a UrgencyDeferable
// announcement is dropped because the master is asleep. Callers that want to
// track suppressed events (e.g. for shadow-state observability) should
// errors.Is against this sentinel; callers that don't can ignore it.
var ErrSuppressedAsleep = errors.New("notify: announcement suppressed (master asleep)")

// UrgencyLevel controls how the notifier handles an announcement when the
// master is asleep. Volume is always cfg.AwakeVolumePercent; urgency only
// changes whether the announcement plays at all.
type UrgencyLevel int

const (
	// UrgencyDeferable: drop entirely while master is asleep. Notifier returns
	// ErrSuppressedAsleep. Persistent-state plugins (vacuum errors, etc.) own
	// their own retry cadence and will re-announce after wake; transient
	// events (presence arrival) accept being missed since they are stale by
	// morning anyway.
	UrgencyDeferable UrgencyLevel = iota

	// UrgencyUrgent: always play, even when asleep. Use for events that must
	// wake the household (security alerts, doorbell, broken-pipe water flow,
	// EV-charger safety conditions, infrastructure crashes).
	UrgencyUrgent
)

// MasterAsleepStateVar is the state variable consulted for the suppress check.
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

// WithUrgency sets the announcement urgency. Default is UrgencyDeferable.
func WithUrgency(level UrgencyLevel) AnnounceOption {
	return func(o *announceOpts) { o.urgency = level }
}

// Manager is the default Notifier implementation backed by Home Assistant.
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	synthesizer  tts.Synthesizer
	cfg          Config
	logger       *zap.Logger
	readOnly     bool
}

// NewManager constructs a Notifier. stateManager may be nil in tests; the
// asleep-suppression check then defaults to "awake" so announcements are not
// silently dropped during tests that don't model sleep state.
func NewManager(haClient ha.HAClient, stateManager *state.Manager, synthesizer tts.Synthesizer, cfg Config, logger *zap.Logger, readOnly bool) *Manager {
	cfg.applyDefaults()
	return &Manager{
		haClient:     haClient,
		stateManager: stateManager,
		synthesizer:  synthesizer,
		cfg:          cfg,
		logger:       logger.Named("notify"),
		readOnly:     readOnly,
	}
}

// Announce speaks message on the configured speakers. Synthesis is performed
// before the HA service call; HA's Sonos integration handles snapshot, ducking,
// playback, and seamless restoration of the prior queue/track/volume via
// announce: true.
func (m *Manager) Announce(ctx context.Context, message string, opts ...AnnounceOption) error {
	o := announceOpts{
		speakers: m.cfg.DefaultSpeakers,
		urgency:  UrgencyDeferable,
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

	if o.urgency == UrgencyDeferable && m.isMasterAsleep() {
		m.logger.Info("Announcement suppressed (master asleep)",
			zap.String("message", message),
			zap.Strings("speakers", o.speakers))
		return ErrSuppressedAsleep
	}

	targetVolume := m.cfg.AwakeVolumePercent

	if m.readOnly {
		m.logger.Info("READ-ONLY: would announce TTS",
			zap.String("message", message),
			zap.Strings("speakers", o.speakers),
			zap.Int("target_volume_percent", targetVolume),
			zap.Int("urgency", int(o.urgency)))
		return nil
	}

	url, err := m.synthesizer.SynthesizeAndServe(ctx, message)
	if err != nil {
		return fmt.Errorf("synthesize announcement: %w", err)
	}

	if err := m.haClient.CallService(ctx, "media_player", "play_media", map[string]interface{}{
		"entity_id":          o.speakers,
		"media_content_id":   url,
		"media_content_type": "music",
		"announce":           true,
		"extra": map[string]interface{}{
			"volume": float64(targetVolume) / 100.0,
		},
	}); err != nil {
		return fmt.Errorf("media_player.play_media call failed: %w", err)
	}

	m.logger.Info("Announcement sent",
		zap.String("message", message),
		zap.Strings("speakers", o.speakers),
		zap.Int("target_volume_percent", targetVolume),
		zap.Int("urgency", int(o.urgency)),
		zap.String("audio_url", url))
	return nil
}

func (m *Manager) isMasterAsleep() bool {
	if m.stateManager == nil {
		return false
	}
	v, err := m.stateManager.GetBool(MasterAsleepStateVar)
	if err != nil {
		// Conservative: treat as awake so announcements still play. A missed
		// urgent announcement is worse than one that played at the wrong time.
		return false
	}
	return v
}
