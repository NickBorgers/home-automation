// Package notify provides a shared TTS announcement service with automatic
// volume management. Every announcement snapshots each speaker's current
// volume, overrides to the configured level (sleep-aware), speaks, then
// restores prior volume after a configurable delay.
package notify

import (
	"context"
	"fmt"
	"os"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// Urgency controls the volume level for an announcement.
type Urgency int

const (
	// Routine is sleep-aware: uses asleep_volume_percent when master is asleep.
	Routine Urgency = iota
	// Urgent always uses awake_volume_percent regardless of sleep state.
	Urgent
)

// Notifier sends TTS announcements with automatic volume management.
type Notifier interface {
	// Speak announces message via TTS with volume snapshot/override/restore.
	// If speakers is nil or empty, the configured default_speakers are used.
	Speak(ctx context.Context, message string, urgency Urgency, speakers []string) error
}

// Config holds notification settings from notification_config.yaml.
type Config struct {
	Notification NotificationSettings `yaml:"notification"`
}

// NotificationSettings contains the individual notification configuration values.
type NotificationSettings struct {
	DefaultSpeakers     []string `yaml:"default_speakers"`
	AwakeVolumePercent  int      `yaml:"awake_volume_percent"`
	AsleepVolumePercent int      `yaml:"asleep_volume_percent"`
	TTSEntityID         string   `yaml:"tts_entity_id"`
	SnapshotRestore     bool     `yaml:"snapshot_restore"`
	RestoreDelaySeconds int      `yaml:"restore_delay_seconds"`
}

// LoadConfig reads notification_config.yaml from path.
// If the file does not exist, safe defaults are returned.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read notification config: %w", err)
	}
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse notification config: %w", err)
	}
	// Apply defaults for any zero values left after parsing.
	if cfg.Notification.TTSEntityID == "" {
		cfg.Notification.TTSEntityID = "tts.google_translate_en_com"
	}
	if cfg.Notification.AwakeVolumePercent == 0 {
		cfg.Notification.AwakeVolumePercent = 60
	}
	if cfg.Notification.AsleepVolumePercent == 0 {
		cfg.Notification.AsleepVolumePercent = 30
	}
	if cfg.Notification.RestoreDelaySeconds == 0 {
		cfg.Notification.RestoreDelaySeconds = 8
	}
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Notification: NotificationSettings{
			DefaultSpeakers: []string{
				"media_player.kitchen",
				"media_player.sitting_room",
				"media_player.front_room",
				"media_player.soundbar",
				"media_player.kids_bathroom",
				"media_player.bedroom",
			},
			AwakeVolumePercent:  60,
			AsleepVolumePercent: 30,
			TTSEntityID:         "tts.google_translate_en_com",
			SnapshotRestore:     true,
			RestoreDelaySeconds: 8,
		},
	}
}

// TTSNotifier implements Notifier using Home Assistant's tts.speak service.
type TTSNotifier struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	cfg          *Config
	logger       *zap.Logger
	readOnly     bool
}

// NewTTSNotifier creates a TTSNotifier.
// stateManager may be nil; in that case sleep-awareness is disabled and the
// awake volume is always used for Routine announcements.
func NewTTSNotifier(haClient ha.HAClient, stateManager *state.Manager, cfg *Config, logger *zap.Logger, readOnly bool) *TTSNotifier {
	return &TTSNotifier{
		haClient:     haClient,
		stateManager: stateManager,
		cfg:          cfg,
		logger:       logger.Named("notify"),
		readOnly:     readOnly,
	}
}

// Speak implements Notifier.
func (n *TTSNotifier) Speak(ctx context.Context, message string, urgency Urgency, speakers []string) error {
	if len(speakers) == 0 {
		speakers = n.cfg.Notification.DefaultSpeakers
	}

	volumePercent := n.cfg.Notification.AwakeVolumePercent
	if urgency == Routine && n.isMasterAsleep() {
		volumePercent = n.cfg.Notification.AsleepVolumePercent
	}

	if n.readOnly {
		n.logger.Info("READ-ONLY: Would send TTS announcement",
			zap.String("message", message),
			zap.Strings("speakers", speakers),
			zap.Int("volume_percent", volumePercent))
		return nil
	}

	// Snapshot current volumes for post-announcement restore.
	snapshots := n.snapshotVolumes(speakers)

	// Override to announcement volume.
	n.setVolumes(ctx, speakers, volumePercent)

	// Send TTS.
	err := n.haClient.CallService(ctx, "tts", "speak", map[string]interface{}{
		"entity_id":              n.cfg.Notification.TTSEntityID,
		"media_player_entity_id": speakers,
		"message":                message,
		"cache":                  true,
	})
	if err != nil {
		n.logger.Error("Failed to send TTS announcement",
			zap.String("message", message),
			zap.Error(err))
		// Restore immediately on failure so speakers are not left at announcement volume.
		if n.cfg.Notification.SnapshotRestore && len(snapshots) > 0 {
			n.restoreVolumes(context.Background(), snapshots)
		}
		return err
	}

	n.logger.Info("TTS announcement sent",
		zap.String("message", message),
		zap.Strings("speakers", speakers),
		zap.Int("volume_percent", volumePercent))

	// Restore volumes after the speech has had time to play.
	if n.cfg.Notification.SnapshotRestore && len(snapshots) > 0 {
		delay := time.Duration(n.cfg.Notification.RestoreDelaySeconds) * time.Second
		go func() {
			time.Sleep(delay)
			n.restoreVolumes(context.Background(), snapshots)
		}()
	}

	return nil
}

// snapshotVolumes reads each speaker's current volume_level attribute (0.0-1.0).
// Speakers whose volume cannot be determined are omitted from the result.
func (n *TTSNotifier) snapshotVolumes(speakers []string) map[string]float64 {
	snapshots := make(map[string]float64, len(speakers))
	for _, entityID := range speakers {
		s, err := n.haClient.GetState(entityID)
		if err != nil || s == nil {
			n.logger.Debug("Could not get speaker state for volume snapshot",
				zap.String("entity", entityID), zap.Error(err))
			continue
		}
		vol, ok := s.Attributes["volume_level"].(float64)
		if !ok {
			n.logger.Debug("Speaker has no volume_level attribute",
				zap.String("entity", entityID))
			continue
		}
		snapshots[entityID] = vol
	}
	return snapshots
}

// setVolumes sets all speakers to the given volume percent (0–100) in one call.
func (n *TTSNotifier) setVolumes(ctx context.Context, speakers []string, volumePercent int) {
	if err := n.haClient.CallService(ctx, "media_player", "volume_set", map[string]interface{}{
		"entity_id":    speakers,
		"volume_level": float64(volumePercent) / 100.0,
	}); err != nil {
		n.logger.Warn("Failed to set announcement volume",
			zap.Int("volume_percent", volumePercent),
			zap.Error(err))
	}
}

// restoreVolumes restores each speaker to its snapshotted volume.
func (n *TTSNotifier) restoreVolumes(ctx context.Context, snapshots map[string]float64) {
	for entityID, vol := range snapshots {
		if err := n.haClient.CallService(ctx, "media_player", "volume_set", map[string]interface{}{
			"entity_id":    entityID,
			"volume_level": vol,
		}); err != nil {
			n.logger.Warn("Failed to restore speaker volume",
				zap.String("entity", entityID),
				zap.Float64("volume", vol),
				zap.Error(err))
		}
	}
}

func (n *TTSNotifier) isMasterAsleep() bool {
	if n.stateManager == nil {
		return false
	}
	asleep, err := n.stateManager.GetBool("isMasterAsleep")
	if err != nil {
		return false
	}
	return asleep
}
