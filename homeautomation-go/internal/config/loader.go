package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"homeautomation/internal/clock"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// MusicConfig represents the music_config.yaml structure
type MusicConfig struct {
	Playlists map[string]interface{} `yaml:"playlists"`
	Speakers  map[string]interface{} `yaml:"speakers"`
	Volumes   map[string]interface{} `yaml:"volumes"`
	// Raw data for any additional fields
	Raw map[string]interface{} `yaml:",inline"`
}

// HueConfig represents the hue_config.yaml structure
type HueConfig struct {
	Lights map[string]interface{} `yaml:"lights"`
	Scenes map[string]interface{} `yaml:"scenes"`
	Groups map[string]interface{} `yaml:"groups"`
	// Raw data for any additional fields
	Raw map[string]interface{} `yaml:",inline"`
}

// ScheduleEntry represents a single day's schedule
type ScheduleEntry struct {
	BeginBackupWake string `yaml:"begin_backup_wake"`
	BackupWakeTime  string `yaml:"backup_wake_time"`
	Dusk            string `yaml:"dusk"`
	Winddown        string `yaml:"winddown"`
	StopScreens     string `yaml:"stop_screens"`
	GoToBed         string `yaml:"go_to_bed"`
	Night           string `yaml:"night"`
}

// ScheduleConfig represents the schedule_config.yaml structure
type ScheduleConfig struct {
	Schedule []ScheduleEntry `yaml:"schedule"`
}

// ParsedSchedule contains parsed schedule times for the current day
type ParsedSchedule struct {
	BeginBackupWake time.Time `json:"begin_backup_wake"`
	BackupWakeTime  time.Time `json:"backup_wake_time"`
	Dusk            time.Time `json:"dusk"`
	Winddown        time.Time `json:"winddown"`
	StopScreens     time.Time `json:"stop_screens"`
	GoToBed         time.Time `json:"go_to_bed"`
	Night           time.Time `json:"night"`
}

// Loader manages configuration file loading and reloading
type Loader struct {
	configDir      string
	logger         *zap.Logger
	musicConfig    *MusicConfig
	hueConfig      *HueConfig
	scheduleConfig *ScheduleConfig
	stopChan       chan struct{}
	timezone       *time.Location
	clock          clock.Clock
}

// NewLoader creates a new configuration loader
func NewLoader(configDir string, logger *zap.Logger) *Loader {
	return &Loader{
		configDir: configDir,
		logger:    logger,
		stopChan:  make(chan struct{}),
		timezone:  time.Local,
		clock:     clock.NewRealClock(),
	}
}

// SetClock sets the clock implementation for schedule parsing.
// This is primarily used for testing with a mock clock.
func (l *Loader) SetClock(clk clock.Clock) {
	if clk != nil {
		l.clock = clk
	}
}

// SetTimezone sets the timezone for schedule parsing and auto-reload scheduling.
// Should be called before StartAutoReload() if a non-local timezone is needed.
func (l *Loader) SetTimezone(timezone *time.Location) {
	if timezone != nil {
		l.timezone = timezone
	}
}

// LoadAll loads all configuration files
func (l *Loader) LoadAll() error {
	l.logger.Info("Loading configuration files", zap.String("dir", l.configDir))

	// Load music config
	if err := l.LoadMusicConfig(); err != nil {
		return fmt.Errorf("failed to load music config: %w", err)
	}

	// Load hue config
	if err := l.LoadHueConfig(); err != nil {
		return fmt.Errorf("failed to load hue config: %w", err)
	}

	// Load schedule config
	if err := l.LoadScheduleConfig(); err != nil {
		return fmt.Errorf("failed to load schedule config: %w", err)
	}

	l.logger.Info("All configuration files loaded successfully")
	return nil
}

// LoadMusicConfig loads the music_config.yaml file
func (l *Loader) LoadMusicConfig() error {
	path := filepath.Join(l.configDir, "music_config.yaml")
	l.logger.Debug("Loading music config", zap.String("path", path))

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read music config: %w", err)
	}

	var config MusicConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse music config: %w", err)
	}

	l.musicConfig = &config
	l.logger.Info("Music config loaded successfully")
	return nil
}

// LoadHueConfig loads the hue_config.yaml file
func (l *Loader) LoadHueConfig() error {
	path := filepath.Join(l.configDir, "hue_config.yaml")
	l.logger.Debug("Loading hue config", zap.String("path", path))

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read hue config: %w", err)
	}

	var config HueConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse hue config: %w", err)
	}

	l.hueConfig = &config
	l.logger.Info("Hue config loaded successfully")
	return nil
}

// LoadScheduleConfig loads the schedule_config.yaml file
func (l *Loader) LoadScheduleConfig() error {
	path := filepath.Join(l.configDir, "schedule_config.yaml")
	l.logger.Debug("Loading schedule config", zap.String("path", path))

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read schedule config: %w", err)
	}

	var config ScheduleConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse schedule config: %w", err)
	}

	l.scheduleConfig = &config
	l.logger.Info("Schedule config loaded successfully",
		zap.Int("entries", len(config.Schedule)))
	return nil
}

// GetMusicConfig returns the loaded music configuration
func (l *Loader) GetMusicConfig() *MusicConfig {
	return l.musicConfig
}

// GetHueConfig returns the loaded hue configuration
func (l *Loader) GetHueConfig() *HueConfig {
	return l.hueConfig
}

// GetScheduleConfig returns the loaded schedule configuration
func (l *Loader) GetScheduleConfig() *ScheduleConfig {
	return l.scheduleConfig
}

// GetTodaysSchedule parses and returns today's schedule with actual timestamps
// Uses the system's local timezone (time.Local)
func (l *Loader) GetTodaysSchedule() (*ParsedSchedule, error) {
	return l.GetTodaysScheduleInTimezone(time.Local)
}

// GetTodaysScheduleInTimezone parses and returns today's schedule with actual timestamps
// in the specified timezone. This should be used when the system timezone differs from
// the desired schedule timezone (e.g., Docker containers running in UTC).
func (l *Loader) GetTodaysScheduleInTimezone(timezone *time.Location) (*ParsedSchedule, error) {
	if l.scheduleConfig == nil {
		return nil, fmt.Errorf("schedule config not loaded")
	}

	// Get current time in the specified timezone using the injectable clock
	now := l.clock.Now().In(timezone)
	weekday := int(now.Weekday())

	if weekday >= len(l.scheduleConfig.Schedule) {
		return nil, fmt.Errorf("no schedule entry for weekday %d", weekday)
	}

	entry := l.scheduleConfig.Schedule[weekday]

	parseTime := func(timeStr string) (time.Time, error) {
		// Parse time in format "HH:MM"
		t, err := time.Parse("15:04", timeStr)
		if err != nil {
			return time.Time{}, err
		}

		// Combine with today's date in the specified timezone
		year, month, day := now.Date()
		return time.Date(year, month, day, t.Hour(), t.Minute(), 0, 0, timezone), nil
	}

	beginBackupWake, err := parseTime(entry.BeginBackupWake)
	if err != nil {
		return nil, fmt.Errorf("failed to parse begin_backup_wake: %w", err)
	}

	backupWakeTime, err := parseTime(entry.BackupWakeTime)
	if err != nil {
		return nil, fmt.Errorf("failed to parse backup_wake_time: %w", err)
	}

	dusk, err := parseTime(entry.Dusk)
	if err != nil {
		return nil, fmt.Errorf("failed to parse dusk: %w", err)
	}

	winddown, err := parseTime(entry.Winddown)
	if err != nil {
		return nil, fmt.Errorf("failed to parse winddown: %w", err)
	}

	stopScreens, err := parseTime(entry.StopScreens)
	if err != nil {
		return nil, fmt.Errorf("failed to parse stop_screens: %w", err)
	}

	goToBed, err := parseTime(entry.GoToBed)
	if err != nil {
		return nil, fmt.Errorf("failed to parse go_to_bed: %w", err)
	}

	night, err := parseTime(entry.Night)
	if err != nil {
		return nil, fmt.Errorf("failed to parse night: %w", err)
	}

	return &ParsedSchedule{
		BeginBackupWake: beginBackupWake,
		BackupWakeTime:  backupWakeTime,
		Dusk:            dusk,
		Winddown:        winddown,
		StopScreens:     stopScreens,
		GoToBed:         goToBed,
		Night:           night,
	}, nil
}

// StartAutoReload starts automatic configuration reloading at 00:01 daily
// Uses the configured timezone (set via SetTimezone) for calculating midnight
func (l *Loader) StartAutoReload() {
	l.logger.Info("Starting auto-reload scheduler (daily at 00:01)",
		zap.String("timezone", l.timezone.String()))

	// Calculate time until next 00:01 in the configured timezone
	now := time.Now().In(l.timezone)
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 1, 0, 0, l.timezone)
	duration := next.Sub(now)

	// Start a goroutine that reloads configs daily
	go func() {
		// Wait for first reload time
		timer := time.NewTimer(duration)
		defer timer.Stop()

		for {
			select {
			case <-timer.C:
				l.logger.Info("Auto-reloading configurations")
				if err := l.LoadAll(); err != nil {
					l.logger.Error("Failed to auto-reload configs", zap.Error(err))
				}

				// Schedule next reload in 24 hours
				timer.Reset(24 * time.Hour)

			case <-l.stopChan:
				l.logger.Info("Stopping auto-reload scheduler")
				return
			}
		}
	}()
}

// Stop stops the auto-reload scheduler
func (l *Loader) Stop() {
	close(l.stopChan)
}
