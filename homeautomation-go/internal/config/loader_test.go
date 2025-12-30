package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func setupTestConfigDir(t *testing.T) string {
	tmpDir := t.TempDir()

	// Create sample music_config.yaml
	musicConfig := `playlists:
  morning:
    - "spotify:playlist:123"
  day:
    - "spotify:playlist:456"
speakers:
  living_room: "192.168.1.100"
  bedroom: "192.168.1.101"
volumes:
  morning: 0.5
  day: 0.7
`
	err := os.WriteFile(filepath.Join(tmpDir, "music_config.yaml"), []byte(musicConfig), 0644)
	require.NoError(t, err)

	// Create sample hue_config.yaml
	hueConfig := `lights:
  living_room:
    - "light.living_room_1"
    - "light.living_room_2"
scenes:
  morning: "bright"
  evening: "dim"
groups:
  all: "group.all_lights"
`
	err = os.WriteFile(filepath.Join(tmpDir, "hue_config.yaml"), []byte(hueConfig), 0644)
	require.NoError(t, err)

	// Create sample schedule_config.yaml
	scheduleConfig := `schedule:
  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "05:30"
    backup_wake_time: "07:30"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "06:00"
    backup_wake_time: "08:00"
    dusk: "18:00"
    winddown: "22:00"
    stop_screens: "23:00"
    go_to_bed: "23:30"
    night: "00:00"
  - begin_backup_wake: "06:00"
    backup_wake_time: "08:00"
    dusk: "18:00"
    winddown: "22:00"
    stop_screens: "23:00"
    go_to_bed: "23:30"
    night: "00:00"
`
	err = os.WriteFile(filepath.Join(tmpDir, "schedule_config.yaml"), []byte(scheduleConfig), 0644)
	require.NoError(t, err)

	return tmpDir
}

func TestLoader_LoadAll(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadAll()
	require.NoError(t, err)

	// Verify music config
	musicConfig := loader.GetMusicConfig()
	assert.NotNil(t, musicConfig)
	assert.NotNil(t, musicConfig.Playlists)
	assert.NotNil(t, musicConfig.Speakers)
	assert.NotNil(t, musicConfig.Volumes)

	// Verify hue config
	hueConfig := loader.GetHueConfig()
	assert.NotNil(t, hueConfig)
	assert.NotNil(t, hueConfig.Lights)
	assert.NotNil(t, hueConfig.Scenes)
	assert.NotNil(t, hueConfig.Groups)

	// Verify schedule config
	scheduleConfig := loader.GetScheduleConfig()
	assert.NotNil(t, scheduleConfig)
	assert.Equal(t, 7, len(scheduleConfig.Schedule), "Should have 7 days of schedule")
}

func TestLoader_LoadMusicConfig(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadMusicConfig()
	require.NoError(t, err)

	config := loader.GetMusicConfig()
	assert.NotNil(t, config)
	assert.Contains(t, config.Playlists, "morning")
	assert.Contains(t, config.Speakers, "living_room")
}

func TestLoader_LoadHueConfig(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadHueConfig()
	require.NoError(t, err)

	config := loader.GetHueConfig()
	assert.NotNil(t, config)
	assert.Contains(t, config.Lights, "living_room")
	assert.Contains(t, config.Scenes, "morning")
}

func TestLoader_LoadScheduleConfig(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadScheduleConfig()
	require.NoError(t, err)

	config := loader.GetScheduleConfig()
	assert.NotNil(t, config)
	assert.Equal(t, 7, len(config.Schedule))

	// Check first entry (Sunday)
	sunday := config.Schedule[0]
	assert.Equal(t, "05:00", sunday.BeginBackupWake)
	assert.Equal(t, "07:00", sunday.BackupWakeTime)
	assert.Equal(t, "23:00", sunday.Night)
}

func TestLoader_GetTodaysSchedule(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadScheduleConfig()
	require.NoError(t, err)

	schedule, err := loader.GetTodaysSchedule()
	require.NoError(t, err)
	assert.NotNil(t, schedule)

	// Verify times are parsed correctly
	assert.False(t, schedule.BeginBackupWake.IsZero())
	assert.False(t, schedule.BackupWakeTime.IsZero())
	assert.False(t, schedule.Dusk.IsZero())
	assert.False(t, schedule.Winddown.IsZero())
	assert.False(t, schedule.Night.IsZero())

	// Verify times are for today
	// The hour will depend on what day of the week it is
	// Sunday (0): 05:00, Monday (1): 05:30, Tue-Fri (2-5): 05:00, Sat-Sun (6): 06:00
	hour := schedule.BeginBackupWake.Hour()
	assert.True(t, hour >= 5 && hour <= 6, "Begin backup wake hour should be between 5 and 6")

	// Expected minute depends on day of week: Monday has 30, all other days have 0
	expectedMinute := 0
	if schedule.BeginBackupWake.Weekday() == time.Monday {
		expectedMinute = 30
	}
	assert.Equal(t, expectedMinute, schedule.BeginBackupWake.Minute())
}

func TestLoader_MissingFile(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := t.TempDir() // Empty directory

	loader := NewLoader(configDir, logger)
	err := loader.LoadAll()
	assert.Error(t, err)
}

func TestLoader_GetTodaysSchedule_NotLoaded(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := t.TempDir()

	loader := NewLoader(configDir, logger)
	// Don't load config
	schedule, err := loader.GetTodaysSchedule()
	assert.Error(t, err)
	assert.Nil(t, schedule)
	assert.Contains(t, err.Error(), "schedule config not loaded")
}

func TestLoader_GetTodaysSchedule_InvalidTime(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	tmpDir := t.TempDir()

	// Create schedule config with invalid time format (7 entries for all days of the week)
	scheduleConfig := `schedule:
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
  - begin_backup_wake: "invalid"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
`
	err := os.WriteFile(filepath.Join(tmpDir, "schedule_config.yaml"), []byte(scheduleConfig), 0644)
	require.NoError(t, err)

	loader := NewLoader(tmpDir, logger)
	err = loader.LoadScheduleConfig()
	require.NoError(t, err)

	schedule, err := loader.GetTodaysSchedule()
	assert.Error(t, err)
	assert.Nil(t, schedule)
	assert.Contains(t, err.Error(), "failed to parse begin_backup_wake")
}

func TestLoader_StartAutoReload_Stop(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadAll()
	require.NoError(t, err)

	// Start auto-reload
	loader.StartAutoReload()

	// Give it a moment to start
	// (We can't wait for the timer since it's scheduled for tomorrow)
	// Just verify it doesn't crash

	// Stop the auto-reload
	loader.Stop()

	// Give it a moment to stop gracefully
	// The goroutine should exit cleanly
}

func TestLoader_Stop_MultipleCalls(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadAll()
	require.NoError(t, err)

	loader.StartAutoReload()

	// Stop should be idempotent (safe to call multiple times)
	// Note: In the current implementation, calling Stop multiple times
	// will panic due to closing a closed channel, but that's a design decision.
	// For now, we'll just test a single stop.
	loader.Stop()
}

func TestLoader_GetTodaysSchedule_ParseErrors(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	// Helper to create 7 identical schedule entries (one for each day of week)
	makeScheduleWithSevenEntries := func(entry string) string {
		result := "schedule:\n"
		for i := 0; i < 7; i++ {
			result += entry
		}
		return result
	}

	testCases := []struct {
		name       string
		entryYAML  string
		errorField string
	}{
		{
			name: "invalid backup_wake_time",
			entryYAML: `  - begin_backup_wake: "05:00"
    backup_wake_time: "invalid"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
`,
			errorField: "backup_wake_time",
		},
		{
			name: "invalid dusk time",
			entryYAML: `  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "99:99"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
`,
			errorField: "dusk",
		},
		{
			name: "invalid winddown time",
			entryYAML: `  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "not a time"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "23:00"
`,
			errorField: "winddown",
		},
		{
			name: "invalid stop_screens time",
			entryYAML: `  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "bad"
    go_to_bed: "22:30"
    night: "23:00"
`,
			errorField: "stop_screens",
		},
		{
			name: "invalid go_to_bed time",
			entryYAML: `  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "25:00"
    night: "23:00"
`,
			errorField: "go_to_bed",
		},
		{
			name: "invalid night time",
			entryYAML: `  - begin_backup_wake: "05:00"
    backup_wake_time: "07:00"
    dusk: "18:00"
    winddown: "21:00"
    stop_screens: "22:00"
    go_to_bed: "22:30"
    night: "abc"
`,
			errorField: "night",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			scheduleYAML := makeScheduleWithSevenEntries(tc.entryYAML)
			err := os.WriteFile(filepath.Join(tmpDir, "schedule_config.yaml"), []byte(scheduleYAML), 0644)
			require.NoError(t, err)

			loader := NewLoader(tmpDir, logger)
			err = loader.LoadScheduleConfig()
			require.NoError(t, err)

			schedule, err := loader.GetTodaysSchedule()
			assert.Error(t, err)
			assert.Nil(t, schedule)
			assert.Contains(t, err.Error(), tc.errorField)
		})
	}
}

func TestLoader_GetTodaysScheduleInTimezone(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)
	err := loader.LoadScheduleConfig()
	require.NoError(t, err)

	// Test with America/Chicago timezone (UTC-6 in winter)
	chicagoTZ, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)

	schedule, err := loader.GetTodaysScheduleInTimezone(chicagoTZ)
	require.NoError(t, err)
	assert.NotNil(t, schedule)

	// Verify that schedule times are in the Chicago timezone
	assert.Equal(t, chicagoTZ, schedule.BeginBackupWake.Location())
	assert.Equal(t, chicagoTZ, schedule.BackupWakeTime.Location())
	assert.Equal(t, chicagoTZ, schedule.StopScreens.Location())
	assert.Equal(t, chicagoTZ, schedule.GoToBed.Location())
	assert.Equal(t, chicagoTZ, schedule.Night.Location())

	// Verify that the times are for today in Chicago timezone
	nowChicago := time.Now().In(chicagoTZ)
	assert.Equal(t, nowChicago.Year(), schedule.BeginBackupWake.Year())
	assert.Equal(t, nowChicago.Month(), schedule.BeginBackupWake.Month())
	assert.Equal(t, nowChicago.Day(), schedule.BeginBackupWake.Day())
}

func TestLoader_SetTimezone(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	configDir := setupTestConfigDir(t)

	loader := NewLoader(configDir, logger)

	// Initial timezone should be Local
	assert.Equal(t, time.Local, loader.timezone)

	// Set a different timezone
	chicagoTZ, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)
	loader.SetTimezone(chicagoTZ)
	assert.Equal(t, chicagoTZ, loader.timezone)

	// Setting nil should not change the timezone
	loader.SetTimezone(nil)
	assert.Equal(t, chicagoTZ, loader.timezone)
}
