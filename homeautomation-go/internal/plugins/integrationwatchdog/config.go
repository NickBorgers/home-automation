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

	// Contradictions lists pairs of entity states that cannot both be true. A frozen
	// integration can report a plausible state ("off") that another source disproves
	// (the Apple TV is "playing"), so neither the bad-state rule nor the timestamp
	// rule fires.
	Contradictions []Contradiction `yaml:"contradictions"`
}

// Contradiction says: Entity in State is not credible while ContradictedBy holds.
// The rule fires after both conditions have held together for DurationMin.
type Contradiction struct {
	// Entity is the watched entity. It must be listed in the target's entities.
	Entity string `yaml:"entity"`

	// State is the state of Entity that the other source disproves.
	State string `yaml:"state"`

	// ContradictedBy is the other entity and the state that disproves Entity.
	ContradictedBy EntityCondition `yaml:"contradicted_by"`

	// DurationMin is how long both conditions must hold before the rule fires.
	// Zero fires on the first observation (still gated by the reload cooldown).
	DurationMin int `yaml:"duration_min"`
}

// EntityCondition names an entity and the state that counts.
type EntityCondition struct {
	Entity string `yaml:"entity"`
	State  string `yaml:"state"`
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
		if len(t.Detection.BadStates) == 0 && t.Detection.StaleLastUpdatedMin == 0 &&
			len(t.Detection.Contradictions) == 0 {
			return fmt.Errorf("watch_targets[%s]: at least one detection rule must be enabled", t.Name)
		}
		if t.Detection.BadStateDurationMin > 0 && len(t.Detection.BadStates) == 0 {
			return fmt.Errorf("watch_targets[%s]: bad_state_duration_min set without bad_states", t.Name)
		}
		for j, c := range t.Detection.Contradictions {
			if err := c.validate(t.Entities); err != nil {
				return fmt.Errorf("watch_targets[%s].contradictions[%d]: %w", t.Name, j, err)
			}
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

// validate checks one contradiction against the target's own entities.
func (c Contradiction) validate(targetEntities []string) error {
	if c.State == "" || c.ContradictedBy.Entity == "" || c.ContradictedBy.State == "" {
		return fmt.Errorf("state, contradicted_by.entity and contradicted_by.state are required")
	}
	if c.DurationMin < 0 {
		return fmt.Errorf("duration_min must not be negative")
	}
	for _, eid := range targetEntities {
		if eid == c.Entity {
			return nil
		}
	}
	return fmt.Errorf("entity %q must be listed in the target's entities", c.Entity)
}

// CheckInterval returns the scan interval, applying the default.
func (c *Config) CheckInterval() time.Duration {
	if c.CheckIntervalSec <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.CheckIntervalSec) * time.Second
}

// Duration returns the configured contradiction window as a Duration.
func (c Contradiction) Duration() time.Duration {
	return time.Duration(c.DurationMin) * time.Minute
}

// WatchedEntities returns every entity the target needs to observe: its own entities
// plus the entities named by contradiction rules.
func (t WatchTarget) WatchedEntities() []string {
	seen := make(map[string]struct{}, len(t.Entities))
	out := make([]string, 0, len(t.Entities))
	add := func(eid string) {
		if _, dup := seen[eid]; dup {
			return
		}
		seen[eid] = struct{}{}
		out = append(out, eid)
	}
	for _, eid := range t.Entities {
		add(eid)
	}
	for _, c := range t.Detection.Contradictions {
		add(c.Entity)
		add(c.ContradictedBy.Entity)
	}
	return out
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
