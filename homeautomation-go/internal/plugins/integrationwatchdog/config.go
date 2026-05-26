// Package integrationwatchdog implements a plugin that watches Home Assistant
// integrations for stale or unavailable entity state and reloads the underlying
// config entry to recover, with cooldown and daily-cap safeguards.
package integrationwatchdog

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level YAML structure.
type Config struct {
	// CheckIntervalSec is how often the periodic scan runs. Defaults to 60 if zero.
	CheckIntervalSec int `yaml:"check_interval_sec"`

	// WatchTargets is the list of integrations to monitor.
	WatchTargets []WatchTarget `yaml:"watch_targets"`
}

// WatchTarget describes one HA integration to watch.
type WatchTarget struct {
	// Name is a stable, lowercase identifier (used in logs and shadow state).
	Name string `yaml:"name"`

	// IntegrationName is a human-friendly label shown in logs/dashboard.
	IntegrationName string `yaml:"integration_name"`

	// Entities is the list of entity_ids whose state should be examined.
	Entities []string `yaml:"entities"`

	// ConfigEntryID is the HA config entry to reload when the target is stale.
	ConfigEntryID string `yaml:"config_entry_id"`

	// Detection is the staleness rule for this target.
	Detection DetectionRule `yaml:"detection"`

	// Reload bounds reload attempts.
	Reload ReloadPolicy `yaml:"reload"`
}

// DetectionRule defines what counts as a stale target. Both rules are OR'd:
// any rule firing triggers a reload.
type DetectionRule struct {
	// BadStates is the set of HA state values that count as "bad" (e.g.
	// "unknown", "unavailable"). Empty means the bad-state rule is disabled.
	BadStates []string `yaml:"bad_states"`

	// BadStateDurationMin is the minimum continuous time in a bad state before
	// the rule fires. Zero means "fire on the first observed bad state" (still
	// gated by the reload cooldown). The rule is enabled iff BadStates is
	// non-empty.
	BadStateDurationMin int `yaml:"bad_state_duration_min"`

	// StaleLastUpdatedMin is the maximum allowed age of the most recent entity
	// last_updated timestamp. Zero disables the timestamp rule.
	StaleLastUpdatedMin int `yaml:"stale_last_updated_min"`
}

// ReloadPolicy bounds reload attempts to avoid hammering HA.
type ReloadPolicy struct {
	// CooldownMin is the minimum time between reload attempts.
	CooldownMin int `yaml:"cooldown_min"`

	// DailyMax is the maximum reload attempts in any rolling 24-hour window.
	DailyMax int `yaml:"daily_max"`

	// PostReloadDelaySec is how long to wait after a reload before re-checking
	// (gives the integration time to reinitialize).
	PostReloadDelaySec int `yaml:"post_reload_delay_sec"`
}

// LoadConfig reads and validates a watchdog config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse watchdog config: %w", err)
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate ensures the loaded config is internally consistent. It returns the
// first problem found.
func (c *Config) Validate() error {
	seen := make(map[string]struct{}, len(c.WatchTargets))
	for i, t := range c.WatchTargets {
		if t.Name == "" {
			return fmt.Errorf("watch_targets[%d]: name is required", i)
		}
		if _, dup := seen[t.Name]; dup {
			return fmt.Errorf("watch_targets[%d]: duplicate name %q", i, t.Name)
		}
		seen[t.Name] = struct{}{}

		if t.ConfigEntryID == "" {
			return fmt.Errorf("watch_targets[%s]: config_entry_id is required", t.Name)
		}
		if len(t.Entities) == 0 {
			return fmt.Errorf("watch_targets[%s]: at least one entity is required", t.Name)
		}
		if len(t.Detection.BadStates) == 0 && t.Detection.StaleLastUpdatedMin == 0 {
			return fmt.Errorf("watch_targets[%s]: at least one detection rule must be enabled", t.Name)
		}
		if t.Detection.BadStateDurationMin > 0 && len(t.Detection.BadStates) == 0 {
			return fmt.Errorf("watch_targets[%s]: bad_state_duration_min set without bad_states", t.Name)
		}
		if t.Reload.CooldownMin <= 0 {
			return fmt.Errorf("watch_targets[%s]: reload.cooldown_min must be positive", t.Name)
		}
		if t.Reload.DailyMax <= 0 {
			return fmt.Errorf("watch_targets[%s]: reload.daily_max must be positive", t.Name)
		}
	}
	return nil
}

// CheckInterval returns the scan interval, applying the default.
func (c *Config) CheckInterval() time.Duration {
	if c.CheckIntervalSec <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.CheckIntervalSec) * time.Second
}

// BadStateDuration returns the configured bad-state window as a Duration.
func (d DetectionRule) BadStateDuration() time.Duration {
	return time.Duration(d.BadStateDurationMin) * time.Minute
}

// StaleLastUpdatedDuration returns the configured timestamp-staleness window as a Duration.
func (d DetectionRule) StaleLastUpdatedDuration() time.Duration {
	return time.Duration(d.StaleLastUpdatedMin) * time.Minute
}

// Cooldown returns the configured cooldown as a Duration.
func (r ReloadPolicy) Cooldown() time.Duration {
	return time.Duration(r.CooldownMin) * time.Minute
}

// PostReloadDelay returns the configured post-reload delay as a Duration.
func (r ReloadPolicy) PostReloadDelay() time.Duration {
	return time.Duration(r.PostReloadDelaySec) * time.Second
}

// IsBadState returns true if state is listed in BadStates.
func (d DetectionRule) IsBadState(state string) bool {
	for _, s := range d.BadStates {
		if s == state {
			return true
		}
	}
	return false
}
