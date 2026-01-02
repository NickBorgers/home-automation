package music

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel(
	// Create a temporary config file
	)

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "music_config.yaml")

	configContent := `---
music:
  morning:
    participants:
      - player_name: Living Room
        base_volume: 25
        leave_muted_if:
          - variable: isMasterAsleep
            value: true
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DWSf2RDTDayIx
        media_type: playlist
        volume_multiplier: 1.0
  day:
    participants:
      - player_name: Living Room
        base_volume: 20
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DX0XUsuxWHRQd
        media_type: playlist
        volume_multiplier: 1.0
  evening:
    participants:
      - player_name: Living Room
        base_volume: 30
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DX4WYpdgoIcn6
        media_type: playlist
        volume_multiplier: 1.0
  winddown:
    participants:
      - player_name: Primary Suite
        base_volume: 15
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DWZd79rJ6a7lp
        media_type: playlist
        volume_multiplier: 0.8
  sleep:
    participants:
      - player_name: Primary Suite
        base_volume: 10
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DWZd79rJ6a7lp
        media_type: playlist
        volume_multiplier: 0.5
  sex:
    participants:
      - player_name: Primary Suite
        base_volume: 35
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DX4WYpdgoIcn6
        media_type: playlist
        volume_multiplier: 1.2
  wakeup:
    participants:
      - player_name: Primary Suite
        base_volume: 20
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:37i9dQZF1DWSf2RDTDayIx
        media_type: playlist
        volume_multiplier: 1.0
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Load the config
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify the config has all expected modes
	expectedModes := []string{"morning", "day", "evening", "winddown", "sleep", "sex", "wakeup"}
	if len(config.Music) != len(expectedModes) {
		t.Errorf("Expected %d music modes, got %d", len(expectedModes), len(config.Music))
	}

	for _, mode := range expectedModes {
		if _, ok := config.Music[mode]; !ok {
			t.Errorf("Missing expected music mode: %s", mode)
		}
	}

	// Check morning mode config in detail
	morning := config.Music["morning"]
	if len(morning.Participants) != 1 {
		t.Errorf("Expected 1 participant for morning mode, got %d", len(morning.Participants))
	}
	if morning.Participants[0].PlayerName != "Living Room" {
		t.Errorf("Expected PlayerName 'Living Room', got '%s'", morning.Participants[0].PlayerName)
	}
	if morning.Participants[0].BaseVolume != 25 {
		t.Errorf("Expected BaseVolume 25, got %d", morning.Participants[0].BaseVolume)
	}
	if len(morning.Participants[0].LeaveMutedIf) != 1 {
		t.Errorf("Expected 1 mute condition, got %d", len(morning.Participants[0].LeaveMutedIf))
	}
	if len(morning.PlaybackOptions) != 1 {
		t.Errorf("Expected 1 playback option, got %d", len(morning.PlaybackOptions))
	}
	if morning.PlaybackOptions[0].VolumeMultiplier != 1.0 {
		t.Errorf("Expected VolumeMultiplier 1.0, got %f", morning.PlaybackOptions[0].VolumeMultiplier)
	}

	// Check winddown mode (with different volume multiplier)
	winddown := config.Music["winddown"]
	if len(winddown.PlaybackOptions) != 1 {
		t.Errorf("Expected 1 playback option for winddown, got %d", len(winddown.PlaybackOptions))
	}
	if winddown.PlaybackOptions[0].VolumeMultiplier != 0.8 {
		t.Errorf("Expected VolumeMultiplier 0.8, got %f", winddown.PlaybackOptions[0].VolumeMultiplier)
	}
}

func TestLoadConfigInvalidPath(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig("/nonexistent/path/music_config.yaml")
	if err == nil {
		t.Error("Expected error for nonexistent config file, got nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidContent := `this is not: valid: yaml: content: definitely: broken`
	if err := os.WriteFile(configPath, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

func TestLoadConfigMissingRequiredMode(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "incomplete_config.yaml")

	// Config missing the "wakeup" mode
	incompleteContent := `---
music:
  morning:
    participants:
      - player_name: Living Room
        base_volume: 25
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:test
        media_type: playlist
        volume_multiplier: 1.0
  day:
    participants:
      - player_name: Living Room
        base_volume: 20
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:test
        media_type: playlist
        volume_multiplier: 1.0
  evening:
    participants:
      - player_name: Living Room
        base_volume: 30
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:test
        media_type: playlist
        volume_multiplier: 1.0
  winddown:
    participants:
      - player_name: Primary Suite
        base_volume: 15
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:test
        media_type: playlist
        volume_multiplier: 0.8
  sleep:
    participants:
      - player_name: Primary Suite
        base_volume: 10
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:test
        media_type: playlist
        volume_multiplier: 0.5
  sex:
    participants:
      - player_name: Primary Suite
        base_volume: 35
        leave_muted_if: []
    playback_options:
      - uri: spotify:playlist:test
        media_type: playlist
        volume_multiplier: 1.2
`

	if err := os.WriteFile(configPath, []byte(incompleteContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for missing required mode, got nil")
	}
	if err != nil && err.Error() != "missing required music mode: wakeup" {
		t.Errorf("Expected error about missing 'wakeup' mode, got: %v", err)
	}
}
